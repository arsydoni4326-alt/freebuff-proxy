<script>
  import { onMount, onDestroy } from 'svelte';
  import { RefreshCw, Save, X, Search, Plus, Minus, Pencil, ChevronDown, ChevronRight } from '@lucide/svelte';
  import PageHeader from '../components/PageHeader.svelte';
  import Button from '../components/Button.svelte';
  import Card from '../components/Card.svelte';
  import Alert from '../components/Alert.svelte';
  import EmptyState from '../components/EmptyState.svelte';
  import StatusBadge from '../components/StatusBadge.svelte';
  import CopyButton from '../components/CopyButton.svelte';
  import { fetchAPI, postForm } from '../api/client.js';
  import { tr } from '../i18n.js';
  import { formatTime } from '../utils/format.js';

  let data = $state(null);
  let loading = $state(true);
  let error = $state('');

  let envContent = $state('');
  let originalContent = $state('');
  let saving = $state(false);
  let result = $state(null); // { ok, message } — save/validate outcome
  let lastSavedTime = $state(null);
  let showDiff = $state(false);
  let configSearch = $state('');
  let expandedCategories = $state(new Set(['core', 'auth']));

  let hasUnsavedChanges = $derived(envContent !== originalContent);

  // Categorized validation errors with types
  let validationErrors = $derived.by(() => {
    const errors = [];
    const lines = envContent.split('\n');
    const seenKeys = new Set();
    for (let i = 0; i < lines.length; i++) {
      const line = lines[i].trim();
      if (!line || line.startsWith('#')) continue;
      const eqIdx = line.indexOf('=');
      if (eqIdx === -1) { errors.push({ line: i + 1, type: 'syntax', message: `Missing '=' separator` }); continue; }
      const key = line.substring(0, eqIdx).trim();
      if (!key) { errors.push({ line: i + 1, type: 'syntax', message: `Empty key name` }); continue; }
      if (!/^[A-Za-z_][A-Za-z0-9_]*$/.test(key)) errors.push({ line: i + 1, type: 'format', message: `Invalid key "${key}" (use A-Z, a-z, 0-9, _)` });
      if (seenKeys.has(key)) errors.push({ line: i + 1, type: 'format', message: `Duplicate key "${key}"` });
      seenKeys.add(key);
    }
    return errors;
  });
  let envValid = $derived(validationErrors.length === 0 && envContent.trim().length > 0);
  let lastSavedTimeStr = $derived(lastSavedTime ? formatTime(lastSavedTime) : '');

  // Diff calculation: added, removed, modified keys
  let configDiff = $derived.by(() => {
    if (!hasUnsavedChanges) return { added: [], removed: [], modified: [] };
    const parseEnv = (c) => {
      const map = new Map();
      for (const line of c.split('\n')) {
        const trimmed = line.trim();
        if (!trimmed || trimmed.startsWith('#')) continue;
        const eqIdx = trimmed.indexOf('=');
        if (eqIdx === -1) continue;
        const key = trimmed.substring(0, eqIdx).trim();
        const value = trimmed.substring(eqIdx + 1).trim();
        if (key) map.set(key, value);
      }
      return map;
    };
    const origKeys = parseEnv(originalContent);
    const curKeys = parseEnv(envContent);
    const added = [];
    const removed = [];
    const modified = [];
    for (const [key, value] of curKeys) {
      if (!origKeys.has(key)) added.push(key);
      else if (origKeys.get(key) !== value) modified.push(key);
    }
    for (const [key] of origKeys) {
      if (!curKeys.has(key)) removed.push(key);
    }
    return { added, removed, modified };
  });

  // Config categories for grouping
  const configCategories = {
    core: { label: 'Core', keys: ['LISTEN_ADDR', 'AUTH_TOKENS', 'API_KEYS', 'ADMIN_TOKEN', 'SAFE_MODE', 'CONFIG_FILE'] },
    auth: { label: 'Authentication', keys: ['ADMIN_TOKEN', 'API_KEYS', 'AUTH_TOKENS'] },
    upstream: { label: 'Upstream', keys: ['BASE_URL', 'CODEBUFF_HOST', 'CODEBUFF_PORT', 'TLS_FINGERPRINT', 'STEALTH_HEADERS', 'REQUEST_JITTER_MS'] },
    dashboard: { label: 'Dashboard', keys: ['DASHBOARD_ENABLED', 'ADMIN_TOKEN'] },
    performance: { label: 'Performance', keys: ['POOL_SIZE', 'MAX_CONCURRENT_SESSIONS', 'RATE_LIMIT_PER_IP', 'RATE_LIMIT_BURST', 'RUN_FINISH_QUEUE_SIZE', 'RUNS_DRAIN_QUEUE_CAP'] },
    reasoning: { label: 'Reasoning', keys: ['REASONING_CACHE_SIZE', 'REASONING_CACHE_TTL', 'MAX_THINKING_BUDGET'] },
    safety: { label: 'Safety & Limits', keys: ['DAILY_SPEND_CAP', 'COUNTRY_BLOCK', 'FALLBACK_MODEL', 'WEBHOOK_URL'] },
  };

  let configSearchLower = $derived(configSearch.toLowerCase());

  // Filtered and grouped effective config
  let filteredEffective = $derived.by(() => {
    if (!data?.effective) return [];
    if (!configSearchLower) return data.effective;
    return data.effective.filter(kv =>
      kv.key.toLowerCase().includes(configSearchLower) ||
      (kv.value && kv.value.toLowerCase().includes(configSearchLower))
    );
  });

  let changedKeysCount = $derived.by(() => {
    const parseKeys = (c) => c.split('\n')
      .filter(l => l.trim() && !l.trim().startsWith('#') && l.includes('='))
      .map(l => l.split('=')[0].trim());
    const origKeys = new Set(parseKeys(originalContent));
    const curKeys = parseKeys(envContent);
    let count = 0;
    for (const key of curKeys) {
      const regex = new RegExp(`^\\s*${key}=(.*)$`, 'm');
      const o = originalContent.match(regex)?.[1]?.trim();
      const c = envContent.match(regex)?.[1]?.trim();
      if (o !== c) count++;
    }
    for (const key of origKeys) {
      if (!curKeys.includes(key)) count++;
    }
    return count;
  });
  let lineCount = $derived.by(() => envContent.split('\n').filter(l => l.trim()).length);
  let keyCount = $derived.by(() => envContent.split('\n').filter(l => l.trim() && !l.trim().startsWith('#') && l.includes('=')).length);

  // Auto-dismiss result banner after timeout
  $effect(() => {
    if (result) {
      const timeout = result.ok ? 5000 : 10000;
      const timer = setTimeout(() => result = null, timeout);
      return () => clearTimeout(timer);
    }
  });

  async function fetchData() {
    try {
      data = await fetchAPI('/admin/api/config');
      envContent = data.env_content || '';
      originalContent = envContent;
      error = '';
    } catch (e) {
      error = e.message || $tr('Failed to fetch configuration');
    } finally {
      loading = false;
    }
  }

  function validateConfig() {
    if (saving) return;
    if (!envContent.trim()) {
      result = { ok: false, message: $tr('Configuration is empty — nothing to save.') };
      return;
    }
    if (validationErrors.length === 0) {
      result = { ok: true, message: $tr('Configuration is valid — {count} key(s) parsed.', { count: keyCount }) };
    } else {
      const shown = validationErrors.slice(0, 5).map((e) => `L${e.line} ${e.message}`).join('; ');
      const more = validationErrors.length > 5 ? ` (+${validationErrors.length - 5} more)` : '';
      result = { ok: false, message: $tr('Configuration invalid ({count}): {detail}', { count: validationErrors.length, detail: `${shown}${more}` }) };
    }
  }

  async function saveConfig(e, opts = {}) {
    e?.preventDefault();
    if (saving || !hasUnsavedChanges) return;
    if (opts.confirm !== false && !window.confirm($tr('Save the .env file and reload the proxy with these changes?'))) {
      return;
    }
    saving = true;
    result = null;

    try {
      const res = await postForm('/admin/config', { content: envContent });
      const json = await res.json();
      result = {
        ok: res.ok && json.ok,
        message: json.message || (res.ok ? $tr('Configuration saved and reloaded.') : $tr('Save failed')),
      };
      if (result.ok) {
        lastSavedTime = new Date();
        await fetchData();
      }
    } catch (e) {
      result = { ok: false, message: e.message || $tr('Network error saving configuration') };
    } finally {
      saving = false;
    }
  }

  function handleBeforeUnload(e) {
    if (hasUnsavedChanges) {
      e.preventDefault();
      e.returnValue = '';
    }
  }

  function handleKeyDown(e) {
    if ((e.ctrlKey || e.metaKey) && e.key === 's') {
      e.preventDefault();
      if (hasUnsavedChanges && !saving) {
        saveConfig(null, { confirm: false });
      }
    }
  }

  onMount(() => {
    fetchData();
    window.addEventListener('beforeunload', handleBeforeUnload);
    window.addEventListener('keydown', handleKeyDown);
  });

  onDestroy(() => {
    window.removeEventListener('beforeunload', handleBeforeUnload);
    window.removeEventListener('keydown', handleKeyDown);
  });
