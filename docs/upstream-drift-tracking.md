# Upstream Drift Tracking

> Phase 4 of the freebuff-proxy hardening roadmap.
> This document describes the infrastructure, processes, and research plan for tracking
> and managing drift between the proxy's offline model registry and the upstream
> CodebuffAI/freebuff repository.

---

## Table of Contents

- [Overview](#overview)
- [Architecture](#architecture)
- [Pinned Upstream Files](#pinned-upstream-files)
- [Drift Detection Infrastructure](#drift-detection-infrastructure)
- [Runtime Self-Healing](#runtime-self-healing)
- [Drift Response Playbook](#drift-response-playbook)
- [CI/CD Integration](#cicd-integration)
- [Research Plan](#research-plan)
- [Testing](#testing)
- [References](#references)

---

## Overview

The freebuff-proxy reimplements approximately 99% of the FreeBuff/Codebuff CLI wire protocol.
Since the upstream protocol is **undocumented** and changes without notice, the proxy must
track upstream changes to avoid breaking.

### Key Challenge

> The upstream CodebuffAI/freebuff repository is the source of truth for model definitions,
> agent mappings, and configuration constants. The proxy maintains a local copy of these
> definitions for offline fallback, but this copy can drift out of sync with upstream.

### Mitigation Strategy

1. **Pinned snapshots**: Five upstream constant files are copied into
   `internal/registry/testdata/upstream/` and committed to the repository.
2. **Fallback parity test**: `TestFallbackParityWithPinnedUpstream` verifies that the
   hardcoded `fallbackAgents` and `fallbackRootByModel` tables match the pinned snapshots.
3. **Automated drift detection**: CI runs `check-upstream.sh` weekly to detect pin drift.
4. **Runtime self-healing**: The live registry refreshes from upstream at runtime.
5. **Sync scripts**: `sync-upstream.sh` / `sync-upstream.ps1` automate the refresh process.

---

## Architecture

```
┌─────────────────────────────────────────────────────────────┐
│                    Upstream (CodebuffAI/freebuff)            │
│  common/src/constants/                                       │
│  ├── free-agents.ts                                         │
│  ├── freebuff-model-ids.ts                                  │
│  ├── freebuff-models.ts                                     │
│  ├── gemini.ts                                              │
│  └── model-config.ts                                        │
└──────────────────────────┬──────────────────────────────────┘
                           │
                           │ sync-upstream.sh
                           │ (manual or CI)
                           ▼
┌─────────────────────────────────────────────────────────────┐
│              Pinned Snapshots (committed)                    │
│  internal/registry/testdata/upstream/                        │
└──────────────────────────┬──────────────────────────────────┘
                           │
                           │ TestFallbackParityWithPinnedUpstream
                           ▼
┌─────────────────────────────────────────────────────────────┐
│              Offline Fallback (compiled-in)                  │
│  internal/registry/registry.go                               │
│  ├── fallbackAgents []agentModels                           │
│  └── fallbackRootByModel map[string]string                  │
└──────────────────────────┬──────────────────────────────────┘
                           │
                           │ LoadFallback() (on startup)
                           ▼
┌─────────────────────────────────────────────────────────────┐
│              Runtime Registry (live)                         │
│  Refreshes from upstream periodically                        │
│  Self-heals if offline fallback is stale                     │
---

## Pinned Upstream Files

The five pinned files mirror `CodebuffAI/freebuff/common/src/constants/`:

| File | Purpose | Key Contents |
|---|---|---|
| `free-agents.ts` | Agent-model mappings | `FREE_MODE_AGENT_MODELS`, `FREEBUFF_ROOT_AGENT_ID_BY_MODEL` |
| `freebuff-model-ids.ts` | Model ID constants | Wire model IDs for all served models |
| `freebuff-models.ts` | Model metadata | Model names, descriptions, capabilities, tiers |
| `gemini.ts` | Gemini-specific config | Gemini model mappings and overrides |
| `model-config.ts` | Model configuration | Default models, fallbacks, quota policies |

### Source Files in Registry

```go
// internal/registry/registry.go
var sourceFiles = []string{
    "free-agents.ts",
    "freebuff-model-ids.ts",
    "freebuff-models.ts",
    "gemini.ts",
    "model-config.ts",
}
```

### Hardcoded Fallback Tables

When upstream sources are unreachable, the registry falls back to hardcoded tables:

```go
var fallbackAgents = []agentModels{
    {agent: "base2-free", models: []string{
        "minimax/minimax-m3",
        // ... more models
    }},
    // ... more agents
}

var fallbackRootByModel = map[string]string{
    "mimo/mimo-v2.5":                  "base2-free-mimo",
    "minimax/minimax-m3":              "base2-free-minimax-m3",
    // ... more mappings
}
```

**Critical**: These tables MUST be updated in sync with pinned snapshots.
`TestFallbackParityWithPinnedUpstream` enforces this.

---

## Drift Detection Infrastructure

### check-upstream.sh

Read-only drift detection script:

```bash
# Check drift without modifying files
bash scripts/check-upstream.sh

# Check against specific upstream ref
bash scripts/check-upstream.sh main
bash scripts/check-upstream.sh af9dea66711618a2d52e52e26d47a7173368e6b0
```

**Output**:
```
check-upstream: comparing pins against CodebuffAI/freebuff @ <sha> (ref: main)

FILE                       PINNED-SHA      VENDOR-SHA      STATUS
------------------------- --------------- --------------- ------
free-agents.ts             abc123def456    abc123def456    SAME
freebuff-model-ids.ts      789ghi012jkl    789ghi012jkl    SAME
freebuff-models.ts         345mno678pqr    345mno678pqr    SAME
gemini.ts                  901stu234vwx    901stu234vwx    SAME
model-config.ts            567yza890bcd    567yza890bcd    SAME

check-upstream: OK — all pinned files match <url> @ <sha>.
```

**Exit codes**: 0 = all same, 1 = drift detected, 2 = environment error.

### sync-upstream.sh

Full sync script (fetch, update, verify, test):

```bash
# Full sync: fetch upstream, copy changes, verify hashes, run tests
bash scripts/sync-upstream.sh

# Check drift only (no file modifications)
bash scripts/sync-upstream.sh --check

# Sync and run full test suite
bash scripts/sync-upstream.sh --test-all

# Sync specific upstream ref
bash scripts/sync-upstream.sh af9dea66711618a2d52e52e26d47a7173368e6b0
```

### Windows Support

```powershell
# PowerShell
.\scripts\sync-upstream.ps1
.\scripts\sync-upstream.ps1 -CheckOnly
.\scripts\sync-upstream.ps1 -TestAll

# Git Bash
"C:\Program Files\Git\bin\bash.exe" scripts/check-upstream.sh
```

---

## Runtime Self-Healing

The live registry refreshes from upstream sources at runtime (configurable via
`REGISTRY_REFRESH`, default: 6 hours). This means:

1. **Offline fallback staleness is temporary** — the live refresh self-heals.
2. **Pinned snapshots are a safety net** — they ensure the offline fallback path works.
3. **CI guards the offline path** — `upstream-drift` workflow ensures pinned snapshots
   don't silently go stale.

### Refresh Flow

```
Startup
  │
  ├─ LoadFallback() → hardcoded fallbackAgents/fallbackRootByModel
  │
  ├─ Start background refresh goroutine
  │     │
  │     ├─ Fetch from upstream sources
  │     ├─ Parse and validate
  │     └─ Update runtime registry
  │
  └─ Serve requests using best available registry state
```

---

## Drift Response Playbook

When drift is detected (CI red or manual check):

### Step 1: Identify Changed Files

```bash
bash scripts/check-upstream.sh
```

Note which files show `DRIFT` status.

### Step 2: Update Pinned Snapshots

```bash
bash scripts/sync-upstream.sh
```

This copies changed files from upstream into `internal/registry/testdata/upstream/`.

### Step 3: Update Fallback Tables

If upstream added/removed models or agents, update the hardcoded tables in
`internal/registry/registry.go`:

```go
// Update fallbackAgents
var fallbackAgents = []agentModels{
    // Add new agents or remove retired ones
}

// Update fallbackRootByModel
var fallbackRootByModel = map[string]string{
    // Add new model→root mappings
}
```

### Step 4: Verify Parity

```bash
env -u AUTH_TOKENS -u ADMIN_TOKEN go test ./internal/registry/...
```

### Step 5: Run Full Test Suite

```bash
env -u AUTH_TOKENS -u ADMIN_TOKEN go test ./...
```

### Step 6: Commit

```bash
git add internal/registry/testdata/upstream
git commit -m "chore(registry): sync pinned upstream models to vendor <sha>"
```

---

## CI/CD Integration

### Weekly Drift Check

The `upstream-drift` workflow runs weekly (Monday 06:17 UTC):

```yaml
# .github/workflows/upstream-drift.yml
name: upstream-drift

on:
  workflow_dispatch:
  schedule:
    - cron: '17 6 * * 1'

jobs:
  drift-check:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v7
      - name: Check pinned registry files vs CodebuffAI/freebuff@main
        run: bash scripts/check-upstream.sh main
```

### What Happens on Drift

1. CI job goes **red**.
2. Maintainers receive notification.
3. Follow the Drift Response Playbook.

### Runtime Self-Healing

Even if CI is red (pinned snapshots stale), the live registry continues to serve requests
using its runtime-refreshed state. The offline fallback is only used when upstream sources
are unreachable.

---

## Troubleshooting Guide (Phase 4.6)

### Common Sync Failures

#### 1. `check-upstream.sh` fails with "git not found"

**Symptom**: `check-upstream: error: git not found on PATH`

**Fix**: Install git — `sudo apt-get install git` (Debian/Ubuntu) or `brew install git` (macOS).

#### 2. `check-upstream.sh` fails with "need sha256sum"

**Symptom**: `check-upstream: error: need sha256sum or shasum on PATH`

**Fix**: Install coreutils — `sudo apt-get install coreutils` (Linux) or `xcode-select --install` (macOS).

#### 3. `sync-upstream.sh` fails to clone upstream

**Symptom**: `fatal: unable to access 'https://github.com/CodebuffAI/freebuff.git/': Could not resolve host: github.com`

**Fix**: Check DNS resolution (`nslookup github.com`) and network connectivity. If behind a corporate proxy, configure git:
```bash
git config --global http.proxy http://proxy.example.com:8080
git config --global https.proxy http://proxy.example.com:8080
```

#### 4. `TestFallbackParityWithPinnedUpstream` fails after sync

**Symptom**: `fallback agent "X" absent from pinned upstream`

**Cause**: Pinned snapshots updated but `fallbackAgents`/`fallbackRootByModel` in `internal/registry/registry.go` not updated to match.

**Fix**: Update the hardcoded tables in `registry.go` to match the new pinned snapshot, then re-run:
```bash
env -u AUTH_TOKENS -u ADMIN_TOKEN go test ./internal/registry/... -run TestFallbackParityWithPinnedUpstream
```

#### 5. DRIFT reported but files look identical (line ending issue)

**Fix**: Normalize line endings:
```bash
git config core.autocrlf false
cd internal/registry/testdata/upstream
git rm --cached *.ts && git add *.ts
```

#### 6. CI "cannot resolve ref" error

**Fix**: Delete the stale shallow clone and re-clone:
```bash
rm -rf ../freebuff-reference
bash scripts/check-upstream.sh main
```

---

## Rollback Procedures (Phase 4.6)

### Rolling Back a Bad Sync

If a sync introduced a regression (wrong model mappings, broken fallback tables):

1. **Identify the problematic commit**:
   ```bash
   git log --oneline -10 -- internal/registry/testdata/upstream
   ```

2. **Revert to the previous pinned snapshot**:
   ```bash
   git revert <bad-commit-sha>
   ```
   Or manually restore:
   ```bash
   git checkout <previous-sha> -- internal/registry/testdata/upstream/
   git checkout <previous-sha> -- internal/registry/registry.go
   ```

3. **Verify the revert**:
   ```bash
   env -u AUTH_TOKENS -u ADMIN_TOKEN go test ./internal/registry/...
   ```

4. **Commit the revert**:
   ```bash
   git add internal/registry/testdata/upstream internal/registry/registry.go
   git commit -m "revert(registry): roll back upstream sync to <previous-sha>"
   ```

### Emergency: Force Offline Fallback

If upstream sources are unreachable and the live registry is returning stale data:

1. The proxy already falls back to hardcoded tables automatically on refresh failure.
2. To force fallback immediately, restart the proxy: `systemctl restart freebuff-proxy`
3. Verify: `curl -s http://127.0.0.1:3457/healthz | jq .registry` — should show `"fallback": true`

---

## Operator Guide: Manual Sync (Phase 4.6)

### Quick Reference

| Operation | Command |
|---|---|
| Check for drift | `bash scripts/check-upstream.sh` |
| Check specific ref | `bash scripts/check-upstream.sh <commit-sha>` |
| Full sync + tests | `bash scripts/sync-upstream.sh` |
| Sync only (no tests) | `bash scripts/sync-upstream.sh --no-test` |
| Sync + full suite | `bash scripts/sync-upstream.sh --test-all` |
| Dry-run | `bash scripts/sync-upstream.sh --check` |

### Step-by-Step Manual Sync

1. **Check drift**: `bash scripts/check-upstream.sh` — look for `DRIFT` status
2. **Perform sync**: `bash scripts/sync-upstream.sh`
3. **If parity tests fail**, update `fallbackAgents`/`fallbackRootByModel` in `registry.go`
4. **Run full suite**: `env -u AUTH_TOKENS -u ADMIN_TOKEN go test ./...`
5. **Commit**: `git commit -m "chore(registry): sync pinned upstream models to vendor <sha>"`
6. **Verify runtime**: `curl -s http://127.0.0.1:3457/healthz | jq .registry`

### Monitoring Registry Health

| Surface | Field | Healthy State |
|---|---|---|
| `/healthz` | `registry.fallback` | `false` (live) |
| `/healthz` | `registry.age_seconds` | < `REGISTRY_REFRESH` (default 6h) |
| `/metrics` | `freebuff_proxy_registry_fallback` | `0` (live) |
| `/metrics` | `freebuff_proxy_registry_age_seconds` | < 21600 |
| CI | `upstream-drift` workflow | Green |

### When to Manually Sync

- **After upstream release**: New models or agent changes in CodebuffAI/freebuff
- **After CI drift alert**: `upstream-drift` workflow goes red
- **Before a major release**: Ensure offline fallback is current
- **After upstream outage recovery**: If live registry returned stale data
## Research Plan

### Phase 4.1: Current Infrastructure (Complete)

- [x] Pinned upstream snapshots in `internal/registry/testdata/upstream/`
- [x] Hardcoded fallback tables in `internal/registry/registry.go`
- [x] `TestFallbackParityWithPinnedUpstream` parity test
- [x] `check-upstream.sh` drift detection script
- [x] `sync-upstream.sh` full sync script
- [x] `upstream-drift` weekly CI workflow
- [x] Runtime self-healing via live registry refresh

### Phase 4.2: Warning-Level Alerts

- [x] Add Slack/Discord webhook notification on CI drift detection — `upstream-drift.yml` fires a best-effort webhook POST when drift is detected and the `DRIFT_WEBHOOK_URL` secret is set
- [x] Add Prometheus metric for registry staleness — `freebuff_proxy_registry_age_seconds` and `freebuff_proxy_registry_fallback` gauges in `/metrics`
- [x] Add `/healthz` field for registry freshness — `registry` object with `fallback`, `last_refresh`, `age_seconds`
- [x] Add dashboard indicator for last successful upstream sync — `registry_fallback` and `registry_last_refresh` fields in overview data

### Phase 4.3: Protocol Drift Detection

- [x] Consolidated error-classification regression tests — `internal/upstream/protocol_regression_test.go` pins all ~27 known upstream error bodies/statuses via `classifyError`
- [x] Field-extraction regression tests — full quota payload → `RateLimitError`, `ip_capped` → `IpCappedError`, `country_blocked` → `CountryBlockedError`
- [x] Timestamp-format regression tests — `parseFlexTime` accepts RFC3339 / unix seconds / unix millis
- [x] Anthropic SSE lifecycle documented — `message_start → content_block_start(s) → content_block_delta(s) → content_block_stop(s) → message_delta → message_stop`; thinking blocks carry `"signature": ""` (runtime-tested in `harness_compatibility_test.go`)

> **Note**: Monitoring upstream response schema / error-code changes and documenting upstream SSE format drift remain live, best-effort items best handled when a real upstream drift is observed (the regression suite above is what catches them in CI). They are tracked under Phase 4.5.

#### Protocol Regression Test Suite

`internal/upstream/protocol_regression_test.go` consolidates drift-pinning tests that run with
every hermetic test pass and fail CI if an upstream wire shape changes:

| Test | What it pins | How to extend |
|---|---|---|
| `TestProtocolRegressionErrorMatrix` | Every known upstream error body/status → Go error type + sentinel + explicit "must NOT be X" guards | Add a row when upstream introduces a new error code or reclassifies one |
| `TestProtocolRegressionParseFlexTime` | Accepted timestamp formats (RFC3339, RFC3339Nano, unix seconds string/float64, unix millis float64) + invalid → error | Add a format when upstream emits a new timestamp shape |
| `TestProtocolRegressionRateLimitFields` | Full `pacific_day` quota payload → `Status/Model/Limit/RecentCount/Period/ResetAt/RetryAfter` | Update values when the quota body shape renames a field |
| `TestProtocolRegressionIpCappedFields` | `ip_capped` → `ActiveUsersForIP/Limit/RetryAfter` | — |
| `TestProtocolRegressionCountryBlockedFields` | `country_blocked` → `CountryCode/CountryBlockReason` | — |
| `anthropicLifecycle` (const) | Anthropic SSE event lifecycle as a compile-time anchor | Update with the lifecycle string |

Key guarantees the matrix pins:
- `429 ip_capped` unwraps to **`ErrIpCapped`** (admission-only, not quota) — distinct from `ErrRateLimited`.
- `409 session_limit_reached` and `429 waiting_room_queued` are **NOT** `ErrSessionInvalid` (the session row is fine).
- `409 session_model_mismatch` + `"limited"` marker maps to **`LimitedIpError`** (`ErrModelIPLimited`).
- `403 {"error":"account_suspended"}` maps to a ban (`BanError`), the same class as `{"status":"banned"}`.

### Phase 4.4: Automated Sync Automation

- [ ] Auto-create PR on drift detection (GitHub Actions)
- [ ] Auto-update `fallbackAgents` when upstream adds models
- [ ] Auto-update `fallbackRootByModel` when agent mappings change

### Phase 4.5: Egress Behavior Tracking

- [ ] Track upstream admission response changes over time
- [ ] Monitor quota window changes (pacific_day, pacific_week)
- [ ] Document model availability changes
- [ ] Add changelog for upstream behavioral shifts

### Phase 4.6: Documentation & Runbooks

- [x] Document drift response runbook in detail — expanded Drift Response Playbook with step-by-step instructions
- [x] Add troubleshooting guide for common sync failures — 6 failure scenarios with symptoms and fixes
- [x] Document rollback procedures for bad syncs — revert workflow + emergency fallback
- [x] Add operator guide for manual sync operations — quick reference table, step-by-step, monitoring, when-to-sync

---

## Runtime Freshness Surfaces (Phase 4.2)

The proxy exposes registry freshness at three surfaces so operators can detect
a stale offline path without inspecting logs:

### `/healthz`

```json
{
  "registry": {
    "fallback": true,
    "last_refresh": null,
    "age_seconds": null
  }
}
```

- `fallback`: `true` when the current model catalog is the offline hardcoded
  fallback; `false` after a successful live refresh.
- `last_refresh`: ISO timestamp of the last successful live refresh, or `null`
  when the registry has never refreshed from upstream.
- `age_seconds`: seconds since the last successful live refresh, or `null` when
  never refreshed.

### `/metrics` (Prometheus)

```
# HELP freebuff_proxy_registry_age_seconds Seconds since the last successful live model registry refresh (0 = never refreshed)
# TYPE freebuff_proxy_registry_age_seconds gauge
freebuff_proxy_registry_age_seconds 0

# HELP freebuff_proxy_registry_fallback 1 when the registry is serving the offline hardcoded fallback, 0 when live-refreshed
# TYPE freebuff_proxy_registry_fallback gauge
freebuff_proxy_registry_fallback 1
```

### Dashboard (Overview)

The overview page includes `registry_fallback` (bool) and `registry_last_refresh`
(human-readable timestamp or empty string) so a stale offline path is visible
at a glance.

### CI Webhook Notification

The `upstream-drift` GitHub Actions workflow fires a best-effort webhook POST
(Slack/Discord-compatible JSON) when drift is detected and the `DRIFT_WEBHOOK_URL`
repository secret is configured. The secret is optional — unset means no
notification.

---

## Testing

### Parity Test

```bash
env -u AUTH_TOKENS -u ADMIN_TOKEN go test ./internal/registry/... -run TestFallbackParityWithPinnedUpstream
```

Verifies that `fallbackAgents` and `fallbackRootByModel` match pinned snapshots.

### Drift Check

```bash
bash scripts/check-upstream.sh
```

Read-only check for hash parity between pinned snapshots and upstream.

### Full Sync Test

```bash
env -u AUTH_TOKENS -u ADMIN_TOKEN go test ./...
```

Comprehensive test suite including registry, pool, stealth, and server tests.

---

## References

- [README — Upstream Drift Tracking & Sync](../README.md#upstream-drift-tracking--sync)
- [ROADMAP — Upstream Drift Tracking](../ROADMAP.md#4-upstream-drift-tracking)
- `internal/registry/registry.go` — Fallback tables and registry logic
- `internal/registry/registry_test.go` — Parity test
- `internal/registry/testdata/upstream/` — Pinned snapshots
- `scripts/check-upstream.sh` — Drift detection
- `scripts/sync-upstream.sh` — Full sync
- `.github/workflows/upstream-drift.yml` — CI workflow
```
