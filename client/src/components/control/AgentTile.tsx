import type { AgentSummary } from '../../stores/appStore';
import { liveState, STATE_CHIP, attnClasses, attnLabel, kindGlyph, timeAgo } from './liveState';
import { LiveDot, WorkingBar, Badge, ActBtn } from './LiveDot';
import { AgentMeta } from '../sidebar/AgentMeta';

export function AgentTile({
  s,
  onOpen,
  onRestart,
  onStop,
  onDestroy,
}: {
  s: AgentSummary;
  onOpen: (id: string) => void;
  onRestart: (id: string) => void;
  onStop: (s: AgentSummary) => void;
  onDestroy: (s: AgentSummary) => void;
}) {

  const attn = s.attention?.primary;
  const st = liveState(s);
  const chip = STATE_CHIP[st];
  const borderCls = attn
    ? 'border-2 ' + attnClasses(attn).split(' ')[0]
    : st === 'working'
      ? 'border-deck-accent/40'
      : st === 'stopped'
        ? 'border-deck-border-soft'
        : 'border-deck-border';
  return (
    <div
      className={`rounded-lg border bg-deck-surface p-3 transition-all ${borderCls} ${
        st === 'working' ? 'shadow-[0_0_0_1px_rgba(99,102,241,0.15)]' : ''
      } ${st === 'stopped' ? 'opacity-60' : ''}`}
    >
      <div className="flex items-center justify-between gap-2">
        <div className="flex items-center gap-1.5 min-w-0">
          <LiveDot hue={s.colorHue} state={st} />
          <span className={`font-mono text-xs font-semibold truncate ${st === 'stopped' ? 'text-deck-text-dim' : ''}`}>
            {s.name}
          </span>
          <span className="text-[9px] uppercase tracking-wide px-1 rounded border border-deck-border text-deck-text-faint">
            {kindGlyph(s.preset)}
          </span>
        </div>
        {attn ? (
          <span className={`text-[9px] font-mono px-1.5 rounded-full border ${attnClasses(attn)}`}>
            {attnLabel(s.attention.reasons[0] || { kind: attn })}
          </span>
        ) : (
          <span className={`text-[9px] font-mono px-1.5 rounded-full border whitespace-nowrap ${chip.cls}`}>
            {chip.label}
          </span>
        )}
      </div>
      {st === 'working' ? (
        <WorkingBar />
      ) : (
        <div className="h-0.5 mt-2" /> // reserve the space so tiles don't jump when the bar toggles
      )}
      <div
        className={`font-mono text-[10px] mt-2 leading-relaxed ${
          st === 'stopped' ? 'text-deck-text-faint' : 'text-deck-text-dim'
        }`}
      >
        <div className="truncate">
          tool&nbsp;&nbsp;: <span className={st === 'working' ? 'text-deck-accent-light' : ''}>{s.lastTool || '—'}</span>
        </div>
        <div className="truncate">target: {s.lastTarget || '—'}</div>
        <div>
          ×{s.toolCount} · {timeAgo(s.lastActivityAt)}
        </div>
        {!!s.todoTotal && (
          <div className="truncate text-deck-accent-light">
            ☑ {s.todoCompleted ?? 0}/{s.todoTotal}{s.todoActive ? ` · ${s.todoActive}` : ''}
          </div>
        )}
      </div>
        <AgentMeta agentId={s.agentId} />
        {/* SubAgentBar 없음: activity 스냅샷은 agent:activity WS 이벤트로만 채워지고,
            서버는 BroadcastToAgent(watchingAgent 필터)로만 송출한다. 관제실은 어떤
            세션도 watch하지 않으므로 스냅샷이 항상 undefined — 렌더해도 null.
            타일 수준의 서브에이전트 표시는 세션 단위가 아닌 요약 레벨 데이터를
            서버가 제공할 때 재도입 가능하다. */}
      <div className="flex gap-1.5 mt-2">
        <Badge>✓ 완료 {s.unread?.completed ?? 0}</Badge>
        <Badge>⚠ 에러 {s.unread?.errors ?? 0}</Badge>
      </div>
      <div className="flex flex-wrap gap-1.5 mt-2 pt-2 border-t border-deck-border-soft">
        <ActBtn onClick={() => onOpen(s.agentId)}>열기</ActBtn>
        <ActBtn onClick={() => onRestart(s.agentId)}>재시작</ActBtn>
        {/* 되돌릴 수 있는 정지는 실행 중일 때만, 되돌릴 수 없는 삭제는 이미 멈춘
            세션에만 노출한다 — 밀도 높은 벽에서 오클릭으로 살아있는 세션을
            날리는 일이 없도록. */}
        {st === 'stopped' ? (
          <ActBtn danger onClick={() => onDestroy(s)}>삭제</ActBtn>
        ) : (
          <ActBtn onClick={() => onStop(s)}>정지</ActBtn>
        )}
      </div>
    </div>
  );
}
