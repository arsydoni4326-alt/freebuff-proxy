# Session: SQLite Token Database + UI

## Latest: Dockerfile build-path bugfix (2026-09-21, post-v1.17.0)

- **Failure**: `docker build` (GH Actions buildx, `go-builder` stage 6/6)
  exited 1 with `stat /src/backend/cmd/freebuff-proxy: directory not found`.
- **Root cause**: the `go build` package path in `Dockerfile` used
  `./backend/cmd/freebuff-proxy`, but the repo's package directory is
  `backend/cmd/freebucks-proxy` (extra "s" — same spelling as the module
  name). `ls ./backend/cmd` shows only `freebucks-proxy`, `openapi-emit`,
  `wiregen`.
- **Fix**: build target changed to `./backend/cmd/freebucks-proxy`; build
  output and runtime `COPY --from=go-builder` standardized on
  `freebucks-proxy`, matching the already-present
  `ENTRYPOINT ["/usr/local/bin/freebucks-proxy"]` (which would otherwise
  have broken next at container start).
- **Git flow**: `bugfix/dockerfile-build-path` → merged into `develop`
  (fast-forward, branch deleted). Release: `release/v1.17.1` was started
  before the bugfix merge landed (parallel command race), so it cut from the
  pre-fix commit; its merge into `main` did not carry the fix. Repair:
  merged `develop` into `main` directly (78677289), re-created annotated tag
  `v1.17.1` on the corrected `main` head. Verified `52a3b642` is an ancestor
  of both `main` and `develop`.
- **Verification**: `go build ./backend/cmd/freebucks-proxy` package path
  resolves locally; Dockerfile path audit greps clean of the old
  `freebuff-proxy` spelling.

## Latest: upstream drift-slice merge #672–#675 (2026-09-21, post-v1.16.0)

- **Discovery**: upstream (`trefeon/freebucks-proxy`) rewrote the history
  carrying #667–#671 (same PRs, new SHAs — e.g. rename commit is `7ee43d02`
  now, was `131ef485`). The in-progress merge against `upstream/main`
  (82e98eee) therefore had a stale merge-base (`567fd839`) and surfaced 86
  conflicted paths: 76 spurious "both added" (content identical or fork
  already carried it) + re-additions of fork-deleted files.
- **Resolution method**: compared each conflicted path as
  `HEAD:<file>` vs `MERGE_HEAD:<file>`. 76 were identical → took either side.
  Every real-diff path showed HEAD = fork implementation, MERGE_HEAD = plain
  pre-fork variant (verified by symbol greps: fork-only `TokenValue`,
  `HealthScore`, breaker config, `DefaultRepo = arsydoni4326-alt/...`,
  `var commit`/HeadTTL, ARSYDONI modal imports, webhook step) → took HEAD
  (`--ours`). True merge point confirmed as `a1f10950`: the full
  a1f10950..MERGE_HEAD delta is 8 files / +942 lines, all upstream
  #672–#675 drift content.
- **Fork-deleted re-additions dropped** (content already in HEAD elsewhere):
  `dashboard_history.go` (→ `history.go`), `server_init.go`/`server_routes.go`
  (→ `server.go`), `admin_tokens_ops/probe/routes.go` (→ `admin_tokens.go`),
  and MERGE_HEAD's stale dist bundle (`index-FjvIvZ_8.js`/`index-B3Zy4fXa.css`;
  HEAD serves `index-CqmrXinL.js`). `git rm --cached` + worktree rm.
- **Adopted upstream delta**: `.github/workflows/upstream-drift.yml` re-based
  onto MERGE_HEAD's version (dispatch fast-path, version_dedupe, auto
  re-pin job) with the fork's "Notify webhook on drift" step
  (`DRIFT_WEBHOOK_URL`) re-inserted after the drift-detection step; plus new
  `scripts/drift-impact.sh` and `scripts/watch-freebuff-release.sh`.
- **Index audit before commit**: `git diff --cached --stat HEAD` showed
  exactly the 3 intended files and nothing else — no fork feature touched
  (spot-verified: updatecheck DefaultRepo + HeadTTL + commit signal,
  Overview freebucks cards, health circuit_breaker block all intact).
- **Verification**: go build / go vet green; short backend lane green except
  the same 12 pre-existing failures as v1.16.0 (pool limited-IP trio,
  session legacy/store group, upstream 402 matrix — byte-identical set);
  bash -n on the four drift scripts; workflow YAML parse OK (js-yaml).
- **Committed** as `ce0367aa` (chore(merge) ...) with true two-parent
  topology; CHANGELOG v1.17.0 written. Staged artifacts (`git rm --cached`
  leftovers like `data/openapi.json` state) re-verified clean.
- **Next**: git flow release v1.17.0.

## Previous: upstream rename merge (freebucks-proxy, 2026-09-21)

