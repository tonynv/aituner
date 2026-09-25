<script>
  import { tick } from 'svelte';
  import TermPanel from './TermPanel.svelte';
  import { api } from './api.js';
  import { app } from './app.svelte.js';

  // Bootstrap's live output in the terminal view: the job's own events, from the moment it started.
  let { fromSeq = 0, since = 0, onclose } = $props(); // since: ms timestamp taken just before the job was started
  let log = $state();
  const lines = $derived(app.log.filter((e) => e.kind === 'bootstrap' && e.seq > fromSeq && e.event.message));
  const job = $derived(app.state?.job);
  // this run's job: a bootstrap job that started at or after `since` (it may finish before a poll ever sees it running)
  const ours = $derived(job?.kind === 'bootstrap' && job.started >= since - 2000);
  const finished = $derived(ours && !job.running);
  const failed = $derived(finished && !!job?.error);
  $effect(() => { lines.length; tick().then(() => log?.scrollTo({ top: log.scrollHeight })); });
  const t = (ms) => new Date(ms).toLocaleTimeString([], { hour12: false });
  async function cancel() { try { await api.cancel(); } catch { /* the job may just have finished */ } }
</script>

<TermPanel title="bootstrap" running={!finished} bind:log>
  {#snippet footer()}
    {#if finished}
      <span class={failed ? 'bad' : 'ok'}>{failed ? `bootstrap failed: ${job.error}` : 'bootstrap complete'}</span>
      <button class="btn small primary" onclick={onclose}>Close</button>
    {:else}
      <span>installing<span class="cursor" aria-hidden="true"></span></span>
      <button class="btn small" onclick={cancel}>Cancel</button>
    {/if}
  {/snippet}
  {#each lines as l (l.seq)}
    <div class="line note"><span class="t dim">[{t(l.time)}]</span><span class="type {l.event.level === 'error' ? 'bad' : l.event.level === 'warn' ? 'warnc' : ''}">{l.event.message}</span></div>
  {:else}
    <div class="line note"><span class="t dim"></span><span class="dim">starting</span></div>
  {/each}
  {#if !finished}<div class="line note"><span></span><span class="cursor" aria-hidden="true"></span></div>{/if}
</TermPanel>
