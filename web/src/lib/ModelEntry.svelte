<script>
  import KvMeter from './KvMeter.svelte';
  import DownloadControl from './DownloadControl.svelte';
  import Copyable from './Copyable.svelte';
  import Icon from './Icon.svelte';

  // A normalized model (candidate or variant). layout: 'grid' card or 'list' row.
  let { m, layout = 'grid', dl, anyActive, blocked, onchange, children } = $props();
</script>

<article class="entry {layout}">
  <div class="info stack tight">
    <div class="row between top">
      <div class="who">
        <h3 class:mono={m.mono}>{m.title}</h3>
        {#if m.subtitle}<div class="faint">{m.subtitle}</div>{/if}
      </div>
      {#if m.grade}<span class="badge {m.gradeClass}" title="canirun.ai fit grade">grade {m.grade}</span>{/if}
    </div>
    <div class="row badges">
      {#each m.badges as b}<span class="badge {b.cls ?? ''}">{b.text}</span>{/each}
    </div>
    {#each m.notes ?? [] as n}<p class="muted note">{n}</p>{/each}
  </div>

  <div class="meter"><KvMeter kv={m.kv} /></div>

  <div class="actions stack tight">
    {#if m.repo}
      <DownloadControl repo={m.repo} status={dl?.[m.repo]} {anyActive} {blocked} {onchange} />
    {/if}
    {#if m.run && dl?.[m.repo]?.state !== 'done'}
      <details class="cmdwrap">
        <summary><Icon name="terminal" size={14} /> Run without downloading first</summary>
        <Copyable text={m.run} />
      </details>
    {/if}
    {#if m.sourceUrl}<a class="faint note" href={m.sourceUrl} target="_blank" rel="noopener noreferrer">Model page</a>{/if}
  </div>

  {#if children}<div class="extra">{@render children()}</div>{/if}
</article>

<style>
  .entry { display: grid; gap: 14px; min-width: 0; }
  .entry.grid { border: 1px solid var(--border); background: var(--surface); border-radius: var(--radius); padding: 16px; grid-template-columns: 1fr; align-content: start; }
  /* list: one row per model, three aligned columns; falls back to a stacked row on narrow screens */
  .entry.list { border-top: 1px solid var(--border); padding: 16px 0; grid-template-columns: minmax(200px, 1.1fr) minmax(220px, 1.2fr) minmax(220px, 1fr); align-items: start; gap: 20px; }
  .entry.list .extra { grid-column: 1 / -1; }
  @media (max-width: 900px) { .entry.list { grid-template-columns: 1fr; gap: 14px; } }
  .tight { gap: 8px; } .stack { display: flex; flex-direction: column; }
  .actions { align-items: flex-start; }
  .actions :global(.dl) { width: 100%; }
  .actions :global(.dl > .btn) { justify-self: start; }
  .top { align-items: flex-start; justify-content: space-between; flex-wrap: nowrap; }
  .who { min-width: 0; } .who h3 { word-break: break-word; }
  .badges { gap: 6px; }
  .note { font-size: 13px; margin: 0; }
  .cmdwrap summary { min-height: 36px; font-size: 13px; color: var(--muted); gap: 6px; }
  .between { justify-content: space-between; }
</style>
