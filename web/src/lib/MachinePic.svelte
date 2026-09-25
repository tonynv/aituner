<script>
  // This Mac's own picture, from macOS (CoreTypes, served by /api/v1/machine/image). When macOS has none for the model,
  // a generic machine drawing in thin lines instead; never a guessed product photo.
  let { size = 48, name = 'This Mac' } = $props();
  let failed = $state(false);
</script>

{#if failed}
  <svg class="generic" width={size} height={size} viewBox="0 0 48 48" fill="none" stroke="currentColor" stroke-width="1.5" role="img" aria-label="{name} (generic machine)">
    <rect x="8" y="6" width="32" height="36" rx="2" />
    <path d="M8 18h32M8 30h32" />
    <path d="M13 12h.01M13 24h.01M13 36h.01" stroke-width="2.5" stroke-linecap="round" />
    <path d="M26 12h9M26 24h9M26 36h9" />
  </svg>
{:else}
  <img src="/api/v1/machine/image" alt={name} width={size} height={size} onerror={() => (failed = true)} />
{/if}

<style>
  img { object-fit: contain; display: block; flex: none; }
  .generic { color: var(--muted); flex: none; }
</style>
