import { FormEvent, useCallback, useEffect, useMemo, useState } from 'react';
import { useNavigate, useParams } from 'react-router-dom';
import { ApiError, api, Run, RunArtifact, RunSummary } from '../lib/api';
import { BottomNav } from '../components/layout/BottomNav';
import { IconBack, IconCheck, IconClose, IconPlay, IconRocket, IconSpinner } from '../components/icons';
import { useGoUp } from '../hooks/useGoUp';

const activeStates = new Set(['queued', 'running', 'awaiting_checks']);

function stateLabel(state: string) {
  switch (state) {
    case 'queued': return '실행 대기';
    case 'running': return '작업 중';
    case 'awaiting_checks': return '검증 중';
    case 'succeeded': return '완료';
    case 'failed': return '실패';
    case 'interrupted': return '중단됨';
    case 'canceled': return '취소됨';
    default: return state;
  }
}

function stateClass(state: string) {
  if (state === 'succeeded') return 'border-emerald-500/50 text-emerald-400 bg-emerald-500/10';
  if (state === 'failed' || state === 'canceled') return 'border-red-500/50 text-red-400 bg-red-500/10';
  if (state === 'running' || state === 'awaiting_checks') return 'border-amber-500/50 text-amber-300 bg-amber-500/10';
  return 'border-deck-border text-deck-text-dim bg-deck-surface';
}

function artifactLabel(artifact: RunArtifact) {
  if (artifact.kind === 'changes.patch') return '변경 내용';
  if (artifact.kind === 'status.txt') return '변경 파일';
  if (artifact.kind === 'review_log') return '독립 리뷰';
  if (artifact.kind.startsWith('check_log:')) return `${artifact.kind.slice('check_log:'.length)} 로그`;
  return artifact.kind;
}

function shortPath(path: string) {
  const parts = path.split('/').filter(Boolean);
  return parts[parts.length - 1] || path;
}

