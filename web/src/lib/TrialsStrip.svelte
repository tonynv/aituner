<script>
  import { fmtNum } from './format.js';
  // Every trial of one measurement as dots on an axis anchored at the median, so the picture is honest: a tight
  // cluster means low variation, a wide scatter means high variation. The axis spans the median +-10% (wider only if
  // a trial falls outside it). Built from HTML positioned by percentage so dots stay round at any width.
  let { values = [], median = 0, unit = '', label = '' } = $props();
  const dev = $derived(median ? Math.max(0, ...values.map((v) => Math.abs(v - median) / Math.abs(median))) : 0);
  const half = $derived(Math.max(0.1, dev * 1.15)); // fraction of the median
  const pos = (v) => (median ? 50 + ((v - median) / (Math.abs(median) * half)) * 50 : 50);
  let tip = $state(null);
  const desc = $derived(`${label}: ${values.length} trials (${values.map((v) => fmtNum(v)).join(', ')} ${unit}), median ${fmtNum(median)} ${unit}, largest deviation ${(dev * 100).toFixed(1)} percent`);
</script>

<div class="strip" role="img" aria-label={desc}>
  <div class="axis"></div>
  <span class="end l mono">−{(half * 100).toFixed(0)}%</span><span class="end r mono">+{(half * 100).toFixed(0)}%</span>
  <div class="median" style:left="50%"></div>
  {#each values as v, i}
    <span class="dot" style:left="{Math.min(100, Math.max(0, pos(v)))}%" role="presentation"
      onpointerenter={() => (tip = { v, i, left: Math.min(100, Math.max(0, pos(v))) })} onpointerleave={() => (tip = null)}></span>
  {/each}
  {#if tip}<div class="tip mono" style:left="{tip.left}%">trial {tip.i + 1}: {fmtNum(tip.v)} {unit}</div>{/if}
</div>

<style>
  .strip { position: relative; height: 32px; min-width: 140px; margin: 0 6px; }
  .axis { position: absolute; left: 0; right: 0; top: 9px; height: 1px; background: var(--border-strong); }
  .median { position: absolute; top: 2px; width: 2px; height: 16px; background: var(--text); transform: translateX(-1px); }
  .dot { position: absolute; top: 3px; width: 12px; height: 12px; margin-left: -6px; border-radius: 50%; background: var(--series-1); box-shadow: 0 0 0 2px var(--surface); cursor: default; }
  .dot:hover { box-shadow: 0 0 0 2px var(--text); }
  .end { position: absolute; top: 18px; font-size: 11px; color: var(--muted); } .end.l { left: 0; } .end.r { right: 0; }
  .tip { position: absolute; bottom: 100%; transform: translateX(-50%); background: var(--surface-2); border: 1px solid var(--border-strong); border-radius: var(--radius); padding: 3px 8px; font-size: 12px; white-space: nowrap; pointer-events: none; z-index: 5; }
</style>
