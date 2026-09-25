<script>
  import { onMount } from 'svelte';
  import Icon from './lib/Icon.svelte';
  import Hardware from './steps/Hardware.svelte';
  import Benchmark from './steps/Benchmark.svelte';
  import Tune from './steps/Tune.svelte';
  import Rerun from './steps/Rerun.svelte';
  import Downloads from './steps/Downloads.svelte';
  import Report from './steps/Report.svelte';
  import Run from './steps/Run.svelte';
  import PinnedStats from './lib/PinnedStats.svelte';
  import { app, start, phaseTab, perfUnlocked, PERF_TABS, act } from './lib/app.svelte.js';
  import { api } from './lib/api.js';

  const MAIN = [
    { tab: 'hardware', label: 'Hardware' },
    { tab: 'downloads', label: 'Downloads' },
    { tab: 'setup', label: 'Setup' },
  ];
  const PERF = [
    { tab: 'benchmark', label: 'Benchmark' },
    { tab: 'tune', label: 'Tune' },
    { tab: 'rerun', label: 'Re-run' },
  ];
  let theme = $state('system');
  let newRunDialog;
  async function confirmNewRun() {
    newRunDialog?.close();
    await act(() => api.newRun());
    app.tab = 'hardware';
  }
  let systemDark = $state(true);

  onMount(() => {
    try { const t = localStorage.getItem('aituner-theme'); if (t === 'dark' || t === 'light') theme = t; } catch { /* storage blocked */ }
    const mq = matchMedia('(prefers-color-scheme: dark)');
    systemDark = mq.matches;
    const onChange = (e) => (systemDark = e.matches);
    mq.addEventListener('change', onChange);
    const stop = start();
    // every page load runs detection again, then shows the machine
    api.detect().catch(() => {}).finally(() => { app.detected = true; });
    return () => { mq.removeEventListener('change', onChange); stop(); };
  });
  $effect(() => {
    const root = document.documentElement;
    if (theme === 'system') root.removeAttribute('data-theme'); else root.setAttribute('data-theme', theme);
  });
  const dark = $derived(theme === 'system' ? systemDark : theme === 'dark');
  function toggleTheme() {
    theme = dark ? 'light' : 'dark';
    try { localStorage.setItem('aituner-theme', theme); } catch { /* storage blocked */ }
  }

  const st = $derived(app.state);
  const runReady = $derived(!!app.serve && ((app.serve.models?.length ?? 0) > 0 || app.serve.server?.state !== 'stopped'));
  const serving = $derived(app.serve?.server?.state === 'running');
  const machine = $derived(st?.hardware ? `${st.hardware.model.name}, ${st.hardware.cpu.chip}` : '');
  const canReport = $derived(!!(st?.headline?.tuned || st?.headline?.baseline));
  // the performance track follows the server's phase (benchmark starts -> Benchmark, finishes -> Tune, ...)
  let lastPhase = null;
  $effect(() => {
    const p = st?.phase;
    if (p && lastPhase && p !== lastPhase && PERF_TABS.includes(app.tab)) app.tab = phaseTab();
    if (p) lastPhase = p;
  });
  const done = (tab) => tab === 'hardware' ? app.detected : tab === 'downloads' ? runReady : tab === 'setup' ? serving : PERF_TABS.includes(tab) && perfUnlocked(tab) && app.tab !== tab && phaseTab() !== tab;
  const locked = (tab) => tab === 'setup' ? !runReady : PERF_TABS.includes(tab) ? !perfUnlocked(tab) : tab === 'report' ? !canReport : false;
</script>