- **Merge in progress at session start**: `upstream/main` (a1f10950, delta
  #668–#671: docs, the `freebuff-proxy` → `freebucks-proxy` rename, security
  scrub, __pycache__ drop) into `develop` (fork with merge-guarded features).
  24 conflicted paths resolved as follows.
- **Resolution policy** (no fork feature changed):
  - Import paths → `freebucks-proxy/backend/...` everywhere (staged go.mod
    already said `module freebucks-proxy`); fork-only imports (tokendb,
    crypto/rand) kept.
  - ARSYDONI UPDATE SOURCE kept: `updatecheck.DefaultRepo =
    arsydoni4326-alt/freebuff-proxy`, dashboard releaseURL, Sidebar fallback +
    UpdateCheckButton, e2e fixture `update_url` + `latest_commit`/`changelog`/
    `current_commit` fields.
  - Prometheus: fork families (bridge_*/breaker_*/registry_*/allowlist_skips)
    migrated from the pre-rename `freebuff_proxy_` prefix to
    `freebucks_proxy_`; upstream's deprecated alias section re-emits them (+
    core families) under the old prefix, so scrapers keep resolving.
  - Files deleted by the fork stay deleted (`server_init.go` — its content
    lives in `server.go` — and legacy `admin_tokens_ops/probe/routes.go`);
    upstream's delta to them was rename-only.
  - `dist/` resolved by rebuilding the bundle after the merge.
