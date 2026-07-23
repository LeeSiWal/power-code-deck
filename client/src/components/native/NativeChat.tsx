import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { agentDeckWS } from '../../lib/ws';
import { api } from '../../lib/api';
import { foldEvents, isTurnActive, toolSummary, type AskQuestion, type ChatItem, type StreamEvent } from '../../lib/nativeEvents';
import {
  IconBolt, IconCheck, IconClose, IconCodeSlash, IconCopy, IconHand, IconPaperclip,
  IconPlanMap, IconPlus, IconSpinner, IconUpload, IconWarning, type IconProps,
} from '../icons';
import { writeClipboard } from '../../lib/clipboard';

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
  driver?: 'claude' | 'codex';
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

const CODEX_MODELS: typeof MODELS = [
  { id: '', label: 'Auto', desc: 'Codex 기본 모델' },
  { id: 'gpt-5.4', label: 'GPT-5.4', desc: '복잡한 코딩 작업' },
  { id: 'gpt-5.3-codex', label: 'GPT-5.3 Codex', desc: 'Codex 최적화 모델' },
  { id: 'gpt-5.3-codex-spark', label: 'Codex Spark', desc: '빠른 코딩 작업' },
];

// Permission modes — the TUI's Shift+Tab cycle. `id` → --permission-mode.
// Switching restarts the session on the same conversation (server SetMode).
// `pill` encodes risk in colour: neutral → indigo → sky (plan) → amber (careful).
// NOTE: "자동 (안전 검사)" (id 'auto') is NOT a CLI --permission-mode — the CLI has no
// such flag (the VS Code extension's version is extension-only). We implement it
// SERVER-SIDE: the CLI runs in default, every gated tool routes to our approve bridge,
// and the broker auto-approves safe calls / asks for risky ones. 전체 허용 is
// bypassPermissions and approves EVERYTHING (also enforced server-side, since the CLI
// keeps asking through our approve tool despite the flag).
const MODES: { id: string; label: string; desc: string; icon: React.ComponentType<IconProps>; pill: string }[] = [
  { id: '', label: '수동', desc: '도구를 실행할 때마다 승인을 요청합니다', icon: IconHand, pill: 'border-deck-border bg-deck-surface text-deck-text-dim' },
  { id: 'acceptEdits', label: '자동 편집', desc: '파일 편집은 자동 승인, 명령 실행은 물어봅니다', icon: IconCodeSlash, pill: 'border-deck-accent/50 bg-deck-accent/10 text-deck-accent-light' },
  { id: 'auto', label: '자동 (안전 검사)', desc: '안전한 작업은 자동 승인, 위험한 명령만 물어봅니다', icon: IconCheck, pill: 'border-emerald-400/45 bg-emerald-400/10 text-emerald-300' },
  { id: 'plan', label: '플랜', desc: '실행 없이 코드를 탐색하고 계획을 먼저 제시합니다', icon: IconPlanMap, pill: 'border-sky-400/40 bg-sky-400/10 text-sky-300' },
  { id: 'bypassPermissions', label: '전체 허용', desc: '모든 도구를 묻지 않고 승인합니다 — 주의해서 사용', icon: IconBolt, pill: 'border-amber-400/45 bg-amber-400/10 text-amber-300' },
];

