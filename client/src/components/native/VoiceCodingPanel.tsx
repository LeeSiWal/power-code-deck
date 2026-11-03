import { useEffect, useRef, useState } from 'react';
import { api } from '../../lib/api';
import { useVoiceCodingSession } from '../../hooks/useVoiceCodingSession';

type Provider = 'claude' | 'codex';
type Props = {
  agentId: string;
  suggestedText: string;
  onSent: () => void;
  onAnnouncement: (text: string) => void;
};

function asProvider(agent: { preset?: string; command?: string }): Provider | null {
  if (agent.preset === 'codex-cli' || agent.command === 'codex') return 'codex';
  if (agent.preset === 'claude-code' || agent.command === 'claude') return 'claude';
  return null;
}

/**
 * Deliberate separation between casual voice dialogue and coding commands.
 * No spoken phrase can automatically call native:input: the user must first
 * review the transcript, then explicitly approve the coding task in this UI.
 */
export function VoiceCodingPanel({ agentId, suggestedText, onSent, onAnnouncement }: Props) {
  const [provider, setProvider] = useState<Provider | null>(null);
  const [checking, setChecking] = useState(true);
  const [candidate, setCandidate] = useState('');
  const [awaitingConfirmation, setAwaitingConfirmation] = useState(false);
  const [feedback, setFeedback] = useState('');
  const [sentPending, setSentPending] = useState(false);
  const [revisionMode, setRevisionMode] = useState(false);
  const [revisionText, setRevisionText] = useState('');
  const onAnnouncementRef = useRef(onAnnouncement);
  useEffect(() => { onAnnouncementRef.current = onAnnouncement; }, [onAnnouncement]);
  const session = useVoiceCodingSession(agentId, provider !== null);

  useEffect(() => {
    let active = true;
    setChecking(true);
    api.getAgent(agentId).then((agent) => {
      if (active) setProvider(asProvider(agent));
    }).catch(() => {
      if (active) setProvider(null);
    }).finally(() => {
      if (active) setChecking(false);
    });
    return () => { active = false; };
  }, [agentId]);

  useEffect(() => {
    session.configureProvider(provider);
  }, [provider, session.configureProvider]);

  useEffect(() => {
    if (!session.lastAnnouncement) return;
    setSentPending(false);
    setFeedback(session.lastAnnouncement);
    onAnnouncementRef.current(session.lastAnnouncement);
  }, [session.lastAnnouncement]);

  const revise = () => {
    if (!revisionText.trim()) return;
    setCandidate(`이전 작업의 방향을 다음과 같이 변경해 줘. 기존 작업 내용을 확인하고, 무리하게 중복 적용하지 말고 변경 사항을 검토한 뒤 진행해: ${revisionText.trim()}`);
    setAwaitingConfirmation(true);
    setRevisionMode(false);
    setFeedback('수정 지시를 검토해 줘. 현재 작업이 완전히 멈춘 뒤에만 실행할 수 있어.');
  };
  const prepare = () => {
    const text = suggestedText.trim();
    if (!text) return;
    setCandidate(text);
    setAwaitingConfirmation(true);
    setFeedback('');
  };
  const confirm = () => {
    if (!awaitingConfirmation || !candidate.trim()) return;
    if (!session.sendTask(candidate)) {
      setFeedback('현재 작업 중이거나 연결되지 않았어. 기존 작업 완료와 승인 대기 상태를 확인해 줘.');
      return;
    }
    setAwaitingConfirmation(false);
    setCandidate('');
    setSentPending(true);
    setFeedback('전송을 요청했어. 에이전트의 실제 수신 및 실행 상태를 확인해 줘.');
    onSent();
  };

  const name = provider === 'codex' ? 'Codex' : 'Claude Code';
  return (
    <section className="rounded-xl border border-deck-border bg-deck-surface p-3 space-y-3" aria-label="음성 개발 작업 연결">
      <div>
        <h2 className="font-semibold text-sm">개발 지시 · 실행 전 확인</h2>
        <p className="text-xs text-deck-text-dim mt-1">
          일반 음성 질문은 코드에 영향을 주지 않아. 아래에서 확인한 개발 지시만 현재 코딩 세션으로 전달해.
        </p>
      </div>
      {checking ? <p className="text-xs">세션 확인 중…</p> : provider ? (
        <p className="text-xs" role="status">
          {name} · {session.connected ? '연결됨' : '연결 중'}{session.busy ? ' · 작업 중' : ''}
        </p>
      ) : (
        <p className="text-xs" role="status">이 세션은 직접 제어할 수 있는 Claude Code 또는 Codex 세션이 아니야. 코딩 화면에서 해당 에이전트를 선택해 줘.</p>
      )}
      {provider && !awaitingConfirmation && (
        <button type="button" onClick={prepare} disabled={!session.connected || session.busy || session.submitting || !suggestedText.trim()}
          className="min-h-11 rounded-lg border border-deck-border px-3 text-sm disabled:opacity-40">
          현재 문장을 개발 지시로 검토하기
        </button>
      )}
      {awaitingConfirmation && (
        <div className="space-y-2 border-t border-deck-border pt-3">
          <label className="block text-xs text-deck-text-dim" htmlFor="voice-task-review">실행할 개발 지시를 확인하고 수정해 줘</label>
          <textarea id="voice-task-review" value={candidate} onChange={(e) => setCandidate(e.target.value)}
            rows={3} className="w-full rounded-lg border border-deck-border bg-deck-bg p-3 text-base resize-y" />
          <p className="text-xs text-deck-text-dim">다음 단계에서 {name}가 실제 코드를 수정할 수 있어. 위험한 도구 실행에는 기존 권한 승인이 필요해.</p>
          <div className="flex flex-wrap gap-2">
            <button type="button" onClick={confirm} disabled={!session.connected || session.busy || session.submitting || !candidate.trim() || session.pendingApprovals.length > 0}
              className="min-h-11 rounded-lg border border-deck-accent px-3 text-sm disabled:opacity-40">확인하고 개발 지시 실행</button>
            <button type="button" onClick={() => setAwaitingConfirmation(false)}
              className="min-h-11 rounded-lg border border-deck-border px-3 text-sm">취소</button>
          </div>
        </div>
      )}
      <p className="text-xs text-deck-text-dim" role="status">{session.progress || '작업 대기 중'}</p>
      {session.lastResult && <p className="text-xs" role="status">마지막 작업 결과 · 도구 {session.lastResult.tools}건 · 오류 {session.lastResult.failed}건 · 승인 거부 {session.lastResult.denied}건{session.lastResult.error ? ' · 작업 오류' : ''}</p>}
      {session.pendingApprovals.length > 0 && (
        <p className="text-sm" role="alert">
          도구 승인 요청 {session.pendingApprovals.length}건이 있어.
          <a href={`/agents/${encodeURIComponent(agentId)}`} className="underline ml-1">코딩 화면에서 확인</a>
        </p>
      )}
      {sentPending && session.submitting && <p role="status" className="text-xs">에이전트의 작업 수신을 확인 중이야.</p>}
      {sentPending && session.busy && <p role="status" className="text-xs">에이전트가 작업을 수행 중이야.</p>}
      {session.busy && <button type="button" onClick={() => { session.interrupt(); setRevisionMode(true); setFeedback('작업 중단을 요청했어. 완료 여부는 작업 상태를 확인해 줘.'); }}
        className="min-h-11 rounded-lg border border-deck-border px-3 text-sm">작업 중단 요청 · 방향 변경</button>}
      {provider && <button type="button" onClick={() => setRevisionMode((v) => !v)}
        className="min-h-11 rounded-lg border border-deck-border px-3 text-sm">{revisionMode ? '방향 변경 취소' : '새 방향 지시 준비'}</button>}
      {revisionMode && <div className="space-y-2">
        <label htmlFor="voice-revision" className="text-xs block">바꿀 방향을 설명해 줘</label>
        <textarea id="voice-revision" value={revisionText} onChange={(e) => setRevisionText(e.target.value)} rows={2}
          className="w-full rounded-lg border border-deck-border bg-deck-bg p-3 text-base" />
        <button type="button" onClick={revise} disabled={!revisionText.trim()} className="min-h-11 rounded-lg border border-deck-border px-3 text-sm disabled:opacity-40">방향 변경 지시 검토</button>
      </div>}
      {session.error && <p role="alert" className="text-xs text-red-400">{session.error}</p>}
      {feedback && <p role="status" className="text-sm">{feedback}</p>}
    </section>
  );
}
