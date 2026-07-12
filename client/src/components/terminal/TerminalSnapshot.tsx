import { useEffect, useRef, useState } from 'react';
import { WasmBridge, type TerminalCore } from '@wterm/core';
import { agentDeckWS } from '../../lib/ws';

/**
 * Lightweight, read-only terminal preview for dashboard agent cards. Instead of a
 * full wterm instance (DOM renderer + input + RAF render loop) per card, it feeds
 * the agent's output stream into a HEADLESS wterm core and paints the visible grid
 * as plain text on a throttle — enough for a glanceable snapshot at a fraction of
 * the cost. Bottom-aligned so the most recent lines stay in view.
 */

const COLS = 80;
const ROWS = 24;
const RENDER_MS = 300; // coalesce paints — a preview doesn't need 60fps

interface TerminalSnapshotProps {
  agentId: string;
}

function codeToChar(code: number): string {
  if (!code || code < 0x20) return ' ';
  try {
    return String.fromCodePoint(code);
  } catch {
    return ' ';
  }
}

export function TerminalSnapshot({ agentId }: TerminalSnapshotProps) {
  const [text, setText] = useState('');
  const bridgeRef = useRef<TerminalCore | null>(null);

  useEffect(() => {
    let disposed = false;
    let attached = false;
    let dirty = false;
    let timer: number | undefined;

    const attach = () => {
      agentDeckWS.send('terminal:attach', { agentId, cols: COLS, rows: ROWS });
      attached = true;
    };

    const paint = () => {
      const b = bridgeRef.current;
      if (!b || disposed) return;
      const rows = b.getRows();
      const cols = b.getCols();
      const lines: string[] = [];
      for (let r = 0; r < rows; r++) {
        let line = '';
        for (let c = 0; c < cols; c++) line += codeToChar(b.getCell(r, c).char);
        lines.push(line.replace(/\s+$/, ''));
      }
      while (lines.length && lines[lines.length - 1] === '') lines.pop();
      setText(lines.join('\n'));
    };

    // Trailing throttle: repaint at most every RENDER_MS while output flows.
    const schedulePaint = () => {
      dirty = true;
      if (timer !== undefined) return;
      timer = window.setTimeout(() => {
        timer = undefined;
        if (dirty && !disposed) {
          dirty = false;
          paint();
        }
      }, RENDER_MS);
    };

    WasmBridge.load()
      .then((b) => {
        if (disposed) return;
        b.init(COLS, ROWS);
        bridgeRef.current = b;
        if (agentDeckWS.connected && !attached) attach();
      })
      .catch(() => { /* preview unavailable — card still shows header/meta */ });

    const unsubOutput = agentDeckWS.on('terminal:output', (payload: any) => {
      if (payload.agentId !== agentId || !bridgeRef.current) return;
      bridgeRef.current.writeString(payload.data);
      schedulePaint();
    });
    const unsubOpen = agentDeckWS.on('open', () => {
      attached = false;
      if (bridgeRef.current) attach();
    });

    if (agentDeckWS.connected && bridgeRef.current) attach();

    return () => {
      disposed = true;
      if (timer !== undefined) clearTimeout(timer);
      unsubOutput();
      unsubOpen();
      agentDeckWS.send('terminal:detach', { agentId });
      bridgeRef.current = null;
    };
  }, [agentId]);

  return (
    <div className="w-full h-full overflow-hidden flex flex-col justify-end bg-[#0a0a0f]">
      <pre className="px-2 py-1 text-[10px] leading-[1.3] font-mono whitespace-pre text-deck-text-dim">
        {text}
      </pre>
    </div>
  );
}
