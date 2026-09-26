<script>
  import Icon from './Icon.svelte';
  import Progress from './Progress.svelte';
  import LogPanel from './LogPanel.svelte';
  import { api } from './api.js';
  import { app } from './app.svelte.js';
  import { fmtNum } from './format.js';
  import { fastestRepo, recommendedRepo, INTERACTIVE_TPS } from './pick.js';

  // The models on this Mac and how fast each one really runs here, measured with real source code as the prompt.
  // Three numbers per model, in plain words; the full measurements are one click away.
  let { st, onnext } = $props();
  let items = $state([]);
  let err = $state('');
  let busy = $state(false);

  const LEAN = 1800; // tokens in a Claude Code request through aituner-claude (lean mode)
  const FULL = 27700; // tokens in an unmodified Claude Code request with a typical setup
  const job = $derived(st.job?.kind === 'modelbench' ? st.job : null);
  const running = $derived(!!job?.running);
  const served = $derived(app.serve?.server?.state && app.serve.server.state !== 'stopped' && app.serve.server.state !== 'error');

  async function load() { try { items = (await api.modelBench()).items; } catch (e) { err = e.message; } }
  $effect(() => { load(); });
  let wasRunning = false;
  $effect(() => { if (wasRunning && !running) load(); wasRunning = running; });

  async function start() {
    err = ''; busy = true;
    try { await api.startModelBench([]); } catch (e) { err = e.message; } finally { busy = false; }
  }
  const cell = (r, n, kv = 0) => r?.runs?.find((c) => c.prompt_tokens === n && c.kv_bits === kv);
  const secs = (tokens, tps) => (tps > 0 ? tokens / tps : 0);
  const fmtSecs = (v) => (v <= 0 ? '–' : v < 10 ? `${v.toFixed(1)} s` : v < 120 ? `${Math.round(v)} s` : `${(v / 60).toFixed(1)} min`);
  const models = $derived(items.map((i) => {
    const r = i.result && !i.result.error ? i.result : null;
    const p1 = cell(r, 1024), p4 = cell(r, 4096), p16 = cell(r, 16384);
    return { repo: i.repo, name: i.repo.split('/').pop(), org: i.repo.split('/')[0], size: i.size_gb, measured: !!r, error: i.result?.error,
      load: r?.load_s, p4, p16, lean: secs(LEAN, p1?.prefill_tps), full: secs(FULL, p16?.prefill_tps) };
  }).sort((a, b) => (b.p4?.decode_tps ?? -1) - (a.p4?.decode_tps ?? -1)));
  const fastest = $derived(fastestRepo(items));
  const suggested = $derived(recommendedRepo(items));
  const unmeasured = $derived(models.filter((m) => !m.measured && !m.error).length);
</script>

