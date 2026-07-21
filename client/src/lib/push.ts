/**
 * Web Push subscription lifecycle on the client.
 *
 * The whole cross-platform story lives in one constraint: iOS/iPadOS only exposes
 * PushManager to an INSTALLED (home-screen) PWA — never a plain Safari tab. So the
 * UI has to detect that case and tell the user to "Add to Home Screen" rather than
 * showing a toggle that can't possibly work. Everywhere else (Android, desktop
 * Chrome/Edge/Firefox, macOS Safari) push works in the browser directly.
 */

import { api } from './api';

export type PushState = 'unsupported' | 'needs-install' | 'denied' | 'on' | 'off';

export function pushSupported(): boolean {
  return 'serviceWorker' in navigator && 'PushManager' in window && 'Notification' in window;
}

// True on an iOS/iPadOS Safari TAB (not yet installed), where PushManager is absent
// until the user adds the app to the Home Screen.
export function iosNeedsInstall(): boolean {
  const ua = navigator.userAgent;
  const isIOS =
    /iPhone|iPad|iPod/.test(ua) ||
    // iPadOS 13+ reports as MacIntel but has a touch screen.
    (navigator.platform === 'MacIntel' && navigator.maxTouchPoints > 1);
  const standalone =
    (navigator as unknown as { standalone?: boolean }).standalone === true ||
    window.matchMedia('(display-mode: standalone)').matches;
  return isIOS && !standalone && !('PushManager' in window);
}

// The subscription MUST bind to the worker that actually carries the 'push'
// handler. That worker is /sw.js (registered on every load in main.tsx and
// controlling scope '/'); it also holds the app-shell cache. Registering it here
// is idempotent for the same script+scope, so we always end up subscribing on the
// one worker that will display the notification — never a second, handler-less one.
async function registerSW(): Promise<ServiceWorkerRegistration> {
  return navigator.serviceWorker.register('/sw.js');
}

// VAPID keys travel as base64url; PushManager wants raw bytes. Backed by an explicit
// ArrayBuffer so the type is BufferSource-compatible (not a SharedArrayBuffer view).
function urlBase64ToUint8Array(base64: string): Uint8Array<ArrayBuffer> {
  const padding = '='.repeat((4 - (base64.length % 4)) % 4);
  const b64 = (base64 + padding).replace(/-/g, '+').replace(/_/g, '/');
  const raw = atob(b64);
  const out = new Uint8Array(new ArrayBuffer(raw.length));
  for (let i = 0; i < raw.length; i++) out[i] = raw.charCodeAt(i);
  return out;
}

export async function pushState(): Promise<PushState> {
  if (iosNeedsInstall()) return 'needs-install';
  if (!pushSupported()) return 'unsupported';
  if (Notification.permission === 'denied') return 'denied';
  try {
    const reg = await navigator.serviceWorker.getRegistration();
    const sub = reg ? await reg.pushManager.getSubscription() : null;
    return sub ? 'on' : 'off';
  } catch {
    return 'off';
  }
}

// Must be called from a user gesture (permission prompt requires one).
export async function enablePush(): Promise<void> {
  if (!pushSupported()) throw new Error('이 브라우저는 웹 푸시를 지원하지 않습니다');
  const perm = await Notification.requestPermission();
  if (perm !== 'granted') throw new Error('알림 권한이 허용되지 않았습니다');
  const { enabled, publicKey } = await api.pushVapidKey();
  if (!enabled || !publicKey) throw new Error('서버에서 푸시가 비활성화되어 있습니다');
  const reg = await registerSW();
  await navigator.serviceWorker.ready;
  const existing = await reg.pushManager.getSubscription();
  const sub =
    existing ??
    (await reg.pushManager.subscribe({
      userVisibleOnly: true,
      applicationServerKey: urlBase64ToUint8Array(publicKey),
    }));
  await api.pushSubscribe(sub.toJSON() as PushSubscriptionJSON);
}

export async function disablePush(): Promise<void> {
  const reg = await navigator.serviceWorker.getRegistration();
  const sub = reg ? await reg.pushManager.getSubscription() : null;
  if (!sub) return;
  try {
    await api.pushUnsubscribe(sub.endpoint);
  } catch {
    /* server may already have pruned it — unsubscribe locally regardless */
  }
  await sub.unsubscribe();
}
