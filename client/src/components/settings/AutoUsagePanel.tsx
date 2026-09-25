import { useEffect, useState } from 'react';
import { api, type AutoUsage } from '../../lib/api';
import { cacheShare, compactTokens, modelUsageLine } from '../../lib/routingLabels';

/**
 * What "자동" sessions actually used over the last 7 days, from the usage each
 * CLI reported per turn. No savings estimate: what a turn would have cost on
 * another model is not observable. The idle table checks the assumption behind
 * the idle threshold — past ~5 minutes the prompt cache should stop being hit.
 */
export function AutoUsagePanel() {
  const [usage, setUsage] = useState<AutoUsage | null>(null);
  const [state, setState] = useState<'loading' | 'ok' | 'error'>('loading');
  useEffect(() => {
    api.autoUsageRecent(7).then((u) => { setUsage(u); setState('ok'); }).catch(() => setState('error'));
  }, []);

  return (
    <section className="min-w-0 rounded-xl border border-deck-border bg-deck-surface p-4 space-y-3 [overflow-wrap:anywhere]">
      <h2 className="text-sm font-semibold">자동 세션 사용량 · 최근 7일</h2>
      {state === 'loading' && <p className="text-xs text-deck-text-dim">불러오는 중…</p>}
      {state === 'error' && <p className="text-xs text-red-400">사용량을 불러오지 못했습니다.</p>}
      {state === 'ok' && usage && usage.turns === 0 && (
        <p className="text-xs text-deck-text-dim">아직 기록된 자동 세션 턴이 없습니다.</p>
      )}
      {state === 'ok' && usage && usage.turns > 0 && (
        <>
          <div className="text-xs space-y-1">
            {usage.models.map((m) => <div key={m.tool + m.model + m.effort}>{modelUsageLine(m)}</div>)}
          </div>
          <p className="text-xs text-deck-text-dim">
            전체 {usage.turns}턴 · 모델 전환 {usage.modelSwitches}회 · 도구 전환 {usage.toolSwitches}회 · 조언 {usage.delegations}회 · 메모로 새로 시작 {usage.freshStarts ?? 0}회
            {usage.handoffTokens ? ` · 인계·요청서 약 ${compactTokens(usage.handoffTokens)}토큰(글자 수 기준 추정)` : ''}
          </p>
          <div className="text-xs">
            <div className="font-medium mb-1">쉰 시간별 캐시 읽기 비율</div>
            <div className="grid grid-cols-[auto_auto_1fr] gap-x-4 gap-y-0.5 text-deck-text-dim">
              <span>쉰 시간</span><span>턴</span><span>입력 중 캐시에서 읽은 비율</span>
              {usage.idle.map((b) => (
                <FragmentRow key={b.label} label={b.label} turns={b.turns} reported={b.reported} share={cacheShare(b.cacheRead, b.inputTotal)} />
              ))}
            </div>
            <p className="mt-1.5 text-[11px] text-deck-text-dim">
              지금 기준: {usage.idleThresholdSeconds < 60 ? `${usage.idleThresholdSeconds}초` : `${Math.round(usage.idleThresholdSeconds / 60)}분`} 이상 쉬면 더 가벼운 모델이나 다른 도구로 옮깁니다.
              기준 이후 구간에서도 캐시 비율이 높게 나오면 기준을 늘리는 편이 토큰을 아낍니다(PCD_AUTO_IDLE_SECONDS).
            </p>
          </div>
        </>
      )}
    </section>
  );
}

function FragmentRow({ label, turns, reported, share }: { label: string; turns: number; reported: number; share: string }) {
  return (
    <>
      <span className="text-deck-text">{label}</span>
      <span>{turns}{turns > reported ? ` (${turns - reported} 미보고)` : ''}</span>
      <span>{share}</span>
    </>
  );
}
