import { useAppStore } from '../../stores/appStore';
import { IconBranch } from '../icons';

interface AgentMetaProps {
  agentId: string;
  compact?: boolean;
}

// 리스닝 포트는 의도적으로 표시하지 않는다 — 되돌리기 전에 아래를 읽을 것.
//
// meta.listeningPorts는 이 에이전트의 포트가 아니라 **머신 전체의 LISTEN 포트**다.
// services/port_scanner.go의 Poll(agentID)이 `ss -tlnp`(또는 lsof)로 시스템 전체를
// 훑은 뒤 agentID를 캐시 키로만 쓰고 필터링에는 전혀 쓰지 않기 때문에, 모든 에이전트가
// 동일한 목록을 받는다. 실제로 이 서버에서 25개가 나왔고 거기에는 pcd 자신(:33033),
// ssh(:4000), postgres(:5432)까지 섞여 있었다.
//
// 게다가 링크도 원격에서는 죽는다: href가 http://localhost:PORT라 도메인으로 접속한
// 폰/아이패드에서는 그 기기 자신을 가리킨다. 실제로 동작하는 경로는 세션 안의
// Browser 패널이며, 그쪽은 /api/proxy를 거쳐 서버가 대신 받아온다.
//
// 되살리려면 스캐너가 에이전트 프로세스 트리와 소켓을 실제로 연결하도록 먼저 고쳐야
// 한다(`ss -tlnp`의 pid를 세션 프로세스의 자손과 대조). 그 전에는 표시할수록 해롭다.
export function AgentMeta({ agentId, compact = false }: AgentMetaProps) {
  const meta = useAppStore((s) => s.agentMeta.get(agentId));

  if (!meta) return null;

  if (compact) {
    if (!meta.gitBranch) return null;
    return (
      <div className="flex items-center gap-2 px-4 py-1.5 text-[11px] text-deck-text-dim truncate border-t border-deck-border/30">
        <span className="flex items-center gap-0.5">
          <IconBranch size={12} /> {meta.gitBranch}
          {meta.gitAhead > 0 && <span className="text-blue-400 ml-0.5">(+{meta.gitAhead})</span>}
          {meta.gitDirty && <span className="text-amber-400 ml-0.5">●</span>}
        </span>
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
