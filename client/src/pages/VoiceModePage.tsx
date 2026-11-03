import { useCallback, useEffect, useRef, useState } from 'react';
import { VoiceCodingPanel } from '../components/native/VoiceCodingPanel';
import { api, apiRequest } from '../lib/api';
import { useVoicePlayback } from '../hooks/useVoicePlayback';
import { answerVoiceStatus, type VoiceAgentStatus } from '../lib/voiceStatus';
import { Link, useParams } from 'react-router-dom';
import { useVoiceInput } from '../hooks/useVoiceInput';
import { useAcousticBargeIn } from '../hooks/useAcousticBargeIn';
import { VoiceIdentityPanel } from '../components/native/VoiceIdentityPanel';

type VoiceNote = { id: number; text: string; answer?: string; source?: string };
type ConversationPhase = 'off' | 'listening' | 'processing' | 'speaking';

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
  const [conversationPhase, setConversationPhase] = useState<ConversationPhase>('off');
  const conversationActiveRef = useRef(false);
  const requestGenerationRef = useRef(0);
  const requestAbortRef = useRef<AbortController | null>(null);
  const [codingText, setCodingText] = useState('');
  const bargeIn = useAcousticBargeIn(() => {
    // An acoustic interruption only stops TTS; it never executes coding work.
    playback.stop();
    voice.reset();
    if (conversationActiveRef.current) setConversationPhase('listening');
    setNotice('목소리가 감지되어 음성 응답을 중단했어. 이어서 말해 줘.');
  });

  useEffect(() => {
    bargeIn.setSpeaking(conversationPhase === 'speaking' && playback.speaking);
  }, [conversationPhase, playback.speaking, bargeIn.setSpeaking]);
  const onCodingSent = useCallback(() => { setCodingText(''); setDraft(''); voice.reset(); }, [voice.reset]);
  const onCodingAnnouncement = useCallback((text: string) => {
    if (conversationPhase === 'listening') voice.reset();
    const spoken = playback.speak(text);
    if (conversationPhase !== 'off' && spoken) setConversationPhase('speaking');
  }, [conversationPhase, voice.reset, playback.speak]);

  const addNote = async (spokenText?: string) => {
    const text = spokenText?.trim() || draft.trim() || voice.finalText.trim();
    if (!text) return;
    playback.stop();
    const generation = ++requestGenerationRef.current;
    requestAbortRef.current?.abort();
    const controller = new AbortController();
    requestAbortRef.current = controller;
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
      const history = notes.slice(-8).flatMap((note) => [
        { role: 'user', content: note.text },
        ...(note.answer ? [{ role: 'assistant', content: note.answer }] : []),
      ]);
      const response = await apiRequest('/voice/dialogue', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ agentId: id, message: text, history }),
        signal: controller.signal,
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
    if (generation !== requestGenerationRef.current || controller.signal.aborted) return;
    requestAbortRef.current = null;
    setNotes((previous) => [...previous, { id: nextId, text, answer, source }]);
    setLoadingStatus(false);
    const spoken = playback.speak(answer);
    if (spokenText !== undefined && conversationActiveRef.current) setConversationPhase(spoken ? 'speaking' : 'listening');
    setNextId((n) => n + 1);
    setDraft('');
    voice.reset();
    setNotice('읽기 전용 상태 답변이야. 코딩 에이전트에는 명령을 전달하지 않았어.');
  };
  // A deliberate start gesture enables a bounded listen -> reply -> listen
  // loop. Only read-only dialogue is submitted automatically. Coding tasks
  // always require a separate reviewed, explicit confirmation.
  const toggleConversation = () => {
    if (conversationPhase !== 'off') {
      conversationActiveRef.current = false;
      requestGenerationRef.current += 1;
      requestAbortRef.current?.abort();
      requestAbortRef.current = null;
      setLoadingStatus(false);
      setConversationPhase('off');
      voice.reset();
      playback.stop();
      bargeIn.stop();
      return;
    }
    if (!voice.supported) {
      setNotice('이 브라우저에서 음성 인식이 지원되지 않아. 텍스트 대화를 이용해 줘.');
      return;
    }
    if (playback.supported && !playback.enabled) playback.toggle();
    // User gesture starts a separate mic monitor for barge-in. Failure falls
    // back to the explicit interrupt button, never blocks ordinary dialogue.
    void bargeIn.start();
    setDraft('');
    conversationActiveRef.current = true;
    setConversationPhase('listening');
  };

  useEffect(() => () => {
    bargeIn.stop();
    requestAbortRef.current?.abort();
    requestGenerationRef.current += 1;
    conversationActiveRef.current = false;
  }, [bargeIn.stop]);

  const interruptSpeech = () => {
    playback.stop();
    voice.reset();
    if (conversationActiveRef.current) setConversationPhase('listening');
  };

  useEffect(() => {
    if (conversationPhase !== 'listening') return;
    if (voice.state === 'error' || voice.state === 'unsupported') {
      setConversationPhase('off');
      setNotice(voice.error || '음성 인식을 시작하지 못했어.');
      return;
    }
    if (voice.state !== 'idle' || voice.finalText.trim() || loadingStatus || playback.speaking) return;
    const timeout = window.setTimeout(() => voice.start(), 350);
    return () => clearTimeout(timeout);
  }, [conversationPhase, voice.state, voice.finalText, voice.error, loadingStatus, playback.speaking, voice.start]);

  useEffect(() => {
    if (conversationPhase !== 'listening' || loadingStatus) return;
    const transcript = voice.finalText.trim();
    if (!transcript || voice.interimText.trim()) return;
    // Allow a natural pause for the end of the utterance. Resetting recognition
    // after consumption prevents a transcript from being submitted twice.
    const timeout = window.setTimeout(() => {
      if (conversationPhase !== 'listening') return;
      setConversationPhase('processing');
      voice.reset();
      void addNote(transcript);
    }, 1300);
    return () => clearTimeout(timeout);
  }, [conversationPhase, voice.finalText, voice.interimText, loadingStatus]);

  useEffect(() => {
    if (conversationPhase !== 'speaking' || playback.speaking) return;
    const timeout = window.setTimeout(() => setConversationPhase('listening'), 450);
    return () => clearTimeout(timeout);
  }, [conversationPhase, playback.speaking]);

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
          <p className="text-xs text-deck-text-dim">자동 음성 대화 · 개발 지시는 별도 승인</p>
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
        <button type="button" onClick={toggleConversation} disabled={!voice.supported}
          aria-pressed={conversationPhase !== 'off'} className="w-full min-h-12 rounded-xl border border-deck-border px-4 py-3 text-sm font-semibold disabled:opacity-40">
          {conversationPhase === 'off' ? '🎙 자동 대화 시작' : '■ 자동 대화 종료'}
        </button>
        {conversationPhase !== 'off' && bargeIn.error && <p role="alert" className="text-xs text-deck-text-dim">{bargeIn.error}</p>}
        {conversationPhase !== 'off' && bargeIn.active && <p className="text-xs text-deck-text-dim">실험적 음성 끼어들기 감지 활성화 · 이어폰 권장 · 스피커 소리로 오작동할 수 있어.</p>}
        {conversationPhase === 'speaking' && <button type="button" onClick={interruptSpeech}
          className="min-h-11 w-full rounded-lg border border-deck-border px-3 text-sm">응답 중단하고 말하기</button>}
        {conversationPhase !== 'off' && <p role="status" className="text-sm text-deck-text-dim">{conversationPhase === 'listening' ? '말씀을 듣고 있어요. 말을 마치면 자동으로 질문해요.' : conversationPhase === 'processing' ? '답변을 준비하고 있어요.' : '음성으로 답변 중이에요.'}</p>}
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
          <button type="button" disabled={!voice.supported || conversationPhase !== 'off'}
            onClick={() => { playback.stop(); voice.state === 'listening' ? voice.stop() : voice.start(); }}
            className="min-h-12 min-w-12 rounded-full border border-deck-border bg-deck-bg disabled:opacity-40"
            aria-label={voice.state === 'listening' ? '녹음 중지' : '마이크 시작'}>
            {voice.state === 'listening' ? '■' : '🎙'}
          </button>
          <span className="text-xs flex-1 text-deck-text-dim">
            {voice.state === 'listening' ? '듣는 중…' : voice.supported ? '마이크를 누르고 말해 줘' : '브라우저 음성 인식 미지원 · 글 입력 가능'}
          </span>
          <button type="button" onClick={() => void addNote()} disabled={conversationPhase !== 'off' || loadingStatus || (!draft.trim() && !voice.finalText.trim())}
            className="rounded-lg border border-deck-border px-3 py-3 text-sm disabled:opacity-40">
            {loadingStatus ? '상태 확인 중…' : '상태 질문'}
          </button>
        </div>
      </div>
    </div>
  );
}
