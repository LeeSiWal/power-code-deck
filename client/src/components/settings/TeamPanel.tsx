import { useEffect, useState } from 'react';
import { api, type RoutingAdapterStatus, type TeamSettings } from '../../lib/api';
import { observationLabel } from '../../lib/routingLabels';

const LIMITS: { key: 'resultMaxChars' | 'maxCallsPerTurn' | 'sessionTokenCap' | 'timeoutSec'; label: string; hint: string }[] = [
  { key: 'resultMaxChars', label: '결과 최대 글자 수', hint: '넘으면 잘라서 지휘자에게 보냅니다' },
  { key: 'maxCallsPerTurn', label: '턴당 최대 호출', hint: '한 번의 답변에서 팀원을 부르는 횟수' },
  { key: 'sessionTokenCap', label: '세션 토큰 상한', hint: 'Claude 팀원의 입력+출력+캐시 생성 합계' },
  { key: 'timeoutSec', label: '호출 시간 제한(초)', hint: '넘으면 팀원을 멈춥니다' },
];

const GOOD = ['installed', 'authenticated'];

/**
 * Settings → 드림팀. Which members a conductor may call and the limits. The
 * member's CLI/model is fixed by the server; only the switch is the user's.
 * Availability comes from the routing snapshot the deck already keeps — nothing
 * here runs a member.
 */
export function TeamPanel() {
  const [saved, setSaved] = useState<TeamSettings | null>(null);
  const [draft, setDraft] = useState<TeamSettings | null>(null);
  const [state, setState] = useState<'loading' | 'ok' | 'error'>('loading');
  const [saving, setSaving] = useState<'idle' | 'busy' | 'done' | 'error'>('idle');
  const [adapters, setAdapters] = useState<RoutingAdapterStatus[] | null>(null);

  useEffect(() => {
    api.teamSettings().then((s) => { setSaved(s); setDraft(s); setState('ok'); }).catch(() => setState('error'));
    api.routingSnapshot().then((s) => setAdapters(s.adapters ?? [])).catch(() => setAdapters(null));
  }, []);

  const dirty = !!draft && JSON.stringify(draft) !== JSON.stringify(saved);
  const save = async () => {
    if (!draft) return;
    setSaving('busy');
    try {
      const s = await api.saveTeamSettings(draft);
      setSaved(s); setDraft(s); setSaving('done');
    } catch {
      setSaving('error');
    }
  };

  return (
    <section data-testid="team-panel" className="min-w-0 rounded-xl border border-deck-border bg-deck-surface p-4 space-y-3 [overflow-wrap:anywhere]">
      <h2 className="text-sm font-semibold">👥 드림팀</h2>
      <p className="text-xs text-deck-text-dim">
        채팅 입력창의 드림팀을 켠 세션은 아래 팀원에게 일부 작업을 맡길 수 있습니다. 팀원은 읽기 전용으로 실행됩니다.
      </p>
      {state === 'loading' && <p className="text-xs text-deck-text-dim">불러오는 중…</p>}
      {state === 'error' && <p className="text-xs text-red-400">드림팀 설정을 불러오지 못했습니다.</p>}
      {draft && (
        <>
          <div className="space-y-2">
            {draft.members.map((m, i) => {
              const a = adapters?.find((x) => x.adapterId === m.adapter);
              return (
                <div key={m.id} className="rounded-lg border border-deck-border/60 p-2 text-xs space-y-1">
                  <label className="flex items-center gap-2">
                    <input
                      type="checkbox"
                      role="switch"
                      checked={m.enabled}
                      onChange={(e) => setDraft({ ...draft, members: draft.members.map((x, j) => (j === i ? { ...x, enabled: e.target.checked } : x)) })}
                    />
                    <span className="font-medium">{m.label}</span>
                    <span className="text-deck-text-dim truncate">{m.model || '계정 기본 모델'}</span>
                  </label>
                  <div className="flex flex-wrap gap-1 text-[11px]">
                    {(['installation', 'auth'] as const).map((k) => {
                      const v = a?.[k]?.value ?? '';
                      return (
                        <span key={k} className="inline-flex gap-1 rounded bg-deck-bg px-1.5 py-0.5">
                          <span className="text-deck-text-dim">{k === 'installation' ? '설치' : '로그인'}</span>
                          <span className={GOOD.includes(v) ? 'text-emerald-400' : 'text-amber-300'}>{observationLabel(v)}</span>
                        </span>
                      );
                    })}
                  </div>
                  {m.model.startsWith('claude-fable') && (
                    <div className="text-[11px] text-amber-300">헤드리스 실행 시 사용량 크레딧이 청구됩니다</div>
                  )}
                </div>
              );
            })}
          </div>
          <div className="grid grid-cols-1 sm:grid-cols-2 gap-2">
            {LIMITS.map((l) => (
              <label key={l.key} className="text-xs space-y-0.5">
                <span className="block font-medium">{l.label}</span>
                <input
                  type="number"
                  min={1}
                  inputMode="numeric"
                  value={draft[l.key]}
                  onChange={(e) => setDraft({ ...draft, [l.key]: Math.max(0, Math.floor(Number(e.target.value) || 0)) })}
                  className="w-full bg-deck-bg border border-deck-border rounded px-2 py-1 text-xs outline-none focus:border-deck-accent"
                />
                <span className="block text-[11px] text-deck-text-dim">{l.hint}</span>
              </label>
            ))}
          </div>
          <div className="flex items-center gap-2">
            <button
              onClick={save}
              disabled={!dirty || saving === 'busy'}
              className="h-8 px-3 rounded-lg bg-deck-accent text-white text-xs font-medium disabled:opacity-40"
            >
              {saving === 'busy' ? '저장 중…' : '저장'}
            </button>
            {saving === 'done' && !dirty && <span className="text-xs text-emerald-400">저장했습니다</span>}
            {saving === 'error' && <span className="text-xs text-red-400">저장하지 못했습니다</span>}
            <span className="text-[11px] text-deck-text-dim">0 이하는 기본값으로 돌아갑니다</span>
          </div>
        </>
      )}
    </section>
  );
}
