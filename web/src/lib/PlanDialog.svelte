<script>
  import Icon from './Icon.svelte';
  // Shows exactly what a setup will do before anything runs: steps, the exact commands, the files, and what is NOT touched.
  let { plan, onconfirm, oncancel, busy = false } = $props();
  let dialog;
  export function open() { dialog?.showModal(); }
  export function close() { dialog?.close(); }
</script>

<dialog bind:this={dialog} aria-labelledby="plan-title" onclose={oncancel}>
  {#if plan}
    <h3 id="plan-title">Set up {plan.title}</h3>
    <p class="muted">{plan.summary}</p>
    <ol class="steps">
      {#each plan.steps as s}
        <li><span class="kind {s.kind}"><Icon name={s.kind === 'install' ? 'download' : s.kind === 'write' ? 'copy' : 'info'} size={14} /></span>
          <div><strong>{s.title}</strong>{#if s.detail}<div class="muted small">{s.detail}</div>{/if}</div></li>
      {/each}
    </ol>
    {#if plan.commands?.length}
      <h4>Commands that will run</h4>
      <pre class="mono cmds">{plan.commands.join('\n')}</pre>
    {/if}
    {#if plan.files?.length}
      <h4>Files created or updated</h4>
      <ul class="files mono">{#each plan.files as f}<li>{f}</li>{/each}</ul>
    {/if}
    <h4>Not touched</h4>
    <ul class="keep">{#each plan.will_not_touch as f}<li><Icon name="shield" size={14} /> {f}</li>{/each}</ul>
    {#if plan.caveat}<p class="caveat" role="note"><Icon name="alert" size={14} /> {plan.caveat}</p>{/if}
    <div class="row end">
      <button class="btn" onclick={() => dialog.close()}>Cancel</button>
      <button class="btn primary" onclick={onconfirm} disabled={busy}><Icon name="check" size={16} /> Install and configure</button>
    </div>
  {/if}
</dialog>

<style>
  dialog { background: var(--surface); color: var(--text); border: 1px solid var(--border-strong); border-radius: var(--radius); padding: 22px; width: min(640px, calc(100vw - 32px)); max-height: calc(100vh - 32px); overflow: auto; }
  dialog::backdrop { background: var(--overlay); }
  h4 { font-size: 12px; text-transform: uppercase; letter-spacing: 0.05em; color: var(--muted); margin: 18px 0 6px; font-weight: 500; }
  .steps { list-style: none; padding: 0; margin: 14px 0 0; display: grid; gap: 10px; }
  .steps li { display: flex; gap: 10px; align-items: flex-start; }
  .kind { display: inline-flex; width: 24px; height: 24px; align-items: center; justify-content: center; border: 1px solid var(--border-strong); border-radius: var(--radius); flex: none; color: var(--muted); }
  .kind.install { color: var(--text); }
  .small { font-size: 13px; }
  .cmds { margin: 0; padding: 10px 12px; background: var(--bg); border: 1px solid var(--border); border-radius: var(--radius); white-space: pre-wrap; overflow-wrap: anywhere; }
  .files, .keep { list-style: none; padding: 0; margin: 0; display: grid; gap: 4px; font-size: 13px; overflow-wrap: anywhere; }
  .keep li { display: flex; gap: 8px; align-items: flex-start; color: var(--muted); }
  .caveat { margin: 16px 0 0; padding: 10px 12px; border: 1px solid var(--warn); color: var(--warn); border-radius: var(--radius); display: flex; gap: 8px; font-size: 14px; }
  .caveat :global(svg) { flex: none; margin-top: 3px; }
  .end { justify-content: flex-end; margin-top: 20px; }
</style>
