import { useEffect, useRef, useState } from 'react';
import { api, DraftHistory as History } from '../../lib/api';

export function DraftHistory({ runId, refreshKey }: { runId: string; refreshKey: string }) {
  const [open, setOpen] = useState(false);
  const [page, setPage] = useState<History>({ drafts: [], nextCursor: '' });
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const [reload, setReload] = useState(0);
  const generation = useRef(0);
  useEffect(() => {
    const current = ++generation.current;
    if (!open) return;
    setBusy(true); setError(''); setPage({ drafts: [], nextCursor: '' });
    api.getDraftHistory(runId).then((next) => {
      if (current === generation.current) setPage(next);
    }).catch((err) => { if (current === generation.current) setError(err instanceof Error ? err.message : '초안 이력 조회 실패'); })
      .finally(() => { if (current === generation.current) setBusy(false); });
    return () => { generation.current++; };
  }, [open, runId, refreshKey, reload]);
  const more = async () => {
    const current = generation.current;
    setBusy(true); setError('');
    try {
      const next = await api.getDraftHistory(runId, page.nextCursor);
      if (current === generation.current) setPage((prior) => ({ drafts: [...prior.drafts, ...next.drafts.filter((d) => !prior.drafts.some((p) => p.id === d.id))], nextCursor: next.nextCursor }));
    } catch (err) { if (current === generation.current) setError(err instanceof Error ? err.message : '초안 이력 조회 실패'); }
    finally { if (current === generation.current) setBusy(false); }
  };
  const labels: Record<string, string> = { running: '생성 중', succeeded: '생성 완료', failed: '생성 실패', interrupted: '중단됨', canceled: '취소됨' };
  return <section className="rounded-xl border border-deck-border bg-deck-surface p-4 space-y-3">
    <button className="text-sm underline" aria-expanded={open} onClick={() => setOpen(!open)}>{open ? '계획 생성 이력 닫기' : '계획 생성 이력 보기'}</button>
    {open && <>
      <p className="text-xs text-deck-text-dim">최신 생성 기록부터 표시합니다. 저장된 초안을 조회하며 현재 편집 내용과 확정된 계획은 그대로 유지됩니다.</p>
      <button className="text-xs underline" disabled={busy} onClick={() => setReload((n) => n + 1)}>이력 새로고침</button>
      {error && <p role="alert" className="text-xs text-red-300">{error}</p>}
      {page.drafts.map((draft) => <div key={draft.id} className="rounded-lg border border-deck-border p-3 space-y-2">
        <p className="text-xs font-semibold">{labels[draft.state] || draft.state} <span className="font-normal text-deck-text-dim">{draft.id.slice(-8)}</span></p>
        <p className="text-xs whitespace-pre-wrap break-words">{draft.detail}</p>
        {draft.plan && <details className="text-xs">
          <summary className="cursor-pointer">초안 내용 · 작업 {draft.plan.tasks.length}개 · 동시 실행 {draft.plan.concurrency}개</summary>
          {draft.plan.tasks.map((task) => <div key={task.id} className="border-t border-deck-border mt-2 pt-2 space-y-1">
            <p className="font-semibold break-words">{task.id} · {task.provider}</p>
            <p className="whitespace-pre-wrap break-words">{task.prompt}</p>
            <p className="text-deck-text-dim break-words">선행 작업: {task.dependsOn?.length ? task.dependsOn.join(', ') : '없음'}</p>
          </div>)}
        </details>}
      </div>)}
      {busy && <p className="text-xs">불러오는 중…</p>}
      {!busy && !error && !page.drafts.length && <p className="text-xs text-deck-text-dim">계획 생성 기록이 없습니다. 직접 편집한 내용은 생성 이력에 포함되지 않습니다.</p>}
      {page.nextCursor && <button className="text-xs underline" disabled={busy} onClick={more}>이전 초안 더 보기</button>}
    </>}
  </section>;
}
