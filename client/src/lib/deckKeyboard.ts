// Layout and sizing of the deck keyboard: an on-screen full keyboard for iPads
// without a physical keyboard. It replaces the iOS soft keyboard, which has no
// Esc/Ctrl/Alt/Tab/arrows and hides symbols behind extra pages.

/** A modifier key. Tap once: applies to the next key; twice: locked; three times: off. */
export type Modifier = 'shift' | 'ctrl' | 'alt' | 'fn';

export interface DeckKey {
  id: string;
  /** Width in row units; every row adds up to ROW_UNITS. */
  w: number;
  /** Printable key: what it types, unshifted and shifted. Letters also type Hangul. */
  ch?: string;
  shiftCh?: string;
  /** Named key (KeyboardEvent.key name) sent through UnifiedInput.pressKey. */
  key?: string;
  label?: string;
  /** Under Fn: a different named key and its label (F1–F12, PgUp, Home, …). */
  fnKey?: string;
  fnLabel?: string;
  mod?: Modifier;
  /** The 한/영 key. */
  lang?: boolean;
  /** Repeats while held (Backspace, arrows, space). */
  repeat?: boolean;
}


const chars = (pairs: string, fnKeys: string[] = []): DeckKey[] =>
  pairs.split(' ').map((p, i) => ({
    id: `c${p[0] === ' ' ? 'space' : p[0]}`, w: 1, ch: p[0], shiftCh: p[1] ?? p[0].toUpperCase(),
    ...(fnKeys[i] ? { fnKey: fnKeys[i], fnLabel: fnKeys[i] } : {}),
  }));

const F = Array.from({ length: 12 }, (_, i) => `F${i + 1}`);

/** Landscape (and 13" portrait): a desktop-like layout, 15 units a row. */
const WIDE_ROWS: DeckKey[][] = [
  [
    { id: 'esc', w: 1, key: 'Escape', label: 'esc' },
    ...chars('`~'),
    ...chars('1! 2@ 3# 4$ 5% 6^ 7& 8* 9( 0) -_ =+', F),
    { id: 'bksp', w: 1, key: 'Backspace', label: '⌫', fnKey: 'Delete', fnLabel: 'del', repeat: true },
  ],
  [
    { id: 'tab', w: 1.5, key: 'Tab', label: 'tab' },
    ...chars('q w e r t y u i o p [{ ]}'),
    { id: 'c\\', w: 1.5, ch: '\\', shiftCh: '|' },
  ],
  [
    { id: 'ctrl', w: 1.75, mod: 'ctrl', label: 'ctrl' },
    ...chars('a s d f g h j k l ;: \'"'),
    { id: 'enter', w: 2.25, key: 'Enter', label: 'return' },
  ],
  [
    // ↑ sits right above ↓, so the arrows form the usual inverted T.
    { id: 'shiftL', w: 2, mod: 'shift', label: '⇧' },
    ...chars('z x c v b n m ,< .> /?'),
    { id: 'shiftR', w: 1, mod: 'shift', label: '⇧' },
    { id: 'up', w: 1, key: 'ArrowUp', label: '↑', fnKey: 'PageUp', fnLabel: 'pgup', repeat: true },
    { id: 'del', w: 1, key: 'Delete', label: '⌦', fnKey: 'Insert', fnLabel: 'ins', repeat: true },
  ],
  [
    { id: 'lang', w: 1.5, lang: true, label: '한/영' },
    { id: 'fn', w: 1.25, mod: 'fn', label: 'fn' },
    { id: 'alt', w: 1.25, mod: 'alt', label: 'alt' },
    { id: 'space', w: 8, ch: ' ', shiftCh: ' ', label: '', repeat: true },
    { id: 'left', w: 1, key: 'ArrowLeft', label: '←', fnKey: 'Home', fnLabel: 'home', repeat: true },
    { id: 'down', w: 1, key: 'ArrowDown', label: '↓', fnKey: 'PageDown', fnLabel: 'pgdn', repeat: true },
    { id: 'right', w: 1, key: 'ArrowRight', label: '→', fnKey: 'End', fnLabel: 'end', repeat: true },
  ],
];

/** Portrait on narrower iPads: 12 units a row so keys stay as wide as a finger
 *  needs, with the symbols the letter rows give up moved to a sixth row (a
 *  portrait screen has the height to spare). */
