import { useCallback, useEffect, useState } from 'react';
import { api, isFeatureOff, RoutingObservation, RoutingProfile, RoutingSnapshot } from '../../lib/api';
import { MODE_LABELS, STRATEGY_LABELS, adapterName, observationLabel, parseRoleLabel, profileName, reasonLabel } from '../../lib/routingLabels';

/**
 * RoutingMatrix — which executors are installed, signed in, billable, allowed
 * and technically supported, kept as separate columns so "not installed",
 * "policy review pending" and "billing unknown" never collapse into one
 * "unavailable". Read-only: spend opt-ins and profiles live in routing.json.
 */
const GOOD = ['installed', 'authenticated', 'subscription_included', 'local', 'supported', 'allowed_for_scope', 'healthy', 'entitled'];
const BAD = ['not_installed', 'not_authenticated', 'auth_expired', 'consumer_auth_discontinued', 'blocked', 'wrong_binary', 'not_implemented', 'unsupported', 'rate_limited', 'quota_exhausted'];

function Chip({ label, o }: { label: string; o: RoutingObservation }) {
  const tone = GOOD.includes(o.value) ? 'text-emerald-400' : BAD.includes(o.value) ? 'text-red-400' : 'text-amber-300';
  return (
    <span className="inline-flex gap-1 rounded bg-deck-bg px-1.5 py-0.5" title={[o.value, o.source, o.detail].filter(Boolean).join(' — ')}>
      <span className="text-deck-text-dim">{label}</span>
      <span className={tone}>{observationLabel(o.value)}</span>
    </span>
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
      setState(isFeatureOff(err) ? 'off' : 'error');
    } finally { setBusy(false); }
  }, []);
  useEffect(() => { load(); }, [load]);

  const byId = new Map<string, RoutingProfile>();
  for (const v of snap?.profiles || []) byId.set(v.profile.id, v.profile);
  for (const c of snap?.decider.candidates || []) if (!byId.has(c.profile.id)) byId.set(c.profile.id, c.profile);
  const displayNames: Record<string, string> = {};
  for (const a of snap?.adapters || []) for (const m of a.models || []) if (m.displayName) displayNames[m.id] = m.displayName;
  const name = (id: string) => profileName(byId.get(id), id, displayNames);

  return (
    <section className="min-w-0 rounded-xl border border-deck-border bg-deck-surface p-4 space-y-3 [overflow-wrap:anywhere]">
      <div className="flex items-center gap-2">
        <h2 className="text-sm font-semibold">모델 라우팅 · 실행기 상태</h2>
        {state === 'ok' && <button className="ml-auto shrink-0 text-xs underline" disabled={busy} onClick={() => load(true)}>{busy ? '확인 중…' : '다시 확인'}</button>}
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

          <div className="space-y-2">
            {snap.adapters.map((a) => (
              <div key={a.adapterId} className="rounded-lg border border-deck-border/60 p-2 text-[11px] space-y-1.5">
                <div className="flex items-baseline gap-2 text-xs">
                  <span className="font-medium">{adapterName(a.adapterId)}</span>
                  <span className="text-deck-text-dim">{a.version || '버전 미확인'}{a.models?.length ? ` · 모델 ${a.models.length}개` : ''}</span>
                </div>
                <div className="flex flex-wrap gap-1">
                  <Chip label="설치" o={a.installation} /><Chip label="로그인" o={a.auth} /><Chip label="과금" o={a.billing} /><Chip label="지원" o={a.technical} /><Chip label="상태" o={a.health} />
                </div>
                {a.inheritedEnv?.length ? <div className="text-amber-300">과금에 영향 줄 수 있는 환경변수: {a.inheritedEnv.join(', ')}</div> : null}
                {a.needsRevalidation && (
                  <div className="text-amber-300">
                    {a.validatedVersion ? `CLI가 업데이트됨 — 재확인 필요 (확인한 버전: ${a.validatedVersion})` : '아직 확인 기록 없음'}{' '}
                    <button className="underline" onClick={async () => { await api.routingMarkValidated(a.adapterId); load(); }}>확인함</button>
                  </div>
                )}
              </div>
            ))}
          </div>

          <div className="text-xs font-semibold pt-1">실행 프로필 (코드 작업 기준)</div>
          {(snap.profiles || []).length === 0 && <p className="text-xs text-deck-text-dim">설치된 실행기가 없거나 routing.json에 프로필이 없습니다. 존재하지 않는 모델을 만들어 등급을 채우지 않습니다.</p>}
          <div className="space-y-2">
            {(snap.profiles || []).map((v) => {
              const manual = v.manualExcluded || [];
              const warn = v.manualWarnings || [];
              const auto = v.automaticExcluded || [];
              return (
                <div key={v.profile.id} className="border-t border-deck-border pt-2 text-xs space-y-0.5">
                  <div className="flex flex-wrap items-baseline gap-x-2">
                    <span className="font-medium" title={v.profile.id}>{name(v.profile.id)}</span>
                    <span className="text-[11px] text-deck-text-dim">
                      {v.profile.tiers?.length ? `등급 ${v.profile.tiers.join(', ')}` : '등급 없음'}{v.profile.generated ? ' · 자동 생성(직접 선택만)' : ''}
                    </span>
                  </div>
                  <div>직접 선택: {manual.length ? <span className="text-red-400">불가 — {manual.map((e) => reasonLabel(e.reason)).join(' · ')}</span> : <span className="text-emerald-400">가능</span>}{warn.length > 0 && <span className="text-amber-300"> (주의: {warn.map((e) => reasonLabel(e.reason)).join(' · ')})</span>}</div>
                  <div>자동 선택: {auto.length ? <span className="text-amber-300">제외 — {auto.map((e) => reasonLabel(e.reason)).join(' · ')}</span> : <span className="text-emerald-400">가능</span>}</div>
                  <div className="text-deck-text-dim">이용 조건: 직접 사용 {observationLabel(v.policy.personal_interactive?.value ?? '')} · 자동 사용 {observationLabel(v.policy.personal_automatic?.value ?? '')} · 무인 실행 {observationLabel(v.policy.unattended?.value ?? '')}</div>
                </div>
              );
            })}
          </div>

          <div className="text-xs">
            난이도 판단: {snap.tierJudge
              ? <>로컬 모델 <b>{snap.tierJudge.endpointRef}</b>
                  {(() => { const up = snap.adapters.find((a) => a.adapterId === `local:${snap.tierJudge!.endpointRef}`)?.installation.value === 'installed';
                    return <span className={up ? 'text-emerald-400' : 'text-amber-300'}>{up ? ' · 연결됨' : ' · 응답 없음 (규칙으로 판단)'}</span>; })()}
                  <span className="text-deck-text-dim"> — 답이 없거나 {((snap.tierJudge.timeoutMs || 3000) / 1000).toFixed(0)}초 안에 못 하면 키워드 규칙으로 판단합니다.</span></>
              : <span className="text-deck-text-dim">키워드 규칙 (routing.json의 tierJudge로 로컬 모델 판단을 켤 수 있습니다)</span>}
          </div>

          <div className="text-xs font-semibold pt-1">작업 배분 판단 · 보조 역할</div>
          <p className="text-xs text-deck-text-dim">
            판단 방식: {STRATEGY_LABELS[snap.decider.strategy]}. 실행 가능한 후보가 둘 이상이고 규칙으로 정해지지 않을 때만 판단 모델을 부릅니다.
            {!snap.decider.callable && ' 판단 실행기가 연결되지 않았습니다.'}
          </p>
          <div className="text-xs">판단 모델: {(snap.decider.ordered || []).length ? (snap.decider.ordered || []).map((id) => {
            const st = snap.decider.stats[id];
            const verified = byId.get(id)?.quality?.decide?.status === 'verified';
            return `${name(id)}${st ? ` (${st.calls}회 호출, 보통 ${st.p50Ms}ms, 유효 ${st.valid}/${st.calls})` : ''}${verified ? '' : ' · 품질 미검증(기록만)'}`;
          }).join(' → ') : <span className="text-deck-text-dim">없음 — 규칙과 직접 선택으로 처리합니다</span>}</div>
          {Object.entries(snap.decider.roles).map(([role, byProvider]) => (
            <div key={role} className="text-xs space-y-1">
              <div className="font-medium">{role === 'reviewer' ? '리뷰어' : role === 'planner' ? '계획 담당' : role}</div>
              {Object.entries(byProvider).map(([provider, label]) => {
                const r = parseRoleLabel(label);
                return (
                  <div key={provider} className="pl-2">
                    <span className="text-deck-text-dim">{adapterName(provider)}로 작업할 때 → </span>
                    {r.ok ? (
                      <>
                        <span title={r.id}>{name(r.id)}</span>
                        <span className="text-deck-text-dim">{r.sameProvider ? ' · 같은 공급자(새 대화)' : ' · 다른 공급자'}</span>
                      </>
                    ) : (
                      <details className="inline">
                        <summary className="inline cursor-pointer text-amber-300">쓸 수 있는 모델 없음 — 수동 확인 필요</summary>
                        <ul className="mt-1 space-y-0.5 text-[11px] text-deck-text-dim">
                          {r.blocked.map((b) => <li key={b.id}><span title={b.id}>{name(b.id)}</span>: {b.reasons.map(reasonLabel).join(' · ')}</li>)}
                          {r.note && <li>{r.note}</li>}
                          {r.blocked.length === 0 && !r.note && <li>이 역할을 허용한 프로필이 없습니다.</li>}
                        </ul>
                      </details>
                    )}
                  </div>
                );
              })}
            </div>
          ))}
          <p className="text-[11px] text-deck-text-dim">이용 조건 상태는 {snap.policy.reviewedAt}에 공식 문서를 검토한 기록이며 법률 판단이 아닙니다. 사용자의 동의 체크로 바뀌지 않습니다.</p>
        </>
      )}
    </section>
  );
}