<div class="stack">
  <div class="row between">
    <p class="muted intro">Measured on this Mac with real source code as the prompt (median of repeated runs). Recommended is the largest model that still writes at {INTERACTIVE_TPS}+ tokens a second: bigger models write better code.</p>
    <button class="btn small" onclick={start} disabled={busy || running || served || !items.length}>
      <Icon name="gauge" size={14} /> {unmeasured === models.length ? 'Measure speeds' : unmeasured ? `Measure ${unmeasured} new` : 'Measure again'}
    </button>
  </div>
  {#if served}<p class="faint small">Stop the running model in Setup to measure: the GPU has to be idle.</p>{/if}
  {#if running}<Progress value={job.progress ?? 0} label="Measuring model speed" /><LogPanel kinds={['modelbench']} />{/if}
  {#if job?.error}<p class="bad" role="alert"><Icon name="alert" size={16} /> {job.error}</p>{/if}
  {#if err}<p class="bad" role="alert"><Icon name="alert" size={16} /> {err}</p>{/if}

  {#if !models.length}
    <div class="card empty"><Icon name="box" /><div><h3>No models yet</h3><p class="muted">Get one in Discover. It downloads in the background and appears here when it is ready.</p></div></div>
  {:else}
    <div class="models">
      {#each models as m (m.repo)}
        <article class="card model">
          <div class="row between top">
            <div class="who"><h3>{m.name}</h3><span class="faint small">{m.org} · {fmtNum(m.size)} GB</span></div>
            {#if m.repo === suggested}<span class="badge ok"><Icon name="check" size={12} /> recommended</span>
            {:else if m.repo === fastest && models.length > 1}<span class="badge">fastest</span>{/if}
          </div>
          {#if m.error}
            <p class="bad small">Could not be measured: {m.error}</p>
          {:else if !m.measured}
            <p class="faint small">Not measured yet. Measure speeds to see how it runs here.</p>
          {:else}
            <dl class="stats">
              <div><dt>Reads</dt><dd class="mono">{m.p4 ? fmtNum(m.p4.prefill_tps) : '–'}<span> tok/s</span></dd><p>how fast it takes in your prompt</p></div>
              <div><dt>Writes</dt><dd class="mono">{m.p4 ? fmtNum(m.p4.decode_tps) : '–'}<span> tok/s</span></dd><p>how fast the answer appears</p></div>
              <div><dt>First reply</dt><dd class="mono">{fmtSecs(m.lean)}</dd><p>wait before Claude Code's first word (lean mode)</p></div>
            </dl>
            <details>
              <summary>All measurements</summary>
              <table>
                <tbody>
                  <tr><th>Writes with a long (16K) context</th><td class="num mono">{m.p16 ? `${fmtNum(m.p16.decode_tps)} tok/s` : '–'}</td></tr>
                  <tr><th>Peak memory at 16K</th><td class="num mono">{m.p16 ? `${fmtNum(m.p16.peak_gb)} GB` : '–'}</td></tr>
                  <tr><th>First reply, unmodified Claude Code (27.7K-token request)</th><td class="num mono">{fmtSecs(m.full)}</td></tr>
                  <tr><th>Load time</th><td class="num mono">{m.load ? `${fmtNum(m.load)} s` : '–'}</td></tr>
                </tbody>
              </table>
              <p class="faint small">"First reply" is the prompt size divided by the measured reading speed: about 1.8K tokens through aituner's lean Claude Code launcher, 27.7K for a stock setup. Later turns reuse the cached prompt and start much sooner.</p>
            </details>
          {/if}
        </article>
      {/each}
    </div>
    <div class="card next">
      <div><h3>Next: run one</h3><p class="muted">Start a model in Setup and connect your editor.</p></div>
      <button class="btn primary" onclick={onnext}>Continue to Setup <Icon name="arrow" size={16} /></button>
    </div>
  {/if}
</div>

<style>
  .between { justify-content: space-between; align-items: flex-start; }
  .intro { max-width: 640px; }
  .small { font-size: 13px; margin: 0; }
  .bad { color: var(--bad); display: flex; gap: 8px; align-items: center; margin: 0; }
  .models { display: grid; gap: 12px; grid-template-columns: repeat(auto-fill, minmax(min(100%, 340px), 1fr)); }
  .model { display: grid; gap: 14px; align-content: start; }
  .top { flex-wrap: nowrap; }
  .who { display: grid; min-width: 0; } .who h3 { overflow-wrap: anywhere; }
  .stats { display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); gap: 12px; margin: 0; }
  .stats div { display: grid; gap: 2px; align-content: start; }
  .stats dt { font-size: 12px; color: var(--muted); text-transform: uppercase; letter-spacing: 0.04em; }
  .stats dd { margin: 0; font-size: 20px; font-weight: 600; }
  .stats dd span { font-size: 12px; font-weight: 400; color: var(--muted); }
  .stats p { font-size: 12px; color: var(--faint); margin: 0; line-height: 1.35; }
  details summary { min-height: 36px; font-size: 13px; color: var(--muted); }
  @media (pointer: coarse) { details summary { min-height: var(--tap); } }
  details table { margin: 6px 0 8px; }
  details th { text-transform: none; letter-spacing: 0; font-size: 13px; font-weight: 400; }
  .num { text-align: right; white-space: nowrap; }
  .empty { display: flex; gap: 14px; align-items: flex-start; }
  .next { display: flex; justify-content: space-between; gap: 16px; align-items: center; flex-wrap: wrap; }
  @media (max-width: 420px) { .stats { grid-template-columns: 1fr 1fr; } }
</style>
