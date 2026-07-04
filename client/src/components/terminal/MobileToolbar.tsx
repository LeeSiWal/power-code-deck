import { agentDeckWS } from '../../lib/ws';
import { TERMINAL_KEYS } from './TerminalKeyBar';

interface MobileToolbarProps {
  agentId: string;
  /** Focus the terminal so the mobile keyboard opens for direct typing. */
  onFocusTerminal: () => void;
}

// PTY control keys are shared with the desktop key bar (TERMINAL_KEYS) so both
// surfaces expose the same navigation / choice / signal keys.
const KEYS = TERMINAL_KEYS;

export function MobileToolbar({ agentId, onFocusTerminal }: MobileToolbarProps) {
  const send = (data: string) => {
    agentDeckWS.send('terminal:input', { agentId, data });
  };

  return (
    <div className="flex gap-2 px-3 py-2.5 overflow-x-auto scrollbar-hide safe-bottom bg-deck-surface border-t border-deck-border">
      <button
        onTouchStart={(e) => { e.preventDefault(); onFocusTerminal(); }}
        onMouseDown={(e) => { e.preventDefault(); onFocusTerminal(); }}
        className="shrink-0 px-4 py-2.5 rounded-lg text-base select-none touch-manipulation active:opacity-70 bg-deck-accent/20 text-deck-accent"
        title="키보드 열기 (터미널에 직접 입력)"
      >
        ⌨
      </button>
      {KEYS.map((key) => (
        <button
          key={key.label}
          onTouchStart={(e) => { e.preventDefault(); send(key.data); }}
          onMouseDown={(e) => { e.preventDefault(); send(key.data); }}
          className={`shrink-0 px-4 py-2.5 rounded-lg text-sm font-mono select-none touch-manipulation active:opacity-70 ${
            (key as any).accent ? 'bg-deck-accent/20 text-deck-accent' : 'bg-deck-bg text-deck-text'
          }`}
        >
          {key.label}
        </button>
      ))}
    </div>
  );
}
