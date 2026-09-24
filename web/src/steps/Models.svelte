<script>
  import { onMount } from 'svelte';
  import Icon from '../lib/Icon.svelte';
  import ModelEntry from '../lib/ModelEntry.svelte';
  import { api } from '../lib/api.js';
  import { fmtBytes } from '../lib/format.js';

  let { st } = $props();
  let data = $state(null);
  let err = $state('');
  let loading = $state(false);
  let unrestricted = $state(true);
  let layout = $state('grid');

  // downloads + folder setting
  let dl = $state({});
  let anyActive = $state(false);
  let settings = $state(null);
  let editing = $state(false);
  let draft = $state('');
  let folderErr = $state('');
  let saving = $state(false);

  const CATS = [
    { key: 'code', title: 'Coding', icon: 'code' },
    { key: 'chat', title: 'Chat and reasoning', icon: 'chat' },
    { key: 'image', title: 'Image generation', icon: 'image' },
  ];
  const blocked = $derived(!!st.job?.running);

  onMount(() => {
    try { const v = localStorage.getItem('aituner-view'); if (v === 'list' || v === 'grid') layout = v; } catch { /* storage blocked */ }
  });
  function setLayout(v) {
    layout = v;
    try { localStorage.setItem('aituner-view', v); } catch { /* storage blocked */ }
  }

  async function load() {
    loading = true; err = '';
    try { data = await api.recommendations(unrestricted); } catch (e) { err = e.message; } finally { loading = false; }
  }
  $effect(() => { if (st.recommendations_unlocked && !data && !loading && !err) load(); });

  async function refreshDownloads() {
    try {
      const r = await api.downloads();
      dl = Object.fromEntries(r.items.map((i) => [i.repo, i]));
      anyActive = r.active;
    } catch { /* keep the last known state; the shell shows connection problems */ }
  }
  async function loadSettings() { try { settings = await api.settings(); } catch { /* shown by the shell */ } }

  $effect(() => {
    if (!st.recommendations_unlocked) return;
    loadSettings();
    refreshDownloads();
    let stop = false, t;
    const tick = async () => { await refreshDownloads(); if (!stop) t = setTimeout(tick, anyActive ? 1000 : 5000); };
    t = setTimeout(tick, 1000);
    return () => { stop = true; clearTimeout(t); };
  });

  function edit() { draft = settings?.models_dir ?? ''; folderErr = ''; editing = true; }
  async function saveFolder(value) {
    saving = true; folderErr = '';
    try { settings = await api.setModelsDir(value); editing = false; await refreshDownloads(); } catch (e) { folderErr = e.message; } finally { saving = false; }
  }

  const gradeClass = (g) => (g === 'S' || g === 'A' ? 'ok' : g === 'B' || g === 'C' ? '' : 'warn');
  const bitsLabel = (b) => (b ? `${b}-bit` : 'quant unknown');
  const fit = (f) => ({ text: f === 'comfortable' ? 'fits comfortably' : 'tight fit', cls: f === 'comfortable' ? 'ok' : 'warn' });

  function candidate(m) {
    const badges = [{ text: m.runtime === 'mlx' ? `MLX ${bitsLabel(m.bits)}` : 'MLX (mflux)' }, fit(m.fit), { text: `${m.size_gb.toFixed(1)} GB` }];
    if (m.est_tps) badges.push({ text: `~${Math.round(m.est_tps)} tok/s` });
    if (m.installed) badges.push({ text: 'already in Ollama', cls: 'ok' });
    return {
      title: m.name, subtitle: `${m.provider}, ${m.active_b ? `${m.params_b}B (${m.active_b}B active)` : `${m.params_b}B`}`,
      grade: m.grade, gradeClass: gradeClass(m.grade), badges, notes: m.notes, kv: m.kv,
      repo: m.runtime === 'mlx' ? m.repo : '', run: m.run, runLabel: m.runtime === 'mlx' ? 'Run without downloading first' : 'Install and run with mflux', sourceUrl: m.source_url,
    };
  }
  function variant(v) {
    const badges = [{ text: bitsLabel(v.bits) }, { text: `${v.size_gb.toFixed(1)} GB` }, { text: v.fit === 'comfortable' ? 'fits' : 'tight', cls: v.fit === 'comfortable' ? 'ok' : 'warn' }];
    if (v.est_tps) badges.push({ text: `~${Math.round(v.est_tps)} tok/s` });
    badges.push({ text: `${v.downloads.toLocaleString()} downloads` });
    if (v.license) badges.push({ text: v.license });
    return { title: v.repo, mono: true, badges, notes: v.warnings, kv: v.kv, repo: v.repo, run: v.run, runLabel: 'Run without downloading first' };
  }
