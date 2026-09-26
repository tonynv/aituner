<script>
  import Icon from './Icon.svelte';
  import { api } from './api.js';
  import { app, refresh } from './app.svelte.js';

  // What "Update now" does depends on how this copy was installed: the app updates itself (verified, then restarts);
  // a Homebrew install upgrades with brew in Terminal; a terminal or development build links to the release.
  let { compact = false } = $props();
  let err = $state('');
  let note = $state('');
  let busy = $state(false);
  const u = $derived(app.update);
  const job = $derived(app.state?.job?.kind === 'update' ? app.state.job : null);
  async function install() {
    err = ''; note = ''; busy = true;
    try {
      await api.updateInstall();
      note = u.method === 'homebrew' ? 'Homebrew is upgrading aituner in Terminal; it reopens when done.' : '';
      await refresh();
    } catch (e) { err = e.message; } finally { busy = false; }
  }
  async function skip() {
    err = '';
    try { app.update = await api.updateSettings({ skip: u.latest.version }); } catch (e) { err = e.message; }
  }
  const lastLine = $derived(app.log.filter((e) => e.kind === 'update' && e.event.message).at(-1)?.event.message ?? '');
</script>

{#if u?.available}
  <div class="actions" class:compact>
    {#if job?.running}
      <span class="muted small">{lastLine || 'Updating'}</span>
    {:else if u.method === 'manual'}
      <a class="btn small primary" href={u.latest.page} target="_blank" rel="noopener noreferrer"><Icon name="external" size={14} /> Get {u.latest.version}</a>
    {:else}
      <button class="btn small primary" onclick={install} disabled={busy}><Icon name="download" size={14} /> Update now</button>
    {/if}
    {#if !job?.running}
      <a class="btn small" href={u.latest.page} target="_blank" rel="noopener noreferrer">What's new</a>
      {#if u.skipped !== u.latest.version}<button class="btn small" onclick={skip}>Skip this version</button>{/if}
    {/if}
  </div>
  {#if job?.error}<p class="bad small" role="alert">{job.error}</p>{/if}
  {#if err}<p class="bad small" role="alert">{err}</p>{/if}
  {#if note}<p class="muted small">{note}</p>{/if}
  {#if u.method === 'manual' && !compact}<p class="faint small">{u.why}</p>{/if}
{/if}

<style>
  .actions { display: flex; gap: 8px; flex-wrap: wrap; align-items: center; }
  .small { font-size: 13px; margin: 0; }
  .bad { color: var(--bad); }
</style>
