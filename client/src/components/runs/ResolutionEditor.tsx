import { useEffect, useState } from 'react';
import { api, ConflictReport, ResolvedFile } from '../../lib/api';

export function ResolutionEditor({ runId, attemptId, taskId, report, onStarted }: { runId: string; attemptId: string; taskId?: string; report: ConflictReport; onStarted: () => Promise<unknown> }) {
  const [files, setFiles] = useState<ResolvedFile[]>([]);
  const [busy, setBusy] = useState(true);
  const [error, setError] = useState('');
  useEffect(() => {
    let disposed = false;
    setBusy(true); setError(''); setFiles([]);
    const load = async () => {
      const loaded: ResolvedFile[] = [];
      for (let start = 0; start < report.files.length; start += 4) {
        if (disposed) return;
        loaded.push(...await Promise.all(report.files.slice(start, start + 4).map(async (f) => {
          const artifact = f.current.artifact || f.incoming.artifact || f.base.artifact;
          return { path: f.path, content: artifact ? await api.readRunArtifact(runId, attemptId, artifact) : '', delete: false };
        })));
      }
      if (!disposed) setFiles(loaded);
    };
    load().catch((err) => { if (!disposed) setError(err instanceof Error ? err.message : '편집 내용을 불러오지 못했습니다'); })
      .finally(() => { if (!disposed) setBusy(false); });
    return () => { disposed = true; };
  }, [runId, attemptId, report]);
  const submit = async () => {
    setBusy(true); setError('');
    try {
      const resolved = files.map((f) => ({ ...f, content: f.delete ? '' : f.content }));
      if (taskId) await api.resolveTask(runId, taskId, attemptId, report.fingerprint!, resolved);
      else await api.resolveIntegration(runId, attemptId, report.fingerprint!, resolved);
      await onStarted();
    } catch (err) {
      setError(err instanceof Error ? err.message : '수정안 검증을 시작하지 못했습니다');
      // A lost response can still mean the durable attempt was created.
      await onStarted().catch(() => undefined);
    }
    finally { setBusy(false); }
  };
  return <div className="space-y-3 border-t border-deck-border pt-3">
    <p className="text-xs">먼저 적용된 내용을 바탕으로 필요한 변경을 합쳐 수정하세요. 수정안은 새 작업 공간에 적용되며 검사·리뷰를 다시 수행합니다.</p>
    {taskId && <p className="text-xs text-deck-text-dim">수정한 의존성을 바탕으로 이 Task를 새로 실행하고 검사·리뷰합니다. 후속 Task와 최종 통합에서 충돌이 다시 발생하면 별도 수정이 필요합니다.</p>}
    {error && <p role="alert" className="text-xs text-red-300 whitespace-pre-wrap break-words">{error}</p>}
    {files.map((file, index) => <div key={file.path} className="space-y-1">
      <label className="block text-xs font-mono break-all">{file.path}<textarea aria-label={`${file.path} 수정 내용`} rows={8} className="input w-full mt-1 font-mono text-xs" disabled={busy || file.delete} value={file.content} maxLength={262144} onChange={(e) => setFiles((old) => old.map((f, i) => i === index ? { ...f, content: e.target.value } : f))} /></label>
      <label className="text-xs"><input type="checkbox" disabled={busy} checked={file.delete} onChange={(e) => setFiles((old) => old.map((f, i) => i === index ? { ...f, delete: e.target.checked } : f))} /> 결과에서 이 파일 삭제</label>
    </div>)}
    <button className="btn-primary" disabled={busy || files.length !== report.files.length || !files.length} onClick={submit}>{busy ? '처리 중…' : taskId ? '수정안으로 Task 재실행·검증' : '수정안으로 새 통합·검증 시작'}</button>
  </div>;
}
