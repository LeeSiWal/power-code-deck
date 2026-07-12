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
  const onHangulDirectRef = useRef(onHangulDirect);
  onHangulDirectRef.current = onHangulDirect;

  const [hasSelection, setHasSelection] = useState(false);
  const [copied, setCopied] = useState(false);
  // freeze-while-selecting: while a selection is held inside the terminal, buffer
  // incoming output instead of writing it — a redraw would replace the DOM rows
  // and wipe the selection mid-drag.
  const selectingRef = useRef(false);
  const pendingRef = useRef<string[]>([]);
  const pendingLenRef = useRef(0);

  const resolvedFontSize = fontSize ?? (isMobile ? 13 : isTablet ? 13 : 14);

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

  const writeToTerm = useCallback((data: string) => {
    if (selectingRef.current) {
      pendingRef.current.push(data);
      pendingLenRef.current += data.length;
      if (pendingLenRef.current > MAX_FROZEN_CHARS) flushPending(); // bound memory
      return;
    }
    handleRef.current?.write(data);
  }, [flushPending]);

  // Attach ONLY once wterm is ready AND the socket is open. The server replies to
  // attach with the current screen buffer immediately; attaching before wterm's
  // write handle exists would drop that first dump — leaving an idle alt-screen
  // app (Claude Code) blank forever.
  const maybeAttach = useCallback(() => {
    if (!readyRef.current || attachedRef.current || !agentDeckWS.connected) return;
    agentDeckWS.send('terminal:attach', { agentId, cols: colsRef.current, rows: rowsRef.current });
    attachedRef.current = true;
  }, [agentId]);

  // WS wiring: output → write; open/close → status + (re)attach; detach on unmount.
  useEffect(() => {
    const unsubOutput = agentDeckWS.on('terminal:output', (payload: any) => {
      if (payload.agentId !== agentId) return;
      writeToTerm(payload.data);
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
  }, [agentId, maybeAttach, writeToTerm]);

  // Track a terminal-scoped selection: drives the freeze + the 복사 button. When
  // the selection clears, flush any output buffered during the freeze.
  useEffect(() => {
    const onSelectionChange = () => {
      const sel = document.getSelection();
      const active = !!sel && !sel.isCollapsed && !!shellRef.current && shellRef.current.contains(sel.anchorNode);
      setHasSelection(active);
      if (active) selectingRef.current = true;
      else flushPending();
    };
    document.addEventListener('selectionchange', onSelectionChange);
    return () => document.removeEventListener('selectionchange', onSelectionChange);
  }, [flushPending]);

  const selectAllTerminal = useCallback(() => {
    const el = shellRef.current?.querySelector('.wterm') as HTMLElement | null;
    const sel = window.getSelection();
    if (!el || !sel) return;
    sel.removeAllRanges();
    sel.selectAllChildren(el);
  }, []);

  const copySelection = useCallback(() => {
    const text = window.getSelection()?.toString() ?? '';
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
    // tap still focuses to type, but scroll / long-press selection aren't fighting a
    // keyboard that appeared unbidden.
    if (isTouchDevice) {
      requestAnimationFrame(() => (document.activeElement as HTMLElement | null)?.blur?.());
    }
    maybeAttach();
  }, [maybeAttach, isTouchDevice]);

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
