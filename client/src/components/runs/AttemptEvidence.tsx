import { useEffect, useRef, useState } from 'react';
import { api, ConflictReport, ConflictVersion, RunArtifact, RunExecution, TaskHistory } from '../../lib/api';
import { ResolutionEditor } from './ResolutionEditor';

type EvidenceActions = {
  runId: string;
  onArtifact: (artifact: RunArtifact, attempt: string) => void;
  onResolve: (report: ConflictReport, attempt: string) => void;
  onRepairStarted?: () => Promise<unknown>;
  repairTaskId?: string;
};
export function visibleArtifact(a: RunArtifact) {
  return a.kind !== 'workspace' && !a.kind.endsWith('_commit') && a.kind !== 'conflicts' && !a.kind.startsWith('conflict:');
}
export function ConflictDetails({ attempt, runId, onArtifact, onResolve, onRepairStarted, repairTaskId }: EvidenceActions & { attempt: RunExecution }) {
  const [open, setOpen] = useState(false);
  const [report, setReport] = useState<ConflictReport | null>(null);
  const [error, setError] = useState('');
  const [editing, setEditing] = useState(false);
  useEffect(() => {
    if (!open) return;
    let disposed = false;
    setError(''); setReport(null);
    api.readRunArtifact(runId, attempt.id, 'conflicts').then((text) => {
      const result = JSON.parse(text) as ConflictReport;
      const validVersion = (v: ConflictVersion) => v && typeof v === 'object' && (v.artifact === undefined || typeof v.artifact === 'string') && (v.unavailable === undefined || typeof v.unavailable === 'string');
      if (!Array.isArray(result.files) || result.files.length > 256 || typeof result.baseCommit !== 'string' || !result.files.every((f) => typeof f.path === 'string' && validVersion(f.base) && validVersion(f.current) && validVersion(f.incoming))) throw new Error('충돌 정보 형식이 올바르지 않습니다');
      if (!disposed) setReport(result);
    }).catch((err) => { if (!disposed) setError(err instanceof Error ? err.message : '충돌 정보를 불러오지 못했습니다'); });
    return () => { disposed = true; };
  }, [open, runId, attempt.id]);
  if (!attempt.artifacts.some((a) => a.kind === 'conflicts')) return null;
  const label = (v: ConflictVersion) => ({ deleted_or_absent: '삭제되었거나 없음', binary: '바이너리 파일', submodule: '하위 저장소', size_limit: '미리보기 크기 초과' }[v.unavailable || ''] || '미리보기 없음');
  return <div className="space-y-2">
    <button className="text-xs underline text-amber-300" onClick={() => setOpen(!open)}>{open ? '충돌 비교 닫기' : '충돌 파일 비교'}</button>
    {open && <div className="border border-amber-500/30 rounded-lg p-3 space-y-3">
      {error && <p role="alert" className="text-xs text-red-300">{error}</p>}
      {!report && !error && <p className="text-xs">불러오는 중…</p>}
      {report && <>
        <p className="text-xs text-deck-text-dim">실패 당시의 내용을 비교합니다. 해결 요청은 새 계획으로 검토하며 이전 실행 기록은 보존됩니다.</p>
        {report.files.map((file) => <div key={file.path} className="space-y-1">
          <p className="text-xs font-mono break-all">{file.path}</p>
          <div className="flex gap-2 flex-wrap">{([['기준 내용', file.base], ['먼저 적용된 내용', file.current], ['추가하려던 내용', file.incoming]] as [string, ConflictVersion][]).map(([name, version]) => <button key={name} className="text-xs underline disabled:opacity-50" disabled={!version.artifact} title={version.artifact ? name : label(version)} onClick={() => onArtifact({ kind: version.artifact!, baseCommit: report.baseCommit }, attempt.id)}>{name}{!version.artifact && ` (${label(version)})`}</button>)}</div>
        </div>)}
        {report.truncated && <p className="text-xs text-amber-300">파일 수 제한으로 일부 충돌만 표시합니다. 전체 내용은 보관된 작업 공간에서 확인해야 합니다.</p>}
        <button className="btn-primary" onClick={() => onResolve(report, attempt.id)}>새 해결 요청 작성</button>
        {onRepairStarted && report.fingerprint && !report.truncated && report.files.every((f) => [f.base, f.current, f.incoming].every((v) => (!v.unavailable || v.unavailable === 'deleted_or_absent') && (!v.mode || ['100644', '100755'].includes(v.mode)))) && <div className="space-y-2">
          <button className="text-xs underline" onClick={() => setEditing(!editing)}>{editing ? '수정 편집기 닫기' : '텍스트 충돌 직접 수정'}</button>
          {editing && <ResolutionEditor runId={runId} attemptId={attempt.id} taskId={repairTaskId} report={report} onStarted={onRepairStarted} />}
        </div>}
      </>}
    </div>}
  </div>;
}

