import { useEffect, useRef, useState } from 'react';
import { api, ApiError, PlanDraft, Run, TaskPlan } from '../../lib/api';

export function PlanEditor({ run, refresh }: { run: Run; refresh: () => Promise<unknown> }) {
  const [draft, setDraft] = useState<PlanDraft | null>(null);
  const [plan, setPlan] = useState<TaskPlan>({ concurrency: 2, tasks: [{ id: 'task_1', prompt: run.prompt, provider: run.provider, dependsOn: [] }] });
  const [busy, setBusy] = useState(false);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const adopted = useRef('');
  const alive = useRef(true);
  useEffect(() => { alive.current = true; return () => { alive.current = false; }; }, []);
  const planning = run.state === 'planning' || draft?.state === 'running';

  useEffect(() => {
    let disposed = false;
    let timer: ReturnType<typeof setTimeout>;
    const poll = async () => {
      let keepPolling = run.state === 'planning' || draft?.state === 'running';
      try {
        const next = await api.getRunPlanDraft(run.id);
        if (disposed) return;
        setDraft(next);
        if (next.state === 'succeeded' && next.plan && adopted.current !== next.id) {
          setPlan(next.plan); adopted.current = next.id;
        }
        keepPolling = next.state === 'running';
        if (!keepPolling && run.state === 'planning') await refresh();
      } catch (err) {
        if (!disposed && !(err instanceof ApiError && err.status === 404)) setError('계획 초안을 불러오지 못했습니다');
      } finally {
        if (!disposed) {
          setLoading(false);
          if (keepPolling) timer = setTimeout(poll, 1500);
        }
      }
    };
    poll();
    return () => { disposed = true; clearTimeout(timer); };
  }, [run.id, run.state, draft?.id, refresh]);

  const generate = async () => {
    setBusy(true); setError('');
    try {
      const result = await api.generateRunPlan(run.id);
      if (!alive.current) return;
      setDraft({ id: result.draftId, state: 'running', detail: '', plan: null });
      await refresh();
    } catch (err) {
      if (alive.current) {
        setError(err instanceof Error ? err.message : '계획 생성 실패');
        // A lost start response may still have created a durable draft.
        try { const next = await api.getRunPlanDraft(run.id); if (alive.current) setDraft(next); } catch { /* retain the start error */ }
        if (alive.current) await refresh();
      }
    }
    finally { if (alive.current) setBusy(false); }
  };
  const save = async () => {
    setBusy(true); setError('');
    try { await api.saveRunPlan(run.id, plan); if (alive.current) await refresh(); }
    catch (err) { if (alive.current) setError(err instanceof Error ? err.message : '계획 확정 실패'); }
    finally { if (alive.current) setBusy(false); }
  };
  const blocked = busy || loading || planning;
  return <section className="rounded-xl border border-deck-border bg-deck-surface p-4 space-y-3">
    <h2 className="text-sm font-semibold">실행 전 작업 계획</h2>
    <p className="text-xs text-deck-text-dim">요청을 직접 나누거나 Antigravity로 초안을 생성하세요. 새 초안은 현재 편집 내용을 대체합니다. 계획 확정 전 편집 내용은 이 화면에만 유지됩니다.</p>
    <button className="btn-primary" disabled={blocked} onClick={generate}>요청으로 새 초안 생성</button>
    {planning && <p className="text-xs">저장소를 읽고 계획을 작성하고 있습니다…</p>}
    {draft && ['failed', 'interrupted'].includes(draft.state) && <p className="text-xs text-amber-300 whitespace-pre-wrap break-words">{draft.detail} 직접 편집하거나 새 초안을 생성할 수 있습니다.</p>}
    {error && <p role="alert" className="text-xs text-red-300 whitespace-pre-wrap break-words">{error}</p>}
    <label className="block text-xs">동시 실행 수 <input type="number" min={1} max={64} disabled={blocked} value={plan.concurrency} onChange={(e) => setPlan({ ...plan, concurrency: Number(e.target.value) })} className="input w-20 ml-2" /></label>
    {plan.tasks.map((task, index) => <div key={task.id} className="border-t border-deck-border pt-3 space-y-2">
      <div className="flex items-center justify-between"><strong className="text-xs">작업 {index + 1}</strong><button className="text-xs underline" disabled={blocked || plan.tasks.length === 1} onClick={() => setPlan({ ...plan, tasks: plan.tasks.filter((t) => t.id !== task.id).map((t) => ({ ...t, dependsOn: t.dependsOn.filter((id) => id !== task.id) })) })}>삭제</button></div>
      <textarea aria-label={`작업 ${index + 1} 내용`} disabled={blocked} rows={3} maxLength={65536} value={task.prompt} onChange={(e) => setPlan({ ...plan, tasks: plan.tasks.map((t) => t.id === task.id ? { ...t, prompt: e.target.value } : t) })} className="input w-full text-sm" />
      <label className="block text-xs">담당 에이전트 <select disabled={blocked} value={task.provider} onChange={(e) => setPlan({ ...plan, tasks: plan.tasks.map((t) => t.id === task.id ? { ...t, provider: e.target.value } : t) })} className="input ml-2">
        <option value="antigravity">Antigravity</option><option value="claude">Claude Code</option><option value="codex">Codex</option>
      </select></label>
      {plan.tasks.length > 1 && <fieldset className="space-y-1"><legend className="text-xs text-deck-text-dim">먼저 완료되어야 할 작업</legend>{plan.tasks.map((dep, i) => dep.id === task.id ? null : <label key={dep.id} className="block text-xs">
        <input type="checkbox" disabled={blocked} checked={task.dependsOn.includes(dep.id)} onChange={(e) => setPlan({ ...plan, tasks: plan.tasks.map((t) => t.id === task.id ? { ...t, dependsOn: e.target.checked ? [...t.dependsOn, dep.id] : t.dependsOn.filter((id) => id !== dep.id) } : t) })} /> 작업 {i + 1}: {dep.prompt.slice(0, 60)}
      </label>)}</fieldset>}
    </div>)}
    <div className="flex gap-2 flex-wrap">
      <button className="text-xs underline" disabled={blocked || plan.tasks.length >= 64} onClick={() => {
        let n = 1; while (plan.tasks.some((t) => t.id === `task_${n}`)) n++;
        setPlan({ ...plan, tasks: [...plan.tasks, { id: `task_${n}`, prompt: '', provider: run.provider, dependsOn: [] }] });
      }}>작업 추가</button>
      <button className="btn-primary" disabled={blocked || plan.tasks.some((t) => !t.prompt.trim()) || plan.concurrency < 1 || plan.concurrency > 64 || !Number.isInteger(plan.concurrency)} onClick={save}>계획 확정</button>
    </div>
    <p className="text-xs text-deck-text-dim">확정 후 계획은 고정됩니다. 확정한 계획에서 실행을 시작할 수 있습니다.</p>
  </section>;
}
