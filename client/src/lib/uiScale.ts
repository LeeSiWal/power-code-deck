/**
 * Fluid UI scaling — makes the whole interface grow on large displays / shrink on
 * small ones, plus a manual size preference from Settings.
 *
 * We drive the CSS `zoom` property (via the `--ui-zoom` custom property consumed by
 * `#root` in globals.css) rather than the root font-size. `zoom` scales the entire
 * app UNIFORMLY — fixed-px sizing included — so components that use arbitrary px
 * (e.g. the control room's `text-[10px]`) scale too, which a rem/font-size approach
 * can't reach. Layout reflows properly, so it reads as a real page zoom, not a blur.
 *
 * Final zoom = autoFactor(viewport) × userScale(setting). The app shell divides its
 * height/width by the zoom (globals.css) so a scaled-up UI still fits one viewport.
 *
 * `?noscale` in the URL disables the automatic (viewport) part; the manual setting
 * still applies.
 */
import { useEffect, useState } from 'react';

// Reference width the design is tuned at → zoom 1.0. Wider viewports scale up,
// narrower scale down (damped, so it's proportional but not 1:1 aggressive).
const REF_W = 1440;
const DAMP = 0.4;
// Never auto-SHRINK below the tuned baseline: screens at or below the reference stay
// at 1.0 (identical to no scaling), and only larger displays grow. Shrinking is left
// to the manual Settings control, so nobody's UI gets unexpectedly smaller.
const AUTO_MIN = 1.0;
const AUTO_MAX = 1.35;

// Phones keep autoFactor 1.0: the mobile layout is already responsive and its touch
// targets are tuned for the default size. The manual setting still applies.
const MOBILE_MIN_W = 640;

// Manual setting bounds (Settings slider). Exported for the slider's min/max.
export const USER_SCALE_MIN = 0.7;
export const USER_SCALE_MAX = 1.6;

// Absolute clamp on the combined result, so auto × manual can't reach an unusable
// extreme.
const HARD_MIN = 0.7;
const HARD_MAX = 1.9;

function clamp(n: number, lo: number, hi: number): number {
  return Math.max(lo, Math.min(hi, n));
}

let autoDisabled = false;
let userScale = clamp(parseFloat(localStorage.getItem('uiScale') || '1') || 1, USER_SCALE_MIN, USER_SCALE_MAX);

function autoFactor(): number {
  if (autoDisabled) return 1;
  const w = window.innerWidth || REF_W;
  if (w < MOBILE_MIN_W) return 1;
  return clamp(1 + (w / REF_W - 1) * DAMP, AUTO_MIN, AUTO_MAX);
}

/** The zoom currently applied to the app (auto × manual, clamped). */
export function currentZoom(): number {
  return clamp(autoFactor() * userScale, HARD_MIN, HARD_MAX);
}

let raf = 0;
function apply() {
  raf = 0;
  document.documentElement.style.setProperty('--ui-zoom', currentZoom().toFixed(4));
  window.dispatchEvent(new Event('ui-zoom'));
}
function schedule() {
  if (!raf) raf = requestAnimationFrame(apply);
}

export function getUserScale(): number {
  return userScale;
}
export function setUserScale(v: number): void {
  userScale = clamp(v, USER_SCALE_MIN, USER_SCALE_MAX);
  localStorage.setItem('uiScale', String(userScale));
  apply();
}

/** Start fluid scaling. Called once at startup before first paint. */
export function initUiScale(): void {
  autoDisabled = new URLSearchParams(location.search).has('noscale');
  apply();
  window.addEventListener('resize', schedule);
  window.visualViewport?.addEventListener('resize', schedule);
}

/** React hook: the current zoom, re-read on viewport or setting change. */
export function useUiZoom(): number {
  const [z, setZ] = useState(() => currentZoom());
  useEffect(() => {
    const on = () => setZ(currentZoom());
    window.addEventListener('ui-zoom', on);
    window.addEventListener('resize', on);
    window.visualViewport?.addEventListener('resize', on);
    return () => {
      window.removeEventListener('ui-zoom', on);
      window.removeEventListener('resize', on);
      window.visualViewport?.removeEventListener('resize', on);
    };
  }, []);
  return z;
}
