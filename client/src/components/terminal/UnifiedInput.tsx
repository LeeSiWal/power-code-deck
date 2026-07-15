import { useEffect, useRef, useState, useCallback, useImperativeHandle, forwardRef, type CSSProperties } from 'react';
import type { CustomTerm, CursorRect } from '../../lib/customTerm/CustomTerm';
import { agentDeckWS } from '../../lib/ws';

/** Imperative handle so the terminal key bar's Enter can submit this draft
 *  instead of sending a raw CR that ignores the buffered text. */
export interface UnifiedInputHandle { submit: () => void }

/**
 * UnifiedInput — the experiment behind `?unifiedInput`. It grafts the Prompt Bar's
 * proven IME textarea ONTO the terminal, anchored over the cursor cell, so a single
 * visible input handles Korean/long text AND terminal control keys. Text is composed
 * inline here (native IME works because the textarea is real and visible — unlike the
 * hidden off-screen one that split jamo on mobile) and committed on Enter via the
 * same bracketed-paste path the Prompt Bar uses. Control / navigation keys go
 * straight to the PTY. When the draft buffer is empty, cursor keys and Backspace
 * drive the app (menus, readline); with a draft they edit the draft, as expected.
 */

const NORMAL_CURSOR: Record<string, string> = {
  ArrowUp: '\x1b[A', ArrowDown: '\x1b[B', ArrowRight: '\x1b[C', ArrowLeft: '\x1b[D',
  Home: '\x1b[H', End: '\x1b[F',
};
const APP_CURSOR: Record<string, string> = {
  ArrowUp: '\x1bOA', ArrowDown: '\x1bOB', ArrowRight: '\x1bOC', ArrowLeft: '\x1bOD',
  Home: '\x1bOH', End: '\x1bOF',
};
// Always-to-the-app keys (no draft dependency). Enter/Tab/Escape/Backspace are
// handled explicitly in the keydown branch.
const FIXED: Record<string, string> = {
  Insert: '\x1b[2~', Delete: '\x1b[3~', PageUp: '\x1b[5~', PageDown: '\x1b[6~',
  F1: '\x1bOP', F2: '\x1bOQ', F3: '\x1bOR', F4: '\x1bOS',
  F5: '\x1b[15~', F6: '\x1b[17~', F7: '\x1b[18~', F8: '\x1b[19~',
  F9: '\x1b[20~', F10: '\x1b[21~', F11: '\x1b[23~', F12: '\x1b[24~',
};
const CTRL_SYM: Record<string, string> = { '[': '\x1b', '\\': '\x1c', ']': '\x1d', '^': '\x1e', '_': '\x1f' };

interface UnifiedInputProps {
  term: CustomTerm;
  agentId: string;
  /** Focus the textarea on mount (desktop; skipped on touch to avoid popping the keyboard). */
  autoFocus?: boolean;
  /** Touch device: soft-keyboard Enter arrives as a beforeinput 'insertLineBreak'
   *  (not a keydown), and there's no Shift, so every Enter submits. */
  touch?: boolean;
}

