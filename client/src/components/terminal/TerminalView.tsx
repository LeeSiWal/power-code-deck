import { useEffect, useRef, useState, useCallback, forwardRef, useImperativeHandle } from 'react';
import { Terminal } from '@xterm/xterm';
import { FitAddon } from '@xterm/addon-fit';
import { Unicode11Addon } from '@xterm/addon-unicode11';
import { WebLinksAddon } from '@xterm/addon-web-links';
import '@xterm/xterm/css/xterm.css';
import { agentDeckWS } from '../../lib/ws';
import { useDevice } from '../../hooks/useDevice';

export interface TerminalHandle {
  /** Imperatively move keyboard focus into the terminal (used after Prompt Bar Send). */
  focus: () => void;
  /**
   * Send a control / navigation key to the PTY. Arrow keys are translated to the
   * application-cursor-key form (ESC O A/B/C/D) when the TUI has DECCKM enabled —
   * the hardcoded normal-mode bytes (ESC [ A/B/C/D) don't work in apps like Claude
   * Code that put the terminal in application cursor mode for their arrow menus.
   */
  sendKey: (data: string) => void;
}

/**
 * Normal-mode cursor sequences (ESC [ x) → application-mode (ESC O x). When a TUI
 * enables DECCKM (application cursor keys), the app expects the SS3 form; sending
 * the CSI form makes arrow keys do nothing or behave erratically in its menus.
 */
const APP_CURSOR_MAP: Record<string, string> = {
  '\x1b[A': '\x1bOA',
  '\x1b[B': '\x1bOB',
  '\x1b[C': '\x1bOC',
  '\x1b[D': '\x1bOD',
};

/** Open a URL from the terminal in a new tab (login links, docs, localhost, …). */
function openTerminalLink(uri: string) {
  try {
    window.open(uri, '_blank', 'noopener,noreferrer');
  } catch {
    /* popup blocked — ignore */
  }
}

/**
 * Synchronous clipboard copy via a hidden textarea + execCommand. Works within
 * a user gesture even in non-secure (http) contexts — e.g. a LAN / home-server
 * URL like http://192.168.x.x:33033 — where navigator.clipboard is unavailable
 * or rejects. Must run synchronously inside the gesture (no preceding await).
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
    ta.style.padding = '0';
    ta.style.border = 'none';
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
 * Copy text to the clipboard. In a secure context (https / localhost) it uses
 * the async Clipboard API (no focus steal). In a non-secure context (LAN http)
 * navigator.clipboard is missing or rejects, so it goes STRAIGHT to the
 * synchronous execCommand path — awaiting the rejecting promise first would
 * spend the user gesture and make the fallback fail too.
 */
async function writeClipboard(text: string): Promise<boolean> {
  if (!text) return false;
  if (window.isSecureContext && navigator.clipboard?.writeText) {
    try {
      await navigator.clipboard.writeText(text);
      return true;
    } catch {
      /* fall through to the synchronous path */
    }
  }
  return legacyCopy(text);
}

/** Read text from the clipboard, or '' when unavailable/denied. */
async function readClipboard(): Promise<string> {
  try {
    if (navigator.clipboard?.readText) return await navigator.clipboard.readText();
  } catch {
    /* denied / non-secure context */
  }
  return '';
}

/** Decode a base64 payload as UTF-8 (so Korean in an OSC 52 sequence survives). */
function decodeBase64Utf8(b64: string): string {
  try {
    const bin = atob(b64.trim());
    const bytes = Uint8Array.from(bin, (c) => c.charCodeAt(0));
    return new TextDecoder().decode(bytes);
  } catch {
    return '';
  }
}

interface TerminalViewProps {
  agentId: string;
  fontSize?: number;
  /**
   * While `true` the Prompt Bar owns the keyboard, so the terminal must not
   * auto-grab focus (font/resize/visibility events would otherwise steal it
   * mid-typing). Explicit user taps and imperative focus() still work.
   */
  focusGuardRef?: React.MutableRefObject<boolean>;
  /** Fired when the user focuses the terminal directly (pointer down). */
  onFocusTerminal?: () => void;
  /** Touch devices: fired when a Hangul char is typed straight into the terminal
   * (bypassing the Prompt Bar), so the parent can nudge the user. */
  onHangulDirect?: () => void;
}

