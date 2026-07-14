import type { CellData } from '@wterm/core';

// Cell colour / style helpers — ported from @wterm/dom's renderer so our own
// renderer produces identical colours, but WITHOUT the ghost-prone bits (no
// per-row box-shadow, no paint-containment layers). See buildRow().

const DEFAULT_COLOR = 256;
const FLAG_BOLD = 0x01;
const FLAG_DIM = 0x02;
const FLAG_ITALIC = 0x04;
const FLAG_UNDERLINE = 0x08;
const FLAG_REVERSE = 0x20;
const FLAG_INVISIBLE = 0x40;
const FLAG_STRIKETHROUGH = 0x80;

function rgbToCSS(packed: number): string {
  return `rgb(${(packed >> 16) & 0xff},${(packed >> 8) & 0xff},${packed & 0xff})`;
}
function colorToCSS(index: number): string | null {
  if (index === DEFAULT_COLOR) return null;
  if (index < 16) return `var(--term-color-${index})`;
  if (index < 232) {
    const n = index - 16;
    return `rgb(${Math.floor(n / 36) * 51},${(Math.floor(n / 6) % 6) * 51},${(n % 6) * 51})`;
  }
  const level = (index - 232) * 10 + 8;
  return `rgb(${level},${level},${level})`;
}
function cellFgCSS(fg: number, fgRgb?: number): string | null {
  return fgRgb !== undefined ? rgbToCSS(fgRgb) : colorToCSS(fg);
}
function cellBgCSS(bg: number, bgRgb?: number): string | null {
  return bgRgb !== undefined ? rgbToCSS(bgRgb) : colorToCSS(bg);
}

function buildCellStyle(fg: number, bg: number, flags: number, fgRgb?: number, bgRgb?: number): string {
  let fgIdx = fg, bgIdx = bg, fgR = fgRgb, bgR = bgRgb;
  if (flags & FLAG_REVERSE) {
    [fgIdx, bgIdx] = [bgIdx, fgIdx];
    [fgR, bgR] = [bgR, fgR];
    if (fgR === undefined && fgIdx === DEFAULT_COLOR) fgIdx = 0;
    if (bgR === undefined && bgIdx === DEFAULT_COLOR) bgIdx = 7;
  }
  const fgCSS = cellFgCSS(fgIdx, fgR);
  const bgCSS = cellBgCSS(bgIdx, bgR);
  let style = '';
  if (fgCSS) style += `color:${fgCSS};`;
  if (bgCSS) style += `background:${bgCSS};`;
  if (flags & FLAG_BOLD) style += 'font-weight:bold;';
  if (flags & FLAG_DIM) style += 'opacity:0.5;';
  if (flags & FLAG_ITALIC) style += 'font-style:italic;';
  const dec: string[] = [];
  if (flags & FLAG_UNDERLINE) dec.push('underline');
  if (flags & FLAG_STRIKETHROUGH) dec.push('line-through');
  if (dec.length) style += `text-decoration:${dec.join(' ')};`;
  if (flags & FLAG_INVISIBLE) style += 'visibility:hidden;';
  return style;
}

function resolveColors(fg: number, bg: number, flags: number, fgRgb?: number, bgRgb?: number) {
  let fgIdx = fg, bgIdx = bg, fgR = fgRgb, bgR = bgRgb;
  if (flags & FLAG_REVERSE) {
    [fgIdx, bgIdx] = [bgIdx, fgIdx];
    [fgR, bgR] = [bgR, fgR];
    if (fgR === undefined && fgIdx === DEFAULT_COLOR) fgIdx = 0;
    if (bgR === undefined && bgIdx === DEFAULT_COLOR) bgIdx = 7;
  }
  return {
    fg: cellFgCSS(fgIdx, fgR) || 'var(--term-fg)',
    bg: cellBgCSS(bgIdx, bgR) || 'var(--term-bg)',
  };
}

