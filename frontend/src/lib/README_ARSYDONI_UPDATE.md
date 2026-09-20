# ARSYDONI UPDATE SOURCE — merge-guarded feature (do not remove)

The dashboard's "update available" modal is a fork-owned feature. It must
survive every merge with upstream (the `CodebuffAI` lineage,
`trefeon/freebuff-proxy`, or any other remote). Upstream does not have it;
a merge that drops these files or repoints the update source is a
resolution mistake, not a cleanup.

## What it does

On every dashboard page load, `App.svelte` calls `checkForUpdate()` from
`updateCheck_arsydoni.js`. The gateway answers `GET /admin/api/version`
from `backend/internal/updatecheck` (repo pinned to
`arsydoni4326-alt/freebuff-proxy`, fail-open). The **primary update signal
is the commit hash**: the running build's commit (stamped at build time via
`-X main.commit=...`) is compared against the repo's `main` branch head
(10-minute cache) — every push to `main` marks the running build outdated,
no release required. When the running commit is unknown (dev builds), the
check falls back to the release-tag comparison (6h cache). When
`has_update` is true the modal opens — on every refresh, until the running
commit matches `main`'s head again. When up to date, nothing renders.

## Files that make up the feature (keep all of them)

- `frontend/src/lib/updateCheck_arsydoni.js` — fetch + normalize
- `frontend/src/lib/components/UpdateModal_Arsydoni.svelte` — the modal
- `frontend/src/lib/README_ARSYDONI_UPDATE.md` — this merge policy
- `frontend/src/App.svelte` — boot call + modal mount (search "ARSYDONI")
- `backend/internal/updatecheck/updatecheck.go` — `DefaultRepo` pin + `Info`
  (tag, tag commit, release notes)
- `backend/internal/dashboard/dashboard.go` — `releaseURL` + `APIVersion`
- `backend/internal/dashboard/admin_wire.go` — `VersionResponse`
  (`latest_commit`, `changelog`)

## Merge-conflict rule

Additive union: if upstream touches any of these files, take BOTH sides —
upstream's change AND this feature. Never resolve by deleting the
`ARSYDONI`-marked blocks. The update source is
`https://github.com/arsydoni4326-alt/freebuff-proxy` (`commits/main` for
the head-commit comparison — primary signal —, `releases/latest` for the
version + notes, `commits/<tag>` for the release commit) — do not repoint
it. The running commit must be stamped at build time (`-X main.commit=...`):
the Dockerfile (`ARG COMMIT`), docker-compose.yml, deploy.yaml, and
.goreleaser.yml all wire it; a build without the stamp degrades to the
release-tag comparison.

## Tests that protect it

- `backend/internal/updatecheck/updatecheck_test.go` (Info + Head +
  CommitOutdated + best-effort commit)
- `backend/internal/server/dashboard_pages_test.go` (`TestUpdateBadgeRendered`)
- `frontend/src/lib/updateCheck_arsydoni.test.js`
- `frontend/e2e/update-modal-arsydoni.spec.ts`
