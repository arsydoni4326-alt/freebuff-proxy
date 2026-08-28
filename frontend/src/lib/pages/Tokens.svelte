<script>
  import { onDestroy } from 'svelte';
  import {
    LogIn,
    Key,
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
  import { fetchAPI, postAPI } from '../api/client.js';
  import { usePolling } from '../utils/polling.js';
  import { generateRandomApiKey } from '../utils/format.js';
  import { tr } from '../i18n.js';

  let data = $state(null);
  let loading = $state(true);
  let error = $state('');

  // Add-token form
  let newToken = $state('');
  let adding = $state(false);
  let actionMessage = $state('');
  let actionOK = $state(true);

  // Client API-key management (API_KEYS in .env)
  let apiKeys = $state([]);
  let clientKeyMessage = $state('');
  let clientKeyOK = $state(true);
  let generatingKey = $state(false);
  let generatedKey = $state('');

  // Device login flow
  let oauthStarting = $state(false);
  let oauthStatus = $state(null);
  let oauthTimer = null;

  // Token table
  let expandedToken = $state(null);
  let spawnModels = $state({});
  let actionPending = $state(false);
  let now = $state(Date.now());

  const tokenValid = $derived(
    newToken.trim() === ''
      ? null
      : /^cb_[A-Za-z0-9_-]{20,}$/.test(newToken.trim())
  );

  async function fetchData() {
    try {
      data = await fetchAPI('/admin/api/tokens');
      try {
        const cfgRes = await fetchAPI('/admin/api/config');
        const envContent = cfgRes?.env_content || '';
        const m = envContent.match(/^\s*API_KEYS=(.*)$/m);
        const val = m ? m[1].trim() : '';
        apiKeys = val ? val.split(',').map((s) => s.trim()).filter(Boolean) : [];
      } catch {
        apiKeys = [];
      }
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
      const result = await postAPI('/admin/tokens/add', { token: newToken.trim() });
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
        return triggerAction(`/admin/tokens/${idx}/unlock`, {}, $tr('Clear cooldown for token {idx}? Only do this if the lock is stale.', { idx }));
      case 'unlock':
        return triggerAction(`/admin/tokens/${idx}/unlock-lock`, {}, $tr('Unlock token {idx}?', { idx }));
      case 'lock':
        return triggerAction(`/admin/tokens/${idx}/lock`, {}, $tr('Lock token {idx}?', { idx }));
      case 'remove':
        return triggerAction('/admin/tokens/remove', { token: idx }, $tr('Remove token {idx} from the pool and .env?', { idx }));
      default:
        return;
    }
  }

  function handleSpawn(idx, model) {
    const m = model || 'mimo/mimo-v2.5';
    triggerAction(`/admin/tokens/${idx}/session`, { model: m }, $tr('Create upstream session for token #{idx} on {model}?', { idx, model: m }));
  }

  function handleRefresh(idx, action) {
    if (action === 'probe') {
      return triggerAction(`/admin/tokens/${idx}/test`, {}, $tr('Probe token #{idx} against upstream?', { idx }));
    }
    return triggerAction(`/admin/tokens/${idx}/finish`, {}, $tr('Finish active runs on token #{idx}?', { idx }));
  }

  async function generateClientKey() {
    if (generatingKey) return;
    generatingKey = true;
    generatedKey = '';
    clientKeyMessage = '';
    try {
      const newKey = generateRandomApiKey();
      const cfgRes = await fetchAPI('/admin/api/config');
      const envContent = cfgRes?.env_content || '';
      const regex = /^\s*API_KEYS=(.*)$/m;
      const match = envContent.match(regex);
      const existing = match ? match[1].trim() : '';
      const updated = existing ? `${existing},${newKey}` : newKey;
      const save = await fetch('/admin/config', {
        method: 'POST',
        headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
        body: new URLSearchParams({ content: envContent.replace(regex, `API_KEYS=${updated}`) }),
      });
      const result = await save.json();
      clientKeyOK = save.ok && result.ok;
      clientKeyMessage = clientKeyOK
        ? $tr('Generated & saved client API key')
        : (result.message || $tr('Failed to save client API key'));
      if (clientKeyOK) {
        generatedKey = newKey;
        fetchData();
      }
    } catch (e) {
      clientKeyOK = false;
      clientKeyMessage = e.message || $tr('Network error generating client key');
    } finally {
      generatingKey = false;
    }
  }

  async function startOAuthLogin() {
    oauthStarting = true;
    oauthStatus = { message: $tr('Starting headless login flow…'), type: 'info' };

    try {
      const res = await fetch('/admin/login/start', { method: 'POST' });
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
            const pollRes = await fetch(`/admin/login/status?fingerprint=${encodeURIComponent(result.fingerprint)}`);
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

  onDestroy(() => {
    clearInterval(oauthTimer);
    clearInterval(tick);
  });
</script>

<div class="page-enter">
  <div class="flex flex-col gap-6">
    <PageHeader title={$tr('Tokens')} description={$tr('Upstream credentials, device login, client API keys, and per-token session quotas')}>
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
        <Button
          variant="primary"
          size="sm"
          onclick={generateClientKey}
          disabled={generatingKey}
        >
          {#if generatingKey}
            <RefreshCw size={14} class="animate-spin" />
            <span>{$tr('Generating…')}</span>
          {:else}
            <Key size={14} />
            <span>{$tr('Generate API Key')}</span>
          {/if}
        </Button>
      {/snippet}
    </PageHeader>

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
    <Card title={$tr('Add Token to Pool')} description={$tr('Paste a FreeBuff auth token (cb_…) to add it to the shared pool and save it to .env. Adding burns no quota.')}>
      <form onsubmit={addToken} class="flex flex-col sm:flex-row items-start sm:items-end gap-3">
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
              <TokenCard
                {token}
                {idx}
                expanded={expandedToken === idx}
                bind:spawnModel={spawnModels[idx]}
                {actionPending}
                {now}
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
  </div>
</div>