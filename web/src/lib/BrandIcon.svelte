<script>
  import Icon from './Icon.svelte';
  // Official marks for the editor integrations: VS Code and Claude from the apps installed on this Mac (read at run
  // time), Neovim, Vim and tmux from their projects (web/public/brands, licences in NOTICE.md). Anything that cannot
  // load falls back to the line icon; the generic OpenAI-compatible card has no brand.
  let { id, size = 28 } = $props();
  const SRC = {
    claude: ['/api/v1/appicon/claude'],
    vscode: ['/api/v1/appicon/vscode'],
    neovim: ['/brands/neovim.svg'],
    'vim-tmux': ['/brands/vim.svg', '/brands/tmux.png'],
  };
  const FALLBACK = { claude: 'terminal', vscode: 'code', neovim: 'terminal', 'vim-tmux': 'terminal', openai: 'server' };
  let failed = $state(false);
  const srcs = $derived(SRC[id] ?? []);
</script>

<span class="brand" aria-hidden="true">
  {#if srcs.length && !failed}
    {#each srcs as s (s)}<img src={s} alt="" width={size} height={size} onerror={() => (failed = true)} />{/each}
  {:else}
    <Icon name={FALLBACK[id] ?? 'terminal'} size={Math.round(size * 0.8)} />
  {/if}
</span>

<style>
  .brand { display: inline-flex; align-items: center; gap: 4px; flex: none; }
  img { object-fit: contain; display: block; }
</style>
