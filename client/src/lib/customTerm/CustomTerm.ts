import type { TerminalCore } from '@wterm/core';
import { InputHandler } from '@wterm/dom';
import { buildRow } from './termRender';

export interface CustomTermOptions {
  core: TerminalCore;
  cols?: number;
  rows?: number;
  cursorBlink?: boolean;
  autoResize?: boolean;
  onData?: (data: string) => void;
  onResize?: (cols: number, rows: number) => void;
  onTitle?: (title: string) => void;
}

/**
 * A minimal DOM terminal built directly on a ghostty TerminalCore. It reuses
 * wterm's InputHandler (keyboard / IME / paste / app-cursor mode) and the core's
 * VT parsing + CJK width, but renders the grid with our OWN renderer (see
 * termRender.buildRow) — no box-shadow gap-fillers, no paint-containment or
 * will-change layers. That combination is what stranded stale paint ("잔상") in
 * WebKit; a plain, layer-free grid repaints cleanly.
 *
 * DOM: <div class="wterm"><div class="term-grid"><div class="term-row">…</div>…
 * The class names match wterm's so the surrounding TerminalView effects (refit,
 * selection, scroll) and CSS keep working unchanged.
 */
export class CustomTerm {
  readonly element: HTMLElement;
  private grid: HTMLElement;
  readonly core: TerminalCore;
  private input: InputHandler | null = null;
  private rowEls: HTMLElement[] = [];
  private sbEls: HTMLElement[] = [];
  private sbCount = 0;
  cols: number;
  rows: number;
  private rowHeight = 0;
  private autoResize: boolean;
  private onData?: (data: string) => void;
  private onResize?: (cols: number, rows: number) => void;
  private onTitle?: (title: string) => void;
  private ro: ResizeObserver | null = null;
  private rafId: number | null = null;
  private destroyed = false;
  private stickBottom = false;

  constructor(element: HTMLElement, opts: CustomTermOptions) {
    this.element = element;
    this.core = opts.core;
    this.cols = opts.cols || 80;
    this.rows = opts.rows || 24;
    this.autoResize = opts.autoResize !== false;
    this.onData = opts.onData;
    this.onResize = opts.onResize;
    this.onTitle = opts.onTitle;
    element.classList.add('wterm');
    if (opts.cursorBlink) element.classList.add('cursor-blink');
    this.grid = document.createElement('div');
    this.grid.className = 'term-grid';
    element.appendChild(this.grid);
    element.addEventListener('click', this.onClickFocus);
  }

  private onClickFocus = () => {
    const sel = window.getSelection();
    if (!sel || sel.isCollapsed) this.input?.focus();
  };

  init(): this {
    this.core.init(this.cols, this.rows);
    this.measureRowHeight();
    this.setupGrid();
    this.input = new InputHandler(
      this.element,
      (data) => { this.stickBottom = this.isAtBottom(); this.onData ? this.onData(data) : this.write(data); this.scrollToBottom(); },
      () => this.core,
    );
    if (this.autoResize) this.setupResizeObserver();
    this.input.focus();
    this.doRender();
    return this;
  }

  private setupGrid() {
    this.grid.innerHTML = '';
    this.rowEls = [];
    this.sbEls = [];
    this.sbCount = 0;
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
    if (typeof data === 'string') this.core.writeString(data);
    else this.core.writeRaw(data);
    this.scheduleRender();
  }

  resize(cols: number, rows: number): void {
    if (cols === this.cols && rows === this.rows) return;
    this.stickBottom = this.isAtBottom();
    this.cols = cols;
    this.rows = rows;
    this.core.resize(cols, rows);
    this.setupGrid();
    this.scheduleRender();
    this.onResize?.(cols, rows);
  }

  focus(): void { this.input?.focus(); }

  private scheduleRender() {
    // Render on the very next animation frame — no setTimeout hop. Writes that
    // arrive in the same frame still coalesce into one paint (cheap), but each
    // new frame of output shows immediately, so a streaming response flows at
    // 60fps instead of appearing in delayed blocks.
    if (this.rafId != null) return;
    this.rafId = requestAnimationFrame(() => { this.rafId = null; this.doRender(); });
  }

  private doRender() {
    if (this.destroyed) return;
    const cursor = this.core.getCursor();
    this.syncScrollback();
    for (let r = 0; r < this.rows; r++) {
      const hasCursor = r === cursor.row && cursor.visible;
      if (this.core.isDirtyRow(r) || r === cursor.row || hasCursor) {
        buildRow(this.rowEls[r], (col) => this.core.getCell(r, col), this.cols, hasCursor ? cursor.col : -1);
      }
    }
    this.core.clearDirty();

    const hasSb = this.core.getScrollbackCount() > 0;
    this.element.classList.toggle('has-scrollback', hasSb);
    if (this.stickBottom) this.scrollToBottom();

    const title = this.core.getTitle();
    if (title !== null && this.onTitle) this.onTitle(title);
    // Terminal replies (cursor-position / device-attribute queries etc.) must go
    // back to the PTY, or Claude Code stalls waiting for them.
    let resp: string | null;
    while ((resp = this.core.getResponse()) !== null) this.onData?.(resp);
  }

  private syncScrollback() {
    const count = this.core.getScrollbackCount();
    if (count === this.sbCount) return;
    // Rebuild scrollback rows (simple + correct; scrollback grows slowly).
    for (const el of this.sbEls) el.remove();
    this.sbEls = [];
    const frag = document.createDocumentFragment();
    for (let i = 0; i < count; i++) {
      const el = document.createElement('div');
      el.className = 'term-row term-scrollback-row';
      buildRow(el, (col) => this.core.getScrollbackCell(i, col), this.cols, -1);
      frag.appendChild(el);
      this.sbEls.push(el);
    }
    this.grid.insertBefore(frag, this.rowEls[0] ?? null);
    this.sbCount = count;
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

  private measureRowHeight() {
    // Natural line box of the terminal font (NOT a .term-row — that's constrained
    // to --term-row-height, which would make this circular). TerminalView's refit
    // later re-measures the same way on font load / resize.
    const probe = document.createElement('span');
    probe.style.cssText = 'visibility:hidden;position:absolute;white-space:pre;';
    probe.textContent = 'Wg가';
    this.grid.appendChild(probe);
    const h = probe.getBoundingClientRect().height;
    probe.remove();
    if (h > 0) { this.rowHeight = Math.ceil(h); this.element.style.setProperty('--term-row-height', `${this.rowHeight}px`); }
  }

  private measureChar(): { w: number; h: number } | null {
    const row = document.createElement('div');
    row.className = 'term-row';
    row.style.cssText = 'visibility:hidden;position:absolute;';
    const span = document.createElement('span');
    span.textContent = 'W';
    row.appendChild(span);
    this.grid.appendChild(row);
    const w = span.getBoundingClientRect().width;
    const h = row.getBoundingClientRect().height;
    row.remove();
    if (!w || !h) return null;
    this.rowHeight = h;
    return { w, h };
  }

  private setupResizeObserver() {
    const initial = this.measureChar();
    if (!initial) return;
    let { w, h } = initial;
    this.ro = new ResizeObserver((entries) => {
      const m = this.measureChar();
      if (m) { w = m.w; h = m.h; }
      for (const e of entries) {
        const { width, height } = e.contentRect;
        const cols = Math.max(1, Math.floor(width / w));
        const rows = Math.max(1, Math.floor(height / h));
        if (cols !== this.cols || rows !== this.rows) this.resize(cols, rows);
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
    this.element.innerHTML = '';
  }
}
