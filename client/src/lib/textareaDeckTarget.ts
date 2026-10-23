import type { DeckTarget, DeckKeyMods } from '../components/terminal/DeckKeyboard';

// Sets a React-controlled textarea's value the way typing does: through the
// native setter (React's own setter would swallow the change) and an `input`
// event, so the component's onChange runs and its state stays the source of
// truth (slash-command matching, history reset, auto-grow).
const nativeValue = Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value')?.set;

function setValue(ta: HTMLTextAreaElement, value: string, caret: number) {
  nativeValue?.call(ta, value);
  ta.dispatchEvent(new Event('input', { bubbles: true }));
  ta.setSelectionRange(caret, caret);
}

/**
 * Lets the deck keyboard type into a chat composer. Text and Hangul land at the
 * caret. Other keys are dispatched as keydown events, so the composer's own
 * handlers decide (Enter sends, Shift+Tab cycles the permission mode, Esc
 * interrupts, ↑/↓ recall history, the / command picker); a key they leave alone
 * gets the default a physical key would have (Backspace deletes, ← → move the
 * caret). The events are untrusted, so the physical-keyboard detector ignores them.
 */
export function textareaDeckTarget(get: () => HTMLTextAreaElement | null): DeckTarget {
  let preeditLen = 0;
  let caretAfter = -1; // where the last compose left the caret

  const compose = (commit: string, preedit: string) => {
    const ta = get();
    if (!ta) return;
    const v = ta.value;
    const end = ta.selectionEnd;
    const start = Math.max(0, (preeditLen && end === caretAfter ? end - preeditLen : ta.selectionStart));
    const caret = start + commit.length + preedit.length;
    setValue(ta, v.slice(0, start) + commit + preedit + v.slice(end), caret);
    preeditLen = preedit.length;
    caretAfter = caret;
  };

  const pressKey = (key: string, mods: DeckKeyMods) => {
    const ta = get();
    preeditLen = 0;
    if (!ta) return;
    const ev = new KeyboardEvent('keydown', {
      key, ctrlKey: !!mods.ctrl, altKey: !!mods.alt, shiftKey: !!mods.shift, bubbles: true, cancelable: true,
    });
    if (!ta.dispatchEvent(ev) || mods.ctrl || mods.alt) return; // handled, or a combination with no default
    const v = ta.value;
    const s = ta.selectionStart, e = ta.selectionEnd;
    const lineStart = v.lastIndexOf('\n', s - 1) + 1;
    const lineEnd = v.indexOf('\n', e) === -1 ? v.length : v.indexOf('\n', e);
    switch (key) {
      case 'Backspace':
        if (s !== e) setValue(ta, v.slice(0, s) + v.slice(e), s);
        else if (s > 0) setValue(ta, v.slice(0, s - 1) + v.slice(s), s - 1);
        break;
      case 'Delete':
        if (s !== e) setValue(ta, v.slice(0, s) + v.slice(e), s);
        else if (s < v.length) setValue(ta, v.slice(0, s) + v.slice(s + 1), s);
        break;
      case 'ArrowLeft': ta.setSelectionRange(Math.max(0, s - (s === e ? 1 : 0)), Math.max(0, s - (s === e ? 1 : 0))); break;
      case 'ArrowRight': ta.setSelectionRange(Math.min(v.length, e + (s === e ? 1 : 0)), Math.min(v.length, e + (s === e ? 1 : 0))); break;
      case 'Home': ta.setSelectionRange(lineStart, lineStart); break;
      case 'End': ta.setSelectionRange(lineEnd, lineEnd); break;
      case 'ArrowUp': case 'PageUp': {
        // Same column on the previous line, or the start of the text.
        if (lineStart === 0) { ta.setSelectionRange(0, 0); break; }
        const prevStart = v.lastIndexOf('\n', lineStart - 2) + 1;
        const at = Math.min(prevStart + (s - lineStart), lineStart - 1);
        ta.setSelectionRange(at, at);
        break;
      }
      case 'ArrowDown': case 'PageDown': {
        if (lineEnd === v.length) { ta.setSelectionRange(v.length, v.length); break; }
        const nextEnd = v.indexOf('\n', lineEnd + 1) === -1 ? v.length : v.indexOf('\n', lineEnd + 1);
        const at = Math.min(lineEnd + 1 + (s - lineStart), nextEnd);
        ta.setSelectionRange(at, at);
        break;
      }
    }
  };

  return {
    compose,
    pressKey,
    composing: () => {
      const ta = get();
      return preeditLen > 0 && !!ta && ta.selectionEnd === caretAfter;
    },
    focusSystemKeyboard: () => {
      const ta = get();
      if (!ta) return;
      ta.inputMode = 'text';
      ta.blur();
      ta.focus({ preventScroll: true });
    },
  };
}
