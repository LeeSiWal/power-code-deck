// Framework-free assertions (type-checked by tsc like the other *.test.ts files).
import { DECK_ROWS, ROW_UNITS, deckKeyboardFits, deckMetrics } from './deckKeyboard';
import { jamoFor } from './hangul';

function equal(actual: unknown, expected: unknown, label: string) {
  if (actual !== expected) throw new Error(`${label}: expected ${String(expected)}, got ${String(actual)}`);
}

// Every row fills the keyboard width.
DECK_ROWS.forEach((row, i) => equal(row.reduce((n, k) => n + k.w, 0), ROW_UNITS, `row ${i} width`));

const keys = DECK_ROWS.flat();
equal(new Set(keys.map((k) => k.id)).size, keys.length, 'key ids are unique');

// A full keyboard: every letter and digit, every ASCII symbol (shifted or not),
// F1–F12 under Fn, and the keys a terminal needs.
const typed = new Set(keys.flatMap((k) => [k.ch, k.shiftCh]).filter(Boolean));
for (const c of 'abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789') equal(typed.has(c), true, `types ${c}`);
for (const c of '`~!@#$%^&*()-_=+[]{}\\|;:\'",.<>/? ') equal(typed.has(c), true, `types ${JSON.stringify(c)}`);
const named = new Set(keys.flatMap((k) => [k.key, k.fnKey]).filter(Boolean));
for (let i = 1; i <= 12; i++) equal(named.has(`F${i}`), true, `F${i} under fn`);
for (const n of ['Escape', 'Tab', 'Enter', 'Backspace', 'Delete', 'ArrowUp', 'ArrowDown', 'ArrowLeft', 'ArrowRight', 'Home', 'End', 'PageUp', 'PageDown'])
  equal(named.has(n), true, `has ${n}`);
for (const m of ['shift', 'ctrl', 'alt', 'fn']) equal(keys.some((k) => k.mod === m), true, `has ${m}`);
equal(keys.some((k) => k.lang), true, 'has 한/영');

// ↑ sits exactly above ↓ (the inverted T of a hardware keyboard).
const offset = (id: string) => {
  for (const row of DECK_ROWS) {
    let x = 0;
    for (const k of row) { if (k.id === id) return x; x += k.w; }
  }
  return -1;
};
equal(offset('up'), offset('down'), '↑ is above ↓');

// Every Hangul jamo of the two-set layout sits on a letter key.
const letterKeys = keys.filter((k) => k.ch && /[a-z]/.test(k.ch));
const jamo = new Set(letterKeys.flatMap((k) => [jamoFor(k.ch!, false), jamoFor(k.ch!, true)]));
for (const j of 'ㄱㄲㄴㄷㄸㄹㅁㅂㅃㅅㅆㅇㅈㅉㅊㅋㅌㅍㅎㅏㅐㅑㅒㅓㅔㅕㅖㅗㅛㅜㅠㅡㅣ') equal(jamo.has(j), true, `types ${j}`);

// Sizes per iPad and orientation (CSS px of the visible screen). Keys stay
// tappable everywhere, and the keyboard never takes half of the screen.
const screens: [string, number, number][] = [
  ['mini portrait', 744, 1133], ['mini landscape', 1133, 744],
  ['11" portrait', 834, 1194], ['11" landscape', 1194, 834],
  ['13" portrait', 1024, 1366], ['13" landscape', 1366, 1024],
  ['split view half', 678, 1024],
];
for (const [name, w, h] of screens) {
  const m = deckMetrics(w, h);
  const total = m.stripHeight + DECK_ROWS.length * m.keyHeight + (DECK_ROWS.length + 1) * m.gap;
  equal(m.keyHeight >= 40, true, `${name}: key height ${m.keyHeight}`);
  equal(total < h * 0.45, true, `${name}: keyboard ${total}px of ${h}`);
  equal((w - 2 * m.padX) / ROW_UNITS >= 40, true, `${name}: a 1-unit key is at least 40px wide`);
  equal(deckKeyboardFits(w, h), true, `${name}: offered`);
}
// Landscape keys are smaller than portrait keys on the same iPad.
equal(deckMetrics(1133, 744).keyHeight < deckMetrics(744, 1133).keyHeight, true, 'mini: landscape keys are shorter');
// Phones are not offered the deck keyboard.
equal(deckKeyboardFits(390, 844), false, 'iPhone portrait');
equal(deckKeyboardFits(844, 390), false, 'iPhone landscape');
