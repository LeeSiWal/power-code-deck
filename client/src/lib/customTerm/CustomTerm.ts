import { Terminal } from '@xterm/headless';
// The addon's own typings import from '@xterm/xterm' (the DOM build we don't
// have), so pull the runtime entry directly and type it structurally below.
import { Unicode11Addon } from '@xterm/addon-unicode11/lib/addon-unicode11.js';
import { InputHandler } from '@wterm/dom';
import { buildRow } from './xtermRender';

export interface CustomTermOptions {
  cols?: number;
  rows?: number;
  cursorBlink?: boolean;
  autoResize?: boolean;
  scrollback?: number;
  onData?: (data: string) => void;
  onResize?: (cols: number, rows: number) => void;
  onTitle?: (title: string) => void;
  /**
   * Flow-control ack: fired after xterm finishes parsing output, reporting the
   * UTF-8 byte count consumed (batched). The server meters the same bytes and
   * pauses a flooding process until enough are acked — VS Code's terminal
   * backpressure, which keeps a big burst from jamming the WebSocket / main thread.
   */
  onAck?: (bytes: number) => void;
  /**
   * Skip creating the built-in InputHandler (the hidden off-screen textarea).
   * Used when an external, visible input surface (UnifiedInput) owns keyboard
   * and IME instead — so the two textareas don't fight for focus/keystrokes.
   */
  disableInput?: boolean;
}

/** Pixel rect of the cursor cell, relative to the terminal element's visible box. */
export interface CursorRect { left: number; top: number; height: number }

/** UTF-8 byte length of a string without allocating (matches the server's
 *  len([]byte) accounting for flow-control acks). */
function utf8Len(s: string): number {
  let n = 0;
  for (let i = 0; i < s.length; i++) {
    const c = s.charCodeAt(i);
    if (c < 0x80) n += 1;
    else if (c < 0x800) n += 2;
    else if (c >= 0xd800 && c <= 0xdbff) { n += 4; i++; } // surrogate pair → 1 rune
    else n += 3;
  }
  return n;
}

/**
 * A DOM terminal built on @xterm/headless — the SAME VT engine VS Code's terminal
 * uses. Claude Code renders correctly there, so parsing its output with xterm (and
 * reading xterm's buffer) reproduces that exactly: none of the stale-attribute
 * "잔상" ghostty left when Claude Code scrolls with insert-line. We only replace
 * xterm's renderer with our own plain-DOM one (no box-shadow / compositing layers),
 * and reuse wterm's InputHandler for keyboard / IME / paste.
 *
 * DOM: <div class="wterm"><div class="term-grid"><div class="term-row">…
 */
export class CustomTerm {
  readonly element: HTMLElement;
  readonly term: Terminal;
  private grid: HTMLElement;
  private input: InputHandler | null = null;
  private rowEls: HTMLElement[] = [];
  private sbEls: HTMLElement[] = [];
  private sbBase = 0;
  private autoResize: boolean;
  private onDataCb?: (data: string) => void;
  private onResizeCb?: (cols: number, rows: number) => void;
  private onTitleCb?: (title: string) => void;
  private onAckCb?: (bytes: number) => void;
  private ackPending = 0;
  private static readonly ACK_THRESHOLD = 16 * 1024;
  private ro: ResizeObserver | null = null;
  private rafId: number | null = null;
  private rowHeight = 0;
  private charWidth = 0;
  private lastRenderError = '';
  private lastRenderRuns = 0;
  private lastRenderMs = 0;
  private renderCount = 0;
  private disableInput: boolean;
  private cursorListener: ((rect: CursorRect | null) => void) | null = null;
  private destroyed = false;
  private stickBottom = true;
  private modeShim: { cursorKeysApp(): boolean; bracketedPaste(): boolean };

