import { useCallback, useEffect, useState } from 'react';
import { VoiceCodingPanel } from '../components/native/VoiceCodingPanel';
import { api, apiRequest } from '../lib/api';
import { useVoicePlayback } from '../hooks/useVoicePlayback';
import { answerVoiceStatus, type VoiceAgentStatus } from '../lib/voiceStatus';
import { Link, useParams } from 'react-router-dom';
import { useVoiceInput } from '../hooks/useVoiceInput';
import { VoiceIdentityPanel } from '../components/native/VoiceIdentityPanel';

type VoiceNote = { id: number; text: string; answer?: string; source?: string };

/**
 * Separate voice conversation surface. This first stage deliberately has NO
 * authority to call native:input, send shell commands or edit repository files.
 * Voice notes may be explicitly reviewed in the coding composer instead.
 */
export function VoiceModePage() {
  const { id = '' } = useParams<{ id: string }>();
  const voice = useVoiceInput('ko-KR');
  const playback = useVoicePlayback();
  const [agentStatus, setAgentStatus] = useState<VoiceAgentStatus | undefined>();
  const [loadingStatus, setLoadingStatus] = useState(false);

  useEffect(() => {
    let stopped = false;
    const refresh = async () => {
      try {
        const all = await api.controlSummaries() as VoiceAgentStatus[];
        if (!stopped) setAgentStatus(all.find((entry) => entry.agentId === id));
      } catch {
        if (!stopped) setAgentStatus(undefined);
      }
    };
    refresh();
    const timer = window.setInterval(refresh, 10000);
    return () => { stopped = true; clearInterval(timer); playback.stop(); };
  }, [id]);
  const [notes, setNotes] = useState<VoiceNote[]>([]);
  const [draft, setDraft] = useState('');
  const [notice, setNotice] = useState('');
  const [nextId, setNextId] = useState(1);
  const [codingText, setCodingText] = useState('');
  const onCodingSent = useCallback(() => { setCodingText(''); setDraft(''); voice.reset(); }, [voice.reset]);
  const onCodingAnnouncement = useCallback((text: string) => { playback.speak(text); }, [playback.speak]);

  const addNote = async () => {
    const text = draft.trim() || voice.finalText.trim();
    if (!text) return;
    playback.stop();
    setLoadingStatus(true);
    let snapshot = agentStatus;
    try {
      const summaries = await api.controlSummaries() as VoiceAgentStatus[];
      snapshot = summaries.find((entry) => entry.agentId === id);
      setAgentStatus(snapshot);
    } catch { /* Retain the last known snapshot, if any. */ }
    // A separate server-side dialogue engine is strictly read-only. If it is
    // disabled, unreachable or fails, fall back to truthful status responses.
    let answer = answerVoiceStatus(text, snapshot).text;
    let source = '작업 상태';
    try {
      const history = notes.slice(-4).flatMap((note) => [
        { role: 'user', content: note.text },
        ...(note.answer ? [{ role: 'assistant', content: note.answer }] : []),
      ]);
      const response = await apiRequest('/voice/dialogue', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ agentId: id, message: text, history }),
      });
      if (response.ok && (response.headers.get('content-type') || '').includes('application/json')) {
        const payload = await response.json() as { text?: string; source?: string };
        if (typeof payload.text === 'string' && payload.text.trim()) {
          answer = payload.text.trim();
          source = '로컬 AI · 읽기 전용';
        }
      }
    } catch {
      // The status-only path works without local model setup or subscription.
    }
    setNotes((previous) => [...previous, { id: nextId, text, answer, source }]);
    setLoadingStatus(false);
    playback.speak(answer);
    setNextId((n) => n + 1);
    setDraft('');
    voice.reset();
    setNotice('읽기 전용 상태 답변이야. 코딩 에이전트에는 명령을 전달하지 않았어.');
  };
  const stageCodingRequest = (text: string) => {
    // Transfer is just a reviewed draft, NEVER a send. The existing coding
    // composer and its own submit/approvals remain responsible for execution.
    sessionStorage.setItem(`pcd:voice-review:${id}`, text);
    setCodingText(text);
    setNotice('개발 지시 검토 칸에 문장을 옮겼어. 실행하려면 별도로 확인해 줘.');
  };

  return (
    <div className="flex flex-col h-full min-h-0 bg-deck-bg text-deck-text">
      <header className="flex items-center justify-between gap-2 border-b border-deck-border bg-deck-surface px-4 py-3">
        <div className="min-w-0">
          <h1 className="font-semibold text-base">음성 모드</h1>
          <p className="text-xs text-deck-text-dim">대화 공간 · 코드 변경 없음</p>
        </div>
        <Link to={`/agents/${id}`} className="shrink-0 rounded-lg border border-deck-border px-3 py-2 text-sm">코딩 화면</Link>
      </header>
      <section className="flex-1 min-h-0 overflow-y-auto p-4 space-y-3" aria-label="음성 대화 기록">
        <VoiceCodingPanel agentId={id} suggestedText={codingText || draft || voice.finalText} onSent={onCodingSent} onAnnouncement={onCodingAnnouncement} />
        <VoiceIdentityPanel />
        <p className="rounded-xl bg-deck-surface border border-deck-border px-4 py-3 text-sm">
          일반 음성 대화는 코드를 수정하지 않아. 개발 지시는 별도 확인 버튼을 누른 경우에만 코딩 세션으로 전달해.
          <span className="block mt-2 text-xs text-deck-text-dim">서버에 읽기 전용 로컬 AI가 설정되어 있으면 상태 질문에 답할 수 있어. 개발 지시에는 명시적인 확인과 기존 코딩 권한 승인이 적용돼.</span>
        </p>
        {notes.map((note) => (
          <article key={note.id} className="ml-auto max-w-[95%] rounded-2xl bg-deck-surface border border-deck-border p-3">
            <div className="text-xs text-deck-text-dim mb-1">나 · 질문</div>
            <p className="whitespace-pre-wrap break-words text-sm">{note.text}</p>
            {note.answer && <div className="mt-3 border-t border-deck-border pt-3 text-sm" aria-label="PowerCodeDeck 상태 응답"><span className="block text-xs text-deck-text-dim mb-1">PowerCodeDeck · {note.source || '상태 안내'}</span>{note.answer}</div>}
            <button type="button" onClick={() => stageCodingRequest(note.text)}
              className="mt-3 rounded-lg border border-deck-border px-3 py-2 text-xs">
              개발 지시 검토하기 (실행 안 함)
            </button>
          </article>
        ))}
      </section>
      <div className="border-t border-deck-border bg-deck-surface p-4 space-y-3">
        {notice && <p role="status" className="text-xs text-deck-text-dim">{notice}</p>}
        {voice.error && <p role="alert" className="text-xs text-red-400">{voice.error}</p>}
        {(voice.finalText || voice.interimText) && <p role="status" className="text-sm break-words">
          인식: {voice.finalText} <span className="text-deck-text-dim">{voice.interimText}</span>
        </p>}
        <textarea aria-label="음성 대화 메모" rows={2} value={draft} onChange={(e) => setDraft(e.target.value)}
          placeholder="말하거나 텍스트를 입력해 질문하거나 개발 지시를 검토할 수 있어."
          className="w-full resize-none rounded-lg border border-deck-border bg-deck-bg p-3 text-base" />
        <div className="flex flex-wrap items-center gap-3">
          {playback.supported && <button type="button" aria-pressed={playback.enabled} onClick={playback.speaking ? playback.stop : playback.toggle} className="min-h-12 px-3 rounded-lg border border-deck-border text-sm">{playback.speaking ? '읽기 중지' : playback.enabled ? '🔊 음성 응답 켜짐' : '🔇 음성 응답 꺼짐'}</button>}
          <button type="button" disabled={!voice.supported}
            onClick={() => { playback.stop(); voice.state === 'listening' ? voice.stop() : voice.start(); }}
            className="min-h-12 min-w-12 rounded-full border border-deck-border bg-deck-bg disabled:opacity-40"
            aria-label={voice.state === 'listening' ? '녹음 중지' : '마이크 시작'}>
            {voice.state === 'listening' ? '■' : '🎙'}
          </button>
          <span className="text-xs flex-1 text-deck-text-dim">
            {voice.state === 'listening' ? '듣는 중…' : voice.supported ? '마이크를 누르고 말해 줘' : '브라우저 음성 인식 미지원 · 글 입력 가능'}
          </span>
          <button type="button" onClick={addNote} disabled={loadingStatus || (!draft.trim() && !voice.finalText.trim())}
            className="rounded-lg border border-deck-border px-3 py-3 text-sm disabled:opacity-40">
            {loadingStatus ? '상태 확인 중…' : '상태 질문'}
          </button>
        </div>
      </div>
    </div>
  );
}
