<script>
  import Icon from '../lib/Icon.svelte';
  import TrialsStrip from '../lib/TrialsStrip.svelte';
  import Sparkline from '../lib/Sparkline.svelte';
  import CompareBars from '../lib/CompareBars.svelte';
  import DataTable from '../lib/DataTable.svelte';
  import Warnings from '../lib/Warnings.svelte';
  import { api, reportUrl } from '../lib/api.js';
  import { fmtNum, fmtBytes } from '../lib/format.js';

  let runs = $state([]);
  let runId = $state('');
  let rep = $state(null);
  let err = $state('');
  let loading = $state(false);
  let view = $state('chart'); // chart | table
  let stageView = $state('');
  let cmpWith = $state('');
  let cmp = $state(null);

  async function load(id) {
    loading = true; err = ''; cmp = null; cmpWith = '';
    try {
      rep = await api.report(id);
      runId = rep.run.id;
      stageView = rep.stages.tuned ? 'tuned' : 'baseline';
    } catch (e) { rep = null; err = e.message; } finally { loading = false; }
  }
  $effect(() => { api.runs().then((r) => (runs = r.runs)).catch(() => {}); load(''); });

  const stage = $derived(rep?.stages?.[stageView]);
  const hw = $derived(rep?.hardware ?? {});
  const when = (t) => new Date(t).toLocaleString([], { dateStyle: 'medium', timeStyle: 'short' });
  const runLabel = (r) => `${when(r.created_at)}${r.stage ? ` · ${r.headline.mlx_generation_tps ? Math.round(r.headline.mlx_generation_tps) + ' tok/s' : r.stage}` : ' · no results'}`;
  const trialRows = $derived((stage?.metrics ?? []).map((m) => ({ label: m.label, result: `${fmtNum(m.value)} ${m.unit}`, spread: `${(spreadOf(m.trials)).toFixed(1)}%`, trials: m.trials.map((t) => fmtNum(t)).join(', ') })));
  function spreadOf(v) {
    if (!v || v.length < 2) return 0;
    const s = [...v].sort((a, b) => a - b);
    const t = s.length >= 5 ? s.slice(1, -1) : s;
    const med = s[Math.floor(s.length / 2)];
    return med ? ((t[t.length - 1] - t[0]) / 2 / med) * 100 : 0;
  }
  const isSeries = (m) => m.metric === 'sustained_matmul_fp16';
  async function doCompare() {
    if (!cmpWith || !runId) { cmp = null; return; }
    try { cmp = await api.compare(cmpWith, runId); } catch (e) { err = e.message; }
  }
</script>