  constructor(element: HTMLElement, opts: CustomTermOptions) {
    this.element = element;
    this.autoResize = opts.autoResize !== false;
    this.disableInput = opts.disableInput === true;
    this.onDataCb = opts.onData;
    this.onResizeCb = opts.onResize;
    this.onTitleCb = opts.onTitle;
    this.onAckCb = opts.onAck;
    this.term = new Terminal({
      cols: opts.cols || 80,
      rows: opts.rows || 24,
      scrollback: opts.scrollback ?? 5000,
      allowProposedApi: true,
    });
    // Unicode 11 widths. xterm ships ONLY the Unicode 6 provider and makes the
    // first registered one active, so the default is a 2010-era width table —
    // wrong for a terminal whose whole point is 한글/CJK. Loading the addon only
    // *registers* the provider; activeVersion must be set explicitly (the classic
    // integration bug). VS Code defaults to '11' for the same reason.
    //
    // Width must agree end-to-end: the app computes the cursor by summing its own
    // wcwidth and never tells us. Disagree by one cell and every later
    // cursor-relative write lands wrong — backspace eats too much, status bars
    // drift, and worst, the app wraps where we didn't so isWrapped becomes a lie
    // and the next resize reflows on top of it.
    try {
      this.term.loadAddon(new Unicode11Addon() as unknown as Parameters<Terminal['loadAddon']>[0]);
      this.term.unicode.activeVersion = '11';
    } catch (e) {
      this.lastRenderError = 'unicode11:' + e; // stay on v6 rather than fail to start
    }
    this.modeShim = {
      cursorKeysApp: () => this.term.modes.applicationCursorKeysMode,
      bracketedPaste: () => this.term.modes.bracketedPasteMode,
    };
    element.classList.add('wterm');
    if (opts.cursorBlink) element.classList.add('cursor-blink');
    this.grid = document.createElement('div');
    this.grid.className = 'term-grid';
    element.appendChild(this.grid);
    element.addEventListener('click', this.onClickFocus);
    // Terminal replies (device attributes, cursor-position reports, focus events)
    // that xterm generates in response to app queries must go back to the PTY.
    this.term.onData((d) => this.onDataCb?.(d));
    this.term.onTitleChange((t) => this.onTitleCb?.(t));
  }

  private onClickFocus = () => {
    const sel = window.getSelection();
    if (!sel || sel.isCollapsed) this.input?.focus();
  };

  get cols(): number { return this.term.cols; }
  get rows(): number { return this.term.rows; }
  get applicationCursorKeys(): boolean { return this.term.modes.applicationCursorKeysMode; }

  init(): this {
    this.measure();
    this.setupGrid();
    // When an external UnifiedInput owns the keyboard we skip the built-in hidden
    // textarea entirely (otherwise both would grab focus / duplicate keystrokes).
    if (!this.disableInput) {
      this.input = new InputHandler(
        this.element,
        (data) => { this.onDataCb ? this.onDataCb(data) : this.write(data); this.stickBottom = true; this.scrollToBottom(); },
        // InputHandler only calls .cursorKeysApp()/.bracketedPaste() on the bridge.
        (() => this.modeShim) as unknown as () => never,
      );
    }
    if (this.autoResize) this.setupResizeObserver();
    this.input?.focus();
    this.render();
    return this;
  }

  /** Whether the built-in hidden-textarea input was created. */
  get hasBuiltInInput(): boolean { return this.input != null; }

  /** Register a callback fired on every render with the cursor's pixel rect (or
   *  null when it can't be located). Used by an external cursor-anchored input. */
  setCursorListener(cb: ((rect: CursorRect | null) => void) | null): void {
    this.cursorListener = cb;
  }

