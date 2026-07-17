import { useState } from 'react';
import { agentDeckWS } from '../../lib/ws';
import { TERMINAL_KEYS } from './TerminalKeyBar';
import { IconCopy, IconPaste } from '../icons';

interface MobileToolbarProps {
  agentId: string;
  /** Routes the key through the terminal so arrow keys honor the app-cursor-key
   * mode (DECCKM). Falls back to a raw PTY write when unavailable. */
  sendKey?: (data: string) => void;
  /** Copy the current terminal selection. Resolves false if nothing is selected. */
  onCopy: () => Promise<boolean>;
  /** Read the clipboard and paste into the input. Resolves false if empty/blocked. */
  onPaste: () => Promise<boolean>;
}

// PTY control keys are shared with the desktop key bar (TERMINAL_KEYS) so both
// surfaces expose the same navigation / choice / signal keys.
const KEYS = TERMINAL_KEYS;

type Flash = 'idle' | 'ok' | 'fail';

export function MobileToolbar({ agentId, sendKey, onCopy, onPaste }: MobileToolbarProps) {
  const [copy, setCopy] = useState<Flash>('idle');
  const [paste, setPaste] = useState<Flash>('idle');

  const send = (data: string) => {
    if (sendKey) sendKey(data);
    else agentDeckWS.send('terminal:input', { agentId, data });
  };

  const flash = (set: (f: Flash) => void, ok: boolean) => {
    set(ok ? 'ok' : 'fail');
    window.setTimeout(() => set('idle'), 1300);
  };

  return (
    <div className="flex gap-2 px-3 py-2 overflow-x-auto scrollbar-hide safe-bottom bg-deck-surface border-t border-deck-border">
      {/* Paste MUST fire from a click, not pointerdown: mobile Safari only grants
          navigator.clipboard.readText() from a genuine click handler (and only over
          https). From pointerdown it silently denies → looked like an empty clipboard.
          Icon-only; success/failure is shown by color (green/red) since there's no label. */}
      <button
        onClick={() => { onPaste().then((ok) => flash(setPaste, ok)); }}
        className={`shrink-0 p-2.5 rounded-lg select-none touch-manipulation active:opacity-70 bg-deck-accent/20 ${
          paste === 'ok' ? 'text-green-400' : paste === 'fail' ? 'text-red-400' : 'text-deck-accent'
        }`}
        title="클립보드 붙여넣기"
        aria-label="붙여넣기"
      >
        <IconPaste size={18} />
      </button>
      {/* Copy the current terminal selection. preventDefault so tapping the button
          doesn't clear the selection before we read it. Icon-only, color feedback. */}
      <button
        onPointerDown={(e) => { e.preventDefault(); onCopy().then((ok) => flash(setCopy, ok)); }}
        className={`shrink-0 p-2.5 rounded-lg select-none touch-manipulation active:opacity-70 bg-deck-bg ${
          copy === 'ok' ? 'text-green-400' : copy === 'fail' ? 'text-red-400' : 'text-deck-text'
        }`}
        title="선택 영역 복사"
        aria-label="복사"
      >
        <IconCopy size={18} />
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
