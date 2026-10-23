import { useMemo, type RefObject } from 'react';
import { DeckKeyboard } from '../terminal/DeckKeyboard';
import { textareaDeckTarget } from '../../lib/textareaDeckTarget';
import { readClipboard, writeClipboard } from '../../lib/clipboard';
import type { KeyboardModeState } from '../../hooks/useKeyboardMode';

/** The page's keyboard mode plus the screen size the deck keyboard is sized for;
 *  undefined where the deck keyboard does not fit (phones, desktops). */
export type ChatKeyboard = KeyboardModeState & { width: number; height: number };

/** The deck keyboard under a chat composer, typing into its textarea. */
export function ChatDeckKeyboard({ taRef, keyboard }: { taRef: RefObject<HTMLTextAreaElement>; keyboard?: ChatKeyboard }) {
  const target = useMemo(() => textareaDeckTarget(() => taRef.current), [taRef]);
  if (!keyboard?.deck || keyboard.hidden) return null;
  return (
    <DeckKeyboard
      target={() => target}
      width={keyboard.width}
      height={keyboard.height}
      onSystemKeyboard={keyboard.switchToSystem}
      onHide={() => keyboard.setHidden(true)}
      insetBottom={false} // the composer is already padded for the home indicator
      onPaste={async () => {
        const text = await readClipboard();
        if (!text) return false;
        target.compose(text, '');
        return true;
      }}
      onCopy={async () => {
        const text = window.getSelection()?.toString() || '';
        return text ? writeClipboard(text) : false;
      }}
    />
  );
}

/** Brings the deck keyboard back (hidden, or the system keyboard in use). */
export function ChatDeckToggle({ keyboard }: { keyboard?: ChatKeyboard }) {
  if (!keyboard || (keyboard.deck && !keyboard.hidden)) return null;
  return (
    <button
      onPointerDown={(e) => e.preventDefault()}
      onClick={keyboard.switchToDeck}
      className="shrink-0 h-8 px-2.5 rounded-lg bg-deck-accent/20 text-deck-accent text-xs"
      title="덱 키보드 (Esc·Ctrl·방향키·한/영이 있는 화면 키보드)"
    >
      ⌨︎ 덱 키보드
    </button>
  );
}