</script>

<div class="space-y-6 page-enter">
  <PageHeader title={$tr('Config')} description={$tr('Runtime .env editor — Save writes the file and reloads the running proxy.')}>
    {#snippet actions()}
      <Button variant="ghost" onclick={fetchData}>
        <RefreshCw size={15} />
        {$tr('Reload')}
      </Button>
      <Button
        variant="primary"
        onclick={saveConfig}
        disabled={saving || !hasUnsavedChanges}
        loading={saving}
      >
        <Save size={15} />
        {$tr('Save')}
      </Button>
    {/snippet}
  </PageHeader>

  {#if loading}
    <div class="space-y-6">
      <div class="skeleton skeleton-card"></div>
      <div class="skeleton skeleton-card"></div>
    </div>
  {:else if error}
    <div class="space-y-4">
      <Alert tone="error">{error}</Alert>
      <div>
        <Button variant="secondary" onclick={fetchData}>
          <RefreshCw size={15} />
          {$tr('Retry')}
        </Button>
      </div>
    </div>
  {:else}
    {#if result}
      <Alert tone={result.ok ? 'success' : 'error'}>
        <div class="flex items-center justify-between gap-3">
          <span>{result.message}</span>
          <button
            type="button"
            onclick={() => result = null}
            class="text-[var(--fp-dim)] hover:text-[var(--fp-text)] transition-colors shrink-0"
            aria-label={$tr('Dismiss alert')}
          >
            <X size={14} />
          </button>
        </div>
      </Alert>
    {/if}

    <div class="grid grid-cols-1 lg:grid-cols-12 gap-6">
      <!-- Editor -->
      <div class="lg:col-span-7">
        <Card
          title={$tr('.env Editor')}
          description={$tr('Edit environment variables. Save validates server-side and reloads; rejected writes are rolled back.')}
        >
          {#snippet actions()}
            {#if data}
              <StatusBadge
                status={data.has_env_file ? $tr('env loaded') : $tr('no env file')}
                tone={data.has_env_file ? 'good' : 'warn'}
              />
            {/if}
            {#if hasUnsavedChanges}
              <StatusBadge
                status={$tr('{count} changed', { count: changedKeysCount })}
                tone="warn"
                pulse
              />
            {/if}
          {/snippet}

          <form onsubmit={saveConfig}>
            <label for="config-env" class="sr-only">{$tr('Environment file content')}</label>
            <textarea
              id="config-env"
              bind:value={envContent}
              rows="18"
              spellcheck="false"
              class="fp-input fp-mono w-full text-[13px] p-3.5
                {validationErrors.length > 0 ? 'border-[var(--fp-error)]/60' : envValid ? 'border-[var(--fp-success)]/40' : ''}"
              placeholder="# Configuration variables..."
            ></textarea>

            {#if validationErrors.length > 0}
              <div role="alert" aria-live="polite" class="mt-3 p-3 rounded-[var(--fp-radius-sm)] fp-inset border-[var(--fp-error)]/30 space-y-1.5">
                <p class="text-xs font-semibold text-[var(--fp-error)]">
                  {$tr('{count} validation error(s):', { count: validationErrors.length })}
                </p>
                {#each validationErrors.slice(0, 5) as err}
                  <p class="text-[11px] font-mono text-[var(--fp-error)]/80 flex items-center gap-1.5">
                    <span class="text-[10px] px-1 py-0.5 rounded bg-[var(--fp-error)]/20 text-[var(--fp-error)]">L{err.line}</span>
                    <span class="text-[10px] px-1 py-0.5 rounded bg-[var(--fp-warning)]/20 text-[var(--fp-warning)] uppercase">{err.type}</span>
                    {err.message}
                  </p>
                {/each}
                {#if validationErrors.length > 5}
                  <p class="text-[11px] text-[var(--fp-dim)]">… {$tr('and {count} more', { count: validationErrors.length - 5 })}</p>
                {/if}
              </div>
            {/if}

            <!-- Diff preview -->
            {#if hasUnsavedChanges && (configDiff.added.length > 0 || configDiff.removed.length > 0 || configDiff.modified.length > 0)}
              <div class="mt-4">
                <button
                  type="button"
                  class="flex items-center gap-2 text-xs text-[var(--fp-muted)] hover:text-[var(--fp-text)] transition-colors"
                  onclick={() => showDiff = !showDiff}
                >
                  {#if showDiff}
                    <ChevronDown size={14} />
                  {:else}
                    <ChevronRight size={14} />
                  {/if}
                  <span>{$tr('Changes preview')}</span>
                  <span class="text-[10px] px-1.5 py-0.5 rounded bg-[var(--fp-accent)]/20 text-[var(--fp-accent)] font-mono">
                    {configDiff.added.length + configDiff.removed.length + configDiff.modified.length} {$tr('keys changed')}
                  </span>
                </button>
                {#if showDiff}
                  <div class="mt-2 fp-inset rounded p-3 space-y-2 text-xs font-mono">
                    {#each configDiff.added as key}
                      <div class="flex items-center gap-2 text-[var(--fp-success)]">
                        <Plus size={12} />
                        <span class="font-semibold">{key}</span>
                        <span class="text-[var(--fp-dim)]">{$tr('(new)')}</span>
                      </div>
                    {/each}
                    {#each configDiff.modified as key}
                      <div class="flex items-center gap-2 text-[var(--fp-warning)]">
                        <Pencil size={12} />
                        <span class="font-semibold">{key}</span>
                        <span class="text-[var(--fp-dim)]">{$tr('(modified)')}</span>
                      </div>
                    {/each}
                    {#each configDiff.removed as key}
                      <div class="flex items-center gap-2 text-[var(--fp-error)]">
                        <Minus size={12} />
                        <span class="font-semibold">{key}</span>
                        <span class="text-[var(--fp-dim)]">{$tr('(removed)')}</span>
                      </div>
                    {/each}
                  </div>
                {/if}
              </div>
            {/if}

            <div class="mt-4 flex flex-col sm:flex-row sm:items-center justify-between gap-3">
              <div class="flex items-center gap-3 text-[11px] text-[var(--fp-dim)] font-mono">
                <span>{lineCount} {$tr('lines')}</span>
                <span class="text-[var(--fp-border-bright)]">|</span>
                <span>{keyCount} {$tr('keys')}</span>
                {#if lastSavedTimeStr}
                  <span class="text-[var(--fp-border-bright)]">|</span>
                  <span>{$tr('saved {time}', { time: lastSavedTimeStr })}</span>
                {/if}
              </div>
              <div class="flex items-center gap-2">
                <Button variant="secondary" onclick={validateConfig} disabled={saving}>
                  {$tr('Validate')}
                </Button>
              </div>
            </div>
          </form>
          <p class="mt-3 text-[11px] text-[var(--fp-dim)]">
            {$tr('Changes take effect after save.')} <kbd class="px-1.5 py-0.5 rounded-[var(--fp-radius-sm)] bg-[var(--fp-surface-2)] text-[10px] font-mono text-[var(--fp-muted)]">Ctrl+S</kbd> {$tr('saves from the keyboard')}.
          </p>
        </Card>
      </div>

      <!-- Effective config -->
      <div class="lg:col-span-5">
        <Card title={$tr('Effective Configuration')} description={$tr('Read-only view of the running configuration. Secret values are masked.')}>
          {#snippet actions()}
            <StatusBadge
              status={`${data?.effective?.length || 0} ${$tr('keys')}`}
              tone={data?.effective?.length ? 'good' : 'warn'}
            />
          {/snippet}

          {#if data?.effective?.length}
            <!-- Search filter -->
            <div class="relative mb-3">
              <Search size={14} class="absolute left-3 top-1/2 -translate-y-1/2 text-[var(--fp-dim)]" />
              <input
                type="text"
                bind:value={configSearch}
                placeholder={$tr('Search config keys…')}
                class="fp-input fp-mono w-full pl-9 pr-3 py-2 text-[12px]"
              />
            </div>

            <!-- Category groups -->
            <div class="space-y-2">
              {#each Object.entries(configCategories) as [catId, cat]}
                {@const catKeys = data.effective.filter(kv => cat.keys.includes(kv.key))}
                {#if catKeys.length > 0 && (!configSearchLower || catKeys.some(kv => kv.key.toLowerCase().includes(configSearchLower) || (kv.value && kv.value.toLowerCase().includes(configSearchLower))))}
                  <div class="rounded border border-[var(--fp-border)] overflow-hidden">
                    <button
                      type="button"
                      class="flex items-center justify-between w-full px-3 py-2 text-xs font-semibold text-[var(--fp-muted)] uppercase tracking-wider hover:bg-[var(--fp-surface-2)]/50 transition-colors"
                      onclick={() => {
                        if (expandedCategories.has(catId)) {
                          const next = new Set(expandedCategories);
                          next.delete(catId);
                          expandedCategories = next;
                        } else {
                          const next = new Set(expandedCategories);
                          next.add(catId);
                          expandedCategories = next;
                        }
                      }}
                    >
                      <span>{cat.label}</span>
                      <span class="flex items-center gap-2">
                        <span class="text-[10px] text-[var(--fp-dim)] font-mono">{catKeys.length}</span>
                        {#if expandedCategories.has(catId)}<ChevronDown size={12} />{:else}<ChevronRight size={12} />{/if}
                      </span>
                    </button>
                    {#if expandedCategories.has(catId)}
                      <table class="fp-table border-t border-[var(--fp-border)]">
                        <thead class="sr-only">
                          <tr><th>{$tr('Key')}</th><th>{$tr('Value')}</th></tr>
                        </thead>
                        <tbody>
                          {#each catKeys as kv}
                            <tr>
                              <td>
                                <div class="flex items-center gap-2 min-w-0">
                                  <span class="fp-num text-[11px] font-semibold text-[var(--fp-text)] truncate">{kv.key}</span>
                                  {#if kv.secret}
                                    <span class="text-[10px] px-1.5 py-0.5 rounded-[var(--fp-radius-sm)] border border-[var(--fp-error)]/40 bg-[var(--fp-error)]/15 text-[#FCA5A5] font-semibold uppercase tracking-wider shrink-0">{$tr('secret')}</span>
                                  {/if}
                                </div>
                              </td>
                              <td>
                                <div class="flex items-center gap-2 min-w-0">
                                  <span class="fp-num text-[11px] text-[var(--fp-muted)] truncate max-w-[180px] font-mono">
                                    {kv.secret ? '••••••••' : (kv.value || '—')}
                                  </span>
                                  {#if kv.value}
                                    <span class="shrink-0"><CopyButton text={kv.value} label={$tr('copy')} /></span>
                                  {/if}
                                </div>
                              </td>
                            </tr>
                          {/each}
                        </tbody>
                      </table>
                    {/if}
                  </div>
                {/if}
              {/each}
            </div>

            <!-- Keys not in any category -->
            {@const uncategorized = data.effective.filter(kv =>
              !Object.values(configCategories).some(cat => cat.keys.includes(kv.key))
            )}
            {#if uncategorized.length > 0 && (!configSearchLower || uncategorized.some(kv => kv.key.toLowerCase().includes(configSearchLower)))}
              <div class="rounded border border-[var(--fp-border)] overflow-hidden mt-2">
                <button
                  type="button"
                  class="flex items-center justify-between w-full px-3 py-2 text-xs font-semibold text-[var(--fp-dim)] uppercase tracking-wider hover:bg-[var(--fp-surface-2)]/50 transition-colors"
                  onclick={() => {
                    if (expandedCategories.has('_other')) {
                      const next = new Set(expandedCategories); next.delete('_other'); expandedCategories = next;
                    } else {
                      const next = new Set(expandedCategories); next.add('_other'); expandedCategories = next;
                    }
                  }}
                >
                  <span>{$tr('Other')}</span>
                  <span class="flex items-center gap-2">
                    <span class="text-[10px] text-[var(--fp-dim)] font-mono">{uncategorized.length}</span>
                    {#if expandedCategories.has('_other')}<ChevronDown size={12} />{:else}<ChevronRight size={12} />{/if}
                  </span>
                </button>
                {#if expandedCategories.has('_other')}
                  <table class="fp-table border-t border-[var(--fp-border)]">
                    <thead class="sr-only"><tr><th>{$tr('Key')}</th><th>{$tr('Value')}</th></tr></thead>
                    <tbody>
                      {#each (configSearchLower ? uncategorized.filter(kv => kv.key.toLowerCase().includes(configSearchLower)) : uncategorized) as kv}
                        <tr>
                          <td>
                            <div class="flex items-center gap-2 min-w-0">
                              <span class="fp-num text-[11px] font-semibold text-[var(--fp-text)] truncate">{kv.key}</span>
                              {#if kv.secret}
                                <span class="text-[10px] px-1.5 py-0.5 rounded-[var(--fp-radius-sm)] border border-[var(--fp-error)]/40 bg-[var(--fp-error)]/15 text-[#FCA5A5] font-semibold uppercase tracking-wider shrink-0">{$tr('secret')}</span>
                              {/if}
                            </div>
                          </td>
                          <td>
                            <div class="flex items-center gap-2 min-w-0">
                              <span class="fp-num text-[11px] text-[var(--fp-muted)] truncate max-w-[180px] font-mono">{kv.secret ? '••••••••' : (kv.value || '—')}</span>
                              {#if kv.value}
                                <span class="shrink-0"><CopyButton text={kv.value} label={$tr('copy')} /></span>
                              {/if}
                            </div>
                          </td>
                        </tr>
                      {/each}
                    </tbody>
                  </table>
                {/if}
              </div>
            {/if}
          {:else}
            <EmptyState title={$tr('No effective configuration')} description={$tr('Start the proxy to populate this view.')}>
              {#snippet action()}
                <Button variant="secondary" onclick={fetchData}>
                  <RefreshCw size={15} />
                  {$tr('Refresh')}
                </Button>
              {/snippet}
            </EmptyState>
          {/if}
        </Card>
      </div>
    </div>
  {/if}
</div>
