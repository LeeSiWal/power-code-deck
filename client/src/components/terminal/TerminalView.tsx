import { useEffect, useRef, useState, useCallback, forwardRef, useImperativeHandle } from 'react';
import { Terminal, type TerminalHandle as WTermReactHandle } from '@wterm/react';
import type { WTerm } from '@wterm/dom';
import type { TerminalCore } from '@wterm/core';
import { GhosttyCore } from '@wterm/ghostty';
import ghosttyWasmUrl from '../../assets/ghostty-vt.wasm?url';
import '@wterm/react/css';
import { wideAwareCore } from '../../lib/wideAwareCore';
// Bundled fixed-width coding font with Latin AND Hangul at a 1:2 advance ratio.
// System fonts (JetBrains Mono etc.) lack Hangul, so Korean fell back to a font
// that isn't 2× the Latin width — breaking the terminal grid's CJK=2-cell model
// (uneven spacing + overflow). Bundling guarantees 한/0 == 2.0 on every device.
import '@fontsource/nanum-gothic-coding/400.css';
import '@fontsource/nanum-gothic-coding/700.css';
import { agentDeckWS } from '../../lib/ws';
import { useDevice } from '../../hooks/useDevice';

export interface TerminalHandle {
  /** Move keyboard focus into the terminal (used after Prompt Bar Send). */
  focus: () => void;
  /**
   * Send a control / navigation key to the PTY. Arrow keys are translated to the
   * application-cursor-key form (ESC O A/B/C/D) when the TUI has DECCKM enabled —
   * apps like Claude Code put the terminal in application cursor mode for their
   * arrow menus, where the normal-mode CSI form does nothing.
   */
  sendKey: (data: string) => void;
}

/** Normal cursor sequences (ESC [ x) → application-mode SS3 (ESC O x). */
const APP_CURSOR_MAP: Record<string, string> = {
  '\x1b[A': '\x1bOA',
  '\x1b[B': '\x1bOB',
  '\x1b[C': '\x1bOC',
  '\x1b[D': '\x1bOD',
};

const HANGUL_RE = /[ᄀ-ᇿ㄰-㆏가-힣]/;

/** Bound the freeze-while-selecting buffer so a long-held selection can't grow it
 * without limit; past this we give up the freeze and let output through. */
const MAX_FROZEN_CHARS = 1_000_000;

/**
 * Synchronous clipboard copy via a hidden textarea + execCommand. Works within a
 * user gesture even in non-secure (http) contexts — e.g. a LAN URL like
 * http://192.168.x.x:5553 — where navigator.clipboard is unavailable or rejects.
 * Must run synchronously inside the gesture (no preceding await).
 */
function legacyCopy(text: string): boolean {
  try {
    const ta = document.createElement('textarea');
    ta.value = text;
    ta.setAttribute('readonly', ''); // avoid popping the mobile keyboard
    ta.style.position = 'fixed';
    ta.style.top = '0';
    ta.style.left = '0';
    ta.style.width = '1px';
    ta.style.height = '1px';
    ta.style.opacity = '0';
    document.body.appendChild(ta);
    ta.focus();
    ta.select();
    ta.setSelectionRange(0, text.length); // iOS Safari needs an explicit range
    const ok = document.execCommand('copy');
    document.body.removeChild(ta);
    return ok;
  } catch {
    return false;
  }
}

/**
 * Copy text to the clipboard. Secure context (https / localhost) → async Clipboard
 * API. Non-secure (LAN http) → straight to the synchronous execCommand path
 * (awaiting the rejecting promise first would spend the user gesture).
 */
async function writeClipboard(text: string): Promise<boolean> {
  if (!text) return false;
  if (window.isSecureContext && navigator.clipboard?.writeText) {
    try {
      await navigator.clipboard.writeText(text);
      return true;
    } catch {
      /* fall through */
    }
  }
  return legacyCopy(text);
}

interface TerminalViewProps {
  agentId: string;
  fontSize?: number;
  /** Fired when the user focuses the terminal directly (desktop click). */
  onFocusTerminal?: () => void;
  /** Touch: a Hangul char typed straight into the terminal — nudge to the Prompt Bar. */
  onHangulDirect?: () => void;
}

/**
 * Interactive terminal backed by wterm (@wterm/react). wterm renders to the DOM,
 * so selection, copy/paste and scrolling are the browser's own. This component
 * scopes selection to the terminal (its own section), keeps a selection stable
 * while a live TUI redraws (freeze-while-selecting), and offers touch-friendly
 * select-all / copy affordances. Wired to the WebSocket PTY protocol
 * (terminal:attach/input/output/resize).
 */
