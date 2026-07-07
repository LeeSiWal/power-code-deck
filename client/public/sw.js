const CACHE_NAME = 'powercodedeck-v3';

self.addEventListener('install', () => {
  self.skipWaiting();
});

self.addEventListener('activate', (event) => {
  event.waitUntil(
    caches.keys().then((keys) =>
      Promise.all(keys.filter((k) => k !== CACHE_NAME).map((k) => caches.delete(k)))
    )
  );
});

self.addEventListener('fetch', (event) => {
  const url = event.request.url;

  // API and WebSocket are never cached — always go to the network.
  if (url.includes('/api/') || url.includes('/ws')) return;

  // Hashed build assets (/assets/index-<hash>.js, etc.) are immutable — a new
  // build produces new filenames. Serve them CACHE-FIRST so repeat loads are
  // instant instead of re-fetching everything over the (slow) WSL localhost.
  if (url.includes('/assets/')) {
    event.respondWith(
      caches.match(event.request).then(
        (hit) =>
          hit ||
          fetch(event.request).then((response) => {
            const clone = response.clone();
            caches.open(CACHE_NAME).then((cache) => cache.put(event.request, clone));
            return response;
          }),
      ),
    );
    return;
  }

  // Everything else (index.html, manifest, icons) — network-first so a new
  // build's index.html (which references the new asset hashes) is picked up,
  // falling back to cache when offline.
  event.respondWith(
    fetch(event.request)
      .then((response) => {
        const clone = response.clone();
        caches.open(CACHE_NAME).then((cache) => cache.put(event.request, clone));
        return response;
      })
      .catch(() => caches.match(event.request)),
  );
});
