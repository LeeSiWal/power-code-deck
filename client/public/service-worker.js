/*
 * PowerCodeDeck service worker — Web Push only.
 *
 * Deliberately NOT a caching/offline worker: the app shell is served with
 * Cache-Control: no-cache and the deck is useless offline anyway (it's a live
 * console). Adding a cache here would just risk serving a stale build. So this
 * worker exists for one reason — to receive push messages when the PWA is in the
 * background (or, on iOS, at all — iOS only delivers Web Push to an installed PWA).
 */

// Take over immediately so a freshly-registered worker can receive pushes without
// waiting for every tab to close first.
self.addEventListener('install', () => self.skipWaiting());
self.addEventListener('activate', (event) => event.waitUntil(self.clients.claim()));

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
  // "silent" pushes get the subscription revoked. (An earlier version suppressed the
  // notification when the app was focused — which meant nothing ever showed while you
  // were testing with the app open, and quietly put the iOS subscription at risk.)
  event.waitUntil(self.registration.showNotification(title, options));
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
