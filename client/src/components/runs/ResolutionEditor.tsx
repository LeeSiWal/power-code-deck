import { useEffect, useState } from 'react';
import { api, ConflictReport, ResolvedFile } from '../../lib/api';

export function ResolutionEditor({ runId, attemptId, report, onStarted }: { runId: string; attemptId: string; report: ConflictReport; onStarted: () => Promise<unknown> }) {
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
      await api.resolveIntegration(runId, attemptId, report.fingerprint!, files.map((f) => ({ ...f, content: f.delete ? '' : f.content })));
      await onStarted();
    } catch (err) {
      setError(err instanceof Error ? err.message : '수정안 검증을 시작하지 못했습니다');
      // A lost response can still mean the durable attempt was created.
      await onStarted().catch(() => undefined);
    }
    finally { setBusy(false); }
  };
  return <div className="space-y-3 border-t border-deck-border pt-3">
    <p className="text-xs">먼저 적용된 내용을 바탕으로 필요한 변경을 합쳐 수정하세요. 저장하면 별도 통합 실행에서 전체 변경과 검사·리뷰를 다시 수행합니다.</p>
    {error && <p role="alert" className="text-xs text-red-300 whitespace-pre-wrap break-words">{error}</p>}
    {files.map((file, index) => <div key={file.path} className="space-y-1">
      <label className="block text-xs font-mono break-all">{file.path}<textarea aria-label={`${file.path} 수정 내용`} rows={8} className="input w-full mt-1 font-mono text-xs" disabled={busy || file.delete} value={file.content} maxLength={262144} onChange={(e) => setFiles((old) => old.map((f, i) => i === index ? { ...f, content: e.target.value } : f))} /></label>
      <label className="text-xs"><input type="checkbox" disabled={busy} checked={file.delete} onChange={(e) => setFiles((old) => old.map((f, i) => i === index ? { ...f, delete: e.target.checked } : f))} /> 결과에서 이 파일 삭제</label>
    </div>)}
    <button className="btn-primary" disabled={busy || files.length !== report.files.length || !files.length} onClick={submit}>{busy ? '처리 중…' : '수정안으로 새 통합·검증 시작'}</button>
  </div>;
}