export function NativeChat({ agentId, cwd, model, driver = 'claude' }: NativeChatProps) {
  const navigate = useNavigate(); // /clear swaps to a freshly created session
  const [events, setEvents] = useState<StreamEvent[]>([]);
  const [pending, setPending] = useState<PendingApproval[]>([]);
  const [running, setRunning] = useState(false);
  const [error, setError] = useState('');
  const [openingSetup, setOpeningSetup] = useState(false);
  const [draft, setDraft] = useState('');
  const [attachments, setAttachments] = useState<{ name: string; path: string }[]>([]);
  const [uploading, setUploading] = useState(false);
  // Optimistic "the agent is on it" flag. `busy` (below) only turns true once the
  // server has echoed your turn back — a WS round-trip you can feel on a phone. This
  // flips the instant you hit send, so the composer shows motion immediately instead
  // of a dead pause. Cleared when the real turn takes over or ends (see effect).
  const [justSent, setJustSent] = useState(false);
  const sentTimer = useRef<number | null>(null);
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

  const models = driver === 'codex' ? CODEX_MODELS : MODELS;
  const modelLabel = models.find((m) => m.id === modelId)?.label ?? modelId ?? 'Auto';
  const currentMode = MODES.find((m) => m.id === modeId) ?? MODES[0];

  // A missing CLI is recoverable from inside the deck. Open a real PTY because
  // both installers and OAuth login are interactive (especially on WSL/SSH where
  // the browser returns a code that must be pasted back into the terminal).
  const cliMissing = /executable file not found|not found in \$PATH|no such file or directory/i.test(error)
    && error.toLowerCase().includes(driver);
  const openSetup = useCallback(async () => {
    setOpeningSetup(true);
    try {
      const isCodex = driver === 'codex';
      const binary = isCodex ? 'codex' : 'claude';
      const installUrl = isCodex ? 'https://chatgpt.com/codex/install.sh' : 'https://claude.ai/install.sh';
      const installShell = isCodex ? 'sh' : 'bash';
      const loginCommand = isCodex ? 'codex login' : 'claude auth login';
      const label = isCodex ? 'Codex' : 'Claude Code';
      const script = [
        'set -e',
        'export PATH="$HOME/.local/bin:$PATH"',
        `if ! command -v ${binary} >/dev/null 2>&1; then`,
        `  printf '\\n${label} CLI를 설치합니다...\\n\\n'`,
        "  command -v curl >/dev/null 2>&1 || { echo 'curl이 필요합니다. 먼저 curl을 설치해주세요.'; exit 1; }",
        `  curl -fsSL ${installUrl} | ${installShell}`,
        '  export PATH="$HOME/.local/bin:$PATH"',
        'fi',
        `printf '\\n${label} 로그인을 시작합니다. 브라우저 인증 후 표시되는 코드를 이 터미널에 붙여넣으세요.\\n\\n'`,
        loginCommand,
        `printf '\\n설정이 완료되었습니다. 브라우저의 뒤로 가기로 원래 ${label} 세션에 돌아가세요.\\n'`,
        'exec "${SHELL:-/bin/bash}" -l',
      ].join('\n');
      const a = await api.createAgent({
        preset: 'custom',
        name: `${label} 설치 및 로그인`,
        workingDir: cwd,
        command: '/bin/bash',
        args: ['-lc', script],
      }) as { id: string };
      navigate(`/agents/${a.id}`);
    } catch (err) {
      setError('설치 터미널을 열지 못했습니다: ' + String(err));
    } finally {
      setOpeningSetup(false);
    }
  }, [cwd, driver, navigate]);

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

  // Everything you've sent, oldest first — the composer's ↑ history. Derived from
  // the conversation rather than tracked separately, so it is already correct after
  // a reconnect or a resume (the server's history is the source of truth).
  const sentHistory = useMemo(() => items.flatMap((i) => (i.kind === 'user' ? [i.text] : [])), [items]);
  // null = not browsing. While browsing, the draft the user had typed is parked in
  // draftBeforeHist so ↓ past the newest entry can put it back.
  const [histIdx, setHistIdx] = useState<number | null>(null);
  const draftBeforeHist = useRef('');

  // What this session can actually invoke: verified built-ins, the user's and the
  // project's .claude/ definitions, and enabled plugins. The server decides what
  // qualifies — every entry there was probed against the real CLI first.
  const [cmds, setCmds] = useState<{ name: string; type: string; description?: string; scope?: string }[]>([]);
  const [cmdIdx, setCmdIdx] = useState(0);
  const [cmdDismissed, setCmdDismissed] = useState(false);
  useEffect(() => {
    api.slashCommands(agentId).then(setCmds).catch(() => { /* no picker is fine */ });
  }, [agentId]);

  // Offer completions only while the draft IS the token — "/dep", "@rev". A space
  // means arguments have started and the choice is already made. The colon is part
  // of the class because plugin entries are namespaced ("/newton:mission") and are
  // only reachable under that name.
  const cmdToken = /^[/@][\w:-]*$/.test(draft) ? draft : null;
  const cmdMatches = useMemo(
    () => (cmdToken ? cmds.filter((c) => c.name.startsWith(cmdToken)).slice(0, 8) : []),
    [cmdToken, cmds],
  );
  const cmdOpen = !cmdDismissed && cmdMatches.length > 0;
  const acceptCmd = (name: string) => {
    setDraft(name + ' '); // leave the caret past a space, ready for arguments
    setCmdDismissed(true);
    taRef.current?.focus();
  };
  // Derived from the events, not tracked separately, so it survives a reconnect:
  // the history alone says whether a turn is still in flight.
  const busy = useMemo(() => isTurnActive(events), [events]);

  // Hand the optimistic flag over to the real `busy` signal: once the turn is
  // actually in flight the safety timer is moot, and when it ends (or never began)
  // the optimistic flag must drop so the indicator doesn't linger.
  useEffect(() => {
    if (busy) {
      if (sentTimer.current) { clearTimeout(sentTimer.current); sentTimer.current = null; }
    } else {
      setJustSent(false);
    }
  }, [busy]);
  // The composer's "working" indicator: true the instant you send, and for the whole
  // turn thereafter.
  const working = busy || justSent;

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

    const open = () => agentDeckWS.send('native:open', { agentId, driver, cwd, model: modelIdRef.current, mode: modeIdRef.current });
    open();
    const offOpen = agentDeckWS.on('open', open); // re-open after a reconnect

    return () => { offEvent(); offHistory(); offApproval(); offState(); offError(); offOpen(); };
  }, [agentId, cwd, model, driver]);

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
    // Show motion at once — don't wait for the server to echo the turn back. A
    // safety timeout drops the flag if no real turn ever materialises (e.g. a line
    // the CLI answers without a turn), so the indicator can't get stuck on.
    setJustSent(true);
    if (sentTimer.current) clearTimeout(sentTimer.current);
    sentTimer.current = window.setTimeout(() => setJustSent(false), 8000);
  }, [agentId]);

  const interrupt = useCallback(() => {
    agentDeckWS.send('native:interrupt', { agentId });
  }, [agentId]);

  const send = useCallback(async () => {
    const text = draft.trim();
    if (!text && !attachments.length) return;
    // /clear starts a genuinely new session instead of being forwarded. Sent to the
    // CLI it drops the context but leaves the transcript on screen, so the chat looks
    // intact while Claude has forgotten every word of it — the worst kind of wrong
    // screen. A fresh session clears both at once.
    if (text === '/clear' && !attachments.length) {
      setDraft('');
      setHistIdx(null);
      try {
        const a = (await api.newSession(agentId)) as { id: string };
        navigate(`/agents/${a.id}`);
      } catch (err) {
        setError('새 세션을 시작하지 못했습니다: ' + String(err));
      }
      return;
    }
    // Attachments ride along as paths inside the project — Claude opens them with
    // its Read tool. Sent as part of the same user turn.
    const msg = attachments.length
      ? (text ? text + '\n\n' : '') + '첨부 파일 (Read 도구로 확인해줘):\n' + attachments.map((a) => a.path).join('\n')
      : text;
    sendText(msg);
    setAttachments([]);
    setHistIdx(null); // sending ends history browsing; the next ↑ starts from the newest
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
          <div className="flex-1 min-w-0">
            <div>{error}</div>
            {cliMissing && (
              <button
                onClick={openSetup}
                disabled={openingSetup}
                className="mt-2 px-3 py-1.5 rounded-md bg-deck-accent text-white disabled:opacity-50"
              >
                {openingSetup ? '설치 터미널 여는 중…' : `${driver === 'codex' ? 'Codex' : 'Claude Code'} 설치 및 로그인`}
              </button>
            )}
          </div>
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
              <IconUpload size={15} className="text-deck-text-dim" /> 컴퓨터에서 업로드
            </button>
          </div>
        )}

        {/* Model switcher menu. */}
        {menu === 'model' && (
          <div className="absolute bottom-14 right-2 z-20 w-64 max-w-[calc(100vw-1rem)] bg-deck-raised border border-deck-border rounded-lg shadow-xl overflow-hidden">
            <div className="px-3 py-1.5 text-[10px] uppercase tracking-wide text-deck-text-dim">모델</div>
            {models.map((m) => (
              <button
                key={m.id}
                onClick={() => pickModel(m.id)}
                className={`w-full text-left px-3 py-2 hover:bg-deck-bg/60 flex items-start gap-2 ${m.id === modelId ? 'bg-deck-bg/40' : ''}`}
              >
                <span className={`mt-0.5 shrink-0 w-3.5 ${m.id === modelId ? 'text-deck-accent' : 'text-transparent'}`}><IconCheck size={14} /></span>
                <span className="min-w-0">
                  <span className={`block text-sm ${m.id === modelId ? 'text-deck-accent' : 'text-deck-text'}`}>{m.label}</span>
                  <span className="block text-xs text-deck-text-dim truncate">{m.desc}</span>
                </span>
              </button>
            ))}
          </div>
        )}

        {/* Permission-mode menu (also cycled by Shift+Tab) — the VS Code extension's
            Modes panel, rebuilt for the deck: icon · name · full description per row,
            check on the active one, and the shortcut spelled out in the header. */}
        {menu === 'mode' && (
          <div className="absolute bottom-14 right-2 z-20 w-80 max-w-[calc(100vw-1rem)] bg-deck-raised border border-deck-border rounded-xl shadow-xl overflow-hidden p-1.5">
            <div className="flex items-center justify-between px-2.5 pt-1 pb-2">
              <span className="text-sm text-deck-text-dim">권한 모드</span>
              <span className="flex items-center gap-1 text-[10px] text-deck-text-dim">
                <kbd className="px-1 py-0.5 rounded border border-deck-border bg-deck-surface">⇧</kbd>
                +
                <kbd className="px-1 py-0.5 rounded border border-deck-border bg-deck-surface">tab</kbd>
                전환
              </span>
            </div>
            {MODES.map((m) => {
              const on = m.id === modeId;
              return (
                <button
                  key={m.id}
                  onClick={() => pickMode(m.id)}
                  className={`w-full text-left px-2.5 py-2.5 rounded-lg flex items-start gap-3 ${
                    on ? 'bg-deck-accent/25' : 'hover:bg-deck-bg/60'
                  }`}
                >
                  <span className={`shrink-0 w-6 flex items-center justify-center h-6 ${
                    m.id === 'bypassPermissions' ? 'text-amber-300' : 'text-deck-text-dim'
                  }`}><m.icon size={17} /></span>
                  <span className="min-w-0 flex-1">
                    <span className={`block text-sm font-medium ${
                      m.id === 'bypassPermissions' ? 'text-amber-300' : 'text-deck-text'
                    }`}>{m.label}</span>
                    <span className="block text-xs text-deck-text-dim leading-snug">{m.desc}</span>
                  </span>
                  {on && <span className="shrink-0 text-deck-accent-light flex items-center h-6"><IconCheck size={15} /></span>}
                </button>
              );
            })}
          </div>
        )}

        {attachments.length > 0 && (
          <div className="flex flex-wrap gap-1.5 px-2 pt-2">
            {attachments.map((a, i) => (
              <span key={i} className="flex items-center gap-1 bg-deck-surface border border-deck-border rounded px-2 py-1 text-xs text-deck-text max-w-[70%]">
                <IconPaperclip size={12} className="shrink-0 text-deck-text-dim" />
                <span className="truncate">{a.name}</span>
                <button
                  onClick={() => setAttachments((prev) => prev.filter((_, j) => j !== i))}
                  className="opacity-60 shrink-0"
                  title="첨부 제거"
                >
                  <IconClose size={12} />
                </button>
              </span>
            ))}
          </div>
        )}

        {cmdOpen && (
          <div className="mx-2 mb-1 rounded-lg border border-deck-border bg-deck-raised overflow-hidden">
            {cmdMatches.map((c, i) => (
              <button
                key={c.name}
                // pointerDown, not click: the textarea's blur would otherwise fire
                // first and the list could unmount before the click landed.
                onPointerDown={(e) => { e.preventDefault(); acceptCmd(c.name); }}
                onMouseEnter={() => setCmdIdx(i)}
                className={`w-full text-left px-3 py-1.5 flex items-baseline gap-2 ${
                  i === Math.min(cmdIdx, cmdMatches.length - 1) ? 'bg-deck-bg/60' : ''
                }`}
              >
                <span className="font-mono text-sm text-deck-accent shrink-0">{c.name}</span>
                {c.description && (
                  <span className="text-xs text-deck-text-dim truncate">{c.description}</span>
                )}
                <span className="ml-auto shrink-0 text-[10px] text-deck-text-faint">
                  {c.scope === 'builtin'
                    ? '내장'
                    : c.scope === 'plugin'
                    ? `플러그인${c.type === 'skill' ? '·스킬' : ''}`
                    : c.scope === 'project'
                      ? '프로젝트'
                      : c.type === 'agent'
                        ? '에이전트'
                        : c.type === 'skill'
                          ? '스킬'
                          : '사용자'}
                </span>
              </button>
            ))}
          </div>
        )}

        {/* Immediate "agent is working" feedback, right above the composer. Appears
            the moment you send (justSent) and stays through the turn (busy), so the
            input never looks like it swallowed your message with no response. */}
        {working && (
          <div className="mx-2 mt-2 flex items-center gap-2 px-3 py-1.5 rounded-lg bg-deck-accent/10 border border-deck-accent/20 text-deck-accent-light text-xs overflow-hidden">
            <IconSpinner size={13} className="animate-spin shrink-0" />
            <span className="shrink-0">에이전트가 작업 중…</span>
            <span className="relative ml-1 flex-1 h-0.5 rounded-full bg-deck-accent/15 overflow-hidden">
              <span className="absolute inset-y-0 left-0 w-1/3 rounded-full bg-deck-accent/60 animate-working-bar" />
            </span>
          </div>
        )}

        <div className="p-2 space-y-2">
          <input ref={fileRef} type="file" multiple className="hidden" onChange={onFilePick} />
          <textarea
            ref={taRef}
            value={draft}
            onChange={(e) => {
              setDraft(e.target.value);
              // Typing means you've left the recalled message behind; the next ↑
              // should start again from the newest entry, not resume mid-walk.
              if (histIdx !== null) setHistIdx(null);
              // A new keystroke re-opens the picker that Esc dismissed, and puts the
              // highlight back on the best match.
              setCmdDismissed(false);
              setCmdIdx(0);
            }}
            onKeyDown={(e) => {
              // The command picker owns the arrows / Enter / Tab / Esc while it is
              // open, so it must be checked before history recall and send — those
              // would otherwise swallow the same keys.
              if (cmdOpen) {
                if (e.key === 'ArrowDown') {
                  e.preventDefault();
                  setCmdIdx((i) => (i + 1) % cmdMatches.length);
                  return;
                }
                if (e.key === 'ArrowUp') {
                  e.preventDefault();
                  setCmdIdx((i) => (i - 1 + cmdMatches.length) % cmdMatches.length);
                  return;
                }
                if ((e.key === 'Enter' || e.key === 'Tab') && !e.shiftKey && !e.nativeEvent.isComposing) {
                  e.preventDefault();
                  acceptCmd(cmdMatches[Math.min(cmdIdx, cmdMatches.length - 1)].name);
                  return;
                }
                if (e.key === 'Escape') {
                  e.preventDefault();
                  setCmdDismissed(true); // dismiss the list, keep what was typed
                  return;
                }
              }
              // Shift+Tab cycles the permission mode, like the Claude Code TUI —
              // otherwise Tab would just move focus out of the box.
              if (e.key === 'Tab' && e.shiftKey) {
                e.preventDefault();
                cycleMode();
                return;
              }
              // ↑ / ↓ recall what you sent before — the terminal key bar's arrows,
              // which had no native equivalent. Only from the very start of the box,
              // so ↑ still moves the caret inside a multi-line draft.
              if (e.key === 'ArrowUp' && sentHistory.length) {
                const ta = e.currentTarget;
                if (histIdx !== null || (ta.selectionStart === 0 && ta.selectionEnd === 0)) {
                  e.preventDefault();
                  if (histIdx === null) draftBeforeHist.current = draft;
                  const next = histIdx === null ? sentHistory.length - 1 : Math.max(0, histIdx - 1);
                  setHistIdx(next);
                  setDraft(sentHistory[next]);
                  return;
                }
              }
              if (e.key === 'ArrowDown' && histIdx !== null) {
                e.preventDefault();
                const next = histIdx + 1;
                if (next >= sentHistory.length) {
                  setHistIdx(null);
                  setDraft(draftBeforeHist.current); // hand back the draft ↑ interrupted
                } else {
                  setHistIdx(next);
                  setDraft(sentHistory[next]);
                }
                return;
              }
              // Esc — the TUI's interrupt, now reachable from the keyboard as well as
              // the 중단 button. While browsing history it backs out first, so Esc
              // never stops a turn you were only scrolling past.
              if (e.key === 'Escape') {
                if (histIdx !== null) {
                  e.preventDefault();
                  setHistIdx(null);
                  setDraft(draftBeforeHist.current);
                  return;
                }
                if (busy) {
                  e.preventDefault();
                  interrupt();
                  return;
                }
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
              className="shrink-0 w-8 h-8 rounded-lg bg-deck-surface border border-deck-border text-deck-text-dim flex items-center justify-center disabled:opacity-40"
              title="추가"
            >
              {uploading ? <IconSpinner size={15} className="animate-spin" /> : <IconPlus size={15} />}
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
              className={`shrink-0 h-8 px-2.5 rounded-full border text-xs font-medium flex items-center gap-1.5 ${currentMode.pill} ${
                menu === 'mode' ? 'ring-1 ring-deck-accent' : ''
              }`}
              title="권한 모드 전환 (Shift+Tab)"
            >
              <currentMode.icon size={13} />
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
      <div className="text-[11px] text-red-400 border border-red-400/40 bg-red-400/5 rounded-lg px-3 py-2 flex items-center gap-1.5">
        <IconWarning size={13} className="shrink-0" />
        승인 브리지가 연결되지 않았습니다 — 권한이 필요한 도구가 전부 자동 거부됩니다.
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
    return <AssistantText text={item.text} />;
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
 * answered ("just tell me which one you want").
 *
 * Selecting and sending are two separate acts: a tap only highlights, and one
 * explicit 보내기 button submits every question's pick at once. Tap-to-send felt
 * fast but a phone thumb has no undo — the answer left before you could read it.
 */
function AskRow({ item, onAnswer }: {
  item: Extract<ChatItem, { kind: 'ask' }>;
  onAnswer: (text: string) => void;
}) {
  // Per-question selections, keyed by question index. State lives here (the row is
  // keyed by the stable tool_use id) so it survives history re-folds.
  const [picked, setPicked] = useState<Record<number, string[]>>({});
  // Free-text "기타" per question — the answer Claude's fixed options didn't cover.
  // Always available, mirroring how AskUserQuestion always offers an "Other".
  const [custom, setCustom] = useState<Record<number, string>>({});
  const [sent, setSent] = useState('');

  const toggle = (qi: number, q: AskQuestion, label: string) => {
    setPicked((p) => {
      const cur = p[qi] ?? [];
      if (q.multiSelect) {
        return { ...p, [qi]: cur.includes(label) ? cur.filter((x) => x !== label) : [...cur, label] };
      }
      // Single select: tapping the picked option again unpicks it.
      return { ...p, [qi]: cur[0] === label ? [] : [label] };
    });
    // Single select: a preset and the free-text field are mutually exclusive, so
    // choosing an option clears whatever was typed.
    if (!q.multiSelect) setCustom((c) => ({ ...c, [qi]: '' }));
  };

  const setCustomText = (qi: number, q: AskQuestion, text: string) => {
    setCustom((c) => ({ ...c, [qi]: text }));
    // Single select: typing overrides any picked option (they can't both win).
    if (!q.multiSelect && text.trim()) setPicked((p) => ({ ...p, [qi]: [] }));
  };

  // The effective answer for a question: preset picks plus (multi) or instead of
  // (single) the free-text entry.
  const answersFor = (qi: number, q: AskQuestion): string[] => {
    const presets = picked[qi] ?? [];
    const c = (custom[qi] ?? '').trim();
    if (q.multiSelect) return c ? [...presets, c] : presets;
    return c ? [c] : presets;
  };

  const complete = item.questions.every((q, qi) => answersFor(qi, q).length > 0);
  const submit = () => {
    if (!complete || sent) return;
    // One question → just the label(s); several → prefix each with its header so
    // Claude can tell which answer belongs to which question.
    const answer = item.questions
      .map((q, qi) => {
        const sel = answersFor(qi, q).join(', ');
        return item.questions.length > 1 ? `${q.header || q.question}: ${sel}` : sel;
      })
      .join('\n');
    onAnswer(answer);
    setSent(answer);
  };

  return (
    <div className="space-y-2">
      {item.questions.map((q, qi) => (
        <div key={qi} className="border border-deck-accent/40 bg-deck-accent/5 rounded-lg p-3 space-y-2">
          {q.header && <div className="text-[10px] uppercase tracking-wide text-deck-accent">{q.header}</div>}
          <div className="text-sm text-deck-text">{q.question}</div>
          <div className="space-y-1.5">
            {q.options.map((o) => {
              const on = answersFor(qi, q).includes(o.label);
              return (
                <button
                  key={o.label}
                  onClick={() => { if (!sent) toggle(qi, q, o.label); }}
                  disabled={!!sent}
                  className={`w-full text-left px-3 py-2 rounded-lg border text-xs disabled:opacity-60 ${
                    on ? 'border-deck-accent bg-deck-accent/20 text-deck-text' : 'border-deck-border text-deck-text'
                  }`}
                >
                  <div className="font-medium flex items-center gap-1.5">
                    {on && <IconCheck size={12} className="shrink-0 text-deck-accent-light" />}
                    {o.label}
                  </div>
                  {o.description && <div className="text-deck-muted mt-0.5">{o.description}</div>}
                </button>
              );
            })}
            {/* Always-present free-text escape hatch: none of the options fit, so say
                it in your own words. Highlights like a selected option when filled. */}
            <div
              className={`rounded-lg border text-xs ${
                (custom[qi] ?? '').trim() ? 'border-deck-accent bg-deck-accent/20' : 'border-deck-border'
              }`}
            >
              <input
                type="text"
                value={custom[qi] ?? ''}
                onChange={(e) => setCustomText(qi, q, e.target.value)}
                onKeyDown={(e) => {
                  if (e.key === 'Enter' && !e.nativeEvent.isComposing && complete) {
                    e.preventDefault();
                    submit();
                  }
                }}
                disabled={!!sent}
                placeholder="기타 — 직접 입력…"
                className="w-full bg-transparent px-3 py-2 text-deck-text outline-none placeholder:text-deck-muted disabled:opacity-60"
              />
            </div>
          </div>
        </div>
      ))}
      {sent ? (
        <div className="text-[11px] text-deck-muted px-1">보냄: {sent}</div>
      ) : (
        <button
          onClick={submit}
          disabled={!complete}
          className="w-full py-2 rounded-lg bg-deck-accent text-white text-xs font-medium disabled:opacity-40"
        >
          {complete ? '선택 보내기' : '항목을 선택하세요'}
        </button>
      )}
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
        // Cap the height and scroll inside: a long Bash command must never push the
        // 허용/거부 buttons off-screen (unreachable on a phone). Buttons stay pinned
        // right below this box no matter how long the command is.
        <pre className="text-[11px] text-deck-muted bg-deck-bg rounded p-2 overflow-auto max-h-40 whitespace-pre-wrap break-all">
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

// ── Assistant text rendering ────────────────────────────────────────────────
// The stream gives us plain text (no markdown pass), so fenced code blocks were
// shown as literal ``` text with nothing to copy, and URLs weren't clickable. This
// splits the text into prose + fenced code, gives each code block a one-tap copy
// button (commands/snippets you'd otherwise retype), and turns URLs into links.

type TextSeg = { type: 'text'; content: string } | { type: 'code'; content: string; lang?: string };

function parseSegments(text: string): TextSeg[] {
  const segs: TextSeg[] = [];
  const re = /```([^\n`]*)\n?([\s\S]*?)```/g;
  let last = 0;
  let m: RegExpExecArray | null;
  while ((m = re.exec(text)) !== null) {
    if (m.index > last) segs.push({ type: 'text', content: text.slice(last, m.index) });
    segs.push({ type: 'code', lang: m[1].trim() || undefined, content: m[2].replace(/\n$/, '') });
    last = m.index + m[0].length;
  }
  const rest = text.slice(last);
  // A still-streaming, not-yet-closed fence: render its body as code so it doesn't
  // flash as raw ``` mid-stream, then re-settle once the closing fence arrives.
  const open = rest.indexOf('```');
  if (open !== -1) {
    if (open > 0) segs.push({ type: 'text', content: rest.slice(0, open) });
    const after = rest.slice(open + 3);
    const nl = after.indexOf('\n');
    const lang = (nl === -1 ? after : after.slice(0, nl)).trim();
    const body = nl === -1 ? '' : after.slice(nl + 1);
    segs.push({ type: 'code', lang: lang || undefined, content: body });
  } else if (rest.length) {
    segs.push({ type: 'text', content: rest });
  }
  return segs;
}

// Split on http(s) URLs; the trailing char class avoids swallowing a sentence's
// closing punctuation into the link.
const URL_SPLIT = /(https?:\/\/[^\s<>()]+[^\s<>().,;:!?'"\]])/g;

function Linkified({ text }: { text: string }) {
  return (
    <>
      {text.split(URL_SPLIT).map((part, i) =>
        /^https?:\/\//.test(part) ? (
          <a
            key={i}
            href={part}
            target="_blank"
            rel="noopener noreferrer"
            className="text-deck-accent-light underline decoration-deck-accent/40 underline-offset-2 break-all"
          >
            {part}
          </a>
        ) : (
          part
        ),
      )}
    </>
  );
}

function CodeBlock({ code, lang }: { code: string; lang?: string }) {
  const [copied, setCopied] = useState(false);
  const copy = async () => {
    if (await writeClipboard(code)) {
      setCopied(true);
      window.setTimeout(() => setCopied(false), 1500);
    }
  };
  return (
    <div className="my-1.5 rounded-lg border border-deck-border overflow-hidden bg-deck-bg">
      <div className="flex items-center justify-between px-2.5 py-1 bg-deck-surface border-b border-deck-border">
        <span className="text-[10px] font-mono text-deck-text-faint">{lang || 'code'}</span>
        <button
          onClick={copy}
          className="flex items-center gap-1 text-[10px] font-mono text-deck-text-dim active:opacity-70 px-1"
        >
          {copied ? (
            <>
              <IconCheck size={11} /> 복사됨
            </>
          ) : (
            <>
              <IconCopy size={11} /> 복사
            </>
          )}
        </button>
      </div>
      <pre className="text-[12px] leading-relaxed font-mono text-deck-text p-2.5 overflow-x-auto selectable">
        <code>{code}</code>
      </pre>
    </div>
  );
}

function AssistantText({ text }: { text: string }) {
  const segs = useMemo(() => parseSegments(text), [text]);
  return (
    <div className="max-w-[95%] text-deck-text text-sm break-words px-1">
      {segs.map((seg, i) =>
        seg.type === 'code' ? (
          <CodeBlock key={i} code={seg.content} lang={seg.lang} />
        ) : (
          <span key={i} className="whitespace-pre-wrap">
            <Linkified text={seg.content} />
          </span>
        ),
      )}
    </div>
  );
}
