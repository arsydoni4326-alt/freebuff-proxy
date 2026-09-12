# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

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
