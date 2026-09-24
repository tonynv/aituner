<script>
  import Icon from './Icon.svelte';
  import Progress from './Progress.svelte';
  import Copyable from './Copyable.svelte';
  import { fmtGB, fmtSpeed } from './format.js';
  import { api } from './api.js';

  // status: this repo's entry from GET /downloads (or undefined); anyActive: some download is running
  let { repo, status, anyActive, blocked = false, onchange } = $props();
  let err = $state('');
  let busy = $state(false);

  const running = $derived(status?.state === 'running');
  const done = $derived(status?.state === 'done');
  const failed = $derived(status?.state === 'error');
  const cancelled = $derived(status?.state === 'cancelled');
  const frac = $derived(status && status.bytes_total > 0 ? status.bytes_done / status.bytes_total : 0);

  async function start() {
    err = ''; busy = true;
    try { await api.startDownload(repo); await onchange?.(); } catch (e) { err = e.message; } finally { busy = false; }
  }
  async function cancel() {
    try { await api.cancelDownload(repo); await onchange?.(); } catch (e) { err = e.message; }
  }
</script>

<div class="dl">
  {#if done}
    <div class="row"><span class="badge ok"><Icon name="check" size={12} /> downloaded</span><span class="faint mono path">{status.dest}</span></div>
    {#if status.run}<Copyable text={status.run} label="Copy command for the downloaded model" />{/if}
  {:else if running}
    <Progress value={frac} label="Download progress" />
    <div class="row between">
      <span class="muted small">{fmtGB(status.bytes_done / 1024 ** 3)} of {fmtGB(status.bytes_total / 1024 ** 3)}{status.speed_bps > 0 ? ` at ${fmtSpeed(status.speed_bps)}` : ''}</span>
      <button class="btn small" onclick={cancel}><Icon name="x" size={14} /> Cancel</button>
    </div>
  {:else}
    <button class="btn small primary" onclick={start} disabled={busy || anyActive || blocked} title={anyActive ? 'Another download is running' : blocked ? 'Downloads wait for a running benchmark' : ''}>
      <Icon name="download" size={14} /> {cancelled ? 'Resume download' : failed ? 'Retry download' : 'Download'}
    </button>
    {#if cancelled}<span class="faint small">Partial files are kept and resumed.</span>{/if}
  {/if}
  {#if failed && status.error}<p class="bad small" role="alert">{status.error}</p>{/if}
  {#if err}<p class="bad small" role="alert">{err}</p>{/if}
</div>

<style>
  .dl { display: grid; gap: 8px; min-width: 0; }
  .between { justify-content: space-between; }
  .small { font-size: 13px; }
  .bad { color: var(--bad); margin: 0; }
  .path { word-break: break-all; font-size: 12px; }
</style>
