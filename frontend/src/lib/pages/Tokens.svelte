<script>
  import { onDestroy, onMount } from 'svelte';
  import {
    LogIn,
    Plus,
    ExternalLink,
    RefreshCw,
  } from '@lucide/svelte';
  import Button from '../components/Button.svelte';
  import Card from '../components/Card.svelte';
  import Field from '../components/Field.svelte';
  import Alert from '../components/Alert.svelte';
  import EmptyState from '../components/EmptyState.svelte';
  import CopyButton from '../components/CopyButton.svelte';
  import PageHeader from '../components/PageHeader.svelte';
  import TokenCard from '../components/TokenCard.svelte';
  import BridgeTokenCard from '../components/BridgeTokenCard.svelte';
  import { fetchAPI, postAPI, csrfHeader } from '../api/client.js';
  import { adminApi, adminActions, tokenActions } from '../api/paths.js';
  import { usePolling } from '../utils/polling.js';
  import { tr } from '../i18n.js';

  let data = $state(null);
  let loading = $state(true);
  let error = $state('');

  // Add-token form
  let newToken = $state('');
  let adding = $state(false);
  let actionMessage = $state('');
  let actionOK = $state(true);
  // Dev Tools surfaces (per-token session spawn toolbar) are hidden unless
  // the operator enables DEVTOOLS_ENABLED=true in .env (same gate as the
  // sidebar's Dev Tools tab and the server-side DevTools route).
  let devToolsEnabled = $state(false);

  // Device login flow
  let oauthStarting = $state(false);
  let oauthStatus = $state(null);
  let oauthTimer = null;

  // Auto-dismiss action messages
  $effect(() => {
    if (actionMessage) {
      const timeout = actionOK ? 5000 : 10000;
      const timer = setTimeout(() => { actionMessage = ''; }, timeout);
      return () => clearTimeout(timer);
    }
  });

  // Token table
  let expandedToken = $state(null);
  let spawnModels = $state({});
  let actionPending = $state(false);
  let now = $state(Date.now());
  let quotaModelFilter = $state('');

  // Derive unique quota models across all tokens for the filter dropdown
  let allQuotaModels = $derived.by(() => {
    if (!data?.tokens) return [];
    const models = new Set();
    for (const t of data.tokens) {
      if (t.quota) for (const q of t.quota) models.add(q.model);
    }
    return Array.from(models).sort();
  });

  function quotaPercent(recent, limit) {
    const l = parseFloat(limit);
    if (!l || l <= 0) return 0;
    return Math.min(100, Math.round((parseFloat(recent) / l) * 100));
  }

  function quotaUsageTone(pct) {
    if (pct >= 95) return 'critical';
    if (pct >= 80) return 'warn';
    return 'good';
  }

  const tokenValid = $derived(
    newToken.trim() === ''
      ? null
      : /^cb_[A-Za-z0-9_-]{20,}$/.test(newToken.trim())
  );

  async function fetchData() {
    try {
      data = await fetchAPI(adminApi.tokens);
      // Seed the per-token spawn-model map so no TokenCard binding ever sees
      // undefined — Svelte 5 rejects bind:spawnModel={undefined} for a prop
      // with a fallback (props_invalid_value) and unmounts the table.
      (data?.tokens ?? []).forEach((t, i) => {
        const idx = t.index ?? i;
        if (!(idx in spawnModels)) spawnModels[idx] = '';
      });
      error = '';
    } catch (e) {
      error = e.message || $tr('Failed to fetch tokens');
    } finally {
      loading = false;
    }
  }

  async function addToken(e) {
    e.preventDefault();
    if (!newToken.trim() || tokenValid === false || adding) return;
    adding = true;
    try {
      const result = await postAPI(adminActions.tokenAdd, { token: newToken.trim() });
      actionOK = result.ok !== false;
      actionMessage = result.message || (actionOK ? $tr('Token added successfully') : $tr('Failed to add token'));
      if (actionOK) {
        newToken = '';
        fetchData();
      }
    } catch (e) {
      actionOK = false;
      actionMessage = e.message || $tr('Network error adding token');
    } finally {
      adding = false;
    }
  }

  async function triggerAction(url, body, confirmMsg) {
    if (confirmMsg && !confirm(confirmMsg)) return;
    actionPending = true;
    try {
      const result = await postAPI(url, body || undefined);
      actionOK = result.ok !== false;
      actionMessage = result.message || (actionOK ? $tr('Action completed') : $tr('Action failed'));
      fetchData();
    } catch (e) {
      actionOK = false;
      actionMessage = e.message || $tr('Network error executing action');
    } finally {
      actionPending = false;
    }
  }

  function handleTokenAction(token, idx, action) {
    switch (action) {
      case 'clear':
        return triggerAction(tokenActions.unlock(idx), {}, $tr('Clear cooldown for token {idx}? Only do this if the lock is stale.', { idx }));
      case 'unlock':
        return triggerAction(tokenActions.unlockLock(idx), {}, $tr('Unlock token {idx}?', { idx }));
      case 'lock':
        return triggerAction(tokenActions.lock(idx), {}, $tr('Lock token {idx}?', { idx }));
      case 'remove':
        return triggerAction(adminActions.tokenRemove, { token: idx }, $tr('Remove token {idx} from the pool and .env?', { idx }));
      default:
        return;
    }
  }

  function handleSpawn(idx, model) {
    const m = model || 'mimo/mimo-v2.5';
    triggerAction(tokenActions.session(idx), { model: m }, $tr('Create upstream session for token #{idx} on {model}?', { idx, model: m }));
  }

  function handleRefresh(idx, action) {
    if (action === 'probe') {
      return triggerAction(tokenActions.test(idx), {}, $tr('Probe token #{idx} against upstream?', { idx }));
    }
    return triggerAction(tokenActions.finish(idx), {}, $tr('Finish active runs on token #{idx}?', { idx }));
  }

  async function startOAuthLogin() {
    oauthStarting = true;
    oauthStatus = { message: $tr('Starting headless login flow…'), type: 'info' };

    try {
      const res = await fetch(adminActions.loginStart, { method: 'POST', headers: csrfHeader('POST') });
      const result = await res.json();

      if (result.fingerprint && result.login_url) {
        oauthStatus = {
          loginUrl: result.login_url,
          fingerprint: result.fingerprint,
          message: $tr('Open this URL in your browser to sign in:'),
          type: 'pending',
        };

        clearInterval(oauthTimer);
        oauthTimer = setInterval(async () => {
          try {
            const pollRes = await fetch(`${adminApi.loginStatus}?fingerprint=${encodeURIComponent(result.fingerprint)}`);
            const pollData = await pollRes.json();

            if (pollData.status === 'completed') {
              clearInterval(oauthTimer);
              oauthStatus = {
                message: $tr('Token #{idx} added to pool and saved to .env.', { idx: pollData.token_index }),
                type: 'success',
              };
              oauthStarting = false;
              fetchData();
            } else if (pollData.status === 'error') {
              clearInterval(oauthTimer);
              oauthStatus = {
                message: $tr('Login failed: {message}', { message: pollData.message || $tr('unknown error') }),
                type: 'error',
              };
              oauthStarting = false;
            }
          } catch {
            // transient poll failure — keep polling
          }
        }, 3000);
      } else {
        oauthStatus = {
          message: result.message || $tr('Failed to start login wizard.'),
          type: 'error',
        };
        oauthStarting = false;
      }
    } catch (e) {
      oauthStatus = { message: $tr('Network error: {message}', { message: e.message }), type: 'error' };
      oauthStarting = false;
    }
  }

  function toggleExpand(idx) {
    expandedToken = expandedToken === idx ? null : idx;
  }

  usePolling(fetchData, 10000);
  const tick = setInterval(() => { now = Date.now(); }, 1000);
  onMount(async () => {
    try {
      const cfgRes = await fetchAPI(adminApi.config);
      const envContent = cfgRes?.env_content || '';
      const m = envContent.match(/^\s*DEVTOOLS_ENABLED=(.*)$/m);
      const val = m ? m[1].trim().toLowerCase() : '';
      devToolsEnabled = val === 'true' || val === '1';
    } catch {
      devToolsEnabled = false;
    }
  });

  onDestroy(() => {
    clearInterval(oauthTimer);
    clearInterval(tick);
  });
