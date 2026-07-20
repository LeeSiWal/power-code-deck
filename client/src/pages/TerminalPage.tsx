import { useState, useEffect, useCallback, useRef } from 'react';
import { useParams, useNavigate, useSearchParams } from 'react-router-dom';
import { TerminalView, type TerminalHandle } from '../components/terminal/TerminalView';
import { NativeChat } from '../components/native/NativeChat';
import { MobileToolbar } from '../components/terminal/MobileToolbar';
import { TerminalKeyBar } from '../components/terminal/TerminalKeyBar';
import { PromptBar } from '../components/terminal/PromptBar';
import { HandoffModal } from '../components/terminal/HandoffModal';
import { SessionHistory } from '../components/terminal/SessionHistory';
import { FileExplorer } from '../components/file/FileExplorer';
import { FilePreview } from '../components/file/FilePreview';
import { FileEditor } from '../components/file/FileEditor';
import { FileBottomSheet } from '../components/file/FileBottomSheet';
import { SubAgentBar } from '../components/animation/SubAgentBar';
import { SubAgentPanel } from '../components/animation/SubAgentPanel';
import { StatusBadge } from '../components/layout/StatusBadge';
import { BrowserPanel } from '../components/browser/BrowserPanel';
import { useDevice } from '../hooks/useDevice';
import { useFileExplorer } from '../hooks/useFileExplorer';
import { useAgentActivity } from '../hooks/useAgentActivity';
import { IconBack, IconFiles, IconClose, IconTerminal, IconHistory, IconPhone, IconGlobe, IconExpand, IconRefresh, AGENT_ICON_MAP } from '../components/icons';
import { api } from '../lib/api';
import { writeClipboard, readClipboard } from '../lib/clipboard';
import { generatePalette } from '../lib/paletteGenerator';
import { useAppStore } from '../stores/appStore';

type CenterTab = 'terminal' | 'editor';

// Unified input is the default: the terminal owns a single cursor-anchored input
// (UnifiedInput), so the separate Prompt Bar is hidden. `?classicInput` is the
// escape hatch that brings the Prompt Bar back as a fallback.
const UNIFIED_INPUT = typeof window === 'undefined' || !window.location.search.includes('classicInput');

/**
 * Claude agents render as a chat driven by the CLI's stream-json events, not as a
 * terminal. `?terminal` forces the old TUI path back for one session.
 *
 * Native is the default where a structured driver exists — Claude stream-json or
 * Codex app-server. A shell or a custom command has no structured stream at all,
 * so those must stay on the terminal: rendering them as a chat would show an empty
 * screen forever.
 */
const CLASSIC_TERMINAL = typeof window !== 'undefined' && window.location.search.includes('terminal');

function nativeCapable(agent: { preset?: string; command?: string }): boolean {
  return agent.preset === 'claude-code' || agent.command === 'claude'
    || agent.preset === 'codex-cli' || agent.command === 'codex';
}

function nativeDriver(agent: { preset?: string; command?: string }): 'claude' | 'codex' {
  return agent.preset === 'codex-cli' || agent.command === 'codex' ? 'codex' : 'claude';
}

/** Whether this agent is rendered as a chat rather than a terminal. */
function usesNative(agent: { preset?: string; command?: string } | null | undefined): boolean {
  return !CLASSIC_TERMINAL && !!agent && nativeCapable(agent);
}

// Side-panel width bounds. The upper bound is generous because these panels are
// read, not glanced at — a session transcript or a deep path needs room on a large
// display — while the lower bound keeps a panel from being dragged to a sliver.
const PANEL_MIN = 180;
const PANEL_MAX = 640;

function readPanelWidth(side: 'left' | 'right', fallback: number): number {
  const raw = Number(localStorage.getItem(`pcd:panel:${side}`));
  if (!Number.isFinite(raw) || raw <= 0) return fallback;
  return Math.max(PANEL_MIN, Math.min(PANEL_MAX, raw));
}