  /** Pixel rect of the cursor cell relative to the terminal element's visible box
   *  (already accounts for scroll). Null when the cursor row isn't in the DOM. */
  cursorRect(): CursorRect | null {
    const buf = this.term.buffer.active;
    const rowEl = this.rowEls[buf.cursorY];
    if (!rowEl) return null;
    const wrect = this.element.getBoundingClientRect();
    const rrect = rowEl.getBoundingClientRect();
    return {
      left: rrect.left - wrect.left + buf.cursorX * this.charWidth,
      top: rrect.top - wrect.top,
      height: this.rowHeight,
    };
  }

  // Matches an http(s) URL. Excludes whitespace, quotes and bracket chars so a link
  // printed in prose stops at the surrounding punctuation; trailing sentence
  // punctuation is trimmed off the match separately.
  private static readonly URL_RE = /https?:\/\/[^\s"'`<>()[\]{}|^\\]+/g;

  /**
   * The URL under a viewport pixel, or null. Maps the pixel to a buffer cell by the
   * uniform row height / char width, then reconstructs the WHOLE logical line across
   * xterm's soft-wrap boundaries — so a link that wrapped over several rows is matched
   * as one string and opens fully — and returns the URL spanning the clicked cell.
   * Builds the text one entry per cell (wide-char placeholder → space) so column N
   * maps to a fixed string offset regardless of CJK width; URLs are ASCII so none of
   * that ever falls inside the match.
   */
  linkAt(clientX: number, clientY: number): string | null {
    if (this.rowHeight <= 0 || this.charWidth <= 0) return null;
    const buf = this.term.buffer.active;
    const rect = this.grid.getBoundingClientRect();
    const lineIdx = Math.floor((clientY - rect.top) / this.rowHeight);
    const col = Math.floor((clientX - rect.left) / this.charWidth);
    if (lineIdx < 0 || col < 0 || col >= this.term.cols || lineIdx >= buf.length) return null;
    // Extend to the full wrapped logical line: back to the first row that isn't a
    // wrap continuation, forward through every continuation row.
    let start = lineIdx;
    while (start > 0 && buf.getLine(start)?.isWrapped) start--;
    let end = lineIdx;
    while (end + 1 < buf.length && buf.getLine(end + 1)?.isWrapped) end++;
    let text = '';
    let hit = -1;
    for (let y = start; y <= end; y++) {
      const line = buf.getLine(y);
      if (!line) continue;
      for (let c = 0; c < this.term.cols; c++) {
        if (y === lineIdx && c === col) hit = text.length;
        const chars = line.getCell(c)?.getChars();
        text += chars && chars.length ? chars : ' ';
      }
    }
    if (hit < 0) return null;
    CustomTerm.URL_RE.lastIndex = 0;
    let m: RegExpExecArray | null;
    while ((m = CustomTerm.URL_RE.exec(text))) {
      if (hit >= m.index && hit < m.index + m[0].length) {
        return m[0].replace(/[.,;:!?)\]}'"]+$/, '');
      }
    }
    return null;
  }

  private setupGrid() {
    this.grid.innerHTML = '';
    this.rowEls = [];
    this.sbEls = [];
    this.sbBase = 0;
    const frag = document.createDocumentFragment();
    for (let r = 0; r < this.rows; r++) {
      const el = document.createElement('div');
      el.className = 'term-row';
      frag.appendChild(el);
      this.rowEls.push(el);
    }
    this.grid.appendChild(frag);
  }

  write(data: string | Uint8Array): void {
    this.stickBottom = this.isAtBottom();
    const n = typeof data === 'string' ? utf8Len(data) : data.length;
    // The ack fires from xterm's write callback — AFTER the bytes are parsed —
    // so the server only counts output the browser has actually consumed.
    this.term.write(data, () => { this.scheduleRender(); this.reportAck(n); });
  }

  /** Batch acked bytes and flush to the server once past the threshold, so a
   *  flood acks every ~16KB (steady drain) instead of per tiny chunk. */
  private reportAck(n: number): void {
    if (!this.onAckCb) return;
    this.ackPending += n;
    if (this.ackPending >= CustomTerm.ACK_THRESHOLD) {
      this.onAckCb(this.ackPending);
      this.ackPending = 0;
    }
  }

