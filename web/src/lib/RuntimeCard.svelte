<script>
  import Icon from './Icon.svelte';
  import MachinePic from './MachinePic.svelte';
  import LogPanel from './LogPanel.svelte';
  import Progress from './Progress.svelte';
  import { api } from './api.js';
  import { act } from './app.svelte.js';

  // Install or update MLX for Mac (mlx + mlx-lm in aituner's own isolated Python environment).
  // rt: the runtime info from GET /serve; disabled/note: e.g. the model is running and holds the environment.
  let { st, rt, disabled = false, note = '' } = $props();
  let err = $state('');
  const jobRunning = $derived(!!st.job?.running);
  const installing = $derived(jobRunning && st.job.kind === 'runtime');

  async function install() { err = ''; try { await act(() => api.serveRuntime()); } catch (e) { err = e.message; } }
</script>

<section class="card stack tight" aria-label="MLX for Mac">
  <div class="row between">
    <div class="row"><MachinePic size={40} name={st.hardware?.model?.name} /><h3>MLX for Mac</h3></div>
    {#if rt?.ready}<span class="badge ok"><Icon name="check" size={12} /> installed</span>{:else}<span class="badge warn">not installed</span>{/if}
  </div>
  {#if rt?.ready}
    <p class="muted">mlx <span class="mono">{rt.mlx_version}</span> · mlx-lm <span class="mono">{rt.mlx_lm_version}</span> · Python <span class="mono">{rt.python_version}</span>. Runs models on the Apple GPU through Metal.</p>
  {:else}
    <p class="muted">MLX is Apple's machine-learning framework for Apple Silicon. aituner installs it, with mlx-lm (the model runner), into its own isolated Python environment. No system Python is changed.</p>
  {/if}
  <div class="row">
    <button class="btn {rt?.ready ? '' : 'primary'}" onclick={install} disabled={jobRunning || disabled}>
      <Icon name={rt?.ready ? 'refresh' : 'download'} size={16} /> {rt?.ready ? 'Update MLX' : 'Install MLX for Mac'}
    </button>
    {#if disabled && note}<span class="faint small">{note}</span>{/if}
  </div>
  {#if installing}<Progress value={st.job.progress ?? 0} label="Installing MLX" /><LogPanel kinds={['runtime']} />{/if}
  {#if st.job?.error && st.job.kind === 'runtime'}<p class="bad" role="alert"><Icon name="alert" size={16} /> {st.job.error}</p>{/if}
  {#if err}<p class="bad" role="alert"><Icon name="alert" size={16} /> {err}</p>{/if}
</section>

<style>
  .between { justify-content: space-between; }
  .small { font-size: 13px; }
  .bad { color: var(--bad); display: flex; gap: 8px; align-items: center; margin: 0; }
</style>
