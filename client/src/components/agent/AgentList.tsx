import { Link } from 'react-router-dom';
import { Agent } from '../../stores/appStore';
import { StatusBadge } from '../layout/StatusBadge';
import { AGENT_ICON_MAP, IconRobot } from '../icons';
import { NotificationRing } from '../notification/NotificationRing';
import { AgentMeta } from '../sidebar/AgentMeta';
import { useAgentNotification } from '../../hooks/useAgentNotification';

interface AgentListProps {
  agents: Agent[];
  onDestroy: (id: string) => void;
}

function AgentListItem({ agent, onDestroy }: { agent: Agent; onDestroy: (id: string) => void }) {
  const IC = AGENT_ICON_MAP[agent.preset];
  useAgentNotification(agent.id);

  return (
    <NotificationRing agentId={agent.id}>
      <Link
        to={`/agents/${agent.id}`}
        className="block card overflow-hidden active:bg-deck-border/20 transition-colors"
      >
        <div className="flex items-center gap-3 px-4 py-3">
          {IC && <IC size={22} />}
          <div className="flex-1 min-w-0">
            <div className="font-medium text-sm truncate">{agent.name}</div>
            <div className="text-[11px] text-deck-text-dim truncate">{agent.workingDir}</div>
          </div>
          <StatusBadge status={agent.status} />
          <button
            onClick={(e) => { e.preventDefault(); e.stopPropagation(); onDestroy(agent.id); }}
            className="text-xs px-3 py-1.5 rounded-lg bg-deck-danger/20 text-deck-danger active:bg-deck-danger/30"
          >
            Kill
          </button>
        </div>
        <AgentMeta agentId={agent.id} compact />
      </Link>
    </NotificationRing>
  );
}

export function AgentList({ agents, onDestroy }: AgentListProps) {
  if (agents.length === 0) {
    return (
      <div className="flex flex-col items-center justify-center py-16 text-deck-text-dim">
        <IconRobot size={34} className="mb-3 opacity-70" />
        <span className="text-sm">에이전트가 없습니다</span>
        <span className="text-xs mt-1">상단의 New 버튼으로 추가하세요</span>
      </div>
    );
  }

  return (
    <div className="space-y-2 p-3">
      {agents.map((agent) => (
        <AgentListItem key={agent.id} agent={agent} onDestroy={onDestroy} />
      ))}
    </div>
  );
}