<div class="stack">
  <div class="row between head">
    <div>
      <h2>Benchmark report</h2>
      <p class="muted">Every measurement with all its trials, what the numbers mean, and how this run compares. Export it or compare it with an earlier run.</p>
    </div>
    {#if rep}
      <div class="row">
        <a class="btn small" href={reportUrl(runId, 'md')} download><Icon name="download" size={14} /> Markdown</a>
        <a class="btn small" href={reportUrl(runId, 'csv')} download><Icon name="download" size={14} /> CSV</a>
        <a class="btn small" href={reportUrl(runId, 'json')} download><Icon name="download" size={14} /> JSON</a>
      </div>
    {/if}
  </div>

  {#if runs.length > 1}
    <label class="pick muted">Run
      <select bind:value={runId} onchange={() => load(runId)}>{#each runs as r}<option value={r.id}>{runLabel(r)}</option>{/each}</select>
    </label>
  {/if}

  {#if loading}<p class="muted">Building report</p>{/if}
  {#if err}<p class="bad" role="alert"><Icon name="alert" size={16} /> {err}</p>{/if}

  {#if rep && stage}
    <div class="row meta">
      <div class="seg" role="group" aria-label="Stage">
        {#each Object.keys(rep.stages) as k}<button class:on={stageView === k} onclick={() => (stageView = k)} aria-pressed={stageView === k}>{k === 'tuned' ? 'After tuning' : 'Baseline'}</button>{/each}
      </div>
      <div class="seg" role="group" aria-label="View">
        <button class:on={view === 'chart'} onclick={() => (view = 'chart')} aria-pressed={view === 'chart'}>Charts</button>
        <button class:on={view === 'table'} onclick={() => (view = 'table')} aria-pressed={view === 'table'}>Table</button>
      </div>
    </div>

    <Warnings items={stage.warnings} />

    {#if stage.derived.length}
      <section class="stack tight">
        <h3>What the numbers mean</h3>
        <div class="grid insights">
          {#each stage.derived as d (d.key)}
            <div class="card ins {d.level}">
              <div class="il">{d.label}</div>
              <div class="iv mono">{fmtNum(d.value)}<span class="u"> {d.unit}</span></div>
              {#if d.unit === '%' && d.value >= 0}<div class="meter" role="img" aria-label="{d.value.toFixed(0)} percent"><span style:width="{Math.min(100, d.value)}%"></span></div>{/if}
              {#if d.note}<p class="note">{d.note}</p>{/if}
              <p class="formula faint mono">{d.formula}</p>
            </div>
          {/each}
        </div>
      </section>
    {/if}

    <section class="stack tight">
      <h3>Measurements <span class="faint small">dots are individual trials; the bar is the median</span></h3>
      {#if view === 'table'}
        <div class="card"><DataTable caption="Benchmark measurements" columns={[{ key: 'label', label: 'Measurement' }, { key: 'result', label: 'Median', num: true }, { key: 'spread', label: 'Spread', num: true }, { key: 'trials', label: 'Trials' }]} rows={trialRows} /></div>
      {:else}
        <div class="card list">
          {#each stage.metrics as m (m.suite + m.engine + m.metric)}
            <div class="mrow">
              <div class="mname">{m.label}{#if m.model}<div class="faint mono small">{m.model}</div>{/if}</div>
              <div class="mchart">
                {#if isSeries(m)}<Sparkline values={m.trials} unit={m.unit} label={m.label} />
                {:else}<TrialsStrip values={m.trials} median={m.value} unit={m.unit} label={m.label} />{/if}
              </div>
              <div class="mval mono">{fmtNum(m.value)} <span class="muted">{m.unit}</span>
                {#if !isSeries(m)}<div class="faint small">±{spreadOf(m.trials).toFixed(1)}% · {m.trials.length} trials</div>{/if}</div>
            </div>
          {/each}
        </div>
      {/if}
    </section>

    {#if rep.compare?.length}
      <section class="stack tight">
        <h3>Before and after tuning</h3>
        {#if view === 'table'}
          <div class="card"><DataTable caption="Before and after" columns={[{ key: 'label', label: 'Measurement' }, { key: 'b', label: 'Before', num: true }, { key: 'a', label: 'After', num: true }, { key: 'd', label: 'Change', num: true }, { key: 'v', label: 'Verdict' }]}
            rows={rep.compare.map((r) => ({ label: r.label, b: fmtNum(r.before.median), a: fmtNum(r.after.median), d: `${r.delta_pct > 0 ? '+' : ''}${r.delta_pct.toFixed(1)}%`, v: r.verdict === 'within_noise' ? `within noise (±${r.noise_pct.toFixed(1)}%)` : r.verdict }))} /></div>
        {:else}
          <div class="card"><CompareBars rows={rep.compare} /></div>
        {/if}
        {#if rep.tune_changes.length}
          <p class="muted small">Applied: {rep.tune_changes.map((c) => c.key + (c.reverted_at ? ' (reverted)' : '')).join(', ')}</p>
        {/if}
      </section>
    {/if}

    {#if runs.length > 1}
      <section class="stack tight">
        <h3>Compare with another run</h3>
        <div class="row">
          <select bind:value={cmpWith} onchange={doCompare} aria-label="Compare this run with">
            <option value="">Choose a run to compare against</option>
            {#each runs.filter((r) => r.id !== runId && r.stage) as r}<option value={r.id}>{runLabel(r)}</option>{/each}
          </select>
        </div>
        {#if cmp}<div class="card"><CompareBars rows={cmp.rows} aName="Earlier run" bName="This run" /></div>{/if}
      </section>
    {/if}

    <section class="stack tight">
      <h3>This machine and software</h3>
      <div class="card"><dl class="kv">
        <dt>Machine</dt><dd>{hw.model?.name} <span class="faint mono">{hw.model?.identifier}</span>, {hw.cpu?.chip}</dd>
        <dt>Cores</dt><dd>{hw.cpu?.cores} CPU ({hw.cpu?.performance_cores}P + {hw.cpu?.efficiency_cores}E), {hw.gpu?.cores} GPU</dd>
        <dt>Memory</dt><dd>{hw.memory?.total_bytes ? fmtBytes(hw.memory.total_bytes) : ''} {hw.memory?.type}</dd>
        <dt>OS</dt><dd>{hw.os?.name} {hw.os?.version} <span class="faint mono">{hw.os?.build}</span></dd>
        {#each Object.entries(rep.versions ?? {}) as [k, v]}<dt>{k}</dt><dd class="mono">{v}</dd>{/each}
        <dt>aituner</dt><dd class="mono">{rep.aituner_version}</dd>
      </dl></div>
    </section>
  {/if}
</div>

<style>
  .between { justify-content: space-between; align-items: flex-start; } .head { gap: 16px; }
  .stack.tight { gap: 10px; }
  a.btn { text-decoration: none; color: var(--text); }
  .pick { display: flex; align-items: center; gap: 10px; }
  select { min-height: var(--tap); padding: 0 10px; border: 1px solid var(--border-strong); border-radius: var(--radius); background: var(--bg); color: var(--text); font: inherit; max-width: 100%; }
  .seg { display: inline-flex; border: 1px solid var(--border-strong); border-radius: var(--radius); overflow: hidden; }
  .seg button { min-height: var(--tap); padding: 0 14px; background: transparent; border: 0; cursor: pointer; color: var(--muted); }
  .seg button + button { border-left: 1px solid var(--border-strong); }
  .seg button.on { background: var(--btn-bg); color: var(--btn-fg); }
  .meta { gap: 12px; }
  .small { font-size: 13px; font-weight: 400; }
  .insights { grid-template-columns: repeat(auto-fill, minmax(min(100%, 280px), 1fr)); }
  .ins { display: grid; gap: 4px; align-content: start; }
  .ins.warn { border-color: var(--warn); }
  .il { font-size: 13px; color: var(--muted); } .iv { font-size: 26px; font-weight: 600; } .u { font-size: 14px; font-weight: 400; color: var(--muted); margin-left: 3px; }
  .meter { height: 6px; background: var(--border); border-radius: 3px; overflow: hidden; } .meter span { display: block; height: 100%; background: var(--series-1); }
  .note { font-size: 13px; margin: 2px 0 0; } .ins.warn .note { color: var(--warn); }
  .formula { font-size: 11.5px; margin: 4px 0 0; }
  .list { display: grid; padding: 0; }
  .mrow { display: grid; grid-template-columns: minmax(180px, 1.2fr) minmax(200px, 2fr) minmax(120px, 0.8fr); gap: 16px; align-items: center; padding: 12px 16px; border-top: 1px solid var(--border); }
  .mrow:first-child { border-top: 0; }
  .mval { text-align: right; white-space: nowrap; }
  .bad { color: var(--bad); display: flex; gap: 8px; align-items: center; }
  @media (max-width: 700px) { .mrow { grid-template-columns: 1fr; gap: 6px; } .mval { text-align: left; } .head { flex-direction: column; } }
</style>
