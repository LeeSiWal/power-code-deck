import { agentDeckWS } from '../../lib/ws';

interface MobileToolbarProps {
  agentId: string;
  /** Open the Prompt Bar for Korean / long text entry. */
  onOpenPrompt: () => void;
}

// Keys sent straight to the PTY. Everything here is terminal control — the
// Prompt Bar handles text; these handle navigation, choices and signals.
const KEYS = [
  { label: 'Esc', data: '\x1b' },
  { label: 'Tab', data: '\t' },
  { label: '⇧Tab', data: '\x1b[Z', accent: true },
  { label: 'Enter', data: '\r' },
  { label: 'y', data: 'y' },
  { label: 'n', data: 'n' },
  { label: 'Ctrl+C', data: '\x03' },
  { label: 'Ctrl+D', data: '\x04' },
  { label: '↑', data: '\x1b[A' },
  { label: '↓', data: '\x1b[B' },
  { label: '←', data: '\x1b[D' },
  { label: '→', data: '\x1b[C' },
];

export function MobileToolbar({ agentId, onOpenPrompt }: MobileToolbarProps) {
  const send = (data: string) => {
    agentDeckWS.send('terminal:input', { agentId, data });
  };

  return (
    <div className="flex gap-2 px-3 py-2.5 overflow-x-auto scrollbar-hide safe-bottom bg-deck-surface border-t border-deck-border">
      <button
        onTouchStart={(e) => { e.preventDefault(); onOpenPrompt(); }}
        onMouseDown={(e) => { e.preventDefault(); onOpenPrompt(); }}
        className="shrink-0 px-4 py-2.5 rounded-lg text-sm font-medium select-none touch-manipulation active:opacity-70 bg-deck-accent/20 text-deck-accent"
      >
        Prompt
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