export const UnifiedInput = forwardRef<UnifiedInputHandle, UnifiedInputProps>(function UnifiedInput(
  { term, agentId, autoFocus, touch },
  ref,
) {
  const taRef = useRef<HTMLTextAreaElement>(null);
  const [rect, setRect] = useState<CursorRect | null>(() => term.cursorRect());
  const [value, setValue] = useState('');
  const valueRef = useRef('');
  valueRef.current = value;
  const composingRef = useRef(false);
  const [style, setStyle] = useState<{ family: string; size: string; color: string }>({
    family: 'monospace', size: '14px', color: 'var(--term-fg, #e2e8f0)',
  });

  // Match the terminal's rendered font/color so the draft aligns with the grid.
  useEffect(() => {
    const cs = getComputedStyle(term.element);
    setStyle({ family: cs.fontFamily, size: cs.fontSize, color: cs.color });
  }, [term]);

  // Follow the cursor: CustomTerm calls this on every render (output redraw).
  useEffect(() => {
    setRect(term.cursorRect());
    term.setCursorListener((r) => setRect(r));
    return () => term.setCursorListener(null);
  }, [term]);

  // A click on the terminal (that isn't selecting text) focuses the input.
  useEffect(() => {
    const el = term.element;
    const onClick = () => {
      const sel = window.getSelection();
      if (!sel || sel.isCollapsed) taRef.current?.focus({ preventScroll: true });
    };
    el.addEventListener('click', onClick);
    return () => el.removeEventListener('click', onClick);
  }, [term]);

  useEffect(() => {
    if (autoFocus) taRef.current?.focus({ preventScroll: true });
  }, [autoFocus]);

  const sendInput = useCallback((data: string) => {
    agentDeckWS.send('terminal:input', { agentId, data });
  }, [agentId]);

  // Enter: commit the draft as a bracketed paste (the Prompt Bar's exact path), or,
  // when there's no draft, send a raw CR so it confirms the app's current line/menu.
  const submit = useCallback(() => {
    const text = valueRef.current;
    if (text) {
      agentDeckWS.send('terminal:pasteSubmit', { agentId, text, mode: 'bracketed-paste' });
      setValue('');
    } else {
      sendInput('\r');
    }
  }, [agentId, sendInput]);

  useImperativeHandle(ref, () => ({ submit }), [submit]);

  // Mobile soft keyboards don't emit a keydown Enter — the return key arrives as a
  // beforeinput 'insertLineBreak'. Intercept it to submit (there's no Shift on
  // touch, so every Enter submits). On desktop the keydown handler runs first and
  // preventDefaults, so this never double-fires there.
  useEffect(() => {
    if (!touch) return;
    const ta = taRef.current;
    if (!ta) return;
    const onBeforeInput = (e: Event) => {
      if (composingRef.current) return;
      if ((e as InputEvent).inputType === 'insertLineBreak') {
        e.preventDefault();
        submit();
      }
    };
    ta.addEventListener('beforeinput', onBeforeInput);
    return () => ta.removeEventListener('beforeinput', onBeforeInput);
  }, [touch, submit]);

  const onKeyDown = useCallback((e: React.KeyboardEvent<HTMLTextAreaElement>) => {
    if (e.nativeEvent.isComposing || composingRef.current) return; // never steal keys mid-IME
    const k = e.key;
    const hasDraft = valueRef.current.length > 0;

    if (k === 'Enter' && !e.shiftKey) { e.preventDefault(); submit(); return; }
    if (k === 'Enter' && e.shiftKey) return; // newline into the draft (textarea default)

    // Ctrl+<key> → control byte, always to the PTY (signals / readline shortcuts).
    if (e.ctrlKey && !e.altKey && !e.metaKey) {
      if (k.length === 1) {
        const code = k.toLowerCase().charCodeAt(0);
        if (code >= 97 && code <= 122) { e.preventDefault(); sendInput(String.fromCharCode(code - 96)); return; }
        if (CTRL_SYM[k]) { e.preventDefault(); sendInput(CTRL_SYM[k]); return; }
      }
    }

    if (k === 'Escape') { e.preventDefault(); sendInput('\x1b'); return; }

    // Cursor / Backspace: drive the app only when there's no draft; with a draft
    // let the textarea handle them (caret move / delete) so editing feels normal.
    if (!hasDraft) {
      const nav = (term.applicationCursorKeys ? APP_CURSOR : NORMAL_CURSOR)[k];
      if (nav) { e.preventDefault(); sendInput(nav); return; }
      if (k === 'Backspace') { e.preventDefault(); sendInput('\x7f'); return; }
    }

    if (k === 'Tab') { e.preventDefault(); sendInput(e.shiftKey ? '\x1b[Z' : '\t'); return; }
    const fixed = FIXED[k];
    if (fixed) { e.preventDefault(); sendInput(fixed); return; }
    // Printable key → fall through; the character lands in the textarea (drafted).
  }, [term, submit, sendInput]);

  if (!rect) return null;

  const lineCount = Math.max(1, value.split('\n').length);
  const boxStyle: CSSProperties = {
    position: 'absolute',
    left: rect.left,
    top: rect.top,
    right: 8,
    height: rect.height * lineCount,
    margin: 0,
    padding: 0,
    border: 'none',
    outline: 'none',
    resize: 'none',
    background: 'transparent',
    color: style.color,
    caretColor: value ? 'var(--deck-accent, #6366f1)' : 'transparent',
    fontFamily: style.family,
    fontSize: style.size,
    lineHeight: `${rect.height}px`,
    whiteSpace: 'pre-wrap',
    overflow: 'hidden',
    zIndex: 15,
  };

  return (
    <textarea
      ref={taRef}
      className="unified-input"
      value={value}
      onChange={(e) => setValue(e.target.value)}
      onKeyDown={onKeyDown}
      onCompositionStart={() => { composingRef.current = true; }}
      onCompositionEnd={() => { composingRef.current = false; }}
      autoComplete="off"
      autoCorrect="off"
      autoCapitalize="off"
      spellCheck={false}
      inputMode="text"
      rows={lineCount}
      style={boxStyle}
    />
  );
});
