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
  }), []);
  const [status, setStatus] = useState<'connecting' | 'connected' | 'disconnected'>('connecting');
  const [hasOutput, setHasOutput] = useState(false);

  const statusRef = useRef(status);
  statusRef.current = status;
  const hasOutputRef = useRef(hasOutput);
  hasOutputRef.current = hasOutput;

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
      fontFamily: "'JetBrains Mono', 'D2Coding', 'Noto Sans KR', monospace",
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
      smoothScrollDuration: isTouchDevice ? 245 : 180,
      allowTransparency: false,
      // Single interactive terminal: xterm always accepts input and forwards it
      // straight to the PTY. Short shell ops, Claude choices, y/n, arrows, Ctrl+C
      // are typed here directly; the Prompt Bar is only for Korean / long text.
      disableStdin: false,
      allowProposedApi: true,
      scrollOnUserInput: false,
    });

    const fitAddon = new FitAddon();
    terminal.loadAddon(fitAddon);
    terminal.loadAddon(new WebLinksAddon());
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
      vvTimer = window.setTimeout(() => scheduleViewportSettle(false), 100);
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
      dataDisposable.dispose();
      terminal.dispose();
      terminalRef.current = null;
      fitAddonRef.current = null;
    };
  }, [agentId, isTouchDevice, resolvedFontSize]);

  const handleClick = useCallback(() => {
    if (isTouchDevice) return;
    terminalRef.current?.focus();
  }, [isTouchDevice]);

  return (
    <div className="relative w-full h-full terminal-shell">
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
    </div>
  );
});
