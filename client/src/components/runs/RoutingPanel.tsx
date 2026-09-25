import { useCallback, useEffect, useMemo, useState } from 'react';
import { ApiError, api, DeciderRecord, Run, RoutingDecision, RoutingMode, RoutingSnapshot, RoutingStrategy, RoutingTimeline } from '../../lib/api';
import { CLASS_LABELS, MODE_LABELS, STRATEGY_LABELS, phaseLabel, reasonLabel, skipLabel, tokens } from '../../lib/routingLabels';

/**
 * RoutingPanel — model routing for one single-task Run.
 *
 * Everything shown comes from the server's recorded decision/transition/attempt
 * history; the panel never decides anything itself. Spend opt-ins and endpoints
 * live in the server's routing.json, so they are shown here but not editable.
 */
const busyPhases = new Set(['routing', 'handoff', 'running', 'quiescing', 'waiting_approval']);

function DeciderView({ r }: { r: DeciderRecord }) {
  if (!r.consulted) {
    return <p className="text-emerald-400/90">판단 호출 생략(최적화): {skipLabel(r.skip || '')}</p>;
  }
  return (
    <div className="space-y-0.5">
      {(r.calls || []).map((c, i) => (
        <p key={i} className="text-deck-text-dim break-all">
          판단 호출 {i + 1} · {c.profileId} · {c.purpose} · {c.latencyMs}ms · {c.valid ? '유효' : `실패(${c.errorClass})`}
          {c.observedModel && ` · 사용 모델 ${c.observedModel}`} · 입력 {tokens(c.usage?.inputTokens)} / 출력 {tokens(c.usage?.outputTokens)}
        </p>
      ))}
      {r.verdict && <p>판단: {r.verdict.action}{r.verdict.profile_id ? ` → ${r.verdict.profile_id}` : ''} ({r.verdict.reason_code}) · {r.applied ? <span className="text-emerald-400">실행에 적용</span> : <span className="text-amber-300">적용 안 함</span>}</p>}
      {r.note && <p className="text-amber-300">{r.note}</p>}
    </div>
  );
}

function DecisionView({ d }: { d: RoutingDecision }) {
  const [open, setOpen] = useState(false);
  const excluded = (d.candidates || []).filter((c) => (c.excluded || []).length > 0);
  return (
    <div className="text-xs space-y-1">
      <div>
        <span className="font-semibold">{d.selected || '선택 없음'}</span>
        <span className="text-deck-text-dim"> · 규칙 최소 등급 {d.ruleTier} · 근거 {d.source}</span>
      </div>
      <p className="whitespace-pre-wrap break-words">{d.reason}</p>
      {d.router && (
        <p className="text-deck-text-dim break-all">
          RouteLLM {d.router.package} · {d.router.checkpoint} · {d.router.device} · 점수 {d.router.score.toFixed(3)} / 임계값 {d.router.threshold} → {d.router.choice}
          {d.routerPair && ` (${d.routerPair.weak} ↔ ${d.routerPair.strong}, ${d.routerPair.calibration || 'uncalibrated'})`} · {d.router.latencyMs}ms{d.router.cached ? ' (캐시)' : ''}
        </p>
      )}
      {d.routerError && <p className="text-amber-300">라우터 사용 불가: {d.routerError}</p>}
      {d.ruleTie && d.ruleTie.length > 1 && <p className="text-deck-text-dim">규칙상 동점 후보: {d.ruleTie.join(', ')}</p>}
      {d.decider && <DeciderView r={d.decider} />}
      {excluded.length > 0 && (
        <button className="underline text-deck-text-dim" onClick={() => setOpen(!open)}>
          제외된 후보 {excluded.length}개 {open ? '숨기기' : '보기'}
        </button>
      )}
      {open && excluded.map((c) => (
        <div key={c.profile.id} className="pl-2 border-l border-deck-border">
          <span className="font-medium">{c.profile.id}</span>: {(c.excluded || []).map((e) => reasonLabel(e.reason)).join(' · ')}
        </div>
      ))}
    </div>
  );
}

