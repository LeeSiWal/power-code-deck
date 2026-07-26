import type { AgentSummary } from '../../stores/appStore';
import { liveState } from './liveState';
import { AgentTile } from './AgentTile';

export function ProjectGroup({
  label,
  agents,
  onOpen,
  onRestart,
  onStop,
  onLogs,
}: {
  label: string;
  agents: AgentSummary[];
  onOpen: (id: string) => void;
  onRestart: (id: string) => void;
  onStop: (s: AgentSummary) => void;
  onLogs: () => void;
}) {
  return (
    <div className="mb-6">
      <div className="flex items-center gap-2.5 mb-2">
        <span className="font-mono text-[10px] uppercase tracking-wide text-deck-text-faint">project</span>
        <span className="font-mono text-[11px] font-semibold truncate">{label}</span>
        <span className="font-mono text-[9px] px-1.5 rounded-full border border-deck-border text-deck-text-dim">
          {agents.length}
        </span>
        {(() => {
          const working = agents.filter((a) => liveState(a) === 'working').length;
          return working > 0 ? (
            <span className="font-mono text-[9px] px-1.5 rounded-full border border-deck-accent/40 bg-deck-accent/10 text-deck-accent-light flex items-center gap-1">
              <span className="w-1.5 h-1.5 rounded-full bg-deck-accent-light animate-pulse-soft" />
              {working} 작업 중
            </span>
          ) : null;
        })()}
        <span className="flex-1 border-t border-dashed border-deck-border-soft" />
      </div>
      <div className="grid grid-cols-1 sm:grid-cols-2 xl:grid-cols-3 gap-3">
        {agents.map((s) => (
          <AgentTile key={s.agentId} s={s} onOpen={onOpen} onRestart={onRestart} onStop={onStop} onLogs={onLogs} />
        ))}
      </div>
    </div>
  );
}