  resize(cols: number, rows: number): void {
    if (cols === this.term.cols && rows === this.term.rows) return;
    this.stickBottom = this.isAtBottom();
    this.term.resize(cols, rows);
    this.setupGrid();
    this.scheduleRender();
    this.onResizeCb?.(cols, rows);
  }

  focus(): void { this.input?.focus(); }

  /** Re-measure the font metrics and re-fit cols/rows to the container (call on
   *  web-font load — the metrics change without a container resize). */
  refit(): void {
    const m = this.measure();
    if (!m) return;
    const rect = this.element.getBoundingClientRect();
    const cs = getComputedStyle(this.element);
    const padX = (parseFloat(cs.paddingLeft) || 0) + (parseFloat(cs.paddingRight) || 0);
    const padY = (parseFloat(cs.paddingTop) || 0) + (parseFloat(cs.paddingBottom) || 0);
    this.resize(Math.max(1, Math.floor((rect.width - padX) / m.charW)), Math.max(1, Math.floor((rect.height - padY) / m.rowH)));
  }

  /**
   * Whether the visible screen holds any non-blank cell. Used by the attach
   * self-heal: an idle full-screen app (Antigravity's trust prompt, a resumed
   * session) emits nothing on its own, so if the screen is still empty a beat
   * after attaching, the replay never landed (or was cleared) and we must
   * re-attach — a check on "did any bytes arrive" misses the case where bytes
   * DID arrive but left the screen blank (a stray clear, a paused render, or a
   * viewer that got detached server-side).
   */
  hasVisibleContent(): boolean {
    const buf = this.term.buffer.active;
    const base = buf.baseY;
    for (let r = 0; r < this.rows; r++) {
      const line = buf.getLine(base + r);
      if (line && line.translateToString(true).trim() !== '') return true;
    }
    return false;
  }

  /** Blank the terminal so an incoming replay redraws from a clean slate (reconnect). */
  reset(): void {
    this.term.reset();
    this.sbBase = 0;
    this.setupGrid();
    this.scheduleRender();
  }

  // While a selection is held, pause re-rendering so incoming output doesn't
  // replace the row DOM under the selection (which would drop the highlight).
  // xterm keeps parsing writes meanwhile; we catch up when unpaused.
  private renderPaused = false;
  setRenderPaused(p: boolean): void {
    this.renderPaused = p;
    if (!p) this.scheduleRender();
  }

  private scheduleRender() {
    if (this.rafId != null || this.renderPaused) return;
    this.rafId = requestAnimationFrame(() => { this.rafId = null; this.render(); });
  }

  private render() {
    if (this.destroyed) return;
    const t0 = performance.now();
    let runs = 0;
    const buf = this.term.buffer.active;
    const base = buf.baseY;
    const cursorAbs = base + buf.cursorY;
    try { this.syncScrollback(base); } catch (e) { this.lastRenderError = 'sb:' + e; }
    for (let r = 0; r < this.rows; r++) {
      const el = this.rowEls[r];
      if (!el) continue;
      const line = buf.getLine(base + r);
      if (!line) { el.innerHTML = ''; el.style.background = ''; continue; }
      const cx = base + r === cursorAbs ? buf.cursorX : -1;
      // A single row that trips the styled builder must NEVER blank the whole
      // screen (the agy "완전히 빈 화면" class). Fall back to plain text for just
      // that row and keep going, recording why for the on-screen diagnostic.
      try { runs += buildRow(el, line, this.term.cols, cx); }
      catch (e) { el.textContent = line.translateToString(true); this.lastRenderError = 'row' + r + ':' + e; }
    }
    // Render cost, for the on-device diagnostic. The DOM renderer's cost scales
    // with STYLE RUNS per row, not characters (xterm.js#791) — a heavily colored
    // full-screen TUI repaint is its worst case, and "too slow to ever finish a
    // frame" is a leading suspect for a screen that looks like it never rendered.
    this.lastRenderRuns = runs;
    this.lastRenderMs = performance.now() - t0;
    this.renderCount++;
    // Only a real scrollback (normal buffer) makes .wterm a scroll container.
    const hasSb = buf.type === 'normal' && base > 0;
    this.element.classList.toggle('has-scrollback', hasSb);
    if (this.stickBottom) this.scrollToBottom();
    // Let an external cursor-anchored input follow the cursor as the app redraws.
    if (this.cursorListener) this.cursorListener(this.cursorRect());
  }

