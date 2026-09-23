<script>
  import Progress from '../lib/Progress.svelte';
  import LogPanel from '../lib/LogPanel.svelte';
  import MetricsTable from '../lib/MetricsTable.svelte';
  import Icon from '../lib/Icon.svelte';
  import { app, act } from '../lib/app.svelte.js';
  import { api } from '../lib/api.js';

  let { st } = $props();
  const running = $derived(st.phase === 'baseline_running');
  const failed = $derived(st.phase === 'detected' && st.run?.note);
</script>

<div class="stack">
  <div>
    <h2>Baseline benchmark</h2>
    <p class="muted">Real measurements on this machine: memory bandwidth, GPU compute, and LLM speed through MLX and Ollama.</p>
  </div>

  {#if running}
    <div class="card stack">
      <Progress value={st.job?.progress ?? 0} label="Benchmark progress" />
      <LogPanel kinds={['benchmark']} />
      <div><button class="btn small" onclick={() => act(() => api.cancel())}><Icon name="x" size={14} /> Cancel</button></div>
    </div>
  {:else}
    {#if failed}<p class="bad" role="alert"><Icon name="alert" size={16} /> {st.run.note}</p>{/if}
    {#if st.baseline.length}
      <div class="card"><MetricsTable metrics={st.baseline} /></div>
      {#if st.skipped?.baseline?.length}
        <p class="muted"><Icon name="info" size={14} /> Skipped: {st.skipped.baseline.join('; ')}</p>
      {/if}
    {/if}
  {/if}
</div>

<style>
  .bad { color: var(--bad); display: flex; gap: 8px; align-items: center; }
</style>
