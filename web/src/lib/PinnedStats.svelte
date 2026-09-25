<script>
  import Icon from './Icon.svelte';
  import { fmtNum } from './format.js';
  // The headline numbers, pinned under the header on every tab. Data is the run being viewed: tuned results when they
  // exist (with the change against baseline), else baseline. Nothing is shown that was not measured.
  let { st, serve, onreport, onrun } = $props();

  const CHIPS = [
    { key: 'gpu_fp16_tflops', metric: 'gpu/mlx/matmul_fp16', label: 'GPU fp16', unit: 'TFLOPS' },
    { key: 'gpu_bandwidth_gbs', metric: 'gpu/mlx/mem_bandwidth', label: 'GPU bandwidth', unit: 'GB/s' },
    { key: 'mlx_generation_tps', metric: 'llm/mlx/generation_tps', label: 'MLX generate', unit: 'tok/s' },
    { key: 'mlx_prompt_tps', metric: 'llm/mlx/prompt_tps', label: 'MLX prefill', unit: 'tok/s' },
    { key: 'ollama_generation_tps', metric: 'llm/ollama/generation_tps', label: 'Ollama generate', unit: 'tok/s' },
  ];
  const head = $derived(st.headline?.tuned ?? st.headline?.baseline ?? null);
  const stage = $derived(st.headline_from ? 'last measured run' : st.headline?.tuned ? 'after tuning' : st.headline?.baseline ? 'baseline' : '');
  const rows = $derived(Object.fromEntries((st.headline_from ? [] : st.compare ?? []).map((r) => [r.key, r])));
  const hasOllama = $derived(!!st.hardware?.software?.ollama?.installed);
  const chips = $derived(CHIPS.filter((c) => head && head[c.key] > 0 && (hasOllama || c.key !== 'ollama_generation_tps')).map((c) => ({ ...c, value: head[c.key], cmp: rows[c.metric] })));
  const pw = $derived(st.hardware?.power);
  const health = $derived(!pw ? '' : pw.source === 'battery' ? 'Battery' : pw.thermal_note?.includes('No thermal') ? 'AC, cool' : pw.source === 'ac' ? 'AC' : '');
  const sign = (v) => (v > 0 ? '+' : '') + v.toFixed(1) + '%';
</script>

<aside class="pin" aria-label="Pinned performance stats">
  <div class="strip">
    {#if chips.length}
      {#each chips as c (c.key)}
        <div class="chip" title="{c.label}: {fmtNum(c.value)} {c.unit} ({stage})">
          <span class="l">{c.label}</span>
          <span class="v mono">{fmtNum(c.value)}<span class="u"> {c.unit}</span></span>
          {#if c.cmp}
            <span class="d mono {c.cmp.verdict}" aria-label="{sign(c.cmp.delta_pct)} versus baseline, {c.cmp.verdict.replace('_', ' ')}">{c.cmp.verdict === 'within_noise' ? '±' : sign(c.cmp.delta_pct)}</span>
          {/if}
        </div>
      {/each}
      {#if st.budget_gb > 0}
        <div class="chip"><span class="l">GPU memory</span><span class="v mono">{fmtNum(st.budget_gb)}<span class="u"> GB</span></span></div>
      {/if}
      {#if health}<div class="chip"><span class="l">Power</span><span class="v">{health}</span></div>{/if}
    {:else}
      <span class="empty muted">Benchmark numbers pin here once measured.</span>
    {/if}
  </div>
  <div class="tail">
    {#if serve && serve.server && serve.server.state !== 'stopped'}
      <button class="chip serving" onclick={onrun} title="Open Setup: {serve.server.repo}" aria-label="Model {serve.server.state}: {serve.server.repo}">
        <span class="l">Model</span>
        <span class="v">{serve.server.state === 'running' ? 'running' : serve.server.state === 'starting' ? 'loading' : serve.server.state}<span class="u"> {serve.server.repo?.split('/').pop()}</span></span>
      </button>
    {/if}
    {#if stage}<span class="stage faint">{stage}</span>{/if}
    <button class="btn small" onclick={onreport} disabled={!head}><Icon name="gauge" size={14} /> Report</button>
  </div>
</aside>

<style>
  .pin { position: sticky; top: 0; z-index: 20; display: flex; align-items: center; gap: 12px; background: var(--bg); border-bottom: 1px solid var(--border); padding: 8px 0; margin: 0 0 4px; }
  .strip { display: flex; gap: 8px; overflow-x: auto; flex: 1; min-width: 0; scrollbar-width: none; -webkit-overflow-scrolling: touch; }
  .strip::-webkit-scrollbar { display: none; }
  .chip { display: flex; flex-direction: column; gap: 1px; padding: 5px 10px; border: 1px solid var(--border); background: var(--surface); border-radius: var(--radius); flex: none; min-width: 84px; }
  .l { font-size: 12px; color: var(--muted); text-transform: uppercase; letter-spacing: 0.04em; white-space: nowrap; }
  .v { font-size: 14px; font-weight: 600; white-space: nowrap; } .u { font-weight: 400; color: var(--muted); font-size: 12px; margin-left: 3px; }
  .d { font-size: 12px; color: var(--muted); }
  .d.faster { color: var(--ok); } .d.slower { color: var(--bad); }
  .tail { display: flex; gap: 10px; align-items: center; flex: none; }
  .stage { font-size: 12px; white-space: nowrap; }
  .serving { cursor: pointer; color: var(--text); font: inherit; text-align: left; border-color: var(--ok); }
  .serving .u { max-width: 120px; overflow: hidden; text-overflow: ellipsis; display: inline-block; vertical-align: bottom; white-space: nowrap; }
  .empty { font-size: 13px; padding: 6px 0; }
  @media (max-width: 560px) { .stage { display: none; } .chip { min-width: 78px; padding: 4px 8px; } }
</style>
