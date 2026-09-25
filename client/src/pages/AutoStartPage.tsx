import { useState } from 'react';
import { useNavigate, useParams } from 'react-router-dom';
import { api, ApiError, isFeatureOff } from '../lib/api';
import { autoPickNote } from '../lib/routingLabels';
import { SESSION_TOOLS, sessionName, setPendingStart, toolForAdapter, type SessionTool } from '../lib/sessionTools';
import { IconBack } from '../components/icons';
import { useGoUp } from '../hooks/useGoUp';

// "자동" start: the router can only pick a tool once it knows the request, so
// the first message is typed here. The session is then created with the picked
// tool/model and the message is sent as soon as its chat opens.
export function AutoStartPage() {
  const { encodedPath } = useParams<{ encodedPath: string }>();
  const workingDir = decodeURIComponent(encodedPath || '');
  const goUp = useGoUp('/');
  const navigate = useNavigate();
  const [goal, setGoal] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');

  const start = async (pinned?: SessionTool) => {
    const text = goal.trim();
    if (!text || busy) return;
    setBusy(true);
    setError('');
    let tool = pinned;
    let model = '';
    let effort = '';
    let note = '';
    if (!tool) {
      try {
        const ch = await api.routingChoose(text);
        tool = ch.profile ? toolForAdapter(ch.profile.adapter) : undefined;
        if (!tool || !ch.profile) throw new Error('unsupported adapter');
        model = ch.profile.model || '';
        effort = ch.profile.effort || '';
        note = autoPickNote(ch.profile, ch.decision.ruleTier, ch.decision.source);
      } catch (err) {
        tool = SESSION_TOOLS[0];
        const why = isFeatureOff(err) ? '라우팅 기능이 꺼져 있어'
          : err instanceof ApiError && err.status === 409 ? '조건에 맞는 모델이 없어'
          : '자동 선택에 실패해';
        note = `${why} ${tool.name}로 시작했습니다. 라우팅 설정은 설정 화면에서 확인할 수 있습니다.`;
      }
    }
    try {
      const a = await api.createAgent({ preset: tool.preset, name: sessionName(tool, workingDir), workingDir, command: tool.command, args: [] }) as { id: string };
      // The chat reads these as its starting model/effort (the DB is empty for a new session).
      try {
        if (model) localStorage.setItem(`pcd:model:${a.id}`, model);
        if (effort && tool.driver === 'claude') localStorage.setItem(`pcd:effort:${a.id}`, effort);
      } catch { /* storage may be blocked */ }
      setPendingStart(a.id, { message: text, note });
      navigate(`/agents/${a.id}`);
    } catch (err) {
      setError('세션을 시작하지 못했습니다: ' + String(err));
      setBusy(false);
    }
  };

  const dirName = workingDir.split('/').filter(Boolean).pop() || workingDir;

  return (
    <div className="flex flex-col h-full safe-top bg-deck-bg overflow-hidden">
      <header className="flex items-center gap-3 px-4 py-2 bg-deck-surface border-b border-deck-border">
        <button onClick={goUp} className="p-1 rounded hover:bg-deck-border/30">
          <IconBack size={16} />
        </button>
        <span className="text-sm font-medium truncate">새 세션 · {dirName}</span>
      </header>
      <main className="flex-1 overflow-y-auto p-4 max-w-lg mx-auto w-full space-y-3">
        <p className="text-xs text-deck-text-dim">
          <span className="text-deck-accent font-medium">자동</span> — 요청을 보고 도구와 모델을 고릅니다. 설치·로그인·과금 조건을 통과한 모델 중에서만 고릅니다.
        </p>
        <textarea
          autoFocus
          value={goal}
          onChange={(e) => setGoal(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === 'Enter' && !e.shiftKey && !e.nativeEvent.isComposing) {
              e.preventDefault();
              start();
            }
          }}
          rows={5}
          placeholder="무엇을 할까요?"
          className="w-full resize-none bg-deck-surface border border-deck-border rounded-lg px-3 py-2 text-sm text-deck-text outline-none focus:border-deck-accent"
        />
        {error && <p className="text-xs text-red-400">{error}</p>}
        <button
          onClick={() => start()}
          disabled={!goal.trim() || busy}
          className="w-full py-2.5 rounded-lg bg-deck-accent text-white text-sm font-medium disabled:opacity-40"
        >
          {busy ? '모델 고르는 중…' : '자동으로 시작'}
        </button>
        <div className="flex flex-wrap items-center gap-2 text-xs text-deck-text-dim">
          <span>직접 고르기:</span>
          {SESSION_TOOLS.map((t) => (
            <button
              key={t.preset}
              onClick={() => start(t)}
              disabled={!goal.trim() || busy}
              className="px-2.5 py-1 rounded-full border border-deck-border hover:bg-deck-border/30 disabled:opacity-40"
            >
              {t.name}
            </button>
          ))}
        </div>
      </main>
    </div>
  );
}
