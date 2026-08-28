<script>
  import {
    ChevronDown,
    ChevronRight,
    Unlock,
    Lock,
    Trash2,
    Zap,
    RefreshCw,
    Check,
    ExternalLink,
  } from '@lucide/svelte';
  import Button from './Button.svelte';
  import StatusBadge from './StatusBadge.svelte';
  import CopyButton from './CopyButton.svelte';
  import PremiumQuotaBar from './PremiumQuotaBar.svelte';
  import { formatLocalDate } from '../utils/format.js';
  import { tr } from '../i18n.js';

  let {
    token,
    idx,
    expanded,
    spawnModel = $bindable(''),
    actionPending,
    now,
    onToggle,
    onAction,
    onSpawn,
    onRefresh,
  } = $props();

  function banBadge(token) {
    if (token.ban_type === 'hard') {
      return { label: $tr('banned — appeal required'), tone: 'critical', pulse: true };
    }
    if (token.ban_type === 'temporary') {
      const until = formatLocalDate(token.banned_until);
      return { label: until ? $tr('banned until {time}', { time: until }) : $tr('banned (temporary)'), tone: 'bad' };
    }
    return null;
  }

  function statusFor(token) {
    const ban = banBadge(token);
    if (ban) return ban;
    if (token.locked) return { label: $tr('locked'), tone: 'warn' };
    if (token.cooldown_active) return { label: $tr('cooldown'), tone: 'warn' };
    const s = token.session_status || '';
    if (s === 'active') return { label: $tr('leased'), tone: 'good', pulse: true };
    if (s === 'queued') return { label: $tr('queued'), tone: 'info' };
    if (s === 'banned') return { label: $tr('banned'), tone: 'bad' };
    return { label: $tr('idle'), tone: 'idle' };
  }

  function cooldownLabel(token) {
    if (!token.cooldown_active || !token.cooldown_until) return '—';
    const ms = new Date(token.cooldown_until).getTime() - now;
    if (ms <= 0) return 'expiring';
    const s = Math.floor(ms / 1000);
    const h = Math.floor(s / 3600);
    const m = Math.floor((s % 3600) / 60);
    const sec = s % 60;
    if (h > 0) return `${h}h ${m}m`;
    if (m > 0) return `${m}m ${sec}s`;
    return `${sec}s`;
  }

  function cooldownTone(token) {
    if (!token.cooldown_until) return 'default';
    const ms = new Date(token.cooldown_until).getTime() - now;
    if (ms >= 0 && ms < 5 * 60_000) return 'warn';
    return 'default';
  }

  const st = $derived(statusFor(token));
</script>

