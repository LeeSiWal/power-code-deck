import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { agentDeckWS } from '../../lib/ws';
import { api } from '../../lib/api';
import { foldEvents, isTurnActive, toolSummary, type AskQuestion, type ChatItem, type StreamEvent } from '../../lib/nativeEvents';

/**
 * NativeChat — a Claude session rendered from its event stream instead of a
 * terminal.
 *
 * Everything the terminal fights (cell widths, cursor state, DEC modes, reflow,
 * replay fidelity) is absent here by construction: this is text in a div. The one
 * thing that DOES need care is the part a TUI can't do well on a phone — a
 * permission prompt you can actually answer with your thumb.
 */

interface PendingApproval {
  id: string;
  toolName: string;
  input: Record<string, unknown>;
  askedAt: string;
}

interface NativeChatProps {
  agentId: string;
  cwd: string;
  model?: string;
}

// Model choices for the switcher. `id` is passed straight to the CLI's --model;
// '' = the CLI default ("Auto"). Switching restarts the session on the same
// conversation (server SetModel), so nothing is lost.
const MODELS: { id: string; label: string; desc: string }[] = [
  { id: '', label: 'Auto', desc: 'CLI 기본 선택' },
  { id: 'claude-fable-5', label: 'Fable 5', desc: '최신 · 복잡하고 긴 작업' },
  { id: 'claude-opus-4-8', label: 'Opus 4.8', desc: '가장 강력 · 깊은 추론' },
  { id: 'claude-opus-4-8[1m]', label: 'Opus 4.8 · 1M', desc: '초대용량 컨텍스트(1M)' },
  { id: 'claude-sonnet-5', label: 'Sonnet 5', desc: '균형 · 빠르고 똑똑' },
  { id: 'claude-haiku-4-5-20251001', label: 'Haiku 4.5', desc: '가장 빠름 · 가벼운 작업' },
];

// Permission modes — the TUI's Shift+Tab cycle. `id` → --permission-mode.
// Switching restarts the session on the same conversation (server SetMode).
// `pill`/`dot` encode risk in colour: neutral → indigo → sky (plan) → amber (careful).
const MODES: { id: string; label: string; desc: string; pill: string; dot: string }[] = [
  { id: '', label: '기본', desc: '도구마다 승인 요청', pill: 'border-deck-border bg-deck-surface text-deck-text-dim', dot: 'bg-deck-text-dim' },
  { id: 'acceptEdits', label: '자동 수정', desc: '파일 편집 자동 승인', pill: 'border-deck-accent/50 bg-deck-accent/10 text-deck-accent-light', dot: 'bg-deck-accent-light' },
  { id: 'plan', label: '플랜', desc: '실행 없이 계획만 세움', pill: 'border-sky-400/40 bg-sky-400/10 text-sky-300', dot: 'bg-sky-300' },
  { id: 'bypassPermissions', label: '전체 허용', desc: '모든 도구 자동 승인', pill: 'border-amber-400/45 bg-amber-400/10 text-amber-300', dot: 'bg-amber-300' },
];

