import { Terminal } from '@xterm/headless';
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
  private ro: ResizeObserver | null = null;
  private rafId: number | null = null;
  private rowHeight = 0;
  private destroyed = false;
  private stickBottom = true;
  private modeShim: { cursorKeysApp(): boolean; bracketedPaste(): boolean };

  constructor(element: HTMLElement, opts: CustomTermOptions) {
    this.element = element;
    this.autoResize = opts.autoResize !== false;
    this.onDataCb = opts.onData;
    this.onResizeCb = opts.onResize;
    this.onTitleCb = opts.onTitle;
    this.term = new Terminal({
      cols: opts.cols || 80,
      rows: opts.rows || 24,
      scrollback: opts.scrollback ?? 5000,
      allowProposedApi: true,
    });
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
    this.input = new InputHandler(
      this.element,
      (data) => { this.onDataCb ? this.onDataCb(data) : this.write(data); this.stickBottom = true; this.scrollToBottom(); },
      // InputHandler only calls .cursorKeysApp()/.bracketedPaste() on the bridge.
      (() => this.modeShim) as unknown as () => never,
    );
    if (this.autoResize) this.setupResizeObserver();
    this.input.focus();
    this.render();
    return this;
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
    this.term.write(data, this.onParsed);
  }
  private onParsed = () => this.scheduleRender();

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
    const buf = this.term.buffer.active;
    const base = buf.baseY;
    const cursorAbs = base + buf.cursorY;
    this.syncScrollback(base);
    for (let r = 0; r < this.rows; r++) {
      const el = this.rowEls[r];
      if (!el) continue;
      const line = buf.getLine(base + r);
      if (!line) { el.innerHTML = ''; el.style.background = ''; continue; }
      const cx = base + r === cursorAbs ? buf.cursorX : -1;
      buildRow(el, line, this.term.cols, cx);
    }
    // Only a real scrollback (normal buffer) makes .wterm a scroll container.
    const hasSb = buf.type === 'normal' && base > 0;
    this.element.classList.toggle('has-scrollback', hasSb);
    if (this.stickBottom) this.scrollToBottom();
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
