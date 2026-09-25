<script>
  import Icon from '../lib/Icon.svelte';
  import LogPanel from '../lib/LogPanel.svelte';
  import Progress from '../lib/Progress.svelte';
  import Copyable from '../lib/Copyable.svelte';
  import IntegrationCard from '../lib/IntegrationCard.svelte';
  import { api } from '../lib/api.js';
  import { app, act, refresh } from '../lib/app.svelte.js';
  import { fmtBytes, fmtTokens } from '../lib/format.js';

  let { st } = $props();
  let sv = $state(null);
  let cn = $state(null);
  let repo = $state('');
  let maxTokens = $state(4096);
  let kvBits = $state(0);
  let project = $state('');
  let err = $state('');
  let busy = $state(false);
  let key = $state('');
  let showKey = $state(false);
  let lines = $state([]);
  let after = 0;

  const server = $derived(sv?.server);
  const state = $derived(server?.state ?? 'stopped');
  const live = $derived(state === 'starting' || state === 'running' || state === 'stopping');
  const jobRunning = $derived(!!st.job?.running);
  const rt = $derived(sv?.runtime);
  const models = $derived(sv?.models ?? []);
  const chosen = $derived(models.find((m) => m.repo === repo));
  const head = $derived(st.headline?.tuned ?? st.headline?.baseline);

  // rough time to first token for a Claude Code turn (about 15,000 prompt tokens), scaled from the measured 3B prefill
  const claudeTurnSeconds = $derived.by(() => {
    if (!head?.mlx_prompt_tps || !chosen?.size_gb) return 0;
    return 15000 / (head.mlx_prompt_tps * (1.82 / chosen.size_gb));
  });

  async function load() {
    try {
      sv = await api.serve();
      cn = await api.connect();
      if (!repo && sv.models.length) repo = (sv.models.find((m) => m.running) ?? sv.models[0]).repo;
      if (!project && cn.project_default) project = cn.project_default;
    } catch (e) { err = e.message; }
  }
  async function pollLogs() {
    try {
      const r = await api.serveLogs(after);
      if (r.lines.length) { after = r.lines[r.lines.length - 1].seq; lines = [...lines, ...r.lines].slice(-300); }
    } catch { /* connection problems are shown by the shell */ }
  }
  $effect(() => {
    load(); pollLogs();
    let stop = false, t;
    const tick = async () => { await load(); if (live) await pollLogs(); await refresh(); if (!stop) t = setTimeout(tick, state === 'starting' ? 1000 : 2500); };
    t = setTimeout(tick, 1000);
    return () => { stop = true; clearTimeout(t); };
  });

  async function installRuntime() { err = ''; try { await act(() => api.serveRuntime()); } catch (e) { err = e.message; } }
  async function start() {
    err = ''; busy = true; lines = []; after = 0;
    try { await api.serveStart(repo, maxTokens, kvBits); await load(); } catch (e) { err = e.message; } finally { busy = false; }
  }
  async function stop() { err = ''; busy = true; try { await api.serveStop(); await load(); } catch (e) { err = e.message; } finally { busy = false; } }
  async function reveal() {
    if (!key) { try { key = (await api.serveKey()).key; } catch (e) { err = e.message; return; } }
    showKey = !showKey;
  }
  const ctx = $derived(sv?.context);
  const running = $derived(state === 'running');
</script>

