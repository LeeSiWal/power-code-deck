import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { agentDeckWS } from '../../lib/ws';
import { foldEvents, toolSummary, type ChatItem, type StreamEvent } from '../../lib/nativeEvents';

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

export function NativeChat({ agentId, cwd, model }: NativeChatProps) {
  const [events, setEvents] = useState<StreamEvent[]>([]);
  const [pending, setPending] = useState<PendingApproval[]>([]);
  const [running, setRunning] = useState(false);
  const [draft, setDraft] = useState('');
  const scrollRef = useRef<HTMLDivElement>(null);
  const stickRef = useRef(true);

  const items = useMemo(() => foldEvents(events), [events]);

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

    const open = () => agentDeckWS.send('native:open', { agentId, cwd, model: model ?? '' });
    open();
    const offOpen = agentDeckWS.on('open', open); // re-open after a reconnect

    return () => { offEvent(); offHistory(); offApproval(); offState(); offOpen(); };
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

  const send = useCallback(() => {
    const text = draft.trim();
    if (!text) return;
    agentDeckWS.send('native:input', { agentId, text });
    // Echo locally: the CLI does not emit our own turn back, so without this the
    // message would vanish until Claude replies.
    setEvents((prev) => [...prev, { type: 'user', message: { role: 'user', content: [{ type: 'text', text }] } }]);
    setDraft('');
  }, [agentId, draft]);

  const decide = useCallback((id: string, behavior: 'allow' | 'deny', message?: string) => {
    agentDeckWS.send('native:decide', { agentId, id, behavior, message });
    setPending((prev) => prev.filter((p) => p.id !== id));
  }, [agentId]);

  return (
    <div className="flex flex-col h-full bg-deck-bg">
      <div ref={scrollRef} onScroll={onScroll} className="flex-1 overflow-y-auto px-3 py-3 space-y-2">
        {items.map((item) => <ChatRow key={`${item.kind}-${item.id}`} item={item} />)}
        {!items.length && (
          <div className="text-deck-muted text-sm py-8 text-center">
            {running ? '세션 시작 중…' : '메시지를 보내 대화를 시작하세요.'}
          </div>
        )}
      </div>

      {pending.map((p) => <ApprovalCard key={p.id} req={p} onDecide={decide} />)}

      <div className="flex gap-2 p-2 border-t border-deck-border safe-bottom">
        <textarea
          value={draft}
          onChange={(e) => setDraft(e.target.value)}
          onKeyDown={(e) => {
            // Enter sends; Shift+Enter is a newline. On mobile the soft keyboard's
            // return arrives as a plain Enter, which is what we want here.
            if (e.key === 'Enter' && !e.shiftKey && !e.nativeEvent.isComposing) {
              e.preventDefault();
              send();
            }
          }}
          rows={1}
          placeholder="Claude에게 메시지…"
          className="flex-1 resize-none bg-deck-surface border border-deck-border rounded-lg px-3 py-2 text-sm text-deck-text outline-none focus:border-deck-accent"
        />
        <button
          onClick={send}
          disabled={!draft.trim()}
          className="px-4 rounded-lg bg-deck-accent text-white text-sm font-medium disabled:opacity-40"
        >
          보내기
        </button>
      </div>
    </div>
  );
}

function ChatRow({ item }: { item: ChatItem }) {
  if (item.kind === 'session') {
    return (
      <div className="text-[11px] text-deck-muted border border-deck-border rounded-lg px-3 py-2">
        <div>{item.model} · v{item.version}</div>
        <div className="truncate">{item.cwd}</div>
        {/* Without our bridge the CLI denies every gated tool AND still calls the
            turn a success. That silence is exactly what we must not reproduce. */}
        {!item.bridgeOk && (
          <div className="text-red-400 mt-1">
            ⚠ 승인 브리지가 연결되지 않았습니다 — 권한이 필요한 도구가 전부 자동 거부됩니다.
          </div>
        )}
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

  // result
  return (
    <div className="text-[11px] text-deck-muted border-t border-deck-border pt-2 mt-2">
      {/* "success" describes the turn, not the work: a turn where every tool was
          blocked still ends successful. Say so rather than implying it happened. */}
      {item.denied.length > 0 && (
        <div className="text-amber-400">거부됨: {item.denied.join(', ')} — 해당 작업은 실행되지 않았습니다.</div>
      )}
      <div>
        턴 {item.turns ?? '?'}
        {item.costUsd != null && ` · $${item.costUsd.toFixed(4)}`}
      </div>
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
