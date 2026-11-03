import { useState } from 'react';
import { Link, useParams } from 'react-router-dom';
import { useVoiceInput } from '../hooks/useVoiceInput';

type VoiceNote = { id: number; text: string };

/**
 * Separate voice conversation surface. This first stage deliberately has NO
 * authority to call native:input, send shell commands or edit repository files.
 * Voice notes may be explicitly reviewed in the coding composer instead.
 */
export function VoiceModePage() {
  const { id = '' } = useParams<{ id: string }>();
  const voice = useVoiceInput('ko-KR');
  const [notes, setNotes] = useState<VoiceNote[]>([]);
  const [draft, setDraft] = useState('');
  const [notice, setNotice] = useState('');
  const [nextId, setNextId] = useState(1);

  const addNote = () => {
    const text = draft.trim() || voice.finalText.trim();
    if (!text) return;
    setNotes((previous) => [...previous, { id: nextId, text }]);
    setNextId((n) => n + 1);
    setDraft('');
    voice.reset();
    setNotice('대화 메모에 저장했어. 코딩 에이전트에는 전달하지 않았어.');
  };
  const stageCodingRequest = (text: string) => {
    // Transfer is just a reviewed draft, NEVER a send. The existing coding
    // composer and its own submit/approvals remain responsible for execution.
    sessionStorage.setItem(`pcd:voice-review:${id}`, text);
    setNotice('코딩 화면의 입력창으로 옮겼어. 확인 후 직접 전송해 줘.');
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
        <p className="rounded-xl bg-deck-surface border border-deck-border px-4 py-3 text-sm">
          이 화면의 대화는 코드를 수정하지 않아. 코드나 로그는 읽어주지 않고, 작업 지시는 코딩 화면에서 따로 확인해 전송해.
          <span className="block mt-2 text-xs text-deck-text-dim">현재는 분리된 음성 대화 UI의 첫 단계로, AI와 실시간 양방향 대화는 아직 연결되지 않았어.</span>
        </p>
        {notes.map((note) => (
          <article key={note.id} className="ml-auto max-w-[95%] rounded-2xl bg-deck-surface border border-deck-border p-3">
            <div className="text-xs text-deck-text-dim mb-1">나 · 음성 메모</div>
            <p className="whitespace-pre-wrap break-words text-sm">{note.text}</p>
            <button type="button" onClick={() => stageCodingRequest(note.text)}
              className="mt-3 rounded-lg border border-deck-border px-3 py-2 text-xs">
              코딩 요청으로 옮기기 (실행 안 함)
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
          placeholder="말하거나 텍스트를 입력해 대화 메모를 남길 수 있어."
          className="w-full resize-none rounded-lg border border-deck-border bg-deck-bg p-3 text-base" />
        <div className="flex items-center gap-3">
          <button type="button" disabled={!voice.supported}
            onClick={() => voice.state === 'listening' ? voice.stop() : voice.start()}
            className="min-h-12 min-w-12 rounded-full border border-deck-border bg-deck-bg disabled:opacity-40"
            aria-label={voice.state === 'listening' ? '녹음 중지' : '마이크 시작'}>
            {voice.state === 'listening' ? '■' : '🎙'}
          </button>
          <span className="text-xs flex-1 text-deck-text-dim">
            {voice.state === 'listening' ? '듣는 중…' : voice.supported ? '마이크를 누르고 말해 줘' : '브라우저 음성 인식 미지원 · 글 입력 가능'}
          </span>
          <button type="button" onClick={addNote} disabled={!draft.trim() && !voice.finalText.trim()}
            className="rounded-lg border border-deck-border px-3 py-3 text-sm disabled:opacity-40">
            대화 메모
          </button>
        </div>
      </div>
    </div>
  );
}
