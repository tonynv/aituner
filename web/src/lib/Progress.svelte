<script>
  let { value = 0, label = '' } = $props();
  const pct = $derived(Math.max(0, Math.min(100, Math.round(value * 100))));
</script>

<div class="wrap">
  <div class="bar" role="progressbar" aria-valuemin="0" aria-valuemax="100" aria-valuenow={pct} aria-label={label || 'Progress'}><div class="fill" style:width="{pct}%"></div></div>
  <span class="mono muted">{pct}%</span>
</div>

<style>
  .wrap { display: flex; align-items: center; gap: 12px; }
  .bar { flex: 1; height: 4px; background: var(--border); border-radius: 2px; overflow: hidden; }
  .fill { height: 100%; background: var(--text); transition: width 0.4s ease; }
</style>