<div class="stack">
  <div>
    <h2>Run a model</h2>
    <p class="muted">Start one of your downloaded models, then connect your editor: aituner installs and configures it for you. Everything stays on this Mac.</p>
  </div>

  <section class="card stack tight" aria-label="MLX for Mac">
    <div class="row between">
      <div class="row"><Icon name="cpu" /><h3>MLX for Mac</h3></div>
      {#if rt?.ready}<span class="badge ok"><Icon name="check" size={12} /> installed</span>{:else}<span class="badge warn">not installed</span>{/if}
    </div>
    {#if rt?.ready}
      <p class="muted">mlx <span class="mono">{rt.mlx_version}</span> · mlx-lm <span class="mono">{rt.mlx_lm_version}</span> · Python <span class="mono">{rt.python_version}</span>. Runs models on the Apple GPU through Metal.</p>
    {:else}
      <p class="muted">MLX is Apple's machine-learning framework for Apple Silicon. aituner installs it, with mlx-lm (the model runner), into its own isolated Python environment. No system Python is changed.</p>
    {/if}
    <div class="row">
      <button class="btn {rt?.ready ? '' : 'primary'}" onclick={installRuntime} disabled={jobRunning || live || busy}>
        <Icon name={rt?.ready ? 'refresh' : 'download'} size={16} /> {rt?.ready ? 'Update MLX' : 'Install MLX for Mac'}
      </button>
      {#if live}<span class="faint small">Stop the model to update MLX.</span>{/if}
    </div>
    {#if st.job?.running && st.job.kind === 'runtime'}<Progress value={st.job.progress ?? 0} label="Installing MLX" /><LogPanel kinds={['runtime']} />{/if}
    {#if st.job?.error && st.job.kind === 'runtime'}<p class="bad" role="alert"><Icon name="alert" size={16} /> {st.job.error}</p>{/if}
  </section>

  <section class="card stack tight" aria-label="Model server">
    <div class="row between">
      <div class="row"><Icon name="server" /><h3>Model</h3></div>
      {#if state === 'running'}<span class="badge ok">running</span>
      {:else if state === 'starting'}<span class="badge warn">loading the model</span>
      {:else if state === 'error'}<span class="badge bad">failed</span>
      {:else}<span class="badge">stopped</span>{/if}
    </div>

    {#if !models.length}
      <p class="muted">No model is downloaded yet. Download one in the <strong>Models</strong> step, then it appears here.</p>
    {:else}
      <div class="fields">
        <label>Model
          <select bind:value={repo} disabled={live}>{#each models as m}<option value={m.repo}>{m.repo} ({m.size_gb.toFixed(1)} GB)</option>{/each}</select>
        </label>
        <label>Reply length limit
          <select bind:value={maxTokens} disabled={live}>{#each [1024, 2048, 4096, 8192, 16384] as n}<option value={n}>{n.toLocaleString()} tokens</option>{/each}</select>
        </label>
        <label>KV cache
          <select bind:value={kvBits} disabled={live}>
            <option value={0}>Full precision (best quality)</option>
            <option value={8}>8-bit (about half the memory)</option>
            <option value={4}>4-bit (about a quarter of the memory)</option>
          </select>
        </label>
      </div>
      {#if claudeTurnSeconds > 0}
        <p class="faint small">A large agent prompt (about 15,000 tokens, like Claude Code sends every turn) takes roughly <strong>{Math.round(claudeTurnSeconds)} s</strong> to process on this model (rough estimate from your measured prefill speed; repeated prompts are cached).</p>
      {/if}
      <div class="row">
        {#if !live}
          <button class="btn primary" onclick={start} disabled={!repo || !rt?.ready || jobRunning || busy}><Icon name="play" size={16} /> Start model</button>
          {#if !rt?.ready}<span class="faint small">Install MLX first.</span>{/if}
        {:else}
          <button class="btn" onclick={stop} disabled={busy || state === 'stopping'}><Icon name="stop" size={16} /> Stop model</button>
        {/if}
      </div>
    {/if}

    {#if server && state !== 'stopped'}
      <dl class="kv">
        <dt>Serving</dt><dd class="mono">{server.repo}</dd>
        {#if running}
          <dt>Ready after</dt><dd>{server.ready_after_s?.toFixed(1)} s</dd>
          <dt>Memory in use</dt><dd>{fmtBytes(server.rss_bytes)}</dd>
          {#if ctx?.known}<dt>Context window</dt><dd>~{fmtTokens(ctx.tokens)} tokens{ctx.model_max ? ` (model maximum ${fmtTokens(ctx.model_max)})` : ''}{ctx.kv_bits ? `, ${ctx.kv_bits}-bit KV cache` : ''}</dd>{/if}
        {/if}
      </dl>
    {/if}
    {#if state === 'error' && server?.error}<pre class="mono errbox" role="alert">{server.error}</pre>{/if}
    {#if live || lines.length}
      <details open={state === 'starting' || state === 'error'}>
        <summary>Server log</summary>
        <div class="log mono" role="log" aria-live="polite">{#each lines as l (l.seq)}<div>{l.text}</div>{:else}<div class="faint">No output yet</div>{/each}</div>
      </details>
    {/if}
  </section>

  {#if running && sv?.gateway}
    <section class="card stack tight" aria-label="Connection details">
      <div class="row"><Icon name="key" /><h3>Connection details</h3></div>
      <p class="muted">Any tool that speaks the OpenAI or Anthropic API can use this. It only listens on this Mac and needs the key.</p>
      <dl class="kv">
        <dt>OpenAI base URL</dt><dd><Copyable text={sv.gateway.base_url} label="Copy base URL" /></dd>
        <dt>Anthropic base URL</dt><dd><Copyable text={sv.gateway.root_url} label="Copy Anthropic base URL" /></dd>
        <dt>Model id</dt><dd><Copyable text={sv.gateway.model} label="Copy model id" /></dd>
        <dt>API key</dt>
        <dd>{#if showKey}<Copyable text={key} label="Copy API key" />{:else}<span class="mono">{sv.key_hint}</span>{/if}
          <button class="btn small" onclick={reveal}>{showKey ? 'Hide' : 'Show'} key</button></dd>
      </dl>
    </section>
  {/if}

  <div>
    <h3>Connect your tools</h3>
    <p class="muted">Choose a tool: aituner shows exactly what it will install and change, asks once, then installs and configures it. Your own editor settings are never modified: each tool gets its own isolated profile.</p>
  </div>
  <label class="proj">Project folder to open
    <input bind:value={project} spellcheck="false" autocomplete="off" aria-label="Project folder" />
  </label>
  {#if cn}
    <div class="grid cards">
      {#each cn.items as item (item.id)}
        <IntegrationCard {item} {project} {jobRunning} disabledReason={running ? '' : 'Start a model first: the configuration embeds the model and the gateway address.'} onchange={load} />
      {/each}
    </div>
    {#if st.job?.kind === 'connect' && (st.job.running || app.log.some((e) => e.kind === 'connect'))}
      <section class="card stack tight" aria-label="Setup log">
        <h3>Setup log</h3>
        {#if st.job.running}<Progress value={st.job.progress ?? 0} label="Setting up" />{/if}
        <LogPanel kinds={['connect']} />
        {#if st.job.error}<p class="bad" role="alert"><Icon name="alert" size={16} /> {st.job.error}</p>{/if}
      </section>
    {/if}
    <p class="faint small">Launchers are written to <span class="mono">{cn.bin_dir}</span>. If that folder is not on your PATH, aituner opens the tools for you, or you can run the launcher by its full path.</p>
  {/if}
  {#if err}<p class="bad" role="alert"><Icon name="alert" size={16} /> {err}</p>{/if}
</div>

<style>
  .between { justify-content: space-between; } .stack.tight { gap: 12px; }
  .fields { display: grid; gap: 12px; grid-template-columns: repeat(auto-fit, minmax(min(100%, 240px), 1fr)); }
  .fields label:first-child { grid-column: 1 / -1; } /* the model name is long: give it a full row */
  label { display: grid; gap: 6px; font-size: 13px; color: var(--muted); min-width: 0; }
  select, input { width: 100%; min-width: 0; min-height: var(--tap); padding: 0 10px; border: 1px solid var(--border-strong); border-radius: var(--radius); background: var(--bg); color: var(--text); font: 14px var(--font-sans); max-width: 100%; }
  input { font-family: var(--font-mono); font-size: 13px; }
  .proj { max-width: 720px; }
  .cards { grid-template-columns: repeat(auto-fill, minmax(min(100%, 420px), 1fr)); }
  .small { font-size: 13px; }
  .bad { color: var(--bad); display: flex; gap: 8px; align-items: center; margin: 0; }
  .errbox { margin: 0; padding: 10px 12px; border: 1px solid var(--bad); color: var(--bad); border-radius: var(--radius); white-space: pre-wrap; overflow-wrap: anywhere; }
  .log { border: 1px solid var(--border); background: var(--bg); border-radius: var(--radius); padding: 10px; max-height: 240px; overflow: auto; font-size: 12.5px; line-height: 1.55; margin-top: 8px; }
  .kv dd { display: flex; gap: 10px; align-items: center; flex-wrap: wrap; min-width: 0; }
  .kv dd :global(.cmd) { flex: 1; min-width: 200px; }
</style>
