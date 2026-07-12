import { Link } from 'react-router-dom';
import { Agent } from '../../stores/appStore';
import { StatusBadge } from '../layout/StatusBadge';
import { SubAgentBar } from '../animation/SubAgentBar';
import { TerminalSnapshot } from '../terminal/TerminalSnapshot';
import { AGENT_ICON_MAP } from '../icons';
import { NotificationRing } from '../notification/NotificationRing';
import { AgentMeta } from '../sidebar/AgentMeta';
import { useAgentNotification } from '../../hooks/useAgentNotification';

interface AgentCardProps {
  agent: Agent;
  onDestroy: (id: string) => void;
}

export function AgentCard({ agent, onDestroy }: AgentCardProps) {
  const IC = AGENT_ICON_MAP[agent.preset];
  useAgentNotification(agent.id);

  return (
    <NotificationRing agentId={agent.id}>
      <div className="card overflow-hidden flex flex-col">
        <div className="flex items-center justify-between px-3 py-2 border-b border-deck-border">
          <div className="flex items-center gap-2">
            {IC && <IC size={18} />}
            <span className="font-medium text-sm text-deck-text">{agent.name}</span>
            <StatusBadge status={agent.status} />
          </div>
          <div className="flex items-center gap-1.5">
            <Link
              to={`/agents/${agent.id}`}
              className="text-xs px-2 py-0.5 rounded bg-deck-bg text-deck-text hover:bg-deck-border"
            >
              Open
            </Link>
            <button
              onClick={() => onDestroy(agent.id)}
              className="text-xs px-2 py-0.5 rounded bg-deck-danger/20 text-deck-danger hover:bg-deck-danger/30"
            >
              Kill
            </button>
          </div>
        </div>

        <AgentMeta agentId={agent.id} />

        <SubAgentBar agentId={agent.id} />

        <div className="flex-1 min-h-[200px]">
          <TerminalSnapshot agentId={agent.id} />
        </div>
      </div>
    </NotificationRing>
  );
}
