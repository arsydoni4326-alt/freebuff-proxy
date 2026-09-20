<script>
  // ARSYDONI UPDATE SOURCE — merge-guarded component; merge policy in
  // lib/README_ARSYDONI_UPDATE.md. Do not remove on upstream merges.
  import { RefreshCw } from "@lucide/svelte";
  import Button from "./Button.svelte";
  import UpdateModalArsydoni from "./UpdateModal_Arsydoni.svelte";
  import { push as pushToast } from "../stores/toast.js";
  import { tr } from "../i18n.js";
  import { checkForUpdate, shortCommit } from "../updateCheck_arsydoni.js";

  /**
   * UpdateCheckButton_Arsydoni — manual "Check for Updates" control. Forces
   * a cache-bypassing GET /admin/api/version and reports the answer in a
   * dialog: update-available (warning) or up-to-date (success), both with
   * the current version + commit vs main's head. Errors surface as a toast
   * and never disturb the dashboard.
   */
  let checking = $state(false);
  let modalOpen = $state(false);
  let statusInfo = $state(null);

  async function runCheck() {
    if (checking) return;
    checking = true;
    try {
      // force=true invalidates the gateway's update cache (10-min head /
      // 6h release TTLs) so the answer reflects the repo state right now.
      statusInfo = await checkForUpdate(true);
      modalOpen = true;
    } catch (err) {
      pushToast({
        tone: "error",
        title: err?.message || $tr("Update check failed. Try again."),
      });
    } finally {
      checking = false;
    }
  }
</script>

<Button variant="ghost" size="sm" onclick={runCheck} disabled={checking}>
  <RefreshCw size={12} class={checking ? "animate-spin" : ""} />
  <span>{checking ? $tr("Checking…") : $tr("Check for Updates")}</span>
</Button>

<UpdateModalArsydoni bind:open={modalOpen} info={statusInfo} />
