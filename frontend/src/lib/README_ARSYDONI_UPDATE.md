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
`arsydoni4326-alt/freebuff-proxy`, 6h cache, fail-open). When `has_update`
is true the modal opens — on every refresh, until the gateway is current.
When up to date, nothing renders.

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
`https://github.com/arsydoni4326-alt/freebuff-proxy` (`releases/latest`
for the version + notes, `commits/<tag>` for the short hash) — do not
repoint it.

## Tests that protect it

- `backend/internal/updatecheck/updatecheck_test.go` (Info + best-effort commit)
- `backend/internal/server/dashboard_pages_test.go` (`TestUpdateBadgeRendered`)
- `frontend/src/lib/updateCheck_arsydoni.test.js`
- `frontend/e2e/update-modal-arsydoni.spec.ts`
