import { useEffect, useRef, useState, useCallback, forwardRef, useImperativeHandle } from 'react';
import { Terminal, type TerminalHandle as WTermReactHandle } from '@wterm/react';
import type { WTerm } from '@wterm/dom';
import '@wterm/react/css';
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
 * so text selection, copy/paste and scrolling are the browser's own — no canvas
 * workarounds, no synthetic-mouse selection, no iOS viewport babysitting. Wired to
 * the WebSocket PTY protocol (terminal:attach/input/output/resize); the Go server
 * is unaware of the front-end library.
 */
export const TerminalView = forwardRef<TerminalHandle, TerminalViewProps>(function TerminalView(
  { agentId, fontSize, onFocusTerminal, onHangulDirect },
  ref,
) {
  const { isMobile, isTablet, isTouchDevice } = useDevice();
  const handleRef = useRef<WTermReactHandle | null>(null);
  const wtRef = useRef<WTerm | null>(null);
  const attachedRef = useRef(false);
  const readyRef = useRef(false);
  const colsRef = useRef(80);
  const rowsRef = useRef(24);
  const [status, setStatus] = useState<'connecting' | 'connected' | 'disconnected'>('connecting');
  const statusRef = useRef(status);
  statusRef.current = status;
  const onHangulDirectRef = useRef(onHangulDirect);
  onHangulDirectRef.current = onHangulDirect;

  const resolvedFontSize = fontSize ?? (isMobile ? 13 : isTablet ? 13 : 14);

  useImperativeHandle(ref, () => ({
    focus: () => handleRef.current?.focus(),
    sendKey: (data: string) => {
      const app = wtRef.current?.bridge?.cursorKeysApp?.() ?? false;
      agentDeckWS.send('terminal:input', { agentId, data: app ? (APP_CURSOR_MAP[data] ?? data) : data });
    },
  }), [agentId]);

  // Attach ONLY once wterm is ready AND the socket is open. The server replies to
  // attach with the current screen buffer immediately; if we attached before
  // wterm's write handle existed, that first dump would be written to a null
  // handle and lost — leaving an idle alt-screen app (Claude Code) blank forever.
  const maybeAttach = useCallback(() => {
    if (!readyRef.current || attachedRef.current || !agentDeckWS.connected) return;
    agentDeckWS.send('terminal:attach', { agentId, cols: colsRef.current, rows: rowsRef.current });
    attachedRef.current = true;
  }, [agentId]);

  // WS wiring: output → write; open/close → status + (re)attach; detach on unmount.
  useEffect(() => {
    const unsubOutput = agentDeckWS.on('terminal:output', (payload: any) => {
      if (payload.agentId !== agentId) return;
      handleRef.current?.write(payload.data);
      if (statusRef.current !== 'connected') setStatus('connected');
    });
    const unsubOpen = agentDeckWS.on('open', () => {
      attachedRef.current = false;
      setStatus('connected');
      maybeAttach();
    });
    const unsubClose = agentDeckWS.on('close', () => setStatus('disconnected'));

    if (agentDeckWS.connected) {
      setStatus('connected');
      maybeAttach();
    }

    return () => {
      unsubOutput();
      unsubOpen();
      unsubClose();
      agentDeckWS.send('terminal:detach', { agentId });
    };
  }, [agentId, maybeAttach]);

  const onData = useCallback((data: string) => {
    if (onHangulDirectRef.current && HANGUL_RE.test(data)) onHangulDirectRef.current();
    agentDeckWS.send('terminal:input', { agentId, data });
  }, [agentId]);

  const onResize = useCallback((cols: number, rows: number) => {
    colsRef.current = cols;
    rowsRef.current = rows;
    if (attachedRef.current) agentDeckWS.send('terminal:resize', { agentId, cols, rows });
  }, [agentId]);

  const onReady = useCallback((wt: WTerm) => {
    wtRef.current = wt;
    colsRef.current = wt.cols;
    rowsRef.current = wt.rows;
    readyRef.current = true;
    // wterm focuses its hidden textarea on init, popping the iOS soft keyboard over
    // the panel on mount. On touch we type via the Prompt Bar, so blur it — a real
    // tap still focuses to type, but scrolling / long-press selection aren't
    // fighting a keyboard that appeared unbidden.
    if (isTouchDevice) {
      requestAnimationFrame(() => (document.activeElement as HTMLElement | null)?.blur?.());
    }
    // wterm is ready now — safe to attach (the screen dump lands on a live handle).
    maybeAttach();
  }, [maybeAttach, isTouchDevice]);

  return (
    <div className="relative w-full h-full wterm-shell">
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

      <Terminal
        ref={handleRef}
        autoResize
        cursorBlink
        onData={onData}
        onResize={onResize}
        onReady={onReady}
        onClick={() => { if (!isTouchDevice) onFocusTerminal?.(); }}
        className="wterm-embed w-full h-full"
        style={{
          // App dark palette + CJK-capable monospace (cells are sized in ch, so a
          // fixed-width CJK font keeps Hangul aligned to the grid).
          ['--term-font-family' as any]: "'JetBrains Mono', 'D2Coding', 'NanumGothicCoding', 'Noto Sans Mono CJK KR', 'Sarasa Mono K', monospace",
          ['--term-font-size' as any]: `${resolvedFontSize}px`,
          ['--term-bg' as any]: '#0a0a0f',
          ['--term-fg' as any]: '#e2e8f0',
          ['--term-cursor' as any]: '#6366f1',
        }}
      />
    </div>
  );
});
