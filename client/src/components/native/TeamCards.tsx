import { useEffect, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { api, type TeamMode } from '../../lib/api';
import type { ChatItem } from '../../lib/nativeEvents';
import { writeClipboard } from '../../lib/clipboard';
import {
  agyPasteMessage, elapsedLabel, parseTeamResult, pickAgyTerminal, teamStatusLabel, teamTokensLabel, type TeamToolCall,
} from '../../lib/teamTools';
import { IconCopy, IconSpinner } from '../icons';

/**
 * Dream team cards: what the conductor's ask_member / handoff_to_agy calls look
 * like in the chat. Rendered in place of the generic tool row (NativeChat ChatRow).
 */

type ToolItem = Extract<ChatItem, { kind: 'tool' }>;

// Labels for the running card, before the result (which carries its own label) arrives.
const MEMBER_LABELS: Record<string, string> = {
  codex: 'Codex',
  'claude-sonnet': 'Claude Sonnet',
  'claude-haiku': 'Claude Haiku',
  'claude-fable': 'Claude Fable',
};

const str = (v: unknown) => (typeof v === 'string' ? v : '');

export function TeamToolCard({ item, call, agentId, cwd, onSend }: {
  item: ToolItem;
  call: TeamToolCall;
  agentId: string;
  cwd: string;
  onSend: (text: string) => void;
}) {
  return call.kind === 'ask'
    ? <MemberCard item={item} args={call.args} agentId={agentId} />
    : <AgyCard item={item} args={call.args} cwd={cwd} onSend={onSend} />;
}

function MemberCard({ item, args, agentId }: { item: ToolItem; args: Record<string, unknown>; agentId: string }) {
  const res = item.status === 'pending' ? null : parseTeamResult(item.result);
  const label = res?.label || MEMBER_LABELS[str(args.member)] || str(args.member) || '팀원';
  const [full, setFull] = useState<string | null>(null);
  const [fullState, setFullState] = useState<'idle' | 'loading' | 'error'>('idle');
  const [raise, setRaise] = useState<'idle' | 'busy' | 'done' | 'error'>('idle');

  const loadFull = async () => {
    if (!res?.callId) return;
    setFullState('loading');
    try {
      setFull((await api.teamResult(agentId, res.callId)).text);
      setFullState('idle');
    } catch {
      setFullState('error');
    }
  };
  const raiseCap = async () => {
    setRaise('busy');
    try { await api.raiseTeamCap(agentId); setRaise('done'); } catch { setRaise('error'); }
  };

  return (
    <div data-testid="team-member-card" className="border border-deck-accent/30 bg-deck-accent/5 rounded-lg px-3 py-2 space-y-1.5">
      <div className="flex items-center gap-2 text-xs min-w-0">
        <span aria-hidden>👥</span>
        <span className="font-medium text-deck-text shrink-0">{label}</span>
        {item.status === 'pending' && (
          <>
            <IconSpinner size={12} className="animate-spin text-deck-accent shrink-0" />
            <span className="text-deck-muted truncate">{str(args.task) || '작업 중…'}</span>
          </>
        )}
        {res && res.status === 'done' && (
          <span className="text-deck-muted truncate">
            {res.elapsedMs !== undefined && `${elapsedLabel(res.elapsedMs)} · `}토큰 {teamTokensLabel(res.tokens)}
          </span>
        )}
        {res && res.status !== 'done' && (
          <span className="text-deck-muted shrink-0">{teamStatusLabel(res.status)}</span>
        )}
      </div>

      {res?.status === 'done' && (
        <div className="text-[13px] text-deck-text whitespace-pre-wrap break-words max-h-72 overflow-y-auto">
          {full ?? res.text}
        </div>
      )}
      {res?.status === 'done' && res.truncated && full === null && (
        <button
          onClick={loadFull}
          disabled={fullState === 'loading'}
          className="text-xs text-deck-accent-light underline disabled:opacity-50"
        >
          {fullState === 'loading' ? '불러오는 중…' : '전체 보기'}
        </button>
      )}
      {fullState === 'error' && <div className="text-[11px] text-deck-muted">원문을 불러오지 못했습니다 (호출 뒤 1시간만 보관됩니다).</div>}

      {res && res.status !== 'done' && res.reason && (
        <div className="text-[11px] text-deck-muted break-words">{res.reason}</div>
      )}
      {res?.status === 'limit' && (
        <button
          onClick={raiseCap}
          disabled={raise === 'busy' || raise === 'done'}
          className="h-7 px-2.5 rounded-full border border-amber-400/45 text-amber-300 text-xs disabled:opacity-60"
        >
          {raise === 'done' ? '상한을 올렸습니다' : raise === 'busy' ? '올리는 중…' : '이번 세션 상한 올리기'}
        </button>
      )}
      {raise === 'error' && <div className="text-[11px] text-red-400">상한을 올리지 못했습니다.</div>}

      {/* Not a TeamResult (an error string, a changed wire format): show it as is. */}
      {!res && item.status !== 'pending' && item.result && (
        <pre className="text-[11px] text-deck-muted whitespace-pre-wrap break-words max-h-48 overflow-y-auto">{item.result}</pre>
      )}
    </div>
  );
}

function AgyCard({ item, args, cwd, onSend }: { item: ToolItem; args: Record<string, unknown>; cwd: string; onSend: (text: string) => void }) {
  const navigate = useNavigate();
  const task = str(args.task);
  const res = item.status === 'pending' ? null : parseTeamResult(item.result);
  const [note, setNote] = useState('');
  const [opening, setOpening] = useState(false);
  const [pasting, setPasting] = useState(false);
  const [paste, setPaste] = useState('');
  const [sent, setSent] = useState(false);

  // Copy first (inside the tap, before any await spends the gesture), then open
  // the project's agy terminal — reusing one if it exists. Never the native
  // Antigravity chat: a preset "custom" session running `agy` renders as a terminal.
  const copyAndOpen = async () => {
    const copied = await writeClipboard(task);
    setOpening(true);
    try {
      const agents = await api.listAgents();
      let target = pickAgyTerminal(agents as { id: string; preset?: string; command?: string; workingDir?: string; status?: string }[], cwd);
      if (!target) {
        const dir = cwd.split('/').filter(Boolean).pop() || cwd;
        target = await api.createAgent({ preset: 'custom', name: `Antigravity 터미널 - ${dir}`, workingDir: cwd, command: 'agy', args: [] }) as { id: string };
      }
      navigate(`/agents/${target.id}`);
    } catch {
      setNote(copied ? '복사했습니다. agy 탭을 열어 붙여 넣으세요.' : 'agy 탭을 열어 붙여 넣으세요.');
    } finally {
      setOpening(false);
    }
  };

  if (res?.status === 'refused') {
    return (
      <div data-testid="team-agy-card" className="border border-deck-border rounded-lg px-3 py-2 text-xs space-y-1">
        <div className="font-medium text-deck-text">agy에게 맡길 일 · {teamStatusLabel(res.status)}</div>
        {res.reason && <div className="text-[11px] text-deck-muted">{res.reason}</div>}
      </div>
    );
  }

  return (
    <div data-testid="team-agy-card" className="border border-sky-400/35 bg-sky-400/5 rounded-lg px-3 py-2 space-y-2">
      <div className="text-xs font-medium text-deck-text">agy에게 맡길 일</div>
      <div className="text-[13px] text-deck-text whitespace-pre-wrap break-words bg-deck-surface rounded p-2 max-h-56 overflow-y-auto">{task || '(요청문 없음)'}</div>
      <div className="flex flex-wrap gap-2">
        <button
          onClick={copyAndOpen}
          disabled={!task || opening}
          className="h-8 px-3 rounded-lg bg-deck-surface border border-deck-border text-xs text-deck-text flex items-center gap-1.5 disabled:opacity-50"
        >
          {opening ? <IconSpinner size={12} className="animate-spin" /> : <IconCopy size={12} />}
          📋 복사하고 agy 탭 열기
        </button>
        <button
          onClick={() => setPasting((v) => !v)}
          disabled={sent}
          className="h-8 px-3 rounded-lg bg-deck-surface border border-deck-border text-xs text-deck-text disabled:opacity-50"
        >
          {sent ? '결과 전달함' : '결과 붙여넣기 ▸'}
        </button>
      </div>
      {note && <div className="text-[11px] text-amber-300">{note}</div>}
      {pasting && !sent && (
        <div className="space-y-1.5">
          <textarea
            value={paste}
            onChange={(e) => setPaste(e.target.value)}
            rows={4}
            placeholder="agy의 답을 여기에 붙여 넣으세요"
            className="w-full resize-y bg-deck-surface border border-deck-border rounded-lg px-2 py-1.5 text-[13px] text-deck-text outline-none focus:border-deck-accent"
          />
          <button
            onClick={() => { onSend(agyPasteMessage(paste)); setSent(true); setPasting(false); }}
            disabled={!paste.trim()}
            className="h-8 px-3 rounded-lg bg-deck-accent text-white text-xs font-medium disabled:opacity-40"
          >
            지휘자에게 보내기
          </button>
        </div>
      )}
    </div>
  );
}

/**
 * The composer's 👥 드림팀 pill. Optimistic: flips at once, rolls back if the
 * server refuses. Never restarts the session; when the running CLI predates the
 * team tools (ready:false) it says the change lands on the next restart.
 */
export function TeamToggle({ agentId, onError }: { agentId: string; onError: (msg: string) => void }) {
  const [mode, setMode] = useState<TeamMode | null>(null);
  useEffect(() => {
    let live = true;
    setMode(null);
    api.getTeam(agentId).then((m) => { if (live) setMode(m); }).catch(() => {});
    return () => { live = false; };
  }, [agentId]);
  if (!mode) return null;

  const flip = async () => {
    const prev = mode;
    const next = { ...mode, on: !mode.on };
    setMode(next);
    try {
      await api.setTeam(agentId, next.on);
      api.getTeam(agentId).then(setMode).catch(() => {});
    } catch (err) {
      setMode(prev);
      onError('드림팀 모드를 바꾸지 못했습니다: ' + String(err));
    }
  };

  const hint = mode.on && !mode.ready;
  return (
    <button
      onClick={flip}
      data-testid="team-toggle"
      aria-pressed={mode.on}
      className={`shrink-0 h-8 px-2.5 rounded-full border text-xs flex items-center gap-1.5 whitespace-nowrap ${
        mode.on ? 'border-emerald-400/45 bg-emerald-400/10 text-emerald-300' : 'border-deck-border bg-deck-surface text-deck-text-dim'
      }`}
      title={hint ? '다음 세션 재시작부터 적용' : mode.on ? '드림팀 켜짐 — 다른 CLI에 일부 작업을 맡길 수 있습니다' : '드림팀 꺼짐'}
    >
      👥 드림팀
      {hint && <span className="text-[10px] opacity-80">· 다음 세션 재시작부터 적용</span>}
    </button>
  );
}
