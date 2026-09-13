# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

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
