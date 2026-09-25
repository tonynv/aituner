<script>
  import { onMount } from 'svelte';
  import Icon from './lib/Icon.svelte';
  import Hardware from './steps/Hardware.svelte';
  import Benchmark from './steps/Benchmark.svelte';
  import Tune from './steps/Tune.svelte';
  import Rerun from './steps/Rerun.svelte';
  import Models from './steps/Models.svelte';
  import Report from './steps/Report.svelte';
  import PinnedStats from './lib/PinnedStats.svelte';
  import { app, start, currentStep, activeStep, maxStep, act } from './lib/app.svelte.js';
  import { api } from './lib/api.js';

  const STEPS = [
    { n: 1, label: 'Hardware' },
    { n: 2, label: 'Benchmark' },
    { n: 3, label: 'Tune' },
    { n: 4, label: 'Re-run' },
    { n: 5, label: 'Models' },
  ];
  let theme = $state('system');
  let newRunDialog;
  async function confirmNewRun() {
    newRunDialog?.close();
    await act(() => api.newRun());
    app.view = null;
  }
  let systemDark = $state(true);

  onMount(() => {
    try { const t = localStorage.getItem('aituner-theme'); if (t === 'dark' || t === 'light') theme = t; } catch { /* storage blocked */ }
    const mq = matchMedia('(prefers-color-scheme: dark)');
    systemDark = mq.matches;
    const onChange = (e) => (systemDark = e.matches);
    mq.addEventListener('change', onChange);
    const stop = start();
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
  const cur = $derived(currentStep());
  const max = $derived(maxStep());
  const active = $derived(activeStep());
  const machine = $derived(st?.hardware ? `${st.hardware.model.name}, ${st.hardware.cpu.chip}` : '');
</script>

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
    <PinnedStats {st} onreport={() => (app.report = !app.report)} />
  {/if}

  {#if st && !st.supported}
    <main><div class="card stack"><h2>Not supported yet</h2><p class="muted">{st.message}</p></div></main>
  {:else if st}
    <nav aria-label="Steps">
      <ol>
        {#each STEPS as s}
          <li>
            <button class="step" class:current={!app.report && active === s.n} class:done={s.n < cur || (s.n <= max && s.n !== active && s.n < 5)} disabled={s.n > max} onclick={() => { app.report = false; app.view = s.n === cur ? null : s.n; }} aria-current={active === s.n ? 'step' : undefined}>
              <span class="num">{#if s.n < cur || (s.n <= max && s.n < 5)}<Icon name="check" size={14} />{:else if s.n > max}<Icon name="lock" size={12} />{:else}{s.n}{/if}</span>
              <span class="lbl">{s.label}</span>
            </button>
          </li>
        {/each}
      </ol>
    </nav>
    <main>
      {#if app.report}<Report />
      {:else if active === 1}<Hardware hw={st.hardware} plan={st.bench_plan} phase={st.phase} />
      {:else if active === 2}<Benchmark {st} />
      {:else if active === 3}<Tune {st} />
      {:else if active === 4}<Rerun {st} onnext={() => (app.view = 5)} />
      {:else if active === 5}<Models {st} />
      {/if}
    </main>
  {:else if !app.error}
    <main><p class="muted">Detecting hardware</p></main>
  {/if}
</div>

<dialog bind:this={newRunDialog} aria-labelledby="nr-title">
  <h3 id="nr-title">Start a new run?</h3>
  <p class="muted">This starts over from the hardware step. The current results stay saved, but the model recommendations lock again until you benchmark, tune and re-run.</p>
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
  main { padding-top: 4px; }
  dialog { background: var(--surface); color: var(--text); border: 1px solid var(--border-strong); border-radius: var(--radius); padding: 20px; max-width: min(520px, calc(100vw - 32px)); }
  dialog::backdrop { background: var(--overlay); }
  dialog p { margin: 10px 0 20px; }
  .end { justify-content: flex-end; }
  @media (max-width: 560px) { .machine { display: none; } .lbl { font-size: 14px; } }
</style>