<tr>
  <td class="w-8">
    <button
      type="button"
      onclick={onToggle}
      aria-expanded={expanded}
      aria-label={expanded ? `Collapse quotas for token ${idx}` : `Expand quotas for token ${idx}`}
      class="inline-flex items-center justify-center w-6 h-6 text-[var(--fp-dim)] hover:text-[var(--fp-text)]"
    >
      {#if expanded}
        <ChevronDown size={16} />
      {:else}
        <ChevronRight size={16} />
      {/if}
    </button>
  </td>
  <td>
    <span class="fp-num text-xs text-[var(--fp-text)]">#{idx}</span>
  </td>
  <td>
    <StatusBadge status={st.label} tone={st.tone} pulse={st.pulse} />
  </td>
  <td>
    {#if token.session_instance}
      <span class="inline-flex items-center gap-2">
        <code class="fp-num text-xs text-[var(--fp-muted)]">{token.session_instance}</code>
        <CopyButton text={token.session_instance} label="Copy" />
      </span>
    {:else}
      <span class="text-xs text-[var(--fp-dim)]">—</span>
    {/if}
  </td>
  <td class="num">
    {#if token.cooldown_active}
      <span class="fp-num text-xs {cooldownTone(token) === 'warn' ? 'text-[var(--fp-warning)]' : 'text-[var(--fp-text)]'}">
        {cooldownLabel(token)}
      </span>
    {:else}
      <span class="fp-num text-xs text-[var(--fp-dim)]">—</span>
    {/if}
  </td>
  <td class="text-right">
    <div class="inline-flex items-center gap-1.5 justify-end">
      {#if token.cooldown_active}
        <Button
          variant="ghost"
          size="sm"
          disabled={actionPending}
          onclick={() => onAction('clear')}
        >
          <Unlock size={13} />
          <span>{$tr('Clear')}</span>
        </Button>
      {/if}
      {#if token.locked}
        <Button
          variant="secondary"
          size="sm"
          disabled={actionPending}
          onclick={() => onAction('unlock')}
        >
          <Unlock size={13} />
          <span>{$tr('Unlock')}</span>
        </Button>
      {:else}
        <Button
          variant="ghost"
          size="sm"
          disabled={actionPending}
          onclick={() => onAction('lock')}
        >
          <Lock size={13} />
          <span>{$tr('Lock')}</span>
        </Button>
      {/if}
      <Button
        variant="danger"
        size="sm"
        disabled={actionPending}
        onclick={() => onAction('remove')}
      >
        <Trash2 size={13} />
        <span>{$tr('Remove')}</span>
      </Button>
    </div>
  </td>
</tr>
{#if expanded}
  <tr>
    <td colspan="6" class="!p-0">
      <div class="fp-inset m-2 rounded p-3">
        <!-- Premium Quota Tracker — pacific_day 5/day pool + glm_v53_flash lane -->
        <div class="flex flex-col gap-2 mb-3">
          {#if token.premium_quota}
            <PremiumQuotaBar quota={token.premium_quota} title={$tr('Premium pool')} {now} />
          {:else}
            <p class="text-xs text-[var(--fp-dim)] italic">{$tr('No premium quota data — run a request or -test-token to populate.')}</p>
          {/if}
          {#if token.glm53flash_quota}
            <PremiumQuotaBar quota={token.glm53flash_quota} title={$tr('GLM 5.3 Flash pool')} {now} />
          {/if}
        </div>
        <!-- Dev Tools: Session Generator & Diagnostics Toolbar -->
        <div class="mb-3 p-2.5 rounded bg-[var(--fp-surface)] border border-[var(--fp-border)] flex flex-wrap items-center justify-between gap-2.5">
          <div class="flex flex-wrap items-center gap-2">
            <span class="text-xs font-semibold text-[var(--fp-muted)] uppercase tracking-wider">{$tr('Dev Session:')}</span>
            <select
              bind:value={spawnModel}
              class="fp-input !text-xs !py-1 !px-2 !h-7 !w-44 !inline-block"
            >
              <option value="openai/gpt-5.6-luna">openai/gpt-5.6-luna (4/d)</option>
              <option value="z-ai/glm-5.3-flash">z-ai/glm-5.3-flash (2/d)</option>
              <option value="deepseek/deepseek-v4-flash">deepseek/deepseek-v4-flash</option>
              <option value="mimo/mimo-v2.5">mimo/mimo-v2.5 (unlimited)</option>
              <option value="deepseek/deepseek-v4-pro">deepseek/deepseek-v4-pro</option>
              <option value="z-ai/glm-5.2">z-ai/glm-5.2 (referral)</option>
            </select>
            <Button
              variant="secondary"
              size="sm"
              class="!h-7 !text-xs !px-2.5"
              disabled={actionPending}
              onclick={() => onSpawn(spawnModel || 'mimo/mimo-v2.5')}
            >
              <Zap size={12} />
              <span>{$tr('Make Session')}</span>
            </Button>
          </div>

          <div class="flex items-center gap-1.5">
            <Button
              variant="ghost"
              size="sm"
              class="!h-7 !text-xs !px-2"
              disabled={actionPending}
              onclick={() => onRefresh('probe')}
            >
              <RefreshCw size={12} />
              <span>{$tr('Probe')}</span>
            </Button>
            <Button
              variant="ghost"
              size="sm"
              class="!h-7 !text-xs !px-2"
              disabled={actionPending}
              onclick={() => onRefresh('finish')}
            >
              <Check size={12} />
              <span>{$tr('Finish Runs')}</span>
            </Button>
          </div>
        </div>
        {#if token.session_remaining_seconds > 0 && token.session_model}
          <div class="mb-2 px-2 py-1 rounded bg-[var(--fp-accent)]/10 text-xs text-[var(--fp-accent)] flex items-center justify-between">
            <span>{$tr('Active Session:')} <code class="fp-num">{token.session_model}</code></span>
            <span class="fp-num">{Math.floor(token.session_remaining_seconds / 60)}m {token.session_remaining_seconds % 60}s remaining</span>
          </div>
        {/if}
        {#if token.has_standing}
          <!-- Standing / trust block (issue #140 P3d): level,
               score progress toward the next level, the cap
               holding the account (capped_by), and upstream's
               suggested earn-back actions. -->
          <div class="mb-2 px-2 py-1.5 rounded bg-[var(--fp-bg)]/40">
            <div class="flex items-center justify-between gap-2 mb-1">
              <p class="text-xs text-[var(--fp-muted)] uppercase tracking-wider font-semibold">{$tr('Account standing')}</p>
              <span class="text-xs text-[var(--fp-text)] font-semibold">{token.standing_label || token.standing_level}</span>
            </div>
            {#if token.standing_score != null && token.standing_next_level}
              <div class="flex items-center gap-2">
                <div class="h-1.5 flex-1 rounded bg-[var(--fp-bg)] overflow-hidden">
                  <div
                    class="h-full rounded bg-[var(--fp-accent)]"
                    style={`width: ${Math.min(100, Math.max(0, token.standing_score))}%`}
                  ></div>
                </div>
                <span class="fp-num text-xs text-[var(--fp-muted)] shrink-0">
                  {$tr('score {score} → next: {level}', { score: token.standing_score, level: token.standing_next_level })}
                </span>
              </div>
            {:else if token.standing_score != null}
              <span class="fp-num text-xs text-[var(--fp-muted)]">{$tr('score {score}', { score: token.standing_score })}</span>
            {/if}
            {#if token.standing_blurb}
              <p class="text-xs text-[var(--fp-dim)] mt-1">{token.standing_blurb}</p>
            {/if}
            {#if token.standing_capped_by}
              <p class="text-xs mt-1 text-[var(--fp-warning)]">
                {$tr('Capped by')} <code class="fp-num">{token.standing_capped_by}</code>{#if token.standing_capped_reason}: {token.standing_capped_reason}{/if}
              </p>
            {/if}
            {#if token.standing_next_steps?.length > 0}
              <ul class="mt-1.5 flex flex-col gap-1">
                {#each token.standing_next_steps as step}
                  <li class="text-xs text-[var(--fp-text)] flex items-start gap-1.5">
                    <span class="fp-num text-[var(--fp-accent)] shrink-0">+{step.points}</span>
                    <span>
                      {step.label}{#if step.detail} — <span class="text-[var(--fp-dim)]">{step.detail}</span>{/if}
                      {#if step.href}
                        <a href={step.href} target="_blank" rel="noopener noreferrer" class="ml-1 text-[var(--fp-accent)] hover:underline inline-flex items-center gap-0.5">
                          <ExternalLink size={10} />
                        </a>
                      {/if}
                    </span>
                  </li>
                {/each}
              </ul>
            {/if}
          </div>
        {/if}
        {#if token.has_quota && token.quota?.length > 0}
          <div class="flex flex-col gap-2">
            <p class="text-xs text-[var(--fp-muted)] uppercase tracking-wider font-semibold">{$tr('Session quotas')}</p>
            {#each token.quota as q}
              <div class="flex flex-col sm:flex-row sm:items-center gap-1 sm:gap-4 px-2 py-1.5 rounded bg-[var(--fp-bg)]/40">
                <code class="fp-num text-xs text-[var(--fp-text)] sm:w-48 shrink-0 truncate">{q.model}</code>
                <span class="fp-num text-xs text-[var(--fp-muted)]">
                  <span class="text-[var(--fp-text)]">{q.recent}</span> / {q.limit}
                  {#if q.limit !== '0' && q.limit !== ''}
                    {$tr('(remaining {count})', { count: Math.max(0, parseFloat(q.limit) - parseFloat(q.recent)) })}
                  {/if}
                </span>
                <span class="fp-num text-xs text-[var(--fp-dim)] sm:ml-auto">
                  {q.period}{#if q.has_entitlement} · {$tr('entitled')} {q.entitled}{/if}
                </span>
                <span class="fp-num text-xs text-[var(--fp-dim)]">
                  {#if q.resets_in}
                    {$tr('reset')} {formatLocalDate(q.reset_at_utc) || q.reset_at} ({q.resets_in})
                  {:else}
                    {$tr('reset')} {formatLocalDate(q.reset_at_utc) || q.reset_at}
                  {/if}
                </span>
              </div>
            {/each}
          </div>
        {:else}
          <p class="text-xs text-[var(--fp-dim)] italic">{$tr('No quota data available for this session.')}</p>
        {/if}
      </div>
    </td>
  </tr>
{/if}
