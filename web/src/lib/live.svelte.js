import { api } from './api.js';

// Polls /api/v1/monitor once a second, appending only new samples. Polling is what keeps the server sampling, so it
// stops as soon as nothing on screen needs it, and pauses while the page is hidden (a closed or minimised window).
export function liveMonitor({ keep = 300, tools = false } = {}) {
  const live = $state({ samples: [], source: '', model: null, tools: [], error: '' });
  let seq = 0, timer, stopped = false, withTools = tools;
  async function tick() {
    if (document.hidden) { timer = setTimeout(tick, 1000); return; }
    try {
      const r = await api.monitor(seq, withTools);
      withTools = false;
      if (r.tools) live.tools = r.tools;
      if (r.samples.length) {
        seq = r.samples[r.samples.length - 1].seq;
        live.samples = [...live.samples, ...r.samples].slice(-keep);
      }
      live.source = r.source;
      live.model = r.model;
      live.error = '';
    } catch (e) {
      live.error = e.status === 401 ? 'Session expired: reopen aituner.' : 'Cannot reach aituner.';
    }
    if (!stopped) timer = setTimeout(tick, live.samples.length ? 1000 : 300); // quick until the first sample lands
  }
  tick();
  return {
    live,
    refreshTools() { withTools = true; },
    stop() { stopped = true; clearTimeout(timer); },
  };
}

export const latest = (samples) => samples[samples.length - 1] ?? null;
export const modelName = (repo) => repo?.split('/').pop() ?? '';
export function uptime(startedAt) {
  if (!startedAt) return '';
  const s = Math.max(0, Math.round((Date.now() - startedAt) / 1000)); // startedAt is unix ms
  return s < 60 ? `${s} s` : s < 3600 ? `${Math.floor(s / 60)} min` : `${Math.floor(s / 3600)} h ${Math.floor((s % 3600) / 60)} min`;
}
