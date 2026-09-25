<script>
  // Accessible table alternative for any chart: same numbers, plain rows.
  let { columns = [], rows = [], caption = '' } = $props();
</script>

<div class="scroll-x">
  <table>
    {#if caption}<caption class="sr">{caption}</caption>{/if}
    <thead><tr>{#each columns as c}<th class={c.num ? 'num' : ''}>{c.label}</th>{/each}</tr></thead>
    <tbody>{#each rows as r}<tr>{#each columns as c}<td class={c.num ? 'num' : ''}>{r[c.key] ?? ''}</td>{/each}</tr>{/each}</tbody>
  </table>
</div>

<style>
  .sr { position: absolute; width: 1px; height: 1px; overflow: hidden; clip-path: inset(50%); }
  .scroll-x > :global(table) { min-width: 480px; }
</style>
