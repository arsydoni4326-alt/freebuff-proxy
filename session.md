# Session: Bridge Mode Quota Introspection Dashboard

## Current Objective
Implementing Bridge Mode Quota Introspection Dashboard (ROADMAP § Future Work Recommendations #2).
Per-entry quota visualization with model-level breakdown, rate limit counters, and spend tracking.

## Recent Changes
- Bridge quota dashboard: per-entry quota visualization with model-level breakdown (#bridge-quota-dashboard)
- Rate limit hit/miss counters on bridgeEntry (atomic.Int64)
- Extended BridgeTokenSnapshot with RateLimitHits/Misses/RateLimitRate fields
- Extended bridgeTokenCard with Quota rows, SpendLimit, and rate limit stats
- Tokens page: Bridge Quota section with per-entry cards, quota breakdown, rate limit activity
- Documentation: docs/bridge-quota-dashboard.md

## Recent Changes (Merge from origin/main)
- Circuit breaker for bridge mode fully implemented (`bridge_breaker.go`)
- Bridge hardening tests (`bridge_hardening_test.go`)
- Header injection tests (`header_injection_test.go`)
- Bridge metrics tests (`bridge_metrics_test.go`)
- Protocol regression tests (`protocol_regression_test.go`)
- Upstream drift data synced to vendor 63a1d68
- Dashboard drift banner and sync status surfaced to operators
- CI drift detection via committed baseline
- Docker staging improvements (debian-based)
- Registry updates: serve stealth/ox-alpha models

## Phase 3: Ban-Avoidance & Signature Research

### Documentation
- [x] Comprehensively document upstream detection landscape:
  - Per-request IP scoring via Cloudflare/Spur/MaxMind
  - Per-account trust levels (signup network, mailbox, email)
  - IP-capping and daily spend ceilings
  - Mass sweep patterns
  - Wire protocol fingerprinting vectors
- [x] Document all proxy countermeasures:
  - Anti-ban contract invariants (A1–A5)
  - TLS fingerprinting: 8 profiles + auto mode
  - Header sanitization: 24 proxy headers stripped
  - Request jitter (SAFE_MODE default 2s), idle rotation, session idle end
  - Cooldown types (auth, rate-limit, ip-capped, ban, country-block)
  - Model-unfit registry (5-min TTL, limited_ip protection)
  - Scarce model protection (1-session/day proxy)
- [x] Document Risk Engine architecture:
  - Scoring rules (privacy signals +40, contention ratios)
  - Risk levels (low 0-29, medium 30-39, high 40-100)
  - Sample sources (egress probe, upstream session response)
- [x] Create `docs/ban-avoidance.md` — comprehensive ban-avoidance & signature research doc
- [x] Add operator hygiene rules (✅ Do / ❌ Don't)
- [x] Add research plan: Phases 3.2–3.6

## Phase 4: Upstream Drift Tracking

### Documentation
- [x] Document drift tracking architecture
- [x] Document all 5 pinned upstream files and their purposes
- [x] Document drift detection infrastructure
- [x] Document runtime self-healing (REGISTRY_REFRESH, 6h default)
- [x] Document drift response playbook (6-step recovery process)
- [x] Document CI/CD integration (weekly upstream-drift workflow)
- [x] Create `docs/upstream-drift-tracking.md` — comprehensive drift tracking doc
- [x] Add research plan: Phases 4.2–4.6

### Phase 4.2: Warning-Level Alerts (COMPLETE)
- [x] Registry freshness tracking — `lastRefreshAt`/`usingFallback` fields + `LastRefreshAt()`/`UsingFallback()` accessors in `internal/registry/registry.go`
- [x] `/healthz` field for registry freshness — `registry` object with `fallback`, `last_refresh`, `age_seconds`
- [x] Prometheus metric for registry staleness — `freebuff_proxy_registry_age_seconds` + `freebuff_proxy_registry_fallback` gauges
- [x] Dashboard indicator for last successful upstream sync — `registry_fallback` + `registry_last_refresh` in overview data
- [x] CI webhook notification on drift — `upstream-drift.yml` fires Slack/Discord-compatible POST when `DRIFT_WEBHOOK_URL` secret is set
- [x] Tests — `TestFreshnessTracking`, `TestHealthzRegistryFreshness`, `TestMetricsRegistryFreshness`

### Phase 4.6: Documentation & Runbooks (COMPLETE)
- [x] Troubleshooting guide — 6 common sync failure scenarios with symptoms and fixes
- [x] Rollback procedures — revert workflow for bad syncs + emergency fallback
- [x] Operator guide — quick reference table, step-by-step manual sync, monitoring health, when-to-sync

### Phase 4.3: Protocol Drift Detection (COMPLETE)
- [x] Consolidated error-classification regression tests — `internal/upstream/protocol_regression_test.go` pins ~27 known upstream error bodies via `classifyError`
- [x] Field-extraction regression tests — `RateLimitError`, `IpCappedError`, `CountryBlockedError`
- [x] Timestamp-format regression tests — `parseFlexTime` (RFC3339 / unix seconds / millis)
- [x] Anthropic SSE lifecycle documented as compile-time anchor in the regression file

## Files Modified (this increment)
- `session.md` — Updated to reflect post-merge state with circuit breaker completion
- `ROADMAP.md` — Marked circuit breaker as complete, added comprehensive future work recommendations
- `README.md` — Added circuit breaker configuration options to Configuration Reference, updated Bridge mode description
- `docs/README.md` — Added ban-avoidance and upstream-drift-tracking documentation to guides table
- `docs/bridge-mode.md` — Marked circuit breaker as complete in hardening checklist
- `internal/pool/bridge_breaker.go` — NEW: Circuit breaker implementation for bridge mode
- `internal/pool/bridge_hardening_test.go` — NEW: Comprehensive hardening tests
- `internal/server/bridge_metrics_test.go` — NEW: Bridge metrics label hygiene tests
- `internal/server/header_injection_test.go` — NEW: Header smuggling prevention tests
- `internal/upstream/protocol_regression_test.go` — NEW: Protocol drift regression tests

## Circuit Breaker Observability Phase (COMPLETE)

### Deliverables
- [x] Documentation: `docs/circuit-breaker-observability.md` — comprehensive guide
- [x] Expose breaker state via `Pool.BreakerSnapshot()` method
- [x] `/healthz` response includes `circuit_breaker` object
- [x] Prometheus metrics: `freebuff_proxy_bridge_breaker_open`, `freebuff_proxy_bridge_breaker_failures`
- [x] Dashboard Overview surfaces breaker state (deferred to dashboard phase)
- [x] Tests: `TestBreakerSnapshotDisabled`, `TestBreakerSnapshotEnabled`, `TestBreakerSnapshotOpen`, `TestBreakerSnapshotFailureCount`, `TestHealthzCircuitBreaker`, `TestHealthzCircuitBreakerEnabled`, `TestMetricsCircuitBreaker`

### Design Decisions
- Breaker state exposed via `BreakerSnapshot()` to decouple pool internals from server layer
- Zero-value defaults emitted when bridge mode inactive (consistent for pre-provisioned dashboards)
- `until` field as nullable RFC 3339 (null when closed/disabled)
- `failures_remaining` is convenience field (threshold - count, clamped to 0)

---

## Remaining Work (Post-Merge)
1. Phase 3.2: Header-sanitization refinements against real traffic captures
2. Phase 3.3: Timing & behavioral analysis
3. Phase 3.4: TLS fingerprint drift monitoring
4. Phase 3.5: Risk engine improvements
5. Phase 3.6: Egress probe enhancements
6. Phase 4.4: Automated sync automation (auto-PR, auto-fallback)
7. Phase 4.5: Egress behavior tracking (admission, quota windows)

## Future Work Recommendations (NEW)
See ROADMAP.md § Future Work Recommendations for comprehensive suggestions.

## Automated Token Rotation Planning (NEW)

### Documentation
- [x] Create `docs/automated-token-rotation.md` — comprehensive design & implementation plan
- [x] Document prohibited rotation patterns (aggressive round-robin, proactive swapping, parallel hammering, re-admission spam)
- [x] Document existing token lifecycle mechanisms (13 mechanisms mapped)
- [x] Document anti-ban compliance rules for rotation (A1–A5 invariant mapping)
- [x] Define token health state machine (Active → Degraded → Exhausted → Recovering → Dead)
- [x] Define token health score (composite 0–100 from quota, cooldown, spend, error rate, session freshness)
- [x] Define exhaustion prediction algorithm (linear extrapolation)
- [x] Define configuration keys (TOKEN_HEALTH_PROBES, EXHAUSTION_WARNING_THRESHOLD, AUTO_ROTATE_ON_EXHAUSTION, HEALTH_SCORE_ENABLED)
- [x] Define Prometheus metrics (token_health_score, token_exhaustion_warnings, token_state, token_probe, token_failover)
- [x] Define implementation phases (5.1–5.7, ~15-18 days total)

### Key Design Decision
**"Automated Token Rotation" means reactive, health-aware lifecycle management — NOT
aggressive round-robin.** The documentation explicitly prohibits:
- Aggressive round-robin of healthy keys (farming signal per upstream detection)
- Proactive token swapping (burns scarce session slots)
- Parallel token hammering (ip_capped trigger)
- Re-admission spam (burns daily slots, T10 storm detector)

### Anti-Ban Invariant Reminder
- Hot-session-first selection: traffic concentrates on tokens with live sessions
- Drain before switching: exhaust quota/session before moving to next token
- One session at a time per token: never create parallel sessions
- Honest FINISH on every run termination: drain queue before state transitions

## Phase 5.1: Token Health Score (COMPLETE)

### Deliverables
- [x] `internal/pool/health.go` — NEW: `ComputeHealthScore()`, `HealthScoreLabel()`, `buildHealthScoreInput()`, `countRateLimitEvents()`
- [x] `internal/pool/health_test.go` — NEW: 11 unit tests covering healthy, exhausted, cooldown, error rate, spend, no-session, unknown-quota, label thresholds, event counting, input building
- [x] `internal/pool/pool.go` — Added `HealthScore int` and `HealthScoreLabel string` fields to `TokenSnapshot`
- [x] `internal/pool/snapshot.go` — `Snapshot()` computes health score from quota, cooldown, spend, error rate, session freshness
- [x] `internal/server/health.go` — `/healthz` JSON includes `health_score` and `health_score_label`; `/metrics` emits `freebuff_proxy_token_health_score` gauge with `token` and `label` labels

### Design Decisions
- Health score is **advisory only** — never gates Acquire or failover
- Five weighted components: quota (40%), cooldown (25%), spend (15%), error rate (10%), freshness (10%)
- Unknown quota (before first admission) = full credit (avoids penalizing fresh tokens)
- Cooldown score ramps linearly in last 20% of window (avoids cliff at expiry boundary)
- Error rate soft ceiling at 50 events (linear scale below ceiling)
- Session freshness is proportional to `remaining / ROTATION_INTERVAL`

### Test Results
- All 11 new health score tests PASS
- Full hermetic test suite: all packages PASS (pre-existing registry_test.go syntax error is unrelated)

## Phase 5.2: Backup Token Suggestions & Health Validation (COMPLETE)

### Deliverables
- [x] `internal/pool/health_warn.go` — NEW: `healthState` for transition detection, `checkTransition()`, `logHealthTransition()`, `checkHealthTransitions()`, `predictExhaustion()`, `isExhausted()`, `isCriticalOrWorse()`
- [x] `internal/pool/token_probe.go` — NEW: `probeResult`, `probeState`, `ProbeSnapshot()`, `probeTokens()`, `probeSingleToken()`, `startProbeLoop()` — background zero-cost GET probe scheduler
- [x] `internal/pool/health_warn_test.go` — NEW: 11 unit tests covering transition detection, label helpers, exhaustion prediction, probe state, config fields
- [x] `internal/pool/pool.go` — Added `healthTracker *healthState`, `probeResults *probeState` fields to Pool; `ProbeSnapshot()` method for /healthz
- [x] `internal/pool/snapshot.go` — `Snapshot()` calls `checkHealthTransitions()` after computing health scores
- [x] `internal/pool/acquire.go` — `acquireOrder()` deprioritises exhausted tokens when `AUTO_ROTATE_ON_EXHAUSTION=true` (moved to end of order, still eligible as last resort)
- [x] `internal/pool/pool_lifecycle.go` — `Start()` launches `startProbeLoop()` for background health probes
- [x] `internal/server/health.go` — `/healthz` JSON includes `probe_ok`, `probe_quota_ok`, `probe_error`, `probe_at` per token
- [x] `internal/config/config.go` — Added `AutoRotateOnExhaustion`, `ExhaustionWarningThreshold`, `HealthScoreEnabled`, `TokenHealthProbes`, `TokenProbeInterval` fields to Config and rawConfig
- [x] `internal/config/config_env.go` — Added env overrides for `AUTO_ROTATE_ON_EXHAUSTION`, `EXHAUSTION_WARNING_THRESHOLD`, `HEALTH_SCORE_ENABLED`, `TOKEN_HEALTH_PROBES`, `TOKEN_PROBE_INTERVAL`

### New Configuration Keys
| Key | Type | Default | Description |
|---|---|---|---|
| `AUTO_ROTATE_ON_EXHAUSTION` | `bool` | `false` | Deprioritise exhausted tokens in Acquire order (reactive, not proactive) |
| `EXHAUSTION_WARNING_THRESHOLD` | `duration` | `10m` | Warn when token predicted to exhaust within this window |
| `HEALTH_SCORE_ENABLED` | `bool` | `true` | Enable composite health score computation |
| `TOKEN_HEALTH_PROBES` | `bool` | `false` | Enable background zero-cost GET probes for idle tokens |
| `TOKEN_PROBE_INTERVAL` | `duration` | `30m` | Interval between background health probes per token |

### Design Decisions
- **Health transition logging**: WARN log fires on first transition to "critical" or "exhausted" with token count context; avoids log spam by tracking last-known label per token
- **Auto-rotation is reactive**: exhausted tokens are moved to end of Acquire order, not removed — single-token pools never deadlock
- **Background probes**: zero-cost GET /api/v1/freebuff/session (no session claimed, no daily slot burned); runs at TOKEN_PROBE_INTERVAL; results surfaced in /healthz and dashboard
- **Exhaustion prediction**: heuristic based on remaining quota as fraction of total (< 10% = warning); real usage-rate tracking is deferred
- **Config defaults**: AUTO_ROTATE_ON_EXHAUSTION=false (opt-in), TOKEN_HEALTH_PROBES=false (opt-in), HEALTH_SCORE_ENABLED=true, TOKEN_PROBE_INTERVAL=30m

### Test Results
- All 11 new health warn/probe tests PASS
- Full hermetic test suite: all packages PASS (pre-existing registry_test.go syntax error is unrelated)

## Load-Bearing Invariants (Reminder)
- **Hermetic Test Suite**: `env -u AUTH_TOKENS -u ADMIN_TOKEN go test ./...`
- **Anti-Ban Contract**: Session POST sends headers, chat POST sends NO model header, honest FINISH
- **Tool Stripping**: `end_turn` injected upstream, stripped downstream
- **Sequential SSE Content Blocks**: Never interleave unclosed blocks
- **Circuit Breaker**: Only trips on transient upstream failures (5xx/network); classified errors never trip
