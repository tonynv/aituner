<script>
  import Icon from './Icon.svelte';
  import { fmtBytes } from './format.js';
  // One folder aituner stores data in: where it is, free space, change (validated by the server), reset, show in Finder.
  // folder: { dir, is_default, info, problem }. onsave(value) returns a promise; '' resets to the default.
  let { title, about, folder, onsave, onreveal, locked = false, lockedWhy = '' } = $props();
  let editing = $state(false);
  let draft = $state('');
  let err = $state('');
  let saving = $state(false);
  function edit() { draft = folder?.dir ?? ''; err = ''; editing = true; }
  async function save(v) {
    saving = true; err = '';
    try { await onsave(v); editing = false; } catch (e) { err = e.message; } finally { saving = false; }
  }
</script>

<section class="card folder" aria-label={title}>
  <div class="row between">
    <div class="row where">
      <Icon name="folder" />
      <div class="info">
        <h3>{title}</h3>
        {#if about}<p class="muted small">{about}</p>{/if}
        {#if editing}
          <input class="path" bind:value={draft} spellcheck="false" autocomplete="off" aria-label="{title} path" onkeydown={(e) => e.key === 'Enter' && save(draft)} />
        {:else}
          <div class="mono path-text">{folder?.dir || 'not set'}</div>
          {#if folder?.info?.total_bytes}<div class="faint small">{fmtBytes(folder.info.free_bytes)} free on this volume{folder.is_default ? ' · default folder' : ''}</div>{/if}
        {/if}
      </div>
    </div>
    <div class="row">
      {#if editing}
        <button class="btn small primary" onclick={() => save(draft)} disabled={saving}>Save</button>
        <button class="btn small" onclick={() => save('')} disabled={saving}>Use default</button>
        <button class="btn small" onclick={() => (editing = false)} disabled={saving}>Cancel</button>
      {:else}
        {#if onreveal}<button class="btn small" onclick={onreveal}><Icon name="external" size={14} /> Show in Finder</button>{/if}
        {#if onsave}<button class="btn small" onclick={edit} disabled={locked} title={locked ? lockedWhy : ''}>Change</button>{/if}
      {/if}
    </div>
  </div>
  {#if folder?.problem}<p class="bad small" role="alert">{folder.problem}</p>{/if}
  {#if err}<p class="bad small" role="alert">{err}</p>{/if}
  {#if editing}<p class="faint small">A folder inside your home directory or on an external drive (/Volumes). It is created if it does not exist.</p>{/if}
</section>

<style>
  .folder { display: grid; gap: 10px; }
  .between { justify-content: space-between; }
  .where { align-items: flex-start; flex-wrap: nowrap; min-width: 0; flex: 1; }
  .info { min-width: 0; flex: 1; display: grid; gap: 4px; }
  .path-text { word-break: break-all; }
  .path { width: 100%; min-height: var(--tap); padding: 0 12px; border: 1px solid var(--border-strong); border-radius: var(--radius); background: var(--bg); color: var(--text); font: 13px var(--font-mono); }
  .small { font-size: 13px; margin: 0; }
  .bad { color: var(--bad); margin: 0; }
</style>
