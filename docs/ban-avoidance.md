# Ban-Avoidance & Signature Research

> Phase 3 of the freebuff-proxy hardening roadmap.
> This document codifies all known upstream detection mechanisms, the proxy's countermeasures,
> and the research plan for ongoing ban-avoidance hardening.

---

## Table of Contents

- [Overview](#overview)
- [Upstream Detection Landscape](#upstream-detection-landscape)
- [Proxy Countermeasures](#proxy-countermeasures)
- [Risk Engine](#risk-engine)
- [Cooldown & Unfit Registries](#cooldown--unfit-registries)
- [Safe Mode Presets](#safe-mode-presets)
- [Operator Hygiene Rules](#operator-hygiene-rules)
- [Research Plan](#research-plan)
- [Testing](#testing)
- [References](#references)

---

## Overview

FreeBuff/Codebuff employs multi-layered abuse detection that can suspend or permanently ban
accounts. The proxy **reduces** ban risk; it does **not** eliminate it. This document captures
everything the project knows about upstream detection and the countermeasures implemented,
plus the research plan for ongoing improvements.

### Key Principle

> The proxy's goal is to make egress traffic **indistinguishable from the official CLI**
> running on a real user's machine. Any deviation from CLI-shaped traffic is a detection signal.

---

## Upstream Detection Landscape

### 1. Per-Request IP Scoring

Every upstream request passes through Cloudflare edge, which determines the **TCP source IP**
via GeoIP. This is the **authoritative** egress classification — HTTP headers like
`X-Forwarded-For` or `CF-Connecting-IP` are **overwritten** at L4 and cannot be spoofed.

The upstream uses MaxMind/Spur Intelligence ASN databases to classify IPs:

| Signal | Source | Effect |
|---|---|---|
| `ipPrivacySignals: ["vpn"]` | Spur Intelligence | Restricted cohort or hard-blocked |
| `ipPrivacySignals: ["proxy"]` | Spur Intelligence | Same as VPN |
| `ipPrivacySignals: ["tor"]` | Spur Intelligence | Hard-blocked |
| `ipPrivacySignals: ["hosting"]` | MaxMind ASN | Restricted cohort |
| `ipPrivacySignals: ["datacenter"]` | MaxMind ASN | Restricted cohort |
| `ipPrivacySignals: ["res_proxy"]` | Spur Intelligence | Restricted cohort |
| `ipPrivacySignals: ["spur-flagged"]` | Spur Intelligence | Restricted cohort |

**Source**: `freebuff-trust.ts` (upstream open-source client).

### 2. Per-Account Trust Levels

Accounts are assigned trust tiers based on signup characteristics:

- **Signup network**: Accounts from the same /24 CIDR (≥8 accounts) permanently capped at lower trust.
- **Shared mailbox**: Accounts sharing a mailbox domain (≥3 accounts) permanently capped.
- **Disposable email**: 6,699 of 7,129 accounts on flagged domains were already banned.

Trust tiers determine session limits (base: 3/day, up to 7/day with trust ladder),
model access (limited tier: only `mimo/mimo-v2.5`), and spend ceilings ($0.50/day restricted).

### 3. IP-Capping (Session Per-IP Limits)

The upstream enforces a per-IP distinct-active-sessions cap:

- Admission returns `ip_capped` with `activeUsersForIp` and `limit` fields.
- Each hit backs off with RetryAfter + ±20% jitter.
- The 3rd hit in a rolling window **locks** until Pacific-midnight reset.

### 4. Daily Spend Ceilings

Server-enforced daily spend limits per account. Restricted cohorts: $0.50/day.
Refusal status: `spend_limited`. Pacific-midnight reset cycle.

### 5. Mass Sweeps

Periodic sweeps against known farm patterns: disposable email domains,
bulk-created accounts from same signup network, automated tool signatures.

### 6. Wire Protocol Fingerprinting

The upstream can detect non-CLI clients by:

- Missing `x-freebuff-model` on session POST
- Present `model` header on chat POST (CLI sends none)
- Wrong User-Agent strings
- TLS fingerprint mismatch (Go stdlib vs. real browser)
- Missing or extra headers

---

## Proxy Countermeasures

### Anti-Ban Contract (Load-Bearing Invariants)

These invariants are enforced by code and verified by tests:

| Invariant | Enforcement | Test Coverage |
|---|---|---|
| Session POST sends `x-freebuff-model` and `x-freebuff-instance-id` | `internal/upstream/client.go` | `internal/upstream/*_test.go` |
| Chat POST carries **NO** `model` header | `internal/upstream/client_chat.go` | `internal/upstream/*_test.go` |
| Pinned `ai-sdk` User-Agent on all chat requests | `internal/upstream/client_chat.go` | `internal/upstream/*_test.go` |
| Honest `FINISH` on run termination | `internal/runs/drain.go` | `internal/runs/drain_orphan_test.go` |
| `end_turn` injected upstream, stripped downstream | `internal/convert/schemacache.go`, `internal/convert/sse.go` | `internal/convert/*_test.go` |

### TLS Fingerprinting (utls)

The proxy uses `refraction-networking/utls` to impersonate real browser TLS handshakes:

| Profile | ClientHelloID | Use Case |
|---|---|---|
| `chrome120` | `HelloChrome_120` | Default Chromium fingerprint |
| `chrome126` | `HelloChrome_120` | Chrome 126 with updated UA/headers |
| `safari17` | `HelloCustom` (custom spec) | Safari 17 on macOS |
| `safari18` | `HelloCustom` (custom spec) | Safari 18 on macOS |
| `firefox120` | `HelloFirefox_120` | Firefox 120 on Linux |
| `firefox128` | `HelloFirefox_128` | Firefox 128 on Linux |
| `edge126` | `HelloEdge_126` | Edge 126 on Windows |
| `random` | Random from above | Per-connection randomization |
| `auto` | Profile-ID-aware | Stashed per-request for retry rotation |

Key implementation details:
- Custom specs (Safari) are **deep-cloned per connection** to avoid utls state sharing.
- ALPN is pinned per transport type (`h2` for HTTP/2, `http/1.1` for HTTP/1.1).
- Profile rotation on transient retries: `rotateStealthProfileForRetry`.

### Request Jitter & Idle Rotation

When `SAFE_MODE=true`:

- Random delay `[0, REQUEST_JITTER)` before upstream calls (default: 2s).
- Profile rotation after `IDLE_ROTATION_TIMEOUT` idle periods.
- Session end after `SESSION_IDLE_END` idle periods (releases admission slot).

---

## Risk Engine

The `RiskEngine` (`internal/stealth/risk.go`) is a passive, thread-safe ban-risk predictor.

### Risk Scoring Rules

| Signal | Score Contribution | Level Threshold |
|---|---|---|
| Egress IP flagged by upstream privacy signals | +40 | ≥40 → HIGH |
| Additional distinct privacy signals | +10 each (cap +20) | — |
| Egress IP near session cap (≥70%) | +30 | ≥30 → MEDIUM |
| Egress IP moderately contended (≥50%) | +20 | — |
| Egress IP somewhat contended (≥30%) | +10 | — |

### Risk Levels

| Level | Score Range | Meaning |
|---|---|---|
| `low` | 0–29 | Normal operation |
| `medium` | 30–39 | Approaching ban risk |
| `high` | 40–100 | Active ban risk; immediate action recommended |

The engine is **read-only** — it warns but never modifies routing decisions.

---

## Cooldown & Unfit Registries

### Cooldown Types

| Type | Trigger | Duration | Source |
|---|---|---|---|
| Auth reject | 401 from run START | `runs.DefaultCooldown` (30 min) | `cooldown.go` |
| Rate limit | 429 quota exhaustion | RetryAfter from upstream | `cooldown.go` |
| IP capped | `ip_capped` admission | RetryAfter + jitter; 3rd hit locks until midnight | `cooldown.go` |
| Ban | 403 banned | Until `resumesAt` timestamp | `cooldown.go` |
| Country blocked | `country_blocked` | ~15 min window | `cooldown.go` |

### Model-Unfit Registry

When a model is refused upstream with `limited_ip`, the `(egress, model)` pair is marked
**unfit** for `modelUnfitTTL` (5 minutes). This prevents burning daily session slots on
re-admission attempts.

Key behaviors:
- `MarkModelUnfit` stores a copy of the refusal error (never aliases caller's object).
- `ClearModelUnfitBefore` only clears marks created before a given timestamp.
- `Acquire` deliberately does **NOT** consult the unfit registry.

### Scarce Model Protection

Models like `deepseek-v4-pro` and `openai/gpt-5.6-luna` are capped at 1 session/day.
The proxy protects these irreplaceable allocations via `scarceHeld` and `scarceActive`.

---

## Safe Mode Presets

When `SAFE_MODE=true` (default):

| Setting | Default | Effect |
|---|---|---|
| `TLS_FINGERPRINT` | `chrome120` | Browser TLS impersonation |
| `REQUEST_JITTER` | `2s` | Random delay before upstream calls |
| `IDLE_ROTATION_TIMEOUT` | Configurable | Profile rotation after idle periods |
| `SESSION_IDLE_END` | Configurable | End sessions after idle periods |

---

## Operator Hygiene Rules

### ✅ Do

| Rule | Rationale |
|---|---|
| Keep `SAFE_MODE=true` | Anti-ban presets active by default |
| Use a **normal residential connection** | Datacenter/VPN IPs trigger restricted cohort |
| Request **only models your tier offers** | Limited-tier models are coerced server-side |
| Keep **one modest account** | Multiple accounts per IP triggers ip_capped |
| Use **one key until rate-limited** | Rotating healthy keys looks like farming |
| Register with a **real email** | Disposable emails are flagged |

### ❌ Don't

| Rule | Rationale |
|---|---|
| Run 24/7 on heavy unattended tasks | Sustained automation is a detection signal |
| Use VPN / proxy / Tor | `ipPrivacySignals: ["vpn"]` → restricted cohort |
| Request out-of-region models | Server coerces to `mimo/mimo-v2.5` on limited tier |
| Create spam clusters (≥8 per /24) | Permanently capped at lower trust |
| Rotate several healthy keys aggressively | Farming signal |
| Use temp-mail domains | Documented ban cohort |
| Use datacenter VPS for egress | MaxMind ASN detection |

---

## Research Plan

### Phase 3.1: Detection Mechanism Documentation (Current)

- [x] Document per-request IP scoring and privacy signals
- [x] Document per-account trust levels and sticky caps
- [x] Document IP-capping and daily spend ceilings
- [x] Document mass sweep patterns
- [x] Document wire protocol fingerprinting vectors

### Phase 3.2: Header-Sanitization Refinements

- [ ] Audit real CLI traffic captures for header ordering/timing patterns
- [ ] Prototype randomized header order within browser-typical ranges
- [ ] Test `Accept-Encoding` order sensitivity
- [ ] Investigate `Sec-CH-UA` bitfield consistency across Chrome versions
- [ ] Document header order as a potential detection vector

### Phase 3.3: Timing & Behavioral Analysis

- [ ] Characterize upstream request-interval monitoring
- [ ] Test burst vs. steady-state request patterns
- [ ] Investigate session-duration anomalies
- [ ] Document connection-reuse patterns

### Phase 3.4: TLS Fingerprint Drift Monitoring

- [ ] Track utls library updates for new browser ClientHelloIDs
- [ ] Add CI check for utls version drift
- [ ] Document Chrome/Firefox/Safari TLS evolution timeline
- [ ] Prototype profile auto-update from upstream utls releases

### Phase 3.5: Risk Engine Improvements

- [ ] Add request-frequency risk signals
- [ ] Add session-duration risk signals
- [ ] Add model-request-pattern risk signals
- [ ] Expose risk breakdown in `/admin` dashboard
- [ ] Add configurable risk thresholds and alerts

### Phase 3.6: Egress Probe Enhancements

- [ ] Add periodic egress IP re-probing
- [ ] Track IP reputation changes over time
- [ ] Correlate IP changes with cooldown/ban events
- [ ] Add probe results to Prometheus metrics

---

## Testing

All ban-avoidance behaviors are verified by hermetic tests:

```bash
env -u AUTH_TOKENS -u ADMIN_TOKEN go test ./...
```

Key test files:

- `internal/stealth/stealth_test.go` — TLS profile application, header sanitization
- `internal/stealth/risk_test.go` — Risk engine scoring
- `internal/pool/pool_cooldown_test.go` — Cooldown behavior
- `internal/pool/unfit_test.go` — Model-unfit registry
- `internal/pool/scarce_test.go` — Scarce model protection
- `internal/server/header_injection_test.go` — Header smuggling prevention
- `internal/pool/bridge_hardening_test.go` — Bridge security hardening

---

## References

- [Getting Started — Safety Rules](getting-started.md#important-safety-warning)
- [Bridge Mode — Security Considerations](bridge-mode.md#security-considerations)
- [Architecture — Anti-Ban Discipline](../ARCHITECTURE.md)
- [README — Key Hygiene & Ban Avoidance](../README.md#key-hygiene--ban-avoidance)
- Upstream source: `CodebuffAI/freebuff` — `freebuff-trust.ts`, `freebuff-scheduler.ts`
- `internal/stealth/` — TLS fingerprinting and header sanitization
- `internal/pool/cooldown.go` — Cooldown management
- `internal/pool/unfit.go` — Model-unfit registry
- `internal/pool/scarce.go` — Scarce model protection
- `internal/stealth/risk.go` — Ban-risk prediction engine
