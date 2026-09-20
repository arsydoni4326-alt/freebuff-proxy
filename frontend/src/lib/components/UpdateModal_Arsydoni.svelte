<script>
  // ARSYDONI UPDATE SOURCE — merge-guarded component; merge policy in
  // lib/README_ARSYDONI_UPDATE.md. Do not remove on upstream merges.
  import {
    ArrowUpCircle,
    ExternalLink,
    GitCommitHorizontal,
  } from "@lucide/svelte";
  import Modal from "./Modal.svelte";
  import Button from "./Button.svelte";
  import { tr } from "../i18n.js";
  import { shortCommit } from "../updateCheck_arsydoni.js";

  /**
   * UpdateModal_Arsydoni — "update available" dialog. App.svelte opens it
   * on every dashboard load while GET /admin/api/version reports
   * has_update; it never renders when the gateway is current.
   *
   * @prop {boolean} open
   * @prop {Record<string, string|boolean>} [info] — normalized version payload
   */
  let { open = $bindable(false), info = null } = $props();

  const current = $derived(info?.current_version || "");
  const latest = $derived(info?.latest_version || "");
  const commit = $derived(shortCommit(info?.latest_commit));
  const changelog = $derived(String(info?.changelog || "").trim());
  const url = $derived(info?.update_url || "");
</script>

<Modal
  bind:open
  title={$tr("Update available")}
  description={$tr("A newer release of freebuff-proxy is available.")}
  size="lg"
>
  {#snippet icon()}
    <div
      class="p-2.5 rounded shrink-0 bg-[var(--fp-warning)]/15 text-[var(--fp-warning)] border border-[var(--fp-warning)]/30"
    >
      <ArrowUpCircle size={20} />
    </div>
  {/snippet}

  <div class="space-y-3">
    <div
      class="grid grid-cols-[auto_1fr] gap-x-3 gap-y-1.5 text-sm items-baseline"
    >
      <span class="text-[var(--fp-muted)]">{$tr("Installed")}</span>
      <span class="fp-mono text-[var(--fp-text)] break-all">
        {current || $tr("unknown")}
      </span>
      <span class="text-[var(--fp-muted)]">{$tr("Latest release")}</span>
      <span class="flex items-center gap-2 min-w-0 flex-wrap">
        <span class="fp-mono text-[var(--fp-text)]">{latest}</span>
        {#if commit}
          <span
            class="fp-mono text-[10px] px-1.5 py-0.5 rounded bg-[var(--fp-surface-2)] text-[var(--fp-dim)] inline-flex items-center gap-1"
          >
            <GitCommitHorizontal size={11} />
            {commit}
          </span>
        {/if}
      </span>
    </div>

    {#if changelog}
      <div>
        <div
          class="text-xs font-semibold text-[var(--fp-muted)] uppercase tracking-wider mb-1.5"
        >
          {$tr("What's changed")}
        </div>
        <div
          class="max-h-48 overflow-y-auto rounded bg-[var(--fp-surface-2)]/60 border border-[var(--fp-border)] p-3 text-xs leading-relaxed text-[var(--fp-text)] whitespace-pre-wrap break-words"
        >
          {changelog}
        </div>
      </div>
    {/if}
  </div>

  {#snippet footer()}
    <Button variant="ghost" onclick={() => (open = false)}>
      {$tr("Later")}
    </Button>
    {#if url}
      <a
        href={url}
        target="_blank"
        rel="noopener noreferrer"
        class="fp-btn fp-btn-primary"
        onclick={() => (open = false)}
      >
        <ExternalLink size={14} />
        {$tr("View release")}
      </a>
    {/if}
  {/snippet}
</Modal>