const NARROW_ROWS: DeckKey[][] = [
  chars('1! 2@ 3# 4$ 5% 6^ 7& 8* 9( 0) -_ =+', F),
  [
    { id: 'tab', w: 1, key: 'Tab', label: 'tab' },
    ...chars('q w e r t y u i o p'),
    { id: 'bksp', w: 1, key: 'Backspace', label: '⌫', fnKey: 'Delete', fnLabel: 'del', repeat: true },
  ],
  [
    { id: 'ctrl', w: 1.5, mod: 'ctrl', label: 'ctrl' },
    ...chars('a s d f g h j k l'),
    { id: 'enter', w: 1.5, key: 'Enter', label: 'return' },
  ],
  [
    { id: 'shiftL', w: 1.5, mod: 'shift', label: '⇧' },
    ...chars('z x c v b n m ,< .>'),
    { id: 'shiftR', w: 1.5, mod: 'shift', label: '⇧' },
  ],
  [
    { id: 'esc', w: 1.5, key: 'Escape', label: 'esc' },
    ...chars('`~ [{ ]} \\| ;: \'" /?'),
    { id: 'alt', w: 1.5, mod: 'alt', label: 'alt' },
    { id: 'up', w: 1, key: 'ArrowUp', label: '↑', fnKey: 'PageUp', fnLabel: 'pgup', repeat: true },
    { id: 'del', w: 1, key: 'Delete', label: '⌦', fnKey: 'Insert', fnLabel: 'ins', repeat: true },
  ],
  [
    { id: 'lang', w: 1.5, lang: true, label: '한/영' },
    { id: 'fn', w: 1.5, mod: 'fn', label: 'fn' },
    { id: 'space', w: 6, ch: ' ', shiftCh: ' ', label: '', repeat: true },
    { id: 'left', w: 1, key: 'ArrowLeft', label: '←', fnKey: 'Home', fnLabel: 'home', repeat: true },
    { id: 'down', w: 1, key: 'ArrowDown', label: '↓', fnKey: 'PageDown', fnLabel: 'pgdn', repeat: true },
    { id: 'right', w: 1, key: 'ArrowRight', label: '→', fnKey: 'End', fnLabel: 'end', repeat: true },
  ],
];

export interface DeckLayout {
  name: 'wide' | 'narrow';
  /** Width of every row in key units. */
  units: number;
  rows: DeckKey[][];
}

export const WIDE_LAYOUT: DeckLayout = { name: 'wide', units: 15, rows: WIDE_ROWS };
export const NARROW_LAYOUT: DeckLayout = { name: 'narrow', units: 12, rows: NARROW_ROWS };

/** Below this width 15 keys a row are narrower than a fingertip, so the
 *  12-unit layout is used (iPad mini / 11" / Air portrait). */
const WIDE_MIN_WIDTH = 1000;

export function deckLayout(width: number): DeckLayout {
  return width >= WIDE_MIN_WIDTH ? WIDE_LAYOUT : NARROW_LAYOUT;
}

/** Sizes for one screen. Derived from the visible size and orientation, not the
 *  device model, so every iPad (mini, 11", 13"), Split View and Stage Manager
 *  window gets a fitting keyboard. */
export interface DeckMetrics {
  keyHeight: number;
  gap: number;
  fontSize: number;
  /** Side padding of the keyboard. */
  padX: number;
  /** The strip above the keys (paste, copy, keyboard switch, hide). */
  stripHeight: number;
}

const clamp = (v: number, lo: number, hi: number) => Math.min(hi, Math.max(lo, v));

export function deckMetrics(width: number, height: number): DeckMetrics {
  const landscape = width > height;
  // Apple's minimum touch target is 44pt; keys grow from there with the
  // screen. Landscape gets a larger share of a shorter height so its five rows
  // are still finger-sized; portrait's six narrow rows take a smaller share of
  // a taller one.
  const keyHeight = Math.round(clamp(height * (landscape ? 0.066 : 0.045), 46, 62));
  const wide = width >= WIDE_MIN_WIDTH;
  return {
    keyHeight,
    // A wider gap misses fewer neighbouring keys; the gap is part of the target.
    gap: wide ? 8 : 6,
    fontSize: Math.round(clamp(keyHeight * 0.4, 17, 23)),
    padX: wide ? 10 : 5,
    stripHeight: landscape ? 34 : 38,
  };
}

/** The deck keyboard is offered on touch screens whose shorter side is at least
 *  that of an iPad mini (744 CSS px); a phone is too narrow for a full keyboard. */
export function deckKeyboardFits(width: number, height: number): boolean {
  return Math.min(width, height) >= 600;
}
