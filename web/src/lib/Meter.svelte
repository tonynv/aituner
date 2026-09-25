<script>
  // A labelled horizontal meter: value against max, the reading in text beside it (never colour alone).
  let { label, value = -1, max = 100, text = '', sub = '' } = $props();
  const pct = $derived(value < 0 || max <= 0 ? 0 : Math.min(100, (value / max) * 100));
</script>

<div class="meter">
  <div class="head"><span class="l">{label}</span><span class="v mono">{value < 0 ? 'no reading' : text}{#if sub}<span class="sub">{sub}</span>{/if}</span></div>
  <div class="track" role="meter" aria-label={label} aria-valuemin="0" aria-valuemax={max} aria-valuenow={value < 0 ? undefined : value} aria-valuetext={value < 0 ? 'no reading' : text}>
    <div class="fill" style:width="{pct}%"></div>
  </div>
</div>

<style>
  .meter { display: flex; flex-direction: column; gap: 6px; min-width: 0; }
  .head { display: flex; justify-content: space-between; align-items: baseline; gap: 8px; }
  .l { font-size: 12px; color: var(--muted); text-transform: uppercase; letter-spacing: 0.04em; white-space: nowrap; }
  .v { font-size: 14px; font-weight: 600; white-space: nowrap; }
  .sub { font-weight: 400; color: var(--muted); font-size: 12px; margin-left: 6px; }
  .track { height: 6px; background: var(--surface-2); border: 1px solid var(--border); border-radius: var(--radius); overflow: hidden; }
  .fill { height: 100%; background: var(--series-1); transition: width 0.4s ease; }
</style>
