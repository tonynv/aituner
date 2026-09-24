// App-shell cache only. The API is never cached: benchmark and hardware data must always be live.
// Network-first so a new build is picked up immediately; the cache is the offline fallback.
const CACHE = 'aituner-shell-v2';

self.addEventListener('install', (e) => {
  e.waitUntil(caches.open(CACHE).then((c) => c.addAll(['/', '/manifest.webmanifest', '/icon.svg'])).then(() => self.skipWaiting()));
});

self.addEventListener('activate', (e) => {
  e.waitUntil(caches.keys().then((ks) => Promise.all(ks.filter((k) => k !== CACHE).map((k) => caches.delete(k)))).then(() => self.clients.claim()));
});

// Vite emits content-hashed files, so every build leaves new names behind. After a fresh shell arrives,
// drop cached /assets/ entries the shell no longer references so the cache cannot grow without bound.
async function pruneAssets(cache, html) {
  const live = new Set([...html.matchAll(/\/assets\/[A-Za-z0-9._-]+/g)].map((m) => m[0]));
  for (const req of await cache.keys()) {
    const p = new URL(req.url).pathname;
    if (p.startsWith('/assets/') && !live.has(p)) await cache.delete(req);
  }
}

self.addEventListener('fetch', (e) => {
  const url = new URL(e.request.url);
  if (e.request.method !== 'GET' || url.origin !== location.origin || url.pathname.startsWith('/api/')) return;
  e.respondWith(
    fetch(e.request)
      .then(async (res) => {
        if (res.ok) {
          const cache = await caches.open(CACHE);
          await cache.put(e.request, res.clone());
          if (e.request.mode === 'navigate') pruneAssets(cache, await res.clone().text()).catch(() => {});
        }
        return res;
      })
      .catch(() => caches.match(e.request).then((m) => m || caches.match('/'))),
  );
});
