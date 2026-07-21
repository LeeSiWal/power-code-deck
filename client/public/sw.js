// This is the ONE service worker the PWA registers (from main.tsx). It does two
// jobs: (1) app-shell caching, and (2) Web Push. Both MUST live here — a second
// worker (an earlier /service-worker.js) fought this one for the scope-'/'
// registration, so the push subscription bound to whichever won and pushes were
// silently dropped by the worker that had no 'push' handler. One worker, no race.
const CACHE_NAME = 'powercodedeck-v6';

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
  // cache:'no-store' so the app shell bypasses the HTTP cache and always gets the
  // current index.html (with the current asset hashes) — iOS Safari otherwise
  // serves a heuristically-cached shell and the app stays stuck on an old build.
  event.respondWith(
    fetch(event.request, { cache: 'no-store' })
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

// --- Web Push -------------------------------------------------------------
// Receives push messages when the PWA is backgrounded (or, on iOS, at all —
// iOS only delivers Web Push to an installed PWA).

self.addEventListener('push', (event) => {
  let data = {};
  try {
    data = event.data ? event.data.json() : {};
  } catch {
    data = { title: 'PowerCodeDeck', body: event.data ? event.data.text() : '' };
  }
  const title = data.title || 'PowerCodeDeck';
  const options = {
    body: data.body || '',
    // Same tag replaces an earlier notification for the same agent/reason instead
    // of stacking a pile of them.
    tag: data.tag || 'powercodedeck',
    renotify: true,
    data: { url: data.url || '/' },
    icon: '/icon-192.png',
    badge: '/icon-192.png',
  };

  // ALWAYS show a notification for every push. iOS/iPadOS enforces this: a push that
  // doesn't result in a shown notification is treated as a violation, and repeated
  // "silent" pushes get the subscription revoked.
  //
  // Defensive fallback: if showNotification rejects for ANY reason (e.g. a bad
  // icon/badge URL — iOS is strict and can refuse the whole notification), retry with
  // the bare essentials so a notification still appears.
  event.waitUntil(
    self.registration.showNotification(title, options).catch(() =>
      self.registration.showNotification(title, {
        body: options.body,
        tag: options.tag,
        data: options.data,
      }),
    ),
  );
});

self.addEventListener('notificationclick', (event) => {
  event.notification.close();
  const url = (event.notification.data && event.notification.data.url) || '/';
  event.waitUntil(
    (async () => {
      const clientList = await self.clients.matchAll({ type: 'window', includeUncontrolled: true });
      // Reuse an existing tab if one is open — navigate it and focus, rather than
      // spawning yet another deck window.
      for (const client of clientList) {
        if ('focus' in client) {
          try {
            await client.navigate(url);
          } catch {
            /* cross-origin or navigation blocked — just focus */
          }
          return client.focus();
        }
      }
      if (self.clients.openWindow) return self.clients.openWindow(url);
    })(),
  );
});
