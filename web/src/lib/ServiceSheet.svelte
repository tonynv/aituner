<script>
  import Icon from './Icon.svelte';
  import { api } from './api.js';
  import { app, refresh } from './app.svelte.js';

  // One service's details and what can be done with it. Every action states exactly what happens, and runs only after
  // it is confirmed here; the server re-checks that it is still available.
  let dialog;
  let d = $state(null);
  let err = $state('');
  let pending = $state(null); // the action being confirmed
  let option = $state(false);
  let started = $state(false);

  export async function open(id) {
    d = null; err = ''; pending = null; option = false; started = false;
    dialog.showModal();
    try { d = await api.service(id); } catch (e) { err = e.message; }
  }
  const job = $derived(app.state?.job?.kind === 'service' ? app.state.job : null);
  const lines = $derived(app.log.filter((e) => e.kind === 'service' && e.event.message).slice(-6));
  async function run() {
    err = '';
    try {
      await api.serviceAction(d.id, pending.id, option);
      started = true;
      pending = null;
      await refresh();
    } catch (e) { err = e.message; }
  }
  async function close() {
    dialog.close();
    await refresh();
  }
</script>

<dialog bind:this={dialog} aria-labelledby="svc-title">
  {#if d}
    <div class="head">
      <span class="dot" class:on={d.active} class:idle={d.installed && !d.active} aria-hidden="true"></span>
      <h3 id="svc-title">{d.name}</h3>
      <span class="state muted">{d.active ? 'active' : d.installed ? 'installed, idle' : 'not installed'}</span>
    </div>
    {#if d.detail}<p class="muted small">{d.detail}</p>{/if}
    {#if d.info.length}<ul class="info">{#each d.info as i (i)}<li>{i}</li>{/each}</ul>{/if}
    {#if d.note}<p class="faint small">{d.note}</p>{/if}

    {#if started}
      <div class="log mono">{#each lines as l (l.seq)}<div>{l.event.message}</div>{/each}{#if job?.running}<div class="faint">working…</div>{/if}</div>
      {#if job?.error}<p class="bad small" role="alert">{job.error}</p>{/if}
    {:else if pending}
      <div class="confirm">
        <p>{pending.confirm}</p>
        {#if pending.option}<label class="opt"><input type="checkbox" bind:checked={option} /> {pending.option}</label>{/if}
      </div>
    {/if}
  {:else if !err}
    <p class="muted">Loading</p>
  {/if}
  {#if err}<p class="bad small" role="alert">{err}</p>{/if}

  <div class="row end">
    {#if pending}
      <button class="btn" onclick={() => (pending = null)}>Cancel</button>
      <button class="btn {pending.id === 'remove' ? 'danger' : 'primary'}" onclick={run}>{pending.label}</button>
    {:else}
      {#if d && !started}
        {#each d.actions as a (a.id)}
          <button class="btn {a.id === 'remove' ? 'danger' : ''}" onclick={() => { pending = a; option = false; }}>
            <Icon name={a.id === 'remove' ? 'trash' : 'stop'} size={14} /> {a.label}
          </button>
        {/each}
      {/if}
      <button class="btn" onclick={close} disabled={!!job?.running}>{started && !job?.running ? 'Done' : 'Close'}</button>
    {/if}
  </div>
</dialog>

<style>
  dialog { background: var(--surface); color: var(--text); border: 1px solid var(--border-strong); border-radius: var(--radius); padding: 20px; width: min(520px, calc(100vw - 32px)); }
  dialog::backdrop { background: var(--overlay); }
  .head { display: flex; align-items: center; gap: 10px; }
  .state { margin-left: auto; font-size: 13px; }
  .dot { width: 10px; height: 10px; border-radius: 50%; border: 1px solid var(--border-strong); flex: none; }
  .dot.idle { background: var(--faint); border-color: var(--faint); }
  .dot.on { background: var(--ok); border-color: var(--ok); }
  .small { font-size: 13px; margin: 8px 0 0; }
  .info { margin: 12px 0 0; padding-left: 18px; display: grid; gap: 6px; font-size: 14px; color: var(--muted); }
  .confirm { margin-top: 14px; padding: 12px 14px; border: 1px solid var(--border-strong); border-radius: var(--radius); background: var(--surface-2); display: grid; gap: 10px; }
  .confirm p { margin: 0; }
  .opt { display: flex; gap: 10px; align-items: center; min-height: var(--tap); cursor: pointer; }
  .opt input { width: 20px; height: 20px; accent-color: var(--accent); }
  .log { margin-top: 14px; padding: 10px 12px; border: 1px solid var(--border); border-radius: var(--radius); background: var(--bg); font-size: 12px; display: grid; gap: 2px; max-height: 180px; overflow: auto; }
  .bad { color: var(--bad); }
  .end { justify-content: flex-end; flex-wrap: wrap; margin-top: 18px; }
</style>
