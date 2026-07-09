const CACHE_NAME = 'powercodedeck-v4';

self.addEventListener('install', () => {
  self.skipWaiting();
});

self.addEventListener('activate', (event) => {
  event.waitUntil(
    (async () => {
      const keys = await caches.keys();
      await Promise.all(keys.filter((k) => k !== CACHE_NAME).map((k) => caches.delete(k)));
      // Take control of already-open tabs immediately so an upgrade doesn't keep
      // serving the previous build until every tab is closed.
      await self.clients.claim();
    })(),
  );
});

// cacheable reports whether a network Response is safe to store. Only cache
// same-origin, non-redirected 200s. A 302 (e.g. a forward-auth redirect to an
// SSO portal), an opaque cross-origin response, or an error page must NEVER be
// cached as the app shell — doing so poisons index.html and leaves the app
// stuck on a redirect/blank page even after auth succeeds.
function cacheable(response) {
  return response && response.ok && response.type === 'basic' && !response.redirected;
}

self.addEventListener('fetch', (event) => {
  const url = event.request.url;

  // API and WebSocket are never cached — always go to the network.
  if (url.includes('/api/') || url.includes('/ws')) return;

  // Only handle GETs; never cache POST/PUT/etc.
  if (event.request.method !== 'GET') return;

  // Hashed build assets (/assets/index-<hash>.js, etc.) are immutable — a new
  // build produces new filenames. Serve them CACHE-FIRST so repeat loads are
  // instant instead of re-fetching everything over the (slow) WSL localhost.
  if (url.includes('/assets/')) {
    event.respondWith(
      caches.match(event.request).then(
        (hit) =>
          hit ||
          fetch(event.request).then((response) => {
            if (cacheable(response)) {
              const clone = response.clone();
              caches.open(CACHE_NAME).then((cache) => cache.put(event.request, clone));
            }
            return response;
          }),
      ),
    );
    return;
  }

  // Everything else (index.html, manifest, icons) — network-first so a new
  // build's index.html (which references the new asset hashes) is picked up.
  // Only a clean same-origin 200 is cached, and the cache is used solely as an
  // offline fallback — a redirect/error is passed straight through, never stored.
  event.respondWith(
    fetch(event.request)
      .then((response) => {
        if (cacheable(response)) {
          const clone = response.clone();
          caches.open(CACHE_NAME).then((cache) => cache.put(event.request, clone));
        }
        return response;
      })
      .catch(() => caches.match(event.request).then((hit) => hit || Promise.reject(new Error('offline')))),
  );
});