// Unicode block/quadrant glyphs are drawn as backgrounds so they tile perfectly.
function getBlockBackground(cp: number, fg: string, bg: string): string {
  switch (cp) {
    case 0x2580: return `linear-gradient(${fg} 50%,${bg} 50%)`;
    case 0x2581: return `linear-gradient(${bg} 87.5%,${fg} 87.5%)`;
    case 0x2582: return `linear-gradient(${bg} 75%,${fg} 75%)`;
    case 0x2583: return `linear-gradient(${bg} 62.5%,${fg} 62.5%)`;
    case 0x2584: return `linear-gradient(${bg} 50%,${fg} 50%)`;
    case 0x2585: return `linear-gradient(${bg} 37.5%,${fg} 37.5%)`;
    case 0x2586: return `linear-gradient(${bg} 25%,${fg} 25%)`;
    case 0x2587: return `linear-gradient(${bg} 12.5%,${fg} 12.5%)`;
    case 0x2588: return fg;
    case 0x2589: return `linear-gradient(to right,${fg} 87.5%,${bg} 87.5%)`;
    case 0x258a: return `linear-gradient(to right,${fg} 75%,${bg} 75%)`;
    case 0x258b: return `linear-gradient(to right,${fg} 62.5%,${bg} 62.5%)`;
    case 0x258c: return `linear-gradient(to right,${fg} 50%,${bg} 50%)`;
    case 0x258d: return `linear-gradient(to right,${fg} 37.5%,${bg} 37.5%)`;
    case 0x258e: return `linear-gradient(to right,${fg} 25%,${bg} 25%)`;
    case 0x258f: return `linear-gradient(to right,${fg} 12.5%,${bg} 12.5%)`;
    case 0x2590: return `linear-gradient(to right,${bg} 50%,${fg} 50%)`;
    case 0x2591: return `color-mix(in srgb,${fg} 25%,${bg})`;
    case 0x2592: return `color-mix(in srgb,${fg} 50%,${bg})`;
    case 0x2593: return `color-mix(in srgb,${fg} 75%,${bg})`;
    case 0x2594: return `linear-gradient(${fg} 12.5%,${bg} 12.5%)`;
    case 0x2595: return `linear-gradient(to right,${bg} 87.5%,${fg} 87.5%)`;
    default: {
      const QUADRANTS: Record<number, boolean[]> = {
        0x2596: [false, false, true, false], 0x2597: [false, false, false, true],
        0x2598: [true, false, false, false], 0x2599: [true, false, true, true],
        0x259a: [true, false, false, true], 0x259b: [true, true, true, false],
        0x259c: [true, true, false, true], 0x259d: [false, true, false, false],
        0x259e: [false, true, true, false], 0x259f: [false, true, true, true],
      };
      const q = QUADRANTS[cp];
      if (!q) return fg;
      if (q.every(Boolean)) return fg;
      const POS = ['0 0', '100% 0', '0 100%', '100% 100%'];
      const layers: string[] = [];
      q.forEach((filled, i) => { if (filled) layers.push(`linear-gradient(${fg},${fg}) ${POS[i]}/50% 50% no-repeat`); });
      layers.push(bg);
      return layers.join(',');
    }
  }
}

function esc(text: string): string {
  return text.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;');
}

type GetCell = (col: number) => CellData;

/**
 * Build one row's DOM into rowEl from a cell accessor. Style runs are coalesced
 * into spans (fewer nodes = fewer paints). The row itself gets an opaque
 * background from its trailing cell so a full repaint clears every pixel — the
 * whole reason we drop wterm's box-shadow gap-filler, which smeared on scroll.
 */
export function buildRow(rowEl: HTMLElement, getCell: GetCell, cols: number, cursorCol: number): void {
  let html = '';
  let runStyle = '';
  let runText = '';
  let runStart = 0;

  const flushRun = (endCol: number) => {
    if (!runText) return;
    if (cursorCol >= runStart && cursorCol < endCol) {
      const chars = [...runText];
      const offset = cursorCol - runStart;
      const before = chars.slice(0, offset).join('');
      const cursorChar = chars[offset] || ' ';
      const after = chars.slice(offset + 1).join('');
      const open = runStyle ? `<span style="${runStyle}">` : '<span>';
      if (before) html += `${open}${esc(before)}</span>`;
      html += runStyle
        ? `<span class="term-cursor" style="${runStyle}">${esc(cursorChar)}</span>`
        : `<span class="term-cursor">${esc(cursorChar)}</span>`;
      if (after) html += `${open}${esc(after)}</span>`;
    } else {
      html += runStyle ? `<span style="${runStyle}">${esc(runText)}</span>` : `<span>${esc(runText)}</span>`;
    }
  };

  for (let col = 0; col < cols; col++) {
    const cell = getCell(col);
    const cp = cell.char;
    if (cp >= 0x2580 && cp <= 0x259f) {
      flushRun(col);
      const colors = resolveColors(cell.fg, cell.bg, cell.flags, cell.fgRgb, cell.bgRgb);
      const cls = col === cursorCol ? 'term-block term-cursor' : 'term-block';
      const bg = getBlockBackground(cp, colors.fg, colors.bg);
      const dim = cell.flags & FLAG_DIM ? 'opacity:0.5;' : '';
      html += `<span class="${cls}" style="background:${bg};${dim}"></span>`;
      runStyle = ''; runText = ''; runStart = col + 1;
    } else {
      const ch = cp >= 32 ? String.fromCodePoint(cp) : ' ';
      const style = buildCellStyle(cell.fg, cell.bg, cell.flags, cell.fgRgb, cell.bgRgb);
      if (style !== runStyle) { flushRun(col); runStyle = style; runText = ch; runStart = col; }
      else runText += ch;
    }
  }
  flushRun(cols);
  rowEl.innerHTML = html;

  // Trailing background: extend the last cell's bg across the row so an opaque
  // repaint fully clears. No box-shadow (that's what smeared on scroll).
  const last = getCell(cols - 1);
  let bgIdx = last.bg, bgR = last.bgRgb;
  if (last.flags & FLAG_REVERSE) {
    bgIdx = last.fg; bgR = last.fgRgb;
    if (bgR === undefined && bgIdx === DEFAULT_COLOR) bgIdx = 7;
  }
  rowEl.style.background = cellBgCSS(bgIdx, bgR) || '';
}
