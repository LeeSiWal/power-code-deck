import { agentDeckWS } from '../../lib/ws';

/**
 * Control keys sent straight to the PTY. The Prompt Bar handles text entry;
 * these handle navigation, choices and signals for interactive CLIs (arrow-key
 * menus, y/n prompts, Ctrl+C, …). Shared by the desktop key bar and the mobile
 * toolbar so both surfaces expose the same keys.
 */
export const TERMINAL_KEYS: { label: string; data: string; accent?: boolean }[] = [
  { label: '↑', data: '\x1b[A' },
  { label: '↓', data: '\x1b[B' },
  { label: '←', data: '\x1b[D' },
  { label: '→', data: '\x1b[C' },
  { label: 'Enter', data: '\r', accent: true },
  { label: 'Esc', data: '\x1b' },
  { label: 'Tab', data: '\t' },
  { label: '⇧Tab', data: '\x1b[Z' },
  { label: 'y', data: 'y' },
  { label: 'n', data: 'n' },
  { label: 'Ctrl+C', data: '\x03' },
  { label: 'Ctrl+D', data: '\x04' },
];

interface TerminalKeyBarProps {
  agentId: string;
  /** Called after a key is sent — desktop uses this to refocus the terminal so
   * the physical keyboard keeps working. */
  onKeySent?: () => void;
}

/** Horizontal, scrollable row of PTY control keys (desktop / tablet). */
export function TerminalKeyBar({ agentId, onKeySent }: TerminalKeyBarProps) {
  const send = (data: string) => {
    agentDeckWS.send('terminal:input', { agentId, data });
    onKeySent?.();
  };

  return (
    <div className="flex gap-1 px-2 py-1 overflow-x-auto scrollbar-hide bg-deck-surface border-t border-deck-border/50">
      {TERMINAL_KEYS.map((key) => (
        <button
          key={key.label}
          // mouseDown + preventDefault keeps focus on the terminal instead of
          // stealing it to the button, so keyboard input is uninterrupted.
          onMouseDown={(e) => { e.preventDefault(); send(key.data); }}
          className={`shrink-0 px-2.5 py-1 rounded text-xs font-mono select-none transition-colors active:opacity-70 ${
            key.accent
              ? 'bg-deck-accent/20 text-deck-accent hover:bg-deck-accent/30'
              : 'bg-deck-bg text-deck-text hover:bg-deck-border/40'
          }`}
        >
          {key.label}
        </button>
      ))}
    </div>
  );
}
