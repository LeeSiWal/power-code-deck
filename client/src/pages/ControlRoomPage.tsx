import { useEffect, useMemo, useState } from 'react';
import { useNavigate, Link } from 'react-router-dom';
import { agentDeckWS } from '../lib/ws';
import { api } from '../lib/api';
import { useAppStore, type AgentSummary, type PendingApproval } from '../stores/appStore';
import { BottomNav } from '../components/layout/BottomNav';
import { ATTN_ORDER } from '../components/control/liveState';
import { dot } from '../components/control/LiveDot';
import { AttentionRail } from '../components/control/AttentionRail';
import { ProjectGroup } from '../components/control/ProjectGroup';
import { ApprovalFeed } from '../components/control/ApprovalFeed';
import { IconPlus, IconLog, IconSettings } from '../components/icons';

// Control Room (v0.3.0): the multi-session overview. It renders purely from
// server-computed summaries + the global approval queue — it never watches any one
// session's detailed stream. Initial state comes over REST; live changes arrive as
// agent:summaries / native:approval / approval:resolved deltas applied to the store
// (wired in useWebSocket), so this page just reads and reacts.

export function ControlRoomPage() {
  const navigate = useNavigate();
  const summaries = useAppStore((s) => s.summaries);
  const approvals = useAppStore((s) => s.approvals);
  const setSummaries = useAppStore((s) => s.setSummaries);
  const setApprovals = useAppStore((s) => s.setApprovals);

  const [connected, setConnected] = useState(agentDeckWS.connected);
  const [toast, setToast] = useState<string | null>(null);
  const [sheetOpen, setSheetOpen] = useState(false);
  // A slow tick so "작업 중 → 대기" decay and the relative-time labels refresh even when
  // no new summary arrives (a working agent streams updates; a quiet one wouldn't).
  const [, forceTick] = useState(0);
  useEffect(() => {
    const id = window.setInterval(() => forceTick((t) => t + 1), 4000);
    return () => window.clearInterval(id);
  }, []);

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

  // 되돌릴 수 없는 삭제 — 정지된 세션에서만 호출된다(AgentTile). 목록에서 사라지는
  // 것은 서버의 agent:destroyed 이벤트가 처리한다.
  async function destroy(s: AgentSummary) {
    if (!window.confirm(`${s.name} 세션을 삭제할까요? 되돌릴 수 없습니다.`)) return;
    try {
      await api.deleteAgent(s.agentId);
      showToast(`${s.name} 삭제됨`);
    } catch (e: any) {
      showToast('삭제 실패: ' + (e?.message || ''));
    }
  }

  return (
    <div className="flex flex-col h-full safe-top bg-deck-bg overflow-hidden">
      {/* Top bar */}
      <header className="flex items-center gap-3 px-4 py-2 bg-deck-surface border-b border-deck-border shrink-0">
        <span className="font-mono font-bold text-sm">⌘ PCD</span>
        <span className="font-mono text-[11px] border border-deck-border rounded px-2 py-0.5">/control</span>
        <div className="flex-1" />
        {/* 생성은 ProjectSelectPage가 담당한다. ?new=1은 "실행 중이면 바로 세션으로"
            자동 리다이렉트를 건너뛰기 위한 우회 파라미터. */}
        <Link
          to="/?new=1"
          className="flex items-center gap-1.5 px-2.5 py-1 rounded-lg bg-deck-accent text-white text-[11px] font-medium active:opacity-80"
        >
          <IconPlus size={13} />
          <span>프로젝트 추가</span>
        </Link>
        {/* 데스크톱·태블릿에는 BottomNav가 없다(md:hidden). 관제실이 대시보드를 흡수하면서
            대시보드 헤더에 있던 이 두 진입점이 같이 사라져, md+ 화면에서는 로그·설정으로
            갈 수 있는 보이는 경로가 없어졌다(커맨드 팔레트는 숨은 경로다). md+에서만
            보여 모바일 BottomNav와 중복되지 않는다. */}
        <Link
          to="/logs"
          className="hidden md:inline-flex items-center p-2 rounded-lg text-deck-text-dim hover:bg-deck-border/30"
          title="로그"
        >
          <IconLog size={16} />
        </Link>
        <Link
          to="/settings"
          className="hidden md:inline-flex items-center p-2 rounded-lg text-deck-text-dim hover:bg-deck-border/30"
          title="설정"
        >
          <IconSettings size={16} />
        </Link>
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
            <AttentionRail items={attention} onOpen={(agentId) => navigate(`/agents/${agentId}`)} />
          )}

          {/* Project groups */}
          {groups.length === 0 ? (
            <div className="font-mono text-xs text-deck-text-faint py-12 text-center">
              실행 중인 세션이 없습니다.
            </div>
          ) : (
            groups.map((g) => (
              <ProjectGroup
                key={g.label}
                label={g.label}
                agents={g.agents}
                onOpen={(id) => navigate(`/agents/${id}`)}
                onRestart={restart}
                onStop={stop}
                onDestroy={destroy}
              />
            ))
          )}
        </main>

        {/* SIDE: approval feed (desktop) */}
        <aside className="hidden lg:block w-[290px] shrink-0 overflow-y-auto p-4 bg-deck-surface/40">
          <ApprovalFeed
            approvals={approvals}
            summaries={summaries}
            onDecide={decide}
            onOpen={(agentId) => navigate(`/agents/${agentId}`)}
          />
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
            <ApprovalFeed
              approvals={approvals}
              summaries={summaries}
              onDecide={decide}
              onOpen={(agentId) => navigate(`/agents/${agentId}`)}
            />
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
