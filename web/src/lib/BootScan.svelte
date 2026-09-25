<script>
  import { onMount, tick } from 'svelte';
  import { detectStream } from './api.js';
  import TermPanel from './TermPanel.svelte';
  import { fmtBytes } from './format.js';

  // The start-up scan: every launch re-detects the hardware, and this shows that detection as it happens. Each line is
  // a real command that just finished (with its real duration); the profile at the end is the parsed result. Nothing
  // here is decorative data. ondetected(state) fires when the state arrives; ondone() when the user moves on.
  let { ondetected, ondone } = $props();

  let lines = $state([]); // {kind: 'probe'|'note'|'err', ...}
  let profile = $state([]);
  let finished = $state(false);
  let failed = $state(false);
  let elapsed = $state(0);
  let log = $state();
  const t0 = performance.now();
  const AUTO_MS = 3500;
  let auto;

  const secs = (ms) => (ms / 1000).toFixed(3);
  const size = (b) => (b >= 1024 ? `${(b / 1024).toFixed(1)} KB` : `${b} B`);

  async function add(l) {
    lines.push({ ...l, at: performance.now() - t0 });
    await tick();
    if (log) log.scrollTop = log.scrollHeight;
  }

  function summarize(st) {
    const h = st.hardware;
    if (!h) return [];
    const sw = h.software ?? {};
    const rows = [
      ['machine', `${h.model?.name ?? 'Mac'}${h.model?.identifier ? ` (${h.model.identifier})` : ''}`],
      ['chip', h.cpu?.chip],
      ['cpu', `${h.cpu?.cores} cores${h.cpu?.performance_cores ? ` · ${h.cpu.performance_cores} performance + ${h.cpu.efficiency_cores} efficiency` : ''}`],
      ['gpu', `${h.gpu?.cores ? `${h.gpu.cores} cores` : h.gpu?.name ?? 'unknown'}${h.gpu?.metal_support ? ` · ${h.gpu.metal_support}` : ''}`],
      ['memory', `${fmtBytes(h.memory?.total_bytes ?? 0)}${h.memory?.unified ? ' unified' : ''}${st.budget_gb > 0 ? ` · GPU budget ${st.budget_gb.toFixed(1)} GB` : ''}`],
      ['storage', h.storage?.total_bytes ? `${fmtBytes(h.storage.free_bytes)} free of ${fmtBytes(h.storage.total_bytes)}` : 'unknown'],
      ['os', `${h.os?.name} ${h.os?.version}${h.os?.build ? ` (${h.os.build})` : ''}`],
      ['power', `${h.power?.source === 'ac' ? 'AC' : h.power?.source === 'battery' ? 'battery' : 'unknown'}${h.power?.thermal_note ? ` · ${h.power.thermal_note.toLowerCase()}` : ''}`],
      ['mlx', sw.mlx?.ready ? `mlx ${sw.mlx.mlx_version} · mlx-lm ${sw.mlx.mlx_lm_version}` : 'not installed (Setup installs it)'],
      ['ollama', sw.ollama?.installed ? `${sw.ollama.version || 'installed'}${sw.ollama.running ? ' · running' : ''}` : 'not installed (optional)'],
      ['python', sw.python?.version || 'not found'],
    ];
    return rows.filter(([, v]) => v);
  }

  function finish() {
    finished = true;
    elapsed = performance.now() - t0;
    auto = setTimeout(done, AUTO_MS);
  }
  function done() {
    clearTimeout(auto);
    ondone();
  }
  function onkey(e) {
    if (e.key === 'Enter' || e.key === 'Escape') { e.preventDefault(); done(); }
  }

  onMount(() => {
    add({ kind: 'note', text: 'probing hardware, sensors and runtimes' });
    detectStream((m) => {
      if (m.probe) add({ kind: 'probe', ...m.probe });
      else if (m.health) {
        const h = m.health;
        const bits = [
          h.gpu_busy_pct >= 0 ? `gpu ${h.gpu_busy_pct}% busy` : null,
          h.cores ? `load ${h.load1.toFixed(2)} on ${h.cores} cores` : null,
          h.free_mem_pct >= 0 ? `memory ${h.free_mem_pct}% free` : null,
          h.thermal_warning ? `THROTTLED (${h.thermal_note})` : 'no throttling',
          h.on_battery ? 'on battery' : 'on AC',
        ].filter(Boolean);
        add({ kind: 'note', text: `sensors: ${bits.join(' · ')}` });
      } else if (m.state) {
        ondetected(m.state);
        profile = summarize(m.state);
        finish();
        tick().then(() => log?.scrollTo({ top: log.scrollHeight }));
      } else if (m.error) {
        failed = true;
        add({ kind: 'err', text: m.error });
        finish();
      }
    }).catch((e) => {
      failed = true;
      add({ kind: 'err', text: e.message });
      finish();
    });
    return () => clearTimeout(auto);
  });
  const probes = $derived(lines.filter((l) => l.kind === 'probe').length);
</script>

<svelte:window onkeydown={onkey} />

<TermPanel title="system scan" running={!finished} bind:log>
  {#snippet footer()}
    {#if finished}
      <span class={failed ? 'bad' : ''}>{failed ? 'scan incomplete' : 'scan complete'} <span class="dim">· {probes} probes · {secs(elapsed)} s</span></span>
      <button class="btn small primary" onclick={done}>Continue <span class="kbd">return</span></button>
      <div class="countdown" style:animation-duration="{AUTO_MS}ms" aria-hidden="true"></div>
    {:else}
      <span>scanning<span class="cursor" aria-hidden="true"></span></span>
      <button class="btn small" onclick={done}>Skip</button>
    {/if}
  {/snippet}

    {#each lines as l, i (i)}
      {#if l.kind === 'probe'}
        <div class="line">
          <span class="t dim">[{secs(l.at)}]</span>
          <span class="cmd type">{l.cmd}</span>
          <span class="res dim">{l.out ? l.out : size(l.bytes)} · {l.ms} ms</span>
          <span class={l.ok ? 'ok' : 'bad'}>{l.ok ? 'ok' : 'fail'}</span>
        </div>
      {:else}
        <div class="line note"><span class="t dim">[{secs(l.at)}]</span><span class={l.kind === 'err' ? 'bad type' : 'type'}>{l.kind === 'err' ? '! ' : '> '}{l.text}</span></div>
      {/if}
    {/each}
    {#if profile.length}
      <div class="profile">
        <div class="rule dim">── profile ──────────────────────────────</div>
        {#each profile as [k, v], i (k)}
          <div class="kv" style:animation-delay="{i * 60}ms"><span class="k dim">{k}</span><span>{v}</span></div>
        {/each}
      </div>
    {/if}
    {#if !finished}<div class="line"><span class="cursor" aria-hidden="true"></span></div>{/if}
</TermPanel>

<style>
  .profile { margin-top: 14px; }
  .rule { white-space: nowrap; overflow: hidden; }
  .kv { display: grid; grid-template-columns: 88px minmax(0, 1fr); gap: 12px; animation: type 0.3s steps(20, end) both; }
  .k { text-transform: uppercase; letter-spacing: 0.06em; font-size: 12px; }
  .countdown { position: absolute; left: 0; top: -1px; height: 1px; width: 100%; background: var(--text); transform-origin: left; animation: countdown linear both; }
  @keyframes countdown { from { transform: scaleX(1); } to { transform: scaleX(0); } }
  @media (max-width: 560px) { .kv { grid-template-columns: 72px minmax(0, 1fr); } }
</style>
