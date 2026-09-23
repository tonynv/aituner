<script>
  import { app } from './app.svelte.js';
  let { kinds = null, max = 200 } = $props();
  const lines = $derived(app.log.filter((e) => e.event.message && (!kinds || kinds.includes(e.kind))).slice(-max));
  let el;
  $effect(() => { lines.length; if (el) el.scrollTop = el.scrollHeight; });
</script>

<div class="log mono" bind:this={el} role="log" aria-live="polite" aria-label="Progress log">
  {#each lines as l (l.seq)}
    <div class={l.event.level}>{l.event.message}</div>
  {:else}
    <div class="faint">Waiting for output</div>
  {/each}
</div>

<style>
  .log { border: 1px solid var(--border); background: var(--surface); border-radius: var(--radius); padding: 12px; max-height: 260px; overflow: auto; font-size: 12.5px; line-height: 1.6; }
  .warn { color: var(--warn); } .error { color: var(--bad); } .trial { color: var(--muted); }
</style>
