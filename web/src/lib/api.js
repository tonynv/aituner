async function req(method, path, body) {
  const res = await fetch(path, {
    method,
    credentials: 'same-origin',
    headers: body === undefined ? undefined : { 'Content-Type': 'application/json' },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  let data = null;
  try { data = await res.json(); } catch { /* non-JSON body */ }
  if (!res.ok) {
    const err = new Error((data && data.message) || res.statusText);
    err.status = res.status; err.kind = data && data.error; err.data = data;
    throw err;
  }
  return data;
}

export const api = {
  state: () => req('GET', '/api/v1/state'),
  detect: () => req('POST', '/api/v1/detect', {}),
  newRun: () => req('POST', '/api/v1/runs', {}),
  benchmark: (confirm) => req('POST', '/api/v1/benchmark', { confirm_downloads: confirm }),
  cancel: () => req('POST', '/api/v1/job/cancel', {}),
  tunePlan: () => req('GET', '/api/v1/tune/plan'),
  tuneApply: (keys) => req('POST', '/api/v1/tune/apply', { keys }),
  tuneRevert: () => req('POST', '/api/v1/tune/revert', {}),
  runs: () => req('GET', '/api/v1/runs'),
  report: (run) => req('GET', `/api/v1/report${run ? `?run=${encodeURIComponent(run)}` : ''}`),
  compare: (a, b) => req('GET', `/api/v1/compare?a=${encodeURIComponent(a)}&b=${encodeURIComponent(b)}`),
  serve: () => req('GET', '/api/v1/serve'),
  serveKey: () => req('GET', '/api/v1/serve/key'),
  serveLogs: (after) => req('GET', `/api/v1/serve/logs?after=${after}`),
  serveRuntime: () => req('POST', '/api/v1/serve/runtime', {}),
  serveStart: (repo, max_tokens, kv_bits) => req('POST', '/api/v1/serve/start', { repo, max_tokens, kv_bits }),
  serveStop: () => req('POST', '/api/v1/serve/stop', {}),
  connect: () => req('GET', '/api/v1/connect'),
  connectSetup: (id) => req('POST', '/api/v1/connect/setup', { id, confirm: true }),
  connectRemove: (id) => req('POST', '/api/v1/connect/remove', { id }),
  connectLaunch: (id, project) => req('POST', '/api/v1/connect/launch', { id, project }),
  modelBench: () => req('GET', '/api/v1/modelbench'),
  startModelBench: (repos) => req('POST', '/api/v1/modelbench', { repos }),
  settings: () => req('GET', '/api/v1/settings'),
  setModelsDir: (dir) => req('PUT', '/api/v1/settings', { models_dir: dir }),
  downloads: () => req('GET', '/api/v1/downloads'),
  startDownload: (repo) => req('POST', '/api/v1/downloads', { repo }),
  cancelDownload: (repo) => req('POST', '/api/v1/downloads/cancel', { repo }),
  monitor: (since, tools) => req('GET', `/api/v1/monitor?since=${since}${tools ? '&tools=1' : ''}`),
  monitorTool: (id, action) => req('POST', '/api/v1/monitor/tool', { id, action, confirm: action === 'install' }),
  updateStatus: () => req('GET', '/api/v1/update'),
  updateCheck: () => req('POST', '/api/v1/update/check', {}),
  updateSettings: (v) => req('PUT', '/api/v1/update/settings', v),
  updateInstall: () => req('POST', '/api/v1/update/install', { confirm: true }),
  storage: () => req('GET', '/api/v1/storage'),
  setFolder: (kind, dir) => req('PUT', `/api/v1/storage/${kind}`, { dir }),
  services: () => req('GET', '/api/v1/services'),
  service: (id) => req('GET', `/api/v1/services/${encodeURIComponent(id)}`),
  serviceAction: (id, action, option) => req('POST', `/api/v1/services/${encodeURIComponent(id)}/${encodeURIComponent(action)}`, { confirm: true, option }),
  bootstrapPlan: () => req('GET', '/api/v1/bootstrap'),
  bootstrap: () => req('POST', '/api/v1/bootstrap', { confirm: true }),
  reveal: (which) => req('POST', '/api/v1/storage/reveal', { which }),
  saveReport: (run) => req('POST', '/api/v1/report/save', { run }),
  resetPreview: () => req('GET', '/api/v1/reset'),
  reset: (models, reports) => req('POST', '/api/v1/reset', { models, reports, confirm: true }),
  recommendations: (unrestricted) => req('GET', `/api/v1/recommendations?unrestricted=${unrestricted ? 1 : 0}`),
};

// Streams hardware detection (NDJSON): onLine receives {probe}, {health}, then {state} or {error} as each arrives.
export async function detectStream(onLine) {
  const res = await fetch('/api/v1/detect', {
    method: 'POST', credentials: 'same-origin', body: '{}',
    headers: { 'Content-Type': 'application/json', Accept: 'application/x-ndjson' },
  });
  if (!res.ok || !res.body) {
    const err = new Error(res.status === 401 ? 'Session expired. Reopen aituner.' : res.statusText || 'detection failed');
    err.status = res.status;
    throw err;
  }
  const reader = res.body.pipeThrough(new TextDecoderStream()).getReader();
  let buf = '';
  for (;;) {
    const { value, done } = await reader.read();
    if (done) break;
    buf += value;
    let i;
    while ((i = buf.indexOf('\n')) >= 0) {
      const line = buf.slice(0, i).trim();
      buf = buf.slice(i + 1);
      if (line) onLine(JSON.parse(line));
    }
  }
}

// Server-sent job output. EventSource reconnects on its own and replays from Last-Event-ID.
export function subscribe(onEvent) {
  const es = new EventSource('/api/v1/events');
  es.onmessage = (m) => { try { onEvent(JSON.parse(m.data)); } catch { /* ignore malformed */ } };
  return () => es.close();
}

// Download links are plain same-origin GETs (the session cookie authenticates them).
export const reportUrl = (run, format) => `/api/v1/report?${run ? `run=${encodeURIComponent(run)}&` : ''}format=${format}&download=1`;
