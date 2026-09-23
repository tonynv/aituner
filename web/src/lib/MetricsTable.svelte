<script>
  import { metricLabel, fmtNum, spread } from './format.js';
  let { metrics = [] } = $props();
</script>

<div class="scroll-x">
  <table>
    <thead><tr><th>Measurement</th><th class="num">Result</th><th class="num">Spread</th><th class="num">Trials</th></tr></thead>
    <tbody>
      {#each metrics as m (m.suite + m.engine + m.metric)}
        <tr>
          <td>{metricLabel(m)}{#if m.model}<div class="faint mono">{m.model}</div>{/if}</td>
          <td class="num">{fmtNum(m.value)} <span class="muted">{m.unit}</span></td>
          <td class="num muted">{spread(m.trials) < 0.05 ? '<0.1' : spread(m.trials).toFixed(1)}%</td>
          <td class="num muted">{m.trials?.length ?? 0}</td>
        </tr>
      {/each}
    </tbody>
  </table>
</div>
