<script>
  import Icon from './Icon.svelte';
  let { text, label = 'Copy command' } = $props();
  let done = $state(false);
  async function copy() {
    try { await navigator.clipboard.writeText(text); done = true; setTimeout(() => (done = false), 1500); } catch { /* clipboard unavailable */ }
  }
</script>

<div class="cmd">
  <pre class="mono">{text}</pre>
  <button class="btn small" onclick={copy} aria-label={label}><Icon name={done ? 'check' : 'copy'} size={14} /></button>
</div>

<style>
  .cmd { display: flex; gap: 8px; align-items: stretch; min-width: 0; }
  pre { flex: 1; margin: 0; padding: 8px 10px; background: var(--bg); border: 1px solid var(--border); border-radius: var(--radius); white-space: pre-wrap; overflow-wrap: anywhere; min-width: 0; }
</style>