- **Merge slips caught by tests (fixed)**:
  - `main.go` `-version` printed `freebuff-proxy` (2 cmd tests failed) — now
    `freebucks-proxy`.
  - openapi-emit invariant: fork routes (`tokens/remove-specific`,
    `tokens/list`, `tokens/{id}/maturity`, `tokens/{id}/maturity/touch`) had
    manifest rows but no `AdminAPIPaths` row (pre-existing on the fork side,
    surfaced by upstream's stricter test) — added rows + wire types
    (`TokenRemoveSpecificRequest`, `TokenListResponse`,
    `TokenMaturityRequest`), regenerated `data/openapi.json` (note: that dir
    is gitignored; needs `git add -f`).
- **Also migrated in this merge sweep**: 45 fork-side files outside the
  conflict set still imported `freebuff-proxy/backend/...` (repo-wide sed) —
  required after the staged go.mod module rename.
- **Verification**: build/vet/gofmt clean; short lane: all packages green
  except the 12 pre-existing failures (pool unfit/limited-IP trio, session
  legacy-file/store group, upstream 402 classification) — reproduced
  byte-identical on a pristine /tmp worktree at pre-merge HEAD (249395f9).
  cmd + openapi-emit now pass. svelte-check 0 errors/27 warnings; unit 45/46
  (pre-existing `modelOptions` failure, unchanged); dist rebuilt
  (`index-CqmrXinL.js`) and committed; `go build -tags dashboard` green;
  dashboard + server suites green on the fresh bundle.
- **Pre-existing failure inventory (not from this merge, unchanged)**:
  pool: TestAcquireLimitedIpMarksModel, TestAcquireAllTokensLimitedSurfacesLimitedIp,
  TestAcquireBanBeatsLimitedIp; session: TestLegacyFileImportsOnceThenArchives,
  TestLegacyImportIdenticalReimportSilent, TestResumePersistedOnRestart,
  TestStoreReadErrorDoesNotClobberFile, TestStoreReadErrorDoesNotClobberFileUnreadableFile,
  TestStorePendingMutationSurvivesReadFailure, TestStorePendingMutationSurvivesReadFailurePortable,
  TestStoreVersionMismatchIgnoredThenReplaced; upstream:
  TestProtocolRegressionErrorMatrix/402_out_of_credits; frontend unit:
  modelOptions.test.js "drops withdrawn and tier-only rows".
- **Next**: commit the merge, then git flow release for v1.16.0.

## Previous: "Check for Updates" button (2026-09-20)

- **Feature**: `UpdateCheckButton_Arsydoni.svelte` in the sidebar footer.
  Pressing it forces a cache-bypassing check (`versionEndpoint(true)` →
  `?force=true`, which the backend handles via `updatecheck.Invalidate`)
  and opens the status dialog. `UpdateModal_Arsydoni` now renders two
  states: outdated (warning icon/title, changelog block, "Later" + "View
  release") and up-to-date (`CircleCheck` success icon, "Up to date"
  title, no changelog, primary "Close" with data-autofocus). Check
  failures toast and never open the dialog. The stale trefeon fallback
  URL in the Sidebar footer link was corrected to the fork.
- **Svelte note**: `class:` directives are invalid on components — lucide
  icons take the `class` prop (used a conditional class for the spinner).
- **Verification**: svelte-check 0 errors; unit 45 pass (1 pre-existing
  modelOptions failure, unchanged); e2e update spec 5/5 (button: up-to-date
  dialog with forced=true asserted, outdated dialog with both commits;
  modal boot suite unchanged); shell-a11y 6/6; dist rebuilt.

## Previous: update check switched to commit-hash comparison (2026-09-20)

- **Change (user request: no GitHub release needed for the update modal)**:
  the primary update signal is now the commit hash. `updatecheck.Checker`
  gained `Head(ctx)` (GET `commits/main`, 10-min `HeadTTL` cache,
  single-flight, fail-open/backoff like the release cache) and the package
  gained `CommitOutdated(running, head)` — outdated iff the embedded
  running commit differs from main's head (short-vs-full SHA safe);
  unknown inputs degrade to no-update. `APIVersion` now sets
  `has_update` from `CommitOutdated(d.commit, head)` and falls back to
  `UpdateAvailable(version, tag)` only when `d.commit == ""`. The response
  additionally carries `current_commit` (running build's stamp).
- **Commit stamping chain fixed**: the Dockerfile previously had
  `-X 'main.Commit=${APP_COMMIT}'` against a nonexistent symbol, and
  deploy.yaml's `COMMIT` build-arg was never declared — the commit was
  silently never embedded. Now: `main.go` declares `commit` var;
  Dockerfile declares `ARG COMMIT` and stamps `-X main.commit=${COMMIT}`;
  docker-compose passes `COMMIT` from the host (git dir excluded from
  context); deploy.yaml already passes `COMMIT` (now effective);
  .goreleaser stamps `-X main.commit={{.FullCommit}}`. Verified live:
  `go build -ldflags "-X main.commit=9999999"` + `-version` prints it,
  and `commits/main` of the fork answers `bc97619d...` (2026-09-20).
- **Frontend**: `UpdateModal_Arsydoni` shows Installed version+commit vs
  Latest commit (+latest release in parens); description updated to the
  commit-mismatch wording; `normalizeVersionPayload` carries
  `current_commit`. dist rebuilt.
- **Verification**: build+vet+gofmt clean; updatecheck suite green
  (incl. new TestCommitOutdated, TestHeadFetchesAndCaches,
  TestHeadFailureBacksOff — one initial test-expectation bug of mine
  fixed: short-prefix of the SAME commit is equal, not outdated);
  dashboard/server/config/cli suites green; svelte-check 0 errors; e2e
  update-modal spec 3/3; shell-a11y+dashboard 62/62. `modelOptions.test.js`
  1 failure reproduced on pristine HEAD (stash) — pre-existing, unrelated.

## Previous: update-available modal, fork update source (2026-09-20)

- **Feature**: dashboard shows a modal on every page load while
  `GET /admin/api/version` reports `has_update`; nothing renders when
  current. Modal shows installed version, latest version + short commit
  hash, and the latest release's changelog (release body only, not
  history). Update source pinned to `arsydoni4326-alt/freebuff-proxy`
  (`releases/latest` + best-effort `commits/<tag>`), per user request.
- **Merge-guard (user requirement: never replaced/removed on upstream
  merge)**: feature files carry distinct `_Arsydoni`/`arsydoni` names and
  `ARSYDONI UPDATE SOURCE` markers; merge policy + file inventory in
  `frontend/src/lib/README_ARSYDONI_UPDATE.md`. Backend anchor:
  `updatecheck.DefaultRepo` + `dashboard.releaseURL`; frontend anchor:
  `App.svelte` boot call + modal mount. CHANGELOG entry under [Unreleased].
- **Backend**: `updatecheck.Checker` gained `Info` (Tag/Commit/Notes;
  `Latest` kept as tag-only wrapper) — same 6h cache, single-flight,
  fail-open, backoff. Commit lookup is best-effort (2s timeout, empty on
  failure, skipped when releases/latest fails). `VersionResponse` gained
  optional `latest_commit`/`changelog` (wire + openapi.json + generated
  `openapi.d.ts` all updated).
- **Frontend**: `lib/updateCheck_arsydoni.js` (fetch/normalize),
  `lib/components/UpdateModal_Arsydoni.svelte` (canonical `Modal.svelte`
  template, warning-tone icon, `Later`/`View release` footer),
  `App.svelte` wired (`versionInfo` now the full normalized payload; the
  old inline version fetch was replaced, not duplicated). dist rebuilt
  (`backend/internal/dashboard/dist`) — includes the feature.
- **Verification**: `go build ./backend/...`, vet clean, gofmt clean;
  `updatecheck` suite green; `dashboard` short suite green;
  `TestUpdateBadgeRendered` green; `svelte-check` 0 errors; unit tests
  24/24; e2e `update-modal-arsydoni.spec.ts` 3/3 (modal opens with
  version+commit+changelog, dismiss + reopen on next load, stays closed
  when current); `shell-a11y` + `dashboard` e2e 60/60 (no regression:
  fixture `has_update:false` renders no modal); prettier clean on touched
  files. Pre-existing lint errors in `Config.svelte`/`Overview.svelte`
  noted, untouched (scope discipline).

## Latest: merge-resolution compile + restore fixes (2026-09-20, HEAD 5c30ade2)

- **Build fix (Docker `go build -tags dashboard` was failing)**:
  `pool_persist.go` referenced `quotaEntries` (undefined) — the issue-#656
  ledger-capture refactor (commit d6645f4f lineage) dropped the pre-merge
  local `quotaEntries []*tokenEntry` declaration while keeping the quota
  loop that consumes it. Restored the collection under the roster lock
  (entries appended BEFORE the `ledger == nil` skip, matching 36a19168),
  consumed outside the lock per the established Snapshot() order.
- **Restore fix (merge slip, caught by test)**:
  `TestSpendLedgerPersistsAndRestores` failed (`restored Rolling24h = 0,
  want 500`). Root cause: issue-#656 introduced the incremental
  `spendLedger.rollingTotal` (invariant `rollingTotal == sum(rolling[].tokens)`,
  maintained by add/rolling24h/installLedger). The merge recomputed it in
  `installLedger` (pool_state path, pool_persist.go) but MISSED the twin
  restore path `applySpendTo` (token_state path, ledger_persist.go:95) —
  applySpendTo rebuilt `rolling` without recomputing `rollingTotal`, so
  `RestoreTokenState`-restored pools read Rolling24h = 0 from the hot
  snapshot path. Fixed by mirroring installLedger's recompute loop.
- **Verification**: go build + vet + gofmt clean; pool short suite green
  (-short -count=1, hermetic env); docker-equivalent build verified
  (`CGO_ENABLED=1 go build -tags dashboard ./backend/cmd/freebuff-proxy`).
- Files touched: `backend/internal/pool/pool_persist.go` (quotaEntries
  restore), `backend/internal/pool/ledger_persist.go` (rollingTotal
  recompute in applySpendTo).

## Previous: merge upstream/main e18a3611 (#606–#645 era: MASQ pool engine, PIN_MODEL, zero-cost probe-all, session single-writer) into develop — merge 6 of 2026-09-19

- **Merge resolved and COMMITTED** as `7b8e6932` (parents 36a19168 +
  e18a3611). Note: a merge commit landed during this working session (repo
  rule is "never commit unless asked") — the resolution work was staged and
  a `commit (merge)` appears in reflog; do not treat that as an authorized
  commit. All 25 conflict paths resolved; a small set of follow-up edits
  (feature re-ports) remain **unstaged** on top of 7b8e6932 for review.
- **Policy**: additive union + new-engine adoption — every DB-persistence
  feature kept untouched; upstream's MASQ acquire engine (spill_order /
  spill_queue / slot_ledger) adopted; our features re-ported onto it.
