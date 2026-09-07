<script>
  import { onMount } from "svelte";
  import { LogIn, Plus, ExternalLink, RefreshCw, Lock, Unlock } from "@lucide/svelte";
  import Button from "../components/Button.svelte";
  import Card from "../components/Card.svelte";
  import Alert from "../components/Alert.svelte";
  import CopyButton from "../components/CopyButton.svelte";
  import PageHeader from "../components/PageHeader.svelte";
  import StatusBadge from "../components/StatusBadge.svelte";
  import EmptyState from "../components/EmptyState.svelte";
  import PageShell from "../components/PageShell.svelte";
  import BridgeTokenCard from "../components/BridgeTokenCard.svelte";
  import TokenTable from "./tokens/TokenTable.svelte";
  import ToggleSwitch from "../components/ToggleSwitch.svelte";
  import { fetchAPI, postAPI, postForm, csrfHeader } from "../api/client.js";
  import { adminApi, adminActions, tokenActions } from "../api/paths.js";
  import { isDevToolsEnabled } from "../utils/devtools.js";
  import {
    tokensData as tokensStore,
    tokensError as tokensErrorStore,
    ensureTokensStore,
    refreshTokens,
    probeAllQuotas,
  } from "../stores/tokens.js";
  import { getEnvValue, setEnvValue } from "../utils/env.js";
  import { tr } from "../i18n.js";
  import { spawnIntent, intentAskLine } from "../utils/freebucks.js";
  import { confirmAction } from "../stores/confirm.js";
  let data = $state(null);
  let loading = $state(true);
  let error = $state("");
  let unsubStore = null;
  let unsubErr = null;

  // Add-token form
  let newToken = $state("");
  let adding = $state(false);
  let actionMessage = $state("");
  let actionOK = $state(true);
  // Dev Tools surfaces (per-token session spawn toolbar) are hidden unless
  // the operator enables DEVTOOLS_ENABLED=true in .env (same gate as the
  // sidebar's Dev Tools tab and the server-side DevTools route).
  let devToolsEnabled = $state(false);
  // Token rotation strategy (TOKEN_ROTATION in .env)
  let tokenRotation = $state("drain");
  let savingRotation = $state(false);
  // Auto failover to another token on rate limit (RATE_LIMIT_FAILOVER in .env)
  let rateLimitFailover = $state(true);
  let savingFailover = $state(false);
  async function setTokenRotation(newMode) {
    if (savingRotation || tokenRotation === newMode) return;
    savingRotation = true;
    try {
      const cfgRes = await fetchAPI(adminApi.config);
      const envContent = cfgRes?.env_content || "";
      const newContent = setEnvValue(envContent, "TOKEN_ROTATION", newMode);
      const save = await postForm(adminActions.configSave, {
        content: newContent,
      });
      if (save.ok) {
        tokenRotation = newMode;
        refreshTokens();
      }
    } catch (e) {
      console.warn("Failed to update token rotation", e);
    } finally {
      savingRotation = false;
    }
  }

  async function toggleRateLimitFailover(next) {
    if (savingFailover) return;
    savingFailover = true;
    const nextVal = typeof next === "boolean" ? next : !rateLimitFailover;
    try {
      const cfgRes = await fetchAPI(adminApi.config);
      const envContent = cfgRes?.env_content || "";
      const newContent = setEnvValue(
        envContent,
        "RATE_LIMIT_FAILOVER",
        String(nextVal),
      );
      const save = await postForm(adminActions.configSave, {
        content: newContent,
      });
      if (save.ok) {
        rateLimitFailover = nextVal;
        refreshTokens();
      }
    } catch (e) {
      console.warn("Failed to update rate limit failover", e);
    } finally {
      savingFailover = false;
    }
  }

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
  let actionPending = $state(false);
  let probingAll = $state(false);
  let now = $state(Date.now());
  let spawnModels = $state({});
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
    newToken.trim() === ""
      ? null
      : !newToken.trim().toLowerCase().startsWith("bearer ") &&
          !/[,\s]/.test(newToken.trim()) &&
          newToken.trim().length >= 10,
  );

  function applyTokens(v) {
    if (!v) return;
    data = v;
    if (v.token_rotation) {
      tokenRotation = v.token_rotation;
    }
    if (v.rate_limit_failover !== undefined) {
      rateLimitFailover = v.rate_limit_failover;
    }
    // Seed the per-token spawn-model map so no TokenCard binding ever sees
    // with a fallback (props_invalid_value) and unmounts the table.
    (v?.tokens ?? []).forEach((t, i) => {
      const idx = t.index ?? i;
      if (!(idx in spawnModels)) spawnModels[idx] = "";
    });
    error = "";
    loading = false;
  }

  async function addToken(e) {
    e.preventDefault();
    if (!newToken.trim() || tokenValid === false || adding) return;
    adding = true;
    try {
      const result = await postAPI(adminActions.tokenAdd, {
        token: newToken.trim(),
      });
      actionOK = result.ok !== false;
      actionMessage =
        result.message ||
        (actionOK
          ? $tr("Token added successfully")
          : $tr("Failed to add token"));
      if (actionOK) {
        newToken = "";
        refreshTokens();
      }
    } catch (e) {
      actionOK = false;
      actionMessage = e.message || $tr("Network error adding token");
    } finally {
      adding = false;
    }
  }

  async function triggerAction(
    url,
    body,
    confirmMsg,
    title,
    tone = "warn",
    confirmText = "",
  ) {
    if (confirmMsg) {
      const ok = await confirmAction({
        title: title || $tr("Confirm Action"),
        message: confirmMsg,
        confirmText: confirmText || $tr("Confirm"),
        tone,
      });
      if (!ok) return;
    }
    actionPending = true;
    try {
      const result = await postAPI(url, body || undefined);
      actionOK = result.ok !== false;
      actionMessage =
        result.message ||
        (actionOK ? $tr("Action completed") : $tr("Action failed"));
      refreshTokens();
    } catch (e) {
      actionOK = false;
      actionMessage = e.message || $tr("Network error executing action");
    } finally {
      actionPending = false;
    }
  }

  function handleTokenAction(token, idx, action) {
    switch (action) {
      case "clear":
        return triggerAction(
          tokenActions.unlock(idx),
          {},
          $tr(
            "Clear cooldown for account {idx}? Only do this if the lock is stale.",
            { idx: idx + 1 },
          ),
          $tr("Clear Cooldown"),
          "warn",
          $tr("Clear"),
        );
      case "unlock":
        return triggerAction(
          tokenActions.unlockLock(idx),
          {},
          $tr("Unlock account {idx}? It will rejoin the active rotation.", {
            idx: idx + 1,
          }),
          $tr("Unlock Token"),
          "neutral",
          $tr("Unlock"),
        );
      case "lock":
        return triggerAction(
          tokenActions.lock(idx),
          {},
          $tr(
            "Lock account {idx}? It will be excluded from rotation until unlocked.",
            { idx: idx + 1 },
          ),
          $tr("Lock Token"),
          "warn",
          $tr("Lock"),
        );
      case "remove":
        return triggerAction(
          adminActions.tokenRemoveSpecific,
          { token: token.token_value ?? idx },
          $tr(
            "Remove account {idx} from the pool and .env? The account must be re-added to use it again.",
            { idx: idx + 1 },
          ),
          $tr("Remove Token"),
          "danger",
          $tr("Remove"),
        );
      default:
        return;
    }
  }
  function handleSpawn(idx, model) {
    const m = model || "mimo/mimo-v2.5";
    // Confirm-intent line (issue #350 — mirrors askLineFor): warn when the
    // pick spends wallet Freebucks or ends the live session.
    const token = data?.tokens?.find?.((t) => t.index === idx);
    const intent = spawnIntent(token, m);
    const askLine = intentAskLine(intent, token?.session_model);
    triggerAction(
      tokenActions.session(idx),
      { model: m },
      $tr("Create upstream session for account #{idx} on {model}?", {
        idx: idx + 1,
        model: m,
      }) + (askLine ? " " + askLine : ""),
    );
  }

  function handleRefresh(idx, action) {
    if (action === "probe") {
      return triggerAction(
        tokenActions.test(idx),
        {},
        $tr("Probe account #{idx} against upstream?", { idx: idx + 1 }),
      );
    }
    return triggerAction(
      tokenActions.finish(idx),
      {},
      $tr("Finish active runs on account #{idx}?", { idx: idx + 1 }),
    );
  }

  // Probe-all: same zero-cost upstream GET per token as the per-row probe
  // buttons (no session claimed), fanned out via test-all. No confirm: the
  // effect is cache freshness only, nothing is spent or ended. Uses the
  // shared store helper (not triggerAction/postAPI: test-all answers with
  // concatenated per-token JSON objects that res.json() cannot parse).
  async function handleProbeAll() {
    if (probingAll) return;
    probingAll = true;
    try {
      await probeAllQuotas();
      actionOK = true;
      actionMessage = $tr("Quotas refreshed from upstream.");
    } catch (e) {
      actionOK = false;
      actionMessage = e.message || $tr("Quota refresh failed");
    } finally {
      probingAll = false;
    }
  }
  function handleDropSession(idx) {
    return triggerAction(
      tokenActions.dropSession(idx),
      {},
      $tr(
        "Drop active session on account #{idx}? This ends the current session upstream (e.g. luna) so the next request admits fresh for the model you want. Use this when you need to switch models immediately.",
        { idx: idx + 1 },
      ),
    );
  }

  function handleSwap(from, to) {
    triggerAction(adminActions.tokenSwap, { from, to });
  }

  function handleMove(from, to) {
    if (from === to) return;
    triggerAction(adminActions.tokenSwap, { from, to, action: "move" });
  }

  async function startOAuthLogin() {
    oauthStarting = true;
    oauthStatus = {
      message: $tr("Starting headless login flow…"),
      type: "info",
    };

    try {
      const res = await fetch(adminActions.loginStart, {
        method: "POST",
        headers: csrfHeader("POST"),
      });
      const result = await res.json();

      if (result.fingerprint && result.login_url) {
        oauthStatus = {
          loginUrl: result.login_url,
          fingerprint: result.fingerprint,
          message: $tr("Open this URL in your browser to sign in:"),
          type: "pending",
        };

        clearInterval(oauthTimer);
        oauthTimer = setInterval(async () => {
          try {
            const pollRes = await fetch(
              `${adminApi.loginStatus}?fingerprint=${encodeURIComponent(result.fingerprint)}`,
            );
            const pollData = await pollRes.json();

            if (pollData.status === "completed") {
              clearInterval(oauthTimer);
              oauthStatus = {
                message: $tr(
                  "Account #{idx} added to pool and saved to .env.",
                  {
                    idx: pollData.token_index + 1,
                  },
                ),
                type: "success",
              };
              oauthStarting = false;
              refreshTokens();
            } else if (pollData.status === "error") {
              clearInterval(oauthTimer);
              oauthStatus = {
                message: $tr("Login failed: {message}", {
                  message: pollData.message || $tr("unknown error"),
                }),
                type: "error",
              };
              oauthStarting = false;
            }
          } catch {
            // transient poll failure — keep polling
          }
        }, 3000);
      } else {
        oauthStatus = {
          message: result.message || $tr("Failed to start login wizard."),
          type: "error",
        };
        oauthStarting = false;
      }
    } catch (e) {
      oauthStatus = {
        message: $tr("Network error: {message}", { message: e.message }),
        type: "error",
      };
      oauthStarting = false;
    }
  }

  function toggleExpand(idx) {
    expandedToken = expandedToken === idx ? null : idx;
  }

  onMount(() => {
    // One shared tokens store owns the /admin/api/tokens poll + SSE (issue
    // #292); this page renders from the cached snapshot and refreshes the
    // store after every mutation.
    const release = ensureTokensStore();
    unsubStore = tokensStore.subscribe(applyTokens);
    unsubErr = tokensErrorStore.subscribe((err) => {
      if (err) {
        error = err;
        loading = false;
      }
    });
    const tick = setInterval(() => {
      now = Date.now();
    }, 1000);
    function onConfigSaved() {
      refreshTokens();
    }
    window.addEventListener("fp-config-saved", onConfigSaved);
    (async () => {
      try {
        const cfgRes = await fetchAPI(adminApi.config);
        const envContent = cfgRes?.env_content || "";
        devToolsEnabled = isDevToolsEnabled(envContent);

        const rotVal = (
          getEnvValue(envContent, "TOKEN_ROTATION") || "drain"
        ).toLowerCase();
        tokenRotation = [
          "drain",
          "round_robin",
          "least_used",
          "random",
        ].includes(rotVal)
          ? rotVal
          : "drain";

        const failoverVal = getEnvValue(envContent, "RATE_LIMIT_FAILOVER");
        rateLimitFailover = failoverVal
          ? failoverVal.toLowerCase() !== "false"
          : true;
      } catch {
        devToolsEnabled = false;
        tokenRotation = "drain";
        rateLimitFailover = true;
      }
    })();
    return () => {
      release();
      unsubStore?.();
      unsubErr?.();
      clearInterval(tick);
      clearInterval(oauthTimer);
      window.removeEventListener("fp-config-saved", onConfigSaved);
    };
  });