  /** Render NOW, bypassing the rAF gate and any render-pause. The self-heal uses
   *  this: if a scheduled rAF was throttled to never (panel hidden/animating at
   *  launch, iOS background), rafId stays non-null and every scheduleRender()
   *  early-returns, so the final buffer never reaches the DOM — a blank screen
   *  over a fully-populated buffer. A timer-driven forceRender() breaks that. */
  forceRender(): void {
    if (this.destroyed) return;
    if (this.rafId != null) { cancelAnimationFrame(this.rafId); this.rafId = null; }
    this.renderPaused = false;
    this.render();
  }

  /** The emulator buffer holds visible content but the rendered DOM rows are all
   *  empty — the render pipeline stalled (throttled rAF) or a row threw. This is
   *  the true "blank screen" signal; hasVisibleContent() alone (buffer-only) is a
   *  false-positive here because the buffer IS populated. */
  domBlankWithContent(): boolean {
    if (!this.hasVisibleContent()) return false;
    for (const el of this.rowEls) {
      if (el && el.textContent && el.textContent.trim() !== '') return false;
    }
    return true;
  }

  /** One-line render state, for the on-device diagnostic banner (no console on a
   *  phone/iPad). Reveals the cause when a blank persists past forceRender().
   *
   *  Each field maps to a specific suspect, so the banner names the cause instead
   *  of just reporting "blank":
   *   - renders=0        → render() never ran (rAF dead / paused). Not a paint bug.
   *   - runs/ms high     → style-run blowup (xterm.js#791): the DOM renderer's cost
   *                        scales with runs per row; a slow enough frame looks blank.
   *   - dom=0 buf=N      → we painted, the DOM has no text: builder produced nothing.
   *   - ovf=1            → rows are wider than the grid: glyphs clipped at cell
   *                        boundaries (xterm.js#3807) — suspect with CJK + D2Coding.
   *   - cw/rh 0          → measurement failed (font not loaded): every cell is 0px,
   *                        so content is painted into a zero-sized grid = invisible.
   */
  renderDiag(): string {
    const buf = this.term.buffer.active;
    let domChars = 0;
    let ovf = 0;
    for (const el of this.rowEls) {
      if (!el) continue;
      domChars += (el.textContent || '').trim().length;
      if (el.scrollWidth > el.clientWidth + 1) ovf++;
    }
    const gridW = this.grid?.getBoundingClientRect().width ?? 0;
    const gridH = this.grid?.getBoundingClientRect().height ?? 0;
    return `pcd-render: cols=${this.term.cols} rows=${this.rows} rowEls=${this.rowEls.length}` +
      ` renders=${this.renderCount} runs=${this.lastRenderRuns} ms=${this.lastRenderMs.toFixed(1)}` +
      ` bufChars=${this.bufferChars()} domChars=${domChars} ovfRows=${ovf}` +
      ` paused=${this.renderPaused} raf=${this.rafId != null} buf=${buf.type} baseY=${buf.baseY}` +
      ` cw=${this.charWidth.toFixed(1)} rh=${this.rowHeight} grid=${gridW.toFixed(0)}x${gridH.toFixed(0)}` +
      ` uni=${this.term.unicode.activeVersion} err=${this.lastRenderError || '-'}`;
  }

