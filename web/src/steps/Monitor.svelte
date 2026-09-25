<script>
  import { onDestroy } from 'svelte';
  import Icon from '../lib/Icon.svelte';
  import Meter from '../lib/Meter.svelte';
  import LiveChart from '../lib/LiveChart.svelte';
  import { liveMonitor, latest, modelName, uptime } from '../lib/live.svelte.js';
  import { api } from '../lib/api.js';
  import { app, isRunning } from '../lib/app.svelte.js';
  import { fmtBytes, fmtNum } from '../lib/format.js';

  // Live performance while a model runs: what is loaded, how busy the GPU is, power, temperature and memory, with the
  // last five minutes charted. Numbers come from macmon when it is installed, else from aituner's own sampler.
  const mon = liveMonitor({ keep: 300, tools: true });
  const live = mon.live;
  onDestroy(() => mon.stop());

  const now = $derived(latest(live.samples));
  const m = $derived(live.model);
  const serving = $derived(m && m.state !== 'stopped');
  const full = $derived(live.source === 'macmon');
  const pick = (k) => live.samples.map((s) => s[k]);
  const seconds = $derived(live.samples.length > 1 ? (live.samples[live.samples.length - 1].t - live.samples[0].t) / 1000 : 0);
  let tick = $state(0); // re-render the uptime once a second
  const t = setInterval(() => tick++, 1000);
  onDestroy(() => clearInterval(t));
  const up = $derived((tick, uptime(m?.started_at)));

  let confirmTool = $state(null);
  let dialog;
  let toolErr = $state('');
  async function openTool(tool) {
    toolErr = '';
    try { await api.monitorTool(tool.id, 'open'); } catch (e) { toolErr = e.message; }
  }
  function askInstall(tool) { confirmTool = tool; dialog.showModal(); }
  async function install() {
    dialog.close(); toolErr = '';
    try { await api.monitorTool(confirmTool.id, 'install'); } catch (e) { toolErr = e.message; }
  }
  // after an install job finishes, list the tools again so the button becomes Open
  let wasRunning = false;
  $effect(() => { const r = isRunning(); if (wasRunning && !r) mon.refreshTools(); wasRunning = r; });
</script>