</script>

<div class="stack">
  <div class="row between head">
    <div>
      <h2>What this machine can run</h2>
      <p class="muted">Sized to your tuned memory, ranked by canirun.ai fit, resolved to Apple-Silicon (MLX) builds that actually load here, with speeds calibrated to what you measured.</p>
    </div>
    <div class="row controls">
      <div class="seg" role="group" aria-label="Layout">
        <button class:on={layout === 'grid'} onclick={() => setLayout('grid')} aria-pressed={layout === 'grid'} aria-label="Grid view"><Icon name="grid" size={16} /> Grid</button>
        <button class:on={layout === 'list'} onclick={() => setLayout('list')} aria-pressed={layout === 'list'} aria-label="List view"><Icon name="list" size={16} /> List</button>
      </div>
      <label class="toggle">
        <input type="checkbox" bind:checked={unrestricted} onchange={() => { data = null; load(); }} />
        <span>Include unrestricted variants</span>
      </label>
    </div>
  </div>

  {#if settings}
    <section class="card folder" aria-label="Download folder">
      <div class="row between">
        <div class="row where">
          <Icon name="folder" />
          <div class="path-info">
            <div class="muted small">Downloads are saved to</div>
            {#if editing}
              <input class="path" bind:value={draft} spellcheck="false" autocomplete="off" aria-label="Models folder path" onkeydown={(e) => e.key === 'Enter' && saveFolder(draft)} />
            {:else}
              <div class="mono path-text">{settings.models_dir || 'not set'}</div>
              {#if settings.info?.path}<div class="faint small">{fmtBytes(settings.info.free_bytes)} free on this volume{settings.is_default ? ', default folder' : ''}</div>{/if}
            {/if}
          </div>
        </div>
        <div class="row">
          {#if editing}
            <button class="btn small primary" onclick={() => saveFolder(draft)} disabled={saving}>Save</button>
            <button class="btn small" onclick={() => saveFolder('')} disabled={saving}>Use default</button>
            <button class="btn small" onclick={() => (editing = false)} disabled={saving}>Cancel</button>
          {:else}
            <button class="btn small" onclick={edit} disabled={anyActive}>Change folder</button>
          {/if}
        </div>
      </div>
      {#if settings.problem}<p class="bad small" role="alert">{settings.problem}</p>{/if}
      {#if folderErr}<p class="bad small" role="alert">{folderErr}</p>{/if}
      {#if editing}<p class="faint small">A folder inside your home directory or on an external drive (/Volumes). It is created if it does not exist.</p>{/if}
    </section>
  {/if}

  {#if loading}<p class="muted">Asking canirun.ai and Hugging Face</p>{/if}
  {#if err}<p class="bad" role="alert"><Icon name="alert" size={16} /> {err} <button class="btn small" onclick={load}>Retry</button></p>{/if}

  {#if data}
    <p class="muted">
      Memory budget {data.budget_gb.toFixed(1)} GB. Speed estimate = measured bandwidth scaled to each model's size (calibration {Math.round(data.efficiency * 100)}%). The bar under each model shows how that budget is used once it loads, and how much KV cache (context) still fits. Estimates only; MoE speeds ignore routing overhead.
    </p>
    {#each CATS as c}
      {#if data.groups[c.key]?.length}
        <section class="stack">
          <div class="row"><Icon name={c.icon} /><h3>{c.title}</h3>
            {#if data.skipped[c.key]}<span class="faint">{data.skipped[c.key]} more ranked by canirun.ai were skipped</span>{/if}</div>
          <div class={layout === 'grid' ? 'grid' : 'listwrap'}>
            {#each data.groups[c.key] as raw (raw.model_id)}
              {@const m = candidate(raw)}
              <ModelEntry {m} {layout} {dl} {anyActive} {blocked} onchange={refreshDownloads}>
                {#snippet children()}
                  {#if raw.variants?.length}
                    <details class="variants">
                      <summary>{raw.variants.length} unrestricted variant{raw.variants.length > 1 ? 's' : ''}</summary>
                      <div class="vlist">
                        {#each raw.variants as v (v.repo)}
                          <ModelEntry m={variant(v)} layout="list" {dl} {anyActive} {blocked} onchange={refreshDownloads} />
                        {/each}
                      </div>
                    </details>
                  {/if}
                {/snippet}
              </ModelEntry>
            {/each}
          </div>
        </section>
      {/if}
    {/each}

    {#if Object.keys(data.skipped_why || {}).length}
      <details class="card">
        <summary>Why some models were left out</summary>
        <ul>{#each Object.entries(data.skipped_why) as [why, n]}<li>{n} x {why}</li>{/each}</ul>
      </details>
    {/if}
    {#if data.warnings?.length}{#each data.warnings as w}<p class="warn"><Icon name="alert" size={14} /> {w}</p>{/each}{/if}
    <p class="faint small">Sources: {data.sources.join('; ')}. Downloads fetch weights, config and tokenizer files only, verified file by file. Commands use the mlx-lm environment aituner created.</p>
  {/if}
</div>

<style>
  .between { justify-content: space-between; align-items: flex-start; }
  .head { gap: 16px; }
  .controls { gap: 16px; }
  .seg { display: inline-flex; border: 1px solid var(--border-strong); border-radius: var(--radius); overflow: hidden; }
  .seg button { display: inline-flex; align-items: center; gap: 6px; min-height: var(--tap); padding: 0 14px; background: transparent; border: 0; cursor: pointer; color: var(--muted); }
  .seg button + button { border-left: 1px solid var(--border-strong); }
  .seg button.on { background: var(--btn-bg); color: var(--btn-fg); }
  .toggle { display: flex; align-items: center; gap: 10px; min-height: var(--tap); cursor: pointer; }
  .toggle input { width: 20px; height: 20px; accent-color: var(--text); }
  .folder { display: grid; gap: 10px; }
  .where { align-items: flex-start; flex-wrap: nowrap; min-width: 0; flex: 1; }
  .path-info { min-width: 0; flex: 1; }
  .path-text { word-break: break-all; }
  .path { width: 100%; min-height: var(--tap); padding: 0 12px; border: 1px solid var(--border-strong); border-radius: var(--radius); background: var(--bg); color: var(--text); font: 13px var(--font-mono); }
  .small { font-size: 13px; }
  .bad { color: var(--bad); display: flex; gap: 8px; align-items: center; flex-wrap: wrap; margin: 0; }
  .warn { color: var(--warn); display: flex; gap: 8px; align-items: center; }
  .listwrap { display: block; }
  .variants summary { min-height: 36px; color: var(--muted); }
  .vlist { padding-left: 12px; border-left: 1px solid var(--border-strong); margin-top: 6px; }
  ul { padding-left: 18px; margin: 10px 0 0; display: grid; gap: 6px; }
  @media (max-width: 700px) { .head { flex-direction: column; } }
</style>
