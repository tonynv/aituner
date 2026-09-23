<script>
  import Icon from '../lib/Icon.svelte';
  import Progress from '../lib/Progress.svelte';
  import LogPanel from '../lib/LogPanel.svelte';
  import { app, act } from '../lib/app.svelte.js';
  import { api } from '../lib/api.js';

  let { st } = $props();
  let plan = $state(null);
  let sel = $state({});
  let err = $state('');
  const applying = $derived(!!(st.job?.running && st.job.kind === 'tune'));
  const selectedKeys = $derived(Object.keys(sel).filter((k) => sel[k]));

  async function load() {
    err = '';
    try {
      plan = await api.tunePlan();
      // preselect the changes that matter; the persistence option stays opt-in
      sel = Object.fromEntries(plan.changes.map((c) => [c.key, !c.key.endsWith('.persist')]));
    } catch (e) { err = e.message; }
  }
  $effect(() => { if (st.phase === 'baseline_done' && !applying && !plan) load(); });

  function toggle(c) {
    sel[c.key] = !sel[c.key];
    if (!sel[c.key]) for (const o of plan.changes) if (o.requires === c.key) sel[o.key] = false; // dependents follow
    if (sel[c.key] && c.requires) sel[c.requires] = true;
  }
  async function apply() {
    err = '';
    try { await act(() => api.tuneApply(selectedKeys)); plan = null; } catch (e) { err = e.message; }
  }
</script>

<div class="stack">
  <div>
    <h2>Tune</h2>
    <p class="muted">Proposed system changes, from what was measured on this machine. Nothing is applied until you approve it, and every change can be reverted.</p>
  </div>

  {#if st.phase !== 'baseline_done'}
    <div class="card stack">
      <h3>{st.tune_changes.length ? 'Applied changes' : 'No changes applied'}</h3>
      {#each st.tune_changes as c (c.id)}
        <div class="row"><Icon name={c.reverted_at ? 'x' : 'check'} size={16} /> <span class="mono">{c.key}</span> <span class="muted">{c.before} to {c.after}</span>
          {#if c.reverted_at}<span class="badge">reverted</span>{/if}</div>
      {/each}
    </div>
  {:else if applying}
    <div class="card stack">
      <div class="row"><Icon name="lock" /> <span>Applying. If a system dialog asks for your administrator password, approve it there. aituner never sees the password.</span></div>
      <Progress value={st.job?.progress ?? 0} label="Applying changes" />
      <LogPanel kinds={['tune']} />
    </div>
  {:else if plan}
    {#if plan.changes.length === 0}
      <div class="card"><p>Nothing worth changing on this machine.</p></div>
    {/if}
    {#each plan.changes as c (c.key)}
      <section class="card stack" class:off={!sel[c.key]}>
        <label class="pick">
          <input type="checkbox" checked={sel[c.key]} onchange={() => toggle(c)} />
          <span class="title">{c.title}</span>
          {#if c.needs_admin}<span class="badge"><Icon name="lock" size={12} /> asks for admin approval</span>{/if}
          <span class="badge">{c.persistence.startsWith('yes') ? 'persists across reboot' : 'temporary'}</span>
        </label>
        <p class="muted">{c.why}</p>
        <p><strong>Expected effect:</strong> <span class="muted">{c.effect}</span></p>
        <pre class="diff" aria-label="Change diff">{#each c.diff.split('\n') as line}<span class={line.startsWith('+') ? 'add' : 'del'}>{line}
</span>{/each}</pre>
        <p class="faint">Persistence: {c.persistence}</p>
      </section>
    {/each}

    {#if plan.not_offered.length}
      <details class="card">
        <summary>Not offered ({plan.not_offered.length})</summary>
        <ul>{#each plan.not_offered as n}<li><strong>{n.title}:</strong> <span class="muted">{n.reason}</span></li>{/each}</ul>
      </details>
    {/if}

    <div class="row">
      <button class="btn primary" onclick={apply} disabled={app.busy}>
        {selectedKeys.length ? `Apply ${selectedKeys.length} change${selectedKeys.length > 1 ? 's' : ''} and continue` : 'Continue without changes'}
        <Icon name="arrow" size={16} />
      </button>
      <span class="muted">Next: the benchmark runs again so you can see the difference.</span>
    </div>
  {:else if !err}
    <p class="muted">Reading the system</p>
  {/if}
  {#if err}<p class="bad" role="alert">{err}</p>{/if}
  {#if st.job?.error && st.job.kind === 'tune'}<p class="bad" role="alert"><Icon name="alert" size={16} /> {st.job.error}</p>{/if}
</div>

<style>
  .off { opacity: 0.7; }
  .pick { display: flex; align-items: center; gap: 12px; flex-wrap: wrap; cursor: pointer; min-height: var(--tap); }
  .pick input { width: 20px; height: 20px; accent-color: var(--text); }
  .title { font-weight: 600; }
  .diff { margin: 0; padding: 10px 12px; border: 1px solid var(--border); background: var(--bg); border-radius: var(--radius); overflow-x: auto; white-space: pre; }
  .add { display: block; background: var(--diff-add-bg); color: var(--ok); }
  .del { display: block; background: var(--diff-del-bg); color: var(--bad); }
  .bad { color: var(--bad); }
  ul { padding-left: 18px; display: grid; gap: 6px; margin: 10px 0 0; }
</style>
