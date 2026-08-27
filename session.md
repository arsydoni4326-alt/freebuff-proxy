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

## Load-Bearing Invariants (Reminder)
- **Hermetic Test Suite**: `env -u AUTH_TOKENS -u ADMIN_TOKEN go test ./...`
- **Anti-Ban Contract**: Session POST sends headers, chat POST sends NO model header, honest FINISH
- **Tool Stripping**: `end_turn` injected upstream, stripped downstream
- **Sequential SSE Content Blocks**: Never interleave unclosed blocks
- **Circuit Breaker**: Only trips on transient upstream failures (5xx/network); classified errors never trip
