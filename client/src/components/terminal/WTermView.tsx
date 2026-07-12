import { useEffect, useRef, useState, useCallback, forwardRef, useImperativeHandle } from 'react';
import { Terminal, type TerminalHandle as WTermReactHandle } from '@wterm/react';
import type { WTerm } from '@wterm/dom';
import '@wterm/react/css';
import { agentDeckWS } from '../../lib/ws';
import { useDevice } from '../../hooks/useDevice';
import type { TerminalHandle } from './TerminalView';

/**
 * SPIKE: a drop-in alternative to TerminalView backed by wterm (@wterm/react)
 * instead of xterm.js. wterm renders to the DOM, so text selection / copy / paste
 * and scrolling are the browser's own — the whole reason we're evaluating it for
 * mobile. Wired to the SAME WebSocket PTY protocol (terminal:attach/input/output/
 * resize) so the Go server is untouched. Opt in with ?term=wterm to A/B on device.
 *
 * Known trade-off to validate on iPhone/iPad:
 *   - CJK (Korean) width: wterm cells are 1ch (.term-block); wide-char handling TBD.
 *   - No mouse reporting: TUIs (Claude Code) fall back to arrow-key menus.
 */

const APP_CURSOR_MAP: Record<string, string> = {
  '\x1b[A': '\x1bOA',
  '\x1b[B': '\x1bOB',
  '\x1b[C': '\x1bOC',
  '\x1b[D': '\x1bOD',
};

const HANGUL_RE = /[ᄀ-ᇿ㄰-㆏가-힣]/;

interface WTermViewProps {
  agentId: string;
  fontSize?: number;
  focusGuardRef?: React.MutableRefObject<boolean>;
  onFocusTerminal?: () => void;
  onHangulDirect?: () => void;
}

export const WTermView = forwardRef<TerminalHandle, WTermViewProps>(function WTermView(
  { agentId, fontSize, onHangulDirect },
  ref,
) {
  const { isMobile, isTablet, isTouchDevice } = useDevice();
  const handleRef = useRef<WTermReactHandle | null>(null);
  const wtRef = useRef<WTerm | null>(null);
  const attachedRef = useRef(false);
  const colsRef = useRef(80);
  const rowsRef = useRef(24);
  const [status, setStatus] = useState<'connecting' | 'connected' | 'disconnected'>('connecting');
  const statusRef = useRef(status);
  statusRef.current = status;

  const onHangulDirectRef = useRef(onHangulDirect);
  onHangulDirectRef.current = onHangulDirect;

  const resolvedFontSize = fontSize ?? (isMobile ? 12 : isTablet ? 13 : 14);

  useImperativeHandle(ref, () => ({
    focus: () => handleRef.current?.focus(),
    sendKey: (data: string) => {
      // Honor application cursor keys (DECCKM) like the xterm path: the core
      // exposes cursorKeysApp() so toolbar arrows drive TUI menus correctly.
      const app = wtRef.current?.bridge?.cursorKeysApp?.() ?? false;
      const out = app ? (APP_CURSOR_MAP[data] ?? data) : data;
      agentDeckWS.send('terminal:input', { agentId, data: out });
    },
  }), [agentId]);

  const attach = useCallback(() => {
    agentDeckWS.send('terminal:attach', { agentId, cols: colsRef.current, rows: rowsRef.current });
    attachedRef.current = true;
  }, [agentId]);

  // WS wiring — output → write, input handled via onData below.
  useEffect(() => {
    const unsubOutput = agentDeckWS.on('terminal:output', (payload: any) => {
      if (payload.agentId !== agentId) return;
      handleRef.current?.write(payload.data);
      if (statusRef.current !== 'connected') setStatus('connected');
    });
    const unsubOpen = agentDeckWS.on('open', () => {
      attachedRef.current = false;
      attach();
      setStatus('connected');
    });
    const unsubClose = agentDeckWS.on('close', () => setStatus('disconnected'));

    if (agentDeckWS.connected) {
      attach();
      setStatus('connected');
    }

    return () => {
      unsubOutput();
      unsubOpen();
      unsubClose();
      agentDeckWS.send('terminal:detach', { agentId });
    };
  }, [agentId, attach]);

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
    // wterm focuses its hidden textarea on init, which pops the iOS soft keyboard
    // over the panel on mount. On touch we type via the Prompt Bar (same as the
    // xterm path), so blur it — a real tap can still focus to type, but scrolling
    // and long-press selection aren't fighting a keyboard that appeared unbidden.
    if (isTouchDevice) {
      requestAnimationFrame(() => (document.activeElement as HTMLElement | null)?.blur?.());
    }
    if (agentDeckWS.connected && !attachedRef.current) attach();
  }, [attach, isTouchDevice]);

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
        className="wterm-embed w-full h-full"
        style={{
          // Match the app's dark palette + CJK-capable monospace stack. Cells are
          // sized in ch, so a fixed-width CJK font is required for grid alignment.
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
