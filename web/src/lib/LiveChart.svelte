<script>
  import { fmtNum } from './format.js';
  // A rolling live series chart: one shared y-axis from zero, time on x (newest at the right), crosshair tooltip on hover.
  // series: [{ label, values: number[] (-1 = no reading), cls: 's1' | 's2' }]. With two series a legend names them.
  let { series = [], max = 0, unit = '', label = '', seconds = 0 } = $props();
  const W = 320, H = 110, L = 30, R = 6, T = 8, B = 16;
  const n = $derived(Math.max(0, ...series.map((s) => s.values.length)));
  const top = $derived(max > 0 ? max : niceMax(Math.max(0, ...series.flatMap((s) => s.values))));
  function niceMax(v) {
    if (v <= 0) return 1;
    const p = 10 ** Math.floor(Math.log10(v)), m = v / p;
    return (m <= 1 ? 1 : m <= 2 ? 2 : m <= 5 ? 5 : 10) * p;
  }
  const x = (i) => L + (n > 1 ? (i / (n - 1)) * (W - L - R) : 0);
  const y = (v) => T + (1 - v / top) * (H - T - B);
  // gaps (no reading) break the line rather than drawing a false zero
  const path = (vals) => vals.map((v, i) => (v < 0 ? '' : `${i && vals[i - 1] >= 0 ? 'L' : 'M'}${x(i).toFixed(1)},${y(v).toFixed(1)}`)).join(' ');
  let hover = $state(-1);
  function move(e) {
    const r = e.currentTarget.getBoundingClientRect();
    const px = ((e.clientX - r.left) / r.width) * W;
    hover = n > 1 ? Math.min(n - 1, Math.max(0, Math.round(((px - L) / (W - L - R)) * (n - 1)))) : n - 1;
  }
  const last = (vals) => { for (let i = vals.length - 1; i >= 0; i--) if (vals[i] >= 0) return vals[i]; return -1; };
  const desc = $derived(`${label}, last ${span(seconds)}. ` + series.map((s) => `${s.label}: now ${last(s.values) < 0 ? 'no reading' : fmtNum(last(s.values)) + ' ' + unit}, peak ${fmtNum(Math.max(0, ...s.values))} ${unit}`).join('; '));
  const tick = (v) => (Number.isInteger(v) ? String(v) : v < 1 ? v.toFixed(2).replace(/0$/, '') : fmtNum(v));
  const span = (sec) => (sec < 90 ? `${Math.round(sec)} s` : `${Math.round(sec / 60)} min`);
  const ago = (i) => { const s = Math.round(((n - 1 - i) / Math.max(1, n - 1)) * seconds); return s ? `${s} s ago` : 'now'; };
</script>

<figure>
  <figcaption class="row between"><span>{label}</span>
    {#if series.length > 1}<span class="legend">{#each series as s}<span class="key"><i class={s.cls}></i>{s.label}</span>{/each}</span>{/if}
  </figcaption>
  <div class="wrap">
    <svg viewBox="0 0 {W} {H}" role="img" aria-label={desc} onpointermove={move} onpointerleave={() => (hover = -1)}>
      {#each [0, 0.5, 1] as f}
        <line x1={L} x2={W - R} y1={y(top * f)} y2={y(top * f)} class="grid" />
        <text x={L - 5} y={y(top * f) + 3.5} class="tick" text-anchor="end">{tick(top * f)}</text>
      {/each}
      {#each series as s}<path d={path(s.values)} class="line {s.cls}" />{/each}
      {#if hover >= 0}
        <line x1={x(hover)} x2={x(hover)} y1={T} y2={H - B} class="cross" />
        {#each series as s}{#if s.values[hover] >= 0}<circle cx={x(hover)} cy={y(s.values[hover])} r="4" class="dot {s.cls}" />{/if}{/each}
      {/if}
      <text x={L} y={H - 3} class="tick">{seconds ? `${span(seconds)} ago` : ''}</text>
      <text x={W - R} y={H - 3} class="tick" text-anchor="end">now</text>
    </svg>
    {#if hover >= 0}
      <div class="tip mono" style:left="{(x(hover) / W) * 100}%">
        <span class="faint">{ago(hover)}</span>
        {#each series as s}<span>{#if series.length > 1}{s.label} {/if}{s.values[hover] >= 0 ? `${fmtNum(s.values[hover])} ${unit}` : 'no reading'}</span>{/each}
      </div>
    {/if}
  </div>
</figure>

<style>
  figure { margin: 0; min-width: 0; }
  figcaption { font-size: 12px; color: var(--muted); text-transform: uppercase; letter-spacing: 0.04em; margin-bottom: 4px; }
  .between { justify-content: space-between; }
  .legend { display: flex; gap: 12px; text-transform: none; letter-spacing: 0; }
  .key { display: inline-flex; align-items: center; gap: 5px; color: var(--text); }
  .key i { width: 12px; height: 2px; display: inline-block; }
  .key i.s1 { background: var(--series-1); } .key i.s2 { background: var(--series-2); }
  .wrap { position: relative; }
  svg { width: 100%; height: auto; display: block; overflow: visible; touch-action: pan-y; }
  .grid { stroke: var(--border); stroke-width: 1; }
  .tick { font: 11px var(--font-mono); fill: var(--muted); }
  .line { fill: none; stroke-width: 2; stroke-linejoin: round; stroke-linecap: round; }
  .line.s1, .dot.s1 { stroke: var(--series-1); } .line.s2, .dot.s2 { stroke: var(--series-2); }
  .dot { fill: var(--surface); stroke-width: 2; }
  .cross { stroke: var(--border-strong); stroke-width: 1; }
  .tip { position: absolute; top: -6px; transform: translate(-50%, -100%); display: flex; gap: 8px; background: var(--surface-2); border: 1px solid var(--border-strong); border-radius: var(--radius); padding: 3px 8px; font-size: 12px; white-space: nowrap; pointer-events: none; z-index: 5; }
</style>
