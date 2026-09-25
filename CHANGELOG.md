# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Changed
- **Merged `upstream/main` (#730–#743)** — 14 upstream commits through the
  vendor `276db8d` (`0.0.193`) pin, integrated on `develop` via the git-flow
  feature branch `feature/upstream-merge-2026-09-25`. All origin features are
  preserved (SQLite token DB, maturity automation, smart routing, quota
  auto-probe, update-check stamp, circuit-breaker observability, registry
  freshness). The upstream deprecation notice (#743) is kept for provenance
  but reworded to state that this fork continues maintenance.
- **Merged `upstream/main` (#708–#727)** — 19 upstream commits through the
  vendor `40c75256` (`0.0.188`) pin, integrated on `develop` via the git-flow
  feature branch `feature/upstream-merge-2026-09-24`. All origin features are
  preserved (SQLite token DB, maturity automation, smart routing, quota
  auto-probe, update-check stamp, circuit-breaker observability, registry
  freshness).

### Added
- **403 `free_mode_cost_mode_required` gate classification** (#741) — a
  Freebuff-only agent id sent with `codebuff_metadata.cost_mode != "free"`
  now surfaces a dedicated 403 sentinel (`ErrFreeModeCostModeRequired`), a
  config refusal like `free_mode_invalid_agent_hierarchy` — never a cooldown,
  never the generic 502.
- **Session locality / egress region detection** — the gateway now declares
  the session timezone from `SESSION_TIMEZONE` (override), the detected egress
  region, or the host zone, and surfaces `session_timezone`,
  `session_timezone_source`, and `egress_region` on `/healthz`
  (`backend/internal/egress/`, `docs/decisions/locality-timezone.md`).
- **Streak Freebucks daily bonus** — the 7+ day streak bonus is surfaced on
  token snapshots and the dashboard Allowances panel.
- **Dashboard streak-touch lever** — `POST /admin/tokens/streak-touch` runs
  on-demand streak touches across pooled accounts (`ForceMaturityTouch`,
  adapted to the origin maturity design; `?force=true` bypasses the
  client-active/today-used skips).
- **Timezone autocomplete** for `SESSION_TIMEZONE` in the dashboard settings.

### Fixed
- **Streak-touch FINISH wire fixes** (#739, #742) — the touch's FINISH step id
  is now an RFC 4122 v4 UUID (`newTouchStepID`, upstream's agent-step schema
  rejects non-UUID ids with 400), and a failed touch turn ships NO step (the
  vendor step enum allows only `running|completed|skipped`; the run-level
  status carries the failure). Both were reproduced live as 400s that
  `maturityTouchRun` silently swallowed. The origin error-checking on
  `FinishRun` is kept.
- **Streak-touch route mount dedupe** (#738) — upstream's official
  `POST /admin/tokens/streak-touch` manifest/OpenAPI row superseded the
  origin-ported row from the #708–#727 merge (duplicate removed; the
  `StreakTouchResponse` type now pins the `{ok, results, total}` envelope).
- **Egress CLI-spoof gaps** (#732, #740) — UA 151, Bearer-only runs, ad legs,
  and verbatim expiry handling closed; the ads product UA now claims the
  vendored CLI version.
- **Tool-call translation** (#733, #734) — live CF-Worker gate mirrored and
  ruled out for #729; #729 tool-call translation matrix added (convert +
  server).
- **Waiting-room same-session retry** (#737) — transient upstream queues
  (`free_mode_capacity_deferred`, the waiting room) retry in place against
  the same session under the `TRANSIENT_RETRIES` budget; transport failures
  still retry on a fresh connection.
- **Merge conflict resolutions** — upstream's `server.go`/`admin_tokens.go`
  package splits were re-consolidated onto the origin single-file structure
  (origin features intact); the upstream `MaturitySnapshot`-based maturity
  design was not adopted (origin `maturityState` automation kept); the
  upstream `FreebucksDailyBonus` and session-locality additions were ported
  onto the origin design.
- **Test catalog drift** — `maturity_auto_test.go` and
  `quota_pool_nil_test.go` updated for the upstream Solar swap (Solar Pro 4 →
  Solar Mini 4) and the premium-pool change (GPT-5.6 Luna → GPT-6 Luna).

## [v1.18.4] - 2026-09-23

### Fixed
- **Update-check commit stamp wiring (ARSYDONI UPDATE SOURCE)** — the
  Dockerfile ldflags rename in `8d08107f` (`VERSION`/`COMMIT` →
  `APP_VERSION`/`APP_COMMIT`) was not carried into its two callers:
  `docker-build.sh` and `docker-compose.yml` still passed the old names,
  which the Dockerfile no longer declares. Local/compose builds silently
  lost the `main.commit` stamp, degrading the dashboard "Check for
  Updates" signal to the release-tag comparison (or nothing on dev tags).
  Both callers now pass `APP_VERSION`/`APP_COMMIT`/`BUILD_DATE`, matching
  the Dockerfile ARGs. The merge-guard policy in
  `frontend/src/lib/README_ARSYDONI_UPDATE.md` documents that any future
  build-arg rename must move all callers in the same commit.
- **Verified intact**: the button, backend endpoint, and update signal were
  already present and merge-guarded in source and in the served bundle
  (`index-BT44S9Xr.js`); only the build stamp wiring was broken.

## [v1.18.3] - 2026-09-23

### Fixed
- **Docker image build (`go-builder` stage)** — merge `805942fc` rewrote
  `backend/internal/session/session_admission.go` and dropped the
  `admitStart := time.Now()` declarations from both `adoptOrCreate` and
  `refresh` while keeping the `queueElapsedHuman(admitStart)` log calls, so
  `go build` failed with `undefined: admitStart` (lines 167/573) and the
  release binary was never produced (`chmod: cannot access
  '/out/freebucks-proxy'`). Both declarations are restored exactly as
  introduced by `ebb00810` — log-only, zero behavior change.
- **Second independent build breaker** — `backend/internal/pool/bridge.go`
  carried a duplicated import block from the same merge (`strings`, `sync`,
  `sync/atomic`, `time` re-declared then unused), failing `go build
  ./backend/...` on its own. The duplicate block is removed.
- **Verification**: `go build ./backend/...` green; the exact Dockerfile
  `go build` command produces a working binary; gofmt/vet green; admission
  session tests green.

### Changed
- **`session.md` untracked** — merge `805942fc` added `session.md` to
  `.gitignore` (agent session state, never public); the tracked copy is
  removed from the index (file remains on disk locally).

## [v1.17.1] - 2026-09-21

### Fixed
- **Docker image build (`go-builder` stage)** — the `go build` step targeted
  the non-existent package path `./backend/cmd/freebuff-proxy`, failing with
  `stat /src/backend/cmd/freebuff-proxy: directory not found`. The actual
  package directory is `backend/cmd/freebucks-proxy`. The build output and
  runtime-stage `COPY` now standardize on the `freebucks-proxy` binary name,
  matching the pre-existing `ENTRYPOINT ["/usr/local/bin/freebucks-proxy"]`,
  so the image builds end-to-end again.

## [v1.17.0] - 2026-09-21

### Changed
- **Merged `upstream/main` drift slice (#672–#675)** — upstream-drift workflow
  gains a `repository_dispatch` fast-path (type `freebuff-cli-release` with
  `client_payload.version`, fired by the new `scripts/watch-freebuff-release.sh`
  reference poller intended for infra-box cron), a `version_dedupe` job that
  stands downstream jobs down when an open PR already carries the live version,
  and an auto re-pin stage (`scripts/repin-all.sh --bot <live-version>`) that
  advances both pins (vendor-version.txt + snapshots.json) atomically after the
  version's drift PRs merge — never auto-merging.
- **New script `scripts/drift-impact.sh`** — export→consumer impact map over the
  exact drift report; exit 1 announces impact entries (never fails the job).

### Fixed
- **Merge completed over upstream's history rewrite** — upstream rewrote the
  history carrying #667–#671 (new SHAs), so the stale merge-base surfaced ~70
  spurious "both added" conflicts against content `develop` had already merged,
  plus re-additions of fork-deleted files. Resolved against the true merge
  point (`a1f10950`): fork-deleted files (`server_init.go`, `server_routes.go`,
  `admin_tokens_ops/probe/routes.go`, `dashboard_history.go`) stay deleted —
  their content already lives in the fork's merged `server.go`,
  `admin_tokens.go`, and `history.go`.

### Preserved
- All merge-guarded fork features, byte-identical to pre-merge HEAD (see
  `frontend/src/lib/README_ARSYDONI_UPDATE.md`): the ARSYDONI UPDATE SOURCE
  update modal + sidebar "Check for Updates" button sourced from
  `arsydoni4326-alt/freebuff-proxy` (`updatecheck.DefaultRepo`, commit-hash
  signal, e2e fixtures), the SQLite token database (`internal/tokendb`), the
  bridge circuit breaker, the passive risk engine, and the fork's
  `DRIFT_WEBHOOK_URL` webhook-alert step — re-kept inside the adopted
  upstream-drift workflow.

### Technical Details
- Resolution policy: every path where HEAD and `upstream/main` were
  byte-identical took the fork side; all real conflicts kept the fork
  implementation (HEAD matched the fork, `upstream/main` was the plain
  pre-fork variant). The only adopted deltas are the #672–#675 files:
  `.github/workflows/upstream-drift.yml`, `scripts/drift-impact.sh`,
  `scripts/watch-freebuff-release.sh`.
- Verified: `go build ./backend/...`, `go vet` green; short backend lane green
  except the 12 known pre-existing failures (pool limited-IP trio, session
  legacy-file/store group, upstream 402 classification — unchanged from
  v1.16.0); bash syntax checks on all four drift scripts; workflow YAML parses.

## [v1.16.0] - 2026-09-21

### Changed
- **Merged `upstream/main` through the `freebucks-proxy` rename (#668–#671)** —
  module path (`freebuff-proxy` → `freebucks-proxy`), Go import paths across
  the whole backend, binary name, and the Prometheus namespace
  (`freebuff_proxy_*` → `freebucks_proxy_*`). The pre-rename metric
  namespace keeps resolving through the upstream deprecated alias section on
  `/metrics` (marked `# Deprecated:`, one emit path, TODO remove after one
  release); the merge-guarded fork families (bridge_*, breaker_*,
  registry_*, allowlist_skips) were migrated onto the new namespace and now
  ride the same alias derivation as the core families.

### Fixed
- **Merge-resolution slips caught by the test suites**
  - `backend/cmd/freebucks-proxy/main.go`: `-version` printed the pre-rename
    product name (`freebuff-proxy dev`); now matches the renamed binary
    (`TestVersionFlagPrintsVersion`, `TestLifecycleSetupDoctorVersion`).
  - `backend/internal/dashboard/admin_wire.go`: the fork's token routes
    (`POST /admin/tokens/remove-specific`, `GET /admin/tokens/list`,
    `POST /admin/tokens/{id}/maturity`, `POST /admin/tokens/{id}/maturity/touch`)
    existed in the route mapper and the admin manifest but had no
    `AdminAPIPaths` row — the openapi-emit table/manifest invariant now holds
    (`TokenRemoveSpecificRequest` / `TokenListResponse` /
    `TokenMaturityRequest` added), and `data/openapi.json` was regenerated.

### Preserved
- All merge-guarded fork features (see `frontend/src/lib/README_ARSYDONI_UPDATE.md`):
  the ARSYDONI UPDATE SOURCE update modal + sidebar "Check for Updates"
  button sourced from `arsydoni4326-alt/freebuff-proxy` (release URL,
  `updatecheck.DefaultRepo`, commit-hash comparison, fixtures), and the
  SQLite token database (`internal/tokendb`, dashboard token
  add/remove/list/remove-specific surface).
- Persistent SQLite state (sessions, cooldowns, spend ledgers, token state).

### Technical Details
- Resolution policy: upstream's delta since the merge base was the rename
  plus docs/security chores; fork-only files (tokendb, update modal,
  bridge/breaker/registry metrics) were kept with new import paths; files
  the fork deleted (`server_init.go`-equivalent code merged into
  `server.go`, legacy `admin_tokens_ops/probe/routes.go`) stay deleted —
  upstream's delta to them was rename-only.
- Verified: `go build ./backend/...`, `go vet`, `gofmt` clean; short backend
  lane green except the 12 known pre-existing failures (pool unfit/limited-IP
  trio, session legacy-file/store trio groups, upstream 402 classification —
  all reproduced byte-identical on a pristine worktree at pre-merge HEAD);
  svelte-check 0 errors; unit 45/46 (1 pre-existing `modelOptions` failure);
  dist rebuilt and committed (bundle `index-CqmrXinL.js`), dashboard tag
  build green; e2e fixtures carry the fork update fields.

## [v1.15.0]

### Added
- **"Check for Updates" button in the sidebar footer (ARSYDONI UPDATE
  SOURCE)** — manual, cache-bypassing check (`?force=true`) that opens the
  update-status dialog: update-available (warning, changelog + release
  link) or up-to-date (success); failures surface as a toast only.

## [v1.14.0]

### Changed
- **Update check now compares commit hashes (ARSYDONI UPDATE SOURCE)** —
  the running build's commit (stamped at build time via
  `-X main.commit=...`) is compared against the fork's `main` branch head
  (`commits/main`, 10-minute cache): every push to `main` marks the build
  outdated, no GitHub release required. Release-tag comparison remains the
  fallback for dev builds without a stamped commit. The Dockerfile now
  declares the `COMMIT` build-arg (previously the CI-passed commit was
  silently dropped), and docker-compose/deploy/goreleaser stamp it too.

## [v1.13.0]

### Added
- **Update-available modal on every dashboard load (ARSYDONI UPDATE SOURCE,
  merge-guarded — see `frontend/src/lib/README_ARSYDONI_UPDATE.md`)**
  - `backend/internal/updatecheck`: repo pin moved to the fork
    (`arsydoni4326-alt/freebuff-proxy`); new `Info` fetch returns the latest
    release tag, that tag's commit (best-effort `commits/<tag>` lookup), and
    the release body (that release's changelog only); `Latest` remains the
    tag-only view. Same 6h cache, single-flight, and fail-open/backoff
    semantics.
  - `GET /admin/api/version` (`VersionResponse`) now carries optional
    `latest_commit` + `changelog`; the update URL points at the fork's
    releases page.
  - `frontend/src/lib/updateCheck_arsydoni.js` +
    `frontend/src/lib/components/UpdateModal_Arsydoni.svelte`: `App.svelte`
    checks once per page load and opens the modal whenever `has_update` is
    true (shown again on every refresh while outdated; nothing renders when
    current). Modal shows installed version, latest version + short commit
    hash, and the latest release's changelog.
  - Tests: `updatecheck` Info/best-effort-commit/skip tests,
    `updateCheck_arsydoni.test.js`, `e2e/update-modal-arsydoni.spec.ts`.

## [v1.12.2]

### Fixed
- **Merge-resolution slips from the v1.12.1-arsydoni4326-alt union** (caught
  by the Docker image build + pool test suite)
  - `pool/pool_persist.go`: re-collect `quotaEntries []*tokenEntry` under the
    roster lock in `snapshotPoolState` — the issue-#656 ledger-capture
    refactor dropped the declaration while the quota persistence loop kept
    consuming it (`undefined: quotaEntries` broke `go build -tags dashboard`,
    i.e. the Docker image build).
  - `pool/ledger_persist.go`: `applySpendTo` (token_state restore path)
    recomputes the incremental `spendLedger.rollingTotal` after rebuilding
    `rolling` — the merge fixed `installLedger` (pool_state path) but missed
    this twin, so restarts read `Rolling24h = 0` from the hot snapshot path
    (`TestSpendLedgerPersistsAndRestores` regression).

## [v1.9.1-arsydoni4326-alt]

### Fixed
- **Merge conflict resolution with upstream/main (#606–#645 era: MASQ pool
  engine, PIN_MODEL/pin skips, zero-cost smart probe-all, session
  single-writer cutover), preserving persistent database state** (merge 6 of
  2026-09-19)
  - Merge of upstream/main e18a3611 (MASQ — Minimal Account Slot Queue
    `spill_order`/`spill_queue`/`slot_ledger`, `PIN_MODEL` + `pin_skips`,
    smart zero-cost probe-all `ProbeAllTokens` + probe honesty #629/#644,
    cookie/secret .gitignore hardening, `tokens/{id}/drop-session` kept
    reporting, `SLOTS_PER_ACCOUNT`/`MAX_SPILL_ACCOUNTS`/`QUEUE_WAIT`/
    `SMART_PROBE_*` knobs) into the DB-persistence lineage (36a19168 = merge
    5 + v1.8.14 tag). All 25 conflict paths resolved; staged and committed
    as `7b8e6932` during this session.
  - Resolution policy: **additive union plus new-engine adoption** — every
    DB-persistence feature of this lineage kept untouched; upstream's MASQ
    acquire engine adopted; our features re-ported onto the new engine
    where upstream replaced the old surface.
  - **Database persistence kept intact**: `session/store.go` (sessions_persist
    + runs blob + Freebucks/deep-clone fields), `pool_persist.go` keeps our
    pool-side per-token quota cache (`pool/probe/quota/*`) **and** adopts
    upstream's terminal-cooldown hints (`pool/cooldown/*`) + bridge survivor
    blob — the upstream "retire and drain" of `pool/probe/quota/*` was NOT
    adopted (this lineage's own writer/restore stays authoritative); boot
    chain `cli_serve.go` → `SetTokenStateStore`/`RestoreTokenState` +
    quota boot seed (ADR-0024) + maturity blob restore (ADR-0026) all
    preserved and re-wired.
  - **Feature re-ports onto the MASQ engine**: MODEL_LOCKS gating +
    `allowlist_skips` fail-fast/filter in `Acquire`; limited-ip unfit
    marking re-added at the admission site + server chat hooks
    (clear-on-success / mark-on-`ErrModelIPLimited`); bridge ip_capped
    remembered-error consult; `TOKEN_ROTATION=random` head rotation;
    `maturitySnapshot` view on `/healthz`; cooldown-tuning live push
    (`applyCooldownTuning`) restored in `New`/`SetConfig`; `config/cooldown.go`
    knob accessors restored (upstream had excised them).
  - `config` trio (`config_keys.go`, `config_load.go`, config.go,
    config_validate.go): full two-way union — our lineage defaults/overlays
    (health, bridge breaker, spend, cooldown *Ms knobs, quota probes,
    maturity, routing) plus upstream's MASQ/PIN/SMART_PROBE knobs; both
    `SMART_PROBE_BACKOFF_MAX` (string) and `SMART_PROBE_BACKOFF_MAX_MS` feed
    one Config field (string wins, Ms falls back to 30m).
  - `server`: `admin_tokens.go` consolidated handler file adopted upstream's
    zero-cost `ProbeAllTokens` probe-all + precious-keep drop-session
    reporting (kept-deleted `admin_tokens_probe.go`/`admin_tokens_routes.go`
    stay deleted); maturity admin routes re-registered; `health.go` emits
    `pin_skips_total` (upstream) **and** `allowlist_skips_total` (ours);
    `engine_attempt.go` re-ports the unfit clear/mark hooks.
  - `snapshot.go`/`pool.go`/`bridge.go`/`pool_lifecycle.go`: field-level
    unions — our `TokenValue`/health-score/quota-boot/park/breaker/gate
    fields plus upstream's MASQ lanes, pin view, cooldown-hint mirror.
  - Restored from our lineage (upstream deleted): `config/cooldown.go`
    (+accessors), `cli/quota_seed.go`, `cli/maturity_store.go`,
    `pool/maturity.go` (+tests), `pool/unfit.go`, `pool/model_locks.go`,
    `pool/quota_bootseed.go`, `pool/quota_visitprobe.go`,
    `pool/cooldown_tuning.go`, `session/session_park.go`,
    `server/admin_maturity.go`.
- **Known limitations recorded** (honest behavioral notes):
  - `ROUTING_SMART`/`TokenMaxConcurrent` knob surface stays in config/healthz
    but the MASQ walk is the live engine — the smart-routing scorer and
    `AUTO_ROTATE_ON_EXHAUSTION` ordering live on the (kept, dead-callable for
    tests) `acquire_order.go` engine and are dormant until re-ported onto
    the spill walk (tracked in `session.md`).
  - Upstream excised its bounded-cooldown classifier windows (#621): ours'
    `COOLDOWN_FANOUT_MS`/`OPAQUE_MS`/`LOADSHED_MS`/`PEAK_HOURS_MS` knobs no
    longer retune the upstream classifier (its `SetCooldownTuning` is a
    documented no-op); `COOLDOWN_DEFAULT_MS`/`COUNTRY_BLOCK_MS`/`CEILING_MS`/
    `IP_MAX_READMITS`/`IP_JITTER_RATIO`/`SESSION_PARK_*`/`SESSION_POLL_MAX_MS`/
    `MATURITY_BACKOFF_MS`/`SMART_PROBE_BACKOFF_MAX_MS` remain live on the
    runs/pool/session enforcement points.
  - Pre-existing failures on our own branch reproduced unchanged at pristine
    `36a19168` (not caused by this merge): 8 `session` store tests
    (`TestStoreReadErrorDoesNotClobberFile*`, `TestStorePendingMutation*`,
    `TestLegacyFileImportsOnceThenArchives`, `TestLegacyImportIdentical*`,
    `TestResumePersistedOnRestart`, `TestStoreVersionMismatchIgnoredThenReplaced`).
    The server-suite rotating flake (`TestConcurrentReloadAndChat`) passes
    solo x3.

### Added
- Upstream #606–#645 adoption: MASQ slot/spill engine (`spill_order.go`,
  `spill_queue.go`, `slot_ledger.go`), `PIN_MODEL` + `pin_skips`,
  `ProbeAllTokens` zero-cost probe-all (server handler + `RenderProbeAllResults`),
  precious-keep drop-session reporting (`RenderDropSessionResult`),
  `cooldown_hint.go` terminal-hint + bridge-survivor persistence,
  `SLOTS_PER_ACCOUNT`/`MAX_SPILL_ACCOUNTS` knobs, hardened .gitignore
  secret patterns, refreshed frontend bundle + admin manifest (68 rows),
  extra e2e/mock suites.

### Preserved
- SQLite token database with `session_state` table for session persistence
  (`SESSION_PERSIST`, Phase 4 DB durability) — untouched by the merge.
- Token state store (locks, quarantines, cooldowns, spend/usage ledgers)
  with `RestoreTokenState` at boot; pool_state pool-side quota-cache writer +
  ledger/admission restore (`RestorePoolPersist` from `Pool.Start`) plus the
  new cooldown-hint/survivor restore; DB settings overlay (ADR-0019) with
  the `config:migrated_env_v1` marker.
- Health scoring suite (`HEALTH_SCORE_ENABLED`), background token health
  probes (`TOKEN_HEALTH_PROBES`), bridge circuit breaker observability
  (`BreakerSnapshot` → /healthz + /metrics), per-token bridge rate limiting,
  refund tracking (`lastRefund`/`pendingRefund`) and the refund-refresh
  route.
- Quota auto-probe scheduler (ADR-0022) + ADR-0024 boot seed; maturity
  automation with the DB maturity blob (MaturityStore) contract unchanged;
  session-park gate, ip_capped daily budget + jitter, cooldown-tuning knobs
  (runs/pool side).

### Technical Details
- Merge verified: all 25 conflict paths resolved; `go build ./backend/...`
  green, `go vet ./backend/...` clean, `gofmt` clean; hermetic tests
  (`env -u AUTH_TOKENS -u ADMIN_TOKEN go test -count=1`): `config` ok,
  `store` ok, `pool` ok (full suite incl. re-ported model-locks/random/
  limited-ip/cooldown tuning/maturity snapshots), `session` shows only the
  8 pre-existing failures (see above), `server` ok, `dashboard` ok (manifest
  parity incl. restored maturity rows), `cli` ok. Frontend: `svelte-check`
  0 errors; `dist` rebuilt from the merged `frontend/src` (per AGENTS.md).
  Noted in `session.md`.

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