export function NativeChat({ agentId, cwd, model }: NativeChatProps) {
  const [events, setEvents] = useState<StreamEvent[]>([]);
  const [pending, setPending] = useState<PendingApproval[]>([]);
  const [running, setRunning] = useState(false);
  const [error, setError] = useState('');
  const [draft, setDraft] = useState('');
  const [attachments, setAttachments] = useState<{ name: string; path: string }[]>([]);
  const [uploading, setUploading] = useState(false);
  const [modelId, setModelId] = useState(() => localStorage.getItem(`pcd:model:${agentId}`) || '');
  const [modeId, setModeId] = useState(() => localStorage.getItem(`pcd:mode:${agentId}`) || '');
  const [menu, setMenu] = useState<null | 'add' | 'model' | 'mode'>(null);
  const scrollRef = useRef<HTMLDivElement>(null);
  const stickRef = useRef(true);
  const taRef = useRef<HTMLTextAreaElement>(null);
  const fileRef = useRef<HTMLInputElement>(null);
  const modelIdRef = useRef(modelId);
  modelIdRef.current = modelId;
  const modeIdRef = useRef(modeId);
  modeIdRef.current = modeId;

  const pickModel = useCallback((id: string) => {
    setMenu(null);
    if (id === modelIdRef.current) return;
    setModelId(id);
    try { localStorage.setItem(`pcd:model:${agentId}`, id); } catch { /* ignore */ }
    // Restart on the same conversation with the new --model.
    agentDeckWS.send('native:setModel', { agentId, model: id });
  }, [agentId]);

  const pickMode = useCallback((id: string) => {
    setMenu(null);
    if (id === modeIdRef.current) return;
    setModeId(id);
    try { localStorage.setItem(`pcd:mode:${agentId}`, id); } catch { /* ignore */ }
    agentDeckWS.send('native:setMode', { agentId, mode: id });
  }, [agentId]);

  // Shift+Tab cycles the permission mode, like the Claude Code TUI.
  const cycleMode = useCallback(() => {
    const i = MODES.findIndex((m) => m.id === modeIdRef.current);
    pickMode(MODES[(i + 1) % MODES.length].id);
  }, [pickMode]);

  const modelLabel = MODELS.find((m) => m.id === modelId)?.label ?? modelId ?? 'Auto';
  const currentMode = MODES.find((m) => m.id === modeId) ?? MODES[0];

  // Grow the input with its content (up to a cap, then it scrolls internally).
  useEffect(() => {
    const ta = taRef.current;
    if (!ta) return;
    ta.style.height = 'auto';
    ta.style.height = `${Math.min(ta.scrollHeight, 160)}px`;
  }, [draft]);

  const onFilePick = useCallback(async (e: React.ChangeEvent<HTMLInputElement>) => {
    const files = Array.from(e.target.files ?? []);
    e.target.value = ''; // let the same file be picked again later
    if (!files.length) return;
    setUploading(true);
    try {
      for (const f of files) {
        const r = await api.attachFile(agentId, f);
        setAttachments((prev) => [...prev, r]);
      }
    } catch (err) {
      setError('파일 업로드 실패: ' + String(err));
    } finally {
      setUploading(false);
    }
  }, [agentId]);

  const items = useMemo(() => foldEvents(events), [events]);
  // Derived from the events, not tracked separately, so it survives a reconnect:
  // the history alone says whether a turn is still in flight.
  const busy = useMemo(() => isTurnActive(events), [events]);

  useEffect(() => {
    const offEvent = agentDeckWS.on('native:event', (p: any) => {
      if (p.agentId !== agentId) return;
      setEvents((prev) => [...prev, p.event as StreamEvent]);
    });
    // History is the native track's replay — no serializer, no ring buffer. The
    // events ARE the state, so reconnecting just means folding them again.
    const offHistory = agentDeckWS.on('native:history', (p: any) => {
      if (p.agentId !== agentId) return;
      setEvents(p.events as StreamEvent[]);
      setRunning(!!p.running);
      // The session's model/mode are authoritative — they may have been chosen on
      // another device, or restored from a past session. Sync the toolbar (and this
      // device's remembered choice) to them so what's shown matches what's running.
      if (typeof p.model === 'string' && p.model !== modelIdRef.current) {
        setModelId(p.model);
        try { localStorage.setItem(`pcd:model:${agentId}`, p.model); } catch { /* ignore */ }
      }
      if (typeof p.mode === 'string' && p.mode !== modeIdRef.current) {
        setModeId(p.mode);
        try { localStorage.setItem(`pcd:mode:${agentId}`, p.mode); } catch { /* ignore */ }
      }
    });
    const offApproval = agentDeckWS.on('native:approval', (p: any) => {
      if (p.agentId !== agentId) return;
      setPending((prev) => (prev.some((x) => x.id === p.id) ? prev : [...prev, p]));
    });
    // State carries whatever the agent is already blocked on — without it, a device
    // that connects mid-run stares at a frozen agent with no prompt to answer.
    const offState = agentDeckWS.on('native:state', (p: any) => {
      if (p.agentId !== agentId) return;
      setRunning(!!p.running);
      setPending(p.pending ?? []);
    });
    // Failures are shown, never swallowed: a message that went nowhere must not
    // look like a message that is being answered.
    const offError = agentDeckWS.on('native:error', (p: any) => {
      if (p.agentId !== agentId) return;
      setError(p.message ?? '알 수 없는 오류');
    });

    const open = () => agentDeckWS.send('native:open', { agentId, cwd, model: modelIdRef.current, mode: modeIdRef.current });
    open();
    const offOpen = agentDeckWS.on('open', open); // re-open after a reconnect

    return () => { offEvent(); offHistory(); offApproval(); offState(); offError(); offOpen(); };
  }, [agentId, cwd, model]);

  // Stick to the bottom unless the user scrolled up to read something.
  useEffect(() => {
    const el = scrollRef.current;
    if (el && stickRef.current) el.scrollTop = el.scrollHeight;
  }, [items, pending]);

  const onScroll = useCallback(() => {
    const el = scrollRef.current;
    if (!el) return;
    stickRef.current = el.scrollHeight - el.scrollTop - el.clientHeight < 40;
  }, []);

  const sendText = useCallback((text: string) => {
    if (!text.trim()) return;
    setError('');
    agentDeckWS.send('native:input', { agentId, text });
  }, [agentId]);

  const interrupt = useCallback(() => {
    agentDeckWS.send('native:interrupt', { agentId });
  }, [agentId]);

  const send = useCallback(() => {
    const text = draft.trim();
    if (!text && !attachments.length) return;
    // Attachments ride along as paths inside the project — Claude opens them with
    // its Read tool. Sent as part of the same user turn.
    const msg = attachments.length
      ? (text ? text + '\n\n' : '') + '첨부 파일 (Read 도구로 확인해줘):\n' + attachments.map((a) => a.path).join('\n')
      : text;
    sendText(msg);
    setAttachments([]);
    // No local echo: the server records the user turn the moment it's sent
    // (NativeService.Send) and fans it out, so it arrives like every other event and
    // survives a reconnect. A local copy would print twice, and — being invisible to
    // the server — would vanish whenever history replaced our events.
    setDraft('');
  }, [draft, attachments, sendText]);

  const decide = useCallback((id: string, behavior: 'allow' | 'deny', message?: string) => {
    agentDeckWS.send('native:decide', { agentId, id, behavior, message });
    setPending((prev) => prev.filter((p) => p.id !== id));
  }, [agentId]);

  return (
    <div className="flex flex-col h-full bg-deck-bg">
      <div ref={scrollRef} onScroll={onScroll} className="selectable flex-1 overflow-y-auto px-3 py-3 space-y-2">
        {items.map((item) => (
          <ChatRow key={`${item.kind}-${item.id}`} item={item} onAnswer={sendText} />
        ))}
        {!items.length && (
          <div className="text-deck-muted text-sm py-8 text-center">
            {/* `claude -p --input-format stream-json` emits NOTHING until the first
                user turn — not even system/init. So a live session with no events
                is not "starting", it's waiting for you. Saying "시작 중…" here made
                a ready session look like a hung one. */}
            {running ? '세션 준비됨 · 메시지를 보내세요.' : '메시지를 보내 대화를 시작하세요.'}
          </div>
        )}
      </div>

      {error && (
        <div className="mx-2 mb-1 px-3 py-2 rounded-lg bg-red-500/15 text-red-400 text-xs flex items-start gap-2">
          <span className="flex-1">{error}</span>
          <button onClick={() => setError('')} className="shrink-0 opacity-60">닫기</button>
        </div>
      )}

      {pending.map((p) => <ApprovalCard key={p.id} req={p} onDecide={decide} />)}

      <div className="border-t border-deck-border safe-bottom relative">
        {/* Backdrop to dismiss an open menu on any outside click. */}
        {menu && <div className="fixed inset-0 z-10" onClick={() => setMenu(null)} />}

        {/* + (Add) menu — mirrors the desktop app's Add popup. */}
        {menu === 'add' && (
          <div className="absolute bottom-14 left-2 z-20 w-56 bg-deck-raised border border-deck-border rounded-lg shadow-xl overflow-hidden text-sm">
            <button
              onClick={() => { setMenu(null); fileRef.current?.click(); }}
              className="w-full text-left px-3 py-2.5 hover:bg-deck-bg/60 text-deck-text flex items-center gap-2"
            >
              <span>⬆️</span> 컴퓨터에서 업로드
            </button>
          </div>
        )}

        {/* Model switcher menu. */}
        {menu === 'model' && (
          <div className="absolute bottom-14 right-2 z-20 w-64 max-w-[calc(100vw-1rem)] bg-deck-raised border border-deck-border rounded-lg shadow-xl overflow-hidden">
            <div className="px-3 py-1.5 text-[10px] uppercase tracking-wide text-deck-text-dim">모델</div>
            {MODELS.map((m) => (
              <button
                key={m.id}
                onClick={() => pickModel(m.id)}
                className={`w-full text-left px-3 py-2 hover:bg-deck-bg/60 flex items-start gap-2 ${m.id === modelId ? 'bg-deck-bg/40' : ''}`}
              >
                <span className={`mt-0.5 shrink-0 w-3 ${m.id === modelId ? 'text-deck-accent' : 'text-transparent'}`}>✓</span>
                <span className="min-w-0">
                  <span className={`block text-sm ${m.id === modelId ? 'text-deck-accent' : 'text-deck-text'}`}>{m.label}</span>
                  <span className="block text-xs text-deck-text-dim truncate">{m.desc}</span>
                </span>
              </button>
            ))}
          </div>
        )}

        {/* Permission-mode menu (also cycled by Shift+Tab). */}
        {menu === 'mode' && (
          <div className="absolute bottom-14 right-2 z-20 w-64 max-w-[calc(100vw-1rem)] bg-deck-raised border border-deck-border rounded-lg shadow-xl overflow-hidden">
            <div className="px-3 py-1.5 text-[10px] uppercase tracking-wide text-deck-text-dim">권한 모드 · Shift+Tab</div>
            {MODES.map((m) => (
              <button
                key={m.id}
                onClick={() => pickMode(m.id)}
                className={`w-full text-left px-3 py-2 hover:bg-deck-bg/60 flex items-start gap-2 ${m.id === modeId ? 'bg-deck-bg/40' : ''}`}
              >
                <span className={`mt-0.5 shrink-0 w-3 ${m.id === modeId ? 'text-deck-accent' : 'text-transparent'}`}>✓</span>
                <span className="min-w-0">
                  <span className="flex items-center gap-1.5">
                    <span className={`w-1.5 h-1.5 rounded-full ${m.dot}`} />
                    <span className={`text-sm ${m.id === modeId ? 'text-deck-accent' : 'text-deck-text'}`}>{m.label}</span>
                  </span>
                  <span className="block text-xs text-deck-text-dim truncate">{m.desc}</span>
                </span>
              </button>
            ))}
          </div>
        )}

        {attachments.length > 0 && (
          <div className="flex flex-wrap gap-1.5 px-2 pt-2">
            {attachments.map((a, i) => (
              <span key={i} className="flex items-center gap-1 bg-deck-surface border border-deck-border rounded px-2 py-1 text-xs text-deck-text max-w-[70%]">
                <span className="truncate">📎 {a.name}</span>
                <button
                  onClick={() => setAttachments((prev) => prev.filter((_, j) => j !== i))}
                  className="opacity-60 shrink-0"
                  title="첨부 제거"
                >
                  ✕
                </button>
              </span>
            ))}
          </div>
        )}

        <div className="p-2 space-y-2">
          <input ref={fileRef} type="file" multiple className="hidden" onChange={onFilePick} />
          <textarea
            ref={taRef}
            value={draft}
            onChange={(e) => setDraft(e.target.value)}
            onKeyDown={(e) => {
              // Shift+Tab cycles the permission mode, like the Claude Code TUI —
              // otherwise Tab would just move focus out of the box.
              if (e.key === 'Tab' && e.shiftKey) {
                e.preventDefault();
                cycleMode();
                return;
              }
              // Enter sends; Shift+Enter is a newline. On mobile the soft keyboard's
              // return arrives as a plain Enter, which is what we want here.
              if (e.key === 'Enter' && !e.shiftKey && !e.nativeEvent.isComposing) {
                e.preventDefault();
                send();
              }
            }}
            rows={1}
            placeholder="Claude에게 메시지…"
            style={{ maxHeight: 160 }}
            className="w-full resize-none overflow-y-auto bg-deck-surface border border-deck-border rounded-lg px-3 py-2 text-sm text-deck-text outline-none focus:border-deck-accent"
          />

          <div className="flex items-center gap-2">
            <button
              onClick={() => setMenu(menu === 'add' ? null : 'add')}
              disabled={uploading}
              className="shrink-0 w-8 h-8 rounded-lg bg-deck-surface border border-deck-border text-deck-text-dim flex items-center justify-center text-lg disabled:opacity-40"
              title="추가"
            >
              {uploading ? '⏳' : '＋'}
            </button>
            <button
              onClick={() => setMenu(menu === 'model' ? null : 'model')}
              className="shrink-0 h-8 px-2.5 rounded-full bg-deck-surface border border-deck-border text-deck-text-dim text-xs flex items-center gap-1.5"
              title="모델 전환"
            >
              <span className="w-1.5 h-1.5 rounded-full bg-deck-accent" />
              {modelLabel}
            </button>
            <button
              onClick={() => setMenu(menu === 'mode' ? null : 'mode')}
              className={`shrink-0 h-8 px-2.5 rounded-full border text-xs font-medium flex items-center gap-1.5 ${currentMode.pill}`}
              title="권한 모드 전환 (Shift+Tab)"
            >
              <span className={`w-1.5 h-1.5 rounded-full ${currentMode.dot}`} />
              {currentMode.label}
            </button>
            <div className="flex-1" />
            {/* Sending mid-turn is allowed. Measured against the real CLI: a message
                written to stdin while a turn is in flight neither interrupts nor
                steers it — the running answer completes untouched, then the queued
                message starts its own turn immediately. So the button stays, and
                only its label changes to say where the message is going. Hiding it
                (as this did before) left Enter still sending, so keyboard and touch
                users had different rules. */}
            {busy && (
              <button
                onClick={interrupt}
                className="shrink-0 px-3 h-8 rounded-lg bg-red-500/20 text-red-400 text-sm font-medium"
                title="답변 중단"
              >
                중단
              </button>
            )}
            <button
              onClick={send}
              disabled={!draft.trim() && !attachments.length}
              className="shrink-0 px-4 h-8 rounded-lg bg-deck-accent text-white text-sm font-medium disabled:opacity-40"
              title={busy ? '현재 답변이 끝나면 이어서 처리됩니다' : undefined}
            >
              {busy ? '이어서' : '보내기'}
            </button>
          </div>
        </div>
      </div>
    </div>
  );
}

