<script>
  import Icon from '../lib/Icon.svelte';
  import { api } from '../lib/api.js';

  let { st } = $props();
  let data = $state(null);
  let err = $state('');
  let loading = $state(false);
  let unrestricted = $state(true);
  let copied = $state('');

  const CATS = [
    { key: 'code', title: 'Coding', icon: 'code' },
    { key: 'chat', title: 'Chat and reasoning', icon: 'chat' },
    { key: 'image', title: 'Image generation', icon: 'image' },
  ];

  async function load() {
    loading = true; err = '';
    try { data = await api.recommendations(unrestricted); } catch (e) { err = e.message; } finally { loading = false; }
  }
  $effect(() => { if (st.recommendations_unlocked && !data && !loading && !err) load(); });

  async function copy(text) {
    try { await navigator.clipboard.writeText(text); copied = text; setTimeout(() => (copied = ''), 1500); } catch { /* clipboard unavailable */ }
  }
  const gradeClass = (g) => (g === 'S' || g === 'A' ? 'ok' : g === 'B' || g === 'C' ? '' : 'warn');
  const bitsLabel = (b) => (b ? `${b}-bit` : 'quant unknown');
</script>

<div class="stack">
  <div class="row between">
    <div>
      <h2>What this machine can run</h2>
      <p class="muted">Sized to your tuned memory, ranked by canirun.ai fit, resolved to Apple-Silicon (MLX) builds that actually load here, with speeds calibrated to what you measured.</p>
    </div>
    <label class="toggle">
      <input type="checkbox" bind:checked={unrestricted} onchange={() => { data = null; load(); }} />
      <span>Include unrestricted variants</span>
    </label>
  </div>

  {#if loading}<p class="muted">Asking canirun.ai and Hugging Face</p>{/if}
  {#if err}<p class="bad" role="alert"><Icon name="alert" size={16} /> {err} <button class="btn small" onclick={load}>Retry</button></p>{/if}

  {#if data}
    <p class="muted">
      Memory budget {data.budget_gb.toFixed(1)} GB. Speed estimate = measured bandwidth scaled to each model's size (calibration {Math.round(data.efficiency * 100)}%). These are estimates; MoE speeds ignore routing overhead.
    </p>
    {#each CATS as c}
      {#if data.groups[c.key]?.length}
        <section class="stack">
          <div class="row"><Icon name={c.icon} /><h3>{c.title}</h3>
            {#if data.skipped[c.key]}<span class="faint">{data.skipped[c.key]} more ranked by canirun.ai were skipped</span>{/if}</div>
          <div class="grid">
            {#each data.groups[c.key] as m (m.model_id)}
              <article class="card stack">
                <div class="row between">
                  <div><h3>{m.name}</h3><div class="faint">{m.provider}{m.active_b ? `, ${m.params_b}B (${m.active_b}B active)` : `, ${m.params_b}B`}</div></div>
                  <span class="badge {gradeClass(m.grade)}" title="canirun.ai fit grade">grade {m.grade}</span>
                </div>
                <div class="row">
                  <span class="badge">{m.runtime === 'mlx' ? `MLX ${bitsLabel(m.bits)}` : 'MLX (mflux)'}</span>
                  <span class="badge {m.fit === 'comfortable' ? 'ok' : 'warn'}">{m.fit === 'comfortable' ? 'fits comfortably' : 'tight fit'}</span>
                  <span class="badge">{m.size_gb.toFixed(1)} GB</span>
                  {#if m.est_tps}<span class="badge">~{Math.round(m.est_tps)} tok/s</span>{/if}
                  {#if m.installed}<span class="badge ok">already in Ollama</span>{/if}
                </div>
                {#if m.notes?.length}{#each m.notes as n}<p class="muted small">{n}</p>{/each}{/if}
                <div class="cmd">
                  <pre class="mono">{m.run}</pre>
                  <button class="btn small" onclick={() => copy(m.run)} aria-label="Copy command"><Icon name={copied === m.run ? 'check' : 'copy'} size={14} /></button>
                </div>
                {#if m.variants?.length}
                  <details>
                    <summary>{m.variants.length} unrestricted variant{m.variants.length > 1 ? 's' : ''}</summary>
                    <div class="stack" style="margin-top:12px">
                      {#each m.variants as v (v.repo)}
                        <div class="variant stack">
                          <div class="mono repo">{v.repo}</div>
                          <div class="row">
                            <span class="badge">{bitsLabel(v.bits)}</span><span class="badge">{v.size_gb.toFixed(1)} GB</span>
                            <span class="badge {v.fit === 'comfortable' ? 'ok' : 'warn'}">{v.fit === 'comfortable' ? 'fits' : 'tight'}</span>
                            {#if v.est_tps}<span class="badge">~{Math.round(v.est_tps)} tok/s</span>{/if}
                            <span class="badge">{v.downloads.toLocaleString()} downloads</span>
                            {#if v.license}<span class="badge">{v.license}</span>{/if}
                          </div>
                          {#each v.warnings as w}<p class="faint small">{w}</p>{/each}
                          <div class="cmd"><pre class="mono">{v.run}</pre><button class="btn small" onclick={() => copy(v.run)} aria-label="Copy command"><Icon name={copied === v.run ? 'check' : 'copy'} size={14} /></button></div>
                        </div>
                      {/each}
                    </div>
                  </details>
                {/if}
                <a class="faint small" href={m.source_url} target="_blank" rel="noopener noreferrer">Model page</a>
              </article>
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
    <p class="faint small">Sources: {data.sources.join('; ')}. Install commands need the mlx-lm environment aituner created (see ~/Library/Application Support/aituner/venv/bin).</p>
  {/if}
</div>

<style>
  .between { justify-content: space-between; align-items: flex-start; }
  .toggle { display: flex; align-items: center; gap: 10px; min-height: var(--tap); cursor: pointer; }
  .toggle input { width: 20px; height: 20px; accent-color: var(--text); }
  .bad { color: var(--bad); display: flex; gap: 8px; align-items: center; flex-wrap: wrap; }
  .warn { color: var(--warn); display: flex; gap: 8px; align-items: center; }
  .small { font-size: 13px; }
  .cmd { display: flex; gap: 8px; align-items: stretch; }
  .cmd pre { flex: 1; margin: 0; padding: 8px 10px; background: var(--bg); border: 1px solid var(--border); border-radius: var(--radius); overflow-x: auto; white-space: pre; }
  .variant { border-top: 1px solid var(--border); padding-top: 12px; gap: 8px; }
  .repo { word-break: break-all; }
  summary { cursor: pointer; min-height: 28px; }
  ul { padding-left: 18px; margin: 10px 0 0; display: grid; gap: 6px; }
</style>
