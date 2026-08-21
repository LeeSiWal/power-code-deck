import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { agentDeckWS } from '../../lib/ws';
import {
  api, ApiError, isLocalPhaseRunning, type IntelligenceMode, type IntelligenceTrace,
  type LocalProvider,
} from '../../lib/api';
import { foldEvents, isTurnActive, toolSummary, type AskQuestion, type ChatItem, type StreamEvent } from '../../lib/nativeEvents';
import {
  IconBolt, IconCheck, IconClose, IconCodeSlash, IconCopy, IconDevices, IconGauge, IconHand,
  IconPaperclip, IconPlanMap, IconPlus, IconSliders, IconSpinner, IconUpload, IconWarning,
  type IconProps,
} from '../icons';
import {
  EMPTY_OPTIONS, hasSessionOptions, parseSessionOptions, SessionOptionsSheet, type SessionOptions,
} from './SessionOptionsSheet';
import { writeClipboard } from '../../lib/clipboard';
import { useAppStore, type ActivityTodo } from '../../stores/appStore';
import { PluginsPanel } from './PluginsPanel';
import { SessionSavingsSummary } from '../intelligence/SessionSavingsSummary';
import { ExecutionModeControl } from '../intelligence/ExecutionModeControl';
import {
  clientCommand, cloudTargetName, routeNativeTask, type LocalOperation,
} from '../intelligence/executionRouting';
import { cloudFailureCode, localFailureCode } from '../intelligence/savings';

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
  // canRemember·rememberTarget: 서버가 판정해 실어 보낸 값. optional — 낡은
  // 서버나 캐시된 상태에는 없을 수 있고, 없으면 버튼을 감추는(안전한) 쪽으로 떨어진다.
  canRemember?: boolean;
  rememberTarget?: string;
}

interface NativeChatProps {
  agentId: string;
  cwd: string;
  model?: string;
  driver?: 'claude' | 'codex';
}

function savedIntelligenceMode(agentId: string): IntelligenceMode {
  const value = localStorage.getItem(`pcd:intelligence-mode:${agentId}`);
  return value === 'LOCAL_PREPROCESS_CLOUD' || value === 'LOCAL_ONLY' ? value : 'CLOUD_ONLY';
}

function savedLocalOperation(agentId: string): LocalOperation {
  const value = localStorage.getItem(`pcd:intelligence-operation:${agentId}`);
  return value === 'summarize' || value === 'explain' || value === 'classify'
    || value === 'log_analysis' || value === 'repository_question' ? value : 'repository_question';
}

function traceFromApiError(error: unknown): IntelligenceTrace | undefined {
  if (!(error instanceof ApiError) || !error.data || typeof error.data !== 'object') return undefined;
  return (error.data as { trace?: IntelligenceTrace }).trace;
}

function localErrorLabel(code?: string): string {
  const labels: Record<string, string> = {
    LOCAL_PROVIDER_UNREACHABLE: 'Local provider is unreachable.',
    LOCAL_MODEL_UNAVAILABLE: 'The configured local model is unavailable.',
    LOCAL_TIMEOUT: 'Local preprocessing timed out.',
    LOCAL_REQUEST_CANCELED: 'Local preprocessing was stopped.',
    LOCAL_GENERATION_FAILED: 'Local context generation failed.',
    CONTEXT_BUILD_FAILED: 'Repository context could not be prepared.',
    CLOUD_EXECUTION_FAILED: 'Cloud fallback could not start.',
    NATIVE_SESSION_NOT_READY: 'The native cloud session is not ready.',
    VALIDATION_FAILED: 'Local Intelligence rejected this task.',
  };
  return code ? labels[code] || code : 'Local Intelligence request failed.';
}

// Model choices for the switcher. `id` is passed straight to the CLI's --model;
// '' = the CLI default ("Auto"). Switching restarts the session on the same
// conversation (server SetModel), so nothing is lost.
const MODELS: { id: string; label: string; desc: string }[] = [
  { id: '', label: 'Auto', desc: 'CLI 기본 선택' },
  { id: 'claude-fable-5', label: 'Fable 5', desc: '최신 · 복잡하고 긴 작업' },
  { id: 'claude-opus-5', label: 'Opus 5', desc: '복잡한 에이전틱 코딩 · 기업용 (1M)' },
  { id: 'claude-opus-4-8', label: 'Opus 4.8', desc: '이전 세대 · 깊은 추론' },
  { id: 'claude-opus-4-8[1m]', label: 'Opus 4.8 · 1M', desc: '초대용량 컨텍스트(1M)' },
  { id: 'claude-sonnet-5', label: 'Sonnet 5', desc: '균형 · 빠르고 똑똑' },
  { id: 'claude-haiku-4-5-20251001', label: 'Haiku 4.5', desc: '가장 빠름 · 가벼운 작업' },
];

// Effort는 사고 깊이와 토큰 소비량을 함께 정한다 — 모델 다음으로 비용에 크게 영향을 주는
// 설정이다. 서버가 기본값 high를 못박으므로 여기에 'Auto' 항목은 없다: CLI 기본값(xhigh)은
// 가장 비싼 설정이라 "고르지 않음"이 곧 "최대로 쓰는 중"이 되어버린다.
// Claude 전용 — Codex에는 대응하는 개념이 없다.
const EFFORTS: { id: string; label: string; desc: string }[] = [
  { id: 'low', label: 'Low', desc: '짧고 범위가 분명한 작업 · 가장 저렴' },
  { id: 'medium', label: 'Medium', desc: '비용 절감 · 일상 작업에 충분' },
  { id: 'high', label: 'High', desc: '기본값 · 품질과 비용의 균형' },
  { id: 'xhigh', label: 'XHigh', desc: '어려운 코딩·에이전트 작업 권장' },
  { id: 'max', label: 'Max', desc: '비용보다 정확도 · 과사고 주의' },
];
const DEFAULT_EFFORT = 'high';

