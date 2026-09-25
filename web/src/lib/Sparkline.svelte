<script>
  import { fmtNum } from './format.js';
  // A per-second series (sustained GPU throughput). Starts at zero so a small dip is not exaggerated; the first and
  // last thirds are shaded and labelled so the drift the report computes is visible.
  let { values = [], unit = '', label = '' } = $props();
  const W = 300, H = 90, L = 4, R = 4, T = 10, B = 14;
  const max = $derived(Math.max(...values, 0.0001) * 1.1);
  const x = (i) => L + (values.length > 1 ? (i / (values.length - 1)) * (W - L - R) : 0);
  const y = (v) => T + (1 - v / max) * (H - T - B);
  const third = $derived(Math.floor(values.length / 3));
  const mean = (a) => (a.length ? a.reduce((s, v) => s + v, 0) / a.length : 0);
  const first = $derived(mean(values.slice(0, third)));
  const last = $derived(mean(values.slice(values.length - third)));
  const path = $derived(values.map((v, i) => `${i ? 'L' : 'M'}${x(i).toFixed(1)},${y(v).toFixed(1)}`).join(' '));
  let tip = $state(null);
  const desc = $derived(`${label}: ${values.length} one-second samples, first third averages ${fmtNum(first)} ${unit}, last third ${fmtNum(last)} ${unit}`);
</script>

<div class="wrap">
  <svg viewBox="0 0 {W} {H}" role="img" aria-label={desc}>
    {#if third > 0}
      <rect x={x(0)} y={T} width={x(third - 1) - x(0)} height={H - T - B} class="band" />
      <rect x={x(values.length - third)} y={T} width={x(values.length - 1) - x(values.length - third)} height={H - T - B} class="band" />
    {/if}
    <line x1={L} x2={W - R} y1={H - B} y2={H - B} class="axis" />
    <path d={path} class="line" />
    {#each values as v, i}
      <circle cx={x(i)} cy={y(v)} r="6" class="hit" role="presentation"
        onpointerenter={() => (tip = { v, i, left: (x(i) / W) * 100 })} onpointerleave={() => (tip = null)} />
    {/each}
    <text x={x(0)} y={H - 2} class="lbl">0 s</text>
    <text x={W - R} y={H - 2} class="lbl" text-anchor="end">{values.length} s</text>
  </svg>
  {#if tip}<div class="tip mono" style:left="{tip.left}%">s {tip.i + 1}: {fmtNum(tip.v)} {unit}</div>{/if}
  <div class="legend"><span>first third <strong>{fmtNum(first)}</strong></span><span>last third <strong>{fmtNum(last)}</strong> {unit}</span></div>
</div>

<style>
  .wrap { position: relative; min-width: 160px; }
  svg { width: 100%; height: auto; display: block; overflow: visible; }
  .band { fill: var(--border); opacity: 0.5; }
  .axis { stroke: var(--border-strong); stroke-width: 1; }
  .line { fill: none; stroke: var(--series-1); stroke-width: 2; stroke-linejoin: round; stroke-linecap: round; }
  .hit { fill: transparent; }
  .hit:hover { fill: var(--series-1); stroke: var(--surface); stroke-width: 2; r: 4; }
  .lbl { font: 10px var(--font-mono); fill: var(--muted); }
  .legend { display: flex; justify-content: space-between; font-size: 12px; color: var(--muted); margin-top: 2px; }
  .legend strong { color: var(--text); font-weight: 600; }
  .tip { position: absolute; top: 0; transform: translateX(-50%); background: var(--surface-2); border: 1px solid var(--border-strong); border-radius: var(--radius); padding: 3px 8px; font-size: 12px; white-space: nowrap; pointer-events: none; z-index: 5; }
</style>
