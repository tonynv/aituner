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
  import Monitor from './steps/Monitor.svelte';
  import Storage from './steps/Storage.svelte';
  import PinnedStats from './lib/PinnedStats.svelte';
  import BootScan from './lib/BootScan.svelte';
  import { app, start, phaseTab, perfUnlocked, PERF_TABS, act } from './lib/app.svelte.js';
  import { api } from './lib/api.js';

  const MAIN = [
    { tab: 'hardware', label: 'Hardware', icon: 'cpu' },
    { tab: 'downloads', label: 'Downloads', icon: 'download' },
    { tab: 'setup', label: 'Setup', icon: 'server' },
    { tab: 'monitor', label: 'Monitor', icon: 'activity' },
  ];
  const PERF = [
    { tab: 'benchmark', label: 'Benchmark', icon: 'gauge' },
    { tab: 'tune', label: 'Tune', icon: 'sliders' },
    { tab: 'rerun', label: 'Re-run', icon: 'refresh' },
  ];
  let theme = $state('system');
  let newRunDialog;
  async function confirmNewRun() {
    newRunDialog?.close();
    await act(() => api.newRun());
    app.tab = 'hardware';
  }
  let systemDark = $state(true);
  let scanning = $state(true);
  function detected(state) { app.state = state; app.detected = true; }
  function scanDone() { scanning = false; app.detected = true; }

  onMount(() => {
    try { const t = localStorage.getItem('aituner-theme'); if (t === 'dark' || t === 'light') theme = t; } catch { /* storage blocked */ }
    const mq = matchMedia('(prefers-color-scheme: dark)');
    systemDark = mq.matches;
    const onChange = (e) => (systemDark = e.matches);
    mq.addEventListener('change', onChange);
    const stop = start();
    // every page load runs detection again (shown by the start-up scan), then shows the machine
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

  // a new tab opens at its top, not wherever the previous page was scrolled to
  $effect(() => { app.tab; window.scrollTo({ top: 0 }); });
  // in the narrow top bar the sections scroll sideways: keep the current one in view
  $effect(() => { app.tab; requestAnimationFrame(() => document.querySelector('nav .item.current')?.scrollIntoView({ block: 'nearest', inline: 'nearest' })); });

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

{#snippet navItem(s)}
  <li>
    <button class="item" class:current={app.tab === s.tab} disabled={locked(s.tab)} onclick={() => (app.tab = s.tab)} aria-current={app.tab === s.tab ? 'page' : undefined}
      title={locked(s.tab) ? (s.tab === 'setup' ? 'Download a model first' : 'Available once the earlier performance step has run') : ''}>
      <Icon name={s.icon} size={16} />
      <span class="lbl">{s.label}</span>
      {#if locked(s.tab)}<span class="state" aria-label="locked"><Icon name="lock" size={12} /></span>
      {:else if done(s.tab)}<span class="state" aria-label="done"><Icon name="check" size={12} /></span>{/if}
    </button>
  </li>
{/snippet}

<!-- A native-style layout: a sidebar of sections (System Settings, Finder) beside the content. On narrow screens the
     sidebar becomes a top bar with the same sections in a scrolling row. -->
<div class="app">
  <aside class="sidebar">
    <div class="brand"><Icon name="gauge" size={18} /><span class="name">aituner</span></div>
    {#if st && st.supported}
      <nav aria-label="Sections">
        <ul>{#each MAIN as s (s.tab)}{@render navItem(s)}{/each}</ul>
        <p class="group">Performance<span class="opt">optional</span></p>
        <ul>{#each PERF as s (s.tab)}{@render navItem(s)}{/each}</ul>
        <p class="group">Settings</p>
        <ul>{@render navItem({ tab: 'storage', label: 'Storage', icon: 'disk' })}</ul>
      </nav>
    {/if}
    <div class="foot">
      {#if machine}<span class="machine">{machine}</span>{/if}
      <div class="tools">
        {#if st?.phase === 'tuned_done'}<button class="btn small" onclick={() => newRunDialog.showModal()}><Icon name="refresh" size={14} /> New run</button>{/if}
        <button class="btn small" onclick={toggleTheme} aria-label="Toggle dark and light theme"><Icon name={dark ? 'sun' : 'moon'} size={16} /></button>
      </div>
    </div>
  </aside>

  <div class="content">
    {#if app.error}
      <div class="banner" role="alert"><Icon name="alert" size={16} /> {app.error}</div>
    {/if}

    {#if st && st.supported}
      <PinnedStats {st} serve={app.serve} onreport={() => (app.tab = app.tab === 'report' ? 'hardware' : 'report')} onrun={() => (app.tab = 'setup')} />
    {/if}

    {#if st && !st.supported}
      <main><div class="card stack"><h2>Not supported yet</h2><p class="muted">{st.message}</p></div></main>
    {:else if st}
      <main>
        {#if app.tab === 'setup'}<Run {st} />
        {:else if app.tab === 'monitor'}<Monitor />
        {:else if app.tab === 'storage'}<Storage />
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
</div>

{#if scanning}<BootScan ondetected={detected} ondone={scanDone} />{/if}

<dialog bind:this={newRunDialog} aria-labelledby="nr-title">
  <h3 id="nr-title">Start a new run?</h3>
  <p class="muted">This starts over from the hardware step. The current results stay saved and can still be compared in the report.</p>
  <div class="row end">
    <button class="btn" onclick={() => newRunDialog.close()}>Keep current results</button>
    <button class="btn primary" onclick={confirmNewRun}>Start new run</button>
  </div>
</dialog>

<style>
  .app { display: grid; grid-template-columns: 224px minmax(0, 1fr); min-height: 100vh; }
  .sidebar { position: sticky; top: 0; height: 100vh; display: flex; flex-direction: column; gap: 18px; padding: 16px 10px 12px; background: var(--surface); border-right: 1px solid var(--border); overflow-y: auto; }
  .brand { display: flex; align-items: center; gap: 8px; padding: 0 8px; font-weight: 600; font-size: 15px; }
  nav ul { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; gap: 2px; }
  .group { margin: 16px 8px 6px; font-size: 11px; font-weight: 600; color: var(--muted); text-transform: uppercase; letter-spacing: 0.06em; }
  .opt { margin-left: 6px; font-weight: 400; text-transform: none; letter-spacing: 0; color: var(--faint); }
  /* sidebar rows behave like buttons: fill on hover, darker while pressed, filled when selected */
  .item { display: flex; align-items: center; gap: 10px; width: 100%; min-height: 32px; padding: 0 8px; border: 0; border-radius: var(--radius); background: transparent; color: var(--text); cursor: pointer; text-align: left; font-size: 14px; transition: background-color 0.1s; }
  .item:hover:not(:disabled) { background: var(--border); }
  .item:active:not(:disabled) { background: var(--border-strong); }
  .item.current { background: var(--border-strong); font-weight: 600; }
  .item:disabled { cursor: not-allowed; color: var(--faint); }
  .lbl { flex: 1; white-space: nowrap; }
  .state { display: inline-flex; color: var(--muted); }
  .foot { margin-top: auto; display: flex; flex-direction: column; gap: 10px; padding: 0 8px; }
  .machine { font-size: 12px; color: var(--muted); }
  .tools { display: flex; gap: 8px; }
  .content { min-width: 0; max-width: 1080px; width: 100%; padding: 0 28px 48px; }
  .banner { display: flex; gap: 8px; align-items: center; margin-top: 16px; padding: 12px 14px; border: 1px solid var(--bad); color: var(--bad); border-radius: var(--radius); }
  main { padding-top: 20px; }
  dialog { background: var(--surface); color: var(--text); border: 1px solid var(--border-strong); border-radius: var(--radius); padding: 20px; max-width: min(520px, calc(100vw - 32px)); }
  dialog::backdrop { background: var(--overlay); }
  dialog p { margin: 10px 0 20px; }
  .end { justify-content: flex-end; }
  @media (pointer: coarse) { .item { min-height: var(--tap); } }
  /* narrow: the sidebar becomes a top bar, sections scroll sideways */
  @media (max-width: 760px) {
    .app { display: block; }
    .sidebar { position: static; height: auto; flex-direction: row; flex-wrap: wrap; align-items: center; gap: 8px 12px; padding: 12px var(--gutter); border-right: 0; border-bottom: 1px solid var(--border); }
    .brand { padding: 0; }
    nav { order: 3; flex-basis: 100%; display: flex; align-items: center; gap: 6px; overflow-x: auto; scrollbar-width: none; }
    nav::-webkit-scrollbar { display: none; }
    nav ul { flex-direction: row; }
    .item { width: auto; min-height: var(--tap); }
    .group { margin: 0 2px 0 8px; padding-left: 10px; border-left: 1px solid var(--border-strong); white-space: nowrap; }
    .opt { display: none; }
    .foot { margin: 0 0 0 auto; flex-direction: row; align-items: center; padding: 0; }
    .machine { display: none; }
    .content { padding: 0 var(--gutter) 48px; }
  }
</style>
