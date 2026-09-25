<script>
  import Icon from '../lib/Icon.svelte';
  import Warnings from '../lib/Warnings.svelte';
  import Progress from '../lib/Progress.svelte';
  import LogPanel from '../lib/LogPanel.svelte';
  import { app, act } from '../lib/app.svelte.js';
  import { api } from '../lib/api.js';
  import { metricLabel, fmtNum } from '../lib/format.js';

  let { st, onnext } = $props();
  let err = $state('');
  let dialog;
  const running = $derived(st.phase === 'tuned_running');
  const applied = $derived(st.tune_changes.filter((c) => !c.reverted_at));
  const counts = $derived({
    faster: st.compare.filter((r) => r.verdict === 'faster').length,
    slower: st.compare.filter((r) => r.verdict === 'slower').length,
    noise: st.compare.filter((r) => r.verdict === 'within_noise').length,
  });
  const needsConfirm = $derived(st.bench_plan?.downloads?.length > 0);

  async function run(confirm) {
    err = ''; dialog?.close();
    try { await act(() => api.benchmark(confirm)); } catch (e) { err = e.message; }
  }
  async function revert() {
    err = '';
    try { await act(() => api.tuneRevert()); } catch (e) { err = e.message; }
  }
  const sign = (v) => (v > 0 ? '+' : '') + v.toFixed(1) + '%';
  const label = (r) => metricLabel({ suite: r.suite, engine: r.engine, metric: r.metric });
</script>

<div class="stack">
  <div>
    <h2>Re-run and compare</h2>
    <p class="muted">The same benchmark, after tuning. Differences smaller than the measured run-to-run noise are marked as such, not counted as gains.</p>
  </div>

  {#if st.phase === 'tune_reviewed'}
    <div class="card stack">
      <h3>{applied.length ? `${applied.length} change${applied.length > 1 ? 's' : ''} applied` : 'No changes applied'}</h3>
      {#each applied as c (c.id)}<div class="row"><Icon name="check" size={16} /> <span class="mono">{c.key}</span></div>{/each}
      {#if !applied.length}<p class="muted">Re-running anyway shows how much the benchmark varies on its own.</p>{/if}
      <div class="row">
        <button class="btn primary" onclick={() => (needsConfirm ? dialog.showModal() : run(false))} disabled={app.busy}><Icon name="play" size={16} /> Re-run benchmark</button>
        {#if applied.length}<button class="btn" onclick={revert} disabled={app.busy}>Revert changes</button>{/if}
      </div>
      {#if st.run?.note && st.run.note.includes('interrupted')}<p class="muted">{st.run.note}</p>{/if}
    </div>
  {:else if running}
    <div class="card stack">
      <Progress value={st.job?.progress ?? 0} label="Re-run progress" />
      <LogPanel kinds={['benchmark']} />
      <div><button class="btn small" onclick={() => act(() => api.cancel())}><Icon name="x" size={14} /> Cancel</button></div>
    </div>
  {:else if st.phase === 'tuned_done'}
    <Warnings items={[...(st.warnings?.baseline ?? []), ...(st.warnings?.tuned ?? [])]} />
    <div class="card row summary">
      <span class="badge ok">{counts.faster} faster</span>
      <span class="badge {counts.slower ? 'bad' : ''}">{counts.slower} slower</span>
      <span class="badge">{counts.noise} within noise</span>
      {#if !counts.faster && !counts.slower}<span class="muted">Tuning made no measurable difference to speed. That is a normal result: these changes mostly widen what fits in memory.</span>{/if}
    </div>
    <div class="card scroll-x">
      <table>
        <thead><tr><th>Measurement</th><th class="num">Before</th><th class="num">After</th><th class="num">Change</th><th>Verdict</th></tr></thead>
        <tbody>
          {#each st.compare as r (r.key)}
            <tr>
              <td>{label(r)}</td>
              <td class="num">{fmtNum(r.before.median)} <span class="muted">{r.unit}</span></td>
              <td class="num">{fmtNum(r.after.median)} <span class="muted">{r.unit}</span></td>
              <td class="num">{sign(r.delta_pct)}</td>
              <td>
                {#if r.verdict === 'faster'}<span class="badge ok">faster</span>
                {:else if r.verdict === 'slower'}<span class="badge bad">slower</span>
                {:else if r.verdict === 'info'}<span class="badge">info</span>
                {:else}<span class="badge">within noise (±{r.noise_pct.toFixed(1)}%)</span>{/if}
              </td>
            </tr>
          {/each}
        </tbody>
      </table>
    </div>
    <div class="row">
      <button class="btn primary" onclick={onnext}>Back to downloads <Icon name="arrow" size={16} /></button>
      {#if applied.length}<button class="btn" onclick={revert} disabled={app.busy}>Revert changes</button>{/if}
    </div>
  {/if}
  {#if err}<p class="bad" role="alert">{err}</p>{/if}
  {#if st.job?.error && st.job.kind !== 'tune'}<p class="bad" role="alert"><Icon name="alert" size={16} /> {st.job.error}</p>{/if}
</div>

<dialog bind:this={dialog} aria-labelledby="dl2">
  <h3 id="dl2">Install and download for the benchmark</h3>
  <ul>{#each st.bench_plan?.downloads ?? [] as d}<li>{d.what} <span class="muted">({d.size})</span></li>{/each}</ul>
  <div class="row end"><button class="btn" onclick={() => dialog.close()}>Cancel</button><button class="btn primary" onclick={() => run(true)}>Install and run</button></div>
</dialog>

<style>
  .bad { color: var(--bad); }
  .summary { gap: 10px; }
  dialog { background: var(--surface); color: var(--text); border: 1px solid var(--border-strong); border-radius: var(--radius); padding: 20px; max-width: min(520px, calc(100vw - 32px)); }
  dialog::backdrop { background: var(--overlay); }
  dialog ul { padding-left: 18px; margin: 12px 0 20px; display: grid; gap: 8px; }
  .end { justify-content: flex-end; }
</style>
