/**
 * ARSYDONI UPDATE CHECK — merge-guarded module (see README_ARSYDONI_UPDATE.md
 * in this directory). Single owner of the dashboard's update-available
 * lookup: GET /admin/api/version (VersionResponse in
 * backend/internal/dashboard/admin_wire.go), sourced from the
 * arsydoni4326-alt/freebuff-proxy main branch: the running build's commit
 * hash is compared against the branch head, so every pushed commit — not
 * only tagged releases — marks the build outdated. App.svelte runs it once
 * per page load and opens UpdateModal_Arsydoni.svelte when has_update is
 * true.
 * Do not delete, inline, or repoint this module on upstream merges.
 */
import { fetchAPI } from "./api/client.js";
import { adminApi } from "./api/paths.js";

/**
 * Version endpoint URL. `force` makes the gateway invalidate its update
 * cache (updatecheck.Invalidate) before answering, so a manual check
 * always reflects the repo state right now.
 * @param {boolean} [force]
 */
export function versionEndpoint(force = false) {
  return force ? `${adminApi.version}?force=true` : adminApi.version;
}

/**
 * Normalize the version payload to a stable shape; unknown/absent fields
 * degrade to empty strings so the modal can hide them.
 * @param {unknown} data - raw GET /admin/api/version body
 */
export function normalizeVersionPayload(data) {
  const d = data && typeof data === "object" ? data : {};
  return {
    current_version: String(d.current_version ?? ""),
    has_update: Boolean(d.has_update),
    latest_version: String(d.latest_version ?? ""),
    update_url: String(d.update_url ?? ""),
    latest_commit: String(d.latest_commit ?? ""),
    current_commit: String(d.current_commit ?? ""),
    changelog: String(d.changelog ?? ""),
  };
}

/** Short (7-char) commit hash for display; empty stays empty. */
export function shortCommit(sha) {
  const s = String(sha ?? "").trim();
  return s ? s.slice(0, 7) : "";
}

/**
 * One update check. Throws on network/auth failure — callers decide how to
 * degrade (App.svelte logs a warning and skips the modal).
 * @param {boolean} [force] - bypass the gateway's update cache
 */
export async function checkForUpdate(force = false) {
  return normalizeVersionPayload(await fetchAPI(versionEndpoint(force)));
}
