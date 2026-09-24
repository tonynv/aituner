<script>
  import { fmtGB, fmtTokens } from './format.js';
  // How the memory budget is used once this model is loaded: weights, runtime overhead, and the room left for KV cache.
  let { kv } = $props();

  const total = $derived(kv ? kv.weights_gb + kv.overhead_gb + kv.room_gb : 0);
  const pct = (v) => (total > 0 ? Math.max(0, (v / total) * 100) : 0);
  // KV bytes the model would use at its own maximum context (fp16), so spare room is shown as spare
  const needGB = $derived(kv && kv.known && kv.max_context ? (kv.bytes_per_token_fp16 * kv.max_context + kv.fixed_bytes) / 1024 ** 3 : Infinity);
  const usedGB = $derived(kv ? Math.min(kv.room_gb, needGB) : 0);
  const spareGB = $derived(kv ? Math.max(0, kv.room_gb - usedGB) : 0);
  const capped = $derived(kv && kv.known && kv.max_context > 0 && kv.tokens_fp16 >= kv.max_context);

  const label = $derived.by(() => {
    if (!kv) return '';
    const base = `Memory budget ${fmtGB(kv.budget_gb)}: weights ${fmtGB(kv.weights_gb)}, runtime ${fmtGB(kv.overhead_gb)}, KV cache room ${fmtGB(kv.room_gb)}.`;
    if (!kv.known) return `${base} KV cache size is not computed for this model: ${kv.reason}.`;
    if (capped) return `${base} The model's full ${fmtTokens(kv.max_context)}-token context fits with an fp16 KV cache.`;
    return `${base} About ${fmtTokens(kv.tokens_fp16)} tokens of context fit with an fp16 KV cache, ${fmtTokens(kv.tokens_8bit)} with an 8-bit KV cache.`;
  });
</script>

{#if kv}
  <div class="kv">
    <div class="bar" role="img" aria-label={label}>
      <span class="seg weights" style:width="{pct(kv.weights_gb)}%"></span>
      <span class="seg runtime" style:width="{pct(kv.overhead_gb)}%"></span>
      <span class="seg used" style:width="{pct(usedGB)}%"></span>
      <span class="seg spare" style:width="{pct(spareGB)}%"></span>
    </div>
    <div class="legend">
      <span class="k"><i class="sw weights"></i>weights {fmtGB(kv.weights_gb)}</span>
      <span class="k"><i class="sw used"></i>KV room {fmtGB(kv.room_gb)}</span>
    </div>
    <div class="ctx">
      {#if !kv.known}
        <span class="faint">KV size unknown for this architecture</span>
      {:else if kv.room_gb <= 0 || kv.tokens_fp16 === 0}
        <span class="warn">No room left for KV cache</span>
      {:else if capped}
        <strong>{fmtTokens(kv.max_context)}</strong> tokens: full context fits
      {:else}
        <strong>~{fmtTokens(kv.tokens_fp16)}</strong> tokens <span class="muted">({fmtTokens(kv.tokens_8bit)} with 8-bit KV)</span>
      {/if}
    </div>
  </div>
{/if}

<style>
  .kv { display: grid; gap: 6px; min-width: 0; }
  .bar { display: flex; height: 10px; border: 1px solid var(--border-strong); border-radius: var(--radius); overflow: hidden; background: var(--bg); }
  .seg { height: 100%; }
  .weights { background: var(--text); }
  .runtime { background: var(--faint); }
  .used { background: var(--ok); }
  .spare { background: repeating-linear-gradient(135deg, var(--border-strong) 0 3px, transparent 3px 6px); }
  .legend { display: flex; gap: 14px; flex-wrap: wrap; font-size: 12px; color: var(--muted); }
  .k { display: inline-flex; align-items: center; gap: 6px; }
  .sw { width: 10px; height: 10px; border-radius: 1px; display: inline-block; }
  .sw.weights { background: var(--text); } .sw.used { background: var(--ok); }
  .ctx { font-size: 13px; }
  .warn { color: var(--warn); }
</style>
