// 의도적으로 미사용 상태 — 지우다 만 파일이 아니다.
//
// 통합 관제실(2026-07)에서 타일 밀도를 위해 스냅샷을 화면에서 뺐다. 파일을 남긴
// 이유는 여기에 재유도하기 번거로운 로직이 있기 때문이다: headless wterm 인스턴스
// 관리, 300ms 스로틀 페인트, `open` 이벤트 시 재attach. import처가 없으므로 vite가
// 트리셰이킹해 번들 비용은 0이고, tsc는 계속 타입 검사를 한다.
//
// 되살리려면:
//   1. AgentTile에서 마운트하되 동시 인스턴스를 1개로 제한할 것
//      (expandedId: string | null — 에이전트마다 headless wterm이 하나씩 생긴다)
//   2. 스냅샷은 80칼럼이라 1/3 폭 타일에서는 읽히지 않는다. 펼친 타일을
//      col-span-full 행으로 전개해야 제 비율이 나온다(모바일 1컬럼에서는 무동작).
//   3. 먼저 확인할 것: tsc는 타입만 보므로 프로토콜 드리프트를 잡지 못한다.
//      `terminal:output` 이벤트 이름과 페이로드 모양이 그대로인지 확인할 것.
//      어긋나면 타입은 통과하는데 화면만 빈 채로 남는다.

import { useEffect, useRef, useState } from 'react';
import { WasmBridge, type TerminalCore } from '@wterm/core';
import { agentDeckWS } from '../../lib/ws';

/**
 * Lightweight, read-only terminal preview for Control Room tiles. Instead of a
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