function ChatRow({ item, onAnswer }: { item: ChatItem; onAnswer: (text: string) => void }) {
  if (item.kind === 'session') {
    // Model / version / cwd are chrome, not conversation — the toolbar already shows
    // the model, so rendering them here just pushes the chat down on every session.
    // The bridge warning is the opposite: without our bridge the CLI denies every
    // gated tool AND still calls the turn a success. That silence is exactly what we
    // must not reproduce, so it stays — and is now the only reason this row renders.
    if (item.bridgeOk) return null;
    return (
      <div className="text-[11px] text-red-400 border border-red-400/40 bg-red-400/5 rounded-lg px-3 py-2">
        ⚠ 승인 브리지가 연결되지 않았습니다 — 권한이 필요한 도구가 전부 자동 거부됩니다.
      </div>
    );
  }

  if (item.kind === 'user') {
    return (
      <div className="flex justify-end">
        <div className="max-w-[85%] bg-deck-accent/20 text-deck-text rounded-lg px-3 py-2 text-sm whitespace-pre-wrap break-words">
          {item.text}
        </div>
      </div>
    );
  }

  if (item.kind === 'assistant') {
    return (
      <div className="max-w-[95%] text-deck-text text-sm whitespace-pre-wrap break-words px-1">
        {item.text}
      </div>
    );
  }

  if (item.kind === 'tool') return <ToolRow item={item} />;

  if (item.kind === 'ask') return <AskRow item={item} onAnswer={onAnswer} />;

  // result — the turn/cost counters were noise between every exchange, so the row
  // now renders only when it has something the user must act on. "success" describes
  // the turn, not the work: a turn where every tool was blocked still ends
  // successful, so say so rather than implying it happened.
  if (!item.denied.length) return null;
  return (
    <div className="text-[11px] text-amber-400 border-t border-deck-border pt-2 mt-2">
      거부됨: {item.denied.join(', ')} — 해당 작업은 실행되지 않았습니다.
    </div>
  );
}

