<script>
  import { onDestroy } from 'svelte';
  import Icon from './lib/Icon.svelte';
  import Meter from './lib/Meter.svelte';
  import LiveChart from './lib/LiveChart.svelte';
  import { liveMonitor, latest, modelName } from './lib/live.svelte.js';
  import { fmtBytes, fmtNum } from './lib/format.js';

  // The panel under the menu bar icon (?view=menubar, shown by the macOS app): the live model and GPU at a glance.
  // It follows the system appearance, like the menu bar itself.
  const mon = liveMonitor({ keep: 60 });
  const live = mon.live;
  onDestroy(() => mon.stop());
  const now = $derived(latest(live.samples));
  const m = $derived(live.model);
  const serving = $derived(m && m.state !== 'stopped');
  const seconds = $derived(live.samples.length > 1 ? (live.samples[live.samples.length - 1].t - live.samples[0].t) / 1000 : 0);

  // The app injects window.webkit.messageHandlers.aituner; in a plain browser these controls are hidden.
  const native = typeof window !== 'undefined' && window.webkit?.messageHandlers?.aituner;
  const send = (msg) => native?.postMessage(msg);
  let panel;
  $effect(() => {
    if (!native || !panel) return;
    const ro = new ResizeObserver(() => send({ height: Math.ceil(panel.getBoundingClientRect().height) }));
    ro.observe(panel);
    return () => ro.disconnect();
  });
</script>

<div class="panel" bind:this={panel}>
  <div class="model">
    <span class="dot" class:on={m?.state === 'running'} class:wait={serving && m?.state !== 'running'} aria-hidden="true"></span>
    <div class="who">
      {#if serving}
        <span class="name">{modelName(m.repo)}</span>
        <span class="muted small">{m.state === 'running' ? 'Live' : m.state === 'starting' ? 'Loading' : m.state}{#if m.rss_bytes > 0} · {fmtBytes(m.rss_bytes)} resident{/if}</span>
      {:else}
        <span class="name">No model running</span>
        <span class="muted small">Start one in aituner, Setup</span>
      {/if}
    </div>
  </div>

  {#if live.error}
    <p class="bad small" role="alert"><Icon name="alert" size={14} /> {live.error}</p>
  {:else if now}
    <Meter label="GPU" value={now.gpu_pct} text="{fmtNum(now.gpu_pct, 0)}%" sub={now.gpu_mhz ? `${now.gpu_mhz} MHz` : ''} />
    <LiveChart label="GPU, last minute" unit="%" max={100} {seconds} series={[{ label: 'GPU', values: live.samples.map((s) => s.gpu_pct), cls: 's1' }]} />
    <Meter label="Memory" value={now.ram_total ? now.ram_used : -1} max={now.ram_total || 1} text={fmtBytes(now.ram_used)} sub="of {fmtBytes(now.ram_total)}" />
    {#if live.source === 'macmon'}
      <div class="facts mono small">
        <span><span class="muted">Power</span> {fmtNum(now.sys_w)} W</span>
        <span><span class="muted">GPU</span> {fmtNum(now.gpu_w)} W</span>
        <span><span class="muted">GPU temp</span> {now.gpu_temp_c < 0 ? '–' : `${fmtNum(now.gpu_temp_c, 0)} °C`}</span>
      </div>
    {/if}
  {:else}
    <p class="muted small">Starting the sampler</p>
  {/if}

  {#if native}
    <div class="actions">
      <button class="btn small primary" onclick={() => send('open')}><Icon name="monitor" size={14} /> Open aituner</button>
      <button class="btn small" onclick={() => send('quit')}><Icon name="power" size={14} /> Quit</button>
    </div>
  {/if}
</div>

<style>
  :global(body) { background: transparent; }
  .panel { display: flex; flex-direction: column; gap: 14px; padding: 14px 16px 16px; width: 100%; }
  .model { display: flex; gap: 10px; align-items: center; }
  .who { display: flex; flex-direction: column; min-width: 0; }
  .name { font-weight: 600; font-size: 14px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .small { font-size: 12px; }
  .dot { width: 8px; height: 8px; flex: none; border-radius: 2px; border: 1px solid var(--border-strong); }
  .dot.on { background: var(--ok); border-color: var(--ok); }
  .dot.wait { background: var(--warn); border-color: var(--warn); }
  .facts { display: flex; justify-content: space-between; gap: 8px; flex-wrap: wrap; }
  .actions { display: flex; gap: 8px; border-top: 1px solid var(--border); padding-top: 12px; }
  .actions .btn { flex: 1; }
  .bad { color: var(--bad); display: flex; gap: 6px; align-items: center; margin: 0; }
</style>
