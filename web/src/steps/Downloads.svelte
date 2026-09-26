<script>
  import { onMount } from 'svelte';
  import Icon from '../lib/Icon.svelte';
  import ModelEntry from '../lib/ModelEntry.svelte';
  import RuntimeCard from '../lib/RuntimeCard.svelte';
  import DownloadQueue from '../lib/DownloadQueue.svelte';
  import InstalledModels from '../lib/InstalledModels.svelte';
  import { app } from '../lib/app.svelte.js';
  import { api } from '../lib/api.js';
  import { fmtBytes } from '../lib/format.js';

  let { st, onnext } = $props();
  let data = $state(null);
  let err = $state('');
  let loading = $state(false);
  let unrestricted = $state(true);
  let layout = $state('grid');
  // three views: Discover (recommendations), Queue (downloading), On this Mac (downloaded, with measured speed)
  let view = $state('discover');
  let cat = $state('all');
  let query = $state('');

  // downloads + folder setting
  let dl = $state({});
  let anyActive = $state(false);
  let settings = $state(null);

  const CATS = [
    { key: 'code', title: 'Coding', icon: 'code' },
    { key: 'chat', title: 'Chat and reasoning', icon: 'chat' },
    { key: 'image', title: 'Image generation', icon: 'image' },
  ];
  const blocked = $derived(!!st.job?.running);

  onMount(() => {
    try { const v = localStorage.getItem('aituner-view'); if (v === 'list' || v === 'grid') layout = v; } catch { /* storage blocked */ }
    refreshDownloads().then(() => { if (activeCount > 0) view = 'queue'; });
  });
  function setLayout(v) {
    layout = v;
    try { localStorage.setItem('aituner-view', v); } catch { /* storage blocked */ }
  }

  async function load() {
    loading = true; err = '';
    try { data = await api.recommendations(unrestricted); } catch (e) { err = e.message; } finally { loading = false; }
  }
  const mlxReady = $derived(!!st.hardware?.software?.mlx?.ready);
  $effect(() => { if (mlxReady && !data && !loading && !err) load(); });

  async function refreshDownloads() {
    try {
      const r = await api.downloads();
      dl = Object.fromEntries(r.items.map((i) => [i.repo, i]));
      anyActive = r.active;
    } catch { /* keep the last known state; the shell shows connection problems */ }
  }
  async function loadSettings() { try { settings = await api.settings(); } catch { /* shown by the shell */ } }

  $effect(() => {
    loadSettings();
    refreshDownloads();
    let stop = false, t;
    const tick = async () => { await refreshDownloads(); if (!stop) t = setTimeout(tick, anyActive ? 1000 : 5000); };
    t = setTimeout(tick, 1000);
    return () => { stop = true; clearTimeout(t); };
  });


  const activeCount = $derived(Object.values(dl).filter((i) => i.state === 'running' || i.state === 'queued' || i.state === 'error').length);
  const doneCount = $derived(Object.values(dl).filter((i) => i.state === 'done').length);
  const q = $derived(query.trim().toLowerCase());
  const matches = (raw) => !q || [raw.name, raw.repo, raw.model_id, ...(raw.variants ?? []).map((v) => v.repo)].some((x) => x?.toLowerCase().includes(q));
  const shown = $derived(data ? Object.fromEntries(CATS.map((c) => [c.key, (cat === 'all' || cat === c.key) ? (data.groups[c.key] ?? []).filter(matches) : []])) : {});
  const shownCount = $derived(Object.values(shown).reduce((a, l) => a + l.length, 0));
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
      <h2>Models</h2>
      <p class="muted">Find models that fit this Mac, download them in the background, and see how fast each one really runs here.</p>
    </div>
  </div>

  <div class="views" role="tablist" aria-label="Models">
    <button role="tab" aria-selected={view === 'discover'} class:on={view === 'discover'} onclick={() => (view = 'discover')}>Discover</button>
    <button role="tab" aria-selected={view === 'queue'} class:on={view === 'queue'} onclick={() => (view = 'queue')}>Queue{#if activeCount}<span class="count">{activeCount}</span>{/if}</button>
    <button role="tab" aria-selected={view === 'mine'} class:on={view === 'mine'} onclick={() => (view = 'mine')}>On this Mac{#if doneCount}<span class="count">{doneCount}</span>{/if}</button>
  </div>

  {#if !mlxReady}
    <RuntimeCard {st} rt={app.serve?.runtime} />
    <p class="muted">Install MLX first (or run Bootstrap in Setup): it is needed to check which models load on this Mac and to download them.</p>
  {/if}

  {#if view === 'queue'}
    <DownloadQueue items={dl} onchange={refreshDownloads} />
    {#if settings}
      <p class="faint small saving"><Icon name="folder" size={14} /> Saving to <span class="mono">{settings.models_dir}</span>{#if settings.info?.path} · {fmtBytes(settings.info.free_bytes)} free{/if} <button class="link" onclick={() => (app.tab = 'storage')}>Change in Storage</button></p>
      {#if settings.problem}<p class="bad small" role="alert">{settings.problem}</p>{/if}
    {/if}
  {:else if view === 'mine'}
    <InstalledModels {st} {onnext} />
  {:else}
  <div class="filters">
    <input class="search" type="search" placeholder="Search models" bind:value={query} aria-label="Search models" />
    <div class="chips" role="group" aria-label="Category">
      <button class:on={cat === 'all'} aria-pressed={cat === 'all'} onclick={() => (cat = 'all')}>All</button>
      {#each CATS as c (c.key)}<button class:on={cat === c.key} aria-pressed={cat === c.key} onclick={() => (cat = c.key)}><Icon name={c.icon} size={14} /> {c.title}</button>{/each}
    </div>
    <div class="row controls">
      <label class="toggle">
        <input type="checkbox" bind:checked={unrestricted} onchange={() => { data = null; load(); }} />
        <span>Unrestricted variants</span>
      </label>
      <div class="seg" role="group" aria-label="Layout">
        <button class:on={layout === 'grid'} onclick={() => setLayout('grid')} aria-pressed={layout === 'grid'} aria-label="Grid view"><Icon name="grid" size={16} /></button>
        <button class:on={layout === 'list'} onclick={() => setLayout('list')} aria-pressed={layout === 'list'} aria-label="List view"><Icon name="list" size={16} /></button>
      </div>
    </div>
  </div>

  {#if loading}<p class="muted">Asking canirun.ai and Hugging Face</p>{/if}
  {#if err}<p class="bad" role="alert"><Icon name="alert" size={16} /> {err} <button class="btn small" onclick={load}>Retry</button></p>{/if}

  {#if data}
    <p class="muted">
      Memory budget {data.budget_gb.toFixed(1)} GB. The bar under each model shows how that budget is used once it loads, and how much KV cache (context) still fits.
      {#if data.efficiency > 0}Speed estimate = measured bandwidth scaled to each model's size (calibration {Math.round(data.efficiency * 100)}%). Estimates only; MoE speeds ignore routing overhead.{:else}No speed estimates yet: benchmark this machine (optional, in the performance tabs) and they appear here.{/if}
    </p>
    {#if !shownCount}<p class="muted">No models match{q ? ` "${query}"` : ''}.</p>{/if}
    {#each CATS as c}
      {#if shown[c.key]?.length}
        <section class="stack">
          <div class="row"><Icon name={c.icon} /><h3>{c.title}</h3>
            {#if data.skipped[c.key]}<span class="faint">{data.skipped[c.key]} more ranked by canirun.ai were skipped</span>{/if}</div>
          <div class={layout === 'grid' ? 'grid' : 'listwrap'}>
            {#each shown[c.key] as raw (raw.model_id)}
              {@const m = candidate(raw)}
              <ModelEntry {m} {layout} {dl} {blocked} onchange={refreshDownloads}>
                {#snippet children()}
                  {#if raw.variants?.length}
                    <details class="variants">
                      <summary>{raw.variants.length} unrestricted variant{raw.variants.length > 1 ? 's' : ''}</summary>
                      <div class="vlist">
                        {#each raw.variants as v (v.repo)}
                          <ModelEntry m={variant(v)} layout="list" {dl} {blocked} onchange={refreshDownloads} />
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
  {/if}
</div>

<style>
  .between { justify-content: space-between; align-items: flex-start; }
  /* a macOS-style segmented control for the three views */
  .views { display: inline-flex; align-self: flex-start; padding: 2px; gap: 2px; background: var(--surface-2); border: 1px solid var(--border); border-radius: 6px; }
  .views button { display: inline-flex; align-items: center; gap: 6px; min-height: 32px; padding: 0 14px; border: 0; border-radius: 4px; background: transparent; color: var(--muted); cursor: pointer; font-weight: 500; }
  .views button.on { background: var(--bg); color: var(--text); box-shadow: 0 1px 2px rgb(0 0 0 / 0.25); }
  .views button:hover:not(.on) { color: var(--text); }
  .count { font-size: 11px; min-width: 18px; padding: 0 5px; border-radius: 9px; background: var(--accent); color: var(--on-accent); text-align: center; }
  .filters { display: flex; flex-wrap: wrap; gap: 12px; align-items: center; }
  .search { flex: 1 1 220px; min-height: 36px; padding: 0 12px; border: 1px solid var(--border-strong); border-radius: var(--radius); background: var(--bg); color: var(--text); font: inherit; }
  .chips { display: flex; gap: 6px; flex-wrap: wrap; }
  .chips button { display: inline-flex; align-items: center; gap: 6px; min-height: 32px; padding: 0 12px; border: 1px solid var(--border-strong); border-radius: var(--radius); background: transparent; color: var(--muted); cursor: pointer; }
  .chips button.on { background: var(--accent); border-color: var(--accent); color: var(--on-accent); }
  .chips button:hover:not(.on) { color: var(--text); background: var(--surface-2); }
  @media (pointer: coarse) { .views button, .chips button, .search { min-height: var(--tap); } }
  @media (max-width: 760px) { .views { align-self: stretch; } .views button { flex: 1; justify-content: center; padding: 0 8px; } }
  .head { gap: 16px; }
  .controls { gap: 16px; }
  .seg { display: inline-flex; border: 1px solid var(--border-strong); border-radius: var(--radius); overflow: hidden; }
  .seg button { display: inline-flex; align-items: center; gap: 6px; min-height: var(--tap); padding: 0 14px; background: transparent; border: 0; cursor: pointer; color: var(--muted); }
  .seg button + button { border-left: 1px solid var(--border-strong); }
  .seg button.on { background: var(--surface-2); color: var(--text); }
  .toggle { display: flex; align-items: center; gap: 10px; min-height: var(--tap); cursor: pointer; }
  .toggle input { width: 20px; height: 20px; accent-color: var(--text); }
  .saving { display: flex; gap: 6px; align-items: center; flex-wrap: wrap; margin: 0; }
  .link { background: none; border: 0; padding: 0 2px; color: var(--text); text-decoration: underline; text-underline-offset: 3px; cursor: pointer; font: inherit; min-height: 24px; }
  .small { font-size: 13px; }
  .bad { color: var(--bad); display: flex; gap: 8px; align-items: center; flex-wrap: wrap; margin: 0; }
  .warn { color: var(--warn); display: flex; gap: 8px; align-items: center; }
  .listwrap { display: block; }
  .variants summary { min-height: 36px; color: var(--muted); }
  .vlist { padding-left: 12px; border-left: 1px solid var(--border-strong); margin-top: 6px; }
  ul { padding-left: 18px; margin: 10px 0 0; display: grid; gap: 6px; }
  @media (max-width: 700px) { .head { flex-direction: column; } }
</style>
