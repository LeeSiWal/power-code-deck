import { useState } from 'react';
import { api, type RouteAgentResult } from '../../lib/api';
import { autoPickNote, autoFallbackNote } from '../../lib/routingLabels';
import { SESSION_TOOLS, setPendingStart } from '../../lib/sessionTools';
import { IconCheck, IconSpinner } from '../icons';

interface AutoChatProps {
  agentId: string;
  onBound: (agent: RouteAgentResult['agent']) => void;
}

// The chat of a "자동" session before its first message. No CLI runs yet: the
// first message routes the session to a tool/model (or a tool picked in the
// menu binds it directly), then the page swaps in the normal chat, which sends
// the message and shows what was picked.
export function AutoChat({ agentId, onBound }: AutoChatProps) {
  const [draft, setDraft] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const [menuOpen, setMenuOpen] = useState(false);

  const bind = async (adapter?: string) => {
    const text = draft.trim();
    if (!text || busy) return;
    setBusy(true);
    setError('');
    setMenuOpen(false);
    try {
      const r: RouteAgentResult = await api.routeAgent(agentId, text, adapter);
      const note = r.profile
        ? autoPickNote(r.profile, r.ruleTier || '', r.source || '')
        : r.fallback ? autoFallbackNote(r.fallback) : '';
      setPendingStart(agentId, { message: text, note });
      onBound(r.agent);
    } catch (err) {
      setError('세션을 시작하지 못했습니다: ' + String(err));
      setBusy(false);
    }
  };

  return (
    <div className="relative flex flex-col h-full bg-deck-bg">
      <div className="flex-1 min-h-0 overflow-y-auto flex items-center justify-center px-6">
        <div className="text-sm text-deck-text-dim text-center max-w-sm">
          메시지를 보내면 요청에 맞는 도구와 모델을 골라 대화를 시작합니다.
        </div>
      </div>

      {error && (
        <div className="mx-2 mb-1 px-3 py-2 rounded-lg bg-red-500/15 text-red-400 text-xs">{error}</div>
      )}

      <div className="border-t border-deck-border safe-bottom relative">
        {menuOpen && (
          <div className="absolute bottom-14 left-2 z-20 w-64 max-w-[calc(100vw-1rem)] bg-deck-raised border border-deck-border rounded-lg shadow-xl overflow-hidden">
            <div className="px-3 py-1.5 text-[10px] uppercase tracking-wide text-deck-text-dim">도구</div>
            <button onClick={() => setMenuOpen(false)} className="w-full text-left px-3 py-2 bg-deck-bg/40 flex items-center gap-2">
              <span className="shrink-0 w-3.5 text-deck-accent"><IconCheck size={14} /></span>
              <span className="text-sm text-deck-accent">자동</span>
              <span className="ml-auto text-xs text-deck-text-dim">요청 보고 선택</span>
            </button>
            {SESSION_TOOLS.map((t) => (
              <button
                key={t.preset}
                onClick={() => bind(t.driver)}
                disabled={!draft.trim() || busy}
                className="w-full text-left px-3 py-2 hover:bg-deck-bg/60 flex items-center gap-2 disabled:opacity-40"
              >
                <span className="shrink-0 w-3.5" />
                <span className="text-sm text-deck-text">{t.name}</span>
                <span className="ml-auto text-xs text-deck-text-dim">이 도구로 보내기</span>
              </button>
            ))}
          </div>
        )}

        {busy && (
          <div className="mx-2 mt-2 flex items-center gap-2 px-3 py-1.5 rounded-lg bg-deck-accent/10 border border-deck-accent/20 text-deck-accent-light text-xs">
            <IconSpinner size={13} className="animate-spin shrink-0" />
            <span>도구와 모델을 고르는 중…</span>
          </div>
        )}

        <div className="p-2 space-y-2">
          <textarea
            autoFocus
            value={draft}
            onChange={(e) => setDraft(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === 'Enter' && !e.shiftKey && !e.nativeEvent.isComposing) {
                e.preventDefault();
                bind();
              }
            }}
            rows={2}
            placeholder="무엇을 할까요? (자동으로 도구와 모델을 고릅니다)"
            className="w-full resize-none bg-deck-surface border border-deck-border rounded-lg px-3 py-2 text-sm text-deck-text outline-none focus:border-deck-accent"
          />
          <div className="flex items-center gap-2">
            <button
              onClick={() => setMenuOpen(!menuOpen)}
              className="shrink-0 h-8 px-2.5 rounded-full bg-deck-surface border border-deck-border text-deck-text-dim text-xs flex items-center gap-1.5"
              title="도구 선택"
            >
              <span className="w-1.5 h-1.5 rounded-full bg-deck-accent" />
              자동
            </button>
            <div className="flex-1" />
            <button
              onClick={() => bind()}
              disabled={!draft.trim() || busy}
              className="shrink-0 h-8 px-4 rounded-lg bg-deck-accent text-white text-sm font-medium disabled:opacity-40"
            >
              {busy ? '고르는 중' : '보내기'}
            </button>
          </div>
        </div>
      </div>
    </div>
  );
}