export function AttemptHistory({ taskId, refreshKey, ...actions }: EvidenceActions & { taskId: string; refreshKey: string }) {
  const [open, setOpen] = useState(false);
  const [page, setPage] = useState<TaskHistory>({ attempts: [], nextCursor: '' });
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const generation = useRef(0);
  useEffect(() => {
    const current = ++generation.current;
    if (!open) return;
    setBusy(true); setError(''); setPage({ attempts: [], nextCursor: '' });
    api.getTaskHistory(actions.runId, taskId).then((next) => {
      if (generation.current === current) setPage(next);
    }).catch((err) => { if (generation.current === current) setError(err instanceof Error ? err.message : '실행 기록 조회 실패'); })
      .finally(() => { if (generation.current === current) setBusy(false); });
    return () => { generation.current++; };
  }, [open, actions.runId, taskId, refreshKey]);
  const more = async () => {
    const current = generation.current;
    setBusy(true); setError('');
    try {
      const next = await api.getTaskHistory(actions.runId, taskId, page.nextCursor);
      if (generation.current === current) setPage((prior) => ({ attempts: [...prior.attempts, ...next.attempts.filter((a) => !prior.attempts.some((p) => p.id === a.id))], nextCursor: next.nextCursor }));
    } catch (err) { if (generation.current === current) setError(err instanceof Error ? err.message : '실행 기록 조회 실패'); }
    finally { if (generation.current === current) setBusy(false); }
  };
  const labels: Record<string, string> = { running: '실행 중', verifying: '검증 중', succeeded: '성공', failed: '실패', interrupted: '중단', canceled: '취소' };
  return <div className="mt-2 space-y-2">
    <button className="text-xs underline" onClick={() => setOpen(!open)}>{open ? '실행 기록 닫기' : '모든 실행 기록'}</button>
    {open && <>
      <p className="text-xs text-deck-text-dim">최신 실행부터 표시합니다.</p>
      {error && <p role="alert" className="text-xs text-red-300">{error}</p>}
      {page.attempts.map((attempt) => <div key={attempt.id} className="border border-deck-border rounded-lg p-3 space-y-2">
        <div className="text-xs font-semibold">{labels[attempt.state] || attempt.state} <span className="text-deck-text-dim font-normal">{attempt.id.slice(-8)}</span></div>
        <p className="text-xs whitespace-pre-wrap break-words">{attempt.detail}</p>
        {attempt.checks.map((check) => <p key={check.name} className="text-xs whitespace-pre-wrap break-words">{check.passed ? '✓' : '✕'} {check.name}: {check.detail}</p>)}
        <div className="flex flex-wrap gap-2">{attempt.artifacts.filter(visibleArtifact).map((a) => <button key={a.kind} className="text-xs underline" onClick={() => actions.onArtifact(a, attempt.id)}>{a.kind === 'changes.patch' ? '변경 내용' : a.kind === 'integration_log' ? '통합 오류' : a.kind === 'review_log' ? '독립 리뷰' : a.kind.replace('check_log:', '')}</button>)}</div>
        <ConflictDetails {...actions} attempt={attempt} />
      </div>)}
      {!busy && !page.attempts.length && <p className="text-xs text-deck-text-dim">실행 기록이 없습니다.</p>}
      {busy && <p className="text-xs">불러오는 중…</p>}
      {page.nextCursor && <button className="text-xs underline" disabled={busy} onClick={more}>이전 기록 더 보기</button>}
    </>}
  </div>;
}
