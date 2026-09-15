# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Fixed
- **Merge conflict resolution with upstream/main, preserving persistent database state (settings live pages merge)**
  - `config/`: reconciled key catalog with upstream's retirements (#506 local
    throttles → unlimited, #510 dead cap knobs deleted, #515 risk_level
    removed, ADR-0027 premium-quota retirement) while re-adding every knob
    with a live consumer: `BRIDGE_RATE_LIMIT_PER_TOKEN`,
    `BRIDGE_CIRCUIT_BREAKER_FAILURES/WINDOW/COOLDOWN`, `MAX_SPEND_PER_DAY`,
    `AUTO_ROTATE_ON_EXHAUSTION`, `EXHAUSTION_WARNING_THRESHOLD`,
    `HEALTH_SCORE_ENABLED`, `TOKEN_HEALTH_PROBES`, `TOKEN_PROBE_INTERVAL`.
    `MaturityTouchModel` default stays `deepseek/deepseek-v4-flash`
    (unmetered, cost-0).
  - `pool/pool_persist.go`: kept the DB-unified runtime persistence
    (pool_state ledgers/admissions write-through) AND adopted upstream's
    new live per-token quota-cache persistence (`pool/probe/quota/<sha>`
    rows, restore via session `SeedQuota` ts-compare, orphan pruning).
    Dropped only the rows of retired features (burst, bridge daily usage,
    smart-probe scheduler blob).
  - `pool/`: restored the bridge circuit breaker plumbing fields, the
    per-client-token rate limiter (`rateLimitAllow` token bucket),
    `validateClientToken` header-injection guard, `usageResetIn` chain,
    and `BridgeTokenSnapshot.SpendPct`/`DeadToken` population — all still
    covered by live tests and /healthz consumers.
  - `server/health.go`: implemented the missing `Pool.BreakerSnapshot` and
    re-wired /healthz `circuit_breaker` + /metrics breaker gauges (previously
    stubbed to zeros pending this method) — circuit-breaker observability is
    no longer disabled.
  - `server/admin_tokens.go`: mode switch now uses the dual-layer
    persist/rollback path (`.env` + settings overlay + token marker via
    `dualWrite`/`tokenMarkerDelta`) and the session-spawn endpoint re-gained
    its `DEVTOOLS_ENABLED` 404 gate.
  - `pool/maturity.go` + dashboard maturity cards: took upstream's nightly
    15-minute pre-reset window rewrite (429 backoff, staggered slots,
    SlotDay/TouchDay, Effective/Auto touch model, restart-safe idempotency);
    the DB maturity blob (MaturityStore) contract is unchanged.
  - `pool/quota.go`: adopted upstream's Freebucks `Spendable()` gate
    (balance + claimable grants, vendor af898dc wire drift) with our
    ledger-persistence hooks intact.
  - `server/admin_tokens_routes.go`: kept deleted (consolidated
    `admin_tokens.go`; refund-refresh route lives there).

### Preserved
- SQLite token database with `session_state` table for session persistence
  (`SESSION_PERSIST`, Phase 4 DB durability) — untouched by the merge.
- Token state store (locks, quarantines, cooldowns, spend/usage ledgers)
  with `RestoreTokenState` at boot; pool_state ledger/admission restore.
- Quota auto-probe scheduler + ADR-0024 boot seed (upstream's smart-probe
  scheduler stays out; autoprobe remains the quota-freshness path).
- Health scoring (HEALTH_SCORE_ENABLED suite), token health probes,
  bridge circuit breaker + per-token bridge rate limiting, refund tracking
  (`lastRefund`/`pendingRefund`) and the refund-refresh on-view trigger.
- `MaturityTouchModel=deepseek/deepseek-v4-flash` unmetered default.

### Technical Details
- Merge verified: all 11 conflict files resolved and staged; no commit made.
  `go build ./backend/...` green; `go vet ./backend/...` clean.
- Full hermetic suite (`env -u AUTH_TOKENS -u ADMIN_TOKEN go test -count=1
  -timeout 10m ./backend/...`): every package ok except two pre-existing
  failures reproduced byte-identical on a pristine HEAD=048c10f worktree —
  `session` store JSON-file tests (8, deferred store refactor) and
  `TestConcurrentReloadAndChat` (racy on this lineage). None were introduced
  by this merge. Noted in `session.md`.