const CODEX_MODELS: typeof MODELS = [
  { id: '', label: 'Auto', desc: 'Codex 기본 모델' },
  { id: 'gpt-5.6-sol', label: 'GPT-5.6 Sol', desc: '깊은 분석 · 높은 완성도' },
  { id: 'gpt-5.6-terra', label: 'GPT-5.6 Terra', desc: '균형 잡힌 일상 작업' },
  { id: 'gpt-5.6-luna', label: 'GPT-5.6 Luna', desc: '빠르고 명확한 반복 작업' },
  { id: 'gpt-5.5', label: 'GPT-5.5', desc: '이전 세대 범용 모델' },
  { id: 'gpt-5.4', label: 'GPT-5.4', desc: '복잡한 코딩 작업' },
  { id: 'gpt-5.4-mini', label: 'GPT-5.4 Mini', desc: '가볍고 빠른 코딩 작업' },
];

// Permission modes — the TUI's Shift+Tab cycle. Switching is applied to the LIVE
// session (server SetMode → the CLI's set_permission_mode control request), so a turn
// already in flight keeps running; only a driver that cannot switch in place (Codex,
// an older CLI) falls back to a restart. `pill` encodes risk in colour: neutral →
// indigo → sky (plan) → amber (careful).
// NOTE: two of these are NOT CLI --permission-mode values. "자동 (안전 검사)" ('auto')
// has no CLI flag at all, and 전체 허용 ('bypassPermissions') is deliberately not passed
// to the CLI either — see cliPermissionMode in native_service.go. Both run the CLI in
// its default mode so every gated tool routes through our approve bridge, where the
// broker decides: auto approves safe calls and asks about risky ones, 전체 허용 approves
// everything. So the bridge stays connected in EVERY mode — if it is missing, that is
// a real fault in any mode, not an expected state.
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
  const [intelligenceRefreshKey, setIntelligenceRefreshKey] = useState(0);
  const [intelligenceMode, setIntelligenceMode] = useState<IntelligenceMode>(() => savedIntelligenceMode(agentId));
  const [localProviders, setLocalProviders] = useState<LocalProvider[]>([]);
  const [providersLoading, setProvidersLoading] = useState(true);
  const [providersError, setProvidersError] = useState(false);
  const [localProvider, setLocalProvider] = useState(() => localStorage.getItem(`pcd:intelligence-provider:${agentId}`) || '');
  const [localOperation, setLocalOperation] = useState<LocalOperation>(() => savedLocalOperation(agentId));
  // The id of the Local Intelligence run this chat is waiting on, or null. A run is
  // a server-side job: it survives this component, this tab, and this device, so
  // what we hold is a subscription key rather than the run itself.
  const [activeTraceId, setActiveTraceId] = useState<string | null>(null);
  // Covers the gap between pressing send and the 202 coming back with a trace id.
  const [intelligenceStarting, setIntelligenceStarting] = useState(false);
  // The turn to put back in the composer if the run fails. The draft is cleared at
  // send time (the server owns the echo), and the failure now arrives seconds later
  // over the socket, so the text has to be parked somewhere until then.
  const restoreOnFailure = useRef<{ text: string; attachments: { name: string; path: string }[] } | null>(null);
  const [intelligenceNotice, setIntelligenceNotice] = useState('');
  const [localOutput, setLocalOutput] = useState('');
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
  // Evicted = another device took over this session. We drop to a standby screen and
  // stop auto-reopening (even across reconnects) until the user taps 연결하기, so the
  // two devices don't fight over who owns the session. The ref mirrors the state so
  // the reconnect handler can read it synchronously.
  const [evicted, setEvicted] = useState(false);
  const evictedRef = useRef(false);
  useEffect(() => { evictedRef.current = evicted; }, [evicted]);
  const sentTimer = useRef<number | null>(null);
  const [modelId, setModelId] = useState(() => {
    const saved = localStorage.getItem(`pcd:model:${agentId}`) || '';
    // Model catalogs change across Codex releases. Never keep launching a stale
    // slug that the current switcher no longer supports: a failed app-server
    // restart otherwise leaves the chat looking blank on every reopen.
    return driver === 'codex' && !CODEX_MODELS.some((choice) => choice.id === saved) ? '' : saved;
  });
  const [modeId, setModeId] = useState(() => localStorage.getItem(`pcd:mode:${agentId}`) || '');
  // An unknown saved value (a hand-edited key, or a level a future CLI drops) folds to
  // the default rather than being sent on — the server would reject it anyway, and a
  // stale slug must never be able to fail the session start.
  const [effortId, setEffortId] = useState(() => {
    const saved = localStorage.getItem(`pcd:effort:${agentId}`) || '';
    return EFFORTS.some((e) => e.id === saved) ? saved : DEFAULT_EFFORT;
  });
  const [menu, setMenu] = useState<null | 'add' | 'model' | 'mode' | 'effort'>(null);
  // Session options live on the server, not in localStorage: they describe the agent
  // and are re-validated at every launch, so the client only ever mirrors them.
  const [options, setOptions] = useState<SessionOptions>(EMPTY_OPTIONS);
  const [optionsDropped, setOptionsDropped] = useState<string[]>([]);
  const [optionsOpen, setOptionsOpen] = useState(false);
  // `/plugin` is intercepted here (the CLI refuses it over the stream), opening this
  // panel. A prefilled query lets "/plugin foo" jump straight to a search.
  const [plugins, setPlugins] = useState<null | { query: string }>(null);
  const scrollRef = useRef<HTMLDivElement>(null);
  const stickRef = useRef(true);
  const taRef = useRef<HTMLTextAreaElement>(null);
  const fileRef = useRef<HTMLInputElement>(null);
  const modelIdRef = useRef(modelId);
  modelIdRef.current = modelId;
  const modeIdRef = useRef(modeId);
  modeIdRef.current = modeId;
  const effortIdRef = useRef(effortId);
  effortIdRef.current = effortId;

  useEffect(() => {
    let active = true;
    setProvidersLoading(true);
    setProvidersError(false);
    api.listLocalProviders()
      .then((providers) => {
        if (!active) return;
        const enabled = providers.filter((provider) => provider.enabled);
        setLocalProviders(enabled);
        setLocalProvider((current) => {
          if (enabled.some((provider) => provider.name === current)) return current;
          if (enabled.length === 1) {
            try { localStorage.setItem(`pcd:intelligence-provider:${agentId}`, enabled[0].name); } catch { /* ignore */ }
            return enabled[0].name;
          }
          return '';
        });
      })
      .catch(() => {
        if (active) setProvidersError(true);
      })
      .finally(() => {
        if (active) setProvidersLoading(false);
      });
    return () => { active = false; };
  }, [agentId]);

  const pickIntelligenceMode = useCallback((next: IntelligenceMode) => {
    setIntelligenceMode(next);
    setIntelligenceNotice('');
    try { localStorage.setItem(`pcd:intelligence-mode:${agentId}`, next); } catch { /* ignore */ }
  }, [agentId]);

  const pickLocalProvider = useCallback((next: string) => {
    setLocalProvider(next);
    try { localStorage.setItem(`pcd:intelligence-provider:${agentId}`, next); } catch { /* ignore */ }
  }, [agentId]);

  const pickLocalOperation = useCallback((next: LocalOperation) => {
    setLocalOperation(next);
    try { localStorage.setItem(`pcd:intelligence-operation:${agentId}`, next); } catch { /* ignore */ }
  }, [agentId]);

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

  // Unlike the permission mode, effort has no live switch — it's a spawn flag, so the
  // server restarts the process on the same conversation, exactly like a model change.
  const pickEffort = useCallback((id: string) => {
    setMenu(null);
    if (id === effortIdRef.current) return;
    setEffortId(id);
    try { localStorage.setItem(`pcd:effort:${agentId}`, id); } catch { /* ignore */ }
    agentDeckWS.send('native:setEffort', { agentId, effort: id });
  }, [agentId]);

  // Shift+Tab cycles the permission mode, like the Claude Code TUI.
  const cycleMode = useCallback(() => {
    const i = MODES.findIndex((m) => m.id === modeIdRef.current);
    pickMode(MODES[(i + 1) % MODES.length].id);
  }, [pickMode]);

  const models = driver === 'codex' ? CODEX_MODELS : MODELS;
  const modelLabel = models.find((m) => m.id === modelId)?.label ?? modelId ?? 'Auto';
  const currentMode = MODES.find((m) => m.id === modeId) ?? MODES[0];
  // Codex has no effort concept, so the control is hidden there rather than shown
  // inert — a setting that silently does nothing is worse than no setting.
  const showEffort = driver !== 'codex';
  const currentEffort = EFFORTS.find((e) => e.id === effortId) ?? EFFORTS[2];

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
  //
  // The height we assign INCLUDES the border (Tailwind's preflight makes everything
  // border-box) but scrollHeight EXCLUDES it, so assigning scrollHeight straight left
  // the content box a border's worth too short — scrollHeight stayed greater than
  // clientHeight and the box showed a scrollbar even while empty. Measure the actual
  // border instead of assuming a width, so this stays correct if the styling changes.
  //
  // Overflow is toggled rather than left on `auto`: below the cap there is nothing to
  // scroll, and a permanently-scrollable box is what made the stray bar visible.
  useEffect(() => {
    const ta = taRef.current;
    if (!ta) return;
    ta.style.height = 'auto';
    const border = ta.offsetHeight - ta.clientHeight; // 0 when there is no border
    const needed = ta.scrollHeight + border;
    ta.style.height = `${Math.min(needed, 160)}px`;
    ta.style.overflowY = needed > 160 ? 'auto' : 'hidden';
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
      const event = p.event as StreamEvent;
      setEvents((prev) => [...prev, event]);
      if (event.type === 'result') setIntelligenceRefreshKey((key) => key + 1);
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
      // "" means the session has no effort setting at all (Codex) — leave the local
      // choice alone rather than overwriting it with a blank that isn't a real level.
      if (typeof p.effort === 'string' && p.effort !== '' && p.effort !== effortIdRef.current) {
        setEffortId(p.effort);
        try { localStorage.setItem(`pcd:effort:${agentId}`, p.effort); } catch { /* ignore */ }
      }
      if (p.options) setOptions(parseSessionOptions(p.options));
    });
    const offOptions = agentDeckWS.on('native:options', (p: any) => {
      if (p.agentId !== agentId) return;
      setOptions(parseSessionOptions(p.options));
      setOptionsDropped(Array.isArray(p.dropped) ? p.dropped : []);
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
    // Another device opened this session — go to standby. We keep receiving events in
    // the background but the overlay hides them; a reclaim reloads authoritative
    // history anyway.
    const offEvicted = agentDeckWS.on('native:evicted', (p: any) => {
      if (p.agentId !== agentId) return;
      setEvicted(true);
    });

    const open = () => agentDeckWS.send('native:open', { agentId, driver, cwd, model: modelIdRef.current, mode: modeIdRef.current, effort: effortIdRef.current });
    open();
    // Re-open after a reconnect — but NOT while evicted, or a background socket blip
    // would silently steal the session back from the device you moved to.
    const offOpen = agentDeckWS.on('open', () => { if (!evictedRef.current) open(); });

    return () => { offEvent(); offHistory(); offOptions(); offApproval(); offState(); offError(); offEvicted(); offOpen(); };
  }, [agentId, cwd, model, driver]);

  // Reclaim the session on this device: re-open (which evicts whoever took it) and
  // leave standby.
  const reclaim = useCallback(() => {
    setEvicted(false);
    evictedRef.current = false;
    agentDeckWS.send('native:open', { agentId, driver, cwd, model: modelIdRef.current, mode: modeIdRef.current, effort: effortIdRef.current });
  }, [agentId, cwd, driver]);

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

  const markJustSent = useCallback(() => {
    // Show motion at once — don't wait for the server to echo the turn back. A
    // safety timeout drops the flag if no real turn ever materialises (e.g. a line
    // the CLI answers without a turn), so the indicator can't get stuck on.
    setJustSent(true);
    if (sentTimer.current) clearTimeout(sentTimer.current);
    sentTimer.current = window.setTimeout(() => setJustSent(false), 8000);
  }, []);

  const sendText = useCallback((text: string) => {
    if (!text.trim()) return;
    setError('');
    agentDeckWS.send('native:input', { agentId, text });
    markJustSent();
  }, [agentId, markJustSent]);

  const interrupt = useCallback(() => {
    agentDeckWS.send('native:interrupt', { agentId });
  }, [agentId]);

  // The run we are watching, pushed here by the socket (useWebSocket → appStore).
  const activeRun = useAppStore((s) => (activeTraceId ? s.intelligenceRuns.get(activeTraceId) : undefined));
  // "Preparing" covers only the local phase: from pressing send until the local
  // model hands off. Once the task is dispatched to the cloud, the ordinary
  // "agent is working" indicator owns the screen, even though the trace stays open
  // until the cloud turn closes it.
  const intelligencePreparing = intelligenceStarting
    || (!!activeTraceId && (!activeRun || isLocalPhaseRunning(activeRun.trace)));

  const failRun = useCallback((trace: IntelligenceTrace) => {
    const localCode = localFailureCode(trace);
    const cloudCode = cloudFailureCode(trace);
    setError(localCode && cloudCode
      ? `${localErrorLabel(localCode)} ${localErrorLabel(cloudCode)} The task was not sent.`
      : `${localErrorLabel(localCode || cloudCode || trace.errorCode)} The task was not sent.`);
    // Give the turn back. Only if the composer is still empty — the user has had
    // seconds to start typing something else, and overwriting that would be worse
    // than losing the original.
    const restore = restoreOnFailure.current;
    restoreOnFailure.current = null;
    if (!restore) return;
    setDraft((current) => current || restore.text);
    setAttachments((current) => (current.length ? current : restore.attachments));
  }, []);

  // The outcome of a run arrives here, not from the call that started it. Anything
  // that used to follow `await runIntelligence(...)` lives in this effect now —
  // which is also why closing the tab mid-run no longer loses the result.
  useEffect(() => {
    if (!activeTraceId || !activeRun) return;
    const { trace } = activeRun;
    if (isLocalPhaseRunning(trace)) return;

    setActiveTraceId(null);
    setIntelligenceRefreshKey((key) => key + 1);

    if (trace.mode === 'LOCAL_ONLY') {
      if (trace.status !== 'SUCCESS') {
        failRun(trace);
        return;
      }
      // The pack is never stored server-side, so this event is the only place it
      // exists. If it is missing, say so rather than showing an empty panel.
      setLocalOutput(activeRun.contextPack || 'Local task completed without a returned context result.');
      setIntelligenceNotice('Local result ready. No cloud execution was used.');
      restoreOnFailure.current = null;
      return;
    }
    if (trace.status === 'FAILED') {
      failRun(trace);
      return;
    }
    markJustSent();
    setIntelligenceNotice(trace.fallback
      ? `Local optimization unavailable. ${localErrorLabel(localFailureCode(trace))} Continuing with ${cloudTargetName(driver)}.`
      : `Optimized context sent to ${cloudTargetName(driver)}.`);
    restoreOnFailure.current = null;
  }, [activeRun, activeTraceId, driver, failRun, markJustSent]);

  // Stopping is a decision now, so it ends the run as a cancellation and never
  // spends a cloud turn on a fallback (the server enforces that half).
  const cancelIntelligenceRun = useCallback(async () => {
    if (!activeTraceId) return;
    try {
      await api.cancelIntelligence(activeTraceId);
    } catch {
      // 404 means it already finished; the trace event that follows says how.
    }
  }, [activeTraceId]);

  const send = useCallback(async () => {
    const text = draft.trim();
    if (!text && !attachments.length) return;
    // A hybrid run holds the turn while the local model chews on the context pack —
    // which has taken minutes on a slow or unreachable provider. Returning silently
    // here made the deck look dead: you typed, pressed Enter, and nothing happened,
    // with no banner and the draft still sitting there. Say so instead, and keep the
    // draft so Enter after it finishes still sends what you wrote.
    if (intelligencePreparing) {
      setError('로컬 컨텍스트를 준비하는 중입니다. 끝나면 전송하거나, 중단하려면 Intelligence 모드를 Cloud Only로 바꾸세요.');
      return;
    }
    const command = !attachments.length ? clientCommand(text) : null;
    // /clear starts a genuinely new session instead of being forwarded. Sent to the
    // CLI it drops the context but leaves the transcript on screen, so the chat looks
    // intact while Claude has forgotten every word of it — the worst kind of wrong
    // screen. A fresh session clears both at once.
    if (command === 'clear') {
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
    // /plugin — the CLI answers "isn't available" over the stream, so the deck owns
    // it. `/plugin install name@marketplace` runs the install directly; anything else
    // (`/plugin`, `/plugins`, `/plugin foo`) opens the management panel, prefilling a
    // search with whatever followed the command.
    if (command === 'plugin') {
      setDraft('');
      setHistIdx(null);
      const rest = text.replace(/^\/plugins?\s*/, '').trim();
      const m = rest.match(/^(install|add|enable)\s+(\S+@\S+)/i);
      if (m) {
        setError('');
        try {
          await api.installPlugin(m[2]);
          setPlugins({ query: '' }); // open the panel so the restart banner is visible
        } catch (err) {
          setError(`${m[2]} 설치 실패: ` + String(err));
        }
      } else {
        setPlugins({ query: rest });
      }
      return;
    }
    const isNativeCommand = command === 'native';
    // Attachments ride along as paths inside the project — Claude opens them with
    // its Read tool. Sent as part of the same user turn.
    const msg = attachments.length
      ? (text ? text + '\n\n' : '') + '첨부 파일 (Read 도구로 확인해줘):\n' + attachments.map((a) => a.path).join('\n')
      : text;
    const hasAttachments = attachments.length > 0;
    if (intelligenceMode === 'LOCAL_PREPROCESS_CLOUD' && busy && !hasAttachments && !isNativeCommand) {
      setError('Wait for the current cloud turn to finish before starting another Hybrid run.');
      return;
    }
    if (intelligenceMode === 'LOCAL_PREPROCESS_CLOUD' && !running && !hasAttachments && !isNativeCommand) {
      setError(`Wait for the ${cloudTargetName(driver)} session to be ready before starting a Hybrid run.`);
      return;
    }
    if (intelligenceMode !== 'CLOUD_ONLY' && !hasAttachments && !isNativeCommand) {
      if (providersLoading) {
        setError('Local Intelligence providers are still loading.');
        return;
      }
      if (!localProvider || !localProviders.some((provider) => provider.name === localProvider)) {
        setError('No Local Intelligence provider selected. Open Settings or choose an enabled provider.');
        return;
      }
    }

    const originalAttachments = attachments;
    setAttachments([]);
    setHistIdx(null); // sending ends history browsing; the next ↑ starts from the newest
    // No local echo: the server records the user turn the moment it's sent
    // (NativeService.Send) and fans it out, so it arrives like every other event and
    // survives a reconnect. A local copy would print twice, and — being invisible to
    // the server — would vanish whenever history replaced our events.
    setDraft('');
    setError('');
    setIntelligenceNotice('');
    setLocalOutput('');

    const usesIntelligence = intelligenceMode !== 'CLOUD_ONLY' && !hasAttachments && !isNativeCommand;
    if (usesIntelligence) setIntelligenceStarting(true);
    try {
      const routed = await routeNativeTask({
        agentId,
        driver,
        task: msg,
        mode: intelligenceMode,
        provider: localProvider,
        operation: localOperation,
        hasAttachments,
        isNativeCommand,
      }, {
        sendNative: sendText,
        runIntelligence: api.runIntelligence,
      });

      if (routed.path === 'cloud') {
        if (routed.attachmentFallback) {
          setIntelligenceNotice('Attachments use Cloud Only. Local optimization was not used.');
        } else if (routed.commandBypass) {
          setIntelligenceNotice('Commands use the direct native path. Local optimization was not used.');
        }
        return;
      }

      // The run was ACCEPTED, not finished. Everything that used to be read off the
      // response — the context pack, the fallback notice, the failure — now arrives
      // on intelligence:trace and is handled by the effect above. This is what lets
      // the run survive a closed tab, a locked phone, or a proxy read timeout.
      setIntelligenceRefreshKey((key) => key + 1);
      restoreOnFailure.current = { text, attachments: originalAttachments };
      setActiveTraceId(routed.start.trace.id);
    } catch (err) {
      // Only synchronous refusals land here now: a 400 the server decided inside the
      // request, or a transport failure that means the run never started at all.
      const failedTrace = traceFromApiError(err);
      if (failedTrace) setIntelligenceRefreshKey((key) => key + 1);
      const localCode = failedTrace ? localFailureCode(failedTrace) : undefined;
      const cloudCode = failedTrace ? cloudFailureCode(failedTrace) : undefined;
      if (!failedTrace) {
        setError('The run could not be started. Nothing was sent — check the connection and retry.');
      } else {
        setError(localCode && cloudCode
          ? `${localErrorLabel(localCode)} ${localErrorLabel(cloudCode)} The task was not sent.`
          : `${localErrorLabel(localCode || cloudCode || failedTrace.errorCode)} The task was not sent.`);
      }
      restoreOnFailure.current = null;
      setDraft(text);
      setAttachments(originalAttachments);
    } finally {
      setIntelligenceStarting(false);
    }
  }, [
    agentId, attachments, busy, draft, driver, intelligenceMode, intelligencePreparing,
    localOperation, localProvider, localProviders, markJustSent, navigate, providersLoading, running, sendText,
  ]);

  const decide = useCallback((id: string, behavior: 'allow' | 'deny', message?: string, remember?: boolean) => {
    agentDeckWS.send('native:decide', { agentId, id, behavior, message, remember });
    setPending((prev) => prev.filter((p) => p.id !== id));
  }, [agentId]);

  return (
    <div className="relative flex flex-col h-full bg-deck-bg">
      {evicted && (
        <div className="absolute inset-0 z-40 flex flex-col items-center justify-center gap-3 px-6 text-center bg-deck-bg/95">
          <IconDevices size={30} className="text-deck-text-dim" />
          <div className="text-sm text-deck-text-dim max-w-xs">
            다른 기기에서 이 세션을 열었습니다.<br />한 번에 한 기기에서만 사용할 수 있어요.
          </div>
          <button
            onClick={reclaim}
            className="px-4 py-2 rounded-lg text-sm font-semibold shadow-lg touch-manipulation active:opacity-70 bg-deck-accent text-white"
          >
            이 기기에서 연결하기
          </button>
        </div>
      )}
      <div ref={scrollRef} onScroll={onScroll} className="selectable flex-1 overflow-y-auto px-3 py-3 space-y-2">
        <SessionSavingsSummary agentId={agentId} refreshKey={intelligenceRefreshKey} driver={driver} />
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

        {/* Effort menu. Changing it restarts the process (it's a spawn flag), so the
            footer says so — the conversation survives, an in-flight turn does not. */}
        {menu === 'effort' && (
          <div className="absolute bottom-14 right-2 z-20 w-72 max-w-[calc(100vw-1rem)] bg-deck-raised border border-deck-border rounded-lg shadow-xl overflow-hidden">
            <div className="px-3 py-1.5 text-[10px] uppercase tracking-wide text-deck-text-dim">
              Effort · 사고 깊이와 토큰 사용량
            </div>
            {EFFORTS.map((e) => (
              <button
                key={e.id}
                onClick={() => pickEffort(e.id)}
                className={`w-full text-left px-3 py-2 hover:bg-deck-bg/60 flex items-start gap-2 ${e.id === effortId ? 'bg-deck-bg/40' : ''}`}
              >
                <span className={`mt-0.5 shrink-0 w-3.5 ${e.id === effortId ? 'text-deck-accent' : 'text-transparent'}`}><IconCheck size={14} /></span>
                <span className="min-w-0">
                  <span className={`block text-sm ${e.id === effortId ? 'text-deck-accent' : 'text-deck-text'}`}>{e.label}</span>
                  <span className="block text-xs text-deck-text-dim leading-snug">{e.desc}</span>
                </span>
              </button>
            ))}
            <div className="px-3 py-2 border-t border-deck-border text-[11px] text-deck-text-dim leading-snug">
              바꾸면 세션이 재시작됩니다. 대화는 이어지지만 진행 중인 작업은 중단됩니다.
            </div>
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
        {intelligencePreparing && (
          <div className="mx-2 mt-2 flex items-center gap-2 overflow-hidden rounded-lg border border-deck-accent/20 bg-deck-accent/10 px-3 py-1.5 text-xs text-deck-accent-light">
            <IconSpinner size={13} className="animate-spin shrink-0" />
            <span className="min-w-0 flex-1 truncate">Preparing local context… {localProvider} → {intelligenceMode === 'LOCAL_ONLY' ? 'Local result' : cloudTargetName(driver)}</span>
            {activeTraceId && (
              <button
                onClick={() => void cancelIntelligenceRun()}
                className="shrink-0 rounded-md px-2 py-0.5 text-[11px] font-medium text-deck-text-dim hover:bg-deck-border/50"
                title="로컬 전처리를 중단합니다. 클라우드로 넘기지 않습니다"
              >
                중단
              </button>
            )}
          </div>
        )}
        {!intelligencePreparing && working && (
          <div className="mx-2 mt-2 flex items-center gap-2 px-3 py-1.5 rounded-lg bg-deck-accent/10 border border-deck-accent/20 text-deck-accent-light text-xs overflow-hidden">
            <IconSpinner size={13} className="animate-spin shrink-0" />
            <span className="shrink-0">에이전트가 작업 중…</span>
            <span className="relative ml-1 flex-1 h-0.5 rounded-full bg-deck-accent/15 overflow-hidden">
              <span className="absolute inset-y-0 left-0 w-1/3 rounded-full bg-deck-accent/60 animate-working-bar" />
            </span>
          </div>
        )}

        {intelligenceNotice && (
          <div className={`mx-2 mt-2 flex items-start justify-between gap-2 rounded-lg border px-3 py-2 text-xs ${
            intelligenceNotice.startsWith('Local optimization unavailable')
              ? 'border-deck-warning/30 bg-deck-warning/5 text-deck-warning'
              : 'border-deck-border bg-deck-surface text-deck-text-dim'
          }`}>
            <span className="min-w-0 leading-relaxed">{intelligenceNotice}</span>
            <button onClick={() => setIntelligenceNotice('')} className="min-h-6 shrink-0 px-1 opacity-70" aria-label="Dismiss Intelligence status">×</button>
          </div>
        )}

        {localOutput && (
          <div className="mx-2 mt-2 rounded-lg border border-deck-success/25 bg-deck-success/5 p-3">
            <div className="flex items-center justify-between gap-2">
              <span className="text-xs font-medium text-deck-success">Local result</span>
              <button onClick={() => setLocalOutput('')} className="min-h-7 px-1 text-deck-text-dim" aria-label="Dismiss local result">×</button>
            </div>
            <pre className="selectable mt-2 max-h-56 overflow-y-auto whitespace-pre-wrap break-words font-mono text-xs leading-relaxed text-deck-text">{localOutput}</pre>
          </div>
        )}

        <div className="p-2 space-y-2">
          <input ref={fileRef} type="file" multiple className="hidden" onChange={onFilePick} />
          <ExecutionModeControl
            mode={intelligenceMode}
            onModeChange={pickIntelligenceMode}
            providers={localProviders}
            providersLoading={providersLoading}
            providersError={providersError}
            provider={localProvider}
            onProviderChange={pickLocalProvider}
            operation={localOperation}
            onOperationChange={pickLocalOperation}
            driver={driver}
            onOpenSettings={() => navigate('/settings')}
          />
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
            placeholder={`${cloudTargetName(driver)}에게 메시지…`}
            style={{ maxHeight: 160 }}
            className="w-full resize-none overflow-y-hidden bg-deck-surface border border-deck-border rounded-lg px-3 py-2 text-sm text-deck-text outline-none focus:border-deck-accent"
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
            {showEffort && (
              <button
                onClick={() => { setMenu(null); setOptionsOpen(true); }}
                className="shrink-0 w-8 h-8 rounded-lg bg-deck-surface border border-deck-border text-deck-text-dim flex items-center justify-center relative"
                title="세션 옵션 — 추가 경로 · 예산 · 자동 압축 · 폴백 모델"
              >
                <IconSliders size={15} />
                {/* A dot only when something is actually configured: the control is
                    otherwise indistinguishable from an unused one at a glance. */}
                {hasSessionOptions(options) && (
                  <span className="absolute top-1 right-1 w-1.5 h-1.5 rounded-full bg-deck-accent" />
                )}
              </button>
            )}
            {showEffort && (
              <button
                onClick={() => setMenu(menu === 'effort' ? null : 'effort')}
                className={`shrink-0 h-8 px-2.5 rounded-full bg-deck-surface border border-deck-border text-deck-text-dim text-xs flex items-center gap-1.5 ${
                  menu === 'effort' ? 'ring-1 ring-deck-accent' : ''
                }`}
                title="Effort 전환 — 사고 깊이와 토큰 사용량"
              >
                <IconGauge size={13} />
                {currentEffort.label}
              </button>
            )}
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
              disabled={intelligencePreparing || (!draft.trim() && !attachments.length) || (
                intelligenceMode === 'LOCAL_PREPROCESS_CLOUD' && (busy || !running) && !attachments.length
                && clientCommand(draft.trim()) === null
              )}
              className="shrink-0 px-4 h-8 rounded-lg bg-deck-accent text-white text-sm font-medium disabled:opacity-40"
              title={intelligenceMode === 'LOCAL_PREPROCESS_CLOUD' && !running && !attachments.length
                && clientCommand(draft.trim()) === null
                ? `${cloudTargetName(driver)} 세션이 준비된 뒤 Hybrid를 실행할 수 있습니다`
                : intelligenceMode === 'LOCAL_PREPROCESS_CLOUD' && busy && !attachments.length
                && clientCommand(draft.trim()) === null
                ? '현재 cloud turn이 끝난 뒤 Hybrid를 실행할 수 있습니다'
                : busy ? '현재 답변이 끝나면 이어서 처리됩니다' : undefined}
            >
              {busy ? '이어서' : '보내기'}
            </button>
          </div>
        </div>
      </div>

      {plugins && (
        <PluginsPanel
          agentId={agentId}
          initialQuery={plugins.query}
          onClose={() => setPlugins(null)}
        />
      )}

      {optionsOpen && (
        <SessionOptionsSheet
          agentId={agentId}
          current={options}
          dropped={optionsDropped}
          onClose={() => { setOptionsOpen(false); setOptionsDropped([]); }}
        />
      )}
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
    //
    // This used to be suppressed in 전체 허용, where the driver dropped the bridge on
    // purpose. It no longer does: 전체 허용 is enforced server-side and the bridge is
    // attached in every mode. A missing bridge is now a genuine fault everywhere, and
    // that mode is the worst place to hide it — it is where a silent denial looks
    // exactly like the agent deciding to skip the work.
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
    // A sub-agent's own words, forwarded by --forward-subagent-text. Indented behind a
    // rule and dimmed so it reads as work happening underneath the answer rather than
    // as the answer: the main thread stays the loudest voice on screen even while
    // several sub-agents are talking.
    if (item.subagent) {
      return (
        <div className="pl-3 border-l-2 border-deck-border/70 my-1">
          <div className="text-[10px] uppercase tracking-wide text-deck-muted mb-0.5">sub-agent</div>
          <div className="text-deck-text-dim text-[13px]"><AssistantText text={item.text} /></div>
        </div>
      );
    }
    return <AssistantText text={item.text} />;
  }

  // TodoWrite arrives as an ordinary tool call, but a raw JSON dump of a 16-item
  // checklist is unreadable in a conversation. Render it as a checklist instead —
  // falling back to the generic row if the input isn't the shape we expect, so a
  // CLI schema change degrades to "plain tool card" rather than a blank.
  if (item.kind === 'tool' && item.name === 'TodoWrite') {
    const todos = todosFromInput(item.input);
    if (todos) return <TodoToolRow todos={todos} />;
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

  // Desktop keyboard control: ↑/↓ move focus between options (and the send button),
  // Enter activates the focused one — so a whole question can be answered without the
  // mouse. Skipped on touch, where auto-focusing would pop the on-screen keyboard.
  const boxRef = useRef<HTMLDivElement>(null);
  const isTouch = typeof window !== 'undefined' && ('ontouchstart' in window || navigator.maxTouchPoints > 0);

  useEffect(() => {
    if (isTouch || sent) return;
    // Take focus to the first option when the question appears (rAF so it's painted
    // and focusable). We grab it even from the composer — a question is a decision the
    // user has to make, and the draft text stays in state regardless of focus.
    const id = requestAnimationFrame(() => {
      boxRef.current
        ?.querySelector<HTMLElement>('[data-ask-focusable]:not([disabled])')
        ?.focus({ preventScroll: true });
    });
    return () => cancelAnimationFrame(id);
    // Mount-only: focus the first option when the question appears.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const onKeyNav = (e: React.KeyboardEvent) => {
    // Inside the free-text field, arrows move the caret — leave them alone.
    if (e.target instanceof HTMLInputElement) return;
    if (e.key !== 'ArrowDown' && e.key !== 'ArrowUp') return;
    const els = Array.from(boxRef.current?.querySelectorAll<HTMLElement>('[data-ask-focusable]:not([disabled])') ?? []);
    if (!els.length) return;
    e.preventDefault();
    const cur = els.indexOf(document.activeElement as HTMLElement);
    const delta = e.key === 'ArrowDown' ? 1 : -1;
    els[cur < 0 ? 0 : (cur + delta + els.length) % els.length].focus({ preventScroll: true });
  };

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
    <div ref={boxRef} onKeyDown={onKeyNav} className="space-y-2">
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
                  data-ask-focusable
                  onClick={() => { if (!sent) toggle(qi, q, o.label); }}
                  disabled={!!sent}
                  className={`w-full text-left px-3 py-2 rounded-lg border text-xs disabled:opacity-60 outline-none focus:ring-2 focus:ring-deck-accent-light ${
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
          data-ask-focusable
          onClick={submit}
          disabled={!complete}
          className="w-full py-2 rounded-lg bg-deck-accent text-white text-xs font-medium disabled:opacity-40 outline-none focus:ring-2 focus:ring-white/70"
        >
          {complete ? '선택 보내기' : '항목을 선택하세요'}
        </button>
      )}
    </div>
  );
}

// todosFromInput narrows a TodoWrite tool input to the checklist, or null when it
// isn't that shape. Every TodoWrite call carries the WHOLE list (verified against
// real transcripts: a 7-item list came back as 16 with earlier entries rewritten),
// so there is nothing to merge — the call's input IS the state at that moment.
function todosFromInput(input: Record<string, unknown>): ActivityTodo[] | null {
  const raw = (input as { todos?: unknown }).todos;
  if (!Array.isArray(raw) || raw.length === 0) return null;
  const todos = raw.filter(
    (t): t is ActivityTodo =>
      !!t && typeof t === 'object' && typeof (t as ActivityTodo).content === 'string',
  );
  return todos.length ? todos : null;
}

// The chat card is the HISTORY of the plan ("here is where it was rewritten"); the
// TodoStrip above the composer is the CURRENT state. Collapsed by default because a
// re-plan happens often and expanding every one would bury the conversation.
// Glyphs match TodoStrip so the same list reads the same in both places.
function TodoToolRow({ todos }: { todos: ActivityTodo[] }) {
  const [open, setOpen] = useState(false);
  const completed = todos.filter((t) => t.status === 'completed').length;
  const active = todos.find((t) => t.status === 'in_progress');

  return (
    <div className="border border-deck-border rounded-lg overflow-hidden">
      <button onClick={() => setOpen((v) => !v)} className="w-full flex items-center gap-2 px-3 py-2 text-left">
        <span className="text-xs font-medium text-deck-accent shrink-0">☑ {completed}/{todos.length}</span>
        <span className="text-xs text-deck-muted truncate flex-1">
          {active ? `▸ ${active.activeForm || active.content}` : '할일 갱신'}
        </span>
        <span className="text-[10px] text-deck-text-faint shrink-0">{open ? '∨' : '∧'}</span>
      </button>
      {open && (
        <div className="px-3 pb-2 space-y-1">
          {todos.map((todo, i) => (
            <div key={`${i}-${todo.content}`} className="flex gap-2 text-xs">
              <span className={
                todo.status === 'completed'
                  ? 'text-emerald-400'
                  : todo.status === 'in_progress'
                    ? 'text-deck-accent'
                    : 'text-deck-text-faint'
              }>
                {todo.status === 'completed' ? '✓' : todo.status === 'in_progress' ? '▸' : '○'}
              </span>
              <span className={todo.status === 'completed' ? 'text-deck-text-faint line-through' : 'text-deck-text-dim'}>
                {todo.status === 'in_progress' ? todo.activeForm || todo.content : todo.content}
              </span>
            </div>
          ))}
        </div>
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
  onDecide: (id: string, behavior: 'allow' | 'deny', message?: string, remember?: boolean) => void;
}) {
  const [reason, setReason] = useState('');
  const [showReason, setShowReason] = useState(false);
  const summary = toolSummary(req.toolName, req.input);

  // Desktop keyboard control: focus 허용/거부 when the prompt appears so ←/→ move
  // between them and Enter decides — the same as the question buttons. A Bash-approval
  // that pops up needs a decision, and typing it out with the mouse was the gap.
  const cardRef = useRef<HTMLDivElement>(null);
  const isTouch = typeof window !== 'undefined' && ('ontouchstart' in window || navigator.maxTouchPoints > 0);
  useEffect(() => {
    if (isTouch) return;
    // A blocked agent needs a decision, so we DO take focus (even from the composer —
    // the draft text is kept in state regardless). rAF waits for paint so the button
    // is actually focusable when we call focus().
    const id = requestAnimationFrame(() => {
      cardRef.current
        ?.querySelector<HTMLElement>('[data-approve-focusable]:not([disabled])')
        ?.focus({ preventScroll: true });
    });
    return () => cancelAnimationFrame(id);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);
  const onKeyNav = (e: React.KeyboardEvent) => {
    if (e.target instanceof HTMLTextAreaElement || e.target instanceof HTMLInputElement) return;
    if (!['ArrowLeft', 'ArrowRight', 'ArrowUp', 'ArrowDown'].includes(e.key)) return;
    const els = Array.from(cardRef.current?.querySelectorAll<HTMLElement>('[data-approve-focusable]:not([disabled])') ?? []);
    if (!els.length) return;
    e.preventDefault();
    const cur = els.indexOf(document.activeElement as HTMLElement);
    const delta = e.key === 'ArrowRight' || e.key === 'ArrowDown' ? 1 : -1;
    els[cur < 0 ? 0 : (cur + delta + els.length) % els.length].focus({ preventScroll: true });
  };

  return (
    <div ref={cardRef} onKeyDown={onKeyNav} className="border-t border-amber-400/40 bg-amber-400/10 px-3 py-3 space-y-2">
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
          data-approve-focusable
          onClick={() => onDecide(req.id, 'allow')}
          className="flex-1 py-2.5 rounded-lg bg-green-500/20 text-green-400 text-sm font-medium outline-none focus:ring-2 focus:ring-green-300 focus:bg-green-500/35"
        >
          허용
        </button>
        {/* canRemember가 true일 때만 렌더: 위험 판정된 호출에는 버튼을 숨긴다.
            undefined(낡은 서버·캐시)는 false와 같이 처리 — 안전한 쪽으로 떨어진다. */}
        {req.canRemember && (
          <button
            data-approve-focusable
            onClick={() => onDecide(req.id, 'allow', undefined, true)}
            className="flex-1 py-2.5 rounded-lg bg-green-500/10 text-green-300 text-sm font-medium outline-none focus:ring-2 focus:ring-green-300"
            title={req.rememberTarget
              ? `"항상 허용"은 이 프로젝트에서 ${req.rememberTarget} 를 다시 묻지 않습니다.`
              : `"항상 허용"은 이 프로젝트에서 ${req.toolName} 도구 전체를 다시 묻지 않습니다.`}
          >
            항상 허용
          </button>
        )}
        <button
          data-approve-focusable
          onClick={() => {
            // A reason is worth asking for: Claude reads it and adapts, so "not
            // that path, use ./tmp" is a far more useful answer than a bare no.
            if (!showReason) { setShowReason(true); return; }
            onDecide(req.id, 'deny', reason.trim() || undefined);
          }}
          className="flex-1 py-2.5 rounded-lg bg-red-500/20 text-red-400 text-sm font-medium outline-none focus:ring-2 focus:ring-red-300 focus:bg-red-500/35"
        >
          {showReason ? '거부하기' : '거부'}
        </button>
      </div>
      {/* 무엇이 저장되는지 모르고 누르는 버튼은 신뢰할 수 없는 결정이다.
          rememberTarget이 있으면 그 대상을, 없으면 도구 전체를 표시한다. */}
      {req.canRemember && (
        <div className="text-[10px] text-deck-text-faint">
          {req.rememberTarget
            ? <>"항상 허용"은 이 프로젝트에서 <span className="font-mono">{req.rememberTarget}</span> 를 다시 묻지 않습니다.</>
            : <>"항상 허용"은 이 프로젝트에서 <span className="font-mono">{req.toolName}</span> 도구 전체를 다시 묻지 않습니다.</>}
        </div>
      )}
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