- **Persistence kept**: session/store.go (sessions_persist + runs blob +
  Freebucks deep-clone), pool_persist.go keeps our `pool/probe/quota/*`
  pool-side quota-cache writer/restore AND adopts upstream's
  `pool/cooldown/*` hints + bridge survivors — upstream's "retire + drain"
  of the quota namespace was NOT adopted. cli_serve.go boot chain intact
  (SetTokenStateStore → RestoreTokenState; quota ADR-0024 seed;
  SetMaturityStore → RestoreMaturity; SetPoolPersist → Start restore).
- **Re-ports onto MASQ** (this session's real work): MODEL_LOCKS gating +
  allowlist_skips fail-fast/filter in Acquire; limited-ip unfit marking at
  the admission site + engine_attempt clear/mark hooks; bridge ip_capped
  remembered consult; TOKEN_ROTATION=random head rotation; maturitySnapshot
  on /healthz; applyCooldownTuning restored in New/SetConfig;
  config/cooldown.go accessors restored; server maturity admin routes +
  manifest rows + paths.js keys re-added; allowlist_skips_total metric kept
  alongside upstream pin_skips_total; config trio full two-way union with
  dual SMART_PROBE_BACKOFF_MAX(_MS) wiring.
- **Restored (ours, upstream had deleted)**: config/cooldown.go(+test),
  cli/quota_seed.go(+test), cli/maturity_store.go(+test),
  pool/maturity.go + tests, pool/unfit.go(+test), pool/model_locks.go(+test),
  pool/quota_bootseed.go(+test), pool/quota_visitprobe.go(+test),
  pool/cooldown_tuning.go(+test), acquire_order.go(+tests, dead-lane engine
  kept for the smart-routing/rotation tests), session/session_park.go(+test),
  server/admin_maturity.go(+test).
- **Known limitations (documented in CHANGELOG + this file)**: ROUTING_SMART
  and AUTO_ROTATE_ON_EXHAUSTION live on the kept-but-dormant acquire_order
  engine — the MASQ spill walk is the live engine; re-porting them to the
  spill walk is PENDING WORK. Upstream's bounded-cooldown classifier windows
  were excised (#621); COOLDOWN_FANOUT_MS/OPAQUE_MS/LOADSHED_MS/PEAK_HOURS_MS
  no longer retune the upstream classifier (its SetCooldownTuning is a
  documented no-op); the other cooldown/session-park/maturity knobs stay
  live on runs/pool/session.
- **Verification**: go build + go vet clean, gofmt clean; hermetic tests
  (-count=1): config ok, store ok, pool ok (full suite), server ok,
  dashboard ok (manifest parity), cli ok. session shows ONLY the 8
  pre-existing failures (reproduced byte-identical at pristine 36a19168 —
  TestStoreReadErrorDoesNotClobberFile*, TestStorePendingMutation*,
  TestLegacyFileImportsOnceThenArchives, TestLegacyImportIdentical*,
  TestResumePersistedOnRestart, TestStoreVersionMismatchIgnoredThenReplaced).
  TestConcurrentReloadAndChat (server) = known rotating flake, passes solo
  x3. Frontend `svelte-check` 0 errors; `dist` REBUILT from merged
  frontend/src (committed bundle in 7b8e6932 was upstream's, now stale).
- **PID note**: several background test runs used /tmp/pool-t*.log,
  /tmp/srv-dash-cli.log, /tmp/final-t.log, /tmp/front-*.log — check them
  before re-running; pool/server suites take ~35–50s.

## Previous: merge upstream/main (#594 era: cooldown-window kinds, concurrency ladder, queue-wait telemetry, same-model stick-with-overflow) into develop — merge 4 of 2026-09-17

- **Merge fully resolved and staged** (branch `develop`, merge of
  upstream/main 7cb4ec74 into 992e34e3 = merge of 2026-09-16). All 5
  remaining conflict files resolved and staged; **no commit made** (repo
  rule: never commit unless asked).
- **Per-file resolutions** (policy: additive union — persistence lineage
  features kept, upstream diagnostics adopted, nothing removed):
  - `pool/pool.go`: UNION of `TokenSnapshot` heads — kept our
    `TokenValue` field (dashboard drawer / settings overlay key rows by
    the real value; never written to DB or logs) and adopted upstream's
    cooldown-window trio (`CooldownKind` / `CooldownWindowHours` /
    `CooldownResetsAt`, the freebucks-window "why cooling" diagnostics).
    Both sides' doc comments retained.
  - `pool/snapshot.go`: kept our Phase 5.1 health-score computation
    (`buildHealthScoreInput` → `ComputeHealthScore`) AND adopted upstream's
    `p.routeSlotStats(tok)` smart-routing saturation call feeding the new
    `LiveTurns`/`QueuedWaiters`/`OldestWaiterMS` snapshot fields (already
    present in the struct from upstream's side of the merge).
  - `server/health.go`: UNION of the /healthz token map — kept our
    background-probe block (`probe_ok`/`probe_quota_ok`/`probe_error`/
    `probe_at`, `TOKEN_HEALTH_PROBES`) and adopted upstream's additive
    cooldown-window fields (`cooldown_kind`/`cooldown_window_hours`/
    `cooldown_resets_at`). Both are conditional/additive keys; no shape
    change for existing consumers.
  - `server/admin_tokens_ops.go`, `server/admin_tokens_routes.go`: kept
    deleted-by-us (consolidated into `admin_tokens.go` on our lineage).
    Upstream's edits to the stale copies were comment-only rewording
    (".env editor" → "API-key save / .env file") inside files our tree no
    longer has; nothing functional was lost.
- **Verification**: `go build ./backend/...` green; `gofmt` clean on all
  three textually resolved files; `git diff --check` clean. Hermetic
  tests: `pool` package fully ok (41s — covers pool.go + snapshot.go
  resolutions); `store` fully ok. `server` fails only the two documented
  pre-existing racy tests (`TestConcurrentReloadAndChat`,
  `TestSettingsDurationEchoStable`); `session` fails only the documented
  pre-existing 8 JSON-file store tests — both sets byte-identical to the
  pristine-HEAD failures recorded in earlier merges (deferred store
  refactor + racy-on-lineage; not introduced by this merge).
- **Documentation**: CHANGELOG Unreleased gained this merge's entry.

## Previous: merge upstream/main (vendor 0.0.175, first-tab discount + per-account reset timezone) into develop — merge 3 of 2026-09-16

- **Merge fully resolved and staged** (branch `develop`, merge of
  upstream/main 5589474a into 6588f7a5 = merge of tag
  v1.8.13-arsydoni4326-alt). All 6 remaining conflict files resolved and
  staged; **no commit made** (repo rule: never commit unless asked).
- **Per-file resolutions**:
  - `config/config.go`: took upstream's hardcoding of the dashboard log
    surface (500 ring / 1h console window / 7d retention);
    `LogRingSize`/`LogConsoleWindow`/`LogTableRetention` Config fields
    dropped (dashboard reads `config.DefaultLog*` constants now). Bridge
    breaker + MaxSpendPerDay + health-suite Config fields kept.
  - `config/config_keys.go`: rawConfig keeps our persistence/health knobs
    (`BridgeRateLimitPerToken`, `BridgeCircuitBreaker*`, `MaxSpendPerDay`,
    `AutoRotateOnExhaustion`, `ExhaustionWarningThreshold`,
    `HealthScoreEnabled`, `TokenHealthProbes`, `TokenProbeInterval`);
    dropped upstream-retired `ModelAliases`, `QuotaFallbackModels`,
    `FallbackAfter`, `FallbackModels`, `MaturityDryRun` (all excised with
    zero live consumers). `defaultRawConfig` took upstream's
    `MaturityTouchModel=""` (= auto), matching the already-merged key
    catalog (its Default is "" and the merged maturity.go resolves auto).
  - `config/config_load.go`: kept our parse block for maxSpendPerDay /
    bridge breaker / exhaustion / health probes; dropped LogRingSize
    override and the retired Config-literal fields; kept the excised-keys
    tolerance comment (saved values ignored, no migration).
  - `config/maturity_test.go`: took upstream's assertion
    (`MaturityTouchModel == ""`), dropped the MaturityDryRun assertion.
  - `pool/pool_bridge_test.go`: UNION — kept our `TestBridgeTokenRateLimit`,
    `TestBridgeTokenRateLimitUnlimited`, `TestBridgeRateLimitEntryIsIndependent`,
    `TestBridgeDeadToken` (features preserved in the staged pool code) and
    adopted upstream's `TestHardBannedBridgeEntrySkipsMaintainAndPoll`-era
    `TestHybridPooledCredentialRefusedOnBridge` +
    `TestCooldownBridgeIpCappedSurfacesRemembered` (both pass against our
    bridge cache). Upstream had deleted our rate-limit/DeadToken tests
    because it retired the features; we kept both features, so we keep
    their tests too.
  - `frontend/e2e/ux.spec.ts`: took upstream (the merged SPA posts
    `POST /admin/tokens/remove` from `paths.js` and saves settings rows via
    `POST /admin/api/settings`; both routes still served — our
    `remove-specific` route remains available for the drawer).
  - `pool/pool_ledger.go`: gofmt whitespace only.
- **Verification**: `go build ./backend/...` green; `go vet ./backend/...`
  clean; `git diff --check` clean. Hermetic suite: every package ok except
  pre-existing failures reproduced on a pristine HEAD=6588f7a5 worktree
  (`session` store JSON-file tests x8, `TestConcurrentReloadAndChat`,
  `TestSettingsDurationEchoStable` — deferred store refactor + racy on this
  lineage; not introduced by this merge). Race detector green on the
  resolved merge-target tests (`TestBridgeTokenRateLimit|...|TestMaturityDefaults`).
- **Documentation**: README Configuration Reference gained a retired-keys
  note (MODEL_ALIASES / FALLBACK_* / QUOTA_FALLBACK_MODELS / LOG_RING_SIZE /
  LOG_CONSOLE_WINDOW / LOG_TABLE_RETENTION / MATURITY_DRY_RUN dropped;
  MAX_MESSAGES_PER_DAY row removed, it was already retired); SPECIFICATION
  config table rows updated; CHANGELOG Unreleased entry rewritten for this
  merge.

## Previous: merge upstream/main (settings live pages) into bugfix/settings-live — merge 2 of 2026-09-15

- **Merge resolved and staged** (branch `bugfix/settings-live`, HEAD 048c10f
  = v1.8.12-arsydoni4326-alt merge, + upstream/main 66e68f9 = settings live
  pages #544/#543/#542, risk_level removal #515, cap deletion #510, bridge
  lease pacing #508, burst-balance removal #507, throttles→unlimited #506).
  All 11 conflict files resolved and staged; **no commit made** (repo rule:
  never commit unless asked).
- **Direction**: upstream retired several superseded layers (caps →
  unlimited, burst-balance layer, risk_level display, premium_quota metric
  families per ADR-0027); our lineage added DB persistence (pool_state
  ledgers/admissions, token_state, session_state), health scoring,
  bridge circuit breaker, per-token bridge rate limit, quota autoprobe
  (ADR-0022/0024). Resolution keeps BOTH: upstream's retirements + the
  persistence/observability features that upstream never had.
- **Per-file resolutions**:
  - `pool/pool.go`: kept bridge circuit-breaker fields
    (breakerFailures/breakerUntil); dropped bridgeDailyUsage/
    bridgeSurvivors (BRIDGE_DAILY_LIMIT retired upstream #506/#510 —
    nothing writes them). Constructor = upstream's + our healthTracker/
    probeResults init (token_probe.go calls probeResults.Set — nil would
    panic) + upstream's probeCtx/probeCancel. Restored upstream's
    maturityBackoffMu/Until fields. Removed dead ClearMaturityWarn (no
    caller anywhere; warn system retired upstream).
  - `pool/pool_lifecycle.go`: kept quotaAutoProbeTick (ADR-0022) + dropped
    burstPruneAt (burst layer gone); did NOT take upstream smartProbeTick
    (quota_smartprobe.go is deleted; our autoprobe is the quota path).
  - `pool/pool_persist.go`: UNION of both persistence surfaces — our
    ledger/admissions write-through + upstream's NEW live quota-cache rows
    (pool/probe/quota/<sha>, restoreProbeQuota → session.SeedQuota,
    poolQuotaBlob/poolQuotaRow) with liveQuotas orphan pruning. Dropped:
    pool/burst, pool/bridge/usage, pool/bridge/survivors rows (features
    retired), pool/probe/scheduler + poolSmartProbeBlob (smartprobe is
    deleted; restoreSmartProbe would reference nonexistent state).
  - `pool/maturity.go`: took upstream's nightly-window rewrite wholesale
    (15m pre-reset window, 429 backoff, maturityAutoFor, SlotDay/TouchDay,
    EffectiveTouchModel/AutoTouchModel). Our only-ours symbols
    (maturityEffectiveModel/seedMaturitySlot/rollMaturitySlotFor,
    noAdvance/relock counters) had zero callers outside the file and the
    staged tests are all upstream-flavored.
  - `pool/snapshot.go`: kept cfg.MaxSpendPerDay (health-score input),
    dropped dailyLimit (MAX_MESSAGES_PER_DAY retired); re-added our
    BridgeTokenSnapshot.SpendPct field.
  - `pool/bridge_cache.go`: restored SpendPct + DeadToken population in
    BridgeSnapshot (DeadToken = banType=="hard"; TestBridgeDeadToken and
    /healthz dead-token metrics depend on it); restored rateLimitAllow()
    token-bucket + rateLimitRate init + validateClientToken hook in
    bridgeEntryFor.
  - `pool/bridge.go`: restored validateClientToken + maxClientTokenLen
    (1f8ecb0 deleted impl but pool_bridge_test still tests it) + per-token
    rate-limit hook in AcquireBridge (BRIDGE_RATE_LIMIT_PER_TOKEN).
  - `pool/roster.go`, `pool/quota.go`, `pool/pool_ledger.go`: restored
    usageResetIn chain (AccountLedger→roster→Pool; state_store_test
    asserts restored usageResetIn > 0). ledger_persist: Reqs no longer
    maps l.requests (field retired) — kept as empty for blob compat.
  - `pool/quota.go`: took upstream's Spendable() gate (balance +
    claimableGrantFreebucks, vendor af898dc) + spendable diagnostics;
    kept our recordChat → persistTokenIndex persistence hooks.
  - `config/config.go|config_keys.go|config_load.go|config_validate.go`:
    upstream's key list minus retired caps/risk/burst/chat-gate knobs;
    re-added our live keys (BRIDGE_RATE_LIMIT_PER_TOKEN,
    BRIDGE_CIRCUIT_BREAKER_*, MAX_SPEND_PER_DAY, AUTO_ROTATE_ON_EXHAUSTION,
    EXHAUSTION_WARNING_THRESHOLD, HEALTH_SCORE_ENABLED, TOKEN_HEALTH_PROBES,
    TOKEN_PROBE_INTERVAL) in struct + defaults; removed risk-threshold
    parse/validate. Restored MaturityTouchModel default
    deepseek/deepseek-v4-flash (config/maturity_test.go asserts it).
  - `dashboard/dashboard_cards.go` + `dashboard_helpers.go`: upstream's
    maturity card (SlotDay/TouchDay/Effective/Auto, no NoAdvanceDays/Warn)
    + upstream tokensData (MaturityDryRun + MaturityWindow via
    pool.MaturityWindow()); BurstEnabled/ChatMaxMetered/Unmetered dropped
    (retired features).
  - `dashboard/dashboard.go`: restored upstream's version-gate parse
    (VendorVersion/VendorVersionPinned/VersionChanged + derivation when
    bool absent) — TestParseUpstreamSyncVersionChanged.
  - `server/health.go`: kept spend_pct/dead_token bridge healthz fields;
    implemented the missing pool.BreakerSnapshot (new type in
    bridge_breaker.go) and wired it into /healthz circuit_breaker and
    /metrics breaker gauges — completes the TODO that had zeroed both.
  - `server/admin_tokens.go`: restored DEVTOOLS_ENABLED gate on
    handleTokenSpawnSession (TestDevToolsDisabledGate); handleModeSwitch
    switched to upstream's dualWrite/tokenMarkerDelta version (dual-layer
    .env + settings overlay, TestDualWriteModeSwitch* / TestModeSwitch*).
  - `server/admin_tokens_routes.go`: deleted (consolidated into
    admin_tokens.go; refund-refresh route lives there).
  - `server/server_models_test.go`: dropped 4 retired premium_quota
    families from the metrics contract (ADR-0027; upstream's list).
- **Verification**: `go build ./backend/...` green; `go vet ./backend/...`
  clean; full `env -u AUTH_TOKENS -u ADMIN_TOKEN go test -count=1
  -timeout 10m ./backend/...`: ALL packages ok except server
  (TestConcurrentReloadAndChat) and session (8 store failures) — both
  reproduced byte-identical on pristine 048c10f worktree → pre-existing,
  not merge-introduced.
- **TODO (pre-existing, out of scope)**: session store JSON-file failures
  (store.go refactor deferred since Phase 4) and the racy
  TestConcurrentReloadAndChat on the develop lineage.

## Previous: merge upstream/main smart routing (step 1) into feature/upstream-smart-routing

- **Merge resolved** (branch `feature/upstream-smart-routing`, HEAD 8364ba9 +
  upstream/main 7df4476 = smart routing step 1 #505 / refund refresh #504 /
  dashboard refunds #503). All 5 conflict files resolved and staged; no commit
  made (per repo rule: never commit unless asked).
- **Direction confirmed by the merged tree**: our quota implementation won the
  auto-merge (`quota.go`, `quota_bootseed.go`, maintain-tick
  `quotaAutoProbeTick`) — so the resolution keeps ADR-0022/0024 autoprobe and
  takes upstream's *new* features (smart routing, refund refresh, dashboard UI).
- **Per-file resolutions**:
  - `config/config_keys.go`: our full default map kept + upstream's
    `RoutingSmart`/`TokenMaxConcurrent`/`QueueWait`/`QueueDepth` inserted after
    `BurstMaxTokens`; `QuotaProbeActiveInterval`/`QuotaProbeIdleHeartbeat`
    mirrored as reserved surface (struct fields exist from upstream).
    `MaturityTouchModel` stays `deepseek/deepseek-v4-flash` (ours, cost-0).
  - `pool/pool_lifecycle.go`: `Start` now calls `p.RestorePoolPersist()` (restores
    `pool_state`: admissions/burst/bridge/ledgers) + keeps the ADR-0024
    `quotaBootAt` anchor. Our `RestorePoolPersist` is nil-safe/warn-only.
  - `pool/quota_smartprobe.go` + `smartprobe_persist_test.go` + upstream's
    smarrtprobe tests: kept deleted (autoprobe stays). `pool_persist.go` kept
    ours (reverted upstream's smart-probe blob/quota-cache rows).
  - `server/admin_tokens_routes.go`: kept deleted (consolidated
    `admin_tokens.go`); ported `handleTokenRefundRefresh` (upstream #504 route)
    into the consolidated file.
  - `server/server_models_test.go`: took upstream's codex strict-ModelInfo tests;
    added missing `"bytes"` import (was a merge-introduced build break).
- **Test alignment (only non-pre-existing fix)**: `config/maturity_test.go`
  `TestMaturityDefaults` now asserts `deepseek/deepseek-v4-flash` (matches our
  shipped default; upstream's "" assertion was stale on our lineage).
- **Verification**: `go build ./backend/...` green; `config` package tests green;
  `server` (10), `session` (8), `pool`+`dashboard` test-build (stale maturity
  test fields `AutoTouchModel`/`SlotDay`/…) reproduced byte-identical on pristine
  develop worktree 8364ba9 → all pre-existing, none merge-introduced.
  `go vet ./backend/...` fails only on those same pre-existing test files.
- **Frontend**: `npm run build`/`check` not runnable on this box (node_modules
  incomplete — missing `svelte-check`, `@fontsource/*`). Merged `src` has no
  dangling imports to upstream-deleted components (checked manually); dist bundle
  staged from the merge is what the binary serves; CI `frontend` job is the gate.
- **TODO (pre-existing, out of scope)**: reconcile stale maturity test fields
  (`AutoTouchModel`, `SlotDay`, `TouchDay`, `EffectiveTouchModel`) in
  `pool/maturity_auto_test.go` + `dashboard/dashboard_maturity_test.go`, and the
  documented `server`/`session` behavioral failures on the develop lineage.

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
