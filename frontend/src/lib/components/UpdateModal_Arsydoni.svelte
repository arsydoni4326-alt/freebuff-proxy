<script>
  // ARSYDONI UPDATE SOURCE — merge-guarded component; merge policy in
  // lib/README_ARSYDONI_UPDATE.md. Do not remove on upstream merges.
  import {
    ArrowUpCircle,
    CircleCheck,
    ExternalLink,
    GitCommitHorizontal,
  } from "@lucide/svelte";
  import Modal from "./Modal.svelte";
  import Button from "./Button.svelte";
  import { tr } from "../i18n.js";
  import { shortCommit } from "../updateCheck_arsydoni.js";

  /**
   * UpdateModal_Arsydoni — update-status dialog with two states. Default
   * (outdated): warning tone, commit + changelog, "View release" action —
   * App.svelte opens it on every dashboard load while GET /admin/api/version
   * reports has_update, and it never renders when the gateway is current.
   * Up-to-date state: success tone, no changelog, "Close" action — used by
   * UpdateCheckButton_Arsydoni when a manual check finds nothing new.
   *
   * @prop {boolean} open
   * @prop {Record<string, string|boolean>} [info] — normalized version payload
   */
  let { open = $bindable(false), info = null } = $props();

  const current = $derived(info?.current_version || "");
  const latest = $derived(info?.latest_version || "");
  const runningCommit = $derived(shortCommit(info?.current_commit));
  const headCommit = $derived(shortCommit(info?.latest_commit));
  const changelog = $derived(String(info?.changelog || "").trim());
  const url = $derived(info?.update_url || "");
  // Up-to-date state: a manual check with no mismatch renders the success
  // variant (no changelog block, no "View release" action).
  const upToDate = $derived(!info?.has_update);
</script>

<Modal
  bind:open
  title={$tr(upToDate ? "Up to date" : "Update available")}
  description={$tr(
    upToDate
      ? "The running build matches the latest commit on main."
      : "The running build is behind the latest commit on main.",
  )}
  size="lg"
>
  {#snippet icon()}
    <div
      class="p-2.5 rounded shrink-0 {upToDate
        ? 'bg-[var(--fp-success)]/15 text-[var(--fp-success)] border border-[var(--fp-success)]/30'
        : 'bg-[var(--fp-warning)]/15 text-[var(--fp-warning)] border border-[var(--fp-warning)]/30'}"
    >
      {#if upToDate}
        <CircleCheck size={20} />
      {:else}
        <ArrowUpCircle size={20} />
      {/if}
    </div>
  {/snippet}

  <div class="space-y-3">
    <div
      class="grid grid-cols-[auto_1fr] gap-x-3 gap-y-1.5 text-sm items-baseline"
    >
      <span class="text-[var(--fp-muted)]">{$tr("Installed")}</span>
      <span class="flex items-center gap-2 min-w-0 flex-wrap">
        <span class="fp-mono text-[var(--fp-text)]">
          {current || $tr("unknown")}
        </span>
        {#if runningCommit}
          <span
            class="fp-mono text-[10px] px-1.5 py-0.5 rounded bg-[var(--fp-surface-2)] text-[var(--fp-dim)] inline-flex items-center gap-1"
          >
            <GitCommitHorizontal size={11} />
            {runningCommit}
          </span>
        {/if}
      </span>
      <span class="text-[var(--fp-muted)]">{$tr("Latest commit")}</span>
      <span class="flex items-center gap-2 min-w-0 flex-wrap">
        {#if headCommit}
          <span
            class="fp-mono text-[10px] px-1.5 py-0.5 rounded inline-flex items-center gap-1 {upToDate
              ? 'bg-[var(--fp-success)]/15 text-[var(--fp-success)]'
              : 'bg-[var(--fp-warning)]/15 text-[var(--fp-warning)]'}"
          >
            <GitCommitHorizontal size={11} />
            {headCommit}
          </span>
        {:else}
          <span class="fp-mono text-[var(--fp-text)]">{$tr("unknown")}</span>
        {/if}
        {#if latest}
          <span class="fp-mono text-[var(--fp-dim)] text-xs">({latest})</span>
        {/if}
      </span>
    </div>

    {#if changelog && !upToDate}
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
    <Button
      variant={upToDate ? "primary" : "ghost"}
      onclick={() => (open = false)}
      data-autofocus={upToDate ? true : undefined}
    >
      {$tr(upToDate ? "Close" : "Later")}
    </Button>
    {#if !upToDate && url}
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
