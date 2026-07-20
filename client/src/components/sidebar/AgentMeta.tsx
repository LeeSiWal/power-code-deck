import { useAppStore } from '../../stores/appStore';
import { IconBranch, IconPlug } from '../icons';

interface AgentMetaProps {
  agentId: string;
  compact?: boolean;
}

export function AgentMeta({ agentId, compact = false }: AgentMetaProps) {
  const meta = useAppStore((s) => s.agentMeta.get(agentId));

  if (!meta) return null;

  if (compact) {
    const hasContent = meta.gitBranch || (meta.listeningPorts && meta.listeningPorts.length > 0);
    if (!hasContent) return null;
    return (
      <div className="flex items-center gap-2 px-4 py-1.5 text-[11px] text-deck-text-dim truncate border-t border-deck-border/30">
        {meta.gitBranch && (
          <span className="flex items-center gap-0.5">
            <IconBranch size={12} /> {meta.gitBranch}
            {meta.gitAhead > 0 && <span className="text-blue-400 ml-0.5">(+{meta.gitAhead})</span>}
            {meta.gitDirty && <span className="text-amber-400 ml-0.5">●</span>}
          </span>
        )}
        {meta.listeningPorts && meta.listeningPorts.length > 0 && (
          <span className="flex items-center gap-0.5">
            <IconPlug size={12} /> {meta.listeningPorts.map(p => `:${p}`).join(' ')}
          </span>
        )}
      </div>
    );
  }

  return (
    <div className="px-3 py-1.5 space-y-0.5 text-[11px] text-deck-text-dim border-t border-deck-border/50">
      {meta.gitBranch && (
        <div className="flex items-center gap-1 truncate">
          <IconBranch size={12} className="shrink-0" />
          <span className="font-mono">{meta.gitBranch}</span>
          {meta.gitAhead > 0 && <span className="text-blue-400">(+{meta.gitAhead})</span>}
          {meta.gitDirty && <span className="text-amber-400">●</span>}
        </div>
      )}
      {meta.listeningPorts && meta.listeningPorts.length > 0 && (
        <div className="flex items-center gap-1.5 flex-wrap">
          <IconPlug size={12} className="shrink-0" />
          {meta.listeningPorts.map((port) => (
            <a
              key={port}
              href={`http://localhost:${port}`}
              target="_blank"
              rel="noopener"
              className="font-mono text-blue-400 hover:underline cursor-pointer"
              onClick={(e) => e.stopPropagation()}
            >
              :{port}
            </a>
          ))}
        </div>
      )}
      {meta.customStatus && (
        <div className="flex items-center gap-1">
          <span className="w-1.5 h-1.5 rounded-full" style={{ background: meta.customStatus.color || '#6366f1' }} />
          <span>{meta.customStatus.text}</span>
        </div>
      )}
      {meta.progress && (
        <div className="flex items-center gap-2">
          <div className="flex-1 h-1 rounded-full bg-deck-border overflow-hidden">
            <div className="h-full rounded-full bg-deck-accent transition-all" style={{ width: `${meta.progress.value * 100}%` }} />
          </div>
          {meta.progress.label && <span className="text-[10px]">{meta.progress.label}</span>}
        </div>
      )}
    </div>
  );
}
