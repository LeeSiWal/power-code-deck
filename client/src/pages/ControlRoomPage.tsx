import { useEffect, useMemo, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { agentDeckWS } from '../lib/ws';
import { api } from '../lib/api';
import { useAppStore, type AgentSummary, type PendingApproval } from '../stores/appStore';
import { BottomNav } from '../components/layout/BottomNav';

// Control Room (v0.3.0): the multi-session overview. It renders purely from
// server-computed summaries + the global approval queue — it never watches any one
// session's detailed stream. Initial state comes over REST; live changes arrive as
// agent:summaries / native:approval / approval:resolved deltas applied to the store
// (wired in useWebSocket), so this page just reads and reacts.

const ATTN_ORDER: Record<string, number> = { approval: 0, error: 1, stalled: 2 };

function attnClasses(primary: string): string {
  switch (primary) {
    case 'approval':
      return 'border-deck-warning text-deck-warning';
    case 'error':
      return 'border-deck-danger text-deck-danger';
    case 'stalled':
      return 'border-deck-text-dim text-deck-text-dim';
    default:
      return 'border-deck-border text-deck-text-dim';
  }
}

function attnLabel(r: { kind: string; count?: number }): string {
  const base = r.kind;
  return r.count && r.count > 1 ? `${base} ·${r.count}` : base;
}

function kindGlyph(preset: string): string {
  const p = (preset || '').toLowerCase();
  if (p.includes('codex')) return 'codex';
  if (p.includes('claude')) return 'claude';
  return 'shell';
}

function timeAgo(ms: number): string {
  if (!ms) return '—';
  const s = Math.max(0, Math.floor((Date.now() - ms) / 1000));
  if (s < 5) return 'now';
  if (s < 60) return `${s}s`;
  const m = Math.floor(s / 60);
  if (m < 60) return `${m}m`;
  const h = Math.floor(m / 60);
  if (h < 24) return `${h}h`;
  return `${Math.floor(h / 24)}d`;
}

function dot(hue: number, hollow = false) {
  const color = `hsl(${hue}, 55%, 55%)`;
  return (
    <span
      className="inline-block w-2.5 h-2.5 rounded-full shrink-0"
      style={hollow ? { border: `1.5px solid ${color}` } : { background: color }}
    />
  );
}

export function ControlRoomPage() {
  const navigate = useNavigate();
  const summaries = useAppStore((s) => s.summaries);
  const approvals = useAppStore((s) => s.approvals);
  const setSummaries = useAppStore((s) => s.setSummaries);
  const setApprovals = useAppStore((s) => s.setApprovals);

  const [connected, setConnected] = useState(agentDeckWS.connected);
  const [toast, setToast] = useState<string | null>(null);
  const [sheetOpen, setSheetOpen] = useState(false);

  // Snapshot fetch — on mount and again on every (re)connect, so a dropped socket
  // heals to the true state instead of drifting on the deltas it missed.
  useEffect(() => {
    const resync = () => {
      api.controlSummaries().then((list) => setSummaries(list as AgentSummary[])).catch(() => {});
      api.listApprovals().then((list) => setApprovals(list as PendingApproval[])).catch(() => {});
    };
    resync();
    const offOpen = agentDeckWS.on('open', () => {
      setConnected(true);
      resync();
    });
    const offClose = agentDeckWS.on('close', () => setConnected(false));
    // A decision this client submitted may lose the race to another device — surface
    // that instead of leaving the button looking like it did nothing.
    const offResult = agentDeckWS.on('native:decideResult', (p: any) => {
      if (p?.result === 'already_resolved') showToast('이미 다른 기기에서 처리된 승인입니다');
    });
    const offResolved = agentDeckWS.on('approval:resolved', () => showToast('승인 처리됨'));
    return () => {
      offOpen();
      offClose();
      offResult();
      offResolved();
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  function showToast(msg: string) {
    setToast(msg);
    window.setTimeout(() => setToast((t) => (t === msg ? null : t)), 2500);
  }

  const list = useMemo(() => Object.values(summaries), [summaries]);

  // Attention rail: every session with a primary attention, most urgent first, then
  // oldest-waiting first (longest-stuck rises to the top).
  const attention = useMemo(
    () =>
      list
        .filter((s) => s.attention?.primary)
        .sort((a, b) => {
          const pa = ATTN_ORDER[a.attention.primary] ?? 9;
          const pb = ATTN_ORDER[b.attention.primary] ?? 9;
          if (pa !== pb) return pa - pb;
          return (a.attention.reasons[0]?.since || 0) - (b.attention.reasons[0]?.since || 0);
        }),
    [list],
  );

  // Group by server-provided projectKey; header uses the server's projectLabel.
  const groups = useMemo(() => {
    const byKey = new Map<string, { label: string; agents: AgentSummary[] }>();
    for (const s of list) {
      const g = byKey.get(s.projectKey) || { label: s.projectLabel || s.projectKey, agents: [] };
      g.agents.push(s);
      byKey.set(s.projectKey, g);
    }
    for (const g of byKey.values()) {
      g.agents.sort((a, b) => {
        const aa = a.attention?.primary ? 0 : 1;
        const bb = b.attention?.primary ? 0 : 1;
        if (aa !== bb) return aa - bb;
        return a.name.localeCompare(b.name);
      });
    }
    return [...byKey.values()].sort((a, b) => a.label.localeCompare(b.label));
  }, [list]);

  function decide(a: PendingApproval, behavior: 'allow' | 'deny') {
    agentDeckWS.send('native:decide', { agentId: a.agentId, id: a.requestId, behavior });
    // Optimistic: the approval:resolved broadcast will also remove it, but dropping
    // it now keeps the tap responsive. (Idempotent — remove-by-id is a no-op twice.)
    useAppStore.getState().removeApproval(a.requestId);
  }

  async function restart(id: string) {
    try {
      await api.restartAgent(id);
    } catch (e: any) {
      showToast('재시작 실패: ' + (e?.message || ''));
    }
  }

  async function stop(s: AgentSummary) {
    try {
      await api.stopAgent(s.agentId);
      showToast(`${s.name} 정지됨`);
    } catch (e: any) {
      showToast('정지 실패: ' + (e?.message || ''));
    }
  }

  // 정지 is the reversible stop (keeps the session, can be restarted) — NOT a delete.
  // Disabled when the agent isn't running. Full delete lives on the dashboard.
  const QuickActions = ({ s }: { s: AgentSummary }) => (
    <div className="flex flex-wrap gap-1.5 mt-2 pt-2 border-t border-deck-border-soft">
      <ActBtn onClick={() => navigate(`/agents/${s.agentId}`)}>열기</ActBtn>
      <ActBtn onClick={() => restart(s.agentId)}>재시작</ActBtn>
      <ActBtn disabled={s.status !== 'running'} onClick={() => stop(s)}>정지</ActBtn>
      <ActBtn onClick={() => navigate('/logs')}>로그</ActBtn>
    </div>
  );

  const Tile = ({ s }: { s: AgentSummary }) => {
    const attn = s.attention?.primary;
    return (
      <div
        className={`rounded-lg border bg-deck-surface p-3 ${attn ? 'border-2 ' + attnClasses(attn).split(' ')[0] : 'border-deck-border'}`}
      >
        <div className="flex items-center justify-between gap-2">
          <div className="flex items-center gap-1.5 min-w-0">
            {dot(s.colorHue, s.status !== 'running')}
            <span className="font-mono text-xs font-semibold truncate">{s.name}</span>
            <span className="text-[9px] uppercase tracking-wide px-1 rounded border border-deck-border text-deck-text-faint">
              {kindGlyph(s.preset)}
            </span>
          </div>
          {attn ? (
            <span className={`text-[9px] font-mono px-1.5 rounded-full border ${attnClasses(attn)}`}>
              {attnLabel(s.attention.reasons[0] || { kind: attn })}
            </span>
          ) : (
            <span className="text-[9px] font-mono px-1.5 rounded-full border border-deck-border text-deck-text-dim">
              {s.status}
            </span>
          )}
        </div>
        <div className="font-mono text-[10px] text-deck-text-dim mt-2 leading-relaxed">
          <div className="truncate">tool&nbsp;&nbsp;: {s.lastTool || '—'}</div>
          <div className="truncate">target: {s.lastTarget || '—'}</div>
          <div>
            ×{s.toolCount} · {timeAgo(s.lastActivityAt)}
          </div>
        </div>
        <div className="flex gap-1.5 mt-2">
          <Badge>✓ 완료 {s.unread?.completed ?? 0}</Badge>
          <Badge>⚠ 에러 {s.unread?.errors ?? 0}</Badge>
        </div>
        <QuickActions s={s} />
      </div>
    );
  };

  const ApprovalCard = ({ a }: { a: PendingApproval }) => (
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
          onClick={() => decide(a, 'allow')}
          className="px-2.5 py-1 rounded text-[10px] font-mono font-bold bg-deck-accent text-white active:opacity-80"
        >
          허용
        </button>
        <button
          onClick={() => decide(a, 'deny')}
          className="px-2.5 py-1 rounded text-[10px] font-mono border border-deck-border text-deck-text active:opacity-80"
        >
          거부
        </button>
        <button
          onClick={() => navigate(`/agents/${a.agentId}`)}
          className="px-2.5 py-1 rounded text-[10px] font-mono border border-dashed border-deck-border text-deck-text-dim active:opacity-80"
        >
          세션 열기
        </button>
      </div>
    </div>
  );

  const ApprovalFeed = () => (
    <>
      <h3 className="font-mono text-xs font-semibold">승인 대기 ({approvals.length})</h3>
      <div className="font-mono text-[9.5px] text-deck-text-faint mb-3">전역 피드 — 세션 watch 불필요</div>
      {approvals.length === 0 ? (
        <div className="font-mono text-[10px] text-deck-text-faint py-6 text-center">대기 중인 승인이 없습니다</div>
      ) : (
        approvals.map((a) => <ApprovalCard key={a.requestId} a={a} />)
      )}
    </>
  );

  return (
    <div className="flex flex-col h-full safe-top bg-deck-bg overflow-hidden">
      {/* Top bar */}
      <header className="flex items-center gap-3 px-4 py-2 bg-deck-surface border-b border-deck-border shrink-0">
        <span className="font-mono font-bold text-sm">⌘ PCD</span>
        <span className="font-mono text-[11px] border border-deck-border rounded px-2 py-0.5">/control</span>
        <button
          onClick={() => navigate('/dashboard')}
          className="font-mono text-[10px] border border-dashed border-deck-border rounded px-2 py-0.5 text-deck-text-dim"
        >
          classic dashboard ↗
        </button>
        <div className="flex-1" />
        <span className="font-mono text-[10px] text-deck-text-dim flex items-center gap-1.5">
          {dot(connected ? 145 : 0, !connected)} {connected ? 'WS connected' : '연결 끊김'}
        </span>
        {/* Mobile: toggle approval sheet */}
        <button
          onClick={() => setSheetOpen((v) => !v)}
          className="lg:hidden font-mono text-[10px] border border-deck-border rounded px-2 py-0.5 text-deck-text-dim"
        >
          승인 {approvals.length > 0 ? `(${approvals.length})` : ''}
        </button>
      </header>

      <div className="flex-1 flex min-h-0">
        {/* MAIN */}
        <main className="flex-1 overflow-y-auto min-h-0 p-4 lg:border-r border-deck-border">
          {/* Attention Rail */}
          {attention.length > 0 && (
            <div className="rounded-lg border border-deck-border bg-deck-raised p-2.5 mb-4">
              <div className="flex items-center justify-between mb-2">
                <span className="font-mono text-[10px] font-bold uppercase tracking-wide text-deck-warning">
                  Attention · {attention.length}
                </span>
                <span className="font-mono text-[9px] text-deck-text-faint hidden sm:block">
                  approval &gt; error &gt; stalled · since ↑
                </span>
              </div>
              <div className="flex gap-2.5 overflow-x-auto pb-1">
                {attention.map((s) => (
                  <button
                    key={s.agentId}
                    onClick={() => navigate(`/agents/${s.agentId}`)}
                    className={`min-w-[150px] text-left rounded-md border bg-deck-surface p-2 ${attnClasses(s.attention.primary).split(' ')[0]}`}
                  >
                    <div className="flex items-center gap-1.5 mb-1">
                      {dot(s.colorHue)}
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
          )}

          {/* Project groups */}
          {groups.length === 0 ? (
            <div className="font-mono text-xs text-deck-text-faint py-12 text-center">
              실행 중인 세션이 없습니다.
            </div>
          ) : (
            groups.map((g) => (
              <div key={g.label} className="mb-6">
                <div className="flex items-center gap-2.5 mb-2">
                  <span className="font-mono text-[10px] uppercase tracking-wide text-deck-text-faint">project</span>
                  <span className="font-mono text-[11px] font-semibold truncate">{g.label}</span>
                  <span className="font-mono text-[9px] px-1.5 rounded-full border border-deck-border text-deck-text-dim">
                    {g.agents.length}
                  </span>
                  <span className="flex-1 border-t border-dashed border-deck-border-soft" />
                </div>
                <div className="grid grid-cols-1 sm:grid-cols-2 xl:grid-cols-3 gap-3">
                  {g.agents.map((s) => (
                    <Tile key={s.agentId} s={s} />
                  ))}
                </div>
              </div>
            ))
          )}
        </main>

        {/* SIDE: approval feed (desktop) */}
        <aside className="hidden lg:block w-[290px] shrink-0 overflow-y-auto p-4 bg-deck-surface/40">
          <ApprovalFeed />
        </aside>
      </div>

      {/* Mobile navigation — matches the other top-level pages (md:hidden). Without
          this, entering /control on a phone leaves no way back out. */}
      <BottomNav />

      {/* Mobile approval bottom sheet */}
      {sheetOpen && (
        <div className="lg:hidden fixed inset-0 z-40" onClick={() => setSheetOpen(false)}>
          <div className="absolute inset-0 bg-black/40" />
          <div
            onClick={(e) => e.stopPropagation()}
            className="absolute left-0 right-0 bottom-0 max-h-[70%] overflow-y-auto rounded-t-2xl border-t-2 border-deck-border bg-deck-bg p-4 pb-6 animate-slide-up"
          >
            <div className="w-9 h-1 rounded bg-deck-border mx-auto mb-3" />
            <ApprovalFeed />
          </div>
        </div>
      )}

      {/* Transient resolved / already-resolved toast */}
      {toast && (
        <div className="fixed bottom-4 left-1/2 -translate-x-1/2 z-50 px-3 py-1.5 rounded-md border border-deck-accent bg-deck-raised font-mono text-[10px] text-deck-accent-light animate-fade-in">
          {toast}
        </div>
      )}
    </div>
  );
}

function ActBtn({
  children,
  onClick,
  danger,
  disabled,
}: {
  children: React.ReactNode;
  onClick: () => void;
  danger?: boolean;
  disabled?: boolean;
}) {
  return (
    <button
      onClick={onClick}
      disabled={disabled}
      className={`px-2.5 py-1 rounded text-[10px] font-mono border active:opacity-80 ${
        disabled
          ? 'border-deck-border-soft text-deck-text-faint opacity-50 cursor-not-allowed'
          : danger
            ? 'border-deck-danger text-deck-danger'
            : 'border-deck-border text-deck-text'
      }`}
    >
      {children}
    </button>
  );
}

function Badge({ children }: { children: React.ReactNode }) {
  return (
    <span className="font-mono text-[9px] px-1.5 py-0.5 rounded-full border border-deck-border text-deck-text-dim">
      {children}
    </span>
  );
}
