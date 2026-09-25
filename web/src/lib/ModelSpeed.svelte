<script>
  import Icon from './Icon.svelte';
  import Progress from './Progress.svelte';
  import LogPanel from './LogPanel.svelte';
  import { api } from './api.js';
  import { app } from './app.svelte.js';
  import { fmtNum } from './format.js';
  import { fastestRepo, recommendedRepo, INTERACTIVE_TPS } from './pick.js';

  // Measured speed of every downloaded model on this machine, with real code prompts. Prefill is how fast a prompt is
  // read (this sets the wait before the first word); decode is how fast the answer is written.
  let { st } = $props();
  let items = $state([]);
  let err = $state('');
  let busy = $state(false);

  const LEAN = 1800; // tokens in a Claude Code request through aituner-claude (--bare)
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
  const fmtSecs = (v) => (v <= 0 ? 'n/a' : v < 10 ? `${v.toFixed(1)} s` : v < 120 ? `${Math.round(v)} s` : `${(v / 60).toFixed(1)} min`);
  const rows = $derived(items.filter((i) => i.result && !i.result.error).map((i) => {
    const p1 = cell(i.result, 1024), p4 = cell(i.result, 4096), p16 = cell(i.result, 16384), k16 = cell(i.result, 16384, 8);
    return { repo: i.repo, size: i.size_gb, load: i.result.load_s, p4, p16, k16, lean: secs(LEAN, p1?.prefill_tps), full: secs(FULL, p16?.prefill_tps) };
  }).sort((a, b) => (b.p4?.decode_tps ?? 0) - (a.p4?.decode_tps ?? 0)));
  const failed = $derived(items.filter((i) => i.result?.error));
  const pending = $derived(items.filter((i) => !i.result));
  const fastest = $derived(fastestRepo(items));
  const suggested = $derived(recommendedRepo(items));
</script>

<section class="card stack tight" aria-label="Model speed">
  <div class="row between">
    <div class="row"><Icon name="gauge" /><h3>Model speed on this Mac</h3></div>
    <button class="btn small" onclick={start} disabled={busy || running || served || !items.length}>
      <Icon name="play" size={14} /> {rows.length ? 'Re-run benchmark' : 'Benchmark downloaded models'}
    </button>
  </div>
  {#if !items.length}<p class="muted">Download a model first; then measure how fast each one really runs here.</p>{/if}
  {#if served}<p class="faint small">Stop the running model in Setup to benchmark: the GPU has to be idle.</p>{/if}
  {#if pending.length && !running}<p class="faint small">Not measured yet: {pending.map((p) => p.repo.split('/').pop()).join(', ')}</p>{/if}
  {#if running}
    <Progress value={job.progress ?? 0} label="Model benchmark" />
    <LogPanel kinds={['modelbench']} />
  {/if}
  {#if job?.error}<p class="bad" role="alert"><Icon name="alert" size={16} /> {job.error}</p>{/if}
  {#if err}<p class="bad" role="alert"><Icon name="alert" size={16} /> {err}</p>{/if}
  {#each failed as f}<p class="bad small" role="alert">{f.repo}: {f.result.error}</p>{/each}

  {#if rows.length}
    <div class="scroll-x">
      <table>
        <thead>
          <tr><th>Model</th><th class="num">Size</th><th class="num">Prompt speed</th><th class="num">Reply speed, 4K ctx</th><th class="num">Reply speed, 16K ctx</th><th class="num">Peak memory</th><th class="num">Claude Code turn (lean)</th><th class="num">(full 27K)</th></tr>
        </thead>
        <tbody>
          {#each rows as r (r.repo)}
            <tr>
              <td class="mono name">{r.repo.split('/').pop()}{#if r.repo === suggested} <span class="badge ok">recommended</span>{:else if r.repo === fastest && rows.length > 1} <span class="badge">fastest</span>{/if}</td>
              <td class="num">{fmtNum(r.size)} GB</td>
              <td class="num">{r.p4 ? fmtNum(r.p4.prefill_tps) : 'n/a'} tok/s</td>
              <td class="num">{r.p4 ? fmtNum(r.p4.decode_tps) : 'n/a'} tok/s</td>
              <td class="num">{r.p16 ? fmtNum(r.p16.decode_tps) : 'n/a'} tok/s</td>
              <td class="num">{r.p16 ? fmtNum(r.p16.peak_gb) : 'n/a'} GB</td>
              <td class="num strong">{fmtSecs(r.lean)}</td>
              <td class="num">{fmtSecs(r.full)}</td>
            </tr>
          {/each}
        </tbody>
      </table>
    </div>
    <p class="faint small">Measured with real source code as the prompt, median of repeated runs. Prompt speed is how fast the model reads; reply speed is how fast it writes. Recommended = the largest model that still replies at {INTERACTIVE_TPS}+ tokens/s (a bigger model writes better code; the fastest is often too small). "Claude Code turn" is the wait before the first word of a cold request: about 1.8K tokens through <span class="mono">aituner-claude</span> (lean mode), or 27.7K for an unmodified Claude Code setup. Repeat turns reuse the cached prompt and are much faster.
      {#if rows.some((r) => r.k16)} An 8-bit KV cache used more memory and was slower in every test here, so aituner leaves it off.{/if}</p>
  {/if}
</section>

<style>
  .between { justify-content: space-between; }
  .small { font-size: 13px; margin: 0; }
  .bad { color: var(--bad); display: flex; gap: 8px; align-items: center; margin: 0; }
  .num { text-align: right; white-space: nowrap; font-variant-numeric: tabular-nums; }
  .strong { font-weight: 600; }
  .name { word-break: break-all; }
  .scroll-x > :global(table) { min-width: 760px; }
</style>
