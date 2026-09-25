<script>
  import { fmtNum } from './format.js';
  // Paired horizontal bars, baseline vs tuned, one scale per row (rows have different units). Bars are 8px thin with
  // 4px rounded data-ends, a 2px gap between the pair, and values printed at the bar end.
  let { rows = [], aName = 'Baseline', bName = 'After tuning' } = $props();
  let tip = $state(null);
  const pct = (v, max) => (max > 0 ? Math.max(1, (v / max) * 100) : 0);
  const sign = (v) => (v > 0 ? '+' : '') + v.toFixed(1) + '%';
</script>

<div class="cb">
  <div class="legend" aria-hidden="true">
    <span class="k"><i class="sw a"></i>{aName}</span><span class="k"><i class="sw b"></i>{bName}</span>
  </div>
  {#each rows as r (r.key)}
    {@const max = Math.max(r.before.median, r.after.median)}
    <div class="row" role="group" aria-label="{r.label}: {aName} {fmtNum(r.before.median)} {r.unit}, {bName} {fmtNum(r.after.median)} {r.unit}, {sign(r.delta_pct)}, {r.verdict.replace('_', ' ')}">
      <div class="name">{r.label}</div>
      <div class="bars">
        <div class="bar-line"><span class="bar a" style:width="{pct(r.before.median, max) * 0.7}%"
          onpointerenter={() => (tip = { r, which: 'a' })} onpointerleave={() => (tip = null)} role="presentation"></span><span class="val mono">{fmtNum(r.before.median)}</span></div>
        <div class="bar-line"><span class="bar b" style:width="{pct(r.after.median, max) * 0.7}%"
          onpointerenter={() => (tip = { r, which: 'b' })} onpointerleave={() => (tip = null)} role="presentation"></span><span class="val mono">{fmtNum(r.after.median)} <span class="muted">{r.unit}</span></span></div>
      </div>
      <div class="delta">
        <span class="mono">{sign(r.delta_pct)}</span>
        {#if r.verdict === 'faster'}<span class="badge ok">faster</span>
        {:else if r.verdict === 'slower'}<span class="badge bad">slower</span>
        {:else if r.verdict === 'info'}<span class="badge">info</span>
        {:else}<span class="badge">within noise ±{r.noise_pct.toFixed(1)}%</span>{/if}
      </div>
    </div>
  {/each}
  {#if tip}
    <div class="tip mono" role="status">{tip.r.label}: {tip.which === 'a' ? aName : bName} {fmtNum(tip.which === 'a' ? tip.r.before.median : tip.r.after.median)} {tip.r.unit}
      (spread ±{(tip.which === 'a' ? tip.r.before.spread_pct : tip.r.after.spread_pct).toFixed(1)}%, {(tip.which === 'a' ? tip.r.before.n : tip.r.after.n)} trials)</div>
  {/if}
</div>

<style>
  .cb { display: grid; gap: 14px; position: relative; }
  .legend { display: flex; gap: 18px; font-size: 13px; color: var(--muted); }
  .k { display: inline-flex; align-items: center; gap: 8px; } .sw { width: 12px; height: 8px; border-radius: 2px; display: inline-block; }
  .sw.a, .bar.a { background: var(--series-1); } .sw.b, .bar.b { background: var(--series-2); }
  .row { display: grid; grid-template-columns: minmax(150px, 0.9fr) minmax(180px, 2fr) minmax(150px, 0.9fr); gap: 16px; align-items: center; }
  .name { font-size: 14px; }
  .bars { display: grid; gap: 2px; }
  .bar-line { display: flex; align-items: center; gap: 8px; min-height: 16px; }
  .bar { display: block; height: 8px; border-radius: 0 4px 4px 0; min-width: 2px; flex: none; }
  .val { font-size: 12px; white-space: nowrap; }
  .bar-line { min-width: 0; }
  .delta { display: flex; gap: 8px; align-items: center; flex-wrap: wrap; font-size: 13px; }
  .tip { position: sticky; bottom: 8px; justify-self: start; background: var(--surface-2); border: 1px solid var(--border-strong); border-radius: var(--radius); padding: 6px 10px; font-size: 12px; }
  @media (max-width: 700px) { .row { grid-template-columns: 1fr; gap: 6px; } }
</style>