{#snippet tabButton(s, n)}
  <li>
    <button class="step" class:current={app.tab === s.tab} class:done={done(s.tab)} disabled={locked(s.tab)} onclick={() => (app.tab = s.tab)} aria-current={app.tab === s.tab ? 'step' : undefined}
      title={locked(s.tab) ? (s.tab === 'setup' ? 'Download a model first' : 'Available once the earlier performance step has run') : ''}>
      <span class="num">{#if locked(s.tab)}<Icon name="lock" size={12} />{:else if done(s.tab)}<Icon name="check" size={14} />{:else if n}{n}{:else}<Icon name="gauge" size={12} />{/if}</span>
      <span class="lbl">{s.label}</span>
    </button>
  </li>
{/snippet}

<div class="shell">
  <header>
    <div class="brand"><Icon name="gauge" size={20} /><h1>aituner</h1>{#if machine}<span class="muted machine">{machine}</span>{/if}</div>
    <div class="row">
      {#if st?.phase === 'tuned_done'}<button class="btn small" onclick={() => newRunDialog.showModal()}><Icon name="refresh" size={14} /> New run</button>{/if}
      <button class="btn small" onclick={toggleTheme} aria-label="Toggle dark and light theme"><Icon name={dark ? 'sun' : 'moon'} size={16} /></button>
    </div>
  </header>

  {#if app.error}
    <div class="banner" role="alert"><Icon name="alert" size={16} /> {app.error}</div>
  {/if}

  {#if st && st.supported}
    <PinnedStats {st} serve={app.serve} onreport={() => (app.tab = app.tab === 'report' ? 'hardware' : 'report')} onrun={() => (app.tab = 'setup')} />
  {/if}

  {#if st && !st.supported}
    <main><div class="card stack"><h2>Not supported yet</h2><p class="muted">{st.message}</p></div></main>
  {:else if st}
    <nav aria-label="Steps">
      <ol>
        {#each MAIN as s, i}
          {@render tabButton(s, i + 1)}
        {/each}
        <li class="sep" aria-hidden="true"></li>
        <li class="group faint" aria-hidden="true">Performance, optional</li>
        {#each PERF as s}
          {@render tabButton(s, 0)}
        {/each}
      </ol>
    </nav>
    <main>
      {#if app.tab === 'setup'}<Run {st} />
      {:else if app.tab === 'report'}<Report />
      {:else if app.tab === 'downloads'}<Downloads {st} onnext={() => (app.tab = 'setup')} />
      {:else if app.tab === 'benchmark'}<Benchmark {st} />
      {:else if app.tab === 'tune'}<Tune {st} />
      {:else if app.tab === 'rerun'}<Rerun {st} onnext={() => (app.tab = 'downloads')} />
      {:else}<Hardware hw={st.hardware} plan={st.bench_plan} phase={st.phase} detecting={!app.detected} />
      {/if}
    </main>
  {:else if !app.error}
    <main><p class="muted">Detecting hardware</p></main>
  {/if}
</div>

<dialog bind:this={newRunDialog} aria-labelledby="nr-title">
  <h3 id="nr-title">Start a new run?</h3>
  <p class="muted">This starts over from the hardware step. The current results stay saved and can still be compared in the report.</p>
  <div class="row end">
    <button class="btn" onclick={() => newRunDialog.close()}>Keep current results</button>
    <button class="btn primary" onclick={confirmNewRun}>Start new run</button>
  </div>
</dialog>

<style>
  .shell { max-width: 1040px; margin: 0 auto; padding: 0 var(--gutter) 48px; }
  header { display: flex; align-items: center; justify-content: space-between; gap: 12px; padding: 16px 0; border-bottom: 1px solid var(--border); }
  .brand { display: flex; align-items: center; gap: 10px; min-width: 0; }
  .machine { font-size: 13px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .banner { display: flex; gap: 8px; align-items: center; margin-top: 16px; padding: 12px 14px; border: 1px solid var(--bad); color: var(--bad); border-radius: var(--radius); }
  nav { margin: 20px 0; overflow-x: auto; }
  ol { list-style: none; display: flex; gap: 8px; padding: 0; margin: 0; min-width: max-content; }
  .step { display: flex; align-items: center; gap: 10px; min-height: var(--tap); padding: 0 14px 0 8px; border: 1px solid var(--border); border-radius: var(--radius); background: transparent; cursor: pointer; color: var(--muted); }
  .step:disabled { cursor: not-allowed; color: var(--faint); }
  .step.current { border-color: var(--text); color: var(--text); }
  .step .num { display: inline-flex; align-items: center; justify-content: center; width: 24px; height: 24px; border: 1px solid currentColor; border-radius: var(--radius); font-size: 12px; font-variant-numeric: tabular-nums; }
  .step.done .num { background: var(--btn-bg); color: var(--btn-fg); border-color: var(--btn-bg); }
  .sep { width: 1px; background: var(--border-strong); margin: 6px 4px; }
  .group { align-self: center; font-size: 12px; white-space: nowrap; }
  main { padding-top: 4px; }
  dialog { background: var(--surface); color: var(--text); border: 1px solid var(--border-strong); border-radius: var(--radius); padding: 20px; max-width: min(520px, calc(100vw - 32px)); }
  dialog::backdrop { background: var(--overlay); }
  dialog p { margin: 10px 0 20px; }
  .end { justify-content: flex-end; }
  @media (max-width: 560px) { .machine { display: none; } .lbl { font-size: 14px; } }
</style>