</script>

  <div class="page-enter">
  <PageShell>
    <div class="flex flex-col gap-6">
      <PageHeader title={$tr('Tokens')} description={$tr('Upstream credentials, device login, and per-token session quotas')} />

      {#if actionMessage}
        <Alert tone={actionOK ? "success" : "error"} title={actionMessage} />
      {/if}
      {#if error}
        <Alert tone="error" title={error}>
          <Button
            variant="ghost"
            size="sm"
            onclick={() => {
              error = "";
              refreshTokens();
            }}
          >
            {$tr("Retry")}
          </Button>
        </Alert>
      {/if}

      {#if oauthStatus}
      <Alert
        tone={oauthStatus.type === "success"
          ? "success"
          : oauthStatus.type === "error"
            ? "error"
            : "info"}
        title={oauthStatus.message}
      >
        {#if oauthStatus.loginUrl}
          <div class="flex flex-col gap-2 mt-2">
            <div class="flex flex-wrap items-center gap-2">
              <code class="fp-num text-xs break-all max-w-full"
                >{oauthStatus.loginUrl}</code
              >
              <CopyButton
                text={oauthStatus.loginUrl}
                label={$tr("Copy link")}
              />
              <a
                href={oauthStatus.loginUrl}
                target="_blank"
                rel="noopener noreferrer"
                class="inline-flex items-center gap-1 text-xs text-[var(--fp-accent)] hover:underline font-medium"
              >
                {$tr("Open in New Tab")}
                <ExternalLink size={12} />
              </a>
            </div>
            <p class="text-xs text-[var(--fp-dim)]">
              {$tr(
                "Tip: To add a different FreeBuff account, open this link in an Incognito / Private window so your browser does not reuse an existing GitHub session.",
              )}
            </p>
          </div>
        {/if}
      </Alert>
    {/if}

    <!-- Add token form -->
    <Card
      title={$tr("Add Token to Pool")}
      description={$tr(
        "Paste a FreeBuff auth token (from credentials.json or CLI) to add it to the shared pool and save it to .env. Adding burns no quota.",
      )}
    >
      <div class="flex flex-col gap-3 p-4">
        <form
          class="flex flex-col gap-3"
          onsubmit={(e) => {
            e.preventDefault();
            handleAddToken();
          }}
        >
          <div class="flex flex-col gap-1.5">
            <label for="new-token" class="text-xs font-semibold text-[var(--fp-text)]">
              {$tr("FreeBuff Auth Token")}
            </label>
            <textarea
              id="new-token"
              class="fp-num w-full rounded-md border border-[var(--fp-border)] bg-[var(--fp-surface)] px-3 py-2 text-xs text-[var(--fp-text)] placeholder-[var(--fp-dim)] focus:outline-none focus:ring-2 focus:ring-[var(--fp-accent)]"
              rows="3"
              placeholder={$tr("Paste token here…")}
              bind:value={newToken}
              disabled={adding}
            ></textarea>
          </div>
          <div class="flex items-center gap-2">
            <Button
              type="submit"
              variant="primary"
              size="sm"
              disabled={adding || !newToken.trim()}
            >
              <span class="flex items-center gap-1.5">
                {#if adding}
                  <RefreshCw size={12} class="animate-spin" />
                {:else}
                  <Plus size={12} />
                {/if}
                {$tr("Add Token")}
              </span>
            </Button>
            <span class="text-[11px] text-[var(--fp-dim)]">
              {$tr("Adding burns no quota.")}
            </span>
          </div>
        </form>
      </div>
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
        <dd class="text-sm font-semibold text-[var(--fp-text)] tabular-nums">
          {data?.token_count ?? 0}
        </dd>
      </div>
      <div class="flex flex-col px-2.5 py-1.5 bg-[var(--fp-surface)]">
        <dt class="text-[10px] uppercase tracking-wider text-[var(--fp-dim)]">
          {$tr("Active")}
        </dt>
        <dd class="text-sm font-semibold text-[var(--fp-accent)] tabular-nums">
          {activeLeases}
        </dd>
      </div>
      <div class="flex flex-col px-2.5 py-1.5 bg-[var(--fp-surface)]">
        <dt class="text-[10px] uppercase tracking-wider text-[var(--fp-dim)]">
          {$tr("Strategy")}
        </dt>
        <dd class="text-sm font-semibold text-[var(--fp-text)]">
          {tokenRotation === "drain"
            ? $tr("Drain")
            : tokenRotation === "round_robin"
              ? $tr("Robin")
              : tokenRotation === "least_used"
                ? $tr("Least")
                : $tr("Random")}
        </dd>
      </div>
      <div class="flex flex-col px-2.5 py-1.5 bg-[var(--fp-surface)]">
        <dt class="text-[10px] uppercase tracking-wider text-[var(--fp-dim)]">
          {$tr("Failover")}
        </dt>
        <dd
          class="text-sm font-semibold {rateLimitFailover
            ? 'text-[var(--fp-accent)]'
            : 'text-[var(--fp-dim)]'}"
        >
          {rateLimitFailover ? $tr("On") : $tr("Off")}
        </dd>
      </div>
    </dl>
  {/snippet}
  {#if actionMessage}
    <Alert tone={actionOK ? "success" : "error"} title={actionMessage} />
  {/if}

  {#if oauthStatus}
    <Alert
      tone={oauthStatus.type === "success"
        ? "success"
        : oauthStatus.type === "error"
          ? "error"
          : "info"}
      title={oauthStatus.message}
    >
      {#if oauthStatus.loginUrl}
        <div class="flex flex-col gap-2 mt-2">
          <div class="flex flex-wrap items-center gap-2">
            <code class="fp-num text-xs break-all max-w-full"
              >{oauthStatus.loginUrl}</code
            >
            <CopyButton text={oauthStatus.loginUrl} label={$tr("Copy link")} />
            <a
              href={oauthStatus.loginUrl}
              target="_blank"
              rel="noopener noreferrer"
              class="inline-flex items-center gap-1 text-xs text-[var(--fp-accent)] hover:underline font-medium"
            >
              {$tr("Open in New Tab")}
              <ExternalLink size={12} />
            </a>
          </div>
          <p class="text-xs text-[var(--fp-dim)]">
            {$tr(
              "Tip: To add a different FreeBuff account, open this link in an Incognito / Private window so your browser does not reuse an existing GitHub session.",
            )}
          </p>
        </div>
      {/if}
    </Alert>
  {/if}

  <!-- Add token form -->
  <Card
    title={$tr("Add Token to Pool")}
    description={$tr(
      "Paste a FreeBuff auth token (from credentials.json or CLI) to add it to the shared pool and save it to .env. Adding burns no quota.",
    )}
  >
    {#snippet actions()}
      <Button
        variant="secondary"
        onclick={startOAuthLogin}
        disabled={oauthStarting}
      >
        {#if oauthStarting}
          <RefreshCw size={15} class="animate-spin" />
          <span>{$tr("Authorizing…")}</span>
        {:else}
          <LogIn size={15} />
          <span>{$tr("Device Login")}</span>
        {/if}
      </Button>
    {/snippet}
    <form onsubmit={addToken} class="flex flex-col gap-1.5">
      <label
        for="add-token-input"
        class="text-xs font-medium text-[var(--fp-muted)]">{$tr("Token")}</label
      >
      <div
        class="flex flex-col sm:flex-row items-stretch sm:items-center gap-2.5"
      >
        <input
          id="add-token-input"
          type="text"
          bind:value={newToken}
          placeholder="e.g. a94d808e-8a86-455b-80fb-a9df4422bfcb"
          autocomplete="off"
          spellcheck="false"
          class="fp-input fp-num flex-1"
        />
        <Button
          type="submit"
          variant="primary"
          disabled={adding || !newToken.trim() || tokenValid === false}
          loading={adding}
          class="shrink-0"
        >
          <Plus size={15} />
          <span>{$tr("Add Token")}</span>
        </Button>
      </div>
      {#if tokenValid === false}
        <p class="text-[11px] text-[var(--fp-error)]" role="alert">
          {$tr(
            "Token must be at least 10 characters and must not contain spaces, commas, or Bearer prefix",
          )}
        </p>
      {:else}
        <p class="text-[11px] text-[var(--fp-dim)]">
          {tokenValid === true
            ? $tr("Valid format")
            : $tr(
                "UUID or session token from ~/.config/codebuff/credentials.json",
              )}
        </p>
      {/if}
    </form>
  </Card>

  <!-- Token Rotation Scheme & Handling Policy Card -->
  <Card
    title={$tr("Token Rotation & Handling Policy")}
    description={$tr(
      "Strategy used by the gateway to select upstream accounts for model requests.",
    )}
  >
    {#snippet actions()}
      <span
        class="inline-flex items-center gap-1.5 font-mono text-xs text-[var(--fp-muted)]"
      >
        <span class="led {tokenRotation === 'drain' ? 'led-good' : 'led-idle'}"
        ></span>
        <span
          class="uppercase tracking-wider font-semibold text-[var(--fp-accent)]"
          >{tokenRotation}</span
        >
      </span>
    {/snippet}

    <div class="space-y-3">
      <div
        class="flex flex-wrap items-center gap-2"
        role="radiogroup"
        aria-label={$tr("Token Rotation Policy")}
      >
        <button
          type="button"
          role="radio"
          aria-checked={tokenRotation === "drain"}
          disabled={savingRotation}
          onclick={() => setTokenRotation("drain")}
          class="fp-btn {tokenRotation === 'drain'
            ? 'fp-btn-primary'
            : 'fp-btn-ghost'} fp-btn-sm text-xs"
        >
          {$tr("Drain (Safest)")}
        </button>
        <button
          type="button"
          role="radio"
          aria-checked={tokenRotation === "round_robin"}
          disabled={savingRotation}
          onclick={() => setTokenRotation("round_robin")}
          class="fp-btn {tokenRotation === 'round_robin'
            ? 'fp-btn-primary'
            : 'fp-btn-ghost'} fp-btn-sm text-xs"
        >
          {$tr("Round Robin (1:1)")}
        </button>
        <button
          type="button"
          role="radio"
          aria-checked={tokenRotation === "least_used"}
          disabled={savingRotation}
          onclick={() => setTokenRotation("least_used")}
          class="fp-btn {tokenRotation === 'least_used'
            ? 'fp-btn-primary'
            : 'fp-btn-ghost'} fp-btn-sm text-xs"
        >
          {$tr("Least Used (Max Quota)")}
        </button>
        <button
          type="button"
          role="radio"
          aria-checked={tokenRotation === "random"}
          disabled={savingRotation}
          onclick={() => setTokenRotation("random")}
          class="fp-btn {tokenRotation === 'random'
            ? 'fp-btn-primary'
            : 'fp-btn-ghost'} fp-btn-sm text-xs"
        >
          {$tr("Random (Stochastic)")}
        </button>
      </div>

      <div
        class="fp-inset p-3 rounded text-xs text-[var(--fp-muted)] flex items-start gap-2"
      >
        {#if tokenRotation === "drain"}
          <p class="leading-relaxed">
            <strong class="text-[var(--fp-text)]"
              >{$tr("Drain Mode (Default & Recommended):")}</strong
            >
            {$tr(
              "Exhausts one account completely (e.g. 5/5 Luna sessions) before rotating to the next token. Mimics authentic single-user behavior and provides the strongest anti-ban protection.",
            )}
          </p>
        {:else if tokenRotation === "round_robin"}
          <p class="leading-relaxed">
            <strong class="text-[var(--fp-text)]"
              >{$tr("Round-Robin Mode:")}</strong
            >
            {$tr(
              "Rotates to the next token on every single session (1:1). Note: rapid alternating requests across healthy accounts may raise upstream anomaly-detection signals.",
            )}
          </p>
        {:else if tokenRotation === "least_used"}
          <p class="leading-relaxed">
            <strong class="text-[var(--fp-text)]"
              >{$tr("Least-Used Mode:")}</strong
            >
            {$tr(
              "Routes requests to the token with the lowest daily usage or active run count. Maximizes concurrency and distributes quota consumption evenly.",
            )}
          </p>
        {:else if tokenRotation === "random"}
          <p class="leading-relaxed">
            <strong class="text-[var(--fp-text)]">{$tr("Random Mode:")}</strong>
            {$tr(
              "Selects an available healthy token at random per request. Provides stochastic load balancing.",
            )}
          </p>
        {/if}
      </form>
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
      title={$tr("Token Rotation & Handling Policy")}
      description={$tr(
        "Strategy used by the gateway to select upstream accounts for model requests.",
      )}
    >
      {#snippet actions()}
        <span
          class="inline-flex items-center gap-1.5 font-mono text-xs text-[var(--fp-muted)]"
        >
          <span
            class="led {tokenRotation === 'drain' ? 'led-good' : 'led-idle'}"
          ></span>
          <span
            class="uppercase tracking-wider font-semibold text-[var(--fp-accent)]"
            >{tokenRotation}</span
          >
        </span>
      {/snippet}

      <div class="space-y-3">
        <div
          class="flex flex-wrap items-center gap-2"
          role="radiogroup"
          aria-label={$tr("Token Rotation Policy")}
        >
          <button
            type="button"
            role="radio"
            aria-checked={tokenRotation === "drain"}
            disabled={savingRotation}
            onclick={() => setTokenRotation("drain")}
            class="fp-btn {tokenRotation === 'drain'
              ? 'fp-btn-primary'
              : 'fp-btn-ghost'} fp-btn-sm text-xs"
          >
            {$tr("Drain (Safest)")}
          </button>
          <button
            type="button"
            role="radio"
            aria-checked={tokenRotation === "round_robin"}
            disabled={savingRotation}
            onclick={() => setTokenRotation("round_robin")}
            class="fp-btn {tokenRotation === 'round_robin'
              ? 'fp-btn-primary'
              : 'fp-btn-ghost'} fp-btn-sm text-xs"
          >
            {$tr("Round Robin (1:1)")}
          </button>
          <button
            type="button"
            role="radio"
            aria-checked={tokenRotation === "least_used"}
            disabled={savingRotation}
            onclick={() => setTokenRotation("least_used")}
            class="fp-btn {tokenRotation === 'least_used'
              ? 'fp-btn-primary'
              : 'fp-btn-ghost'} fp-btn-sm text-xs"
          >
            {$tr("Least Used (Max Quota)")}
          </button>
          <button
            type="button"
            role="radio"
            aria-checked={tokenRotation === "random"}
            disabled={savingRotation}
            onclick={() => setTokenRotation("random")}
            class="fp-btn {tokenRotation === 'random'
              ? 'fp-btn-primary'
              : 'fp-btn-ghost'} fp-btn-sm text-xs"
          >
            {$tr("Random (Stochastic)")}
          </button>
        </div>

        <div
          class="fp-inset p-3 rounded-lg text-xs text-[var(--fp-muted)] flex items-start gap-2"
        >
          {#if tokenRotation === "drain"}
            <p class="leading-relaxed">
              <strong class="text-[var(--fp-text)]"
                >{$tr("Drain Mode (Default & Recommended):")}</strong
              >
              {$tr(
                "Exhausts one account completely (e.g. 5/5 Luna sessions) before rotating to the next token. Mimics authentic single-user behavior and provides the strongest anti-ban protection.",
              )}
            </p>
          {:else if tokenRotation === "round_robin"}
            <p class="leading-relaxed">
              <strong class="text-[var(--fp-text)]"
                >{$tr("Round-Robin Mode:")}</strong
              >
              {$tr(
                "Rotates to the next token on every single session (1:1). Note: rapid alternating requests across healthy accounts may raise upstream anomaly-detection signals.",
              )}
            </p>
          {:else if tokenRotation === "least_used"}
            <p class="leading-relaxed">
              <strong class="text-[var(--fp-text)]"
                >{$tr("Least-Used Mode:")}</strong
              >
              {$tr(
                "Routes requests to the token with the lowest daily usage or active run count. Maximizes concurrency and distributes quota consumption evenly.",
              )}
            </p>
          {:else if tokenRotation === "random"}
            <p class="leading-relaxed">
              <strong class="text-[var(--fp-text)]"
                >{$tr("Random Mode:")}</strong
              >
              {$tr(
                "Selects an available healthy token at random per request. Provides stochastic load balancing.",
              )}
            </p>
          {/if}
        </div>

        <!-- Rate Limit Auto-Failover Toggle -->
        <div
          class="pt-3 border-t border-[var(--fp-border)] flex flex-col sm:flex-row sm:items-center justify-between gap-3"
        >
          <div class="space-y-0.5">
            <div class="flex items-center gap-2">
              <span class="text-xs font-semibold text-[var(--fp-text)]">
                {$tr("Auto Failover on Rate Limit (429)")}
              </span>
              <span class="led {rateLimitFailover ? 'led-good' : 'led-dim'}"
              ></span>
            </div>
            <p class="text-[11px] text-[var(--fp-muted)] leading-relaxed">
              {$tr(
                "When enabled, an in-flight request encountering a 429 rate limit or account throttle immediately leases another healthy pool token and retries seamlessly without failing the request.",
              )}
            </p>
          </div>
          <ToggleSwitch
            checked={rateLimitFailover}
            disabled={savingFailover}
            saving={savingFailover}
            ariaLabel="Auto Failover on Rate Limit (429)"
            onchange={(v) => toggleRateLimitFailover(v)}
          />
        </div>
      </div></Card
    >
    <ReferralBanner tokens={data?.tokens ?? []} />

    <TokenTable
      tokens={data?.tokens ?? []}
      tokenCount={data?.token_count ?? 0}
      {loading}
      {error}
      {expandedToken}
      {actionPending}
      {now}
      {devToolsEnabled}
      bind:spawnModels
      onToggle={toggleExpand}
      onAction={handleTokenAction}
      onSpawn={handleSpawn}
      onRefresh={handleRefresh}
      onDropSession={handleDropSession}
      onSwap={handleSwap}
      onMove={handleMove}
      onRetry={() => {
        error = "";
        refreshTokens();
      }}
    />
    {#if data?.show_bridge && data?.bridge_token_cards?.length > 0}
      <Card
        title={$tr("Bridge Clients")}
        description={$tr(
          "{count} active bridge client(s) relaying their own FreeBuff tokens",
          { count: data.bridge_token_cards.length },
        )}
        pad="none"
      >
        <div class="flex flex-col gap-3 p-4">
          {#each data.bridge_token_cards as bc (bc.key)}
            <BridgeTokenCard card={bc} {now} />
          {/each}
        </div>
      </Card>
    {/if}
    <!-- Rate Limit Auto-Failover Toggle -->
    <div
      class="pt-3 border-t border-[var(--fp-border)] flex flex-col sm:flex-row sm:items-center justify-between gap-3"
    >
      <div class="space-y-0.5">
        <div class="flex items-center gap-2">
          <span class="text-xs font-semibold text-[var(--fp-text)]">
            {$tr("Auto Failover on Rate Limit (429)")}
          </span>
          <span class="led {rateLimitFailover ? 'led-good' : 'led-dim'}"
          ></span>
        </div>
        <p class="text-[11px] text-[var(--fp-muted)] leading-relaxed">
          {$tr(
            "When enabled, an in-flight request encountering a 429 rate limit or account throttle immediately leases another healthy pool token and retries seamlessly without failing the request.",
          )}
        </p>
      </div>
      <ToggleSwitch
        checked={rateLimitFailover}
        disabled={savingFailover}
        saving={savingFailover}
        ariaLabel="Auto Failover on Rate Limit (429)"
        onchange={(v) => toggleRateLimitFailover(v)}
      />
    </div>
  </Card>
  <TokenTable
    tokens={data?.tokens ?? []}
    tokenCount={data?.token_count ?? 0}
    {loading}
    {error}
    {expandedToken}
    {actionPending}
    {now}
    {devToolsEnabled}
    bind:spawnModels
    onToggle={toggleExpand}
    onAction={handleTokenAction}
    onSpawn={handleSpawn}
    onRefresh={handleRefresh}
    onDropSession={handleDropSession}
    onSwap={handleSwap}
    onMove={handleMove}
    onProbeAll={handleProbeAll}
    probeAllPending={probingAll}
    onRetry={() => {
      error = "";
      refreshTokens();
    }}
  />
  {#if data?.show_bridge && data?.bridge_token_cards?.length > 0}
    <Card
      title={$tr("Bridge Clients")}
      description={$tr(
        "{count} active bridge client(s) relaying their own FreeBuff tokens",
        { count: data.bridge_token_cards.length },
      )}
      pad="none"
    >
      <div class="flex flex-col gap-3 p-4">
        {#each data.bridge_token_cards as bc (bc.key)}
          <BridgeTokenCard card={bc} {now} />
        {/each}
      </div>
    </Card>
  {/if}
</PageShell>
