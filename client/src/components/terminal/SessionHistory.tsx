import { useEffect, useState, useCallback } from 'react';
import { useNavigate } from 'react-router-dom';
import { api } from '../../lib/api';
import { IconClose, IconBack } from '../icons';

interface SessionSummary {
  id: string;
  startedAt: string;
  lastAt: string;
  messageCount: number;
  preview: string;
}
interface SessionMessage {
  role: 'user' | 'assistant';
  text: string;
  timestamp: string;
}

interface SessionHistoryProps {
  agentId: string;
  onClose: () => void;
}

function fmt(ts: string): string {
  if (!ts) return '';
  const d = new Date(ts);
  if (isNaN(d.getTime())) return ts.slice(0, 16).replace('T', ' ');
  return d.toLocaleString('ko-KR', { month: 'numeric', day: 'numeric', hour: '2-digit', minute: '2-digit' });
}

/**
 * Panel (side-panel tab / bottom sheet) that browses past Claude Code sessions
 * (transcripts) for this agent's project: view a session's conversation, delete
 * it, or resume it (launches a new agent with `claude --resume`). Single column:
 * list → tap → detail with a back button, so it fits a narrow side panel.
 */
export function SessionHistory({ agentId, onClose }: SessionHistoryProps) {
  const navigate = useNavigate();
  const [sessions, setSessions] = useState<SessionSummary[] | null>(null);
  const [selected, setSelected] = useState<SessionSummary | null>(null);
  const [messages, setMessages] = useState<SessionMessage[] | null>(null);
  const [busy, setBusy] = useState(false);

  const load = useCallback(() => {
    api.listSessions(agentId).then(setSessions).catch(() => setSessions([]));
  }, [agentId]);
  useEffect(() => { load(); }, [load]);

  const open = useCallback((s: SessionSummary) => {
    setSelected(s);
    setMessages(null);
    api.getSession(agentId, s.id).then(setMessages).catch(() => setMessages([]));
  }, [agentId]);

  const del = useCallback(async (s: SessionSummary) => {
    if (!window.confirm('이 세션 기록을 삭제할까요? 되돌릴 수 없습니다.')) return;
    setBusy(true);
    try {
      await api.deleteSession(agentId, s.id);
      setSelected(null);
      setMessages(null);
      load();
    } finally {
      setBusy(false);
    }
  }, [agentId, load]);

  const resume = useCallback(async (s: SessionSummary) => {
    setBusy(true);
    try {
      const a = await api.resumeSession(agentId, s.id) as { id: string };
      onClose();
      navigate(`/agents/${a.id}`);
    } catch {
      setBusy(false);
    }
  }, [agentId, navigate, onClose]);

  const startNew = useCallback(async () => {
    setBusy(true);
    try {
      const a = await api.newSession(agentId);
      onClose();
      navigate(`/agents/${a.id}`);
    } catch {
      setBusy(false);
    }
  }, [agentId, navigate, onClose]);

  return (
    <div className="flex flex-col h-full min-h-0 bg-deck-surface">
      <header className="flex items-center gap-2 px-3 py-2 border-b border-deck-border shrink-0">
        {selected ? (
          <button onClick={() => { setSelected(null); setMessages(null); }}
                  className="p-1 -ml-1 rounded hover:bg-deck-border/30 text-deck-text-dim" title="목록으로">
            <IconBack size={14} />
          </button>
        ) : null}
        <span className="font-semibold text-sm">지난 세션 기록</span>
        {!selected && sessions && <span className="text-xs text-deck-text-dim">{sessions.length}개</span>}
        <button onClick={onClose} className="ml-auto p-1 rounded hover:bg-deck-border/30"><IconClose size={14} /></button>
      </header>

      {/* List */}
      {!selected && (
        <div className="flex-1 min-h-0 overflow-y-auto">
          <div className="p-2 border-b border-deck-border/50">
            <button
              disabled={busy}
              onClick={startNew}
              className="w-full flex items-center justify-center gap-1.5 px-3 py-2 rounded bg-deck-accent text-white text-sm font-medium touch-manipulation active:opacity-70 disabled:opacity-50"
            >
              <span className="text-base leading-none">+</span> 새 세션 시작
            </button>
          </div>
          {sessions === null && <div className="p-4 text-xs text-deck-text-dim">불러오는 중…</div>}
          {sessions?.length === 0 && <div className="p-4 text-xs text-deck-text-dim">이 프로젝트의 지난 세션이 없습니다.</div>}
          {sessions?.map((s) => (
            <button
              key={s.id}
              onClick={() => open(s)}
              className="w-full text-left px-3 py-2.5 border-b border-deck-border/50 hover:bg-deck-bg/60 touch-manipulation"
            >
              <div className="text-sm text-deck-text truncate">{s.preview || '(빈 세션)'}</div>
              <div className="text-[11px] text-deck-text-dim mt-0.5">{fmt(s.lastAt)} · {s.messageCount}개 메시지</div>
            </button>
          ))}
        </div>
      )}

      {/* Detail */}
      {selected && (
        <>
          <div className="flex items-center gap-2 px-3 py-2 border-b border-deck-border/50 shrink-0">
            <span className="text-xs text-deck-text-dim truncate flex-1">{fmt(selected.startedAt)} 시작</span>
            <button disabled={busy} onClick={() => resume(selected)}
                    className="text-xs px-2.5 py-1 rounded bg-deck-accent text-white touch-manipulation active:opacity-70 disabled:opacity-50">
              이어하기
            </button>
            <button disabled={busy} onClick={() => del(selected)}
                    className="text-xs px-2.5 py-1 rounded bg-deck-danger/20 text-deck-danger touch-manipulation active:opacity-70 disabled:opacity-50">
              삭제
            </button>
          </div>
          <div className="flex-1 min-h-0 overflow-y-auto selectable px-3 py-3 space-y-3">
            {messages === null && <div className="text-xs text-deck-text-dim">불러오는 중…</div>}
            {messages?.length === 0 && <div className="text-xs text-deck-text-dim">표시할 대화가 없습니다.</div>}
            {messages?.map((m, i) => (
              <div key={i} className={m.role === 'user' ? 'flex justify-end' : 'flex justify-start'}>
                <div className={`max-w-[88%] rounded-lg px-3 py-2 text-sm leading-relaxed whitespace-pre-wrap break-words ${
                  m.role === 'user' ? 'bg-deck-accent/20 text-deck-text' : 'bg-deck-bg text-deck-text'
                }`}>
                  {m.text}
                </div>
              </div>
            ))}
          </div>
        </>
      )}
    </div>
  );
}
