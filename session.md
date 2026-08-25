# Session: Phase 3 & 4 — Ban-Avoidance & Upstream Drift Tracking

## Current Objective
Phase 3: Ban-avoidance & signature research — Documentation complete.
Phase 4: Upstream drift tracking — Documentation complete.

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
- `ROADMAP.md` — updated Phase 3, Phase 4, Phase 4.2, Phase 4.3, and Phase 4.6 progress checkmarks
- `session.md` — Phase 3/4/4.2/4.3/4.6 progress tracking
- `docs/ban-avoidance.md` — NEW: comprehensive ban-avoidance & signature research doc
- `docs/upstream-drift-tracking.md` — NEW: drift tracking doc + Phase 4.2 freshness + Phase 4.3 protocol regression docs + Phase 4.6 runbooks
- `internal/registry/registry.go` — added `lastRefreshAt`/`usingFallback` fields + accessors
- `internal/registry/registry_test.go` — added `TestFreshnessTracking`
- `internal/server/health.go` — added `registry` to `/healthz` + Prometheus gauges
- `internal/server/server_models_test.go` — added `TestHealthzRegistryFreshness`, `TestMetricsRegistryFreshness`
- `internal/dashboard/dashboard_data.go` — added `RegistryFallback`/`RegistryLastRefresh` to overview
- `.github/workflows/upstream-drift.yml` — added `DRIFT_WEBHOOK_URL` webhook notification
- `internal/upstream/protocol_regression_test.go` — NEW: Phase 4.3 protocol drift regression test suite

## Next Steps
1. Phase 3.2: Header-sanitization refinements against real traffic captures
2. Circuit breaker for batch upstream failures (deferred from Phase 2)
3. Phase 4.4: Automated sync automation (auto-PR, auto-fallback)
4. Phase 4.5: Egress behavior tracking (admission, quota windows)
5. Run full hermetic test suite (`env -u AUTH_TOKENS -u ADMIN_TOKEN go test ./...`)

## Load-Bearing Invariants (Reminder)
- **Hermetic Test Suite**: `env -u AUTH_TOKENS -u ADMIN_TOKEN go test ./...`
- **Anti-Ban Contract**: Session POST sends headers, chat POST sends NO model header, honest FINISH
- **Tool Stripping**: `end_turn` injected upstream, stripped downstream
- **Sequential SSE Content Blocks**: Never interleave unclosed blocks
