import { useState, useEffect, useCallback, useRef } from 'react';
import { useParams, useNavigate, useSearchParams } from 'react-router-dom';
import { TerminalView, type TerminalHandle } from '../components/terminal/TerminalView';
import { MobileToolbar } from '../components/terminal/MobileToolbar';
import { TerminalKeyBar } from '../components/terminal/TerminalKeyBar';
import { PromptBar } from '../components/terminal/PromptBar';
import { HandoffModal } from '../components/terminal/HandoffModal';
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
import { useSubAgents } from '../hooks/useSubAgents';
import { IconBack, IconFiles, IconClose, IconTerminal, AGENT_ICON_MAP } from '../components/icons';
import { api } from '../lib/api';
import { generatePalette } from '../lib/paletteGenerator';
import { useAppStore } from '../stores/appStore';

type CenterTab = 'terminal' | 'editor';

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

  // Single interactive terminal + a device-aware Prompt Bar for Korean / long
  // prompts. Touch devices (mobile / iPad) can't reliably compose Korean in
  // xterm, so the Prompt Bar is mandatory there — it can only be collapsed, not
  // closed. On desktop it is an optional overlay toggled by shortcut / button.
  const forcePromptBar = isTouchDevice;
  const terminalApiRef = useRef<TerminalHandle | null>(null);
  const promptFocusedRef = useRef(false); // suspends terminal auto-focus while typing in the Prompt Bar
  const [promptOpen, setPromptOpen] = useState(forcePromptBar);
  const [promptCollapsed, setPromptCollapsed] = useState(false);
  const [hangulHintShown, setHangulHintShown] = useState(false);
  const [hangulToast, setHangulToast] = useState(false);

  const focusTerminal = useCallback(() => {
    promptFocusedRef.current = false;
    terminalApiRef.current?.focus();
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
  const [rightTab, setRightTab] = useState<'subagent' | 'browser'>('subagent');
  const [mobileFilesOpen, setMobileFilesOpen] = useState(false);
  const [mobileAnimOpen, setMobileAnimOpen] = useState(false);
  const [mobileBrowserOpen, setMobileBrowserOpen] = useState(false);
  const [leftWidth, setLeftWidth] = useState(220);
  const [rightWidth, setRightWidth] = useState(220);
  const [editing, setEditing] = useState(false);
  const [terminalMountKey, setTerminalMountKey] = useState(0);
  const [terminalReady, setTerminalReady] = useState(false);
  const resizingRef = useRef<'left' | 'right' | null>(null);

  const agentId = id || '';

  const {
    tree, selectedFile, fileContent, changedFiles,
    fetchTree, openFile, saveFile, createDir, deleteFile,
    setSelectedFile,
  } = useFileExplorer(agentId || null);

  const { subAgents } = useSubAgents(agentId);

  useEffect(() => {
    if (agentId) {
      api.getAgent(agentId).then(setAgent).catch(() => navigate('/dashboard'));
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

  useEffect(() => {
    if (!agentId) return;

    const navEntry = performance.getEntriesByType('navigation')[0] as PerformanceNavigationTiming | undefined;
    const isDirectReload = navEntry?.type === 'reload';

    const armTerminal = () => {
      window.setTimeout(() => setTerminalReady(true), isDirectReload ? 650 : 0);
    };

    setTerminalReady(false);

    if (document.readyState === 'complete') {
      armTerminal();
      return;
    }

    const onLoad = () => armTerminal();
    window.addEventListener('load', onLoad);
    return () => window.removeEventListener('load', onLoad);
  }, [agentId]);

  useEffect(() => {
    if (!agentId) return;
    const bootstrapKey = `terminal-bootstrap:${agentId}`;
    if (sessionStorage.getItem(bootstrapKey) === 'done') return;

    const timer = window.setTimeout(() => {
      sessionStorage.setItem(bootstrapKey, 'done');
      setTerminalMountKey((key) => key + 1);
    }, 450);

    return () => clearTimeout(timer);
  }, [agentId]);

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
        const newWidth = Math.max(150, Math.min(400, side === 'left' ? startWidth + delta : startWidth - delta));
        if (side === 'left') setLeftWidth(newWidth);
        else setRightWidth(newWidth);
      });
    };

    const onMouseUp = () => {
      resizingRef.current = null;
      cancelAnimationFrame(rafId);
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
          {handoffEnabled && (
            <button
              onClick={() => setHandoffOpen(true)}
              className="p-1.5 rounded active:bg-deck-border/30 text-sm"
              title="모바일에서 이어하기"
            >
              📱
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
            🌐
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
          <div className="flex-1 min-h-0">
            {terminalReady ? (
              <TerminalView
                key={`${agentId}:${terminalMountKey}`}
                ref={terminalApiRef}
                agentId={agentId}
                focusGuardRef={promptFocusedRef}
                onHangulDirect={handleHangulDirectInput}
              />
            ) : (
              <div className="h-full w-full" />
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
              <FilePreview path={selectedFile} content={fileContent} onEdit={() => setEditing(true)} />
            )}
          </div>
        )}

        {/* Bottom input — mandatory Prompt Bar (한글/긴 프롬프트) + terminal
            control keys. Korean is composed in the Prompt Bar's textarea and
            pasted into the terminal; direct xterm typing would split jamo. */}
        {activeTab === 'terminal' && (
          <>
            <PromptBar
              agentId={agentId}
              forced
              collapsed={promptCollapsed}
              onToggleCollapse={() => setPromptCollapsed((c) => !c)}
              onClose={() => {}}
              onFocusTerminal={focusTerminal}
              onFocusChange={(f) => { promptFocusedRef.current = f; }}
            />
            <MobileToolbar agentId={agentId} onOpenPrompt={openPrompt} />
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
                  subAgents={subAgents}
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
    <div className="flex flex-col h-[100dvh] safe-top bg-deck-bg overflow-hidden">
      {/* Header */}
      <header className="flex items-center gap-2 px-3 py-1.5 bg-deck-surface border-b border-deck-border shrink-0">
        <button onClick={() => navigate('/dashboard')} className="p-1 rounded hover:bg-deck-border/30">
          <IconBack size={14} />
        </button>
        {AgentIcon && <AgentIcon size={16} />}
        <span className="font-medium text-sm truncate">{agent.name}</span>
        <StatusBadge status={agent.status} />
        <span className="text-xs ml-auto truncate text-deck-text-dim">{agent.workingDir}</span>

        {handoffEnabled && (
          <button
            onClick={() => setHandoffOpen(true)}
            className="text-xs px-2 py-0.5 rounded transition-colors bg-deck-bg text-deck-text-dim hover:bg-deck-accent/20 hover:text-deck-accent"
            title="Continue on Mobile — 모바일에서 이어하기"
          >
            📱 이어하기
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
          🌐
        </button>
        <button
          onClick={() => setZoomedPanel(zoomedPanel ? null : 'terminal')}
          className={`text-xs px-2 py-0.5 rounded transition-colors ${
            zoomedPanel ? 'bg-amber-500/20 text-amber-400' : 'bg-deck-bg text-deck-text-dim'
          }`}
          title="Cmd+Shift+Z"
        >
          ⛶
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
              {terminalReady ? (
                <TerminalView
                  key={`${agentId}:${terminalMountKey}`}
                  ref={terminalApiRef}
                  agentId={agentId}
                  focusGuardRef={promptFocusedRef}
                  onHangulDirect={handleHangulDirectInput}
                />
              ) : (
                <div className="h-full w-full" />
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
                <FilePreview path={selectedFile} content={fileContent} onEdit={() => setEditing(true)} />
              )}
            </div>
          )}

          {/* Bottom controls — optional Prompt Bar (desktop) / mandatory on
              iPad + touch, plus PTY control keys (arrows / Enter / Esc / …). */}
          {activeTab === 'terminal' && (
            <>
              {(forcePromptBar || promptOpen) && (
                <PromptBar
                  agentId={agentId}
                  forced={forcePromptBar}
                  collapsed={promptCollapsed}
                  onToggleCollapse={() => setPromptCollapsed((c) => !c)}
                  onClose={() => { setPromptOpen(false); focusTerminal(); }}
                  onFocusTerminal={focusTerminal}
                  onFocusChange={(f) => { promptFocusedRef.current = f; }}
                  autoFocus={!forcePromptBar}
                />
              )}
              <TerminalKeyBar
                agentId={agentId}
                onKeySent={() => terminalApiRef.current?.focus()}
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
              ) : (
                <SubAgentPanel subAgents={subAgents} palette={generatePalette(agent?.colorHue ?? 220)} onClose={() => setRightPanelOpen(false)} />
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
