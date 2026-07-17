import type { IBufferLine, IBufferCell } from '@xterm/headless';

// Render one xterm buffer line into a DOM row. We read xterm's parsed grid (the
// same engine VS Code's terminal uses — so it matches how Claude Code renders
// there, no stale-attribute "잔상"), and draw it as plain spans. No box-shadow,
// no compositing hints.

function paletteCSS(idx: number): string {
  if (idx < 16) return `var(--term-color-${idx})`;
  if (idx < 232) {
    const n = idx - 16;
    return `rgb(${Math.floor(n / 36) * 51},${(Math.floor(n / 6) % 6) * 51},${(n % 6) * 51})`;
  }
  const level = (idx - 232) * 10 + 8;
  return `rgb(${level},${level},${level})`;
}
function rgbCSS(v: number): string {
  return `rgb(${(v >> 16) & 0xff},${(v >> 8) & 0xff},${v & 0xff})`;
}
function fgOf(c: IBufferCell): string | null {
  if (c.isFgDefault()) return null;
  if (c.isFgRGB()) return rgbCSS(c.getFgColor());
  return paletteCSS(c.getFgColor());
}
function bgOf(c: IBufferCell): string | null {
  if (c.isBgDefault()) return null;
  if (c.isBgRGB()) return rgbCSS(c.getBgColor());
  return paletteCSS(c.getBgColor());
}

/** Style string for a cell (fg/bg already inverse-resolved), coalesced into runs. */
function cellStyle(c: IBufferCell): string {
  let fg = fgOf(c);
  let bg = bgOf(c);
  if (c.isInverse()) {
    const nf = bg ?? 'var(--term-bg)';
    const nb = fg ?? 'var(--term-fg)';
    fg = nf; bg = nb;
  }
  let s = '';
  if (fg) s += `color:${fg};`;
  if (bg) s += `background:${bg};`;
  if (c.isBold()) s += 'font-weight:bold;';
  if (c.isDim()) s += 'opacity:0.5;';
  if (c.isItalic()) s += 'font-style:italic;';
  if (c.isUnderline() && c.isStrikethrough && c.isStrikethrough()) s += 'text-decoration:underline line-through;';
  else if (c.isUnderline()) s += 'text-decoration:underline;';
  else if (c.isStrikethrough && c.isStrikethrough()) s += 'text-decoration:line-through;';
  if (c.isInvisible && c.isInvisible()) s += 'visibility:hidden;';
  return s;
}

function esc(t: string): string {
  return t.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;');
}

// A reusable cell object avoids allocating one per getCell (xterm recommends this).
let scratch: IBufferCell | undefined;

/**
 * Build a row's innerHTML from an xterm buffer line. cursorX marks the cursor
 * column (or -1). Wide (CJK) cells report width 2 and their trailing cell width
 * 0 — we render the wide char and skip the width-0 continuation, so the glyph
 * fills both columns naturally (no ZWSP hack).
 */
export function buildRow(rowEl: HTMLElement, line: IBufferLine, cols: number, cursorX: number): number {
  let html = '';
  let runStyle = '';
  let runText = '';
  let runStart = 0;
  let runs = 0; // style runs emitted — the DOM renderer's real cost driver (xterm.js#791)

  const flush = (endCol: number) => {
    if (!runText) return;
    runs++;
    if (cursorX >= runStart && cursorX < endCol) {
      const chars = [...runText];
      const off = cursorX - runStart;
      const before = chars.slice(0, off).join('');
      const cur = chars[off] || ' ';
      const after = chars.slice(off + 1).join('');
      const open = runStyle ? `<span style="${runStyle}">` : '<span>';
      if (before) html += `${open}${esc(before)}</span>`;
      html += runStyle ? `<span class="term-cursor" style="${runStyle}">${esc(cur)}</span>` : `<span class="term-cursor">${esc(cur)}</span>`;
      if (after) html += `${open}${esc(after)}</span>`;
    } else {
      html += runStyle ? `<span style="${runStyle}">${esc(runText)}</span>` : `<span>${esc(runText)}</span>`;
    }
  };

  for (let x = 0; x < cols; x++) {
    const cell = line.getCell(x, scratch);
    if (!cell) break;
    scratch = cell;
    const w = cell.getWidth();
    if (w === 0) continue; // trailing half of a wide char — already drawn
    const ch = cell.getChars() || ' ';
    const style = cellStyle(cell);
    if (style !== runStyle) { flush(x); runStyle = style; runText = ch; runStart = x; }
    else runText += ch;
  }
  flush(cols);
  rowEl.innerHTML = html;
  return runs;
}
