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
  recommendations: (unrestricted) => req('GET', `/api/v1/recommendations?unrestricted=${unrestricted ? 1 : 0}`),
};

// Server-sent job output. EventSource reconnects on its own and replays from Last-Event-ID.
export function subscribe(onEvent) {
  const es = new EventSource('/api/v1/events');
  es.onmessage = (m) => { try { onEvent(JSON.parse(m.data)); } catch { /* ignore malformed */ } };
  return () => es.close();
}
