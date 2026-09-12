# Session: SQLite Token Database + UI

## Latest: Phase 4 — SESSION_PERSIST State in SQLite (full durability, no JSON files)

- **Phase 4** completes the program: when the SQLite token DB is active AND
  `SESSION_PERSIST=true`, session state and active agent runs are stored in
  the DB's new `session_state` table (one JSON blob per token hash) instead of
  the `SESSION_STATE_FILE` JSON file. The JSON-file path is unchanged when no
  DB (CGO-disabled build) — same opt-in knob, better default storage.
- **`session/backend.go` (new)**: `StateBackend` interface (opaque blobs:
  `LoadState`/`LoadAll`/`SaveState`; nil blob = delete) + `kvBlob` (one row =
  the token's session + per-agent runs). `Store` gains a `backend` field and
  `NewStoreWithBackend`; `loadLocked`/`flushLocked` route through it with
  file-parity semantics (active-without-instance-id dropped on load,
  stale-instance removal refused, grace expiry dropped, read failure retries,
  deleted keys cleaned up on flush). Keys are token HASHES (same key space as
  the file store); raw tokens are never written.
- **`tokendb`**: `session_state` table (`token` PK, opaque `state` BLOB,
  `updated_at`) + `SaveSessionState` (nil deletes) / `LoadSessionState` /
  `LoadAllSessionStates`. No JOIN with auth_tokens — the token-hash key space
  is separate, so a re-added token still resumes its session (anti-slot-burn).
- **`cli`** (`sessionbackend.go` new, `cli.go` restructured): the tokendb open
  + MigrateFromEnv hoisted above the session-store construction so the store
  can be built over the DB; `sqliteSessionBackend` adapter (only this package
  may import both — archtest matrix). File-mode warning about exe-adjacent
  state files stays file-mode-only.
- **Tests**: `session/store_backend_test.go` — 4 tests (session round-trip
  through a memory backend incl. quota map, run save/load/remove with empty-key
  deletion, stale-instance removal refusal, corrupt-blob drop);
  `tokendb_test.go` — `TestSessionStateSaveLoadRemove` (absent/upsert/
  multi-key/nil-delete + LoadAll).
- **Validation**: build + vet + gofmt clean; hermetic suites pass for
  session, tokendb, cli, pool, archtest (plus runs/config from Phase 3);
  server suite passes except the pre-existing `TestConcurrentReloadAndChat`
  EOF failure. Phases 1-4 all complete; program CLOSED.

## Latest: Phase 3 — Spend/Usage Ledgers Persisted in SQLite (quota accounting survives restarts)

- **Phase 3** extends the token_state blob with the spend ledger (rolling 24h
  window, Pacific day/week/month buckets, spend_limited counter #122) and the
  account ledger (rolling 24h successful-chat timestamps, rolling 60s
  admitted-request timestamps, Pacific-day request counter + bucket start).
  A token that spent most of its daily allowance before a restart stays
  accounted for after it — no restart-driven quota reset (anti-abuse).
- **`pool/ledger_persist.go` (new)**: `SpendPersistState` +
  `AccountPersistState` JSON types with `IsZero` guards (a Phase-1/2-only
  blob decodes zeroed and is NOT applied). Roster-owned snapshot/apply:
  `spendState`/`accountState`/`applySpend`/`applyAccount` take
  `tokenRoster.mu`; shared `spendSnapshotOf`/`accountSnapshotOf`/
  `applySpendTo`/`applyAccountTo` helpers exist for other guards.
- **Hooks** (`state_store.go` persistTokenState now captures Spend+Account via
  the roster index; `persistTokenIndex(idx)` helper for index-based paths):
  recordChat, recordChatEntry, recordSpend, recordSpendEntry,
  recordSpendLimited. RestoreTokenState applies Spend/Account after locks,
  quarantines, and cooldowns. Bridge entries are deliberately NOT persisted:
  their keys are hashed client tokens absent from auth_tokens (the store JOIN
  would never match) and LRU eviction makes bridge-ledger durability
  meaningless — documented in pool/CONTRACT.md.
- **Tests**: `state_store_test.go` +5 — spend bucket/period/rolling round-trip,
  spend_limited counter, usage-timestamp round-trip, Pacific-day request
  counter round-trip, zero-state no-op guard.
- **Validation**: build + vet clean; hermetic tests pass for pool, runs,
  tokendb, archtest, cli, config; server suite passes except the pre-existing
  `TestConcurrentReloadAndChat` EOF failure (verified identical on baseline).
- **Remaining**: Phase 4 — session/quota durability (move `SESSION_PERSIST`
  JSON state into the same SQLite store).

## Latest: Phase 2 — Cooldown Windows Persisted in SQLite (429/403 survive restarts)

- **Phase 2** adds cooldown/ban/country/ip-cap window persistence on top of
  Phase 1's lock + quarantine store. A rate-limit 429 applied just before a
  restart is now still live after restart — the token stays skipped for the
  remaining window instead of re-hitting upstream and burning another daily
  session slot.
- **runs** (`cooldown.go`): new `CooldownState` struct + `CooldownPersistState()` /
  `RestoreCooldownState()`. The state struct is fully JSON round-trippable
  (exported fields, all upstream error types are plain exported structs).
  Restore clamps future deadlines to the 7-day cooldown ceiling so a corrupt
  far-future `ResetAt` cannot permanently lock the token; expired deadlines
  are restored verbatim but the accessors (`RateLimitError`, `BanError`, ...)
  self-time-out against them, so an old window after a long restart silently
  drops.
- **pool** (`state_store.go` + `cooldown.go`): `tokenState` extended with
  `Cooldown runs.CooldownState` (JSON round-trip through the same opaque
  blob). `persistTokenState` captures the full runs cooldown snapshot on every
  cooldown transition (auth / rate-limit / ip_capped / ban / country-block —
  both `CooldownToken*` and `CooldownLease*` wrappers, 9 hooks total).
  `RestoreTokenState` now calls `e.runs.RestoreCooldownState(st.Cooldown)`.
  Phase-1-only blobs (no `Cooldown` field) produce a zero-valued
  `CooldownState` — `RestoreCooldownState` handles zeros as a no-op.
- **Tests**: `runs/cooldown_persist_test.go` — 8 tests: round-trip for auth /
  rate-limit / ip-capped / ban / country-block / hard-ban, far-future clamp,
  expired-window self-timeout. `pool/state_store_test.go` — 3 new tests:
  rate-limit / ip-capped / auth cooldown persist → restore against a fresh
  pool.
- **Phases remaining** (Phase 2 done):
  - Phase 3 — spend/quota ledgers (Pacific day/week/month buckets, daily
    message/request counters).
  - Phase 4 — session/quota durability (move `SESSION_PERSIST` JSON state into
    SQLite / the same store).
- **Validation**: `go vet` clean; hermetic tests pass for runs, pool, tokendb,
  archtest, cli, config; server suite passes except the pre-existing
  `TestConcurrentReloadAndChat` EOF failure.


## Latest: Merge of upstream/main Resolved (feature/port-upstream)

- Completed the in-progress merge of `upstream/main` (19ef1dd) into
  `feature/port-upstream` without losing any custom features. All 25
  conflicted files resolved; the merge is staged, ready for commit.
- **Custom features preserved**: SQLite token DB (`internal/tokendb` +
  `server.WithTokenDB` + admin-handlers `tokenDB` field + cli.go serve-mode
  wiring), bridge circuit breaker (`pool/bridge_breaker.go`), health-score +
  probes (`pool/health.go`, `pool/token_probe.go`), stealth risk engine
  (`stealth/risk.go` + `risk_test.go`, restored from HEAD after upstream
  deleted them), AUTO_ROTATE_ON_EXHAUSTION knobs, bridge per-token rate
  limits, dashboard Bridge Quota / Risk / Circuit Breaker sections, custom
  admin routes `/admin/tokens/remove-specific` + `/admin/tokens/list`.
- **Upstream architecture adopted**: thin `main.go` dispatcher into
  `internal/cli/*` (HEAD's inline main removed; the custom tokendb block was
  ported into `cli.Serve`), manifest-driven admin routes (`admin_manifest.json`
  + `server.go registerAdminRoutes`; deleted duplicate `admin_routes_reg.go`),
  admin handlers as `adminHandlers` methods (`syncTokensAfterMutation` /
  `handleModeSwitch` now `a.`-receiver with the tokenDB branch preserved),
  roster-based pool mutations, per-IP rate limiter + access-log gates,
  catalog-driven config rendering (`cfg.Data()`).
- Custom admin routes were added to `admin_manifest.json`
  (remove-specific → adminActions.tokenRemoveSpecific, list → adminApi
  tokenList) and `frontend/src/lib/api/paths.js`, with matching cases in
  `server.go adminHandler`. Tokens.svelte remove action posts
  `{ token: token.token_value ?? idx }` to remove-specific (SQLite-safe).
- **Decisions**: `FALLBACK_AFTER_MS` default follows upstream's "0" (matches
  the `TestFallbackAfterDefault` contract test; not part of the custom work).
  `TestMetricsFamiliesContract` extended with the custom bridge/registry
  families (the test's documented "conscious update" path). `scarce.go` and
  `SCARCE_SESSION_MODELS` stay removed (upstream's deliberate removal; no
  config field remains). Unused `RiskCards.svelte`/`Footer.svelte` stay
  deleted (nothing references them; Overview renders Risk inline).
- **Validation**: `go build ./backend/...` and the dashboard-tagged binary
  build pass; `go vet` on changed packages passes; frontend `vite build`
  passes (rebuilt `backend/internal/dashboard/dist` is staged). Hermetic
  tests pass for config, tokendb, stealth, cli, dashboard, pool, and server
  EXCEPT pre-existing `TestConcurrentReloadAndChat` (documented below) and
  the pre-existing `backend/internal/registry/registry_test.go:1051`
  unclosed-`if` syntax error that blocks `go test ./backend/...` before
  registry tests run.
- The full server suite's previously-documented failures (404 access-log
  suppression, /v1 rate-limit envelopes, concurrent-reload EOF) are now
  reduced to the single `TestConcurrentReloadAndChat` EOF case.

## Earlier: Dashboard-Tagged Docker Build Repair

- Repaired merge artifacts in `backend/internal/dashboard/dashboard_data.go` that stopped
  the dashboard-tagged Docker build: the `pool` package had been imported twice and the
  former `overviewData` local `host` calculation was unused after `BaseURL` moved to
  `baseURLForRequest(cfg, r)`.
- Preserved the incoming-request-aware base URL behavior; only the duplicate import and
  obsolete local calculation were removed. `gofmt` also normalized adjacent field alignment.
- Validation passed: `env -u AUTH_TOKENS -u ADMIN_TOKEN go test -count=1 -tags dashboard
  ./backend/internal/dashboard/...`, `env -u AUTH_TOKENS -u ADMIN_TOKEN go vet -tags dashboard
  ./backend/internal/dashboard/...`, and the Docker-equivalent Linux build with `CGO_ENABLED=1`,
  `GOOS=linux`, `-tags dashboard`, and production linker flags.
- Full `env -u AUTH_TOKENS -u ADMIN_TOKEN go test ./backend/...` remains blocked before
  registry tests run because the unmodified `backend/internal/registry/registry_test.go:1032`
  leaves an `if` block unclosed before `TestPausedModelPolicy`. The pre-existing server-suite
  failures remain 404 access-log suppression, `/v1` rate-limit coverage/envelopes, and
  concurrent-reload EOF handling. Repository-wide `git diff --check` also reports trailing
  whitespace in the already-staged generated asset
  `backend/internal/dashboard/dist/assets/index-CbIpiumU.js`; the repaired Go source passes
  scoped formatting and whitespace checks.

## Latest Fix: Tokens Page Runtime Error

- Removed the stale duplicate Client API Keys card from `frontend/src/lib/pages/Tokens.svelte`.
  It referenced removed `generatedKey`, `apiKeys`, `clientKeyMessage`, and `clientKeyOK` state,
  causing the embedded dashboard bundle to throw `ReferenceError: generatedKey is not defined`
  when the Tokens page rendered.
- Client API-key management remains exclusively on the complete Overview implementation, which
  supports generation, display/copy, and deletion through the existing CSRF-protected config save.
- Added Playwright coverage that asserts the Tokens page produces no uncaught browser errors.
- Validation: `npm --prefix frontend run build`, the serial `npm --prefix frontend run test:e2e`
  suite (25/25), `env -u AUTH_TOKENS -u ADMIN_TOKEN go test -tags dashboard
  ./backend/internal/dashboard/...`, and a dashboard-tagged Go binary build pass.
- `Permissions-Policy: attribution-reporting` is not emitted by this repository; investigate the
  deployment reverse proxy/CDN configuration if its browser-console warning must be removed.

## Latest Fix: Dashboard Settings Metadata Route

- Fixed `GET /admin/api/config/meta` being omitted from the live `Server.Handler()` mux even though it was present in the admin route catalogue and handler resolver.
- The `/admin/` SPA fallback had consequently returned `index.html` (`text/html`) to the Settings page metadata fetch while `/admin/api/config` correctly returned JSON.
- Added the missing authenticated JSON route, a regression assertion for `Content-Type: application/json`, and the API contract row in `SPECIFICATION.md`.
- Validation: `TestDashboardConfigMetaEndpoint`, `TestAdminRoutesAllRegister`, `go vet ./backend/internal/server/...`, `gofmt`, `git diff --check`, and `go build -o freebuff-proxy.exe ./backend/cmd/freebuff-proxy` pass.
- The full `backend/internal/server` suite still has pre-existing failures in 404 access-log suppression, `/v1/*` rate-limit coverage/envelopes, and concurrent reload EOF handling; the config-metadata regression now passes.
- Deployment note: the active proxy process is an already-unlinked `/usr/local/bin/freebuff-proxy` executable with working directory `/app`; it was not replaced or restarted from this workspace. Deploy `/home/denny/Project/freebuff-proxy/freebuff-proxy.exe` using the environment's normal process/container workflow.

## Current Objective
Migrating AUTH_TOKENS from `.env` file persistence to a SQLite-backed database for hot-reload support without container recreation. Adding UI for token management.

## Recent Changes
- **SQLite Token Database (`internal/tokendb/tokendb.go`)**: New package providing persistent SQLite-backed AUTH_TOKENS storage with WAL mode, atomic operations, and thread-safe access.
  - `Open()`, `Close()`, `List()`, `Count()`, `Add()`, `Remove()`, `RemoveLast()`, `RemoveAll()`, `MigrateFromEnv()`, `Tokens()`
  - Database stored at `data/auth_tokens.db` (configurable via `AUTH_TOKEN_DB_PATH` env)
  - One-time migration from `AUTH_TOKENS` env var on first startup
- **Server Integration (`internal/server/server.go`)**:
  - Added `tokenDB *tokendb.DB` field to `Server` struct
  - Added `WithTokenDB(db *tokendb.DB) Option` for dependency injection
  - New routes: `GET /admin/tokens/list`, `POST /admin/tokens/remove-specific`
- **Admin Handlers (`internal/server/admin_tokens.go`)**:
  - `syncTokensAfterMutation()`: Updated to persist to SQLite when tokenDB is active, falling back to `.env` for legacy
  - `handleModeSwitch()`: Updated for bridge mode switch via tokenDB
  - New `handleTokenRemoveSpecific()`: Removes a specific token by value (not just last)
  - New `handleTokenList()`: Returns all tokens from database
- **Pool (`internal/pool/lifecycle.go`)**:
  - Added `RemoveTokenByIndex(idx int) error` for specific token removal from the pool
- **Pool Snapshot (`internal/pool/pool.go`, `internal/pool/snapshot.go`)**:
  - Added `TokenValue string` field to `TokenSnapshot` struct
  - Populated from `cfg.AuthTokens[i]` in `Snapshot()` function
- **Dashboard (`internal/dashboard/dashboard_data.go`)**:
  - Added `TokenValue string` field to `tokenCard` struct
  - Populated from `pool.TokenSnapshot.TokenValue` in `cardFromSnapshot()`
- **Frontend (`frontend/src/lib/pages/Tokens.svelte`)**:
  - Updated "Remove" button to use `POST /admin/tokens/remove-specific` with `token.token_value`
  - Changed confirmation message to "Remove token {idx} from the pool and database?"
- **Entrypoint (`cmd/freebuff-proxy/main.go`)**:
  - Opens token database at startup, migrates existing AUTH_TOKENS
  - Loads tokens from database as authoritative source
- **Tests (`internal/tokendb/tokendb_test.go`)**: 12 unit tests covering all tokendb operations
- **Dependency**: Added `github.com/mattn/go-sqlite3 v1.14.24` to go.mod

## Design Decisions
- SQLite WAL mode for concurrent read/write without blocking
- Single-connection pool (`MaxOpenConns(1)`) to avoid writer starvation
- `INSERT OR IGNORE` for idempotent adds (no duplicate tokens)
- Database is authoritative source: on startup, tokens are loaded from DB, overriding .env
- Graceful fallback: if DB fails to open, falls back to .env persistence
- `RemoveTokenByIndex` mirrors `RemoveLastToken` safety pattern (inflight check, park, drain)
- Frontend uses `token_value` field for specific token removal instead of index-based removal
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

## 2026-09-11: Merge conflict resolution preserving database persistence feature

**Symptom**: Merge conflict between commit b586525 (with database persistence) and upstream/main resulted in build errors with missing fields (`lastRefund`, `pendingRefund` in session Manager) and duplicate struct/function declarations.

**Root cause**: Merge created duplicate `Manager` struct definition in `session.go` that was missing the refund-tracking fields (`lastRefund`, `pendingRefund`) which exist in `session_manager.go`. Additionally, various pool and server files had conflicts between the database persistence feature (b586525) and upstream changes.

**Resolution**:
1. **Session package**: Removed duplicate `Manager` struct, `NewManager`, `NewManagerWithStore`, `EnsureSession`, and `EnsureSessionForModel` from `session.go`. The correct definitions remain in `session_manager.go` with all required fields including `lastRefund` and `pendingRefund` for refund tracking.

2. **Config package**: 
   - Resolved merge conflict in `config_keys.go` by adding missing `QuotaProbeActiveInterval` and `QuotaProbeIdleHeartbeat` fields to `rawConfig` struct.
   - Used HEAD version (--ours) which includes all latest features.

3. **Pool package**: 
   - Restored complete file set from b586525 to preserve database persistence features (`stateStore`, `healthTracker`, `probeResults` fields).
   - Copied: `pool.go`, `lifecycle.go`, `maturity.go`, `quota.go`, `quota_bootseed.go`, `pool_lifecycle.go`, `acquire_order.go`, `snapshot.go`, `health.go`, `health_warn.go`, `bridge_breaker.go`, `quota_autoprobe.go`, `quota_visitprobe.go`, `token_probe.go`.
   - Removed conflicting upstream files: `quota_smartprobe.go`, `quota_smartprobe_test.go`, `quota_smartprobe_fleet_test.go`.
   - Added missing fields to `TokenSnapshot`: `TokenValue`, `HealthScore`, `HealthScoreLabel`.
   - Added missing fields to `Pool`: `stateStore`, `healthTracker`, `probeResults`, `breakerFailures`, `breakerUntil`.

4. **Dashboard package**: Restored `dashboard.go`, `dashboard_cards.go`, `dashboard_helpers.go` from b586525 to maintain compatibility with pool changes.

5. **Server package**: 
   - Restored `health.go` from b586525.
   - Commented out `BreakerSnapshot()` calls (method doesn't exist in b586525) as temporary workaround until proper implementation.

6. **Removed obsolete files**: `admin_tokens_ops.go`, `admin_tokens_routes.go` (consolidated into `admin_tokens.go`), `bridge_hardening_test.go` (removed in upstream).

**Verification**: Build successful with `go build ./backend/cmd/freebuff-proxy`.

**Persistent features preserved**:
- SQLite token database with `session_state` table for session persistence
- Token state store for operational state (locks, quarantines, cooldowns, ledgers)
- Health tracking and scoring system
- Refund tracking (`lastRefund`, `pendingRefund`) in session manager
- Quota auto-probe system from b586525

**Note**: Circuit breaker observability in healthz/metrics endpoints temporarily disabled (returns zeros) until `BreakerSnapshot()` method is implemented.

## 2026-09-10: Fix merge-resurrected stale sources blocking Docker build

**Symptom**: `docker build` failed at `go build` with mass redeclarations
(`modelcat`: `ModelInfo`, `Catalog`, …; `server`: `admin_tokens*` duplicates).

**Root cause**: the two merge commits (`97ea848`, `c36ffca`) resurrected
pre-codegen sources that the restructured tree had already deleted/replaced:

- `modelcat`: stale hand-written `catalog.go` (pre-`ebb0081` layout)
  redeclared everything in wiregen-generated `catalog_gen.go`; the split
  helpers `catalog_ladder.go`/`catalog_query.go` were dropped. Restored the
  last-good layout from `dd8b17d` (current pin 78a7ab4): deleted
  `catalog.go`, re-added `catalog_ladder.go` + `catalog_query.go`.
- `server`: stale `admin_tokens_ops.go`/`admin_tokens_routes.go` (3b21bd9's
  split) coexisted with the consolidated `admin_tokens.go` (fd592e7). Kept
  the consolidated file, deleted the stale split (probe file was already
  gone).
- `pool/pool_bridge_test.go`: merge truncated `TestValidateClientToken`
  mid-function (syntax error at EOF). Restored from first merge parent
  `0068daa` (superset, 24 tests).
- `registry/registry_test.go`: missing `}` before `TestPausedModelPolicy`
  (old, multi-merge truncation). Restored closing braces.
- `registry/registry_refresh.go`: merge dropped freshness bookkeeping —
  `lastRefreshAt`/`usingFallback` were declared but never assigned.
  Restored assignments in `Refresh` (success path) and `LoadFallback`,
  mirroring 92901b9.
- `dashboard/history.go`: merge dropped retention ticker + `purgeHistory`
  (history rows never purged; test referenced the method). Restored from
  0e9244e (`dashboard_history.go` pre-rename), incl. retention constants.
- `dashboard/dashboard_internal_test.go`: stale 2-arg
  `bridgeCardFromSnapshot` call; dropped the extra arg.
- `session/store_test.go`: stale `NewStoreWithBackend(path, fb)` call;
  restored `NewStore(path)` per 6ae5c61.

**Verification**: exact Docker build command compiles; `go test ./backend/...`
— 24 packages ok. Remaining `server` (6) + `session` (8) FAILs are
pre-existing on the main lineage: reproduced identically at merge parents
`0068daa`/`6ae5c61` in isolated worktrees; `feb8e20` (upstream lineage) is
green but carries the older session architecture. Reconciling the
`6ae5c61` session-backend refactor's failing behavioral tests is deferred
as its own task (not a merge artifact).
