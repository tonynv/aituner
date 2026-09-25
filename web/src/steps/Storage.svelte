<script>
  import Icon from '../lib/Icon.svelte';
  import FolderSetting from '../lib/FolderSetting.svelte';
  import { api } from '../lib/api.js';
  import { refresh } from '../lib/app.svelte.js';
  import { fmtBytes } from '../lib/format.js';

  // Everything aituner keeps on this Mac, where it is, and clearing it.
  let st = $state(null);
  let err = $state('');
  let preview = $state(null);
  let clearModels = $state(false);
  let clearReports = $state(false);
  let dialog;
  let clearing = $state(false);
  let done = $state('');

  async function load() {
    try { st = await api.storage(); err = ''; } catch (e) { err = e.message; }
  }
  $effect(() => { load(); });
  const reveal = (which) => () => api.reveal(which).catch((e) => (err = e.message));

  async function openClear() {
    done = ''; err = '';
    try { preview = await api.resetPreview(); clearModels = false; clearReports = false; dialog.showModal(); } catch (e) { err = e.message; }
  }
  const modelBytes = $derived((preview?.models ?? []).reduce((a, m) => a + m.bytes, 0));
  async function confirmClear() {
    clearing = true;
    try {
      const r = await api.reset(clearModels, clearReports);
      dialog.close();
      const bits = [];
      if (clearModels) bits.push(`${r.models_deleted.length} model${r.models_deleted.length === 1 ? '' : 's'} deleted (${fmtBytes(r.bytes_freed)} freed)`);
      if (clearReports) bits.push(`${r.runs_deleted} run${r.runs_deleted === 1 ? '' : 's'} and their reports deleted`);
      done = bits.join(' · ');
      await refresh();
      await load();
    } catch (e) { err = e.message; dialog.close(); } finally { clearing = false; }
  }
</script>

<div class="stack">
  <div>
    <h2>Storage</h2>
    <p class="muted">Everything aituner keeps on this Mac. Change where models, reports and the knowledge base live, open any folder in Finder, or clear what aituner created. Missing folders are created by Bootstrap in Setup, after you confirm.</p>
  </div>

  {#if err}<p class="bad" role="alert"><Icon name="alert" size={16} /> {err}</p>{/if}
  {#if done}<p class="okmsg" role="status"><Icon name="check" size={16} /> {done}</p>{/if}

  {#if st}
    <FolderSetting title="Models" about="Downloaded models. Only folders aituner downloaded are listed or deleted here." folder={st.models}
      onsave={async (v) => { await api.setModelsDir(v); await load(); }} onreveal={reveal('models')} />
    <FolderSetting title="Reports" about="Saved benchmark reports (Markdown, CSV and JSON), written by Save to folder on the Report page." folder={st.reports}
      onsave={async (v) => { st = await api.setFolder('reports', v); }} onreveal={reveal('reports')} />
    <FolderSetting title="Knowledge base" about="Documents for retrieval (RAG) with your running model. Kept here for the knowledge base feature, which is next to be built." folder={st.knowledge}
      onsave={async (v) => { st = await api.setFolder('knowledge', v); }} onreveal={reveal('knowledge')} />
    <section class="card folder" aria-label="App data">
      <div class="row between">
        <div class="row where">
          <Icon name="disk" />
          <div class="info">
            <h3>App data</h3>
            <p class="muted small">Run history, settings and logs. Kept by macOS in your Library; not movable.</p>
            <div class="mono path-text">{st.data.dir}</div>
            <div class="faint small">database {fmtBytes(st.data.db_bytes)} · logs {fmtBytes(st.data.log_bytes)}</div>
          </div>
        </div>
        <button class="btn small" onclick={reveal('data')}><Icon name="external" size={14} /> Show in Finder</button>
      </div>
    </section>

    <section class="card stack tight danger" aria-label="Clear data">
      <div class="row"><Icon name="trash" /><h3>Clear data</h3></div>
      <p class="muted small">Delete the models aituner downloaded and/or every benchmark run and report. You see exactly what goes before anything is deleted.</p>
      <div><button class="btn small" onclick={openClear}><Icon name="trash" size={14} /> Clear models or reports</button></div>
    </section>
  {/if}
</div>

<dialog bind:this={dialog} aria-labelledby="clr-title">
  <h3 id="clr-title">Clear data</h3>
  {#if preview}
    {#if preview.busy}<p class="bad small">{preview.busy}</p>{/if}
    <label class="opt">
      <input type="checkbox" bind:checked={clearModels} disabled={!preview.models.length || !!preview.busy} />
      <span><span><strong>Models</strong> <span class="muted">· {preview.models.length} downloaded by aituner, {fmtBytes(modelBytes)}</span></span>
        {#if preview.models.length}<span class="list mono">{#each preview.models as m (m.repo)}<span>{m.repo} · {fmtBytes(m.bytes)}</span>{/each}</span>{/if}
        <span class="faint small">Other files in {preview.models_dir} are not touched. A running model is stopped first.</span>
      </span>
    </label>
    <label class="opt">
      <input type="checkbox" bind:checked={clearReports} disabled={!!preview.reports_blocked || !!preview.busy || !preview.runs} />
      <span><span><strong>Reports</strong> <span class="muted">· {preview.measured_runs} measured run{preview.measured_runs === 1 ? '' : 's'} ({preview.runs} in total), their results, tuning history and model speed tests</span></span>
        {#if preview.reports_blocked}<span class="bad small">{preview.reports_blocked}</span>{/if}
        <span class="faint small">Report files already saved to the reports folder stay.</span>
      </span>
    </label>
    <p class="muted small">This cannot be undone.</p>
  {/if}
  <div class="row end">
    <button class="btn" onclick={() => dialog.close()}>Cancel</button>
    <button class="btn danger" onclick={confirmClear} disabled={clearing || (!clearModels && !clearReports)}>Delete</button>
  </div>
</dialog>

<style>
  .folder { display: grid; gap: 10px; }
  .between { justify-content: space-between; }
  .where { align-items: flex-start; flex-wrap: nowrap; min-width: 0; flex: 1 1 260px; } /* the buttons wrap below before the text gets squeezed */
  .info { min-width: 0; flex: 1; display: grid; gap: 4px; }
  .path-text { word-break: break-all; }
  .tight { gap: 10px; }
  .small { font-size: 13px; margin: 0; }
  .bad { color: var(--bad); display: flex; gap: 8px; align-items: center; margin: 0; }
  .okmsg { color: var(--ok); display: flex; gap: 8px; align-items: center; margin: 0; }
  .opt { display: flex; gap: 12px; align-items: flex-start; padding: 12px 0; border-top: 1px solid var(--border); cursor: pointer; }
  .opt input { width: 20px; height: 20px; margin-top: 2px; accent-color: var(--text); flex: none; }
  .opt > span { display: grid; gap: 4px; }
  .list { display: grid; gap: 2px; font-size: 12px; color: var(--muted); max-height: 140px; overflow-y: auto; }
  dialog { background: var(--surface); color: var(--text); border: 1px solid var(--border-strong); border-radius: var(--radius); padding: 20px; width: min(560px, calc(100vw - 32px)); }
  dialog::backdrop { background: var(--overlay); }
  .end { justify-content: flex-end; margin-top: 12px; }
</style>