<div class="stack">
  <div>
    <h2>Monitor</h2>
    <p class="muted">Live performance of this Mac, updated every second. It keeps sampling while a model is running, even with this window closed.</p>
  </div>

  <section class="card model" aria-label="Model">
    <div class="row between">
      <div class="row">
        <Icon name="server" />
        {#if serving}
          <div class="who">
            <h3>{modelName(m.repo)}</h3>
            <span class="muted small">{m.repo}</span>
          </div>
        {:else}
          <div class="who"><h3>No model running</h3><span class="muted small">Start one in Setup to serve it to your editor.</span></div>
        {/if}
      </div>
      <div class="row">
        {#if serving}
          <span class="badge {m.state === 'running' ? 'ok' : 'warn'}"><Icon name={m.state === 'running' ? 'check' : 'refresh'} size={12} /> {m.state === 'starting' ? 'loading' : m.state}</span>
          {#if m.rss_bytes > 0}<span class="badge">{fmtBytes(m.rss_bytes)} resident</span>{/if}
          {#if up}<span class="badge">up {up}</span>{/if}
        {/if}
        <button class="btn small" onclick={() => (app.tab = 'setup')}><Icon name="code" size={14} /> {serving ? 'Connect an editor' : 'Setup'}</button>
      </div>
    </div>
  </section>

  {#if live.error}<p class="bad" role="alert"><Icon name="alert" size={14} /> {live.error}</p>{/if}

  {#if now}
    <section class="card tiles" aria-label="Now">
      <Meter label="GPU" value={now.gpu_pct} text="{fmtNum(now.gpu_pct, 0)}%" sub={now.gpu_mhz ? `${now.gpu_mhz} MHz` : ''} />
      <Meter label="CPU" value={now.cpu_pct} text="{fmtNum(now.cpu_pct, 0)}%" />
      <Meter label="Memory" value={now.ram_total ? now.ram_used : -1} max={now.ram_total || 1} text={fmtBytes(now.ram_used)} sub="of {fmtBytes(now.ram_total)}{now.swap_used > 0 ? `, swap ${fmtBytes(now.swap_used)}` : ''}" />
      {#if full}
        <div class="stat"><span class="l">Power</span><span class="v mono">{fmtNum(now.sys_w)}<span class="u">W system</span></span>
          <span class="muted small mono">GPU {fmtNum(now.gpu_w)} · CPU {fmtNum(now.cpu_w)} · ANE {fmtNum(now.ane_w)} W</span></div>
        <div class="stat"><span class="l">Temperature</span><span class="v mono">{now.gpu_temp_c < 0 ? 'no reading' : `${fmtNum(now.gpu_temp_c, 0)} °C`}<span class="u">GPU</span></span>
          <span class="muted small mono">CPU {now.cpu_temp_c < 0 ? 'no reading' : `${fmtNum(now.cpu_temp_c, 0)} °C`}</span></div>
      {/if}
    </section>

    <section class="card charts" aria-label="Last five minutes">
      <LiveChart label="GPU utilisation" unit="%" max={100} {seconds} series={[{ label: 'GPU', values: pick('gpu_pct'), cls: 's1' }]} />
      {#if full}
        <LiveChart label="Power" unit="W" {seconds} series={[{ label: 'GPU', values: pick('gpu_w'), cls: 's1' }, { label: 'CPU', values: pick('cpu_w'), cls: 's2' }]} />
      {/if}
      <LiveChart label="Memory used" unit="GB" max={now.ram_total / 1024 ** 3} {seconds} series={[{ label: 'Memory', values: live.samples.map((s) => (s.ram_total ? s.ram_used / 1024 ** 3 : -1)), cls: 's1' }]} />
    </section>
    <p class="faint small">Source: {full ? 'macmon (reads the same counters as powermetrics, no admin needed)' : 'aituner’s built-in sampler: GPU utilisation and memory only. Install macmon below for power, temperature and CPU.'}</p>
  {:else if !live.error}
    <p class="muted">Starting the sampler</p>
  {/if}

  <section class="card stack tight" aria-label="Terminal monitors">
    <div class="row"><Icon name="terminal" /><h3>Terminal monitors</h3></div>
    <p class="muted small">Full-screen monitors built for Apple Silicon, in their own Terminal window. None needs admin rights.</p>
    {#each live.tools as tool (tool.id)}
      <div class="tool row between">
        <div class="who">
          <span class="name">{tool.name}{#if tool.installed}<span class="badge ok"><Icon name="check" size={12} /> installed</span>{/if}</span>
          <span class="muted small">{tool.about} <a href={tool.homepage} target="_blank" rel="noopener noreferrer">Project page</a></span>
        </div>
        {#if tool.installed}
          <button class="btn small" onclick={() => openTool(tool)}><Icon name="external" size={14} /> Open</button>
        {:else}
          <button class="btn small" disabled={isRunning()} onclick={() => askInstall(tool)}><Icon name="download" size={14} /> Install</button>
        {/if}
      </div>
    {/each}
    {#if toolErr}<p class="bad small" role="alert">{toolErr}</p>{/if}
  </section>
</div>

<dialog bind:this={dialog} aria-labelledby="mt-title">
  <h3 id="mt-title">Install {confirmTool?.name}?</h3>
  <p class="muted">This runs <code>brew install {confirmTool?.id}</code>. Homebrew downloads it from its official bottles; nothing else changes.</p>
  <div class="row end">
    <button class="btn" onclick={() => dialog.close()}>Cancel</button>
    <button class="btn primary" onclick={install}>Install</button>
  </div>
</dialog>

<style>
  .between { justify-content: space-between; }
  .tight { gap: 10px; }
  .small { font-size: 13px; }
  .who { display: flex; flex-direction: column; min-width: 0; }
  .tiles { display: grid; gap: 18px; grid-template-columns: repeat(auto-fill, minmax(min(100%, 150px), 1fr)); }
  .stat { display: flex; flex-direction: column; gap: 2px; }
  .l { font-size: 12px; color: var(--muted); text-transform: uppercase; letter-spacing: 0.04em; }
  .v { font-size: 14px; font-weight: 600; } .u { font-weight: 400; color: var(--muted); font-size: 12px; margin-left: 4px; }
  .charts { display: grid; gap: 24px; grid-template-columns: repeat(auto-fill, minmax(min(100%, 300px), 1fr)); }
  .tool { padding: 10px 0; border-top: 1px solid var(--border); }
  .name { display: inline-flex; gap: 8px; align-items: center; font-weight: 600; }
  .bad { color: var(--bad); display: flex; gap: 6px; align-items: center; }
  dialog { background: var(--surface); color: var(--text); border: 1px solid var(--border-strong); border-radius: var(--radius); padding: 20px; max-width: min(520px, calc(100vw - 32px)); }
  dialog::backdrop { background: var(--overlay); }
  dialog p { margin: 10px 0 20px; }
  .end { justify-content: flex-end; }
</style>
