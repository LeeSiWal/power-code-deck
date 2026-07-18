import { useState, useEffect, useRef, useMemo, useCallback } from 'react';
import { useNavigate } from 'react-router-dom';
import { useAppStore } from '../stores/appStore';

interface Command {
  id: string;
  label: string;
  shortcut?: string;
  category: string;
  action: () => void;
  keywords?: string[];
}

function fuzzyMatch(query: string, text: string): boolean {
  let qi = 0;
  const q = query.toLowerCase();
  const t = text.toLowerCase();
  for (let ti = 0; ti < t.length && qi < q.length; ti++) {
    if (t[ti] === q[qi]) qi++;
  }
  return qi === q.length;
}

export function CommandPalette() {
  const { commandPaletteOpen, setCommandPaletteOpen, agents, setZoomedPanel, zoomedPanel } = useAppStore();
  const [query, setQuery] = useState('');
  const [selectedIndex, setSelectedIndex] = useState(0);
  const inputRef = useRef<HTMLInputElement>(null);
  const listRef = useRef<HTMLDivElement>(null);
  const navigate = useNavigate();

  const commands = useMemo<Command[]>(() => {
    const cmds: Command[] = [];

    agents.forEach((agent, i) => {
      cmds.push({
        id: `agent-${agent.id}`,
        label: `${agent.name} 열기`,
        shortcut: i < 9 ? `\u2318${i + 1}` : undefined,
        category: '에이전트',
        action: () => navigate(`/agents/${agent.id}`),
        keywords: [agent.preset, agent.workingDir],
      });
    });

    cmds.push({
      id: 'new-agent',
      label: '새 에이전트 만들기',
      shortcut: '\u2318N',
      category: '에이전트',
      action: () => navigate('/'),
    });

    cmds.push(
      { id: 'nav-dashboard', label: '대시보드', shortcut: '\u2318D', category: '이동', action: () => navigate('/dashboard') },
      { id: 'nav-projects', label: '프로젝트 선택', shortcut: '\u2318O', category: '이동', action: () => navigate('/') },
      { id: 'nav-logs', label: '로그', shortcut: '\u2318L', category: '이동', action: () => navigate('/logs') },
      { id: 'nav-settings', label: '설정', category: '이동', action: () => navigate('/settings') },
    );

    cmds.push({
      id: 'zoom-toggle',
      label: zoomedPanel ? '패널 줌 해제' : '패널 줌 토글',
      shortcut: '\u2318\u21E7Z',
      category: '보기',
      action: () => setZoomedPanel(zoomedPanel ? null : 'terminal'),
    });

    return cmds;
  }, [agents, navigate, zoomedPanel, setZoomedPanel]);

  const filtered = useMemo(() => {
    if (!query) return commands;
    return commands.filter((cmd) => {
      const searchText = `${cmd.label} ${cmd.keywords?.join(' ') || ''}`;
      return fuzzyMatch(query, searchText);
    });
  }, [commands, query]);

  useEffect(() => {
    if (commandPaletteOpen) {
      setQuery('');
      setSelectedIndex(0);
      setTimeout(() => inputRef.current?.focus(), 50);
    }
  }, [commandPaletteOpen]);

  const handleKeyDown = useCallback((e: React.KeyboardEvent) => {
    if (e.key === 'ArrowDown') {
      e.preventDefault();
      setSelectedIndex((i) => Math.min(i + 1, filtered.length - 1));
    } else if (e.key === 'ArrowUp') {
      e.preventDefault();
      setSelectedIndex((i) => Math.max(i - 1, 0));
    } else if (e.key === 'Enter' && filtered[selectedIndex]) {
      e.preventDefault();
      filtered[selectedIndex].action();
      setCommandPaletteOpen(false);
    } else if (e.key === 'Escape') {
      setCommandPaletteOpen(false);
    }
  }, [filtered, selectedIndex, setCommandPaletteOpen]);

  useEffect(() => {
    const el = listRef.current?.children[selectedIndex] as HTMLElement;
    el?.scrollIntoView({ block: 'nearest' });
  }, [selectedIndex]);

  useEffect(() => {
    const handler = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key === 'k') {
        e.preventDefault();
        setCommandPaletteOpen(!commandPaletteOpen);
      }
    };
    document.addEventListener('keydown', handler);
    return () => document.removeEventListener('keydown', handler);
  }, [commandPaletteOpen, setCommandPaletteOpen]);

  if (!commandPaletteOpen) return null;

  let lastCategory = '';

  return (
    <div className="fixed inset-0 z-[100] flex items-start justify-center pt-[15vh]">
      <div className="fixed inset-0 bg-black/50" onClick={() => setCommandPaletteOpen(false)} />
      <div className="relative w-full max-w-lg mx-4 bg-deck-surface border border-deck-border rounded-xl shadow-2xl overflow-hidden">
        <div className="flex items-center gap-2 px-4 py-3 border-b border-deck-border">
          <svg width="16" height="16" viewBox="0 0 16 16" fill="none"><circle cx="7" cy="7" r="5.5" stroke="#8791a4" strokeWidth="1.5" fill="none"/><line x1="11" y1="11" x2="14" y2="14" stroke="#8791a4" strokeWidth="1.5" strokeLinecap="round"/></svg>
          <input
            ref={inputRef}
            type="text"
            value={query}
            onChange={(e) => { setQuery(e.target.value); setSelectedIndex(0); }}
            onKeyDown={handleKeyDown}
            placeholder="검색..."
            className="flex-1 bg-transparent text-sm outline-none text-deck-text placeholder-deck-text-dim"
          />
          <kbd className="text-[10px] px-1.5 py-0.5 rounded bg-deck-bg text-deck-text-dim border border-deck-border">ESC</kbd>
        </div>

        <div ref={listRef} className="max-h-[50vh] overflow-y-auto py-2 min-h-0">
          {filtered.length === 0 && (
            <div className="px-4 py-6 text-center text-sm text-deck-text-dim">결과 없음</div>
          )}
          {filtered.map((cmd, i) => {
            const showCategory = cmd.category !== lastCategory;
            lastCategory = cmd.category;
            return (
              <div key={cmd.id}>
                {showCategory && (
                  <div className="px-4 py-1 text-[10px] font-medium uppercase tracking-wider text-deck-text-dim">
                    {cmd.category}
                  </div>
                )}
                <button
                  onClick={() => { cmd.action(); setCommandPaletteOpen(false); }}
                  onMouseEnter={() => setSelectedIndex(i)}
                  className={`w-full text-left px-4 py-2 text-sm flex items-center justify-between transition-colors ${
                    i === selectedIndex ? 'bg-deck-accent/10 text-deck-text' : 'text-deck-text-dim hover:bg-deck-border/30'
                  }`}
                >
                  <span>{cmd.label}</span>
                  {cmd.shortcut && (
                    <kbd className="text-[10px] px-1.5 py-0.5 rounded bg-deck-bg text-deck-text-dim border border-deck-border ml-2">
                      {cmd.shortcut}
                    </kbd>
                  )}
                </button>
              </div>
            );
          })}
        </div>
      </div>
    </div>
  );
}
