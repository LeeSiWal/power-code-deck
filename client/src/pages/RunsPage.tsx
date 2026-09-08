import { FormEvent, useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { useNavigate, useParams } from 'react-router-dom';
import { ApiError, api, Run, RunApproval, RunArtifact, RunSummary, PlanSnapshot, ApplyPreview, ConflictReport } from '../lib/api';
import { BottomNav } from '../components/layout/BottomNav';
import { IconBack, IconCheck, IconClose, IconPlay, IconRocket, IconSpinner } from '../components/icons';
import { useGoUp } from '../hooks/useGoUp';
import { PlanEditor } from '../components/runs/PlanEditor';
import { DraftHistory } from '../components/runs/DraftHistory';
import { AttemptHistory, ConflictDetails, visibleArtifact } from '../components/runs/AttemptEvidence';

const activeStates = new Set(['queued', 'planning', 'running', 'awaiting_checks', 'plan_running', 'integrating']);

function stateLabel(state: string) {
  switch (state) {
    case 'queued': return '실행 대기';
    case 'pending': return '실행 대기';
    case 'planned': return '계획 저장됨';
    case 'planning': return '계획 초안 생성 중';
    case 'plan_running': return 'Task 실행 중';
    case 'awaiting_integration': return '결과 통합 대기';
    case 'integrating': return '결과 통합·최종 검증 중';
    case 'integration_failed': return '결과 통합·검증 실패';
    case 'verifying': return '검증 중';
    case 'running': return '작업 중';
    case 'awaiting_checks': return '검증 중';
    case 'succeeded': return '완료';
    case 'failed': return '실패';
    case 'interrupted': return '중단됨';
    case 'canceled': return '취소됨';
    case 'applied': return '브랜치 적용 완료';
    case 'applying': return '브랜치 적용 중';
    case 'needs_attention': return '적용 상태 확인 필요';
    default: return state;
  }
}

function stateClass(state: string) {
  if (state === 'succeeded') return 'border-emerald-500/50 text-emerald-400 bg-emerald-500/10';
  if (state === 'failed' || state === 'canceled' || state === 'integration_failed') return 'border-red-500/50 text-red-400 bg-red-500/10';
  if (activeStates.has(state)) return 'border-amber-500/50 text-amber-300 bg-amber-500/10';
  return 'border-deck-border text-deck-text-dim bg-deck-surface';
}

function artifactLabel(artifact: RunArtifact) {
  if (artifact.kind === 'changes.patch') return '변경 내용';
  if (artifact.kind === 'status.txt') return '변경 파일';
  if (artifact.kind === 'review_log') return '독립 리뷰';
  if (artifact.kind === 'integration_log') return '통합 오류 로그';
  if (artifact.kind === 'resolution_plan') return '적용한 충돌 수정안';
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
  const [provider, setProvider] = useState('antigravity');
  const [creationMode, setCreationMode] = useState<'plan' | 'direct'>('plan');
  const formRef = useRef<HTMLFormElement>(null);
  const [resolutionNotice, setResolutionNotice] = useState('');
  const [approvals, setApprovals] = useState<RunApproval[]>([]);
  const [deciding, setDeciding] = useState('');
  const [plan, setPlan] = useState<PlanSnapshot | null>(null);
  const [applyPreview, setApplyPreview] = useState<ApplyPreview | null>(null);
  const applicationPending = plan?.applications.some((a) => a.state === 'applying') ?? false;
  const selectedID = useRef(id);
  selectedID.current = id;
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
      if (selectedID.current !== runId) return null;
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
    setApplyPreview(null);
    setPlan(null);
    setApprovals([]);
    if (!id) {
      setRun(null);
      return;
    }
    setRun(null);
    loadRun(id);
  }, [id, loadRun]);

  useEffect(() => {
    if (!id || run?.id !== id || !['planned', 'plan_running', 'awaiting_integration', 'integrating', 'integration_failed', 'succeeded', 'canceled'].includes(run.state)) return;
    let disposed = false;
    let timer: ReturnType<typeof setTimeout>;
    const poll = async () => {
      try {
        const next = await api.getRunPlan(id);
        if (!disposed) setPlan(next);
      } catch (err) {
        if (!disposed && !(err instanceof ApiError && err.status === 404)) setError('작업 계획을 불러오지 못했습니다');
      } finally {
        if (!disposed && (['plan_running', 'integrating'].includes(run.state) || applicationPending)) timer = setTimeout(poll, 1500);
      }
    };
    poll();
    return () => { disposed = true; clearTimeout(timer); };
  }, [id, run?.id, run?.state, applicationPending]);

  useEffect(() => {
    if (!id || run?.id !== id || !['running', 'plan_running'].includes(run.state)) { setApprovals([]); return; }
    let disposed = false;
    let timer: ReturnType<typeof setTimeout>;
    const poll = async () => {
      try {
        const pending = await api.runApprovals(id);
        if (!disposed) setApprovals(pending || []);
      } catch (err) {
        if (!disposed && !(err instanceof ApiError && err.status === 409)) {
          setError('승인 요청을 불러오지 못했습니다. 연결을 확인하세요.');
        }
      } finally {
        if (!disposed) timer = setTimeout(poll, 1500);
      }
    };
    poll();
    return () => { disposed = true; clearTimeout(timer); };
  }, [id, run?.id, run?.state]);

  const decide = async (request: RunApproval, behavior: 'allow' | 'deny') => {
    if (!run || deciding) return;
    setDeciding(request.id);
    try {
      await api.decideRunApproval(run.id, request.id, behavior);
      setApprovals((items) => items.filter((item) => item.id !== request.id));
    } catch (err) {
      setError(err instanceof Error ? err.message : '승인을 처리하지 못했습니다');
    } finally { setDeciding(''); }
  };

  useEffect(() => {
    if (!id || !run || !activeStates.has(run.state)) return;
    const timer = window.setInterval(async () => {
      const next = await loadRun(id);
      if (next && !activeStates.has(next.state)) loadList();
    }, 1500);
    return () => window.clearInterval(timer);
  }, [id, run?.state, loadRun, loadList]);

  const latest = useMemo(() => run?.executions[run.executions.length - 1], [run]);

  const refreshPlanEditor = useCallback(async () => {
    if (id) await loadRun(id);
    await loadList();
  }, [id, loadRun, loadList]);

  const refreshRepair = useCallback(async () => {
    if (!id) return;
    await loadRun(id);
    const next = await api.getRunPlan(id);
    if (selectedID.current === id) setPlan(next);
    await loadList();
  }, [id, loadRun, loadList]);

  const start = useCallback(async (runId: string, planned = false) => {
    setSubmitting(true);
    setError('');
    try {
      if (planned) await api.startRunPlan(runId);
      else await api.startRun(runId);
      await loadRun(runId);
      if (planned) {
        const snapshot = await api.getRunPlan(runId);
        if (selectedID.current === runId) setPlan(snapshot);
      }
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
    setResolutionNotice('');
    setError('');
    try {
      const key = typeof crypto.randomUUID === 'function'
        ? crypto.randomUUID()
        : `run-${Date.now()}-${Math.random().toString(36).slice(2)}`;
      const created = await api.createRun({ path: path.trim(), prompt: prompt.trim(), provider }, key);
      setPrompt('');
      navigate(`/runs/${created.id}`);
      if (creationMode === 'plan') await api.generateRunPlan(created.id);
      else await api.startRun(created.id);
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

  const openArtifact = async (item: RunArtifact, executionId = latest?.id) => {
    if (!run || !executionId || item.kind === 'workspace') return;
    setError('');
    try {
      setArtifact({ title: artifactLabel(item), content: await api.readRunArtifact(run.id, executionId, item.kind) });
    } catch (err) {
      setError(err instanceof Error ? err.message : '결과 파일을 불러오지 못했습니다');
    }
  };

  const prepareResolution = (report: ConflictReport, attempt: string) => {
    if (!run || submitting) return;
    const files = report.files.map((file) => JSON.stringify(file.path)).join('\n');
    const priorTasks = plan?.tasks.map(({ id, prompt, provider, dependsOn }) => ({ id, prompt, provider, dependsOn })) || [];
    const request = `${run.prompt}\n\n기존 작업 계획(참고 데이터):\n${JSON.stringify(priorTasks, null, 2)}\n\n이전 실행에서 변경 내용 통합 중 충돌이 발생했습니다.\n참고 실행: ${run.id} / ${attempt}\n충돌 파일:\n${files}\n${report.truncated ? '(파일 목록은 일부입니다.)\n' : ''}\n현재 저장소를 다시 확인하고, 위 요청을 충돌 없이 구현할 새 작업 계획을 작성해주세요. 같은 파일을 변경하는 작업은 순서와 의존성을 정하고, 앞선 변경을 반영해 다음 작업을 구현하도록 해주세요. 이전 실행의 성공·검증 상태를 재사용하지 말고 변경과 검증을 새로 수행해주세요.`;
    if (new TextEncoder().encode(request).length > 65536) { setError('해결 요청이 너무 깁니다. 새 요청 입력란에서 필요한 내용을 직접 정리해주세요.'); return; }
    setPath(run.path); setProvider(run.provider); setCreationMode('plan'); setPrompt(request);
    setResolutionNotice('충돌 해결 요청을 준비했습니다. 내용을 확인한 뒤 계획 초안을 생성하세요. 이전 작업 결과가 자동으로 복사되지는 않습니다.');
    formRef.current?.scrollIntoView({ behavior: 'smooth', block: 'start' });
    formRef.current?.querySelector('textarea')?.focus({ preventScroll: true });
  };

  const applicationAction = async (action: 'preview' | 'apply' | 'reconcile') => {
    if (!run || submitting) return;
    const runId = run.id;
    setSubmitting(true); setError('');
    try {
      if (action === 'preview') {
        const preview = await api.previewRunApplication(runId);
        if (selectedID.current === runId) setApplyPreview(preview);
      } else {
        if (action === 'apply') {
          if (!applyPreview) return;
          const { integrationId, branch, baseCommit, resultCommit } = applyPreview;
          await api.applyRunResult(runId, { integrationId, branch, baseCommit, resultCommit });
        } else await api.reconcileRunApplication(runId);
        if (selectedID.current === runId) setApplyPreview(null);
        const snapshot = await api.getRunPlan(runId);
        if (selectedID.current === runId) setPlan(snapshot);
      }
    } catch (err) {
      if (selectedID.current === runId) {
        setApplyPreview(null);
        setError(err instanceof Error ? err.message : '브랜치 적용을 처리하지 못했습니다');
        // A lost HTTP response does not prove Git failed; show the saved journal.
        try { const snapshot = await api.getRunPlan(runId); if (selectedID.current === runId) setPlan(snapshot); } catch { /* retain the original error */ }
      }
    } finally { setSubmitting(false); }
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
          <form ref={formRef} onSubmit={create} className="rounded-xl border border-deck-border bg-deck-surface p-3 space-y-3">
            {resolutionNotice && <p role="status" className="text-xs text-amber-300">{resolutionNotice}</p>}
            <div>
              <div className="text-xs font-semibold">새 작업</div>
              <div className="text-[11px] text-deck-text-dim mt-0.5">선택한 에이전트가 구현하고 Antigravity가 독립 리뷰합니다.</div>
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
              <span className="text-[11px] text-deck-text-dim">선호 구현 에이전트</span>
              <select value={provider} onChange={(event) => setProvider(event.target.value)} className="input w-full mt-1">
                <option value="antigravity">Antigravity</option>
                <option value="claude">Claude Code</option>
                <option value="codex">Codex</option>
              </select>
            </label>
            <label className="block">
              <span className="text-[11px] text-deck-text-dim">할 일</span>
              <textarea value={prompt} onChange={(event) => setPrompt(event.target.value)} rows={4} placeholder="추가하거나 고칠 내용을 설명하세요" className="input w-full mt-1 resize-y text-sm" />
            </label>
            <button type="submit" disabled={disabled || submitting || !path.trim() || !prompt.trim()} className="btn-primary w-full justify-center disabled:opacity-40">
              {submitting ? <IconSpinner size={14} className="animate-spin" /> : <IconPlay size={14} color="#fff" />}
              {creationMode === 'plan' ? '계획 초안 만들기' : '바로 작업 시작'}
            </button>
            <label className="block text-xs">진행 방식 <select value={creationMode} onChange={(e) => setCreationMode(e.target.value as 'plan' | 'direct')} className="input w-full mt-1">
              <option value="plan">계획을 확인한 뒤 실행</option><option value="direct">기존 단일 작업 바로 실행</option>
            </select></label>
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
                      <IconPlay size={13} color="#fff" /> {run.state === 'queued' ? '단일 작업 실행' : '다시 실행'}
                    </button>
                  )}
                  {(['planning', 'running', 'awaiting_checks', 'planned', 'plan_running', 'awaiting_integration', 'integrating', 'integration_failed'].includes(run.state)) && (
                    <button onClick={cancel} disabled={submitting} className="px-2.5 py-1.5 rounded-lg border border-red-500/40 text-red-300 text-xs disabled:opacity-40">중단</button>
                  )}
                </div>
              </div>

              {['queued', 'planning'].includes(run.state) && run.executions.length === 0 && <PlanEditor key={run.id} run={run} refresh={refreshPlanEditor} />}
              <DraftHistory key={`draft-history-${run.id}`} runId={run.id} refreshKey={run.state} />

              {plan && (
                <div className="rounded-xl border border-deck-border bg-deck-surface p-4 space-y-3">
                  <div className="text-sm font-semibold">작업 계획 · {plan.tasks.length}개 Task</div>
                  <p className="text-xs text-deck-text-dim">Task가 모두 통과하면 결과를 통합하고 최종 검증합니다. 검증 후 대상 브랜치와 변경 내용을 확인해 결과를 적용할 수 있습니다.</p>
                  {run.state === 'succeeded' && !plan.applications.some((a) => ['applying', 'needs_attention', 'applied'].includes(a.state)) && !applyPreview && <button className="btn-primary" disabled={submitting} onClick={() => applicationAction('preview')}>브랜치 적용 내용 확인</button>}
                  {applyPreview && <div className="rounded-lg border border-amber-500/40 p-3 space-y-2">
                    <p className="text-sm font-semibold">적용 대상: {applyPreview.branch.replace(/^refs\/heads\//, '')}</p>
                    <p className="text-xs font-mono break-all">{applyPreview.baseCommit} → {applyPreview.resultCommit}</p>
                    <pre className="text-xs whitespace-pre-wrap break-words max-h-48 overflow-auto">{applyPreview.summary || '파일 내용 변경 없음'}</pre>
                    <button className="text-xs underline" onClick={() => openArtifact({ kind: 'changes.patch', baseCommit: applyPreview.baseCommit }, applyPreview.integrationId)}>전체 변경 내용 열기</button>
                    <p className="text-xs">이 브랜치의 파일과 커밋을 검증된 결과로 진행합니다. 적용 중에는 다른 Git 작업을 실행하지 마세요.</p>
                    <div className="flex gap-2"><button className="btn-primary" disabled={submitting} onClick={() => applicationAction('apply')}>확인한 결과 적용</button><button disabled={submitting} className="text-xs underline" onClick={() => setApplyPreview(null)}>닫기</button></div>
                  </div>}
                  {plan.applications.map((a) => <div key={a.id} className="border-t border-deck-border pt-3 space-y-1">
                    <p className="text-sm font-semibold">{stateLabel(a.state)} · {a.branch.replace(/^refs\/heads\//, '')}</p>
                    <p className="text-xs whitespace-pre-wrap break-words">{a.detail}</p>
                    {['needs_attention', 'applying'].includes(a.state) && <button className="btn-primary" disabled={submitting} onClick={() => applicationAction('reconcile')}>실제 브랜치 상태 확인</button>}
                  </div>)}
                  {['awaiting_integration', 'integration_failed'].includes(run.state) && <button className="btn-primary" disabled={submitting} onClick={async () => {
                    setSubmitting(true); setError('');
                    try {
                      await api.integrateRunPlan(run.id);
                      await loadRun(run.id);
                      const snapshot = await api.getRunPlan(run.id);
                      if (selectedID.current === run.id) setPlan(snapshot);
                      await loadList();
                    } catch (err) { setError(err instanceof Error ? err.message : '결과 통합을 시작하지 못했습니다'); }
                    finally { setSubmitting(false); }
                  }}>{run.state === 'integration_failed' ? '결과 통합 다시 시도' : '결과 통합·최종 검증'}</button>}
                  {run.state === 'planned' && plan.selection.ready.length > 0 && <button className="btn-primary" disabled={submitting} onClick={() => start(run.id, true)}>계획 실행</button>}
                  {plan.tasks.map((task) => (
                    <div key={task.id} className="border-t border-deck-border pt-3">
                      <div className="flex gap-2 items-center text-xs">
                        <strong>{task.id}</strong><span>{task.provider}</span>
                        <span className="ml-auto">{plan.selection.blocked.includes(task.id) ? '선행 작업 실패로 대기' : stateLabel(task.state)}</span>
                      </div>
                      <p className="text-xs mt-1 whitespace-pre-wrap break-words">{task.prompt}</p>
                      {task.dependsOn.length > 0 && <p className="text-[11px] mt-1 text-deck-text-dim">선행 작업: {task.dependsOn.join(', ')}</p>}
                      {task.detail && <p className="text-xs mt-1 whitespace-pre-wrap break-words">{task.detail}</p>}
                      {task.checks.map((check) => <p key={check.name} className="text-xs mt-1">{check.passed ? '✓' : '✕'} {check.name}: {check.detail}</p>)}
                      <div className="flex gap-2 flex-wrap mt-2">{task.artifacts.filter(visibleArtifact).map((a) => <button key={a.kind} className="text-xs underline" onClick={() => openArtifact(a, task.attemptId)}>{artifactLabel(a)}</button>)}</div>
                      <ConflictDetails key={task.attemptId} runId={run.id} attempt={{ id: task.attemptId, state: task.state, detail: task.detail, checks: task.checks, artifacts: task.artifacts }} onArtifact={openArtifact} onResolve={prepareResolution} repairTaskId={task.id} onRepairStarted={run.state === 'planned' && task.state === 'failed' ? refreshRepair : undefined} />
                      <AttemptHistory key={`${run.id}:${task.id}`} runId={run.id} taskId={task.id} refreshKey={`${task.attemptId}:${task.state}`} onArtifact={openArtifact} onResolve={prepareResolution} />
                      {task.state === 'failed' && run.state === 'planned' && <button className="btn-primary mt-2" disabled={submitting} onClick={async () => {
                        setSubmitting(true);
                        try {
                          await api.retryRunTask(run.id, task.id);
                          const snapshot = await api.getRunPlan(run.id);
                          if (selectedID.current === run.id) setPlan(snapshot);
                        }
                        catch (err) { setError(err instanceof Error ? err.message : '재시도 준비 실패'); }
                        finally { setSubmitting(false); }
                      }}>재시도 준비</button>}
                    </div>
                  ))}
                  {plan.integrations.map((attempt, index) => (
                    <div key={attempt.id} className="border-t border-deck-border pt-3 space-y-2">
                      <div className="text-sm font-semibold">최종 통합 #{index + 1} · {stateLabel(attempt.state)}</div>
                      <p className="text-xs whitespace-pre-wrap break-words">{attempt.detail}</p>
                      {attempt.checks.map((check) => <p key={check.name} className="text-xs whitespace-pre-wrap break-words">{check.passed ? '✓' : '✕'} {check.name}: {check.detail}</p>)}
                      <div className="flex gap-2 flex-wrap">{attempt.artifacts.filter(visibleArtifact).map((a) => <button key={a.kind} className="text-xs underline" onClick={() => openArtifact(a, attempt.id)}>{artifactLabel(a)}</button>)}</div>
                      <ConflictDetails runId={run.id} attempt={attempt} onArtifact={openArtifact} onResolve={prepareResolution} onRepairStarted={run.state === 'integration_failed' && plan.integrations[plan.integrations.length - 1]?.id === attempt.id ? refreshRepair : undefined} />
                      {attempt.artifacts.filter((a) => a.kind === 'result_commit').map((a) => <p key={a.kind} className="text-xs font-mono break-all">검증 결과: {a.baseCommit}</p>)}
                    </div>
                  ))}
                </div>
              )}

              {approvals.length > 0 && (
                <div className="rounded-xl border border-amber-500/40 bg-deck-surface p-4 space-y-3">
                  <div className="text-sm font-semibold">승인이 필요합니다</div>
                  {approvals.map((request) => (
                    <div key={request.id} className="space-y-2">
                      <div className="text-xs font-medium">{request.toolName}</div>
                      {plan && <div className="text-xs text-deck-text-dim">Task: {plan.tasks.find((task) => task.attemptId === request.sessionId)?.id || request.sessionId}</div>}
                      <pre className="text-xs whitespace-pre-wrap break-words max-h-48 overflow-auto">{JSON.stringify(request.input, null, 2)}</pre>
                      <div className="flex gap-2">
                        <button disabled={!!deciding} onClick={() => decide(request, 'allow')} className="btn-primary disabled:opacity-40">이번 요청 허용</button>
                        <button disabled={!!deciding} onClick={() => decide(request, 'deny')} className="px-3 py-2 text-xs border border-deck-border rounded-lg disabled:opacity-40">거부</button>
                      </div>
                    </div>
                  ))}
                </div>
              )}

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