</script>

<div class="page-enter">
  <div class="flex flex-col gap-6">
    <PageHeader title={$tr('Tokens')} description={$tr('Upstream credentials, device login, client API keys, and per-token session quotas')} />

    {#if actionMessage}
      <Alert tone={actionOK ? 'success' : 'error'} title={actionMessage} />
    {/if}
    {#if error}
      <Alert tone="error" title={error}>
        <Button variant="ghost" size="sm" onclick={() => { error = ''; fetchData(); }}>
          {$tr('Retry')}
        </Button>
      </Alert>
    {/if}

    {#if oauthStatus}
      <Alert
        tone={oauthStatus.type === 'success' ? 'success' : oauthStatus.type === 'error' ? 'error' : 'info'}
        title={oauthStatus.message}
      >
        {#if oauthStatus.loginUrl}
          <div class="flex flex-wrap items-center gap-2">
            <code class="fp-num text-xs break-all max-w-full">{oauthStatus.loginUrl}</code>
            <CopyButton text={oauthStatus.loginUrl} label={$tr('Copy link')} />
            <a
              href={oauthStatus.loginUrl}
              target="_blank"
              rel="noopener noreferrer"
              class="inline-flex items-center gap-1 text-xs text-[var(--fp-accent)] hover:underline"
            >
              {$tr('Open')}
              <ExternalLink size={12} />
            </a>
          </div>
        {/if}
      </Alert>
    {/if}

    <!-- Add token form -->
    <Card
      title={$tr('Add Token to Pool')}
      description={$tr('Paste a FreeBuff auth token (cb_…) to add it to the shared pool and save it to .env. Adding burns no quota.')}
    >
      {#snippet actions()}
        <Button
          variant="secondary"
          size="sm"
          onclick={startOAuthLogin}
          disabled={oauthStarting}
        >
          {#if oauthStarting}
            <RefreshCw size={14} class="animate-spin" />
            <span>{$tr('Authorizing…')}</span>
          {:else}
            <LogIn size={14} />
            <span>{$tr('Device Login')}</span>
          {/if}
        </Button>
      {/snippet}
      <form onsubmit={addToken} class="flex flex-col sm:flex-row items-start sm:items-center gap-3">
        <div class="flex-1 w-full">
          <Field
            label={$tr('Token')}
            hint={tokenValid === true ? $tr('Valid format') : tokenValid === false ? $tr('Invalid format') : $tr('Format: cb_…')}
            error={tokenValid === false ? $tr('Token must match cb_… with at least 20 characters') : ''}
            id="add-token-input"
          >
            <input
              id="add-token-input"
              type="text"
              bind:value={newToken}
              placeholder="cb_…"
              autocomplete="off"
              spellcheck="false"
              class="fp-input fp-num w-full"
            />
          </Field>
        </div>
        <Button
          type="submit"
          variant="primary"
          disabled={adding || !newToken.trim() || tokenValid === false}
          loading={adding}
        >
          <Plus size={16} />
          <span>{$tr('Add Token')}</span>
        </Button>
      </form>
    </Card>

    <!-- Client API-key management -->
    <Card
      title={$tr('Client API Keys')}
      description={$tr('sk-fb-… credentials for clients (omp, curl) to authenticate against this proxy. Stored in the API_KEYS line of .env.')}
    >
      {#if generatedKey}
        <div class="fp-inset rounded p-3 mb-3 flex flex-wrap items-center gap-2">
          <span class="text-xs text-[var(--fp-muted)]">{$tr('New key:')}</span>
          <code class="fp-num text-xs text-[var(--fp-accent)] break-all">{generatedKey}</code>
          <CopyButton text={generatedKey} label="Copy" />
        </div>
      {/if}
      {#if apiKeys.length > 0}
        <div class="flex flex-col gap-2 mb-3">
          {#each apiKeys as key}
            <div class="fp-inset rounded flex items-center justify-between gap-2 px-3 py-2">
              <code class="fp-num text-xs truncate">{key}</code>
              <CopyButton text={key} label="Copy" />
            </div>
          {/each}
        </div>
      {/if}
      {#if clientKeyMessage}
        <Alert tone={clientKeyOK ? 'success' : 'error'} title={clientKeyMessage} />
      {/if}
    </Card>

    <!-- Bridge Quota section (bridge mode only) -->
    {#if data?.in_bridge}
      <Card
        title={$tr('Bridge Quota')}
        description={data ? $tr('{count} active bridge client(s)', { count: data.bridge_tokens || 0 }) : ''}
      >
        {#if !data.bridge_token_cards || data.bridge_token_cards.length === 0}
          <EmptyState
            title={$tr('No active bridge clients')}
            description={$tr('Bridge tokens appear here after a client sends a request with a valid FreeBuff token.')}
          />
        {:else}
          <div class="flex flex-col gap-4">
            {#each data.bridge_token_cards as card}
              {@const st = card.status === 'dead' ? { label: $tr('dead'), tone: 'critical', pulse: true }
                : card.status === 'locked' ? { label: $tr('locked'), tone: 'warn' }
                : card.status === 'cooldown' ? { label: $tr('cooldown'), tone: 'warn', pulse: true }
                : card.status === 'active' ? { label: $tr('active'), tone: 'good', pulse: true }
                : { label: card.status, tone: 'idle' }}
              <div class="fp-inset rounded p-3 flex flex-col gap-3">
                <!-- Header row -->
                <div class="flex items-center justify-between gap-3 flex-wrap">
                  <div class="flex items-center gap-2">
                    <code class="fp-num text-xs text-[var(--fp-accent)]">{card.key}</code>
                    <StatusBadge status={st.label} tone={st.tone} pulse={st.pulse} />
                  </div>
                  <div class="flex items-center gap-3 text-xs text-[var(--fp-dim)]">
                    {#if card.model}
                      <span>{$tr('model:')} <code class="fp-num text-[var(--fp-text)]">{card.model}</code></span>
                    {/if}
                    <span>{$tr('runs')} <span class="fp-num text-[var(--fp-text)]">{card.active_runs}</span></span>
                    <span>{$tr('reqs')} <span class="fp-num text-[var(--fp-text)]">{card.requests}</span></span>
                  </div>
                </div>

                <!-- Spend overview -->
                <div class="flex items-center gap-3 text-xs">
                  <span class="text-[var(--fp-muted)]">{$tr('Spend')}:</span>
                  {#if card.spend_limit > 0}
                    {@const spendPct = Math.min(100, Math.round((card.spend_day / card.spend_limit) * 100))}
                    {@const spendTone = spendPct >= 95 ? 'critical' : spendPct >= 80 ? 'warn' : 'good'}
                    <div class="flex-1 max-w-xs">
                      <div class="relative h-1.5 rounded-full bg-[var(--fp-border)]/40 overflow-hidden">
                        <div
                          class="absolute inset-y-0 left-0 rounded-full transition-all duration-500"
                          class:bg-[var(--fp-success)]={spendTone === 'good'}
                          class:bg-[var(--fp-warning)]={spendTone === 'warn'}
                          class:bg-[var(--fp-error)]={spendTone === 'critical'}
                          style="width: {spendPct}%"
                        ></div>
                      </div>
                    </div>
                    <span class="fp-num text-[var(--fp-text)]">{card.spend_day}</span> / <span class="fp-num">{card.spend_limit}</span>
                    <span class="text-[var(--fp-dim)]">({spendPct}%)</span>
                  {:else}
                    <span class="fp-num text-[var(--fp-text)]">{card.spend_day}</span> <span class="text-[var(--fp-dim)]">{$tr('(unlimited)')}</span>
                  {/if}
                </div>

                <!-- Quota breakdown -->
                {#if card.quota && card.quota.length > 0}
                  <div class="flex flex-col gap-2">
                    <p class="text-[11px] text-[var(--fp-muted)] uppercase tracking-wider font-semibold">{$tr('Session Quotas')}</p>
                    {#each card.quota as q}
                      {@const pct = q.limit > 0 ? Math.min(100, Math.round((q.recent / q.limit) * 100)) : 0}
                      {@const tone = pct >= 95 ? 'critical' : pct >= 80 ? 'warn' : 'good'}
                      <div class="flex flex-col gap-1 px-2 py-1.5 rounded bg-[var(--fp-bg)]/40">
                        <div class="flex items-center gap-2 sm:gap-4 text-xs">
                          <code class="fp-num text-[var(--fp-text)] sm:w-48 shrink-0 truncate">{q.model}</code>
                          <span class="fp-num text-[var(--fp-muted)]">
                            <span class="text-[var(--fp-text)]">{q.recent}</span> / {q.limit}
                          </span>
                          <span class="fp-num text-[var(--fp-dim)] sm:ml-auto">{q.period}</span>
                        </div>
                      </div>
                    {/each}
                  </div>
                {:else}
                  <p class="text-xs text-[var(--fp-dim)] italic">{$tr('No quota data — session not yet admitted.')}</p>
                {/if}

                <!-- Rate limit activity -->
                {#if card.rate_limit_rate > 0}
                  <div class="flex items-center gap-3 text-xs text-[var(--fp-dim)]">
                    <span>{$tr('Rate limit')}:</span>
                    <span class="fp-num">{card.rate_limit_rate}</span> {$tr('req/s')}
                    <span class="text-[var(--fp-success)]">{$tr('hits')} <span class="fp-num text-[var(--fp-text)]">{card.rate_limit_hits}</span></span>
                    <span class="text-[var(--fp-warning)]">{$tr('misses')} <span class="fp-num text-[var(--fp-text)]">{card.rate_limit_misses}</span></span>
                  </div>
                {/if}

                <!-- Ban info -->
                {#if card.ban_type}
                  <div class="flex items-center gap-2 text-xs">
                    {#if card.ban_type === 'hard'}
                      <StatusBadge status={$tr('banned — appeal required')} tone="critical" pulse />
                    {:else}
                      <StatusBadge status={$tr('banned until {time}', { time: formatLocalDate(card.banned_until) || card.banned_until })} tone="bad" />
                    {/if}
                  </div>
                {/if}

                <!-- Cooldown info -->
                {#if card.cooldown_until}
                  <div class="text-xs text-[var(--fp-warning)]">
                    {$tr('Cooldown until')} <span class="fp-num">{formatLocalDate(card.cooldown_until) || card.cooldown_until}</span>
                  </div>
                {/if}

                <!-- Actions -->
                <div class="flex items-center gap-2 pt-1 border-t border-[var(--fp-border)]/30">
                  {#if card.locked}
                    <Button
                      variant="secondary"
                      size="sm"
                      disabled={actionPending}
                      onclick={() => triggerAction(`/admin/bridge-tokens/${card.key}/unlock`, {}, $tr('Unlock bridge token?'))}
                    >
                      <Unlock size={13} />
                      <span>{$tr('Unlock')}</span>
                    </Button>
                  {:else}
                    <Button
                      variant="ghost"
                      size="sm"
                      disabled={actionPending}
                      onclick={() => triggerAction(`/admin/bridge-tokens/${card.key}/lock`, {}, $tr('Lock bridge token?'))}
                    >
                      <Lock size={13} />
                      <span>{$tr('Lock')}</span>
                    </Button>
                  {/if}
                </div>
              </div>
            {/each}
          </div>
        {/if}
      </Card>
    {/if}

    <!-- Token table -->
    <Card
      title={$tr('Pool Tokens')}
      description={data ? $tr('{count} pooled token(s)', { count: data.token_count || 0 }) : ''}
      pad="none"
    >
      {#if loading}
        <div class="p-4 flex flex-col gap-3">
          <div class="skeleton skeleton-text w-1/3"></div>
          <div class="skeleton skeleton-line"></div>
          <div class="skeleton skeleton-line"></div>
          <div class="skeleton skeleton-line"></div>
          <div class="skeleton skeleton-line"></div>
        </div>
      {:else if error}
        <EmptyState
          title={$tr('Could not load tokens')}
          description={error}
        >
          {#snippet action()}
            <Button variant="secondary" onclick={() => { error = ''; fetchData(); }}>
              {$tr('Retry')}
            </Button>
          {/snippet}
        </EmptyState>
      {:else if !data?.tokens || data.tokens.length === 0}
        <EmptyState
          title={$tr('No tokens in pool')}
          description={$tr('Add one above or use Device Login to generate credentials via browser.')}
        />
      {:else}
        <table class="fp-table w-full">
          <thead>
            <tr>
              <th class="w-8"></th>
              <th>{$tr('Token')}</th>
              <th>{$tr('Status')}</th>
              <th>{$tr('Instance')}</th>
              <th class="num">{$tr('Cooldown')}</th>
              <th class="text-right">{$tr('Actions')}</th>
            </tr>
          </thead>
          <tbody>
            {#each data.tokens as token, i (token.index ?? i)}
              {@const idx = token.index ?? i}
              {@const st = statusFor(token)}
              {@const isExpanded = expandedToken === idx}
              <tr>
                <td class="w-8">
                  <button
                    type="button"
                    onclick={() => toggleExpand(idx)}
                    aria-expanded={isExpanded}
                    aria-label={isExpanded ? `Collapse quotas for token ${idx}` : `Expand quotas for token ${idx}`}
                    class="inline-flex items-center justify-center w-6 h-6 text-[var(--fp-dim)] hover:text-[var(--fp-text)]"
                  >
                    {#if isExpanded}
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
                        onclick={() => triggerAction(`/admin/tokens/${idx}/unlock`, {}, $tr('Clear cooldown for token {idx}? Only do this if the lock is stale.', { idx }))}
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
                        onclick={() => triggerAction(`/admin/tokens/${idx}/unlock-lock`, {}, $tr('Unlock token {idx}?', { idx }))}
                      >
                        <Unlock size={13} />
                        <span>{$tr('Unlock')}</span>
                      </Button>
                    {:else}
                      <Button
                        variant="ghost"
                        size="sm"
                        disabled={actionPending}
                        onclick={() => triggerAction(`/admin/tokens/${idx}/lock`, {}, $tr('Lock token {idx}?', { idx }))}
                      >
                        <Lock size={13} />
                        <span>{$tr('Lock')}</span>
                      </Button>
                    {/if}
                    <Button
                      variant="danger"
                      size="sm"
                      disabled={actionPending}
                      onclick={() => triggerAction('/admin/tokens/remove-specific', { token: token.token_value || '' }, $tr('Remove token {idx} from the pool and database?', { idx }))}
                    >
                      <Trash2 size={13} />
                      <span>{$tr('Remove')}</span>
                    </Button>
                  </div>
                </td>
              </tr>
              {#if isExpanded}
                <tr>
                  <td colspan="6" class="!p-0">
                    <div class="fp-inset m-2 rounded p-3">
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
                          <div class="flex items-center justify-between">
                            <p class="text-xs text-[var(--fp-muted)] uppercase tracking-wider font-semibold">{$tr('Session quotas')}</p>
                            {#if allQuotaModels.length > 1}
                              <select
                                bind:value={quotaModelFilter}
                                class="text-[11px] font-mono bg-[var(--fp-bg)] border border-[var(--fp-border)] rounded px-2 py-1 text-[var(--fp-muted)]"
                              >
                                <option value="">{$tr('All models')}</option>
                                {#each allQuotaModels as m}
                                  <option value={m}>{m}</option>
                                {/each}
                              </select>
                            {/if}
                          </div>
                          {#each (quotaModelFilter ? token.quota.filter(q => q.model === quotaModelFilter) : token.quota) as q}
                            {@const pct = quotaPercent(q.recent, q.limit)}
                            {@const tone = quotaUsageTone(pct)}
                            <div class="flex flex-col gap-1 px-2 py-2 rounded bg-[var(--fp-bg)]/40">
                              <div class="flex items-center gap-2 sm:gap-4 text-xs">
                                <code class="fp-num text-[var(--fp-text)] sm:w-48 shrink-0 truncate">{q.model}</code>
                                <span class="fp-num text-[var(--fp-muted)]">
                                  <span class="text-[var(--fp-text)]">{q.recent}</span> / {q.limit}
                                  {#if q.limit !== '0' && q.limit !== ''}
                                    <span class="text-[var(--fp-dim)]">
                                      ({$tr('remaining {count}', { count: Math.max(0, parseFloat(q.limit) - parseFloat(q.recent)) })})
                                    </span>
                                  {/if}
                                </span>
                                <span class="fp-num text-[var(--fp-dim)] sm:ml-auto">
                                  {q.period}{#if q.has_entitlement} · {$tr('entitled')} {q.entitled}{/if}
                                </span>
                                <span class="fp-num text-[var(--fp-dim)]">
                                  {#if q.resets_in}{$tr('reset')} {formatLocalDate(q.reset_at_utc) || q.reset_at} ({q.resets_in}){:else}{$tr('reset')} {formatLocalDate(q.reset_at_utc) || q.reset_at}{/if}
                                </span>
                              </div>
                              <!-- Visual usage bar -->
                              {#if q.limit !== '0' && q.limit !== ''}
                                <div class="relative h-1.5 rounded-full bg-[var(--fp-border)]/40 overflow-hidden" role="progressbar" aria-valuenow={pct} aria-valuemin="0" aria-valuemax="100" aria-label={`${q.model} usage ${pct}%`}>
                                  <div
                                    class="absolute inset-y-0 left-0 rounded-full transition-all duration-500"
                                    class:bg-[var(--fp-success)]={tone === 'good'}
                                    class:bg-[var(--fp-warning)]={tone === 'warn'}
                                    class:bg-[var(--fp-error)]={tone === 'critical'}
                                    style="width: {pct}%"
                                  ></div>
                                </div>
                                <span class="text-[10px] font-mono {tone === 'critical' ? 'text-[var(--fp-error)]' : tone === 'warn' ? 'text-[var(--fp-warning)]' : 'text-[var(--fp-dim)]'}">
                                  {pct}% {$tr('used')}
                                </span>
                              {/if}
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
              <TokenCard
                {token}
                {idx}
                expanded={expandedToken === idx}
                bind:spawnModel={spawnModels[idx]}
                {actionPending}
                {now}
                {devToolsEnabled}
                onToggle={() => toggleExpand(idx)}
                onAction={(action) => handleTokenAction(token, idx, action)}
                onSpawn={(model) => handleSpawn(idx, model)}
                onRefresh={(action) => handleRefresh(idx, action)}
              />
            {/each}
          </tbody>
        </table>
      {/if}
    </Card>
    {#if data?.show_bridge && data?.bridge_token_cards?.length > 0}
      <Card
        title={$tr('Bridge Clients')}
        description={$tr('{count} active bridge client(s) relaying their own FreeBuff tokens', { count: data.bridge_token_cards.length })}
        pad="none"
      >
        <div class="flex flex-col gap-3 p-4">
          {#each data.bridge_token_cards as bc}
            <BridgeTokenCard card={bc} {now} />
          {/each}
        </div>
      </Card>
    {/if}
  </div>
</div>