export function RunsPage() {
  const { id } = useParams();
  const navigate = useNavigate();
  const goUp = useGoUp('/control');
  const [runs, setRuns] = useState<RunSummary[]>([]);
  const [run, setRun] = useState<Run | null>(null);
  const [recentProjects, setRecentProjects] = useState<{ name: string; path: string }[]>([]);
  const [path, setPath] = useState('');
  const [prompt, setPrompt] = useState('');
  const [loading, setLoading] = useState(true);
  const [submitting, setSubmitting] = useState(false);
  const [disabled, setDisabled] = useState(false);
  const [error, setError] = useState('');
  const [artifact, setArtifact] = useState<{ title: string; content: string } | null>(null);

  const loadList = useCallback(async () => {
    try {
      const result = await api.listRuns();
      setRuns(result.runs || []);
      setDisabled(false);
    } catch (err) {
      if (err instanceof ApiError && err.status === 404) setDisabled(true);
      else setError(err instanceof Error ? err.message : 'Run 목록을 불러오지 못했습니다');
    } finally {
      setLoading(false);
    }
  }, []);

  const loadRun = useCallback(async (runId: string) => {
    try {
      const next = await api.getRun(runId);
      setRun(next);
      setRuns((current) => current.map((item) => item.id === next.id
        ? { id: next.id, path: next.path, prompt: next.prompt, provider: next.provider, state: next.state }
        : item));
      setError('');
      return next;
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Run을 불러오지 못했습니다');
      return null;
    }
  }, []);

  useEffect(() => {
    loadList();
    api.recentProjects(8)
      .then((items: any) => setRecentProjects(Array.isArray(items) ? items : []))
      .catch(() => {});
  }, [loadList]);

  useEffect(() => {
    setArtifact(null);
    if (!id) {
      setRun(null);
      return;
    }
    setRun(null);
    loadRun(id);
  }, [id, loadRun]);

  useEffect(() => {
    if (!id || !run || !activeStates.has(run.state)) return;
    const timer = window.setInterval(async () => {
      const next = await loadRun(id);
      if (next && !activeStates.has(next.state)) loadList();
    }, 1500);
    return () => window.clearInterval(timer);
  }, [id, run?.state, loadRun, loadList]);

  const latest = useMemo(() => run?.executions[run.executions.length - 1], [run]);

  const start = useCallback(async (runId: string) => {
    setSubmitting(true);
    setError('');
    try {
      await api.startRun(runId);
      await loadRun(runId);
      await loadList();
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Run을 시작하지 못했습니다');
    } finally {
      setSubmitting(false);
    }
  }, [loadList, loadRun]);

  const create = async (event: FormEvent) => {
    event.preventDefault();
    if (!path.trim() || !prompt.trim() || submitting) return;
    setSubmitting(true);
    setError('');
    try {
      const key = typeof crypto.randomUUID === 'function'
        ? crypto.randomUUID()
        : `run-${Date.now()}-${Math.random().toString(36).slice(2)}`;
      const created = await api.createRun({ path: path.trim(), prompt: prompt.trim(), provider: 'antigravity' }, key);
      setPrompt('');
      navigate(`/runs/${created.id}`);
      await api.startRun(created.id);
      await Promise.all([loadRun(created.id), loadList()]);
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Run을 만들지 못했습니다');
    } finally {
      setSubmitting(false);
    }
  };

  const cancel = async () => {
    if (!run || submitting) return;
    setSubmitting(true);
    setError('');
    try {
      await api.cancelRun(run.id);
      await Promise.all([loadRun(run.id), loadList()]);
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Run을 중단하지 못했습니다');
    } finally {
      setSubmitting(false);
    }
  };

  const openArtifact = async (item: RunArtifact) => {
    if (!run || !latest || item.kind === 'workspace') return;
    setError('');
    try {
      setArtifact({ title: artifactLabel(item), content: await api.readRunArtifact(run.id, latest.id, item.kind) });
    } catch (err) {
      setError(err instanceof Error ? err.message : '결과 파일을 불러오지 못했습니다');
    }
  };

  return (
    <div className="flex flex-col h-full safe-top bg-deck-bg overflow-hidden">
      <header className="flex items-center gap-2 px-4 py-2 bg-deck-surface border-b border-deck-border shrink-0">
        <button onClick={goUp} className="hidden md:inline-flex p-1 -ml-1 rounded hover:bg-deck-border/30 text-deck-text-dim" title="관제실로">
          <IconBack size={15} />
        </button>
        <IconRocket size={16} color="#818cf8" />
        <span className="text-sm font-medium">Runs</span>
        <span className="text-[10px] text-deck-text-dim">PowerCodeDeck 2.0</span>
      </header>

      <main className="flex-1 min-h-0 overflow-y-auto md:overflow-hidden md:flex">
        <aside className="md:w-[340px] md:shrink-0 md:overflow-y-auto border-b md:border-b-0 md:border-r border-deck-border p-4">
          <form onSubmit={create} className="rounded-xl border border-deck-border bg-deck-surface p-3 space-y-3">
            <div>
              <div className="text-xs font-semibold">새 작업</div>
              <div className="text-[11px] text-deck-text-dim mt-0.5">Antigravity가 별도 작업 공간에서 구현하고 검증합니다.</div>
            </div>
            <label className="block">
              <span className="text-[11px] text-deck-text-dim">프로젝트 폴더</span>
              <input value={path} onChange={(event) => setPath(event.target.value)} placeholder="/Users/me/code/project" className="input w-full mt-1 font-mono text-xs" />
            </label>
            {recentProjects.length > 0 && (
              <div className="flex gap-1.5 overflow-x-auto pb-0.5">
                {recentProjects.map((project) => (
                  <button key={project.path} type="button" onClick={() => setPath(project.path)} title={project.path}
                    className="shrink-0 px-2 py-1 rounded-md border border-deck-border text-[10px] text-deck-text-dim hover:text-deck-text">
                    {project.name}
                  </button>
                ))}
              </div>
            )}
            <label className="block">
              <span className="text-[11px] text-deck-text-dim">할 일</span>
              <textarea value={prompt} onChange={(event) => setPrompt(event.target.value)} rows={4} placeholder="추가하거나 고칠 내용을 설명하세요" className="input w-full mt-1 resize-y text-sm" />
            </label>
            <button type="submit" disabled={disabled || submitting || !path.trim() || !prompt.trim()} className="btn-primary w-full justify-center disabled:opacity-40">
              {submitting ? <IconSpinner size={14} className="animate-spin" /> : <IconPlay size={14} color="#fff" />}
              작업 시작
            </button>
          </form>

          <div className="mt-5 mb-2 text-[11px] font-medium uppercase tracking-wider text-deck-text-dim">최근 Runs</div>
          {loading ? (
            <div className="text-xs text-deck-text-dim py-3">불러오는 중…</div>
          ) : disabled ? (
            <div className="rounded-lg border border-deck-border p-3 text-xs text-deck-text-dim">
              2.0 실행 기능이 꺼져 있습니다. 서버에서 PCD_V2_ENABLED=1로 활성화할 수 있습니다.
            </div>
          ) : runs.length === 0 ? (
            <div className="text-xs text-deck-text-dim py-3">아직 Run이 없습니다.</div>
          ) : (
            <div className="space-y-1.5">
              {runs.map((item) => (
                <button key={item.id} onClick={() => navigate(`/runs/${item.id}`)}
                  className={`w-full text-left rounded-lg border p-2.5 ${item.id === id ? 'border-deck-accent bg-deck-accent/10' : 'border-deck-border bg-deck-surface hover:bg-deck-border/20'}`}>
                  <div className="flex items-center gap-2">
                    <span className="text-xs font-medium truncate flex-1">{shortPath(item.path)}</span>
                    <span className={`text-[9px] px-1.5 py-0.5 rounded-full border ${stateClass(item.state)}`}>{stateLabel(item.state)}</span>
                  </div>
                  <div className="text-[11px] text-deck-text-dim truncate mt-1">{item.prompt}</div>
                </button>
              ))}
            </div>
          )}
        </aside>

        <section className="flex-1 min-w-0 md:overflow-y-auto p-4">
          {error && <div className="mb-3 rounded-lg border border-red-500/40 bg-red-500/10 text-red-300 px-3 py-2 text-xs">{error}</div>}
          {!run ? (
            <div className="h-full min-h-48 flex items-center justify-center text-sm text-deck-text-dim">새 작업을 시작하거나 최근 Run을 선택하세요.</div>
          ) : (
            <div className="max-w-4xl mx-auto space-y-4">
              <div className="rounded-xl border border-deck-border bg-deck-surface p-4">
                <div className="flex items-start gap-3">
                  <div className="min-w-0 flex-1">
                    <div className="flex items-center gap-2 mb-2">
                      <span className="font-semibold truncate">{shortPath(run.path)}</span>
                      <span className={`text-[10px] px-2 py-0.5 rounded-full border ${stateClass(run.state)}`}>{stateLabel(run.state)}</span>
                    </div>
                    <p className="text-sm whitespace-pre-wrap break-words">{run.prompt}</p>
                    <div className="mt-2 text-[10px] font-mono text-deck-text-dim truncate" title={run.path}>{run.path}</div>
                  </div>
                  {(run.state === 'queued' || run.state === 'failed' || run.state === 'interrupted') && (
                    <button onClick={() => start(run.id)} disabled={submitting} className="btn-primary shrink-0 disabled:opacity-40">
                      <IconPlay size={13} color="#fff" /> {run.state === 'queued' ? '시작' : '다시 실행'}
                    </button>
                  )}
                  {(run.state === 'running' || run.state === 'awaiting_checks') && (
                    <button onClick={cancel} disabled={submitting} className="px-2.5 py-1.5 rounded-lg border border-red-500/40 text-red-300 text-xs disabled:opacity-40">중단</button>
                  )}
                </div>
              </div>

              {latest && (
                <>
                  <div className="rounded-xl border border-deck-border bg-deck-surface overflow-hidden">
                    <div className="px-4 py-2.5 border-b border-deck-border text-xs font-semibold">검증</div>
                    <div className="divide-y divide-deck-border/50">
                      {run.requiredChecks.map((name) => {
                        const check = latest.checks.find((item) => item.name === name);
                        return (
                          <div key={name} className="px-4 py-3 flex items-start gap-3">
                            {check ? (check.passed ? <IconCheck size={15} color="#34d399" /> : <IconClose size={15} color="#f87171" />) : <IconSpinner size={15} color="#fbbf24" className={activeStates.has(run.state) ? 'animate-spin' : ''} />}
                            <div className="min-w-0">
                              <div className="text-xs font-medium">{name === 'diff_check' ? '변경 형식' : name === 'review' ? '독립 리뷰' : name}</div>
                              {check?.detail && <div className="text-[11px] text-deck-text-dim mt-1 whitespace-pre-wrap break-words">{check.detail}</div>}
                            </div>
                          </div>
                        );
                      })}
                    </div>
                  </div>

                  {latest.detail && (
                    <div className="rounded-xl border border-deck-border bg-deck-surface p-4">
                      <div className="text-xs font-semibold mb-2">작업 결과</div>
                      <div className="text-xs text-deck-text-dim whitespace-pre-wrap break-words">{latest.detail}</div>
                    </div>
                  )}

                  {latest.artifacts.some((item) => item.kind !== 'workspace') && (
                    <div className="rounded-xl border border-deck-border bg-deck-surface p-4">
                      <div className="text-xs font-semibold mb-2">결과 파일</div>
                      <div className="flex flex-wrap gap-2">
                        {latest.artifacts.filter((item) => item.kind !== 'workspace').map((item) => (
                          <button key={item.kind} onClick={() => openArtifact(item)} className="px-2.5 py-1.5 rounded-lg border border-deck-border text-xs hover:bg-deck-border/30">
                            {artifactLabel(item)}
                          </button>
                        ))}
                      </div>
                    </div>
                  )}
                </>
              )}

              {artifact && (
                <div className="rounded-xl border border-deck-border bg-deck-surface overflow-hidden">
                  <div className="px-4 py-2.5 border-b border-deck-border flex items-center">
                    <span className="text-xs font-semibold flex-1">{artifact.title}</span>
                    <button onClick={() => setArtifact(null)} className="p-1 text-deck-text-dim"><IconClose size={14} /></button>
                  </div>
                  <pre className="p-4 max-h-[55vh] overflow-auto text-[11px] leading-relaxed whitespace-pre-wrap break-words font-mono">{artifact.content || '(내용 없음)'}</pre>
                </div>
              )}
            </div>
          )}
        </section>
      </main>
      <BottomNav />
    </div>
  );
}