export function TerminalPage() {
  const { id } = useParams<{ id: string }>();
  const navigate = useNavigate();
  const [searchParams, setSearchParams] = useSearchParams();
  const { isMobile, isTablet, isTouchDevice } = useDevice();
  const handoffEnabled = useAppStore((s) => s.authConfig?.handoffEnabled ?? true);
  const [agent, setAgent] = useState<any>(null);
  const [activeTab, setActiveTab] = useState<CenterTab>('terminal');
  const [handoffOpen, setHandoffOpen] = useState(false);
  const [handoffToast, setHandoffToast] = useState(false);
  const [mobileSessionsOpen, setMobileSessionsOpen] = useState(false);

  // The Prompt Bar is the primary text input on EVERY device. The terminal's own
  // (hidden textarea) IME can't reliably compose Korean — jamo split even on macOS —
  // so Korean / long text is always composed in the Prompt Bar and pasted in. The
  // terminal still takes direct keys (arrows, y/n, Ctrl+C) via a tap or the key bar.
  const forcePromptBar = true;
  const terminalApiRef = useRef<TerminalHandle | null>(null);
  const promptFocusedRef = useRef(false); // suspends terminal auto-focus while typing in the Prompt Bar
  const [promptOpen, setPromptOpen] = useState(forcePromptBar);
  const [promptCollapsed, setPromptCollapsed] = useState(false);
  const [hangulHintShown, setHangulHintShown] = useState(false);
  const [hangulToast, setHangulToast] = useState(false);

  const focusTerminal = useCallback(() => {
    promptFocusedRef.current = false;
    // Drop any lingering terminal selection when focus moves back to the terminal
    // (e.g. after Prompt Bar Send) so a stale highlight doesn't leave a "잔상".
    window.getSelection()?.removeAllRanges();
    terminalApiRef.current?.focus();
  }, []);

  // Prompt Bar focus/blur. On focus, clear any leftover terminal selection — the
  // Prompt Bar buttons preventDefault (to keep focus), which would otherwise keep a
  // stale terminal selection painted.
  const handlePromptFocusChange = useCallback((focused: boolean) => {
    promptFocusedRef.current = focused;
    if (focused) window.getSelection()?.removeAllRanges();
  }, []);

  // Send a control / navigation key via the terminal so arrow keys are translated
  // to the app-cursor-key form (ESC O x) when the TUI has DECCKM on — the toolbars'
  // hardcoded ESC [ x bytes don't drive arrow menus in apps like Claude Code.
  const sendTerminalKey = useCallback((data: string) => {
    terminalApiRef.current?.sendKey(data);
  }, []);

  // Copy the current terminal selection to the clipboard (mobile toolbar 복사).
  const handleCopy = useCallback(async () => {
    const text = window.getSelection()?.toString() || '';
    if (!text) return false;
    return writeClipboard(text);
  }, []);

  // Read the clipboard and paste into the input (mobile toolbar 붙여넣기). The read
  // is kicked off synchronously inside the tap so the clipboard permission gesture
  // isn't lost; the pasted text lands in the unified-input draft.
  const handlePaste = useCallback(async () => {
    const text = await readClipboard();
    if (!text) return false;
    terminalApiRef.current?.paste(text);
    return true;
  }, []);

  // Expand + focus the Prompt Bar (Prompt button / shortcut).
  const openPrompt = useCallback(() => {
    promptFocusedRef.current = true;
    setPromptOpen(true);
    setPromptCollapsed(false);
  }, []);

  // A Hangul char typed directly into the terminal — nudge the user to the
  // Prompt Bar once per session (touch devices only).
  const handleHangulDirectInput = useCallback(() => {
    setHangulHintShown((shown) => {
      if (shown) return shown;
      setHangulToast(true);
      window.setTimeout(() => setHangulToast(false), 4000);
      return true;
    });
  }, []);

  const { zoomedPanel, setZoomedPanel } = useAppStore();

  // Panels
  const [leftPanelOpen, setLeftPanelOpen] = useState(!isMobile);
  const [rightPanelOpen, setRightPanelOpen] = useState(false);
  const [rightTab, setRightTab] = useState<'subagent' | 'browser' | 'sessions'>('subagent');
  const [mobileFilesOpen, setMobileFilesOpen] = useState(false);
  const [mobileAnimOpen, setMobileAnimOpen] = useState(false);
  const [mobileBrowserOpen, setMobileBrowserOpen] = useState(false);
  // Desktop side panels. 220px was too narrow for what they actually hold: the
  // explorer truncated nested paths, and the right panel's session list — previews,
  // timestamps, sub-agent lines — wrapped constantly. Widths persist, so a resize
  // sticks instead of snapping back on every reload.
  const [leftWidth, setLeftWidth] = useState(() => readPanelWidth('left', 300));
  const [rightWidth, setRightWidth] = useState(() => readPanelWidth('right', 340));
  // The mouseup handler is created once per drag and would otherwise close over the
  // width as it was when the drag STARTED — these mirror the live value.
  const leftWidthRef = useRef(leftWidth);
  leftWidthRef.current = leftWidth;
  const rightWidthRef = useRef(rightWidth);
  rightWidthRef.current = rightWidth;
  const [editing, setEditing] = useState(false);
  const resizingRef = useRef<'left' | 'right' | null>(null);

  const agentId = id || '';

  const {
    tree, selectedFile, fileContent, changedFiles,
    fetchTree, openFile, saveFile, createDir, createFile, deleteFile, renameFile,
    setSelectedFile,
  } = useFileExplorer(agentId || null);

  const { activity } = useAgentActivity(agentId);

  useEffect(() => {
    if (agentId) {
      api.getAgent(agentId)
        .then((a) => {
          setAgent(a);
          // Remember this as the session to auto-resume on the next fresh app load
          // / refresh (see ProjectSelectPage).
          try { localStorage.setItem('pcd:lastAgentId', agentId); } catch { /* ignore */ }
        })
        .catch(() => navigate('/dashboard'));
    }
  }, [agentId, navigate]);

  // Arrived from a handoff QR — expand the Prompt Bar (Korean / long prompts)
  // and briefly confirm the connection, then strip the query param.
  useEffect(() => {
    if (searchParams.get('from') !== 'handoff') return;
    setPromptOpen(true);
    setPromptCollapsed(false);
    setHandoffToast(true);
    const t = window.setTimeout(() => setHandoffToast(false), 5000);
    const next = new URLSearchParams(searchParams);
    next.delete('from');
    setSearchParams(next, { replace: true });
    return () => window.clearTimeout(t);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  // Cmd+Shift+Z: panel zoom toggle. Cmd/Ctrl+K or Cmd/Ctrl+P: open Prompt Bar.
  useEffect(() => {
    const handler = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.shiftKey && e.key === 'z') {
        e.preventDefault();
        setZoomedPanel(zoomedPanel ? null : 'terminal');
        return;
      }
      // Cmd only (not Ctrl): Ctrl+K / Ctrl+P are readline kill-line / prev-history
      // and must stay available to the terminal. Non-Mac uses the Prompt button.
      if (e.metaKey && !e.ctrlKey && !e.shiftKey && (e.key === 'k' || e.key === 'p')) {
        e.preventDefault();
        openPrompt();
      }
    };
    document.addEventListener('keydown', handler);
    return () => document.removeEventListener('keydown', handler);
  }, [zoomedPanel, setZoomedPanel, openPrompt]);

  const handleOpenFile = useCallback((path: string) => {
    openFile(path);
    setActiveTab('editor');
    if (isMobile) setMobileFilesOpen(false);
  }, [openFile, isMobile]);

  const handleCloseFile = useCallback(() => {
    setSelectedFile(null);
    setEditing(false);
    setActiveTab('terminal');
  }, [setSelectedFile]);

  // Resizable panels (desktop)
  const handleMouseDown = useCallback((side: 'left' | 'right', e: React.MouseEvent) => {
    e.preventDefault();
    resizingRef.current = side;
    const startX = e.clientX;
    const startWidth = side === 'left' ? leftWidth : rightWidth;
    let rafId = 0;

    const onMouseMove = (e: MouseEvent) => {
      if (!resizingRef.current) return;
      cancelAnimationFrame(rafId);
      rafId = requestAnimationFrame(() => {
        const delta = e.clientX - startX;
        const newWidth = Math.max(
          PANEL_MIN,
          Math.min(PANEL_MAX, side === 'left' ? startWidth + delta : startWidth - delta),
        );
        if (side === 'left') setLeftWidth(newWidth);
        else setRightWidth(newWidth);
      });
    };

    const onMouseUp = () => {
      const side = resizingRef.current;
      resizingRef.current = null;
      cancelAnimationFrame(rafId);
      // Remember the width once the drag ends, not on every frame — one write
      // instead of hundreds, and the value stored is the one actually settled on.
      if (side) {
        try {
          const el = side === 'left' ? leftWidthRef : rightWidthRef;
          localStorage.setItem(`pcd:panel:${side}`, String(el.current));
        } catch { /* private mode — the panel just won't remember */ }
      }
      document.removeEventListener('mousemove', onMouseMove);
      document.removeEventListener('mouseup', onMouseUp);
    };

    document.addEventListener('mousemove', onMouseMove);
    document.addEventListener('mouseup', onMouseUp);
  }, [leftWidth, rightWidth]);

  if (!agentId) return null;

  if (!agent) {
    return (
      <div className="flex items-center justify-center h-[100dvh] text-deck-text-dim">Loading...</div>
    );
  }

  const AgentIcon = AGENT_ICON_MAP[agent.preset];
  const fileName = selectedFile ? selectedFile.split('/').pop() || '' : '';

  // ──────────── MOBILE LAYOUT ────────────
  if (isMobile) {
    return (
      <div className="flex flex-col h-full safe-top bg-deck-bg overflow-hidden">
        {/* Header */}
        <header className="flex items-center gap-2 px-3 py-2 bg-deck-surface border-b border-deck-border shrink-0">
          <button onClick={() => navigate('/dashboard')} className="p-1.5 -ml-1 rounded active:bg-deck-border/30">
            <IconBack size={16} />
          </button>
          {AgentIcon && <AgentIcon size={18} />}
          <span className="font-medium text-sm truncate flex-1">{agent.name}</span>
          <StatusBadge status={agent.status} />
          {/* Full reload — iOS standalone PWA has no browser refresh, so a wedged
              session would otherwise be unrecoverable without deleting the app. */}
          <button
            onClick={() => window.location.reload()}
            className="p-1.5 rounded active:bg-deck-border/30 text-deck-text-dim"
            title="새로고침 — 세션이 멈췄을 때"
          >
            <IconRefresh size={16} />
          </button>
          <button
            onClick={() => setMobileSessionsOpen(true)}
            className="p-1.5 rounded active:bg-deck-border/30 text-sm"
            title="지난 세션 기록"
          >
            <IconHistory size={16} />
          </button>
          {handoffEnabled && (
            <button
              onClick={() => setHandoffOpen(true)}
              className="p-1.5 rounded active:bg-deck-border/30 text-sm"
              title="모바일에서 이어하기"
            >
              <IconPhone size={16} />
            </button>
          )}
          <button
            onClick={() => setMobileAnimOpen(true)}
            className={`p-1.5 rounded active:bg-deck-border/30 ${mobileAnimOpen ? 'bg-purple-500/20' : ''}`}
            title="Animation"
          >
            <svg width="16" height="16" viewBox="0 0 16 16" fill="none" style={{ flexShrink: 0 }}>
              <circle cx="8" cy="8" r="3" stroke="#a855f7" strokeWidth="1.2" fill="none" />
              <circle cx="8" cy="8" r="6" stroke="#a855f7" strokeWidth="0.8" fill="none" strokeDasharray="2 2" />
              <circle cx="8" cy="4" r="1.2" fill="#a855f7" />
              <circle cx="11.5" cy="10" r="1.2" fill="#a855f7" opacity="0.6" />
              <circle cx="4.5" cy="10" r="1.2" fill="#a855f7" opacity="0.6" />
            </svg>
          </button>
          <button
            onClick={() => setMobileBrowserOpen(true)}
            className="p-1.5 rounded active:bg-deck-border/30 text-sm"
            title="Browser"
          >
            <IconGlobe size={16} />
          </button>
          <button onClick={() => setMobileFilesOpen(true)} className="p-1.5 rounded active:bg-deck-border/30">
            <IconFiles size={16} />
          </button>
        </header>

        {/* Tabs when file is open */}
        {selectedFile && fileContent !== null && (
          <div className="flex shrink-0 bg-deck-surface border-b border-deck-border">
            <button
              onClick={() => setActiveTab('terminal')}
              className={`flex items-center gap-1.5 px-4 py-2 text-sm border-r border-deck-border ${
                activeTab === 'terminal' ? 'bg-deck-bg text-deck-text' : 'text-deck-text-dim'
              }`}
            >
              <IconTerminal size={12} /> Terminal
            </button>
            <button
              onClick={() => setActiveTab('editor')}
              className={`flex items-center gap-1.5 px-4 py-2 text-sm ${
                activeTab === 'editor' ? 'bg-deck-bg text-deck-text' : 'text-deck-text-dim'
              }`}
            >
              {fileName}
              <button onClick={(e) => { e.stopPropagation(); handleCloseFile(); }} className="p-1 rounded active:bg-deck-border/30 ml-1">
                <IconClose size={10} />
              </button>
            </button>
          </div>
        )}

        {/* Sub-agent bar (tap to open animation panel) */}
        <div onClick={() => setMobileAnimOpen(true)} className="cursor-pointer">
          <SubAgentBar agentId={agentId} />
        </div>

        {/* Content — no absolute positioning, flex fills remaining space */}
        {activeTab === 'terminal' && (
          <div className="flex-1 min-w-0 min-h-0 overflow-hidden">
            {usesNative(agent) ? (
              <NativeChat key={agentId} agentId={agentId} cwd={agent.workingDir} driver={nativeDriver(agent)} />
            ) : (
              <TerminalView
                key={agentId}
                ref={terminalApiRef}
                agentId={agentId}
                onFocusTerminal={focusTerminal}
                onHangulDirect={handleHangulDirectInput}
              />
            )}
          </div>
        )}
        {selectedFile && fileContent !== null && activeTab === 'editor' && (
          <div className="flex-1 min-h-0 overflow-hidden">
            {editing ? (
              <FileEditor
                path={selectedFile}
                content={fileContent}
                onSave={async (content) => { await saveFile(selectedFile, content); setEditing(false); }}
                onCancel={() => setEditing(false)}
              />
            ) : (
              <FilePreview path={selectedFile} content={fileContent} agentId={agentId} onEdit={() => setEditing(true)} />
            )}
          </div>
        )}

        {/* Bottom input — mandatory Prompt Bar (한글/긴 프롬프트) + terminal
            control keys. Korean is composed in the Prompt Bar's textarea and
            pasted into the terminal; direct typing into the terminal would split jamo. */}
        {activeTab === 'terminal' && !usesNative(agent) && (
          <>
            {!UNIFIED_INPUT && (
              <PromptBar
                agentId={agentId}
                forced
                collapsed={promptCollapsed}
                onToggleCollapse={() => setPromptCollapsed((c) => !c)}
                onClose={() => {}}
                onFocusTerminal={focusTerminal}
                onFocusChange={handlePromptFocusChange}
              />
            )}
            <MobileToolbar agentId={agentId} sendKey={sendTerminalKey} onCopy={handleCopy} onPaste={handlePaste} />
          </>
        )}

        {/* Hangul-in-terminal hint (once per session) */}
        {hangulToast && (
          <div className="fixed left-1/2 -translate-x-1/2 bottom-32 z-50 px-4 py-2 rounded-lg text-xs text-center shadow-xl bg-deck-surface border border-deck-accent/40 text-deck-text max-w-[90%]">
            한글 입력은 하단 <span className="text-deck-accent font-medium">Prompt Bar</span>를 사용하면 자모 분리를 피할 수 있습니다.
          </div>
        )}

        {/* Handoff arrival toast */}
        {handoffToast && (
          <div className="fixed left-1/2 -translate-x-1/2 top-16 z-[55] px-4 py-2.5 rounded-lg text-xs text-center shadow-xl bg-deck-surface border border-deck-accent/40 text-deck-text max-w-[90%]">
            PC에서 작업하던 세션에 연결되었습니다.<br />
            한글/긴 프롬프트는 하단 <span className="text-deck-accent font-medium">Prompt Bar</span>에서 입력하세요.
          </div>
        )}

        {/* Continue on Mobile modal */}
        {handoffOpen && (
          <HandoffModal agentId={agentId} agentName={agent.name} onClose={() => setHandoffOpen(false)} />
        )}

        {/* Past-session history bottom sheet */}
        {mobileSessionsOpen && (
          <>
            <div className="fixed inset-0 bg-black/40 z-40" onClick={() => setMobileSessionsOpen(false)} />
            <div className="fixed bottom-0 left-0 right-0 z-50 rounded-t-xl safe-bottom bg-deck-surface border-t border-deck-border animate-slide-up"
                 style={{ height: '80dvh' }}>
              <div className="h-full overflow-hidden rounded-t-xl">
                <SessionHistory agentId={agentId} onClose={() => setMobileSessionsOpen(false)} />
              </div>
            </div>
          </>
        )}

        {/* File bottom sheet */}
        <FileBottomSheet
          open={mobileFilesOpen}
          onClose={() => setMobileFilesOpen(false)}
          agentId={agentId}
          workingDir={agent.workingDir}
        />

        {/* Animation bottom sheet */}
        {mobileAnimOpen && (
          <>
            <div className="fixed inset-0 bg-black/40 z-40" onClick={() => setMobileAnimOpen(false)} />
            <div className="fixed bottom-0 left-0 right-0 z-50 rounded-t-xl safe-bottom bg-deck-surface border-t border-deck-border animate-slide-up"
                 style={{ height: '60dvh' }}>
              <div className="flex justify-center py-2" onClick={() => setMobileAnimOpen(false)}>
                <div className="w-10 h-1 rounded-full bg-deck-border" />
              </div>
              <div className="h-[calc(100%-28px)] overflow-hidden">
                <SubAgentPanel
                  activity={activity}
                  palette={generatePalette(agent?.colorHue ?? 220)}
                  onClose={() => setMobileAnimOpen(false)}
                />
              </div>
            </div>
          </>
        )}

        {/* Browser bottom sheet */}
        {mobileBrowserOpen && (
          <>
            <div className="fixed inset-0 bg-black/40 z-40" onClick={() => setMobileBrowserOpen(false)} />
            <div className="fixed bottom-0 left-0 right-0 z-50 rounded-t-xl safe-bottom bg-deck-surface border-t border-deck-border animate-slide-up"
                 style={{ height: '75dvh' }}>
              <div className="flex justify-center py-2" onClick={() => setMobileBrowserOpen(false)}>
                <div className="w-10 h-1 rounded-full bg-deck-border" />
              </div>
              <div className="h-[calc(100%-28px)] overflow-hidden">
                <BrowserPanel agentId={agentId} onClose={() => setMobileBrowserOpen(false)} />
              </div>
            </div>
          </>
        )}
      </div>
    );
  }

  // ──────────── DESKTOP / TABLET LAYOUT ────────────
  return (
    <div className="flex flex-col h-full safe-top bg-deck-bg overflow-hidden">
      {/* Header */}
      <header className="flex items-center gap-2 px-3 py-1.5 bg-deck-surface border-b border-deck-border shrink-0">
        <button onClick={() => navigate('/dashboard')} className="p-1 rounded hover:bg-deck-border/30">
          <IconBack size={14} />
        </button>
        {AgentIcon && <AgentIcon size={16} />}
        <span className="font-medium text-sm truncate">{agent.name}</span>
        <StatusBadge status={agent.status} />
        <span className="text-xs ml-auto truncate text-deck-text-dim">{agent.workingDir}</span>

        <button
          onClick={() => { if (rightPanelOpen && rightTab === 'sessions') { setRightPanelOpen(false); } else { setRightPanelOpen(true); setRightTab('sessions'); } }}
          className={`text-xs px-2 py-0.5 rounded transition-colors ${
            rightPanelOpen && rightTab === 'sessions' ? 'bg-deck-accent/20 text-deck-accent' : 'bg-deck-bg text-deck-text-dim'
          }`}
          title="지난 세션 기록 보기 · 이어하기 · 삭제"
        >
          <span className="inline-flex items-center gap-1"><IconHistory size={13} /> 세션 기록</span>
        </button>

        {handoffEnabled && (
          <button
            onClick={() => setHandoffOpen(true)}
            className="text-xs px-2 py-0.5 rounded transition-colors bg-deck-bg text-deck-text-dim hover:bg-deck-accent/20 hover:text-deck-accent"
            title="Continue on Mobile — 모바일에서 이어하기"
          >
            <span className="inline-flex items-center gap-1"><IconPhone size={13} /> 이어하기</span>
          </button>
        )}

        {!forcePromptBar && (
          <button
            onClick={() => { if (promptOpen) { setPromptOpen(false); focusTerminal(); } else { openPrompt(); } }}
            className={`text-xs px-2 py-0.5 rounded transition-colors ${
              promptOpen ? 'bg-deck-accent/20 text-deck-accent' : 'bg-deck-bg text-deck-text-dim'
            }`}
            title="Prompt Bar (⌘K / ⌘P)"
          >
            Prompt
          </button>
        )}

        <button
          onClick={() => setLeftPanelOpen(!leftPanelOpen)}
          className={`text-xs px-2 py-0.5 rounded transition-colors ${
            leftPanelOpen ? 'bg-deck-accent/20 text-deck-accent' : 'bg-deck-bg text-deck-text-dim'
          }`}
        >
          Files
        </button>

        <button
          onClick={() => { if (rightPanelOpen && rightTab === 'subagent') { setRightPanelOpen(false); } else { setRightPanelOpen(true); setRightTab('subagent'); } }}
          className={`text-xs px-2 py-0.5 rounded transition-colors ${
            rightPanelOpen && rightTab === 'subagent' ? 'bg-purple-500/20 text-purple-400' : 'bg-deck-bg text-deck-text-dim'
          }`}
        >
          Anim
        </button>
        <button
          onClick={() => { if (rightPanelOpen && rightTab === 'browser') { setRightPanelOpen(false); } else { setRightPanelOpen(true); setRightTab('browser'); } }}
          className={`text-xs px-2 py-0.5 rounded transition-colors ${
            rightPanelOpen && rightTab === 'browser' ? 'bg-cyan-500/20 text-cyan-400' : 'bg-deck-bg text-deck-text-dim'
          }`}
        >
          <IconGlobe size={14} />
        </button>
        <button
          onClick={() => setZoomedPanel(zoomedPanel ? null : 'terminal')}
          className={`text-xs px-2 py-0.5 rounded transition-colors ${
            zoomedPanel ? 'bg-amber-500/20 text-amber-400' : 'bg-deck-bg text-deck-text-dim'
          }`}
          title="Cmd+Shift+Z"
        >
          <IconExpand size={14} />
        </button>
        {/* Full reload — the PWA (esp. iOS standalone) has no browser refresh, so a
            wedged session needs an in-app way to reload. */}
        <button
          onClick={() => window.location.reload()}
          className="text-xs px-2 py-0.5 rounded transition-colors bg-deck-bg text-deck-text-dim hover:bg-deck-accent/20 hover:text-deck-accent"
          title="새로고침 — 세션이 멈췄을 때"
        >
          <IconRefresh size={14} />
        </button>
      </header>

      {/* Three-panel layout */}
      <div className="flex flex-1 min-h-0 overflow-hidden">
        {/* Left: File explorer */}
        {leftPanelOpen && !zoomedPanel && (
          <>
            <div className="shrink-0 flex flex-col overflow-hidden min-h-0 border-r border-deck-border" style={{ width: `${leftWidth}px` }}>
              <FileExplorer
                tree={tree}
                changedFiles={changedFiles}
                onSelect={handleOpenFile}
                onRefresh={fetchTree}
                onMkdir={createDir}
                onNewFile={createFile}
                onRename={renameFile}
                onDelete={deleteFile}
                workingDir={agent.workingDir}
              />
            </div>
            <div
              className="w-1 cursor-col-resize shrink-0 hover:opacity-100 opacity-0 transition-opacity bg-deck-accent"
              onMouseDown={(e) => handleMouseDown('left', e)}
            />
          </>
        )}

        {/* Center: Terminal + Editor */}
        <div className="flex-1 min-w-0 min-h-0 flex flex-col overflow-hidden">
          {/* Tab bar when file is open */}
          {selectedFile && fileContent !== null && (
            <div className="flex shrink-0 bg-deck-surface border-b border-deck-border">
              <button
                onClick={() => setActiveTab('terminal')}
                className={`flex items-center gap-1.5 px-3 py-1 text-xs border-r border-deck-border ${
                  activeTab === 'terminal' ? 'bg-deck-bg text-deck-text' : 'text-deck-text-dim'
                }`}
              >
                <IconTerminal size={12} /> Terminal
              </button>
              <button
                onClick={() => setActiveTab('editor')}
                className={`flex items-center gap-1.5 px-3 py-1 text-xs border-r border-deck-border ${
                  activeTab === 'editor' ? 'bg-deck-bg text-deck-text' : 'text-deck-text-dim'
                }`}
              >
                {fileName}
                <button onClick={(e) => { e.stopPropagation(); handleCloseFile(); }}
                        className="p-0.5 rounded hover:bg-deck-border/30 ml-1">
                  <IconClose size={8} />
                </button>
              </button>
            </div>
          )}

          {/* Content — flex fills remaining space, no absolute */}
          {activeTab === 'terminal' && (
            <div className="flex-1 min-h-0">
              {usesNative(agent) ? (
                <NativeChat key={agentId} agentId={agentId} cwd={agent.workingDir} driver={nativeDriver(agent)} />
              ) : (
                <TerminalView
                  key={agentId}
                  ref={terminalApiRef}
                  agentId={agentId}
                  onFocusTerminal={focusTerminal}
                  onHangulDirect={handleHangulDirectInput}
                />
              )}
            </div>
          )}
          {selectedFile && fileContent !== null && activeTab === 'editor' && (
            <div className="flex-1 min-h-0 overflow-hidden">
              {editing ? (
                <FileEditor
                  path={selectedFile}
                  content={fileContent}
                  onSave={async (content) => { await saveFile(selectedFile, content); setEditing(false); }}
                  onCancel={() => setEditing(false)}
                />
              ) : (
                <FilePreview path={selectedFile} content={fileContent} agentId={agentId} onEdit={() => setEditing(true)} />
              )}
            </div>
          )}

          {/* Bottom controls — optional Prompt Bar (desktop) / mandatory on
              iPad + touch, plus PTY control keys (arrows / Enter / Esc / …).
              Terminal sessions only: these write raw bytes to a PTY, and a native
              session has none, so on native chat every key here was a silent no-op
              (engine.Write finds no session and drops it). The mobile branch already
              guarded this; desktop did not, leaving a full row of dead buttons.
              Nothing is lost in native — 중단 replaces Esc/Ctrl+C, approval cards
              replace y/n, question buttons replace the arrows, and Shift+Tab lives
              in the composer as the permission-mode switch. */}
          {activeTab === 'terminal' && !usesNative(agent) && (
            <>
              {!UNIFIED_INPUT && (forcePromptBar || promptOpen) && (
                <PromptBar
                  agentId={agentId}
                  forced={forcePromptBar}
                  collapsed={promptCollapsed}
                  onToggleCollapse={() => setPromptCollapsed((c) => !c)}
                  onClose={() => { setPromptOpen(false); focusTerminal(); }}
                  onFocusTerminal={focusTerminal}
                  onFocusChange={handlePromptFocusChange}
                  autoFocus={!forcePromptBar}
                />
              )}
              <TerminalKeyBar
                agentId={agentId}
                onKeySent={() => terminalApiRef.current?.focus()}
                sendKey={sendTerminalKey}
                isTouch={isTouchDevice}
              />
            </>
          )}
        </div>

        {/* Right: Sub-agent panel */}
        {rightPanelOpen && !zoomedPanel && (
          <>
            <div
              className="w-1 cursor-col-resize shrink-0 hover:opacity-100 opacity-0 transition-opacity bg-purple-500"
              onMouseDown={(e) => handleMouseDown('right', e)}
            />
            <div className="shrink-0 flex flex-col overflow-hidden min-h-0 border-l border-deck-border" style={{ width: `${rightWidth}px` }}>
              {rightTab === 'browser' ? (
                <BrowserPanel agentId={agentId} onClose={() => setRightPanelOpen(false)} />
              ) : rightTab === 'sessions' ? (
                <SessionHistory agentId={agentId} onClose={() => setRightPanelOpen(false)} />
              ) : (
                <SubAgentPanel activity={activity} palette={generatePalette(agent?.colorHue ?? 220)} onClose={() => setRightPanelOpen(false)} />
              )}
            </div>
          </>
        )}
      </div>

      {/* Hangul-in-terminal hint (once per session) */}
      {hangulToast && (
        <div className="fixed left-1/2 -translate-x-1/2 bottom-24 z-50 px-4 py-2 rounded-lg text-xs text-center shadow-xl bg-deck-surface border border-deck-accent/40 text-deck-text max-w-[90%]">
          한글 입력은 하단 <span className="text-deck-accent font-medium">Prompt Bar</span>를 사용하면 자모 분리를 피할 수 있습니다.
        </div>
      )}

      {/* Handoff arrival toast */}
      {handoffToast && (
        <div className="fixed left-1/2 -translate-x-1/2 top-16 z-[55] px-4 py-2.5 rounded-lg text-xs text-center shadow-xl bg-deck-surface border border-deck-accent/40 text-deck-text max-w-[90%]">
          PC에서 작업하던 세션에 연결되었습니다. 한글/긴 프롬프트는 <span className="text-deck-accent font-medium">Prompt Bar</span>에서 입력하세요.
        </div>
      )}

      {/* Continue on Mobile modal */}
      {handoffOpen && (
        <HandoffModal agentId={agentId} agentName={agent.name} onClose={() => setHandoffOpen(false)} />
      )}
    </div>
  );
}
