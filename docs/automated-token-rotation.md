# Automated Token Rotation — Design & Implementation Plan

> **Status:** Planning  
> **Phase:** 3 of Future Work Recommendations (ROADMAP § High Priority #3)  
> **Last Updated:** 2026-08-27

---

## Table of Contents

- [Overview](#overview)
- [Why "Automated Rotation" is Hard](#why-automated-rotation-is-hard)
- [Existing Token Lifecycle & Rotation](#existing-token-lifecycle--rotation)
- [Prohibited Patterns](#prohibited-patterns)
- [Design Principles](#design-principles)
- [Scope](#scope)
- [Architecture](#architecture)
- [Configuration](#configuration)
- [Metrics & Observability](#metrics--observability)
- [Anti-Ban Compliance](#anti-ban-compliance)
- [Testing](#testing)
- [Implementation Phases](#implementation-phases)
- [Related Documentation](#related-documentation)

---

## Overview

"Automated Token Rotation" in the freebuff-proxy context does **NOT** mean round-robin
swapping of healthy tokens. It means **intelligent, reactive, health-aware token lifecycle
management** — specifically:

1. **Detect** tokens approaching quota exhaustion before they fail mid-request.
2. **Fail over** to healthy tokens reactively, not proactively.
3. **Suggest** operator actions (add backup tokens, re-authenticate expired tokens) via
   dashboard alerts and error messages.
4. **Validate** token health proactively using `-test-token` and health probes.
5. **Manage** token lifecycle transitions (active → degraded → exhausted → recovered) with
   clear state machine semantics.

This document defines the design, constraints, and implementation plan.

---

## Why "Automated Rotation" is Hard

### Upstream Constraints

| Constraint | Impact | Source |
|---|---|---|
| **Session slots are scarce** | Base: 3/day per account, up to 7/day with trust ladder. Each admission burns a daily slot. | `session.go`, README § Key Concepts |
| **Per-IP distinct-active-sessions cap** | Multiple sessions from one IP → `ip_capped`. 3rd hit in rolling window → lock until Pacific midnight. | `docs/ban-avoidance.md` § IP-Capping |
| **Aggressive key rotation is a farming signal** | "Don't rotate several healthy keys aggressively" — upstream detects rapid healthy-key cycling. | `docs/getting-started.md` § Hygiene Rules |
| **Trust-level sticky caps** | Accounts from same /24 CIDR (≥8) or mailbox (≥3) permanently capped. | `docs/ban-avoidance.md` § Per-Account Trust |
| **Daily spend ceilings** | Restricted cohorts: $0.50/day, server-enforced. | `docs/ban-avoidance.md` § Daily Spend |
| **Mass sweep detection** | Disposable email domains, bulk-created accounts, automation patterns. | `docs/ban-avoidance.md` § Mass Sweeps |

### Proxy-Side Complexity

- Each token has its own cooldown state, quota window, spend ledger, and session lifecycle.
- Sessions are expensive to create (daily slot burn) and cannot be reused across tokens.
- Run rotation (`ROTATION_INTERVAL`) and idle rotation (`IDLE_ROTATION_TIMEOUT`) already
  manage run lifecycle per-token.
- Bridge mode has its own LRU cache, circuit breaker, and per-entry lifecycle.

---

## Existing Token Lifecycle & Rotation

### What Already Exists

The proxy already implements significant token lifecycle management:

| Mechanism | Location | Description |
|---|---|---|
| **Hot-session-first selection** | `internal/pool/acquire.go` | Tokens with live sessions are tried first; cold tokens only when hot ones fail. |
| **Round-robin start** | `internal/pool/acquire.go:52` | `p.rr.Add(1)` — atomic counter for cold-start order. |
| **Cooperative failover** | `internal/pool/acquire.go` | On rate-limit/ban/cap, next token is tried linearly. |
| **Run rotation** | `internal/runs/runs.go` | `ROTATION_INTERVAL` (default 6h): old run drained/finished, fresh run started. |
| **Idle rotation** | `internal/config/config.go` | `IDLE_ROTATION_TIMEOUT` (SAFE_MODE default 30m): finishes runs after idle. |
| **Quota-aware ordering** | `internal/pool/acquire.go:587-700` | Within hot set, tokens ranked by smallest remaining quota (drain closest to limit). |
| **Quota exhaustion exclusion** | `internal/pool/quota.go` | Tokens with exhausted model quota excluded from pass; rate-limit errors surfaced. |
| **Cooldown management** | `internal/pool/cooldown.go` | Auth-reject (30m), rate-limit (Retry-After), ip-capped, ban, country-block. |
| **Model-unfit registry** | `internal/pool/unfit.go` | (egress, model) pairs marked unfit for 5min after `limited_ip` refusal. |
| **Scarce model protection** | `internal/pool/scarce.go` | Tokens with active scarce sessions (pro/luna) protected from eviction. |
| **Session idle end** | `internal/session/session.go` | `SESSION_IDLE_END`: ends sessions after idle, releasing daily admission slot. |
| **Quota fallback** | `internal/pool/scarce.go` | `QUOTA_FALLBACK_MODELS`: auto-fallback when session quota exhausted. |
| **Circuit breaker** | `internal/pool/bridge_breaker.go` | Bridge mode: trips on transient failures, prevents cascading bans. |

### What's Missing (This Phase)

| Gap | Impact | Proposed Solution |
|---|---|---|
| **No proactive exhaustion detection** | Tokens fail mid-request with 429 instead of preemptive failover. | Token health scoring + predictive exhaustion warnings. |
| **No backup token suggestion** | Operators don't know when to add tokens. | Dashboard alerts + `/healthz` exhaustion indicators. |
| **No token health validation** | Expired/invalid tokens discovered only on first request. | Background health probes (reuse `-test-token` probe). |
| **No token lifecycle state machine** | Token states are implicit (cooldown map, locked flag, quota snapshot). | Explicit `TokenState` enum with transitions + Prometheus metrics. |
| **No exhaustion-aware pre-failover** | Pool exhausts all tokens before surfacing 429. | Early exhaustion detection with best-effort failover messaging. |

---

## Prohibited Patterns

The following rotation patterns are **explicitly prohibited** by upstream detection and
project anti-ban invariants:

### ❌ Aggressive Round-Robin

```
Request 1 → Token A
Request 2 → Token B
Request 3 → Token C
Request 4 → Token A
```

**Why prohibited:** "Don't rotate several healthy keys aggressively" — farming signal.
Upstream detects rapid healthy-key cycling from a single IP as automation abuse.

**Current mitigation:** Hot-session-first selection concentrates traffic on tokens
with live sessions. Round-robin only applies to cold-start order.

### ❌ Proactive Token Swapping

```
Every N minutes: swap Token A → Token B even though A is healthy
```

**Why prohibited:** Session slots are scarce (3-7/day). Proactive swapping burns
admission slots without cause, looks like session-spam automation.

**Current mitigation:** `ROTATION_INTERVAL` (6h) and `IDLE_ROTATION_TIMEOUT` (30m)
manage run lifecycle, not token swapping.

### ❌ Parallel Token Hammering

```
Same request sent to Token A, B, C simultaneously (fan-out for speed)
```

**Why prohibited:** Per-IP session cap (`ip_capped`). 3rd hit → lock until midnight.
Multiple concurrent sessions from one IP = farming pattern.

**Current mitigation:** Leader-election gate (`modelAdmissionGate`) prevents
duplicate session creates per model.

### ❌ Re-Admission Spam

```
Token A fails → immediately re-admit Token A → fail → re-admit → ...
```

**Why prohibited:** Each re-admission burns a daily session slot. Re-admit storms
(T10 detector) are a documented ban vector.

**Current mitigation:** Cooldown windows, re-admit storm detector
(`stormThreshold=3`, `stormWindow=60s`).

---

## Design Principles

1. **Reactive, not proactive.** Rotate only when evidence demands it — never swap
   healthy tokens preemptively.

2. **Drain before switching.** Exhaust a token's current quota/session before
   moving to the next. One active session at a time per token.

3. **Transparent state.** Every token's lifecycle state is visible in `/healthz`,
   `/metrics`, and the dashboard — no hidden rotation logic.

4. **Operator-first.** Automated rotation suggests; operators decide. Auto-rotation
   is opt-in, conservative, and auditable.

5. **Anti-ban compliance.** Every rotation decision passes through the anti-ban
   contract (A1–A5 invariants, TLS fingerprinting, header sanitization, jitter).

6. **Idempotent & safe.** Rotation never loses in-flight requests. Runs are
   drained (honest FINISH) before any token transition.

---

## Scope

### In Scope

| Feature | Description | Priority |
|---|---|---|
| **Token Health Score** | Composite score from quota, cooldown, spend, error history. | High |
| **Exhaustion Prediction** | Predict when a token will exhaust based on usage rate + remaining quota. | High |
| **Pre-failover Messaging** | Surface "token X will exhaust in Y minutes" before it fails. | High |
| **Dashboard Exhaustion Alerts** | Visual indicators for tokens approaching quota limits. | High |
| **Background Health Probes** | Periodic `-test-token`-style probes for idle tokens. | Medium |
| **Token Lifecycle State Machine** | Explicit states: Active → Degraded → Exhausted → Recovering → Active. | Medium |
| **Exhaustion-Aware Pre-Selection** | Skip tokens predicted to exhaust within N seconds of request. | Medium |
| **Backup Token Suggestions** | Dashboard + logs: "Add backup tokens for model X". | Medium |
| **Auto-Rotation (Opt-in)** | Config-driven: auto-rotate to next token when primary exhausts. | Low |
| **Operator Notification Webhook** | Alert on token exhaustion / ban / recovery events. | Low |

### Out of Scope

- Changing the existing hot-session-first / failover semantics.
- Proactive token swapping for load balancing.
- Multi-IP egress rotation (addressed in Phase 3.3–3.6).
- Token marketplace or auto-provisioning.
- Changing upstream session slot limits.

---

## Architecture

### Token Health State Machine

```
                    ┌─────────────────────────────────┐
                    │           RECOVERING             │
                    │  (cooldown expired, probing)     │
                    └──────────┬───────────────────────┘
                               │ probe success
                               ▼
┌──────────┐  quota OK  ┌──────────────┐  quota low  ┌─────────────┐
│          │ ◄───────── │              │ ──────────► │             │
│  ACTIVE  │            │   ACTIVE     │             │  DEGRADED   │
│          │ ─────────► │              │ ◄───────── │             │
└─────┬────┘  error/    └──────────────┘  recovery   └──────┬──────┘
      │               ban/429                quota            │
      │                                                       │
      ▼                                                       ▼
┌──────────┐  reset    ┌─────────────┐  reset    ┌──────────────┐
│EXHAUSTED │ ◄──────── │  COOLDOWN   │ ─────────►│  EXHAUSTED   │
│          │           │             │           │              │
└──────────┘           └─────────────┘           └──────────────┘
       │                                              │
       │  quota reset / admission                     │  permanent
       ▼                                              ▼
┌──────────┐                                   ┌──────────────┐
│RECOVERING│                                   │    DEAD      │
│          │                                   │  (banned)    │
└──────────┘                                   └──────────────┘
```

### Token Health Score

The health score is a composite 0–100 value computed from:

| Component | Weight | Source | Description |
|---|---|---|---|
| Quota Remaining | 40% | `quotaRemaining()` | Percentage of session quota remaining for primary model. |
| Cooldown Status | 25% | `CooldownUntil()` | 0 when cooling down, 100 when clear. |
| Spend Headroom | 15% | `spendView.Rolling24h` vs `MaxSpendPerDay` | Percentage of daily spend budget remaining. |
| Error Rate | 10% | Recent error count (rolling window) | Lower error rate → higher score. |
| Session Freshness | 10% | Session age vs `ROTATION_INTERVAL` | Recently admitted → higher score. |

Score thresholds:
- **≥ 80**: Active (green)
- **50–79**: Degraded (yellow) — operator alert recommended
- **20–49**: Critical (orange) — backup token recommended
- **< 20**: Exhausted (red) — failover imminent or active

### Exhaustion Prediction

Predict token exhaustion using linear extrapolation:

```
remaining_quota = quota_limit - recent_count
tokens_per_minute = recent_count / minutes_since_window_start
minutes_to_exhaust = remaining_quota / tokens_per_minute

if minutes_to_exhaust < EXHAUSTION_WARNING_THRESHOLD:
    surface warning
```

This is advisory — the proxy cannot know the exact upstream reset time until
the next admission response provides it.

---

## Configuration

### New Configuration Keys

| Key | Type | Default | Description |
|---|---|---|---|
| `TOKEN_HEALTH_PROBES` | `bool` | `false` | Enable background health probes for idle tokens. |
| `TOKEN_PROBE_INTERVAL` | `duration` | `30m` | Interval between health probes per token. |
| `EXHAUSTION_WARNING_THRESHOLD` | `duration` | `10m` | Warn when token predicted to exhaust within this window. |
| `AUTO_ROTATE_ON_EXHAUSTION` | `bool` | `false` | Opt-in: automatically failover to next healthy token when primary exhausts mid-request (reactive, not proactive). |
| `HEALTH_SCORE_ENABLED` | `bool` | `true` | Enable token health score computation and exposure. |

### Environment Variables

```bash
# Enable background health probes (opt-in, conservative)
TOKEN_HEALTH_PROBES=true
TOKEN_PROBE_INTERVAL=30m

# Exhaustion warning threshold
EXHAUSTION_WARNING_THRESHOLD=10m

# Auto-rotation (opt-in, reactive only — never proactive)
AUTO_ROTATE_ON_EXHAUSTION=false

# Health score (enabled by default, zero-cost computation)
HEALTH_SCORE_ENABLED=true
```

---

## Metrics & Observability

### New Prometheus Metrics

| Metric | Type | Labels | Description |
|---|---|---|---|
| `freebuff_proxy_token_health_score` | gauge | `token` | Current health score (0–100) per token. |
| `freebuff_proxy_token_exhaustion_warnings_total` | counter | `token`, `model` | Cumulative exhaustion warnings surfaced. |
| `freebuff_proxy_token_state` | gauge | `token`, `state` | Current lifecycle state (0=inactive, 1=active, 2=degraded, 3=exhausted, 4=dead). |
| `freebuff_proxy_token_probe_total` | counter | `token`, `result` | Health probe results (success/failure). |
| `freebuff_proxy_token_failover_total` | counter | `reason` | Failover events by reason (exhaustion, cooldown, error, ban). |

### `/healthz` Extensions

```json
{
  "tokens": [
    {
      "index": 1,
      "health_score": 82,
      "state": "active",
      "exhaustion_eta": "2026-08-27T15:30:00Z",
      "quota_remaining": { "deepseek/deepseek-v4-flash": 3 },
      "last_probe": "2026-08-27T14:00:00Z",
      "probe_result": "success"
    }
  ]
}
```

### Dashboard Extensions

- **Token Cards**: Health score badge (color-coded), exhaustion ETA, lifecycle state.
- **Overview**: Aggregate health distribution (X active, Y degraded, Z exhausted).
- **Alerts Panel**: "Token 2 will exhaust flash quota in ~8 minutes. Add backup tokens."

---

## Anti-Ban Compliance

Every automated rotation decision must pass these checks before execution:

### A1–A5 Invariant Compliance

| Invariant | Rotation Impact | Enforcement |
|---|---|---|
| **A1**: Session POST sends `x-freebuff-model` + `x-freebuff-instance-id` | New session admission during rotation must include these headers. | `internal/upstream/client.go` — enforced at transport layer. |
| **A2**: Chat POST carries NO model header | Rotated token's chat requests must never include model header. | `internal/convert/convert.go` — header stripped before relay. |
| **A3**: Pinned `ai-sdk` User-Agent | Rotated token must use same User-Agent. | `internal/stealth/stealth.go` — pinned per-client. |
| **A4**: Honest `FINISH` on run termination | Rotated token must FINISH its old run before starting a new one. | `internal/runs/runs.go` — drain queue + `finishIfReady`. |
| **A5**: TLS fingerprint consistency | Rotated token must maintain same TLS profile. | `internal/stealth/stealth.go` — profile per-pool, not per-token. |

### Rotation-Specific Anti-Ban Rules

1. **Never rotate a healthy token.** Only rotate when:
   - Token is exhausted (quota = 0)
   - Token is in cooldown (rate-limit, ban, ip-capped)
   - Token's session has expired and re-admission failed
   - Operator explicitly triggered rotation via dashboard

2. **One session at a time per token.** Never create a second session for a token
   while one is active (daily slot conservation).

3. **Respect cooldown windows.** Never bypass a cooldown to force rotation — wait
   for the window to expire.

4. **Drain before rotate.** Every run must be FINISHed (honest termination) before
   a token transitions to a new state.

5. **No fan-out rotation.** Never send the same request to multiple tokens
   simultaneously for redundancy.

---

## Testing

### Test Strategy

All rotation behaviors must be verified by hermetic tests:

```bash
env -u AUTH_TOKENS -u ADMIN_TOKEN go test ./...
```

### Required Test Files

| File | Coverage |
|---|---|
| `internal/pool/token_health_test.go` | Health score computation, state transitions, edge cases. |
| `internal/pool/exhaustion_prediction_test.go` | Exhaustion ETA accuracy, division-by-zero, edge cases. |
| `internal/pool/rotation_test.go` | Auto-rotation trigger conditions, failover correctness, anti-ban compliance. |
| `internal/pool/token_lifecycle_test.go` | State machine transitions, concurrent state changes. |
| `internal/pool/token_probe_test.go` | Background probe scheduling, probe result integration. |

### Key Test Scenarios

1. **Quota exhaustion failover**: Token A exhausts → failover to Token B → Token B serves.
2. **Cooldown rotation**: Token A rate-limited → cooldown → Token B serves → Token A recovers.
3. **No healthy token**: All tokens exhausted/cooldown → proper 429 + best Retry-After.
4. **Ban detection**: Token banned → excluded permanently → dashboard alert → operator removes.
5. **Health score accuracy**: Score reflects actual token health across all dimensions.
6. **Exhaustion prediction**: ETA is within ±2 minutes of actual exhaustion.
7. **Concurrent rotation**: Multiple requests during rotation → no lost requests.
8. **Drain before rotate**: Old run FINISHed before new run STARTed.

---

## Implementation Phases

### Phase 5.1: Token Health Score (High Priority) ✅ COMPLETE

**Files to create/modify:**
- `internal/pool/token_health.go` — Health score computation.
- `internal/pool/token_health_test.go` — Unit tests.
- `internal/pool/acquire.go` — Expose health score in `TokenSnapshot`.
- `internal/server/health.go` — Surface health score in `/healthz`.
- `internal/telemetry/metrics.go` — Add `token_health_score` gauge.

**Estimated effort:** 2–3 days.

### Phase 5.2: Exhaustion Prediction & Pre-Failover (High Priority) ✅ COMPLETE

**Files to create/modify:**
- `internal/pool/exhaustion.go` — Exhaustion ETA computation.
- `internal/pool/exhaustion_test.go` — Unit tests.
- `internal/pool/acquire.go` — Skip tokens predicted to exhaust within threshold.
- `internal/server/errors.go` — Enrich error messages with exhaustion ETA.
- `internal/server/health.go` — Surface `exhaustion_eta` in `/healthz`.

**Estimated effort:** 2–3 days.

### Phase 5.3: Dashboard Exhaustion Alerts (High Priority)

**Files to modify:**
- `internal/dashboard/assets/` — Add exhaustion indicators to token cards.
- `internal/server/tokens.go` — Extend `TokenSnapshot` with health fields.

**Estimated effort:** 1–2 days.

### Phase 5.4: Background Health Probes (Medium Priority) ✅ COMPLETE

**Files to create/modify:**
- `internal/pool/token_probe.go` — Background probe scheduler.
- `internal/pool/token_probe_test.go` — Unit tests.
- `internal/config/config.go` — Add `TOKEN_HEALTH_PROBES`, `TOKEN_PROBE_INTERVAL`.
- `internal/server/health.go` — Surface probe results.

**Estimated effort:** 2–3 days.

### Phase 5.5: Token Lifecycle State Machine (Medium Priority)

**Files to create/modify:**
- `internal/pool/token_lifecycle.go` — State machine + transitions.
- `internal/pool/token_lifecycle_test.go` — Unit tests.
- `internal/pool/acquire.go` — Use state machine for selection.
- `internal/telemetry/metrics.go` — Add `token_state` gauge.

**Estimated effort:** 3–4 days.

### Phase 5.6: Auto-Rotation Opt-In (Low Priority) ✅ COMPLETE

**Files to create/modify:**
- `internal/pool/auto_rotate.go` — Reactive auto-rotation logic.
- `internal/pool/auto_rotate_test.go` — Unit tests.
- `internal/config/config.go` — Add `AUTO_ROTATE_ON_EXHAUSTION`.

**Estimated effort:** 2–3 days.

### Phase 5.7: Documentation & Integration (Final) ✅ COMPLETE

**Files to modify:**
- `docs/automated-token-rotation.md` — Update with implementation details.
- `README.md` — Add rotation configuration section.
- `AGENTS.md` — Update package map.
- `session.md` — Track progress.

**Estimated effort:** 1 day.

---

## Related Documentation

- [Ban-Avoidance & Signature Research](ban-avoidance.md) — Upstream detection landscape and countermeasures.
- [Bridge Mode](bridge-mode.md) — Bridge-mode architecture, invariants B1–B8.
- [Architecture](../ARCHITECTURE.md) — System components and request flows.
- [Specification](../SPECIFICATION.md) — API surface and behavioral rules.
- [Roadmap](../ROADMAP.md) — Feature checklist and planned work.
- [Getting Started](getting-started.md) — Operator hygiene rules (✅ Do / ❌ Don't).

---

*This document defines the design and implementation plan for Automated Token Rotation.
Implementation follows the phased approach above, with each phase independently testable
and deployable.*

