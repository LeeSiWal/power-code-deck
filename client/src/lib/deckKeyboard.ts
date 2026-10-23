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

export const ROW_UNITS = 15;

const chars = (pairs: string, fnKeys: string[] = []): DeckKey[] =>
  pairs.split(' ').map((p, i) => ({
    id: `c${p[0] === ' ' ? 'space' : p[0]}`, w: 1, ch: p[0], shiftCh: p[1] ?? p[0].toUpperCase(),
    ...(fnKeys[i] ? { fnKey: fnKeys[i], fnLabel: fnKeys[i] } : {}),
  }));

const F = Array.from({ length: 12 }, (_, i) => `F${i + 1}`);

export const DECK_ROWS: DeckKey[][] = [
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
    { id: 'shiftL', w: 2.25, mod: 'shift', label: '⇧' },
    ...chars('z x c v b n m ,< .> /?'),
    { id: 'up', w: 1, key: 'ArrowUp', label: '↑', fnKey: 'PageUp', fnLabel: 'pgup', repeat: true },
    { id: 'shiftR', w: 1.75, mod: 'shift', label: '⇧' },
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
  // Landscape leaves little height for the terminal, so keys take a smaller
  // share of it; portrait keys grow with the taller screen. 40px keeps a key
  // tappable (Apple's minimum target is 44pt including the gap).
  const keyHeight = Math.round(clamp(height * (landscape ? 0.06 : 0.047), 40, 58));
  const wide = width >= 1000;
  return {
    keyHeight,
    gap: wide ? 7 : 5,
    fontSize: Math.round(clamp(keyHeight * 0.36, 14, 20)),
    padX: wide ? 10 : 4,
    stripHeight: landscape ? 30 : 34,
  };
}

/** The deck keyboard is offered on touch screens whose shorter side is at least
 *  that of an iPad mini (744 CSS px); a phone is too narrow for 15 keys a row. */
export function deckKeyboardFits(width: number, height: number): boolean {
  return Math.min(width, height) >= 600;
}
