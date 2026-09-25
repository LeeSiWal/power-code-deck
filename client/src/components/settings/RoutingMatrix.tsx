import { useCallback, useEffect, useState } from 'react';
import { ApiError, api, RoutingObservation, RoutingSnapshot } from '../../lib/api';
import { MODE_LABELS, STRATEGY_LABELS, reasonLabel } from '../../lib/routingLabels';

/**
 * RoutingMatrix — which executors are installed, signed in, billable, allowed
 * and technically supported, kept as separate columns so "not installed",
 * "policy review pending" and "billing unknown" never collapse into one
 * "unavailable". Read-only: spend opt-ins and profiles live in routing.json.
 */
function Cell({ o }: { o: RoutingObservation }) {
  const good = ['installed', 'authenticated', 'subscription_included', 'local', 'supported', 'allowed_for_scope', 'healthy', 'entitled'].includes(o.value);
  const bad = ['not_installed', 'not_authenticated', 'auth_expired', 'consumer_auth_discontinued', 'blocked', 'wrong_binary', 'not_implemented', 'unsupported', 'rate_limited', 'quota_exhausted'].includes(o.value);
  return (
    <td className="px-2 py-1.5 align-top" title={[o.source, o.detail].filter(Boolean).join(' — ')}>
      <span className={good ? 'text-emerald-400' : bad ? 'text-red-400' : 'text-amber-300'}>{o.value || 'unknown'}</span>
    </td>
  );
}