/**
 * Claude asking the user a question.
 *
 * Headless mode cannot prompt: the CLI answers AskUserQuestion itself with "The
 * user did not answer the questions" the moment it's called. But the questions and
 * options ride along in the tool input, so we render them as real buttons and send
 * the pick as the next user turn — which is exactly how Claude expects to be
 * answered ("just tell me which one you want"). Tap instead of typing.
 */
function AskRow({ item, onAnswer }: {
  item: Extract<ChatItem, { kind: 'ask' }>;
  onAnswer: (text: string) => void;
}) {
  const [picked, setPicked] = useState<string[]>([]);

  const answer = (q: AskQuestion, label: string) => {
    if (q.multiSelect) {
      setPicked((p) => (p.includes(label) ? p.filter((x) => x !== label) : [...p, label]));
      return;
    }
    setPicked([label]);
    onAnswer(label);
  };

  return (
    <div className="space-y-2">
      {item.questions.map((q, qi) => (
        <div key={qi} className="border border-deck-accent/40 bg-deck-accent/5 rounded-lg p-3 space-y-2">
          {q.header && <div className="text-[10px] uppercase tracking-wide text-deck-accent">{q.header}</div>}
          <div className="text-sm text-deck-text">{q.question}</div>
          <div className="space-y-1.5">
            {q.options.map((o) => {
              const on = picked.includes(o.label);
              return (
                <button
                  key={o.label}
                  onClick={() => answer(q, o.label)}
                  className={`w-full text-left px-3 py-2 rounded-lg border text-xs ${
                    on ? 'border-deck-accent bg-deck-accent/20 text-deck-text' : 'border-deck-border text-deck-text'
                  }`}
                >
                  <div className="font-medium">{o.label}</div>
                  {o.description && <div className="text-deck-muted mt-0.5">{o.description}</div>}
                </button>
              );
            })}
          </div>
          {q.multiSelect && (
            <button
              onClick={() => { if (picked.length) onAnswer(picked.join(', ')); }}
              disabled={!picked.length}
              className="w-full py-2 rounded-lg bg-deck-accent text-white text-xs font-medium disabled:opacity-40"
            >
              {picked.length ? `${picked.length}개 선택 · 보내기` : '선택하세요'}
            </button>
          )}
        </div>
      ))}
    </div>
  );
}

