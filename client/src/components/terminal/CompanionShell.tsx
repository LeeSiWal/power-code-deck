import { useEffect, useState } from 'react';
import { agentDeckWS } from '../../lib/ws';
import { TerminalView } from './TerminalView';

export function CompanionShell({ agentId, onClose }: { agentId: string; onClose: () => void }) {
  const [state, setState] = useState<{ running: boolean; message?: string }>({ running: true });
  const [generation, setGeneration] = useState(0);

  useEffect(() => agentDeckWS.on('shell:state', (payload: any) => {
    if (payload.agentId === agentId) {
      setState({ running: !!payload.running, message: payload.message });
    }
  }), [agentId]);

  return (
    <div className="h-full min-h-0 flex flex-col bg-deck-bg">
      <div className="h-8 shrink-0 flex items-center gap-2 px-2 border-b border-deck-border bg-deck-surface">
        <span className="text-xs font-medium">Shell</span>
        {state.message && <span className="text-[10px] text-amber-400 truncate">{state.message}</span>}
        <span className={`ml-auto w-1.5 h-1.5 rounded-full ${state.running ? 'bg-emerald-400' : 'bg-deck-text-dim'}`} />
        {state.running ? (
          <button
            className="text-[10px] text-red-400 hover:text-red-300"
            onClick={() => agentDeckWS.send('shell:kill', { agentId })}
          >
            종료
          </button>
        ) : (
          <button
            className="text-[10px] text-deck-accent"
            onClick={() => { setState({ running: true }); setGeneration((n) => n + 1); }}
          >
            다시 시작
          </button>
        )}
        <button className="text-xs text-deck-text-dim" onClick={onClose}>✕</button>
      </div>
      <div className="flex-1 min-h-0">
        {state.running && <TerminalView key={generation} agentId={agentId} channel="shell" />}
      </div>
    </div>
  );
}
