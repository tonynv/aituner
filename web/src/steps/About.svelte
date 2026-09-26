<script>
  import Icon from '../lib/Icon.svelte';
  import UpdateActions from '../lib/UpdateActions.svelte';
  import { api } from '../lib/api.js';
  import { app } from '../lib/app.svelte.js';

  // Version and software update.
  let err = $state('');
  let checking = $state(false);
  const u = $derived(app.update);
  const when = (ms) => new Date(ms).toLocaleString([], { dateStyle: 'medium', timeStyle: 'short' });
  async function check() {
    err = ''; checking = true;
    try { app.update = await api.updateCheck(); } catch (e) { err = e.message; } finally { checking = false; }
  }
  async function setAuto(auto) {
    try { app.update = await api.updateSettings({ auto }); } catch (e) { err = e.message; }
  }
  const HOW = {
    app: 'Updates are downloaded from GitHub, checked (Developer ID signature, Apple notarization, checksums) and installed; aituner then restarts.',
    homebrew: 'aituner was installed with Homebrew, so Homebrew updates it: "Update now" runs brew upgrade --cask aituner in Terminal.',
  };
</script>

<div class="stack">
  <div>
    <h2>About aituner</h2>
    <p class="muted">Benchmark, tune and run local AI models on your Mac.</p>
  </div>

  {#if u}
    <section class="card stack tight" aria-label="Version">
      <div class="row between">
        <div class="row"><Icon name="info" /><h3>Version {u.current || 'unknown'}</h3>{#if !u.release}<span class="badge">development build</span>{/if}</div>
        <button class="btn small" onclick={check} disabled={checking}><Icon name="refresh" size={14} /> {checking ? 'Checking' : 'Check for updates'}</button>
      </div>
      {#if u.available}
        <p><strong>aituner {u.latest.version}</strong> is available{u.latest.published ? `, released ${new Date(u.latest.published).toLocaleDateString()}` : ''}.</p>
        <UpdateActions />
        {#if u.latest.notes}<details><summary>Release notes</summary><pre class="notes">{u.latest.notes}</pre></details>{/if}
      {:else if u.checked_at && !u.error}
        <p class="muted">{u.latest ? `Up to date: ${u.latest.version} is the latest release.` : 'No release has been published yet.'}</p>
      {/if}
      {#if u.error}<p class="bad small" role="alert">Could not check: {u.error}</p>{/if}
      {#if err}<p class="bad small" role="alert">{err}</p>{/if}
      <p class="faint small">{HOW[u.method] ?? u.why}{u.checked_at ? ` Last checked ${when(u.checked_at)}.` : ''}</p>
      <label class="toggle"><input type="checkbox" checked={u.auto} onchange={(e) => setAuto(e.currentTarget.checked)} /> <span>Check for updates automatically (once a day)</span></label>
    </section>
  {/if}

  <section class="card stack tight" aria-label="Links">
    <div class="row"><Icon name="external" /><h3>Links</h3></div>
    <div class="row">
      <a class="btn small" href="https://aituner.app/" target="_blank" rel="noopener noreferrer">Documentation</a>
      <a class="btn small" href="https://github.com/tonynv/aituner/releases" target="_blank" rel="noopener noreferrer">Releases</a>
      <a class="btn small" href="https://github.com/tonynv/aituner/issues" target="_blank" rel="noopener noreferrer">Report an issue</a>
    </div>
    <p class="muted small">Contact: <a href="mailto:info@aituner.app">info@aituner.app</a></p>
  </section>
</div>

<style>
  .between { justify-content: space-between; }
  .tight { gap: 12px; }
  .small { font-size: 13px; margin: 0; }
  .bad { color: var(--bad); }
  .notes { white-space: pre-wrap; font-size: 13px; background: var(--surface-2); border: 1px solid var(--border); border-radius: var(--radius); padding: 12px; margin: 8px 0 0; max-height: 320px; overflow: auto; }
  details summary { min-height: 36px; font-size: 14px; color: var(--muted); }
  .toggle { display: flex; align-items: center; gap: 10px; min-height: var(--tap); cursor: pointer; }
  .toggle input { width: 20px; height: 20px; accent-color: var(--accent); }
</style>