export const TerminalView = forwardRef<TerminalHandle, TerminalViewProps>(function TerminalView(
  { agentId, fontSize, focusGuardRef, onFocusTerminal, onHangulDirect },
  ref,
) {
  const termRef = useRef<HTMLDivElement>(null);
  const terminalRef = useRef<Terminal | null>(null);
  const fitAddonRef = useRef<FitAddon | null>(null);
  const { isMobile, isTablet, isTouchDevice } = useDevice();

  // Kept in a ref so the (heavy) terminal effect doesn't re-run when the prop
  // identity changes.
  const onHangulDirectRef = useRef(onHangulDirect);
  onHangulDirectRef.current = onHangulDirect;

  useImperativeHandle(ref, () => ({
    focus: () => terminalRef.current?.focus(),
    sendKey: (data: string) => {
      const term = terminalRef.current;
      const out = term?.modes.applicationCursorKeysMode ? (APP_CURSOR_MAP[data] ?? data) : data;
      agentDeckWS.send('terminal:input', { agentId, data: out });
    },
  }), [agentId]);
  const [status, setStatus] = useState<'connecting' | 'connected' | 'disconnected'>('connecting');
  const [hasOutput, setHasOutput] = useState(false);
  const [hasSelection, setHasSelection] = useState(false);
  const [copied, setCopied] = useState(false);
  // Touch text-selection mode. On touch a one-finger drag scrolls the viewport, so
  // there is no way to drag-select. When this is on, drags are turned into an
  // xterm selection instead (and native scroll is suspended); tap the toggle again
  // to go back to scrolling.
  const [selectMode, setSelectMode] = useState(false);

  const statusRef = useRef(status);
  statusRef.current = status;
  const hasOutputRef = useRef(hasOutput);
  hasOutputRef.current = hasOutput;
  // Last non-empty selection text. A live TUI (Claude Code) redraws constantly
  // and xterm clears the selection on each write, so we snapshot the selected
  // text the moment it exists and copy that even after the highlight is gone.
  const selectionTextRef = useRef('');

  // Copy the current (or last-captured) terminal selection. Used by the floating
  // 복사 button and, via copyTerminalSelection, the Cmd+C / Ctrl+Shift+C handler.
  const copySelection = useCallback(() => {
    const sel = terminalRef.current?.getSelection() || selectionTextRef.current;
    if (!sel) return;
    writeClipboard(sel).then((ok) => {
      if (!ok) return;
      setCopied(true);
      window.setTimeout(() => setCopied(false), 1200);
    });
  }, []);

  const resolvedFontSize = fontSize ?? (isMobile ? 12 : isTablet ? 13 : 14);

  useEffect(() => {
    const container = termRef.current;
    if (!container) return;

    let disposed = false;
    let attached = false;
    const settleTimers: number[] = [];
    let postRenderTimer = 0;
    let initialRenderSettled = false;
    const ua = navigator.userAgent;
    const isAppleTouchWebKit =
      isTouchDevice &&
      /AppleWebKit/i.test(ua) &&
      (/iP(ad|hone|od)/i.test(ua) || (navigator.platform === 'MacIntel' && navigator.maxTouchPoints > 1));

    const terminal = new Terminal({
      fontSize: resolvedFontSize,
      // Use monospace CJK fallbacks only. 'Noto Sans KR' is proportional and made
      // Korean render at uneven widths in the terminal; D2Coding / NanumGothicCoding
      // / Noto Sans Mono CJK are fixed-width and align to the terminal grid.
      fontFamily: "'JetBrains Mono', 'D2Coding', 'NanumGothicCoding', 'Noto Sans Mono CJK KR', 'Sarasa Mono K', monospace",
      theme: {
        background: '#0a0a0f',
        foreground: '#e2e8f0',
        cursor: '#6366f1',
        cursorAccent: '#0a0a0f',
        selectionBackground: '#6366f140',
        selectionForeground: '#ffffff',
        black: '#1e1e2e',
        red: '#ef4444',
        green: '#22c55e',
        yellow: '#f59e0b',
        blue: '#6366f1',
        magenta: '#a855f7',
        cyan: '#06b6d4',
        white: '#e2e8f0',
        brightBlack: '#64748b',
        brightRed: '#f87171',
        brightGreen: '#4ade80',
        brightYellow: '#fbbf24',
        brightBlue: '#818cf8',
        brightMagenta: '#c084fc',
        brightCyan: '#22d3ee',
        brightWhite: '#f8fafc',
      },
      cursorBlink: true,
      scrollback: 3000,
      scrollSensitivity: isTouchDevice ? 1.1 : 1.16,
      // Touch: 0 (no JS smooth-scroll animation). The viewport scrolls natively via
      // iOS momentum; xterm's own animated scroll on top of that fought the native
      // inertia and made finger-scrolling stutter. Desktop keeps a short animation.
      smoothScrollDuration: isTouchDevice ? 0 : 180,
      allowTransparency: false,
      // Single interactive terminal: xterm always accepts input and forwards it
      // straight to the PTY. Short shell ops, Claude choices, y/n, arrows, Ctrl+C
      // are typed here directly; the Prompt Bar is only for Korean / long text.
      disableStdin: false,
      allowProposedApi: true,
      scrollOnUserInput: false,
      // TUIs like Claude Code enable mouse reporting, so a plain drag is sent to
      // the app as mouse events instead of selecting text — making copy
      // impossible. Let the user force a local text selection by holding a
      // modifier while dragging: Option(⌥) on macOS, Shift elsewhere (xterm's
      // built-in default). rightClickSelectsWord adds a quick word grab.
      macOptionClickForcesSelection: true,
      rightClickSelectsWord: true,
      // Make OSC 8 hyperlinks (e.g. Claude Code's login link) clickable — open
      // them in a new tab. Without this, xterm renders the link but clicks do
      // nothing. Plain-text URLs are handled by WebLinksAddon below.
      linkHandler: {
        activate: (_event, uri) => openTerminalLink(uri),
      },
    });

    const fitAddon = new FitAddon();
    terminal.loadAddon(fitAddon);
    // Plain-text http(s) URLs (works across soft-wrapped lines, so a long login
    // URL that wraps is still one clickable link).
    terminal.loadAddon(new WebLinksAddon((_event, uri) => openTerminalLink(uri)));
    try {
      const unicodeAddon = new Unicode11Addon();
      terminal.loadAddon(unicodeAddon);
      terminal.unicode.activeVersion = '11';
    } catch {}

    terminalRef.current = terminal;
    fitAddonRef.current = fitAddon;

    // All keystrokes typed into the terminal go straight to the PTY. Korean is
    // entered via the Prompt Bar (mobile soft keyboards split jamo when typed
    // directly into xterm); if a Hangul char arrives here on a touch device the
    // user bypassed the Prompt Bar, so notify the parent to nudge them.
    const HANGUL_RE = /[ᄀ-ᇿ㄰-㆏가-힣]/;
    const dataDisposable = terminal.onData((data) => {
      if (isTouchDevice && onHangulDirectRef.current && HANGUL_RE.test(data)) {
        onHangulDirectRef.current();
      }
      agentDeckWS.send('terminal:input', { agentId, data });
    });

    // --- Copy / paste ---
    // xterm renders to a canvas, so its selection is NOT a DOM selection and the
    // browser's native Cmd+C copies nothing. Wire clipboard explicitly:
    //   • Copy  — Cmd+C (macOS) or Ctrl+Shift+C, only when there is a selection
    //             (so a bare Ctrl+C still sends SIGINT to the shell).
    //   • Paste — Cmd+V is handled natively by xterm's textarea; Ctrl+Shift+V
    //             (Linux/Windows terminal convention) is wired here.
    const copyTerminalSelection = () => {
      const sel = terminal.getSelection() || selectionTextRef.current;
      if (!sel) return;
      writeClipboard(sel).then((ok) => {
        if (!ok) return;
        setCopied(true);
        window.setTimeout(() => setCopied(false), 1200);
      });
    };
    const pasteToTerminal = async () => {
      const text = await readClipboard();
      if (text) agentDeckWS.send('terminal:pasteOnly', { agentId, text, mode: 'bracketed-paste' });
    };

    // OSC 52 — apps like Claude Code copy the mouse selection to the system
    // clipboard by emitting an OSC 52 sequence ("sent N chars via OSC 52").
    // xterm ignores it by default, so a plain drag never reached the clipboard.
    // Handle it here → a normal drag copies without needing Option/Shift.
    terminal.parser.registerOscHandler(52, (data) => {
      const semi = data.indexOf(';');           // "c;<base64>" → payload after ';'
      const payload = semi === -1 ? data : data.slice(semi + 1);
      if (!payload || payload === '?') return true; // query, not a set — ignore
      const text = decodeBase64Utf8(payload);
      if (text) {
        writeClipboard(text).then((ok) => {
          if (!ok) return;
          setCopied(true);
          window.setTimeout(() => setCopied(false), 1200);
        });
      }
      return true; // handled
    });
    terminal.attachCustomKeyEventHandler((e) => {
      if (e.type !== 'keydown') return true;
      const isCopy =
        (e.metaKey && !e.shiftKey && !e.ctrlKey && e.code === 'KeyC') ||
        (e.ctrlKey && e.shiftKey && e.code === 'KeyC');
      if (isCopy) {
        // Also honor a just-captured selection that a redraw already cleared.
        if (terminal.hasSelection() || selectionTextRef.current) {
          // preventDefault so the browser's native (empty) copy doesn't clobber
          // the clipboard text we write ourselves.
          e.preventDefault();
          copyTerminalSelection();
          return false;
        }
        return true; // no selection → let Ctrl+C pass through as SIGINT
      }
      if (e.ctrlKey && e.shiftKey && e.code === 'KeyV') {
        e.preventDefault();
        pasteToTerminal();
        return false;
      }
      return true;
    });
    // Snapshot the selection as soon as it exists. A live TUI wipes it on the
    // next redraw, so keep the captured text + copy button alive for a grace
    // period after the highlight disappears.
    let selHideTimer = 0;
    const selectionDisposable = terminal.onSelectionChange(() => {
      const sel = terminal.getSelection();
      if (sel) {
        selectionTextRef.current = sel;
        clearTimeout(selHideTimer);
        setHasSelection(true);
      } else {
        clearTimeout(selHideTimer);
        selHideTimer = window.setTimeout(() => {
          selectionTextRef.current = '';
          setHasSelection(false);
        }, 5000);
      }
    });

    // Auto-focus that must yield to the Prompt Bar and never pop the mobile
    // keyboard. Explicit taps / imperative focus() bypass this.
    const autoFocusTerminal = () => {
      if (disposed) return;
      if (isTouchDevice) return;
      if (focusGuardRef?.current) return;
      terminal.focus();
    };

    // --- safeFit: skip same size, double-RAF, then focus ---
    let lastW = 0;
    let lastH = 0;
    let fitRaf = 0;
    let openRaf = 0;
    let hasOpened = false;
    const getViewport = () => container.querySelector('.xterm-viewport') as HTMLDivElement | null;
    const getScrollArea = () => container.querySelector('.xterm-scroll-area') as HTMLDivElement | null;

    const syncTerminalSize = () => {
      if (!attached || disposed || !hasOpened) return;
      agentDeckWS.send('terminal:resize', {
        agentId,
        cols: terminal.cols,
        rows: terminal.rows,
      });
    };

    const attachTerminal = () => {
      if (disposed || attached || !hasOpened) return;
      attached = true;
      agentDeckWS.send('terminal:attach', {
        agentId,
        cols: terminal.cols,
        rows: terminal.rows,
      });
    };

    const refreshViewportScroll = () => {
      const viewport = getViewport();
      const scrollArea = getScrollArea();
      if (!viewport) return;

      if (isAppleTouchWebKit) {
        viewport.style.setProperty('-webkit-overflow-scrolling', 'auto');
        viewport.style.overflowY = 'hidden';
        void viewport.offsetHeight;
        viewport.style.overflowY = 'scroll';
        viewport.style.setProperty('-webkit-overflow-scrolling', 'touch');
      } else {
        viewport.style.setProperty('-webkit-overflow-scrolling', 'touch');
        viewport.style.overflowY = 'scroll';
      }

      viewport.style.overscrollBehaviorY = 'contain';
      viewport.style.touchAction = 'auto';
      viewport.style.pointerEvents = 'auto';
      if (scrollArea) {
        scrollArea.style.pointerEvents = 'auto';
      }

      // Force WebKit to rebuild the scrollable viewport after reload.
      const top = viewport.scrollTop;
      viewport.scrollTop = top + 1;
      void viewport.offsetHeight;
      viewport.scrollTop = top;
    };

    const forceViewportResizeCycle = () => {
      if (!isAppleTouchWebKit) return;
      if (terminal.cols < 2 || terminal.rows < 2) return;

      const cols = terminal.cols;
      const rows = terminal.rows;

      // iOS Safari sometimes keeps xterm's viewport inert until a real resize path runs.
      terminal.resize(cols, rows - 1);
      terminal.resize(cols, rows);
    };

    const safeFit = (force = false, reattach = false) => {
      if (disposed || !hasOpened) return;
      if (!container.isConnected) return;

      const w = container.clientWidth;
      const h = container.clientHeight;
      if (w <= 0 || h <= 0) return;
      if (!force && w === lastW && h === lastH) return;
      lastW = w;
      lastH = h;

      cancelAnimationFrame(fitRaf);
      fitRaf = requestAnimationFrame(() => {
        requestAnimationFrame(() => {
          if (disposed) return;
          try { fitAddon.fit(); } catch { return; }
          forceViewportResizeCycle();
          terminal.refresh(0, Math.max(terminal.rows - 1, 0));
          refreshViewportScroll();
          syncTerminalSize();
        });
      });
    };

    // --- bindReady: safeFit + focus (called after pageshow, vv resize, fonts) ---
    const bindReady = (reattach = false) => {
      if (!hasOpened) return;
      lastW = 0; lastH = 0;
      safeFit(true, reattach);
      requestAnimationFrame(() => {
        requestAnimationFrame(() => {
          autoFocusTerminal();
        });
      });
    };

    const scheduleViewportSettle = (reattach = false) => {
      if (!hasOpened) return;
      bindReady(reattach);
      if (!isAppleTouchWebKit) return;

      [32, 96, 180, 320].forEach((delay) => {
        const timer = window.setTimeout(() => {
          bindReady(false);
        }, delay);
        settleTimers.push(timer);
      });
    };

    const schedulePostRenderSettle = () => {
      if (!isAppleTouchWebKit || disposed || !hasOpened) return;
      clearTimeout(postRenderTimer);
      postRenderTimer = window.setTimeout(() => {
        if (disposed) return;
        bindReady(false);
      }, 40);
    };

    const openWhenReady = (attempt = 0) => {
      if (disposed || hasOpened) return;
      if (!container.isConnected) return;

      const w = container.clientWidth;
      const h = container.clientHeight;
      if (w <= 0 || h <= 0) {
        openRaf = requestAnimationFrame(() => openWhenReady(attempt + 1));
        return;
      }

      // On direct page reload Safari often needs a couple of stable frames before xterm.open.
      if (attempt < 2) {
        openRaf = requestAnimationFrame(() => openWhenReady(attempt + 1));
        return;
      }

      terminal.open(container);
      hasOpened = true;
      lastW = 0;
      lastH = 0;

      if (agentDeckWS.connected) {
        attachTerminal();
        scheduleViewportSettle(true);
        setStatus('connected');
      }
    };
    openWhenReady();

    const unsubConnect = agentDeckWS.on('open', () => {
      attached = false;
      if (!hasOpened) return;
      attachTerminal();
      scheduleViewportSettle(true);
      setStatus('connected');
    });
    const unsubDisconnect = agentDeckWS.on('close', () => {
      setStatus('disconnected');
    });

    // --- Output ---
    const unsubOutput = agentDeckWS.on('terminal:output', (payload: any) => {
      if (payload.agentId === agentId) {
        if (!hasOpened) return;
        terminal.write(payload.data);
        if (!hasOutputRef.current) scheduleViewportSettle(false);
        if (!hasOutputRef.current) setHasOutput(true);
        if (statusRef.current !== 'connected') setStatus('connected');
      }
    });
    const unsubRender = terminal.onRender(() => {
      if (!hasOpened) return;
      if (initialRenderSettled) return;
      initialRenderSettled = true;
      schedulePostRenderSettle();
      settleTimers.push(window.setTimeout(() => schedulePostRenderSettle(), 120));
      settleTimers.push(window.setTimeout(() => schedulePostRenderSettle(), 260));
    });

    // ========================================
    // Event sources: safeFit + focus
    // ========================================

    // 1. ResizeObserver — container size changes
    const ro = new ResizeObserver(() => safeFit());
    ro.observe(container);

    // 2. pageshow — initial load + bfcache restore → full rebind
    const onPageShow = () => scheduleViewportSettle(false);
    window.addEventListener('pageshow', onPageShow);

    const onWindowLoad = () => scheduleViewportSettle(false);
    window.addEventListener('load', onWindowLoad);

    const onVisibilityChange = () => {
      if (document.visibilityState === 'visible') scheduleViewportSettle(false);
    };
    document.addEventListener('visibilitychange', onVisibilityChange);

    // 3. visualViewport.resize — debounced
    const vv = window.visualViewport;
    let vvTimer = 0;
    const onVVResize = () => {
      clearTimeout(vvTimer);
      vvTimer = window.setTimeout(() => {
        // On touch the terminal never holds the soft keyboard (Korean / long text
        // goes through the Prompt Bar), so a visualViewport resize here is almost
        // always the iOS address bar collapsing as the user scrolls. Running the
        // full settle (refit + viewport rebuild + scrollTop poke) mid-scroll makes
        // it stutter — a cheap size-guarded fit keeps resize correct without the
        // jank. Real container changes (Prompt Bar open/close) are handled by the
        // ResizeObserver anyway. Desktop keeps the full settle for keyboard focus.
        if (isTouchDevice) safeFit();
        else scheduleViewportSettle(false);
      }, 100);
    };
    vv?.addEventListener('resize', onVVResize);

    // 4. Web fonts
    document.fonts?.ready?.then(() => scheduleViewportSettle(false));

    // 5. pointerdown on container → focus terminal (explicit user intent).
    // Skipped on touch so tapping to scroll/select never pops the keyboard;
    // touch users type via the Prompt Bar and mobile toolbar instead.
    const onPointerDown = () => {
      if (isTouchDevice) return;
      terminal.focus();
      onFocusTerminal?.();
    };
    container.addEventListener('pointerdown', onPointerDown, { passive: true });

    // Initial
    if (hasOpened) safeFit();

    return () => {
      disposed = true;
      cancelAnimationFrame(fitRaf);
      cancelAnimationFrame(openRaf);
      clearTimeout(postRenderTimer);
      clearTimeout(vvTimer);
      settleTimers.forEach((timer) => clearTimeout(timer));
      unsubOutput();
      unsubRender.dispose();
      unsubConnect();
      unsubDisconnect();
      agentDeckWS.send('terminal:detach', { agentId });
      ro.disconnect();
      window.removeEventListener('pageshow', onPageShow);
      window.removeEventListener('load', onWindowLoad);
      document.removeEventListener('visibilitychange', onVisibilityChange);
      vv?.removeEventListener('resize', onVVResize);
      container.removeEventListener('pointerdown', onPointerDown);
      clearTimeout(selHideTimer);
      dataDisposable.dispose();
      selectionDisposable.dispose();
      terminal.dispose();
      terminalRef.current = null;
      fitAddonRef.current = null;
    };
  }, [agentId, isTouchDevice, resolvedFontSize]);

  // Touch text-selection mode. The listeners are attached ONLY while select mode is
  // on — a persistent non-passive touchmove listener would stop the browser from
  // fast-pathing normal scrolling and reintroduce stutter. When on, a one-finger
  // drag is translated into synthetic mouse events so xterm's own SelectionService
  // highlights text (with edge auto-scroll). Mouse-reporting TUIs (Claude Code)
  // suppress local selection unless the event carries a force modifier — Option on
  // macOS / iPadOS, Shift elsewhere — added only then; on a plain shell Shift would
  // extend a previous selection instead of starting a fresh single-drag select.
  useEffect(() => {
    if (!isTouchDevice || !selectMode) return;
    const container = termRef.current;
    const terminal = terminalRef.current;
    if (!container || !terminal) return;

    const xtermIsMac = ['Macintosh', 'MacIntel', 'MacPPC', 'Mac68K'].includes(navigator.platform);
    const mouseReportingOn = () => {
      try {
        return !!(terminal as any)._core?.coreMouseService?.areMouseEventsActive;
      } catch {
        return false;
      }
    };
    const dispatchSelMouse = (type: string, clientX: number, clientY: number, target: EventTarget) => {
      const force = mouseReportingOn();
      target.dispatchEvent(new MouseEvent(type, {
        view: window,
        bubbles: true,
        cancelable: true,
        clientX,
        clientY,
        button: 0,
        buttons: type === 'mouseup' ? 0 : 1,
        detail: 1,
        shiftKey: force && !xtermIsMac,
        altKey: force && xtermIsMac,
      }));
    };

    let selecting = false;
    const onStart = (e: TouchEvent) => {
      const xel = container.querySelector('.xterm') as HTMLElement | null; // SelectionService listens here
      const t = e.touches[0];
      if (!xel || !t) return;
      e.preventDefault();
      selecting = true;
      dispatchSelMouse('mousedown', t.clientX, t.clientY, xel);
    };
    const onMove = (e: TouchEvent) => {
      if (!selecting) return;
      const t = e.touches[0];
      if (!t) return;
      e.preventDefault();
      dispatchSelMouse('mousemove', t.clientX, t.clientY, document); // drag lands on document
    };
    const onEnd = (e: TouchEvent) => {
      if (!selecting) return;
      selecting = false;
      const t = e.changedTouches[0];
      dispatchSelMouse('mouseup', t?.clientX ?? 0, t?.clientY ?? 0, document);
    };

    container.addEventListener('touchstart', onStart, { passive: false });
    container.addEventListener('touchmove', onMove, { passive: false });
    container.addEventListener('touchend', onEnd);
    container.addEventListener('touchcancel', onEnd);
    return () => {
      container.removeEventListener('touchstart', onStart);
      container.removeEventListener('touchmove', onMove);
      container.removeEventListener('touchend', onEnd);
      container.removeEventListener('touchcancel', onEnd);
    };
  }, [isTouchDevice, selectMode]);

  const handleClick = useCallback(() => {
    if (isTouchDevice) return;
    terminalRef.current?.focus();
  }, [isTouchDevice]);

  return (
    <div className={`relative w-full h-full terminal-shell${selectMode ? ' select-mode' : ''}`}>
      {/* Status overlays — pointer-events:none so they don't block xterm */}
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

      {status === 'connected' && !hasOutput && (
        <div className="absolute top-0 left-0 right-0 z-10 px-3 py-1.5 text-xs flex items-center gap-2 pointer-events-none"
             style={{ background: 'rgba(34,197,94,0.08)', color: '#64748b' }}>
          <span className="w-2 h-2 rounded-full shrink-0" style={{ background: '#22c55e' }} />
          Connected — waiting for output...
        </div>
      )}

      <div
        ref={termRef}
        onClick={handleClick}
        className="w-full h-full cursor-text"
      />

      {/* Select-mode toggle (touch only). Off → one-finger drag scrolls; On → drag
          selects text (native scroll suspended) so it can be copied. Bottom-left so
          it never overlaps the 복사 button (bottom-right). */}
      {isTouchDevice && (
        <button
          onClick={() => setSelectMode((m) => !m)}
          className={`absolute bottom-2 left-2 z-20 px-3 py-1.5 rounded-lg text-xs font-semibold shadow-lg
                      touch-manipulation active:opacity-70 ${
                        selectMode ? 'bg-deck-accent text-white' : 'bg-deck-surface/90 text-deck-text-dim border border-deck-border'
                      }`}
          title={selectMode ? '선택 모드 — 드래그로 텍스트 선택. 탭하면 스크롤로 돌아갑니다.' : '탭하면 선택 모드 — 드래그로 텍스트를 선택해 복사할 수 있습니다.'}
        >
          {selectMode ? '✓ 선택 모드' : '⇱ 선택'}
        </button>
      )}

      {/* Copy button — appears while text is selected. mousedown/touchstart with
          preventDefault so clicking it doesn't clear the terminal selection. */}
      {hasSelection && (
        <button
          onMouseDown={(e) => { e.preventDefault(); copySelection(); }}
          onTouchStart={(e) => { e.preventDefault(); copySelection(); }}
          className="absolute bottom-2 right-2 z-20 px-3 py-1.5 rounded-lg text-xs font-semibold shadow-lg
                     touch-manipulation active:opacity-70 bg-deck-accent text-white"
          title="선택 영역 복사 (⌘C / Ctrl+Shift+C). TUI에서는 그냥 드래그해도 복사됩니다."
        >
          {copied ? '복사됨 ✓' : '복사'}
        </button>
      )}

      {/* OSC 52 copy feedback — a plain drag in a TUI copies with no xterm
          selection, so show a transient confirmation instead of the button. */}
      {copied && !hasSelection && (
        <div className="absolute bottom-2 right-2 z-20 px-3 py-1.5 rounded-lg text-xs font-semibold shadow-lg
                        pointer-events-none bg-deck-accent text-white">
          복사됨 ✓
        </div>
      )}
    </div>
  );
});
