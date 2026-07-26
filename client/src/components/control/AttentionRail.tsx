import type { AgentSummary } from '../../stores/appStore';
import { liveState, attnClasses, attnLabel, timeAgo } from './liveState';
import { LiveDot } from './LiveDot';

export function AttentionRail({ items, onOpen }: { items: AgentSummary[]; onOpen: (agentId: string) => void }) {
  return (
    <div className="rounded-lg border border-deck-border bg-deck-raised p-2.5 mb-4">
      <div className="flex items-center justify-between mb-2">
        <span className="font-mono text-[10px] font-bold uppercase tracking-wide text-deck-warning">
          Attention · {items.length}
        </span>
        <span className="font-mono text-[9px] text-deck-text-faint hidden sm:block">
          approval &gt; error &gt; stalled · since ↑
        </span>
      </div>
      <div className="flex gap-2.5 overflow-x-auto pb-1">
        {items.map((s) => (
          <button
            key={s.agentId}
            onClick={() => onOpen(s.agentId)}
            className={`min-w-[150px] text-left rounded-md border bg-deck-surface p-2 ${attnClasses(s.attention.primary).split(' ')[0]}`}
          >
            <div className="flex items-center gap-1.5 mb-1">
              <LiveDot hue={s.colorHue} state={liveState(s)} />
              <span className="font-mono text-[11px] font-bold truncate">{s.name}</span>
            </div>
            <span className={`text-[9px] font-mono px-1.5 rounded-full border ${attnClasses(s.attention.primary)}`}>
              {attnLabel(s.attention.reasons[0] || { kind: s.attention.primary })}
            </span>
            <div className="font-mono text-[9px] text-deck-text-dim mt-1 truncate">
              {s.lastTool ? `${s.lastTool} · ` : ''}
              {timeAgo(s.lastActivityAt)}
            </div>
          </button>
        ))}
      </div>
    </div>
  );
}
