import { api, subscribe } from './api.js';

// Single reactive store for the whole app (Svelte 5 runes in a module).
export const app = $state({
  state: null,
  error: null, // connection / fatal
  view: null, // user-selected step; null = follow the server's step
  report: false, // showing the Report view instead of a step
  log: [],
  lastSeq: 0,
  busy: false,
});

// tuned_done stays on step 4 (the comparison); Models is unlocked but the user chooses when to go there
const PHASE_STEP = { detected: 1, baseline_running: 2, baseline_done: 3, tune_reviewed: 4, tuned_running: 4, tuned_done: 4 };
export const currentStep = () => (app.state && app.state.phase ? PHASE_STEP[app.state.phase] || 1 : 1);
export const maxStep = () => (app.state && app.state.recommendations_unlocked ? 5 : currentStep());
export const activeStep = () => app.view ?? currentStep();
export const isRunning = () => !!(app.state && app.state.job && app.state.job.running);

export async function refresh() {
  try {
    app.state = await api.state();
    app.error = null;
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
