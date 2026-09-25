import { useEffect, useRef, useState } from 'react';
import { api, CleanupPreview } from '../../lib/api';

export function WorkspaceCleanup({ runId, state }: { runId: string; state: string }) {
  const [page, setPage] = useState<CleanupPreview | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const [notice, setNotice] = useState('');
  const [selected, setSelected] = useState('');
  const generation = useRef(0);
  useEffect(() => { generation.current++; setPage(null); setError(''); setNotice(''); setSelected(''); setBusy(false); return () => { generation.current++; }; }, [runId, state]);
  const load = async (before = '') => {
    const current = generation.current;
    setBusy(true); setError(''); setSelected('');
    try {
      const next = await api.previewCleanup(runId, before);
      if (current === generation.current) setPage((prior) => before && prior ? { candidates: [...prior.candidates, ...next.candidates.filter((c) => !prior.candidates.some((p) => p.id === c.id))], nextCursor: next.nextCursor } : next);
    } catch (err) { if (current === generation.current) setError(err instanceof Error ? err.message : '정리 대상 조회 실패'); }
    finally { if (current === generation.current) setBusy(false); }
  };
  const remove = async () => {
    const candidate = page?.candidates.find((c) => c.id === selected);
    if (!candidate?.eligible || !candidate.fingerprint) return;
    const current = generation.current;
    setBusy(true); setError(''); setNotice('');
    try {
      await api.cleanupWorkspace(runId, candidate.id, candidate.fingerprint);
      if (current !== generation.current) return;
      setNotice('작업 공간을 정리했습니다. 로그와 보관 커밋은 유지됩니다.');
      await load();
    } catch (err) {
      if (current === generation.current) { setError(err instanceof Error ? err.message : '정리 결과를 확인하지 못했습니다. 대상을 다시 조회하세요.'); setSelected(''); setPage(null); }
    } finally { if (current === generation.current) setBusy(false); }
  };
  const labels: Record<string, string> = { draft: '계획 초안', task: 'Task 실행', integration: '최종 통합', execution: '단일 실행' };
  return <section className="rounded-xl border border-deck-border bg-deck-surface p-4 space-y-3">
    <h2 className="text-sm font-semibold">보관 작업 공간 정리</h2>
    <p className="text-xs text-deck-text-dim">파일이 보관 커밋과 일치하는 종료된 작업 공간만 정리합니다. 로그·초안·충돌 스냅샷·커밋은 보존됩니다. 추가 파일이나 미해결 충돌이 있는 폴더는 제외합니다.</p>
    <button className="text-xs underline" disabled={busy} onClick={() => load()}>정리 대상 확인</button>
    {error && <p role="alert" className="text-xs text-red-300 whitespace-pre-wrap break-words">{error}</p>}
    {notice && <p role="status" className="text-xs">{notice}</p>}
    {page?.candidates.map((c) => <div key={c.id} className="border-t border-deck-border pt-2 space-y-1 text-xs">
      <p>{labels[c.kind] || c.kind} · {c.id.slice(-8)}</p><p className="text-deck-text-dim">{c.reason}</p>
      {c.eligible && <button className="underline" disabled={busy} onClick={() => setSelected(c.id)}>이 작업 공간 정리 선택</button>}
      {selected === c.id && <div className="space-y-2"><p>이 작업 공간 폴더를 삭제합니다. 계속하려면 아래 버튼을 누르세요.</p><button className="btn-primary" disabled={busy} onClick={remove}>확인한 작업 공간 삭제</button> <button className="underline" disabled={busy} onClick={() => setSelected('')}>취소</button></div>}
    </div>)}
    {page && !page.candidates.length && <p className="text-xs">등록된 작업 공간이 없습니다.</p>}
    {page?.nextCursor && <button className="text-xs underline" disabled={busy} onClick={() => load(page.nextCursor)}>다음 대상 보기</button>}
    {busy && <p className="text-xs">확인 중…</p>}
  </section>;
}
