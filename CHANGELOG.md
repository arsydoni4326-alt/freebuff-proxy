# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Fixed
- **Merge conflict resolution with upstream/main (#605 era: dead-code prune,
  settings display desync fix, restart-only key flags), preserving persistent
  database state** (merge 5 of 2026-09-17)
  - Merge of upstream/main 328261be (dead frontend-file prune
    `Stepper`/`Pips`/`FieldBox`/`Sparkline`/`history.js`, dead exports removal,
    settings renderKey desync fix for five keys, `AUTO_DISCOVER_TOKEN` +
    `REGISTRY_REFRESH` flagged restart_only, cooldown/session-park tuning
    knobs #605) into the DB-persistence lineage (c7db90b0 = merge 4 of
    2026-09-17). All 3 conflict paths resolved; **no commit made** (repo
    rule: never commit unless asked).
  - Resolution policy: **additive union** — every DB-persistence feature of
    this lineage kept untouched, every upstream knob adopted, nothing
    renamed or removed.
  - `config/config_keys.go` (`defaultRawConfig`): union of both default
    sets — our persistence/health lineage defaults (`SessionPersist`,
    `SessionStateFile`, `MaxSpendPerDay`, `BridgeCircuitBreaker*`,
    `RateLimitBurst`, `AutoRotateOnExhaustion`, `ExhaustionWarningThreshold`,
    `HealthScoreEnabled`, `TokenHealthProbes`, `TokenProbeInterval`) and
    upstream's cooldown/session-park tuning defaults (`Cooldown*Ms`,
    `SessionParkEnabled`, `SessionParkThresholdMs`, `SessionPollMaxMs`,
    `SmartProbeBackoffMaxMs`, `MaturityBackoffMs`).
  - `config/config_load.go` (`cfg := Config{...}`): same union at the
    Config-literal level — our lineage assignments and upstream's cooldown
    tuning assignments (`CooldownDefault` … `MaturityBackoff`) all populate
    the merged Config struct.
  - `pool/quota_smartprobe.go`: kept deleted-by-us (the ADR-0022 quota
    auto-probe scheduler `quota_autoprobe.go` remains the quota path);
    upstream's activity-aware smart-probe scheduler was already superseded
    on this lineage.
  - `pool/cooldown_tuning.go`: the adopted upstream push assigns
    `quotaProbeMaxInterval` from `SMART_PROBE_BACKOFF_MAX_MS`; the variable
    previously lived in the deleted smartprobe file, so its declaration
    (default 30m) and the knob's live-apply push moved here — catalog
    surface and tuning tests unchanged.
- Live-boot persistence chain verified end-to-end after resolution:
  `cli_serve.go` loads DB tokens → `SetTokenStateStore` → `RestoreTokenState`
  (locks/quarantines/cooldowns/ledgers), and `SetPoolPersist(histStore)` →
  `RestorePoolPersist()` from `Pool.Start` (pool_state
  admissions/burst/bridge/ledgers) — both untouched by the merge.

### Preserved
- SQLite token database with `session_state` table for session persistence
  (`SESSION_PERSIST`, Phase 4 DB durability) — untouched by the merge.
- Token state store (locks, quarantines, cooldowns, spend/usage ledgers)
  with `RestoreTokenState` at boot; pool_state ledger/admission restore
  (`RestorePoolPersist` from `Pool.Start`); DB settings overlay
  (ADR-0019) with the `config:migrated_env_v1` marker.
- Health scoring suite (`HEALTH_SCORE_ENABLED`), background token health
  probes (`TOKEN_HEALTH_PROBES`), bridge circuit breaker observability
  (`BreakerSnapshot` → /healthz + /metrics), per-token bridge rate limiting,
  refund tracking (`lastRefund`/`pendingRefund`) and the refund-refresh
  route.
- Quota auto-probe scheduler + ADR-0024 boot seed; maturity automation with
  the DB maturity blob (MaturityStore) contract unchanged.

