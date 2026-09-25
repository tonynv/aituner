import { api, subscribe } from './api.js';

// Single reactive store for the whole app (Svelte 5 runes in a module).
export const app = $state({
  state: null,
  serve: null, // model-server status, polled with the state so every tab can show it
  error: null, // connection / fatal
  tab: 'hardware', // what is on screen; every launch starts at the hardware home page
  detected: false, // this page load has run detection (the server re-detects on request)
  log: [],
  lastSeq: 0,
  busy: false,
});

// Main path: hardware -> downloads -> setup -> monitor. Benchmark, tune and re-run are an optional performance track that
// follows the server's phase; a tab unlocks once the run has reached it.
export const TABS = ['hardware', 'downloads', 'setup', 'monitor', 'benchmark', 'tune', 'rerun', 'report'];
export const PERF_TABS = ['benchmark', 'tune', 'rerun'];
const PHASE_TAB = { detected: 'hardware', baseline_running: 'benchmark', baseline_done: 'tune', tune_reviewed: 'rerun', tuned_running: 'rerun', tuned_done: 'rerun' };
export const phaseTab = () => (app.state && PHASE_TAB[app.state.phase]) || 'hardware';
export const perfUnlocked = (tab) => {
  const order = { benchmark: 1, tune: 2, rerun: 3 };
  const reached = { hardware: 0, benchmark: 1, tune: 2, rerun: 3 }[phaseTab()] ?? 0;
  return reached >= order[tab];
};
export const isRunning = () => !!(app.state && app.state.job && app.state.job.running);

export async function refresh() {
  try {
    app.state = await api.state();
    app.error = null;
    api.serve().then((v) => (app.serve = v)).catch(() => {});
  } catch (e) {
    app.error = e.status === 401 ? 'Session expired. Reopen aituner from its terminal window (press o).' : 'Cannot reach aituner. Is it still running?';
  }
}

let timer;
export function start() {
  refresh();
  const unsub = subscribe((ev) => {
    if (ev.seq <= app.lastSeq) return;
    app.lastSeq = ev.seq;
    app.log.push(ev);
    if (app.log.length > 500) app.log.splice(0, app.log.length - 500);
  });
  const tick = async () => {
    await refresh();
    timer = setTimeout(tick, isRunning() ? 1000 : 4000);
  };
  timer = setTimeout(tick, 1500);
  return () => { clearTimeout(timer); unsub(); };
}

// Run an action, surface its error, and refresh state.
export async function act(fn) {
  app.busy = true;
  try {
    const r = await fn();
    await refresh();
    return r;
  } finally {
    app.busy = false;
  }
}
