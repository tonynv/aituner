<script>
  import Icon from './Icon.svelte';
  import Progress from './Progress.svelte';
  import { fmtGB, fmtSpeed } from './format.js';
  import { api } from './api.js';

  // The queue sits at the top of the Downloads page: what is downloading, what waits (in order), and what needs
  // attention. Finished downloads are listed compactly; the Setup step picks up from there.
  let { items, onchange } = $props();
  let err = $state('');

  const order = { running: 0, queued: 1, error: 2, cancelled: 3, done: 4 };
  const rows = $derived(
    Object.values(items)
      .filter((i) => i.state !== 'done')
      .sort((a, b) => order[a.state] - order[b.state] || (a.position ?? 0) - (b.position ?? 0) || a.repo.localeCompare(b.repo)),
  );
  const done = $derived(Object.values(items).filter((i) => i.state === 'done').sort((a, b) => a.repo.localeCompare(b.repo)));
  const frac = (i) => (i.bytes_total > 0 ? i.bytes_done / i.bytes_total : 0);

  async function remove(repo) { err = ''; try { await api.cancelDownload(repo); await onchange?.(); } catch (e) { err = e.message; } }
  async function again(repo) { err = ''; try { await api.startDownload(repo); await onchange?.(); } catch (e) { err = e.message; } }
</script>

<section class="card stack tight" aria-label="Download queue">
  <div class="row between">
    <div class="row"><Icon name="download" /><h3>Download queue</h3></div>
    <span class="faint small">{rows.filter((r) => r.state === 'running' || r.state === 'queued').length} active · {done.length} downloaded</span>
  </div>

  {#if !rows.length && !done.length}
    <p class="muted">Nothing queued yet. Pick models below with <strong>Add to queue</strong>; they download one at a time, in the order you add them.</p>
  {/if}

  {#each rows as i (i.repo)}
    <div class="item">
      <div class="row between">
        <span class="mono name">{i.repo}</span>
        <div class="row">
          {#if i.state === 'running'}<span class="badge warn">downloading</span>
          {:else if i.state === 'queued'}<span class="badge">queued #{i.position}</span>
          {:else if i.state === 'error'}<span class="badge bad">failed</span>
          {:else}<span class="badge">stopped</span>{/if}
          {#if i.state === 'running' || i.state === 'queued'}
            <button class="btn small" onclick={() => remove(i.repo)}><Icon name="x" size={14} /> {i.state === 'queued' ? 'Remove' : 'Cancel'}</button>
          {:else}
            <button class="btn small" onclick={() => again(i.repo)}><Icon name="refresh" size={14} /> {i.state === 'error' ? 'Retry' : 'Resume'}</button>
          {/if}
        </div>
      </div>
      {#if i.state === 'running'}
        <Progress value={frac(i)} label="Download progress for {i.repo}" />
        <span class="muted small">{fmtGB(i.bytes_done / 1024 ** 3)} of {fmtGB(i.bytes_total / 1024 ** 3)}{i.speed_bps > 0 ? ` at ${fmtSpeed(i.speed_bps)}` : ''}</span>
      {/if}
      {#if i.state === 'error' && i.error}<p class="bad small" role="alert">{i.error}</p>{/if}
      {#if i.state === 'cancelled'}<span class="faint small">Partial files are kept and resumed.</span>{/if}
    </div>
  {/each}

  {#if done.length}
    <div class="ready">
      <div class="faint small">Downloaded</div>
      {#each done as i (i.repo)}
        <div class="row between"><span class="mono name">{i.repo}</span><span class="badge ok"><Icon name="check" size={12} /> ready</span></div>
      {/each}
    </div>
  {/if}
  {#if err}<p class="bad small" role="alert">{err}</p>{/if}
</section>

<style>
  .between { justify-content: space-between; }
  .item { display: grid; gap: 8px; padding: 12px 0; border-top: 1px solid var(--border); }
  .ready { display: grid; gap: 8px; padding-top: 12px; border-top: 1px solid var(--border); }
  .name { min-width: 0; word-break: break-all; font-size: 13px; }
  .small { font-size: 13px; }
  .bad { color: var(--bad); margin: 0; }
  .badge.bad { border-color: var(--bad); color: var(--bad); }
</style>
