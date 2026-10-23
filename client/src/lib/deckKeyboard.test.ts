// Framework-free assertions (type-checked by tsc like the other *.test.ts files).
import { NARROW_LAYOUT, WIDE_LAYOUT, deckKeyboardFits, deckLayout, deckMetrics } from './deckKeyboard';
import { jamoFor } from './hangul';

function equal(actual: unknown, expected: unknown, label: string) {
  if (actual !== expected) throw new Error(`${label}: expected ${String(expected)}, got ${String(actual)}`);
}

for (const layout of [WIDE_LAYOUT, NARROW_LAYOUT]) {
  const at = (what: string) => `${layout.name}: ${what}`;
  // Every row fills the keyboard width.
  layout.rows.forEach((row, i) => equal(row.reduce((n, k) => n + k.w, 0), layout.units, at(`row ${i} width`)));

  const keys = layout.rows.flat();
  equal(new Set(keys.map((k) => k.id)).size, keys.length, at('key ids are unique'));

  // A full keyboard: every letter and digit, every ASCII symbol (shifted or not),
  // F1–F12 under Fn, and the keys a terminal needs.
  const typed = new Set(keys.flatMap((k) => [k.ch, k.shiftCh]).filter(Boolean));
  for (const c of 'abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789') equal(typed.has(c), true, at(`types ${c}`));
  for (const c of '`~!@#$%^&*()-_=+[]{}\\|;:\'",.<>/? ') equal(typed.has(c), true, at(`types ${JSON.stringify(c)}`));
  const named = new Set(keys.flatMap((k) => [k.key, k.fnKey]).filter(Boolean));
  for (let i = 1; i <= 12; i++) equal(named.has(`F${i}`), true, at(`F${i} under fn`));
  for (const n of ['Escape', 'Tab', 'Enter', 'Backspace', 'Delete', 'Insert', 'ArrowUp', 'ArrowDown', 'ArrowLeft', 'ArrowRight', 'Home', 'End', 'PageUp', 'PageDown'])
    equal(named.has(n), true, at(`has ${n}`));
  for (const m of ['shift', 'ctrl', 'alt', 'fn']) equal(keys.some((k) => k.mod === m), true, at(`has ${m}`));
  equal(keys.some((k) => k.lang), true, at('has 한/영'));

  // ↑ sits exactly above ↓ (the inverted T of a hardware keyboard).
  const offset = (id: string) => {
    for (const row of layout.rows) {
      let x = 0;
      for (const k of row) { if (k.id === id) return x; x += k.w; }
    }
    return -1;
  };
  equal(offset('up'), offset('down'), at('↑ is above ↓'));

  // Every Hangul jamo of the two-set layout sits on a letter key.
  const letterKeys = keys.filter((k) => k.ch && /[a-z]/.test(k.ch));
  const jamo = new Set(letterKeys.flatMap((k) => [jamoFor(k.ch!, false), jamoFor(k.ch!, true)]));
  for (const j of 'ㄱㄲㄴㄷㄸㄹㅁㅂㅃㅅㅆㅇㅈㅉㅊㅋㅌㅍㅎㅏㅐㅑㅒㅓㅔㅕㅖㅗㅛㅜㅠㅡㅣ') equal(jamo.has(j), true, at(`types ${j}`));
}

// Every iPad, both orientations (CSS px of the visible screen), plus Split View.
// Keys are finger-sized everywhere — on a full screen at least 55px wide (a
// 1-unit key, gaps taken out; the old 15-key rows gave 44px on an iPad mini in
// portrait), in a Split View half 48px — and at least 46px tall, above Apple's
// 44pt minimum target. The keyboard leaves more than half of the screen.
const screens: [string, number, number, number][] = [
  ['mini portrait', 744, 1133, 55], ['mini landscape', 1133, 744, 55],
  ['iPad / Air portrait', 820, 1180, 55], ['iPad / Air landscape', 1180, 820, 55],
  ['11" portrait', 834, 1194, 55], ['11" landscape', 1194, 834, 55],
  ['13" portrait', 1024, 1366, 55], ['13" landscape', 1366, 1024, 55],
  ['split view half', 678, 1024, 48],
];
for (const [name, w, h, minKeyWidth] of screens) {
  const m = deckMetrics(w, h);
  const layout = deckLayout(w);
  const rows = layout.rows.length;
  const keyWidth = (w - 2 * m.padX - (layout.units - 1) * m.gap) / layout.units;
  const total = m.stripHeight + rows * m.keyHeight + (rows + 1) * m.gap;
  equal(keyWidth >= minKeyWidth, true, `${name}: key width ${keyWidth.toFixed(1)}px (${layout.name})`);
  equal(m.keyHeight >= 46, true, `${name}: key height ${m.keyHeight}`);
  equal(total < h * 0.47, true, `${name}: keyboard ${total}px of ${h}`);
  equal(deckKeyboardFits(w, h), true, `${name}: offered`);
}
equal(deckLayout(744).name, 'narrow', 'mini portrait uses the 12-unit layout');
equal(deckLayout(1133).name, 'wide', 'landscape uses the full 15-unit layout');
// Phones are not offered the deck keyboard.
equal(deckKeyboardFits(390, 844), false, 'iPhone portrait');
equal(deckKeyboardFits(844, 390), false, 'iPhone landscape');
