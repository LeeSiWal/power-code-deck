import type { TerminalCore, CellData } from '@wterm/core';

const ZWSP = 0x200b;

/**
 * East Asian Wide / Fullwidth code points — the ones a terminal renders across
 * two cells. Enough coverage for CJK terminal content (Hangul, Kana, Han, …).
 */
function isWide(cp: number): boolean {
  return (
    (cp >= 0x1100 && cp <= 0x115f) || // Hangul Jamo
    (cp >= 0x2e80 && cp <= 0x303e) || // CJK radicals · Kangxi · CJK symbols
    (cp >= 0x3041 && cp <= 0x33ff) || // Kana · CJK misc
    (cp >= 0x3400 && cp <= 0x4dbf) || // CJK Ext A
    (cp >= 0x4e00 && cp <= 0x9fff) || // CJK Unified
    (cp >= 0xa000 && cp <= 0xa4cf) || // Yi
    (cp >= 0xac00 && cp <= 0xd7a3) || // Hangul Syllables
    (cp >= 0xf900 && cp <= 0xfaff) || // CJK Compatibility
    (cp >= 0xfe30 && cp <= 0xfe4f) || // CJK Compatibility Forms
    (cp >= 0xff00 && cp <= 0xff60) || // Fullwidth Forms
    (cp >= 0xffe0 && cp <= 0xffe6) ||
    (cp >= 0x20000 && cp <= 0x3fffd)  // CJK Ext B+
  );
}

/**
 * Wrap a TerminalCore so wterm's DOM renderer draws wide (CJK) characters across
 * BOTH of their cells. A wide char occupies two cells; ghostty marks the second
 * as a space, and the renderer would draw that space — adding a full cell of
 * width per CJK char (→ "가 나 다" with gaps + line overflow). We turn the
 * continuation space into a zero-width space so the wide glyph itself fills the
 * pair. A continuation cell is a space whose immediately preceding cell is wide.
 */
/**
 * @param forceDirty optional gate: when it returns true the renderer redraws every
 * row (clears stale colored-cell backgrounds); when false it defers to the core's
 * real dirty tracking. Callers pass `() => noActiveSelection` so a held selection's
 * DOM isn't replaced each frame (which orphans its highlight paint on Safari — the
 * "커서 잔상").
 */
export function wideAwareCore(base: TerminalCore, forceDirty?: () => boolean): TerminalCore {
  const fix = (cell: CellData, prev: CellData | null): CellData =>
    cell.char === 32 && prev !== null && isWide(prev.char) ? { ...cell, char: ZWSP } : cell;

  return new Proxy(base, {
    get(target, prop, receiver) {
      if (prop === 'getCell') {
        return (row: number, col: number) =>
          fix(target.getCell(row, col), col > 0 ? target.getCell(row, col - 1) : null);
      }
      if (prop === 'getScrollbackCell') {
        return (offset: number, col: number) =>
          fix(target.getScrollbackCell(offset, col), col > 0 ? target.getScrollbackCell(offset, col - 1) : null);
      }
      // Force every visible row to re-render each frame so stale colored cell
      // backgrounds can't linger (wterm's dirty-row optimization misses some
      // background-only changes during CJK redraws). But NOT while a selection is
      // held — re-rendering under it orphans the highlight paint on Safari — then
      // defer to the core's real dirty tracking.
      if (prop === 'isDirtyRow') {
        return (row: number) => (!forceDirty || forceDirty()) ? true : target.isDirtyRow(row);
      }
      const value = Reflect.get(target, prop, receiver);
      return typeof value === 'function' ? value.bind(target) : value;
    },
  }) as TerminalCore;
}
