<script>
  import { Zap, RefreshCw, Check } from '@lucide/svelte';
  import Button from './Button.svelte';
  import { postAPI } from '../api/client.js';
  import { tr } from '../i18n.js';

  let { idx, onSpawn } = $props();

  let spawnModel = $state('mimo/mimo-v2.5');
  let actionPending = $state(false);

  async function triggerAction(action, body, confirmMsg) {
    if (confirmMsg && !confirm(confirmMsg)) return;
    actionPending = true;
    try {
      const res = await postAPI(`/admin/tokens/${idx}/${action}`, body);
      onSpawn?.({ ok: res.ok, message: res.message || (res.ok ? $tr('Action completed') : $tr('Action failed')) });
    } catch (e) {
      onSpawn?.({ ok: false, message: e.message || $tr('Action failed') });
    } finally {
      actionPending = false;
    }
  }
</script>

<td>
  <select
    bind:value={spawnModel}
    class="fp-input !text-xs !py-1 !px-2 !h-8 !w-48"
  >
    <option value="openai/gpt-5.6-luna">openai/gpt-5.6-luna (5/d)</option>
    <option value="mimo/mimo-v2.5">mimo/mimo-v2.5 (unlimited)</option>
    <option value="z-ai/glm-5.3-flash">z-ai/glm-5.3-flash (2/d)</option>
    <option value="deepseek/deepseek-v4-flash">deepseek/deepseek-v4-flash</option>
    <option value="deepseek/deepseek-v4-pro">deepseek/deepseek-v4-pro</option>
    <option value="z-ai/glm-5.2">z-ai/glm-5.2 (referral)</option>
  </select>
</td>
<td class="text-right">
  <div class="inline-flex items-center gap-1.5 justify-end">
    <Button
      variant="primary"
      size="sm"
      disabled={actionPending}
      onclick={() => triggerAction('session', { model: spawnModel }, $tr('Spawn upstream session on token #{idx} for {model}?', { idx, model: spawnModel }))}
    >
      <Zap size={13} />
      <span>{$tr('Make Session')}</span>
    </Button>
    <Button
      variant="secondary"
      size="sm"
      disabled={actionPending}
      onclick={() => triggerAction('test', {}, $tr('Probe token #{idx}?', { idx }))}
    >
      <RefreshCw size={13} />
      <span>{$tr('Probe')}</span>
    </Button>
    <Button
      variant="ghost"
      size="sm"
      disabled={actionPending}
      onclick={() => triggerAction('finish', {}, $tr('Finish runs on token #{idx}?', { idx }))}
    >
      <Check size={13} />
      <span>{$tr('Finish')}</span>
    </Button>
  </div>
</td>