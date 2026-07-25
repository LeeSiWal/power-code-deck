// A stable per-browser id, persisted in localStorage. It ties this device's
// WebSocket connection and its push subscription together on the server, so the
// deck can make a session exclusive to one device and aim push notifications at only
// the device you're actually using. Not an account or a fingerprint — just a random
// token that survives reloads (and is regenerated if storage is cleared).

const KEY = 'pcd:deviceId';

let cached = '';

export function getDeviceId(): string {
  if (cached) return cached;
  try {
    const existing = localStorage.getItem(KEY);
    if (existing) {
      cached = existing;
      return cached;
    }
  } catch {
    /* storage disabled — fall through to an ephemeral id for this page load */
  }
  cached =
    typeof crypto !== 'undefined' && 'randomUUID' in crypto
      ? crypto.randomUUID()
      : 'dev-' + Math.random().toString(36).slice(2) + Date.now().toString(36);
  try {
    localStorage.setItem(KEY, cached);
  } catch {
    /* ignore — cached holds it for this session at least */
  }
  return cached;
}