### Fixed
- **Merge conflict resolution with upstream/main (smart routing step 1)**
  - `config/config_keys.go`: reconciled both default maps — kept the ADR-0022/0024
    quota auto-probe, `MaturityTouchModel=deepseek/deepseek-v4-flash`, waiting-room,
    risk, circuit-board and health knobs; added upstream's smart-routing knobs
    (`RoutingSmart`, `TokenMaxConcurrent`, `QueueWait`, `QueueDepth`) and mirrored
    the reserved `QuotaProbeActiveInterval`/`QuotaProbeIdleHeartbeat` surface.
  - `pool/pool_lifecycle.go`: `Pool.Start` now calls `RestorePoolPersist()` so live
    `pool_state` counters (admissions, burst hits, bridge usage, account ledgers)
    survive restarts through the DB-backed store, while keeping the ADR-0024
    staggered boot-probe anchor.
  - `pool/quota_smartprobe.go` (+ `smartprobe_persist_test.go`): kept removed — the
    ADR-0022 quota auto-probe scheduler (`quota_autoprobe.go` + `quota_bootseed.go`)
    remains the quota-freshness path.
  - `server/admin_tokens_routes.go`: kept removed (consolidated `admin_tokens.go`);
    ported the `handleTokenRefundRefresh` route into the consolidated file.
  - `server/server_models_test.go`: took upstream's codex strict-`ModelInfo`
    conformance tests and restored the missing `bytes` import.
  - `pool/pool_persist.go`: kept our DB-unified runtime persistence
    (no upstream smart-probe blob rows).

### Added
- Upstream smart pool routing step 1 (`pool/route_smart.go`): per-token live-turn
  slot semaphores with FIFO waiter queues, unified scorer, `ROUTING_SMART` master
  switch (default on). Legacy acquire path restored with `ROUTING_SMART=false`.
- Refund-refresh on-view trigger (`pool/refund_refresh.go`, `POST
  /admin/tokens/{id}/refund-refresh`, dashboard refund line).
- Dashboard pending-refund lines, tier headers, and NEW markers (upstream #503-505).
- `TestConformanceCodexModelsStrictModelInfo` wire-parity coverage.

### Preserved
- SQLite token database with `session_state` table for session persistence.
- Token state store (locks, quarantines, cooldowns, spend/account ledgers) with
  `RestoreTokenState` at boot.
- Quota auto-probe scheduler + ADR-0024 boot seed (`quota_autoprobe.go`,
  `quota_bootseed.go`).
- Maturity automation (ADR-0026), health scoring, bridge circuit breaker.
- `MaturityTouchModel=deepseek/deepseek-v4-flash` unmetered default.

### Technical Details
- Merge verified: conflicts resolved and staged; `go build ./backend/...` green;
  `config` package tests green.
- Pre-existing line failures reproduced byte-identical on pristine
  develop (detached worktree at 8364ba9): `server` (10), `session` (8),
  `pool`/`dashboard` test-package vet errors on stale maturity test fields
  (`AutoTouchModel`/`SlotDay`/…), `TestMaturityDefaults` assertion drift — none
  were introduced by this merge. Noted in `session.md`.

## [v1.8.11-arsydoni4326-alt] - 2026-09-12

### Fixed
- **Merge conflict resolution preserving database persistence feature**
  - Resolved merge conflict between commit b586525 (with database persistence) and upstream/main
  - Fixed missing `lastRefund` and `pendingRefund` fields in session Manager that caused build errors
  - Removed duplicate `Manager` struct definition in `session.go` while preserving correct version in `session_manager.go`
  - Added missing `QuotaProbeActiveInterval` and `QuotaProbeIdleHeartbeat` fields to `rawConfig` struct in config package
  - Restored complete pool package file set from b586525 to preserve database persistence features
  - Added missing `TokenValue`, `HealthScore`, and `HealthScoreLabel` fields to `TokenSnapshot` struct
  - Added missing `stateStore`, `healthTracker`, `probeResults`, `breakerFailures`, and `breakerUntil` fields to `Pool` struct
  - Restored dashboard package files (`dashboard.go`, `dashboard_cards.go`, `dashboard_helpers.go`) from b586525
  - Removed obsolete files: `admin_tokens_ops.go`, `admin_tokens_routes.go`, `bridge_hardening_test.go`
  - Removed conflicting upstream files: `quota_smartprobe.go`, `quota_smartprobe_test.go`, `quota_smartprobe_fleet_test.go`

### Changed
- Temporarily disabled circuit breaker observability in `/healthz` and `/metrics` endpoints (returns zeros) until `BreakerSnapshot()` method is implemented

### Preserved
- SQLite token database with `session_state` table for session persistence
- Token state store for operational state (locks, quarantines, cooldowns, ledgers)
- Session refund tracking (`lastRefund`, `pendingRefund`) in session manager
- Health tracking and scoring system
- Quota auto-probe system from b586525

### Technical Details
- Build verified successful with `go build ./backend/cmd/freebuff-proxy`
- All database persistence features from commit b586525 preserved and functional
- Refund tracking for session early-end receipts maintained
- Token operational state persistence across restarts maintained

## [v1.8.10-arsydoni4326-alt] - 2026-09-11

### Added
- Initial database persistence mode implementation
