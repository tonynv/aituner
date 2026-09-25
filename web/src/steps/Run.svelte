<script>
  import Icon from '../lib/Icon.svelte';
  import LogPanel from '../lib/LogPanel.svelte';
  import Progress from '../lib/Progress.svelte';
  import RuntimeCard from '../lib/RuntimeCard.svelte';
  import Copyable from '../lib/Copyable.svelte';
  import IntegrationCard from '../lib/IntegrationCard.svelte';
  import { api } from '../lib/api.js';
  import { app, refresh } from '../lib/app.svelte.js';
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

  // Wait before the first word of a cold Claude Code request: ~1.8K tokens through aituner-claude (lean), ~27.7K unmodified.
  // Uses the measured prefill speed of this model when the model benchmark has run; otherwise scales the 3B benchmark model.
  const LEAN = 1800, FULL = 27700;
  let bench = $state(null);
  $effect(() => { api.modelBench().then((r) => (bench = r.items.find((i) => i.repo === repo)?.result ?? null)).catch(() => {}); });
  const turn = $derived.by(() => {
    const at = (n) => bench?.runs?.find((c) => c.kv_bits === 0 && c.prompt_tokens === n)?.prefill_tps;
    if (at(1024) && at(16384)) return { lean: LEAN / at(1024), full: FULL / at(16384), measured: true };
    if (!head?.mlx_prompt_tps || !chosen?.size_gb) return null;
    const tps = head.mlx_prompt_tps * (1.82 / chosen.size_gb);
    return { lean: LEAN / tps, full: FULL / tps, measured: false };
  });
  const fmtSecs = (v) => (v < 10 ? `${v.toFixed(1)} s` : v < 120 ? `${Math.round(v)} s` : `${(v / 60).toFixed(1)} min`);

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
    <h2>Set up and run</h2>
    <p class="muted">Start one of your downloaded models, then connect your editor: aituner installs and configures it for you. Everything stays on this Mac.</p>
  </div>

  <RuntimeCard {st} {rt} disabled={live || busy} note="Stop the model to update MLX." />

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
      {#if turn}
        <p class="faint small">First reply of a Claude Code session takes about <strong>{fmtSecs(turn.lean)}</strong> through <span class="mono">aituner-claude</span> (a lean 1.8K-token request), or <strong>{fmtSecs(turn.full)}</strong> for an unmodified Claude Code setup (27.7K tokens). {turn.measured ? 'Measured on this model.' : 'Estimated from the benchmark model; run the model benchmark on the Downloads page for exact figures.'} Repeat turns reuse the cached prompt.</p>
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
