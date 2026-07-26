import type { AgentSummary, PendingApproval } from '../../stores/appStore';
import { timeAgo } from './liveState';

function ApprovalCard({
  a,
  summaries,
  onDecide,
  onOpen,
}: {
  a: PendingApproval;
  summaries: Record<string, AgentSummary>;
  onDecide: (a: PendingApproval, behavior: 'allow' | 'deny') => void;
  onOpen: (agentId: string) => void;
}) {
  return (
    <div className="rounded-lg border border-deck-border bg-deck-surface p-3 mb-2.5">
      <div className="flex items-center justify-between">
        <span className="font-mono text-[10.5px] font-semibold truncate">
          {summaries[a.agentId]?.name || a.agentId}
        </span>
        <span className="font-mono text-[10px] text-deck-text-dim">{timeAgo(Date.parse(a.askedAt))}</span>
      </div>
      <div className="font-mono text-[11px] my-1.5">{a.toolName}</div>
      {a.input != null && (
        <div className="border border-dashed border-deck-border-soft rounded p-1.5 mb-2 max-h-16 overflow-hidden">
          <pre className="font-mono text-[9px] text-deck-text-dim whitespace-pre-wrap break-all">
            {JSON.stringify(a.input).slice(0, 160)}
          </pre>
        </div>
      )}
      <div className="flex gap-2">
        <button
          onClick={() => onDecide(a, 'allow')}
          className="px-2.5 py-1 rounded text-[10px] font-mono font-bold bg-deck-accent text-white active:opacity-80"
        >
          허용
        </button>
        <button
          onClick={() => onDecide(a, 'deny')}
          className="px-2.5 py-1 rounded text-[10px] font-mono border border-deck-border text-deck-text active:opacity-80"
        >
          거부
        </button>
        <button
          onClick={() => onOpen(a.agentId)}
          className="px-2.5 py-1 rounded text-[10px] font-mono border border-dashed border-deck-border text-deck-text-dim active:opacity-80"
        >
          세션 열기
        </button>
      </div>
    </div>
  );
}

export function ApprovalFeed({
  approvals,
  summaries,
  onDecide,
  onOpen,
}: {
  approvals: PendingApproval[];
  summaries: Record<string, AgentSummary>;
  onDecide: (a: PendingApproval, behavior: 'allow' | 'deny') => void;
  onOpen: (agentId: string) => void;
}) {
  return (
    <>
      <h3 className="font-mono text-xs font-semibold">승인 대기 ({approvals.length})</h3>
      <div className="font-mono text-[9.5px] text-deck-text-faint mb-3">전역 피드 — 세션 watch 불필요</div>
      {approvals.length === 0 ? (
        <div className="font-mono text-[10px] text-deck-text-faint py-6 text-center">대기 중인 승인이 없습니다</div>
      ) : (
        approvals.map((a) => (
          <ApprovalCard
            key={a.requestId}
            a={a}
            summaries={summaries}
            onDecide={onDecide}
            onOpen={onOpen}
          />
        ))
      )}
    </>
  );
}