export function RoutingPanel({ run, onChanged }: { run: Run; onChanged: () => void }) {
  const [snapshot, setSnapshot] = useState<RoutingSnapshot | null>(null);
  const [timeline, setTimeline] = useState<RoutingTimeline | null>(null);
  const [unavailable, setUnavailable] = useState(false);
  const [profile, setProfile] = useState('');
  const [preview, setPreview] = useState<RoutingDecision | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const [blocked, setBlocked] = useState<RoutingDecision | null>(null);

  const load = useCallback(async () => {
    try {
      const [snap, tl] = await Promise.all([api.routingSnapshot(), api.runRouting(run.id)]);
      setSnapshot(snap);
      setTimeline(tl);
    } catch (err) {
      if (err instanceof ApiError && err.status === 404) setUnavailable(true);
      else setError(err instanceof Error ? err.message : '라우팅 정보를 불러오지 못했습니다');
    }
  }, [run.id]);

  useEffect(() => { load(); }, [load]);
  const phase = timeline?.state.phase || 'idle';
  useEffect(() => {
    if (!busyPhases.has(phase) && !['running', 'awaiting_checks'].includes(run.state)) return;
    const timer = setInterval(async () => {
      try { setTimeline(await api.runRouting(run.id)); } catch { /* keep last view */ }
    }, 2500);
    return () => clearInterval(timer);
  }, [phase, run.id, run.state]);

  const profiles = snapshot?.profiles || [];
  const mode: RoutingMode = timeline?.state.mode || snapshot?.mode || 'off';
  const act = useCallback(async (fn: () => Promise<unknown>) => {
    setBusy(true); setError(''); setBlocked(null);
    try {
      await fn();
      await load();
      onChanged();
    } catch (err) {
      if (err instanceof ApiError && (err.data as { decision?: RoutingDecision })?.decision) setBlocked((err.data as { decision: RoutingDecision }).decision);
      setError(err instanceof Error ? err.message : '요청이 실패했습니다');
      await load();
    } finally { setBusy(false); }
  }, [load, onChanged]);

  const events = useMemo(() => {
    if (!timeline) return [];
    const rows: { at: string; key: string; node: JSX.Element }[] = [];
    for (const t of timeline.transitions) rows.push({ at: t.createdAt, key: `t${t.seq}`, node: <span><span className="text-deck-text-dim">{phaseLabel(t.from)} → </span><strong>{phaseLabel(t.to)}</strong> · {t.cause}{t.detail ? ` · ${t.detail}` : ''}</span> });
    for (const a of timeline.attempts) {
      const r = a.report;
      rows.push({ at: a.finishedAt || a.startedAt, key: `a${a.executionId}`, node: (
        <div className="space-y-0.5">
          <div><strong>{a.profileId}</strong> ({a.adapter}{a.model ? ` · 요청 ${a.model}` : ''}{a.effort ? ` · ${a.effort}` : ''}){r?.observedModel ? ` · 관측 ${r.observedModel}` : ''}
            {r?.observedModel && a.model && r.observedModel !== a.model && <span className="text-amber-300"> · 요청 모델과 다름</span>}</div>
          {r?.reviewer && <div className="text-deck-text-dim">검토: {r.reviewer}{r.reviewUsage ? ` · 입력 ${tokens(r.reviewUsage.inputTokens)} / 출력 ${tokens(r.reviewUsage.outputTokens)}` : ' · 사용량 미보고'}</div>}
          {a.inheritedFrom && <div className="text-deck-text-dim">이전 시도 {a.inheritedFrom}의 변경분을 이어받음 · {a.continuation?.kind}</div>}
          {a.finishedAt && <div>결과: {CLASS_LABELS[a.class] ?? a.class}{a.action ? ` → ${a.action.kind}: ${a.action.reason}` : ''}</div>}
          {r && <div className="text-deck-text-dim">
            라우팅 {r.timings.routingMs}ms · 인계 {r.timings.handoffMs}ms · 실행 {Math.round(r.timings.execMs / 1000)}s · 검증 {Math.round(r.timings.verifyMs / 1000)}s
            {r.usage ? ` · 입력 ${tokens(r.usage.inputTokens)} / 출력 ${tokens(r.usage.outputTokens)} / 캐시읽기 ${tokens(r.usage.cacheReadTokens)} (${r.usage.scope === 'conversation' ? '대화 누적' : '이번 턴'}, ${r.usage.local ? '로컬' : '공급자 보고'})` : ' · 사용량 미보고'}
            {!r.quiesceVerified && <span className="text-amber-300"> · 프로세스 종료 미확인: {r.quiesceDetail}</span>}
          </div>}
        </div>
      ) });
    }
    return rows.sort((x, y) => x.at.localeCompare(y.at));
  }, [timeline]);

  if (unavailable) return null;
  const st = timeline?.state;
  const running = busyPhases.has(phase);
  const lastDecision = timeline?.decisions[timeline.decisions.length - 1];

  return (
    <div className="rounded-xl border border-deck-border bg-deck-surface p-4 space-y-3">
      <div className="flex items-center gap-2 flex-wrap">
        <div className="text-sm font-semibold">모델 라우팅</div>
        <span className="text-[11px] px-2 py-0.5 rounded-full border border-deck-border">{phaseLabel(phase)}</span>
        {st?.currentProfile && <span className="text-xs text-deck-text-dim">현재 {st.currentProfile}</span>}
        {st?.pendingProfile && <span className="text-xs text-amber-300">전환 예약: {st.pendingProfile} ({st.pendingWhen === 'now' ? '즉시' : '다음 경계'})</span>}
        {st && st.attempts > 0 && <span className="text-xs text-deck-text-dim ml-auto">시도 {st.attempts} · 전환 {st.switches}</span>}
      </div>
      {snapshot?.configError && <p className="text-xs text-red-400">routing.json 오류로 라우팅이 꺼져 있습니다: {snapshot.configError}</p>}

      <div className="grid gap-2 sm:grid-cols-3">
        <label className="text-xs space-y-1">
          <span className="text-deck-text-dim">모드</span>
          <select className="w-full bg-deck-bg border border-deck-border rounded-lg px-2 py-1.5" value={mode} disabled={busy || running}
            onChange={(e) => act(() => api.setRunRouting(run.id, { mode: e.target.value as RoutingMode, pinProfile: st?.pinProfile || '', pinAdapter: st?.pinAdapter || '' }))}>
            {(['off', 'manual', 'shadow', 'auto'] as RoutingMode[]).map((m) => <option key={m} value={m}>{MODE_LABELS[m]}</option>)}
          </select>
        </label>
        <label className="text-xs space-y-1">
          <span className="text-deck-text-dim">공급자 고정</span>
          <select className="w-full bg-deck-bg border border-deck-border rounded-lg px-2 py-1.5" value={st?.pinAdapter || ''} disabled={busy || running}
            onChange={(e) => act(() => api.setRunRouting(run.id, { mode, pinProfile: '', pinAdapter: e.target.value }))}>
            <option value="">고정 안 함</option>
            {Array.from(new Set(profiles.map((p) => p.profile.adapter))).map((a) => <option key={a} value={a}>{a}</option>)}
          </select>
        </label>
        <label className="text-xs space-y-1">
          <span className="text-deck-text-dim">프로필 고정</span>
          <select className="w-full bg-deck-bg border border-deck-border rounded-lg px-2 py-1.5" value={st?.pinProfile || ''} disabled={busy || running}
            onChange={(e) => act(() => api.setRunRouting(run.id, { mode, pinProfile: e.target.value, pinAdapter: '' }))}>
            <option value="">고정 안 함</option>
            {profiles.map((p) => <option key={p.profile.id} value={p.profile.id}>{p.profile.id}</option>)}
          </select>
        </label>
      </div>

      <div className="flex flex-wrap gap-3 items-center text-xs">
        <label className="flex items-center gap-2">
          <span className="text-deck-text-dim">판단 전략</span>
          <select className="bg-deck-bg border border-deck-border rounded-lg px-2 py-1.5" value={timeline?.options.strategy || ''} disabled={busy || running}
            onChange={(e) => act(() => api.setRunRouting(run.id, { mode, pinProfile: st?.pinProfile || '', pinAdapter: st?.pinAdapter || '', strategy: e.target.value as RoutingStrategy | '' }))}>
            {(['', 'rules', 'routellm', 'commercial_llm'] as const).map((k) => <option key={k} value={k}>{STRATEGY_LABELS[k]}{k === '' && timeline ? ` (${STRATEGY_LABELS[snapshot?.decider?.strategy || 'rules']})` : ''}</option>)}
          </select>
        </label>
        {timeline?.strategy === 'commercial_llm' && mode === 'shadow' && (
          <label className="flex items-center gap-1.5">
            <input type="checkbox" checked={!!timeline?.options.commercialShadow} disabled={busy || running}
              onChange={(e) => act(() => api.setRunRouting(run.id, { mode, pinProfile: st?.pinProfile || '', pinAdapter: st?.pinAdapter || '', commercialShadow: e.target.checked }))} />
            <span>이 Run에서 상용 판단 Shadow 허용</span>
          </label>
        )}
      </div>
      {timeline?.strategy === 'commercial_llm' && mode === 'shadow' && (
        <p className="text-[11px] text-deck-text-dim">RouteLLM Shadow는 로컬 BERT라 구독 사용량이 없지만, 상용 판단 Shadow는 판단 호출마다 해당 구독 사용량을 씁니다(하루 {snapshot?.decider?.limits.shadowMaxCallsPerDay ?? 0}회 한도, 최근 24시간 {snapshot?.decider?.shadowCallsLast24h ?? 0}회 사용).</p>
      )}
      {mode === 'off' ? (
        <p className="text-xs text-deck-text-dim">라우팅이 꺼져 있습니다. 아래의 기존 실행 버튼이 이 Run의 공급자({run.provider})를 CLI 기본 설정으로 실행합니다.</p>
      ) : (
        <div className="space-y-2">
          <div className="flex gap-2 flex-wrap items-center">
            <select className="bg-deck-bg border border-deck-border rounded-lg px-2 py-1.5 text-xs min-w-0 max-w-full" value={profile} onChange={(e) => setProfile(e.target.value)}>
              <option value="">{mode === 'manual' ? '프로필 선택…' : '자동 선택'}</option>
              {profiles.map((p) => {
                const reasons = p.manualExcluded || [];
                return <option key={p.profile.id} value={p.profile.id} disabled={reasons.length > 0}>
                  {p.profile.id} · {p.profile.model || 'CLI 기본 모델'}{p.profile.effort ? `/${p.profile.effort}` : ''}{p.profile.tiers?.length ? ` · ${p.profile.tiers.join(',')}` : ''}{reasons.length ? ` — ${reasonLabel(reasons[0].reason)}` : (p.manualWarnings || []).length ? ` ⚠ ${reasonLabel((p.manualWarnings || [])[0].reason)}` : ''}
                </option>;
              })}
            </select>
            <button className="text-xs underline" disabled={busy} onClick={async () => {
              setError('');
              try { setPreview(await api.previewRunRouting(run.id, profile)); } catch (err) { setError(err instanceof Error ? err.message : '미리보기 실패'); }
            }}>미리보기</button>
            {!running && !['succeeded', 'canceled'].includes(run.state) && (
              <button className="btn-primary" disabled={busy || (mode === 'manual' && !profile)} onClick={() => act(() => api.startRoutedRun(run.id, profile))}>
                {profile ? '이 프로필로 실행' : mode === 'shadow' ? `${run.provider} 실행 (라우팅 판단 기록)` : '자동 선택으로 실행'}
              </button>
            )}
            {!running && timeline?.strategy === 'commercial_llm' && mode !== 'manual' && !['succeeded', 'canceled'].includes(run.state) && (
              <button className="text-xs border border-deck-border rounded-lg px-2 py-1.5" disabled={busy} onClick={() => act(() => api.startRoutedRun(run.id, '', true))}>판단 다시 요청 후 실행</button>
            )}
            {running && profile && <>
              <button className="text-xs border border-deck-border rounded-lg px-2 py-1.5" disabled={busy} onClick={() => act(() => api.switchRunProfile(run.id, profile, 'boundary'))}>다음 경계에서 전환</button>
              <button className="text-xs border border-amber-500/50 text-amber-300 rounded-lg px-2 py-1.5" disabled={busy} onClick={() => act(() => api.switchRunProfile(run.id, profile, 'now'))}>지금 전환(현재 작업 중단)</button>
            </>}
          </div>
          {phase === 'reconcile' && <p className="text-xs text-amber-300">이전 실행의 종료나 부작용을 확인하지 못했습니다. 작업 공간과 외부 작업 상태를 확인한 뒤 직접 다시 실행하세요. 자동으로 재시작하지 않습니다.</p>}
          {mode === 'auto' && !snapshot?.switching.autoEscalate && <p className="text-[11px] text-deck-text-dim">자동 승격이 꺼져 있어(switching.autoEscalate=false) 검증 실패 후에는 사용자 선택을 기다립니다.</p>}
        </div>
      )}

      {preview && <div className="rounded-lg border border-deck-border p-2"><div className="text-[11px] text-deck-text-dim mb-1">미리보기(기록되지 않음)</div><DecisionView d={preview} /></div>}
      {blocked && <div className="rounded-lg border border-red-500/40 p-2"><div className="text-[11px] text-red-400 mb-1">실행하지 않은 이유</div><DecisionView d={blocked} /></div>}
      {!blocked && lastDecision && <div className="rounded-lg border border-deck-border p-2"><div className="text-[11px] text-deck-text-dim mb-1">마지막 결정{lastDecision.mode === 'shadow' ? ' (Shadow: 실행에는 적용 안 됨)' : ''}</div><DecisionView d={lastDecision} /></div>}
      {error && <p className="text-xs text-red-400 break-words">{error}</p>}

      {timeline && timeline.usage.length > 0 && (
        <div className="text-xs space-y-0.5">
          <div className="text-deck-text-dim">역할별 사용량 (공급자 보고값; 청구액이 아님)</div>
          {timeline.usage.map((u) => (
            <div key={u.role}>{({ decision: '판단', execution: '실행', retry: '재시도', review: '검토', local: '로컬' } as Record<string, string>)[u.role] || u.role}: 호출 {u.calls} · 입력 {u.inputTokens.toLocaleString()} / 출력 {u.outputTokens.toLocaleString()}{!u.complete && <span className="text-amber-300"> · 일부 미보고({u.calls - u.reported}건)</span>}</div>
          ))}
          <div className="font-medium">상용 합계(판단·검토 포함): 입력 {timeline.commercialUsage.inputTokens.toLocaleString()} / 출력 {timeline.commercialUsage.outputTokens.toLocaleString()}{!timeline.commercialUsage.complete && <span className="text-amber-300"> · 미보고 포함, 실제보다 작을 수 있음</span>}</div>
        </div>
      )}
      {events.length > 0 && (
        <details className="text-xs">
          <summary className="cursor-pointer text-deck-text-dim">전환·인계 이력 {events.length}건</summary>
          <ol className="mt-2 space-y-1.5">
            {events.map((e) => <li key={e.key} className="flex gap-2"><span className="text-deck-text-dim shrink-0 font-mono">{e.at.slice(11, 19)}</span><div className="min-w-0 break-words">{e.node}</div></li>)}
          </ol>
          <button className="mt-2 underline text-deck-text-dim" disabled={busy || running} onClick={() => act(() => api.forgetRunRouting(run.id))}>이 Run의 라우팅 기록 삭제</button>
        </details>
      )}
    </div>
  );
}
