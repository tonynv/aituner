<script>
  import Icon from './Icon.svelte';
  import PlanDialog from './PlanDialog.svelte';
  import Copyable from './Copyable.svelte';
  import { api } from './api.js';
  import { app } from './app.svelte.js';

  let { item, project, disabledReason = '', jobRunning = false, onchange } = $props();
  let dlg;
  let err = $state('');
  let note = $state('');
  let busy = $state(false);
  const p = $derived(item.plan);
  const st = $derived(item.status);
  const isGeneric = $derived(item.id === 'openai');
  const icon = $derived({ claude: 'terminal', vscode: 'code', neovim: 'terminal', 'vim-tmux': 'terminal', openai: 'server' }[item.id] || 'terminal');

  async function setup() {
    err = ''; busy = true; dlg.close();
    try { await api.connectSetup(item.id); await onchange?.(); } catch (e) { err = e.message; } finally { busy = false; }
  }
  async function remove() {
    err = ''; busy = true;
    try { await api.connectRemove(item.id); await onchange?.(); } catch (e) { err = e.message; } finally { busy = false; }
  }
  async function open() {
    err = ''; note = ''; busy = true;
    try { const r = await api.connectLaunch(item.id, project); note = 'Opened. Equivalent command: ' + r.command; } catch (e) { err = e.message; } finally { busy = false; }
  }
</script>

<section class="card ic" aria-label={p.title}>
  <div class="row between">
    <div class="row"><Icon name={icon} /><h3>{p.title}</h3></div>
    <div class="row badges">
      {#if !isGeneric}<span class="badge {st.installed ? 'ok' : ''}">{st.installed ? 'installed' : 'not installed'}</span>{/if}
      <span class="badge {st.configured ? 'ok' : ''}">{st.configured ? 'configured for the local model' : 'not configured'}</span>
    </div>
  </div>
  <p class="muted">{p.summary}</p>

  {#if st.configured && st.launcher && !isGeneric}
    <div><div class="faint small">Start it from a terminal:</div><Copyable text={st.launcher} label="Copy launcher path" /></div>
  {/if}

  <div class="row">
    {#if !st.configured}
      <button class="btn primary" onclick={() => dlg.open()} disabled={busy || jobRunning || !!disabledReason} title={disabledReason}>
        <Icon name="download" size={16} /> {isGeneric ? 'Write connection file' : 'Set up'}
      </button>
    {:else}
      {#if !isGeneric}<button class="btn primary" onclick={open} disabled={busy || !!disabledReason} title={disabledReason}><Icon name="external" size={16} /> Open in project</button>{/if}
      <button class="btn" onclick={() => dlg.open()} disabled={busy || jobRunning || !!disabledReason}>Re-run setup</button>
      <button class="btn" onclick={remove} disabled={busy || jobRunning}><Icon name="trash" size={16} /> Remove</button>
    {/if}
  </div>
  {#if disabledReason && !st.configured}<p class="faint small">{disabledReason}</p>{/if}
  {#if note}<p class="ok small mono">{note}</p>{/if}
  {#if err}<p class="bad small" role="alert"><Icon name="alert" size={14} /> {err}</p>{/if}
  {#if p.caveat && !st.configured}<p class="caveat small"><Icon name="alert" size={14} /> {p.caveat}</p>{/if}
</section>

<PlanDialog bind:this={dlg} plan={p} onconfirm={setup} oncancel={() => {}} {busy} />

<style>
  .ic { display: grid; gap: 12px; align-content: start; }
  .between { justify-content: space-between; } .badges { gap: 6px; }
  .small { font-size: 13px; margin: 0; }
  .ok { color: var(--ok); overflow-wrap: anywhere; } .bad { color: var(--bad); display: flex; gap: 6px; align-items: flex-start; }
  .caveat { margin: 0; color: var(--warn); display: flex; gap: 8px; } .caveat :global(svg) { flex: none; margin-top: 3px; }
</style>