function ToolRow({ item }: { item: Extract<ChatItem, { kind: 'tool' }> }) {
  const [open, setOpen] = useState(false);
  const summary = toolSummary(item.name, item.input);
  const dot =
    item.status === 'pending' ? 'bg-amber-400 animate-pulse' :
    item.status === 'error' ? 'bg-red-400' : 'bg-green-400';

  return (
    <div className="border border-deck-border rounded-lg overflow-hidden">
      <button onClick={() => setOpen((v) => !v)} className="w-full flex items-center gap-2 px-3 py-2 text-left">
        <span className={`w-1.5 h-1.5 rounded-full shrink-0 ${dot}`} />
        <span className="text-xs font-medium text-deck-text shrink-0">{item.name}</span>
        {item.subagent && <span className="text-[10px] text-deck-muted shrink-0">sub</span>}
        <span className="text-xs text-deck-muted truncate flex-1">{summary}</span>
      </button>
      {open && (
        <div className="px-3 pb-2 space-y-2">
          <pre className="text-[11px] text-deck-muted bg-deck-surface rounded p-2 overflow-x-auto">
            {JSON.stringify(item.input, null, 2)}
          </pre>
          {item.result && (
            <pre className="text-[11px] text-deck-muted bg-deck-surface rounded p-2 overflow-x-auto max-h-48">
              {item.result}
            </pre>
          )}
        </div>
      )}
    </div>
  );
}

