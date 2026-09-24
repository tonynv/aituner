<script>
  import Icon from './Icon.svelte';
  let { items = [] } = $props();
</script>

{#if items?.length}
  <div class="warns" role="status">
    {#each items as w}<p><Icon name="alert" size={14} /> {w}</p>{/each}
  </div>
{/if}

<style>
  .warns { display: grid; gap: 6px; padding: 12px 14px; border: 1px solid var(--warn); border-radius: var(--radius); color: var(--warn); font-size: 14px; }
  p { display: flex; gap: 8px; align-items: flex-start; margin: 0; }
  p :global(svg) { flex: none; margin-top: 3px; }
</style>
