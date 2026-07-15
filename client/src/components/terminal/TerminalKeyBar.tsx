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
  /** Routes the key through the terminal so arrow keys honor the app-cursor-key
   * mode (DECCKM). Falls back to a raw PTY write when unavailable. */
  sendKey?: (data: string) => void;
  /** Touch device (iPad etc.): enlarge tap targets and lift the bar clear of the
   * home-indicator so the arrow / Enter keys aren't sitting under the home bar. */
  isTouch?: boolean;
}

/** Horizontal, scrollable row of PTY control keys (desktop / tablet). */
export function TerminalKeyBar({ agentId, onKeySent, sendKey, isTouch }: TerminalKeyBarProps) {
  const send = (data: string) => {
    if (sendKey) sendKey(data);
    else agentDeckWS.send('terminal:input', { agentId, data });
    onKeySent?.();
  };

  return (
    <div
      className={`flex overflow-x-auto scrollbar-hide bg-deck-surface border-t border-deck-border/50 ${
        isTouch ? 'gap-2 px-3 pt-2' : 'gap-1 px-2 pt-1 pb-1'
      }`}
      // Add the home-indicator inset to the bottom padding on touch so the keys
      // clear the iPad/iPhone home bar; env() resolves to 0 on desktop.
      style={isTouch ? { paddingBottom: 'calc(0.5rem + env(safe-area-inset-bottom))' } : undefined}
    >
      {TERMINAL_KEYS.map((key) => (
        <button
          key={key.label}
          // pointerDown fires once for both mouse and touch (a plain onMouseDown
          // double-fires on iPad after the synthesized mouse event); preventDefault
          // keeps focus on the terminal so keyboard input is uninterrupted.
          onPointerDown={(e) => { e.preventDefault(); send(key.data); }}
          className={`shrink-0 font-mono select-none transition-colors active:opacity-70 ${
            isTouch ? 'px-4 py-2.5 rounded-lg text-sm touch-manipulation' : 'px-2.5 py-1 rounded text-xs'
          } ${
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