export function RoutingMatrix() {
  const [snap, setSnap] = useState<RoutingSnapshot | null>(null);
  const [state, setState] = useState<'loading' | 'off' | 'error' | 'ok'>('loading');
  const [busy, setBusy] = useState(false);

  const load = useCallback(async (refresh = false) => {
    setBusy(true);
    try {
      setSnap(refresh ? await api.routingRefresh() : await api.routingSnapshot());
      setState('ok');
    } catch (err) {
      setState(err instanceof ApiError && err.status === 404 ? 'off' : 'error');
    } finally { setBusy(false); }
  }, []);
  useEffect(() => { load(); }, [load]);

  return (
    <section className="rounded-xl border border-deck-border bg-deck-surface p-4 space-y-3">
      <div className="flex items-center gap-2">
        <h2 className="text-sm font-semibold">모델 라우팅 · 실행기 상태</h2>
        {state === 'ok' && <button className="ml-auto text-xs underline" disabled={busy} onClick={() => load(true)}>{busy ? '확인 중…' : '다시 확인'}</button>}
      </div>
      {state === 'loading' && <p className="text-xs text-deck-text-dim">확인 중…</p>}
      {state === 'off' && <p className="text-xs text-deck-text-dim">2.0 실행 기능이 꺼져 있습니다(PCD_V2_ENABLED=1로 활성화).</p>}
      {state === 'error' && <p className="text-xs text-red-400">라우팅 상태를 불러오지 못했습니다.</p>}
      {snap && state === 'ok' && (
        <>
          <p className="text-xs text-deck-text-dim">
            기본 모드 {MODE_LABELS[snap.mode]} · 구독 외 과금 {snap.spend.allowApiMetered || snap.spend.allowPurchasedCredits || snap.spend.allowPlanCredits || snap.spend.allowPromoCredits ? '일부 허용됨' : '모두 차단'} · 과금 미확인 경로 {snap.spend.allowUnknownBilling ? '허용' : '차단'} · 자동 승격 {snap.switching.autoEscalate ? `허용(전환 ${snap.switching.maxSwitchesPerRun}회·시도 ${snap.switching.maxAttemptsPerRun}회·${snap.switching.maxRunMinutes}분)` : '꺼짐'}
          </p>
          {snap.configError && <p className="text-xs text-red-400">routing.json 오류: {snap.configError}</p>}
          <div className="overflow-x-auto">
            <table className="text-[11px] w-full min-w-[640px]">
              <thead className="text-deck-text-dim text-left">
                <tr>{['실행기', '버전', '설치', '인증', '과금', '기술 지원', '건강', '모델 수'].map((h) => <th key={h} className="px-2 py-1 font-medium">{h}</th>)}</tr>
              </thead>
              <tbody className="divide-y divide-deck-border/50">
                {snap.adapters.map((a) => (
                  <tr key={a.adapterId}>
                    <td className="px-2 py-1.5 font-medium">{a.adapterId}{a.inheritedEnv?.length ? <div className="text-amber-300 font-normal">env: {a.inheritedEnv.join(', ')}</div> : null}</td>
                    <td className="px-2 py-1.5">{a.version || '—'}{a.needsRevalidation && <div className="text-amber-300">{a.validatedVersion ? `재검증 필요 (검증: ${a.validatedVersion})` : '검증 기록 없음'} <button className="underline" onClick={async () => { await api.routingMarkValidated(a.adapterId); load(); }}>확인함</button></div>}</td>
                    <Cell o={a.installation} /><Cell o={a.auth} /><Cell o={a.billing} /><Cell o={a.technical} /><Cell o={a.health} />
                    <td className="px-2 py-1.5">{a.models?.length ?? '—'}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
          <div className="text-xs font-semibold pt-1">실행 프로필 (코드 작업 기준)</div>
          {(snap.profiles || []).length === 0 && <p className="text-xs text-deck-text-dim">설치된 실행기가 없거나 routing.json에 프로필이 없습니다. 존재하지 않는 모델을 만들어 등급을 채우지 않습니다.</p>}
          <div className="space-y-2">
            {(snap.profiles || []).map((v) => (
              <div key={v.profile.id} className="border-t border-deck-border pt-2 text-xs space-y-0.5">
                <div className="flex flex-wrap gap-x-2">
                  <span className="font-medium">{v.profile.id}</span>
                  <span className="text-deck-text-dim">{v.profile.adapter} · {v.profile.model || 'CLI 기본 모델'}{v.profile.effort ? ` · ${v.profile.effort}` : ''} · 등급 {v.profile.tiers?.length ? v.profile.tiers.join(', ') : '매핑 없음'} · 과금 {v.profile.billing || 'unknown'}{v.profile.generated ? ' · 자동 생성(수동 전용)' : ''}</span>
                </div>
                <div>수동 선택: {(v.manualExcluded || []).length ? <span className="text-red-400">{(v.manualExcluded || []).map((e) => reasonLabel(e.reason)).join(' · ')}</span> : <span className="text-emerald-400">가능</span>}{(v.manualWarnings || []).length > 0 && <span className="text-amber-300"> (주의: {(v.manualWarnings || []).map((e) => reasonLabel(e.reason)).join(' · ')})</span>}</div>
                <div>자동 선택: {(v.automaticExcluded || []).length ? <span className="text-amber-300">{(v.automaticExcluded || []).map((e) => reasonLabel(e.reason)).join(' · ')}</span> : <span className="text-emerald-400">가능</span>}</div>
                <div className="text-deck-text-dim">이용 검토: 개인·직접 {v.policy.personal_interactive?.value} · 개인·자동 {v.policy.personal_automatic?.value} · 무인 {v.policy.unattended?.value}</div>
              </div>
            ))}
          </div>
          <div className="text-xs font-semibold pt-1">작업 배분 판단 · 보조 역할</div>
          <p className="text-xs text-deck-text-dim">
            기본 판단 전략: {STRATEGY_LABELS[snap.decider.strategy]} · 판단 호출은 실제 후보가 둘 이상이고 규칙으로 가려지지 않을 때만 합니다.
            제한: 호출당 {snap.decider.limits.timeoutSeconds}초, 출력 {snap.decider.limits.maxOutputBytes}바이트, 형식 재시도 {snap.decider.limits.maxFormatRetries}회, 근거 보강 {snap.decider.limits.maxContextRounds}회.
            {!snap.decider.callable && ' 판단 실행기가 연결되지 않았습니다.'}
          </p>
          <div className="text-xs">판단용 프로필(순서대로): {(snap.decider.ordered || []).length ? (snap.decider.ordered || []).map((id) => {
            const st = snap.decider.stats[id];
            const verified = (snap.decider.candidates || []).find((c) => c.profile.id === id)?.profile.quality?.decide?.status === 'verified';
            return `${id}${st ? ` (실측 ${st.calls}회, p50 ${st.p50Ms}ms, 유효 ${st.valid}/${st.calls})` : ' (미측정)'}${verified ? '' : ' · 품질 미검증 → 기록용'}`;
          }).join(' → ') : <span className="text-amber-300">없음 — 규칙·직접 선택으로 처리합니다</span>}</div>
          {(snap.decider.candidates || []).filter((c) => (c.excluded || []).length > 0 && !(c.excluded || []).some((e) => e.reason === 'role_not_allowed')).map((c) => (
            <div key={c.profile.id} className="text-xs text-deck-text-dim">{c.profile.id}: {(c.excluded || []).map((e) => reasonLabel(e.reason)).join(' · ')}</div>
          ))}
          {Object.entries(snap.decider.roles).map(([role, byProvider]) => (
            <div key={role} className="text-xs">
              <span className="font-medium">{role === 'reviewer' ? '리뷰어' : '계획'}</span>
              {Object.entries(byProvider).map(([provider, label]) => (
                <div key={provider} className={label.startsWith('unavailable') ? 'text-amber-300 pl-2' : 'text-deck-text-dim pl-2'}>{provider} 실행 시: {label.startsWith('unavailable') ? `허용된 프로필 없음 — 수동 확인 필요 (${label.slice(13)})` : label}</div>
              ))}
            </div>
          ))}
          <p className="text-[11px] text-deck-text-dim">이용 검토 상태는 {snap.policy.reviewedAt}에 공식 문서를 검토한 기록이며 법률 판단이 아닙니다. 사용자의 동의 체크로 바뀌지 않습니다.</p>
        </>
      )}
    </section>
  );
}
