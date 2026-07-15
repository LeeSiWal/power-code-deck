import { agentDeckWS } from '../../lib/ws';
import { TERMINAL_KEYS } from './TerminalKeyBar';

interface MobileToolbarProps {
  agentId: string;
  /** Expand + focus the Prompt Bar for Korean / long text entry. */
  onOpenPrompt: () => void;
  /** Routes the key through the terminal so arrow keys honor the app-cursor-key
   * mode (DECCKM). Falls back to a raw PTY write when unavailable. */
  sendKey?: (data: string) => void;
}

// PTY control keys are shared with the desktop key bar (TERMINAL_KEYS) so both
// surfaces expose the same navigation / choice / signal keys.
const KEYS = TERMINAL_KEYS;

export function MobileToolbar({ agentId, onOpenPrompt, sendKey }: MobileToolbarProps) {
  const send = (data: string) => {
    if (sendKey) sendKey(data);
    else agentDeckWS.send('terminal:input', { agentId, data });
  };

  return (
    <div className="flex gap-2 px-3 py-2 overflow-x-auto scrollbar-hide safe-bottom bg-deck-surface border-t border-deck-border">
      <button
        // A single pointerdown fires once for both touch and mouse. Having both
        // onTouchStart AND onMouseDown fired twice per tap on touch (touchstart +
        // the synthesized mousedown) — the "여러번 눌림". preventDefault keeps focus
        // on the input so the soft keyboard stays up.
        onPointerDown={(e) => { e.preventDefault(); onOpenPrompt(); }}
        className="shrink-0 px-4 py-2.5 rounded-lg text-sm font-medium select-none touch-manipulation active:opacity-70 bg-deck-accent/20 text-deck-accent"
        title="한글/긴 프롬프트 입력"
      >
        Prompt 입력
      </button>
      {KEYS.map((key) => (
        <button
          key={key.label}
          onPointerDown={(e) => { e.preventDefault(); send(key.data); }}
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