### Technical Details
- Merge verified: all 3 conflict paths resolved and staged; `go build
  ./backend/...` green; `gofmt` clean on all resolved files; hermetic tests
  (`env -u AUTH_TOKENS -u ADMIN_TOKEN go test -count=1`): `config` ok
  (0.7s), `pool` ok (41s — covers cooldown_tuning + Config-literal union),
  `store` ok (1.1s — covers persist_carry / pool_persist / settings
  overlay). Noted in `session.md`.


### Fixed
- **Merge conflict resolution with upstream/main (#594 era: cooldown-window
  kinds, concurrency ladder, queue-wait telemetry, same-model
  stick-with-overflow-assist), preserving persistent database state**
  - Merge of upstream/main 7cb4ec74 (cooldown window diagnostics
    `cooldown_kind`/`cooldown_window_hours`/`cooldown_resets_at`, smart-routing
    concurrency ladder + live-turn slot queue telemetry
    `LiveTurns`/`QueuedWaiters`/`OldestWaiterMS`, queue-wait phase timing,
    history rollup export, hidden-keys settings card, same-model
    stick-with-overflow-assist #594) into the DB-persistence lineage
    (992e34e3 = merge of 2026-09-16). All 5 conflict files resolved;
    **no commit made** (repo rule: never commit unless asked).
  - Resolution policy: **additive union** — every DB-persistence feature of
    this lineage kept untouched, every upstream diagnostic adopted, nothing
    renamed or removed.
  - `pool/pool.go`: `TokenSnapshot` union — kept our `TokenValue` field
    (dashboard drawer / settings overlay key rows by the real token value;
    never written to DB or logs) and adopted upstream's cooldown-window trio
    (`CooldownKind` / `CooldownWindowHours` / `CooldownResetsAt`) so the
    freebucks-window "why is this token cooling" diagnostics work.
  - `pool/snapshot.go`: kept the Phase 5.1 health-score computation
    (`buildHealthScoreInput` → `ComputeHealthScore`) and adopted upstream's
    `routeSlotStats` saturation counters in the same snapshot rows.
  - `server/health.go`: `/healthz` per-token map union — our background
    health-probe fields (`probe_ok`, `probe_quota_ok`, `probe_error`,
    `probe_at`, `TOKEN_HEALTH_PROBES`) and upstream's additive cooldown
    window fields (`cooldown_kind`, `cooldown_window_hours`,
    `cooldown_resets_at`) are both emitted; existing consumers see no shape
    change (all new keys are conditional).
  - `server/admin_tokens_ops.go`, `server/admin_tokens_routes.go`: kept
    deleted-by-us (consolidated into `admin_tokens.go`); upstream's edits to
    the stale copies were comment-only rewording with no functional effect.

### Preserved
- SQLite token database with `session_state` table for session persistence
  (`SESSION_PERSIST`, Phase 4 DB durability) — untouched by the merge.
- Token state store (locks, quarantines, cooldowns, spend/usage ledgers)
  with `RestoreTokenState` at boot; pool_state ledger/admission restore
  (`RestorePoolPersist` from `Pool.Start`).
- Health scoring suite (`HEALTH_SCORE_ENABLED`), background token health
  probes (`TOKEN_HEALTH_PROBES`), bridge circuit breaker observability
  (`BreakerSnapshot` → /healthz + /metrics), per-token bridge rate limiting,
  refund tracking (`lastRefund`/`pendingRefund`) and the refund-refresh
  route.
- Quota auto-probe scheduler + ADR-0024 boot seed; maturity automation with
  the DB maturity blob (MaturityStore) contract unchanged.

### Technical Details
- Merge verified: all 5 conflict files resolved and staged; `go build
  ./backend/...` green; `gofmt` clean on all resolved files; `git diff
  --check` clean. Hermetic tests (`env -u AUTH_TOKENS -u ADMIN_TOKEN go
  test`): `pool` (41s, covers both pool resolutions) and `store` fully ok;
  `server` fails only the two documented pre-existing racy tests
  (`TestConcurrentReloadAndChat`, `TestSettingsDurationEchoStable`) and
  `session` only the documented pre-existing 8 JSON-file store tests —
  byte-identical to pristine-HEAD failures recorded in earlier merges; none
  introduced by this merge. Noted in `session.md`.

### Fixed
- **Merge conflict resolution with upstream/main (vendor 0.0.175: first-tab discount + per-account reset timezone), preserving persistent database state**
  - Merge of upstream/main 5589474a (vendor b609d73 0.0.175, wire re-pin
    #567, drift-data #558, first-tab discount + per-account reset timezone
    port #568, toast notifications #563, interactables e2e inventory #566,
    dashboard table tidy #561) into the DB-persistence lineage
    (6588f7a5 = merge of tag v1.8.13-arsydoni4326-alt).
  - `config/config.go|config_keys.go|config_load.go`: reconciled the key
    surface — upstream retired the model-fallback layer (`MODEL_ALIASES`,
    `FALLBACK_AFTER_MS`, `FALLBACK_MODEL`, `QUOTA_FALLBACK_MODELS`) and the
    log-surface knobs (`LOG_RING_SIZE`, `LOG_CONSOLE_WINDOW`,
    `LOG_TABLE_RETENTION`; dashboard log surface is now hardcoded: 500
    ring / 1h console window / 7d history retention) and removed
    `MATURITY_DRY_RUN` (touches run live). All retired keys are tolerated
    as unknown and ignored; no config migration needed. Every knob with a
    live consumer is kept: `BRIDGE_RATE_LIMIT_PER_TOKEN`,
    `BRIDGE_CIRCUIT_BREAKER_FAILURES/WINDOW/COOLDOWN`, `MAX_SPEND_PER_DAY`,
    `AUTO_ROTATE_ON_EXHAUSTION`, `EXHAUSTION_WARNING_THRESHOLD`,
    `HEALTH_SCORE_ENABLED`, `TOKEN_HEALTH_PROBES`, `TOKEN_PROBE_INTERVAL`.
    `MATURITY_TOUCH_MODEL` default is now `""` (= auto: cheapest served
    unmetered row, fail-closed on priced rows; explicit id overrides),
    matching upstream and the merged key catalog.
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
- Maturity auto touch model: the nightly touches still never spend
  Freebucks (fail-closed on priced rows); the resolved model now comes from
  the auto pick (cheapest served unmetered row) instead of a hardcoded
  default id.

### Added
- Upstream bridge tests merged alongside our retained rate-limit and
  DeadToken coverage: hybrid pooled-credential refusal pin
  (`TestHybridPooledCredentialRefusedOnBridge`) and the ip_capped
  remembered-error pin (`TestCooldownBridgeIpCappedSurfacesRemembered`) —
  all green against our persisted bridge-cache implementation.
- `frontend/e2e/ux.spec.ts` and the tokens page aligned with upstream's
  `POST /admin/tokens/remove` + `POST /admin/api/settings` flow (both
  routes still served; our `remove-specific` route remains available).

### Technical Details
- Merge verified: all 17 conflict files across the two merge layers
  resolved and staged; no commit made (repo rule: never commit unless
  asked). `go build ./backend/...` green; `go vet ./backend/...` clean;
  merged-out files gofmt'd.
- Full hermetic suite (`env -u AUTH_TOKENS -u ADMIN_TOKEN go test -count=1
  -timeout 10m ./backend/...`): every package ok except pre-existing
  failures reproduced byte-identical on a pristine HEAD=6588f7a5 worktree —
  `session` store JSON-file tests (8, deferred store refactor),
  `TestConcurrentReloadAndChat` and `TestSettingsDurationEchoStable` (racy
  on this lineage). None were introduced by this merge. Noted in
  `session.md`.

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