/**
 * The permission prompt. This is the thing the terminal track can never do well on
 * a phone: a real button, and — because the CLI blocks on our answer with no
 * timeout — you can leave it sitting here for an hour and answer later.
 */
function ApprovalCard({ req, onDecide }: {
  req: PendingApproval;
  onDecide: (id: string, behavior: 'allow' | 'deny', message?: string) => void;
}) {
  const [reason, setReason] = useState('');
  const [showReason, setShowReason] = useState(false);
  const summary = toolSummary(req.toolName, req.input);

  return (
    <div className="border-t border-amber-400/40 bg-amber-400/10 px-3 py-3 space-y-2">
      <div className="text-xs text-deck-text">
        <span className="font-semibold">{req.toolName}</span> 실행을 요청했습니다
      </div>
      {summary && (
        <pre className="text-[11px] text-deck-muted bg-deck-bg rounded p-2 overflow-x-auto whitespace-pre-wrap break-all">
          {summary}
        </pre>
      )}
      {showReason && (
        <textarea
          value={reason}
          onChange={(e) => setReason(e.target.value)}
          rows={2}
          placeholder="거부 이유 (Claude가 읽고 다른 방법을 찾습니다)"
          className="w-full resize-none bg-deck-bg border border-deck-border rounded px-2 py-1.5 text-xs text-deck-text outline-none"
        />
      )}
      <div className="flex gap-2">
        <button
          onClick={() => onDecide(req.id, 'allow')}
          className="flex-1 py-2.5 rounded-lg bg-green-500/20 text-green-400 text-sm font-medium"
        >
          허용
        </button>
        <button
          onClick={() => {
            // A reason is worth asking for: Claude reads it and adapts, so "not
            // that path, use ./tmp" is a far more useful answer than a bare no.
            if (!showReason) { setShowReason(true); return; }
            onDecide(req.id, 'deny', reason.trim() || undefined);
          }}
          className="flex-1 py-2.5 rounded-lg bg-red-500/20 text-red-400 text-sm font-medium"
        >
          {showReason ? '거부하기' : '거부'}
        </button>
      </div>
    </div>
  );
}
