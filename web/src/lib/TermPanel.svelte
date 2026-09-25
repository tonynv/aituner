<script>
  import { onMount } from 'svelte';
  // Full-window terminal view shared by the start-up scan and Bootstrap: header with a running clock, a scrolling log,
  // and a footer. Faint scanlines and one sweep while running; the app's own palette, no neon.
  let { title, running = true, children, footer, log = $bindable() } = $props();
  const t0 = performance.now();
  let elapsed = $state(0);
  onMount(() => {
    const id = setInterval(() => { if (running) elapsed = performance.now() - t0; }, 50);
    return () => clearInterval(id);
  });
</script>

<div class="term" class:finished={!running} role="dialog" aria-modal="true" aria-labelledby="term-title">
  <div class="sweep" aria-hidden="true"></div>
  <header>
    <span id="term-title">aituner <span class="dim">::</span> {title}</span>
    <span class="dim">T+{(elapsed / 1000).toFixed(3)}s</span>
  </header>
  <div class="log" bind:this={log} aria-live="polite">{@render children()}</div>
  <footer>{@render footer()}</footer>
</div>

<style>
  :global(body:has(.term)) { overflow: hidden; }
  .term { position: fixed; inset: 0; z-index: 100; display: flex; flex-direction: column; background: var(--bg); color: var(--text);
    font-family: var(--font-mono); font-size: 13px; line-height: 1.6; padding: calc(20px + var(--safe-t)) calc(24px + var(--safe-r)) calc(16px + var(--safe-b)) calc(24px + var(--safe-l)); overflow: hidden; }
  .term::before { content: ''; position: absolute; inset: 0; pointer-events: none;
    background: repeating-linear-gradient(to bottom, transparent 0 2px, color-mix(in srgb, var(--text) 4%, transparent) 2px 3px); }
  .sweep { position: absolute; left: 0; right: 0; top: 0; height: 120px; pointer-events: none;
    background: linear-gradient(to bottom, transparent, color-mix(in srgb, var(--text) 6%, transparent) 90%, color-mix(in srgb, var(--text) 18%, transparent));
    animation: sweep 2.4s linear infinite; }
  .finished .sweep { display: none; }
  @keyframes sweep { from { transform: translateY(-120px); } to { transform: translateY(100vh); } }
  header, footer { position: relative; display: flex; align-items: center; justify-content: space-between; gap: 12px; }
  header { padding-bottom: 12px; border-bottom: 1px solid var(--border); text-transform: uppercase; letter-spacing: 0.08em; font-size: 12px; }
  footer { padding-top: 12px; border-top: 1px solid var(--border); flex-wrap: wrap; }
  .log { position: relative; flex: 1; overflow-y: auto; padding: 14px 0; scrollbar-width: none; }
  .log::-webkit-scrollbar { display: none; }
  /* shared line styles for the content rendered inside */
  .term :global(.dim) { color: var(--muted); }
  .term :global(.ok) { color: var(--ok); }
  .term :global(.bad) { color: var(--bad); }
  .term :global(.warnc) { color: var(--warn); }
  .term :global(.line) { display: grid; grid-template-columns: max-content minmax(0, 1fr) auto 36px; gap: 12px; align-items: baseline; }
  .term :global(.line.note) { grid-template-columns: max-content minmax(0, 1fr); }
  .term :global(.cmd) { white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
  .term :global(.res) { white-space: nowrap; text-align: right; }
  .term :global(.type) { animation: type 0.22s steps(18, end) both; }
  @keyframes -global-type { from { clip-path: inset(0 100% 0 0); } to { clip-path: inset(0 0 0 0); } }
  .term :global(.cursor) { display: inline-block; width: 8px; height: 14px; margin-left: 4px; vertical-align: -2px; background: var(--text); animation: blink 1s steps(1) infinite; }
  @keyframes -global-blink { 50% { opacity: 0; } }
  .term :global(.kbd) { margin-left: 6px; padding: 0 4px; border: 1px solid currentColor; border-radius: 2px; font-size: 10px; opacity: 0.7; text-transform: uppercase; }
  @media (max-width: 560px) {
    .term :global(.line) { grid-template-columns: minmax(0, 1fr) 36px; }
    .term :global(.line .t), .term :global(.line .res) { display: none; }
    .term :global(.line.note) { grid-template-columns: minmax(0, 1fr); }
  }
  @media (prefers-reduced-motion: reduce) { .sweep { display: none; } }
</style>