  /** Non-blank chars the emulator believes are on the visible screen. Paired with
   *  domChars in the diagnostic: buf>0 && dom==0 is the true "blank screen" state. */
  private bufferChars(): number {
    const buf = this.term.buffer.active;
    let n = 0;
    for (let r = 0; r < this.rows; r++) {
      const line = buf.getLine(buf.baseY + r);
      if (line) n += line.translateToString(true).trim().length;
    }
    return n;
  }

  /** The live diagnostic string (for the ?debug overlay — same data as the banner,
   *  but shown continuously rather than only when a blank is detected). */
  diagLine(): string {
    return this.renderDiag();
  }

  /** Paint the render diagnostic into the top row so a persistent blank surfaces
   *  its cause on-screen instead of showing nothing. */
  showDiagBanner(): void {
    const diag = this.renderDiag();
    const el = this.rowEls[0];
    if (el) el.textContent = diag;
    // Also to the console (for a desktop devtools session) in case the banner
    // itself is hidden by whatever is blanking the screen.
    console.warn(diag);
  }

  private syncScrollback(base: number) {
    if (base === this.sbBase) return;
    const buf = this.term.buffer.active;
    if (base < this.sbBase) {
      // Buffer shrank/reset — rebuild all scrollback rows.
      for (const el of this.sbEls) el.remove();
      this.sbEls = [];
      this.sbBase = 0;
    }
    // Append the newly-scrolled-off lines (indices this.sbBase .. base-1).
    const frag = document.createDocumentFragment();
    for (let y = this.sbBase; y < base; y++) {
      const el = document.createElement('div');
      el.className = 'term-row term-scrollback-row';
      const line = buf.getLine(y);
      if (line) buildRow(el, line, this.term.cols, -1);
      frag.appendChild(el);
      this.sbEls.push(el);
    }
    this.grid.insertBefore(frag, this.rowEls[0] ?? null);
    this.sbBase = base;
  }

  private isAtBottom(): boolean {
    const el = this.element;
    return el.scrollHeight - el.scrollTop - el.clientHeight < 5;
  }
  private scrollToBottom() {
    const el = this.element;
    const max = el.scrollHeight - el.clientHeight;
    el.scrollTop = max > 0 ? max : 0;
  }

  private measure(): { charW: number; rowH: number } | null {
    const probe = document.createElement('span');
    probe.style.cssText = 'visibility:hidden;position:absolute;top:0;left:0;white-space:pre;';
    probe.textContent = 'W'.repeat(50);
    this.grid.appendChild(probe);
    const charW = probe.getBoundingClientRect().width / 50;
    probe.textContent = 'Wg가';
    const rowH = probe.getBoundingClientRect().height;
    probe.remove();
    if (!charW || !rowH) return null;
    this.charWidth = charW;
    this.rowHeight = Math.ceil(rowH);
    this.element.style.setProperty('--term-row-height', `${this.rowHeight}px`);
    return { charW, rowH: this.rowHeight };
  }

  private setupResizeObserver() {
    let m = this.measure();
    this.ro = new ResizeObserver((entries) => {
      const nm = this.measure();
      if (nm) m = nm;
      if (!m) return;
      for (const e of entries) {
        const { width, height } = e.contentRect;
        const cols = Math.max(1, Math.floor(width / m.charW));
        const rows = Math.max(1, Math.floor(height / m.rowH));
        if (cols !== this.term.cols || rows !== this.term.rows) this.resize(cols, rows);
      }
    });
    this.ro.observe(this.element);
  }

  destroy(): void {
    this.destroyed = true;
    if (this.rafId != null) cancelAnimationFrame(this.rafId);
    this.ro?.disconnect();
    this.input?.destroy();
    this.element.removeEventListener('click', this.onClickFocus);
    this.term.dispose();
    this.element.innerHTML = '';
  }
}
