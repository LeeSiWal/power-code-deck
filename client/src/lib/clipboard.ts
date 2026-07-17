// Clipboard helpers that work in both secure (https / localhost) and non-secure
// (LAN http) contexts. Copy falls back to a synchronous execCommand path; read
// needs the async Clipboard API (only available in a secure context) since there
// is no reliable execCommand('paste').

/**
 * Synchronous clipboard copy via a hidden textarea + execCommand. Works within a
 * user gesture even in non-secure (http) contexts — e.g. a LAN URL like
 * http://192.168.x.x:5553 — where navigator.clipboard is unavailable or rejects.
 * Must run synchronously inside the gesture (no preceding await).
 */
export function legacyCopy(text: string): boolean {
  try {
    const ta = document.createElement('textarea');
    ta.value = text;
    ta.setAttribute('readonly', ''); // avoid popping the mobile keyboard
    ta.style.position = 'fixed';
    ta.style.top = '0';
    ta.style.left = '0';
    ta.style.width = '1px';
    ta.style.height = '1px';
    ta.style.opacity = '0';
    document.body.appendChild(ta);
    ta.focus();
    ta.select();
    ta.setSelectionRange(0, text.length); // iOS Safari needs an explicit range
    const ok = document.execCommand('copy');
    document.body.removeChild(ta);
    return ok;
  } catch {
    return false;
  }
}

/**
 * Copy text to the clipboard. Secure context (https / localhost) → async Clipboard
 * API. Non-secure (LAN http) → straight to the synchronous execCommand path
 * (awaiting the rejecting promise first would spend the user gesture).
 */
export async function writeClipboard(text: string): Promise<boolean> {
  if (!text) return false;
  if (window.isSecureContext && navigator.clipboard?.writeText) {
    try {
      await navigator.clipboard.writeText(text);
      return true;
    } catch {
      /* fall through */
    }
  }
  return legacyCopy(text);
}

/**
 * Read text from the clipboard. Only works in a secure context (the prod domain
 * is https, so this is fine on mobile) and within a user gesture. Returns '' when
 * unavailable or denied — there is no execCommand('paste') fallback.
 */
export async function readClipboard(): Promise<string> {
  if (navigator.clipboard?.readText) {
    try {
      return await navigator.clipboard.readText();
    } catch {
      /* denied or insecure context */
    }
  }
  return '';
}