export const TerminalView = forwardRef<TerminalHandle, TerminalViewProps>(function TerminalView(
  { agentId, fontSize, onFocusTerminal, onHangulDirect },
  ref,
) {
  const { isMobile, isTablet, isTouchDevice } = useDevice();
  const shellRef = useRef<HTMLDivElement>(null);
  const handleRef = useRef<WTermReactHandle | null>(null);
  const wtRef = useRef<WTerm | null>(null);
  const attachedRef = useRef(false);
  const readyRef = useRef(false);
  const colsRef = useRef(80);
  const rowsRef = useRef(24);
  const [status, setStatus] = useState<'connecting' | 'connected' | 'disconnected'>('connecting');
  const statusRef = useRef(status);
  statusRef.current = status;
  // Exclusive viewer: another device took over this session. We stop attaching
  // (so the two devices don't ping-pong) until the user reclaims it here.
  const [evicted, setEvicted] = useState(false);
  const evictedRef = useRef(evicted);
  evictedRef.current = evicted;
  const onHangulDirectRef = useRef(onHangulDirect);
  onHangulDirectRef.current = onHangulDirect;

  const [hasSelection, setHasSelection] = useState(false);
  const [copied, setCopied] = useState(false);
  const [debugInfo, setDebugInfo] = useState<string>('');
  const debug = typeof window !== 'undefined' && window.location.search.includes('debug');
  // Terminal core: the ghostty (libghostty) VT core measures CJK as 2 cells like
  // Claude Code does, unlike wterm's built-in Zig core (1 cell) which drifts and
  // garbles Korean. Wrapped so the renderer draws wide glyphs across both cells.
  // Loaded async; the terminal mounts once it's ready (or falls back to built-in).
  const [core, setCore] = useState<TerminalCore | null>(null);
  const [coreFailed, setCoreFailed] = useState(false);
  const coreReady = core !== null || coreFailed;
  // Whether the running app has mouse tracking on (Claude Code and other TUIs do).
  // When on, the app owns scrolling — a wheel/drag must be forwarded as a mouse
  // event so the app scrolls ITS conversation (its screen is the alternate buffer,
  // so wterm has no scrollback of its own to move). Detected by scanning output.
  const [mouseTracking, setMouseTracking] = useState(false);
  const mouseTrackingRef = useRef(false);
  mouseTrackingRef.current = mouseTracking;
  const sgrMouseRef = useRef(false);
  // "Jump to bottom" affordance shown when the user has scrolled up. For a
  // mouse-tracking app (Claude Code owns its scroll, so we can't read its
  // position) we track net wheel direction: up increments, down decrements,
  // clamped — the button shows while we're a few lines up and hides near bottom.
  const [showScrollBottom, setShowScrollBottom] = useState(false);
  const scrollUpAccumRef = useRef(0);
  // Freeze output ONLY during an active drag-select (mousedown → mouseup) so the
  // selection doesn't shift under the pointer, then resume. (Holding the freeze for
  // as long as a selection merely existed left the terminal frozen with a lingering
  // highlight — the "뿌연 잔상".)
  const selectingRef = useRef(false);
  const pendingRef = useRef<string[]>([]);
  const pendingLenRef = useRef(0);
  // Last non-empty terminal selection text, snapshotted so 복사 / Cmd+C still work
  // for a moment after a redraw clears the visible highlight. hasSelectionRef mirrors
  // the state for the touch scroll gate; selGraceRef lingers the copy button.
  const selectionTextRef = useRef('');
  const hasSelectionRef = useRef(false);
  hasSelectionRef.current = hasSelection;
  const selGraceRef = useRef(0);
  // True only while a selection is genuinely non-collapsed inside the terminal (no
  // grace). Gates the renderer's force-redraw so a held selection isn't re-rendered
  // under (which orphans its highlight paint on Safari — the drag "잔상"), and
  // force-redraw resumes the instant it clears to sweep any leftover.
  const selectionActiveRef = useRef(false);

  const resolvedFontSize = fontSize ?? (isMobile ? 13 : isTablet ? 13 : 14);

  // Load the ghostty core once. On failure, fall back to wterm's built-in core.
  useEffect(() => {
    let disposed = false;
    GhosttyCore.load({ wasmPath: ghosttyWasmUrl, scrollbackLimit: 10000 })
      .then((gc) => { if (!disposed) setCore(wideAwareCore(gc, () => !selectionActiveRef.current)); })
      .catch((err) => {
        console.error('[terminal] ghostty core load failed, using built-in core', err);
        if (!disposed) setCoreFailed(true);
      });
    return () => { disposed = true; };
  }, []);

  useImperativeHandle(ref, () => ({
    focus: () => handleRef.current?.focus(),
    sendKey: (data: string) => {
      const app = wtRef.current?.bridge?.cursorKeysApp?.() ?? false;
      agentDeckWS.send('terminal:input', { agentId, data: app ? (APP_CURSOR_MAP[data] ?? data) : data });
    },
  }), [agentId]);

  const flushPending = useCallback(() => {
    selectingRef.current = false;
    if (pendingRef.current.length) {
      handleRef.current?.write(pendingRef.current.join(''));
      pendingRef.current = [];
      pendingLenRef.current = 0;
    }
  }, []);

  // Track mouse-tracking / SGR mode from the output stream (apps toggle it with
  // DECSET/DECRST). Cheap early-out when no CSI ? is present.
  const scanMouseMode = useCallback((data: string) => {
    if (data.indexOf('\x1b[?') === -1) return;
    for (const m of ['1000', '1002', '1003']) {
      if (data.includes(`\x1b[?${m}h`)) setMouseTracking(true);
      else if (data.includes(`\x1b[?${m}l`)) setMouseTracking(false);
    }
    if (data.includes('\x1b[?1006h')) sgrMouseRef.current = true;
    else if (data.includes('\x1b[?1006l')) sgrMouseRef.current = false;
  }, []);

  // Force WebKit to drop stale paint ("잔상"). Safari keeps a compositing backing
  // store for the row grid that it only partially invalidates when we rewrite rows
  // during a scroll/resize/stream — old glyphs and background blocks linger. Toggle
  // the grid's `display` synchronously (none → force reflow → restore): that
  // destroys and rebuilds its layer so it repaints from scratch. It's all in one JS
  // task so no blank frame is ever shown (flicker-free), and it targets .term-grid
  // (inner) — NOT .wterm — so wterm's own ResizeObserver (which watches .wterm)
  // doesn't fire. scrollTop is preserved (the grid collapses to 0 height while off).
  const forceRepaint = useCallback(() => {
    const wt = shellRef.current?.querySelector('.wterm') as HTMLElement | null;
    const grid = wt?.querySelector('.term-grid') as HTMLElement | null;
    if (!wt || !grid) return;
    const st = wt.scrollTop;
    grid.style.display = 'none';
    void grid.offsetHeight;
    grid.style.display = '';
    if (wt.scrollTop !== st) wt.scrollTop = st;
  }, []);

  // Anti-잔상 pump. WebKit strands the grid's old paint whenever the screen CHANGES
  // — not just on scroll/resize but also while a response streams in and pushes the
  // conversation up (esp. around colored blocks like the user's own message box,
  // where the ghost "번진다"). So repaint every frame while the terminal is actively
  // changing, and stop ~300ms after the last change so we never churn when idle.
  // markActive() is pinged from output, scroll, and resize.
  const activeUntilRef = useRef(0);
  const pumpingRef = useRef(false);
  const markActive = useCallback(() => {
    activeUntilRef.current = performance.now() + 300;
    if (pumpingRef.current) return;
    pumpingRef.current = true;
    const tick = () => {
      forceRepaint();
      if (performance.now() < activeUntilRef.current) requestAnimationFrame(tick);
      else pumpingRef.current = false;
    };
    requestAnimationFrame(tick);
  }, [forceRepaint]);

  const writeToTerm = useCallback((data: string) => {
    scanMouseMode(data);
    if (selectingRef.current) {
      pendingRef.current.push(data);
      pendingLenRef.current += data.length;
      if (pendingLenRef.current > MAX_FROZEN_CHARS) flushPending(); // bound memory
      return;
    }
    handleRef.current?.write(data);
    markActive(); // repaint while output is changing the screen (kills 잔상)
  }, [flushPending, scanMouseMode, markActive]);

  // Attach ONLY once wterm is ready AND the socket is open. The server replies to
  // attach with the current screen buffer immediately; attaching before wterm's
  // write handle exists would drop that first dump — leaving an idle alt-screen
  // app (Claude Code) blank forever.
  const maybeAttach = useCallback(() => {
    if (evictedRef.current || !readyRef.current || attachedRef.current || !agentDeckWS.connected) return;
    // Reset the terminal BEFORE the server's replay lands. On a reconnect wterm
    // still holds the pre-disconnect grid; replaying the log on top of it (a TUI's
    // absolute cursor moves applied over stale content, possibly at a different
    // size) is what shifts/garbles the text ("글자 밀림"). RIS (ESC c) clears the
    // grid + scrollback + cursor so the replay redraws from a clean slate. Harmless
    // on the first attach (terminal is already empty).
    handleRef.current?.write('\x1bc');
    agentDeckWS.send('terminal:attach', { agentId, cols: colsRef.current, rows: rowsRef.current });
    attachedRef.current = true;
  }, [agentId]);

  // Reclaim the session on this device (after being evicted by another device).
  const reclaim = useCallback(() => {
    setEvicted(false);
    evictedRef.current = false;
    attachedRef.current = false;
    maybeAttach();
  }, [maybeAttach]);

  // WS wiring: output → write; open/close → status + (re)attach; detach on unmount.
  useEffect(() => {
    const unsubOutput = agentDeckWS.on('terminal:output', (payload: any) => {
      if (payload.agentId !== agentId) return;
      writeToTerm(payload.data);
      if (statusRef.current !== 'connected') setStatus('connected');
    });
    const unsubOpen = agentDeckWS.on('open', () => {
      // A fresh socket is a clean slate on the server: new viewerID, attached to
      // nothing, no eviction record. So auto-reclaim — clear any stale eviction
      // from the PREVIOUS connection and re-attach without the user tapping
      // "다시 열기". iOS/iPadOS freezes background sockets, so a phone/iPad
      // returning to the foreground reconnects here; this is what makes the
      // handoff between two touch devices feel automatic. Note this only fires
      // on a genuine (re)connect — a device evicted while its socket stays live
      // gets no 'open', so two simultaneously-foreground devices don't ping-pong.
      setEvicted(false);
      evictedRef.current = false;
      attachedRef.current = false;
      setStatus('connected');
      maybeAttach();
    });
    const unsubClose = agentDeckWS.on('close', () => setStatus('disconnected'));
    // Another device attached — release this viewer and stop attaching until the
    // user reclaims it. Tell the server to drop us (clears our server-side viewer).
    const unsubEvicted = agentDeckWS.on('terminal:evicted', (payload: any) => {
      if (payload.agentId !== agentId) return;
      attachedRef.current = false;
      setEvicted(true);
      agentDeckWS.send('terminal:detach', { agentId });
    });

    if (agentDeckWS.connected) {
      setStatus('connected');
      maybeAttach();
    }

    return () => {
      unsubOutput();
      unsubOpen();
      unsubClose();
      unsubEvicted();
      agentDeckWS.send('terminal:detach', { agentId });
    };
  }, [agentId, maybeAttach, writeToTerm]);

  // Track a terminal-scoped selection for the 복사 button and touch scroll gate.
  // Snapshot the text so copy still works right after a redraw clears the highlight;
  // linger the button briefly (grace) instead of yanking it away on the next redraw.
  useEffect(() => {
    const onSelectionChange = () => {
      const sel = document.getSelection();
      const active = !!sel && !sel.isCollapsed && !!shellRef.current && shellRef.current.contains(sel.anchorNode);
      selectionActiveRef.current = active; // immediate — gates the renderer force-redraw
      if (active) {
        selectionTextRef.current = sel!.toString();
        clearTimeout(selGraceRef.current);
        setHasSelection(true);
      } else {
        clearTimeout(selGraceRef.current);
        selGraceRef.current = window.setTimeout(() => {
          selectionTextRef.current = '';
          setHasSelection(false);
        }, 4000);
      }
    };
    document.addEventListener('selectionchange', onSelectionChange);
    return () => document.removeEventListener('selectionchange', onSelectionChange);
  }, []);

  // Freeze output only while the mouse button is held (drag-selecting), then flush.
  useEffect(() => {
    const el = shellRef.current;
    if (!el) return;
    const onDown = () => { selectingRef.current = true; };
    const onUp = () => { flushPending(); };
    el.addEventListener('mousedown', onDown);
    window.addEventListener('mouseup', onUp);
    return () => {
      el.removeEventListener('mousedown', onDown);
      window.removeEventListener('mouseup', onUp);
    };
  }, [flushPending]);

  const selectAllTerminal = useCallback(() => {
    const el = shellRef.current?.querySelector('.wterm') as HTMLElement | null;
    const sel = window.getSelection();
    if (!el || !sel) return;
    sel.removeAllRanges();
    sel.selectAllChildren(el);
  }, []);

  // Send a mouse-wheel burst to the app (alt-screen apps own scrolling). Position
  // at the viewport centre; SGR encoding when the app enabled it.
  const sendAppWheel = useCallback((up: boolean, steps: number) => {
    const el = shellRef.current?.querySelector('.wterm') as HTMLElement | null;
    if (!el) return;
    const rect = el.getBoundingClientRect();
    const cols = wtRef.current?.cols || 80;
    const rows = wtRef.current?.rows || 24;
    const x = Math.max(1, Math.min(cols, Math.floor((rect.width / 2) / (rect.width / cols)) + 1));
    const y = Math.max(1, Math.min(rows, Math.floor((rect.height / 2) / (rect.height / rows)) + 1));
    const cb = up ? 64 : 65;
    const seq = sgrMouseRef.current
      ? `\x1b[<${cb};${x};${y}M`
      : `\x1b[M${String.fromCharCode(32 + cb)}${String.fromCharCode(32 + x)}${String.fromCharCode(32 + y)}`;
    let data = '';
    for (let i = 0; i < steps; i++) data += seq;
    if (data) agentDeckWS.send('terminal:input', { agentId, data });
  }, [agentId]);

  const scrollToBottom = useCallback(() => {
    const el = shellRef.current?.querySelector('.wterm') as HTMLElement | null;
    if (mouseTrackingRef.current) {
      sendAppWheel(false, 60); // alt-screen: burst of wheel-down toward the latest
    } else if (el) {
      el.scrollTop = el.scrollHeight; // native scrollback
    }
    scrollUpAccumRef.current = 0;
    setShowScrollBottom(false);
  }, [sendAppWheel]);

  const copySelection = useCallback(() => {
    const text = window.getSelection()?.toString() || selectionTextRef.current;
    if (!text) return;
    writeClipboard(text).then((ok) => {
      if (!ok) return;
      setCopied(true);
      window.setTimeout(() => setCopied(false), 1200);
    });
  }, []);

  // Scope the GUI select-all (Cmd+A) to the terminal when the user is working in
  // it — so it grabs the terminal's text, not the whole page. Ctrl+A is left alone
  // (it's readline's beginning-of-line inside a terminal).
  useEffect(() => {
    const onKeyDown = (e: KeyboardEvent) => {
      if (e.key !== 'a' || !e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return;
      const shell = shellRef.current;
      if (!shell) return;
      const sel = document.getSelection();
      const inTerminal =
        shell.contains(document.activeElement) ||
        (!!sel && !sel.isCollapsed && shell.contains(sel.anchorNode)) ||
        document.activeElement === document.body ||
        document.activeElement === null;
      if (inTerminal) {
        e.preventDefault();
        selectAllTerminal();
      }
    };
    document.addEventListener('keydown', onKeyDown);
    return () => document.removeEventListener('keydown', onKeyDown);
  }, [selectAllTerminal]);

  // Forward wheel / touch-drag as mouse-wheel events when the app has mouse
  // tracking on (Claude Code etc.). wterm itself doesn't report mouse events, so
  // without this the app never scrolls its conversation. Only attached while
  // tracking is on — a plain shell keeps wterm's native scrollback scroll.
  useEffect(() => {
    if (!mouseTracking) return;
    const shell = shellRef.current;
    const wtermEl = shell?.querySelector('.wterm') as HTMLElement | null;
    if (!shell || !wtermEl) return;

    const cellAt = (clientX: number, clientY: number) => {
      const rect = wtermEl.getBoundingClientRect();
      const cols = wtRef.current?.cols || 80;
      const rows = wtRef.current?.rows || 24;
      const x = Math.min(cols, Math.max(1, Math.floor((clientX - rect.left) / (rect.width / cols)) + 1));
      const y = Math.min(rows, Math.max(1, Math.floor((clientY - rect.top) / (rect.height / rows)) + 1));
      return { x, y };
    };
    const rowPx = () => {
      const rect = wtermEl.getBoundingClientRect();
      return rect.height / (wtRef.current?.rows || 24) || 18;
    };
    // SGR (1006) wheel: ESC[<64;x;yM = up, ESC[<65;x;yM = down. Fallback X10 when
    // the app only enabled legacy tracking.
    const wheelSeq = (up: boolean, x: number, y: number) => {
      const cb = up ? 64 : 65;
      if (sgrMouseRef.current) return `\x1b[<${cb};${x};${y}M`;
      return `\x1b[M${String.fromCharCode(32 + cb)}${String.fromCharCode(32 + x)}${String.fromCharCode(32 + y)}`;
    };
    const sendWheel = (up: boolean, clientX: number, clientY: number, steps: number) => {
      const { x, y } = cellAt(clientX, clientY);
      let data = '';
      for (let i = 0; i < steps; i++) data += wheelSeq(up, x, y);
      if (data) agentDeckWS.send('terminal:input', { agentId, data });
      markActive(); // keep repainting while the app's scroll redraws stream in
      // Net wheel direction → jump-to-bottom visibility (we can't read the app's
      // own scroll position). Clamped so it returns to 0 within a screen or two.
      scrollUpAccumRef.current = Math.max(0, Math.min(40, scrollUpAccumRef.current + (up ? steps : -steps)));
      setShowScrollBottom(scrollUpAccumRef.current > 1);
    };

    // Coalesce wheel events to ONE controlled send per frame. A trackpad fires
    // dozens of wheel events per second; forwarding each one floods the app with
    // scroll requests → it scrolls wildly and redraws every frame (janky + leaves
    // paint trails). Accumulating and flushing on rAF keeps scrolling smooth.
    let wheelAccum = 0;
    let wheelX = 0;
    let wheelY = 0;
    let wheelRaf = 0;
    const flushWheel = () => {
      wheelRaf = 0;
      if (Math.abs(wheelAccum) < 1) return;
      const up = wheelAccum < 0;
      const steps = Math.max(1, Math.min(3, Math.round(Math.abs(wheelAccum) / 50)));
      wheelAccum = 0;
      sendWheel(up, wheelX, wheelY, steps);
    };
    const onWheel = (e: WheelEvent) => {
      if (!e.deltaY) return;
      e.preventDefault();
      wheelAccum += e.deltaY;
      wheelX = e.clientX;
      wheelY = e.clientY;
      if (!wheelRaf) wheelRaf = requestAnimationFrame(flushWheel);
    };

    let startY = 0, lastY = 0, accum = 0, decided = false, scrolling = false;
    const onTouchStart = (e: TouchEvent) => {
      const t = e.touches[0];
      if (!t) return;
      startY = lastY = t.clientY;
      accum = 0; decided = false; scrolling = false;
    };
    const onTouchMove = (e: TouchEvent) => {
      const t = e.touches[0];
      if (!t) return;
      // Leave an active selection (handle drags) and stationary long-press alone.
      if (hasSelectionRef.current) return;
      if (!decided) {
        if (Math.abs(t.clientY - startY) < 8) return; // not yet a clear drag
        decided = true; scrolling = true; lastY = t.clientY;
      }
      if (!scrolling) return;
      e.preventDefault(); // we own the gesture → scroll the app, not the DOM
      accum += t.clientY - lastY;
      lastY = t.clientY;
      // Slightly less than a full row so a drag feels responsive, not stiff.
      const step = rowPx() * 0.8;
      while (Math.abs(accum) >= step) {
        const up = accum > 0; // finger down → reveal earlier content → wheel up
        sendWheel(up, t.clientX, t.clientY, 1);
        accum += up ? -step : step;
      }
    };
    const onTouchEnd = () => { decided = false; scrolling = false; };

    shell.addEventListener('wheel', onWheel, { passive: false });
    wtermEl.addEventListener('touchstart', onTouchStart, { passive: false });
    wtermEl.addEventListener('touchmove', onTouchMove, { passive: false });
    wtermEl.addEventListener('touchend', onTouchEnd);
    wtermEl.addEventListener('touchcancel', onTouchEnd);
    return () => {
      if (wheelRaf) cancelAnimationFrame(wheelRaf);
      shell.removeEventListener('wheel', onWheel);
      wtermEl.removeEventListener('touchstart', onTouchStart);
      wtermEl.removeEventListener('touchmove', onTouchMove);
      wtermEl.removeEventListener('touchend', onTouchEnd);
      wtermEl.removeEventListener('touchcancel', onTouchEnd);
    };
  }, [mouseTracking, agentId, markActive]);

  // Non-alt-screen (scrollback) case: toggle the jump-to-bottom button from the
  // native scroll position of the .wterm scroll container.
  useEffect(() => {
    if (!coreReady) return;
    let el: HTMLElement | null = null;
    let timer = 0;
    const onScroll = () => {
      if (!el || mouseTrackingRef.current) return;
      setShowScrollBottom(el.scrollHeight - el.scrollTop - el.clientHeight > 12);
    };
    const attach = (tries: number) => {
      el = shellRef.current?.querySelector('.wterm') as HTMLElement | null;
      if (el) { el.addEventListener('scroll', onScroll, { passive: true }); onScroll(); }
      else if (tries < 20) timer = window.setTimeout(() => attach(tries + 1), 100);
    };
    attach(0);
    return () => { clearTimeout(timer); el?.removeEventListener('scroll', onScroll); };
  }, [coreReady, mouseTracking]);

  // Diagnostic (?debug): measure real rendered advances vs the single-char width
  // the layout assumes, plus computed-vs-actual cols. Reveals non-monospace fonts,
  // wide-char drift, and PTY/grid size mismatches.
  useEffect(() => {
    if (!debug) return;
    const measure = () => {
      const wtermEl = shellRef.current?.querySelector('.wterm') as HTMLElement | null;
      const gridEl = shellRef.current?.querySelector('.term-grid') as HTMLElement | null;
      if (!wtermEl) return;
      const probe = document.createElement('div');
      probe.style.cssText = 'position:absolute;visibility:hidden;white-space:pre;left:-9999px;top:0;';
      wtermEl.appendChild(probe);
      const widthOf = (s: string) => { probe.textContent = s; return probe.getBoundingClientRect().width; };
      const one = widthOf('0');
      const ten0 = widthOf('0000000000') / 10;
      const tenM = widthOf('MMMMMMMMMM') / 10;
      const tenI = widthOf('iiiiiiiiii') / 10;
      const tenKo = widthOf('가나다라마바사아자차') / 10;
      probe.remove();
      const cs = getComputedStyle(wtermEl);
      const contentW = wtermEl.clientWidth - parseFloat(cs.paddingLeft) - parseFloat(cs.paddingRight);
      const computedCols = one > 0 ? Math.floor(contentW / one) : 0;
      const gridW = gridEl ? gridEl.getBoundingClientRect().width : 0;
      const info = [
        `font: ${cs.fontFamily.split(',')[0]} ${cs.fontSize}`,
        `'0'=${one.toFixed(2)} 0x10=${ten0.toFixed(2)} M=${tenM.toFixed(2)} i=${tenI.toFixed(2)} 한=${tenKo.toFixed(2)}`,
        `mono: ${Math.abs(ten0 - one) < 0.15 && Math.abs(tenM - one) < 0.15 && Math.abs(tenI - one) < 0.15 ? 'YES' : 'NO ⚠'}`,
        `한/0 ratio=${(tenKo / one).toFixed(2)} (want ~2.0)`,
        `contentW=${contentW.toFixed(0)} gridW=${gridW.toFixed(0)} computedCols=${computedCols} wt.cols=${wtRef.current?.cols ?? '?'} wt.rows=${wtRef.current?.rows ?? '?'}`,
      ].join('\n');
      setDebugInfo(info);
    };
    const id = window.setInterval(measure, 1000);
    const t = window.setTimeout(measure, 400);
    return () => { window.clearInterval(id); window.clearTimeout(t); };
  }, [debug]);

  // Re-fit line-spacing AND letter-spacing to the container/font. wterm re-measures
  // the char width (→ cols, "자간") on a container resize, but its row height
  // (→ line-spacing, "줄간격") is measured only once at init and never updated —
  // so a resize or a late-loading web font leaves the row spacing stale. Re-measure
  // the current font's line height into --term-row-height and nudge the width to
  // force wterm's own char re-measure, on font load AND on every container resize.
  useEffect(() => {
    if (!coreReady) return;
    let disposed = false;
    let t = 0;
    const refit = () => {
      if (disposed) return;
      const el = shellRef.current?.querySelector('.wterm') as HTMLElement | null;
      if (!el) return;
      // 줄간격: natural line height of the current font (inherits --term-font-size
      // and the terminal's line-height).
      const probe = document.createElement('span');
      probe.style.cssText = 'position:absolute;visibility:hidden;white-space:pre;top:-9999px;left:-9999px;';
      probe.textContent = 'Wg가';
      el.appendChild(probe);
      const h = probe.getBoundingClientRect().height;
      probe.remove();
      if (h > 0) el.style.setProperty('--term-row-height', `${Math.ceil(h)}px`);
      // 자간/cols: fire wterm's ResizeObserver so it re-measures char width.
      const restore = el.style.width;
      el.style.width = `${Math.max(0, el.clientWidth - 1)}px`;
      requestAnimationFrame(() => {
        if (disposed) return;
        el.style.width = restore;
        // Reflow of the grid on resize is the other place WebKit strands old paint;
        // pump repaints for a moment once the new width has been applied.
        requestAnimationFrame(() => { if (!disposed) markActive(); });
      });
    };
    const debouncedRefit = () => { clearTimeout(t); t = window.setTimeout(refit, 80); };

    const fonts = (document as any).fonts;
    fonts?.ready?.then?.(() => refit());
    fonts?.load?.('400 14px "Nanum Gothic Coding"', '가나다ABC').then(() => refit()).catch(() => {});

    const shell = shellRef.current;
    const ro = shell ? new ResizeObserver(debouncedRefit) : null;
    ro?.observe(shell!);
    refit();

    return () => { disposed = true; clearTimeout(t); ro?.disconnect(); };
  }, [coreReady, markActive]);

  const onData = useCallback((data: string) => {
    if (onHangulDirectRef.current && HANGUL_RE.test(data)) onHangulDirectRef.current();
    agentDeckWS.send('terminal:input', { agentId, data });
  }, [agentId]);

  const onResize = useCallback((cols: number, rows: number) => {
    colsRef.current = cols;
    rowsRef.current = rows;
    // Forward to the PTY when we're this session's active viewer. NOT while evicted
    // — an inactive device resizing (e.g. orientation change) would resize the PTY
    // out from under the device that's actually in use. (Not gated on attachedRef so
    // wterm's initial pre-attach measurement still reaches the PTY.)
    if (agentDeckWS.connected && !evictedRef.current) {
      agentDeckWS.send('terminal:resize', { agentId, cols, rows });
    }
  }, [agentId]);

  const onReady = useCallback((wt: WTerm) => {
    wtRef.current = wt;
    colsRef.current = wt.cols;
    rowsRef.current = wt.rows;
    readyRef.current = true;
    // wterm focuses its hidden textarea on init, popping the iOS soft keyboard over
    // the panel on mount. On touch we type via the Prompt Bar, so blur it — a real
    // tap still focuses to type, but scroll / long-press selection aren't fighting a
    // keyboard that appeared unbidden.
    if (isTouchDevice) {
      requestAnimationFrame(() => (document.activeElement as HTMLElement | null)?.blur?.());
    }
    maybeAttach();
    // Guarantee the PTY gets the real measured size: by the next frame wterm's
    // ResizeObserver has fit the grid to the container, so push that size even if
    // its onResize fired before we were attached.
    requestAnimationFrame(() => {
      const term = wtRef.current;
      if (term && agentDeckWS.connected) {
        colsRef.current = term.cols;
        rowsRef.current = term.rows;
        agentDeckWS.send('terminal:resize', { agentId, cols: term.cols, rows: term.rows });
      }
    });
  }, [maybeAttach, isTouchDevice, agentId]);

  return (
    <div ref={shellRef} className="relative w-full h-full wterm-shell">
      {status !== 'connected' && (
        <div className="absolute top-0 left-0 right-0 z-10 px-3 py-1.5 text-xs flex items-center gap-2 pointer-events-none"
             style={{
               background: status === 'disconnected' ? 'rgba(239,68,68,0.15)' : 'rgba(245,158,11,0.15)',
               color: status === 'disconnected' ? '#ef4444' : '#f59e0b',
             }}>
          <span className={`w-2 h-2 rounded-full shrink-0 ${status !== 'disconnected' ? 'animate-pulse' : ''}`}
                style={{ background: status === 'disconnected' ? '#ef4444' : '#f59e0b' }} />
          {status === 'disconnected' ? 'Disconnected — reconnecting...' : 'Connecting...'}
        </div>
      )}

      {evicted && (
        <div className="absolute inset-0 z-40 flex flex-col items-center justify-center gap-3 px-6 text-center bg-deck-bg/95">
          <div className="text-2xl">🖥️📱</div>
          <div className="text-sm text-deck-text-dim max-w-xs">
            다른 기기에서 이 세션을 열었습니다.<br />한 번에 한 기기에서만 볼 수 있어요.
          </div>
          <button
            onClick={reclaim}
            className="px-4 py-2 rounded-lg text-sm font-semibold shadow-lg touch-manipulation active:opacity-70 bg-deck-accent text-white"
          >
            이 기기에서 다시 열기
          </button>
        </div>
      )}

      {coreReady && (
      <Terminal
        ref={handleRef}
        {...(core ? { core } : {})}
        autoResize
        cursorBlink
        onData={onData}
        onResize={onResize}
        onReady={onReady}
        onClick={() => { if (!isTouchDevice) onFocusTerminal?.(); }}
        // alt-noscroll: a mouse-tracking app (Claude Code) owns its own scrolling and
        // its screen exactly fills the viewport, so .wterm needs no scroll container.
        // Dropping overflow:auto removes the scroll COMPOSITING LAYER that WebKit
        // strands stale paint in during scroll (the "잔상" that survives a full grid
        // re-render, because it lives on .wterm — not .term-grid). Plain shells keep
        // overflow:auto for native scrollback.
        className={`wterm-embed w-full h-full${mouseTracking ? ' alt-noscroll' : ''}`}
        style={{
          // Bundled Nanum Gothic Coding — Latin AND Hangul from ONE fixed-width
          // font at a 1:2 advance ratio, so the terminal grid (which assumes
          // CJK = 2 cells) stays aligned on every device. monospace is only a
          // last-resort fallback for glyphs the font lacks.
          ['--term-font-family' as any]: "'Nanum Gothic Coding', monospace",
          ['--term-font-size' as any]: `${resolvedFontSize}px`,
          ['--term-bg' as any]: '#0a0a0f',
          ['--term-fg' as any]: '#e2e8f0',
          ['--term-cursor' as any]: '#6366f1',
        }}
      />
      )}

      {debug && debugInfo && (
        <pre className="absolute top-1 left-1 z-30 px-2 py-1 rounded text-[9px] leading-tight whitespace-pre
                        pointer-events-none bg-black/80 text-green-300 border border-green-500/40 max-w-[95%] overflow-hidden">
          {debugInfo}
        </pre>
      )}

      {/* Jump to the latest — shown when scrolled up (native scrollback or an
          alt-screen app's own scroll). */}
      {showScrollBottom && (
        <button
          onMouseDown={(e) => { e.preventDefault(); scrollToBottom(); }}
          onTouchStart={(e) => { e.preventDefault(); scrollToBottom(); }}
          className="absolute bottom-3 left-1/2 -translate-x-1/2 z-20 w-9 h-9 flex items-center justify-center
                     rounded-full shadow-lg touch-manipulation active:opacity-70 bg-deck-accent text-white text-lg leading-none"
          title="최하단으로 이동"
        >
          ↓
        </button>
      )}

      {/* Touch-friendly selection controls. Native long-press selection also works;
          these make it reliable in a scrolling terminal. mousedown/touchstart with
          preventDefault so tapping them doesn't clear the selection first. */}
      {isTouchDevice && (
        <div className="absolute bottom-2 right-2 z-20 flex gap-1.5">
          <button
            onMouseDown={(e) => { e.preventDefault(); selectAllTerminal(); }}
            onTouchStart={(e) => { e.preventDefault(); selectAllTerminal(); }}
            className="px-3 py-1.5 rounded-lg text-xs font-semibold shadow-lg touch-manipulation active:opacity-70
                       bg-deck-surface/90 text-deck-text-dim border border-deck-border"
            title="터미널 내용 전체 선택"
          >
            전체선택
          </button>
          {(hasSelection || copied) && (
            <button
              onMouseDown={(e) => { e.preventDefault(); copySelection(); }}
              onTouchStart={(e) => { e.preventDefault(); copySelection(); }}
              className="px-3 py-1.5 rounded-lg text-xs font-semibold shadow-lg touch-manipulation active:opacity-70
                         bg-deck-accent text-white"
              title="선택 영역 복사"
            >
              {copied ? '복사됨 ✓' : '복사'}
            </button>
          )}
        </div>
      )}
    </div>
  );
});
