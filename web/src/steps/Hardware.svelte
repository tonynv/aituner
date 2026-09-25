<script>
  import Icon from '../lib/Icon.svelte';
  import { fmtBytes } from '../lib/format.js';
  import { app, act } from '../lib/app.svelte.js';
  import { api } from '../lib/api.js';

  let { hw, plan, phase, detecting = false } = $props();
  let modelsDir = $state('');
  $effect(() => { api.settings().then((s) => (modelsDir = s.models_dir)).catch(() => {}); });
  let dialog;
  let err = $state('');

  const wired = $derived(hw.memory.wired_limit_mb > 0 ? `${(hw.memory.wired_limit_mb / 1024).toFixed(1)} GB (set)` : 'macOS default');
  const needsConfirm = $derived(plan && plan.downloads && plan.downloads.length > 0);

  async function run(confirm) {
    err = '';
    dialog?.close();
    try { await act(() => api.benchmark(confirm)); app.tab = 'benchmark'; } catch (e) { err = e.message; }
  }
  function click() { needsConfirm ? dialog.showModal() : run(false); }
  async function redetect() { err = ''; try { await act(() => api.detect()); } catch (e) { err = e.message; } }
</script>

<div class="stack">
  <div>
    <h2>This machine</h2>
    <p class="muted">{detecting ? 'Detecting the hardware…' : 'Detected directly from the hardware, fresh on every launch.'}</p>
  </div>

  {#if !detecting}

  <div class="grid">
    <section class="card">
      <div class="head"><Icon name="cpu" /><h3>System</h3></div>
      <dl class="kv">
        <dt>Model</dt><dd>{hw.model.name} <span class="faint mono">{hw.model.identifier}</span></dd>
        <dt>Chip</dt><dd>{hw.cpu.chip}</dd>
        <dt>CPU cores</dt><dd>{hw.cpu.cores} ({hw.cpu.performance_cores} performance, {hw.cpu.efficiency_cores} efficiency)</dd>
        <dt>OS</dt><dd>{hw.os.name} {hw.os.version} <span class="faint mono">{hw.os.build}</span></dd>
        <dt>Architecture</dt><dd class="mono">{hw.arch}</dd>
      </dl>
    </section>

    <section class="card">
      <div class="head"><Icon name="gpu" /><h3>GPU</h3></div>
      <dl class="kv">
        <dt>GPU</dt><dd>{hw.gpu.name}</dd>
        <dt>GPU cores</dt><dd>{hw.gpu.cores}</dd>
        <dt>Metal</dt><dd class="mono">{hw.gpu.metal_support}</dd>
        <dt>Displays</dt><dd>{hw.displays?.length ? hw.displays.join(', ') : 'none'}</dd>
      </dl>
    </section>

    <section class="card">
      <div class="head"><Icon name="memory" /><h3>Memory</h3></div>
      <dl class="kv">
        <dt>Total</dt><dd>{fmtBytes(hw.memory.total_bytes)} {hw.memory.type}{hw.memory.unified ? ', unified with GPU' : ''}</dd>
        <dt>GPU memory limit</dt><dd>{wired}</dd>
        <dt>Free now</dt><dd>{hw.memory.pressure_free_pct >= 0 ? hw.memory.pressure_free_pct + '%' : 'unknown'}</dd>
      </dl>
    </section>

    <section class="card">
      <div class="head"><Icon name="disk" /><h3>Storage</h3></div>
      <dl class="kv">
        <dt>Free</dt><dd>{fmtBytes(hw.storage.free_bytes)} of {fmtBytes(hw.storage.total_bytes)}</dd>
        <dt>Models go to</dt><dd class="faint mono">{modelsDir || 'not set'}</dd>
      </dl>
    </section>

    <section class="card">
      <div class="head"><Icon name="power" /><h3>Power and thermal</h3></div>
      <dl class="kv">
        <dt>Power</dt><dd>{hw.power.source === 'ac' ? 'AC power' : hw.power.source === 'battery' ? 'Battery' : 'unknown'}</dd>
        <dt>Thermal</dt><dd>{hw.power.thermal_note.replace(/^Note:\s*/, '') || 'unknown'}</dd>
        <dt>High Power Mode</dt><dd>{hw.power.high_power_mode_supported ? 'supported' : 'not available on this Mac'}</dd>
      </dl>
    </section>

    <section class="card">
      <div class="head"><Icon name="terminal" /><h3>AI software</h3></div>
      <dl class="kv">
        <dt>Homebrew</dt><dd>{hw.software.homebrew ? 'installed' : 'not found'}</dd>
        <dt>Python</dt><dd>{hw.software.python.version || 'not found'}</dd>
        <dt>MLX</dt><dd>{hw.software.mlx.ready ? `mlx ${hw.software.mlx.mlx_version}, mlx-lm ${hw.software.mlx.mlx_lm_version}` : 'not installed yet'}</dd>
        <dt>Ollama</dt><dd>{hw.software.ollama.running ? `${hw.software.ollama.version}, running` : hw.software.ollama.installed ? 'installed, not running' : 'not installed'}</dd>
        {#if hw.software.ollama.models?.length}
          <dt>Ollama models</dt><dd>{hw.software.ollama.models.map((m) => m.name).join(', ')}</dd>
        {/if}
      </dl>
    </section>
  </div>

  <div class="card cta">
    <div>
      <h3>Next: choose models to download</h3>
      <p class="muted">Pick models that fit this machine and queue them. Then set up MLX and connect your editor.</p>
    </div>
    <div class="row">
      <button class="btn" onclick={redetect} disabled={app.busy}><Icon name="refresh" size={16} /> Re-detect</button>
      <button class="btn primary" onclick={() => (app.tab = 'downloads')}>Next: downloads <Icon name="arrow" size={16} /></button>
    </div>
  </div>

  {#if phase === 'detected'}
    <details class="card optional">
      <summary><Icon name="gauge" size={16} /> Optional: benchmark this machine</summary>
      <p class="muted">Runs memory, GPU and LLM benchmarks (about 2 minutes). It pins real numbers on every tab, adds speed estimates to the model list, and is the baseline that tuning is judged against.</p>
      <div class="row"><button class="btn" onclick={click} disabled={app.busy}><Icon name="play" size={16} /> Run benchmark</button></div>
    </details>
  {/if}
  {#if err}<p class="bad" role="alert">{err}</p>{/if}
  {/if}
</div>

<dialog bind:this={dialog} aria-labelledby="dl-title">
  <h3 id="dl-title">Install and download for the benchmark</h3>
  <p class="muted">These are needed once. Nothing else is installed.</p>
  <ul>
    {#each plan?.downloads ?? [] as d}<li>{d.what} <span class="muted">({d.size})</span></li>{/each}
  </ul>
  <div class="row end">
    <button class="btn" onclick={() => dialog.close()}>Cancel</button>
    <button class="btn primary" onclick={() => run(true)}><Icon name="download" size={16} /> Install and run</button>
  </div>
</dialog>

<style>
  .head { display: flex; align-items: center; gap: 8px; margin-bottom: 12px; color: var(--muted); }
  .head h3 { color: var(--text); }
  .cta { display: flex; justify-content: space-between; gap: 16px; align-items: center; flex-wrap: wrap; }
  .bad { color: var(--bad); }
  .optional summary { display: flex; align-items: center; gap: 8px; min-height: var(--tap); cursor: pointer; }
  .optional p { margin: 8px 0 12px; }
  dialog { background: var(--surface); color: var(--text); border: 1px solid var(--border-strong); border-radius: var(--radius); padding: 20px; max-width: min(520px, calc(100vw - 32px)); }
  dialog::backdrop { background: var(--overlay); }
  dialog ul { padding-left: 18px; margin: 12px 0 20px; display: grid; gap: 8px; }
  .end { justify-content: flex-end; }
</style>
