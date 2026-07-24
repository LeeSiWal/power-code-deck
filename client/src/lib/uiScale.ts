/**
 * Fluid UI scaling — makes the whole interface grow on large displays and shrink on
 * small ones, proportional to the viewport.
 *
 * How it works: Tailwind's spacing, font-size and width utilities are all rem-based,
 * so the entire chrome (text, padding, panels, buttons) scales together when the root
 * `html` font-size changes. We set that font-size from the viewport size, clamped so
 * it never gets comically large on a 4K panel or unreadably small on a tiny window.
 *
 * Layout structure does NOT move: Tailwind's responsive breakpoints (`md:`, `lg:`)
 * are px-based, so which layout renders is unchanged — only the sizing within it
 * scales. A few things opt out by design and keep their own sizing: the terminal font
 * (its own setting), user-dragged panel widths (stored in px), and hairline borders.
 *
 * Opt out for debugging with `?noscale` in the URL.
 */

// Reference viewport the design is tuned at → BASE_FONT_PX. A viewport this size
// renders at exactly the browser-default 16px; larger scales up, smaller scales down.
const BASE_W = 1440;
const BASE_H = 900;
const BASE_FONT_PX = 16;

// Multiplier bounds keep the extremes sane: ~0.85× on small windows, ~1.4× on big
// displays. Widen these to make scaling more aggressive.
const MIN_SCALE = 0.85;
const MAX_SCALE = 1.4;

// Phones are left at 1.0×: the mobile layout is already responsive and its touch
// targets are sized for the default 16px, so scaling them down hurts tap accuracy
// more than it helps. Lower this (e.g. to 0) to let phones scale too.
const MOBILE_MIN_W = 640;

function computeFontPx(): number {
  const w = window.innerWidth || BASE_W;
  const h = window.innerHeight || BASE_H;
  if (w < MOBILE_MIN_W) return BASE_FONT_PX;
  // Drive off whichever axis is more constrained, so a wide-but-short window scales
  // by its height and never overflows vertically (and vice-versa).
  const raw = Math.min(w / BASE_W, h / BASE_H);
  const scale = Math.max(MIN_SCALE, Math.min(MAX_SCALE, raw));
  return BASE_FONT_PX * scale;
}

let raf = 0;
function apply() {
  raf = 0;
  document.documentElement.style.fontSize = `${computeFontPx().toFixed(2)}px`;
}

function schedule() {
  if (raf) return;
  raf = requestAnimationFrame(apply);
}

/** Start fluid scaling. Idempotent-safe to call once at startup. */
export function initUiScale(): void {
  if (new URLSearchParams(location.search).has('noscale')) return;
  apply();
  window.addEventListener('resize', schedule);
  window.visualViewport?.addEventListener('resize', schedule);
}
