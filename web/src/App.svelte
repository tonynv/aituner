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
    { tab: 'hardware', tile: 'graphite', label: 'Hardware', icon: 'cpu' },
    { tab: 'downloads', tile: 'blue', label: 'Downloads', icon: 'download' },
    { tab: 'setup', tile: 'indigo', label: 'Setup', icon: 'server' },
    { tab: 'monitor', tile: 'green', label: 'Monitor', icon: 'activity' },
  ];
  const PERF = [
    { tab: 'benchmark', tile: 'orange', label: 'Benchmark', icon: 'gauge' },
    { tab: 'tune', tile: 'purple', label: 'Tune', icon: 'sliders' },
    { tab: 'rerun', tile: 'teal', label: 'Re-run', icon: 'refresh' },
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

  // phones: bottom tab bar (main path + More); everything else lives in the More sheet
  let moreSheet;
  const MORE = [
    { tab: 'benchmark', tile: 'orange', label: 'Benchmark', icon: 'gauge' },
    { tab: 'tune', tile: 'purple', label: 'Tune', icon: 'sliders' },
    { tab: 'rerun', tile: 'teal', label: 'Re-run', icon: 'refresh' },
    { tab: 'report', tile: 'blue', label: 'Report', icon: 'list' },
    { tab: 'storage', tile: 'pink', label: 'Storage', icon: 'disk' },
  ];
  const TITLES = { hardware: 'Hardware', downloads: 'Downloads', setup: 'Setup', monitor: 'Monitor', benchmark: 'Benchmark', tune: 'Tune', rerun: 'Re-run', report: 'Report', storage: 'Storage' };
  const inMore = $derived(MORE.some((m) => m.tab === app.tab));
  function go(tab) { app.tab = tab; moreSheet?.close(); }
  // iOS large titles: the bar's small title fades in only once the page's own big title has scrolled under the bar
  let compact = $state(false);
  $effect(() => {
    app.tab;
    let raf = 0;
    const check = () => {
      raf = 0;
      const h = document.querySelector('main h2');
      compact = !h || h.getBoundingClientRect().bottom < 44 + (parseFloat(getComputedStyle(document.documentElement).getPropertyValue('--safe-t')) || 0);
    };
    const onScroll = () => { if (!raf) raf = requestAnimationFrame(check); };
    addEventListener('scroll', onScroll, { passive: true });
    requestAnimationFrame(check);
    return () => { removeEventListener('scroll', onScroll); cancelAnimationFrame(raf); };
  });
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
  // Setup is always open: Bootstrap and the MLX install live there; its model and editor parts wait for a download
  const locked = (tab) => PERF_TABS.includes(tab) ? !perfUnlocked(tab) : tab === 'report' ? !canReport : false;
</script>

{#snippet navItem(s)}
  <li>
    <button class="item" class:current={app.tab === s.tab} disabled={locked(s.tab)} onclick={() => (app.tab = s.tab)} aria-current={app.tab === s.tab ? 'page' : undefined}
      title={locked(s.tab) ? 'Available once the earlier performance step has run' : ''}>
      <span class="tile" style:background="var(--tile-{s.tile})"><Icon name={s.icon} size={14} /></span>
      <span class="lbl">{s.label}</span>
      {#if locked(s.tab)}<span class="state" aria-label="locked"><Icon name="lock" size={12} /></span>
      {:else if done(s.tab)}<span class="state" aria-label="done"><Icon name="check" size={12} /></span>{/if}
    </button>
  </li>
{/snippet}

{#snippet serviceList()}
  <ul class="services" aria-label="Tools and services">
    {#each app.services as sv (sv.id)}
      <li title="{sv.name}: {sv.detail}">
        <span class="dot" class:on={sv.active} class:idle={sv.installed && !sv.active} aria-hidden="true"></span>
        <span class="sname">{sv.name}</span>
        <span class="sdetail">{sv.active ? 'active' : sv.installed ? 'idle' : 'off'}</span>
      </li>
    {/each}
  </ul>
{/snippet}

<!-- Native-style layouts: on a Mac-sized window a sidebar of sections (System Settings, Finder) beside the content; on a
     phone an iOS layout: a pinned, blurred header with the section title, a bottom tab bar and a More sheet. -->
<header class="mhead">
  <span class="mbrand"><Icon name="gauge" size={16} /></span>
  <span class="mtitle" class:shown={compact}>{TITLES[app.tab] ?? 'aituner'}</span>
  <button class="mbtn" onclick={toggleTheme} aria-label="Toggle dark and light theme"><Icon name={dark ? 'sun' : 'moon'} size={20} /></button>
</header>
<div class="app">
  <aside class="sidebar">
    <div class="brand"><Icon name="gauge" size={18} /><span class="name">aituner</span></div>
    {#if st && st.supported}
      <nav aria-label="Sections">
        <ul>{#each MAIN as s (s.tab)}{@render navItem(s)}{/each}</ul>
        <p class="group">Performance<span class="opt">optional</span></p>
        <ul>{#each PERF as s (s.tab)}{@render navItem(s)}{/each}</ul>
        <p class="group">Settings</p>
        <ul>{@render navItem({ tab: 'storage', tile: 'pink', label: 'Storage', icon: 'disk' })}</ul>
      </nav>
    {/if}
    <div class="foot">
      {#if app.services.length}{@render serviceList()}{/if}
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

{#if st && st.supported}
  <nav class="tabbar" aria-label="Sections">
    {#each MAIN as s (s.tab)}
      <button class:current={app.tab === s.tab} onclick={() => go(s.tab)} aria-current={app.tab === s.tab ? 'page' : undefined}>
        <Icon name={s.icon} size={24} /><span>{s.label}</span>
      </button>
    {/each}
    <button class:current={inMore} onclick={() => { moreSheet.showModal(); moreSheet.focus(); }} aria-haspopup="dialog">
      <Icon name="grid" size={24} /><span>More</span>
    </button>
  </nav>
{/if}

<dialog class="sheet" bind:this={moreSheet} aria-label="More" tabindex="-1" onclick={(e) => e.target === moreSheet && moreSheet.close()}>
  <div class="grabber" aria-hidden="true"></div>
  <ul class="group-list">
    {#each MORE as m (m.tab)}
      <li><button class:current={app.tab === m.tab} disabled={locked(m.tab)} onclick={() => go(m.tab)}>
        <span class="tile" style:background="var(--tile-{m.tile})"><Icon name={m.icon} size={16} /></span>
        <span class="lbl">{m.label}</span>
        {#if locked(m.tab)}<Icon name="lock" size={14} />{:else}<span class="chev"><Icon name="chevron" size={16} /></span>{/if}
      </button></li>
    {/each}
  </ul>
  {#if app.services.length}<p class="sheet-h">Tools and services</p><div class="group-box">{@render serviceList()}</div>{/if}
  {#if machine}<p class="sheet-foot">{machine}</p>{/if}
  {#if st?.phase === 'tuned_done'}<button class="btn" onclick={() => { moreSheet.close(); newRunDialog.showModal(); }}><Icon name="refresh" size={16} /> New run</button>{/if}
  <button class="btn done" onclick={() => moreSheet.close()}>Done</button>
</dialog>

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
  .app { display: grid; grid-template-columns: 224px minmax(0, 1fr); min-height: 100vh;
    /* the sidebar column's colour and divider run the full page height, however long the content is */
    background: linear-gradient(to right, var(--surface) 0 223px, var(--border) 223px 224px, var(--bg) 224px); }
  .sidebar { position: sticky; top: 0; height: 100vh; display: flex; flex-direction: column; gap: 18px; padding: 16px 10px 12px; background: var(--surface); border-right: 1px solid var(--border); overflow-y: auto; }
  .brand { display: flex; align-items: center; gap: 8px; padding: 0 8px; font-weight: 600; font-size: 15px; }
  nav ul { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; gap: 2px; }
  .group { margin: 16px 8px 6px; font-size: 11px; font-weight: 600; color: var(--muted); text-transform: uppercase; letter-spacing: 0.06em; }
  .opt { margin-left: 6px; font-weight: 400; text-transform: none; letter-spacing: 0; color: var(--faint); }
  /* sidebar rows behave like buttons: fill on hover, darker while pressed, filled when selected */
  .item { display: flex; align-items: center; gap: 10px; width: 100%; min-height: 32px; padding: 0 8px; border: 0; border-radius: var(--radius); background: transparent; color: var(--text); cursor: pointer; text-align: left; font-size: 14px; transition: background-color 0.1s; }
  .item:hover:not(.current):not(:disabled) { background: var(--border); }
  .item:active:not(.current):not(:disabled) { background: var(--border-strong); }
  .item.current { background: var(--accent); color: var(--on-accent); font-weight: 600; }
  .item.current .state { color: var(--on-accent); }
  /* System Settings-style icon tiles: coloured square, white glyph */
  .tile { display: inline-flex; align-items: center; justify-content: center; width: 22px; height: 22px; flex: none; border-radius: 4px; color: var(--on-tile); }
  .item:disabled .tile { opacity: 0.45; }
  .item:disabled { cursor: not-allowed; color: var(--faint); }
  .lbl { flex: 1; white-space: nowrap; }
  .state { display: inline-flex; color: var(--muted); }
  .foot { margin-top: auto; display: flex; flex-direction: column; gap: 10px; padding: 0 8px; }
  .machine { font-size: 12px; color: var(--muted); }
  /* status: green = working now, grey = installed and idle, hollow = not installed; the word says the same */
  .services { list-style: none; margin: 0; padding: 10px 0 0; border-top: 1px solid var(--border); display: grid; gap: 4px; }
  .services li { display: grid; grid-template-columns: 10px 1fr auto; gap: 8px; align-items: center; font-size: 12px; }
  .dot { width: 8px; height: 8px; border-radius: 50%; border: 1px solid var(--border-strong); }
  .dot.idle { background: var(--faint); border-color: var(--faint); }
  .dot.on { background: var(--ok); border-color: var(--ok); box-shadow: 0 0 0 0 color-mix(in srgb, var(--ok) 50%, transparent); animation: live 2s ease-out infinite; }
  @keyframes live { 70% { box-shadow: 0 0 0 5px transparent; } 100% { box-shadow: 0 0 0 0 transparent; } }
  .sname { color: var(--text); }
  .sdetail { color: var(--muted); }
  .tools { display: flex; gap: 8px; }
  .content { min-width: 0; max-width: 1080px; width: 100%; padding: 0 28px 48px; }
  .banner { display: flex; gap: 8px; align-items: center; margin-top: 16px; padding: 12px 14px; border: 1px solid var(--bad); color: var(--bad); border-radius: var(--radius); }
  main { padding-top: 20px; }
  dialog { background: var(--surface); color: var(--text); border: 1px solid var(--border-strong); border-radius: var(--radius); padding: 20px; max-width: min(520px, calc(100vw - 32px)); }
  dialog::backdrop { background: var(--overlay); }
  dialog p { margin: 10px 0 20px; }
  .end { justify-content: flex-end; }
  @media (pointer: coarse) { .item { min-height: var(--tap); } }
  /* phone header and tab bar exist only below 760px */
  .mhead, .tabbar { display: none; }
  .tile { display: inline-flex; }
  @media (max-width: 760px) {
    .app { display: block; background: var(--bg); }
    .sidebar { display: none; }
    .content { padding: 0 var(--gutter) calc(64px + var(--safe-b) + 24px); }
    main { padding-top: 12px; }
    .content :global(.pin) { position: static; }
    /* iOS navigation bar: pinned, translucent, blurring what scrolls under it */
    .mhead { display: grid; grid-template-columns: 44px 1fr 44px; align-items: center; position: sticky; top: 0; z-index: 30;
      padding: var(--safe-t) calc(8px + var(--safe-r)) 0 calc(8px + var(--safe-l)); min-height: calc(44px + var(--safe-t));
      background: color-mix(in srgb, var(--bg) 78%, transparent); -webkit-backdrop-filter: saturate(180%) blur(20px); backdrop-filter: saturate(180%) blur(20px);
      border-bottom: 0.5px solid var(--border); }
    .mbrand { display: inline-flex; justify-content: center; color: var(--muted); }
    .mtitle { text-align: center; font-weight: 600; font-size: 17px; opacity: 0; transition: opacity 0.2s; }
    .mtitle.shown { opacity: 1; }
    .mbtn { display: inline-flex; align-items: center; justify-content: center; width: 44px; height: 44px; border: 0; background: none; color: var(--accent-text); cursor: pointer; }
    /* iOS tab bar: fixed to the bottom, clear of the home indicator */
    .tabbar { display: grid; grid-template-columns: repeat(5, 1fr); position: fixed; left: 0; right: 0; bottom: 0; z-index: 30;
      padding: 4px var(--safe-r) var(--safe-b) var(--safe-l); background: color-mix(in srgb, var(--surface) 82%, transparent);
      -webkit-backdrop-filter: saturate(180%) blur(20px); backdrop-filter: saturate(180%) blur(20px); border-top: 0.5px solid var(--border); }
    .tabbar button { display: flex; flex-direction: column; align-items: center; gap: 2px; min-height: 49px; padding: 4px 0 2px; border: 0; background: none;
      color: var(--muted); font-size: 10px; font-weight: 500; cursor: pointer; }
    .tabbar button.current { color: var(--accent-text); }
    .tabbar button:active { opacity: 0.6; }
    /* More: an iOS sheet with inset grouped rows */
    .sheet { width: 100%; max-width: 100%; margin: auto 0 0; border: 0; border-radius: 12px 12px 0 0; background: var(--bg);
      padding: 8px calc(16px + var(--safe-r)) calc(16px + var(--safe-b)) calc(16px + var(--safe-l)); max-height: 88vh; overflow-y: auto; }
    .sheet[open] { display: flex; flex-direction: column; gap: 12px; animation: sheet-up 0.28s cubic-bezier(0.2, 0.8, 0.2, 1); }
    .sheet:focus { outline: none; }
    .grabber { width: 36px; height: 5px; border-radius: 3px; background: var(--border-strong); margin: 0 auto 4px; }
    .group-list { list-style: none; margin: 0; padding: 0; background: var(--surface); border-radius: 10px; overflow: hidden; }
    .group-list li + li button { border-top: 0.5px solid var(--border); }
    .group-list button { display: flex; align-items: center; gap: 12px; width: 100%; min-height: 48px; padding: 0 14px; border: 0; background: none; color: var(--text); font-size: 17px; text-align: left; cursor: pointer; }
    .group-list button:active:not(:disabled) { background: var(--surface-2); }
    .group-list button:disabled { color: var(--faint); }
    .group-list button.current .lbl { font-weight: 600; }
    .group-list .tile { width: 29px; height: 29px; border-radius: 7px; }
    .group-list .lbl { flex: 1; }
    .chev { display: inline-flex; color: var(--faint); transform: rotate(-90deg); }
    .sheet-h { margin: 8px 16px 0; font-size: 13px; color: var(--muted); text-transform: uppercase; letter-spacing: 0.02em; }
    .group-box { background: var(--surface); border-radius: 10px; padding: 12px 14px; }
    .group-box .services { border: 0; padding: 0; gap: 10px; font-size: 15px; }
    .group-box .services li { font-size: 15px; }
    .sheet-foot { margin: 0 16px; font-size: 13px; color: var(--muted); }
    .sheet .done { width: 100%; }
  }
  @keyframes sheet-up { from { transform: translateY(100%); } to { transform: translateY(0); } }
</style>
