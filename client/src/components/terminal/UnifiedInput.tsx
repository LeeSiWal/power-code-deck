import { useEffect, useRef, useState, useCallback, useImperativeHandle, forwardRef, type CSSProperties } from 'react';
import type { CustomTerm, CursorRect } from '../../lib/customTerm/CustomTerm';
import { agentDeckWS } from '../../lib/ws';

/** Imperative handle so the terminal key bar's Enter can submit this draft
 *  instead of sending a raw CR that ignores the buffered text, and so the mobile
 *  toolbar's 붙여넣기 can drop clipboard text into the draft at the caret. */
export interface UnifiedInputHandle {
  submit: () => void;
  insert: (text: string) => void;
  /** Deck keyboard: replace the syllable being composed (if any) with `commit`
   *  followed by the new in-progress `preedit`, at the caret. */
  compose: (commit: string, preedit: string) => void;
  /** Deck keyboard: whether the last `preedit` is still in place (the user has
   *  not moved the caret or typed elsewhere since). */
  composing: () => boolean;
  /** Deck keyboard: a non-text key, as if pressed on a physical keyboard. */
  pressKey: (key: string, mods: KeyMods) => void;
  /** Focus the input so the system soft keyboard comes up (call from a tap). */
  focusSystemKeyboard: () => void;
}

export interface KeyMods { ctrl?: boolean; alt?: boolean; shift?: boolean }

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
const CTRL_SYM: Record<string, string> = { '[': '\x1b', '\\': '\x1c', ']': '\x1d', '^': '\x1e', '_': '\x1f', ' ': '\x00' };
// Ctrl+arrow (word moves in readline and most editors): CSI 1;5 X.
const CTRL_ARROW: Record<string, string> = { ArrowUp: '\x1b[1;5A', ArrowDown: '\x1b[1;5B', ArrowRight: '\x1b[1;5C', ArrowLeft: '\x1b[1;5D' };

interface UnifiedInputProps {
  term: CustomTerm;
  agentId: string;
  channel?: 'terminal' | 'shell';
  /** The remote is asking for a secret. The PTY turns echo OFF for these prompts,
   *  but this input echoes into a REAL textarea, so the password would otherwise be
   *  typed in the clear. Hides the draft locally and sends it raw (see submit). */
  masked?: boolean;
  /** Focus the textarea on mount (desktop; skipped on touch to avoid popping the keyboard). */
  autoFocus?: boolean;
  /** Touch device: soft-keyboard Enter arrives as a beforeinput 'insertLineBreak'
   *  (not a keydown), and there's no Shift, so every Enter submits. */
  touch?: boolean;
  /** The deck keyboard is in use: keep the system soft keyboard down
   *  (inputmode="none") while the input still takes focus, a caret and
   *  physical-keyboard keys. */
  deckKeyboard?: boolean;
}

export const UnifiedInput = forwardRef<UnifiedInputHandle, UnifiedInputProps>(function UnifiedInput(
  { term, agentId, channel = 'terminal', masked = false, autoFocus, touch, deckKeyboard },
  ref,
) {
  const taRef = useRef<HTMLTextAreaElement>(null);
  const [rect, setRect] = useState<CursorRect | null>(() => term.cursorRect());
  const [value, setValue] = useState('');
  const valueRef = useRef('');
  valueRef.current = value;
  const composingRef = useRef(false);
  // Deck keyboard state: where the caret is and how long the in-progress
  // syllable before it is. Tracked here rather than read from the DOM because
  // several keys can land before React re-renders the textarea.
  const caretRef = useRef(0);
  const preeditLenRef = useRef(0);
  const [style, setStyle] = useState<{ family: string; size: string; color: string; bg: string }>({
    family: 'monospace', size: '14px', color: 'var(--term-fg, #e7e9f0)', bg: 'var(--term-bg, #0a0a0f)',
  });

  // Match the terminal's rendered font/color/background so the draft aligns with the
  // grid AND (when it has text) can paint an opaque box over the terminal cells behind
  // it — otherwise the app's own placeholder / prompt text shows THROUGH the draft.
  useEffect(() => {
    const cs = getComputedStyle(term.element);
    setStyle({ family: cs.fontFamily, size: cs.fontSize, color: cs.color, bg: cs.backgroundColor });
  }, [term]);

  // Follow the cursor: CustomTerm calls this on every render (output redraw). But
  // ONLY while the draft is empty — once the user starts typing, freeze the box where
  // they began. Otherwise an app redraw (e.g. Claude Code printing a question) drags
  // the floating input across the screen, mixing it into the output and appearing to
  // swallow the half-typed text; freezing also keeps it mounted/focused if the cursor
  // scrolls out of view (cursorRect → null would otherwise blank the textarea).
  useEffect(() => {
    setRect(term.cursorRect());
    term.setCursorListener((r) => { if (!valueRef.current) setRect(r); });
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
    agentDeckWS.send(`${channel}:input`, { agentId, data });
  }, [agentId, channel]);

  // Enter: commit the draft as a bracketed paste (the Prompt Bar's exact path), or,
  // when there's no draft, send a raw CR so it confirms the app's current line/menu.
  //
  // EXCEPT at a password prompt. Bracketed paste is a mode the *application* has to
  // turn on (ESC[?2004h); sudo/ssh/git read the password with a plain tty read and
  // never enable it, so the wrapper bytes ESC[200~ / ESC[201~ arrive as part of the
  // secret and the password is silently wrong. Send it raw instead — which is also
  // exactly what typing it character by character would have produced.
  const submit = useCallback(() => {
    const text = valueRef.current;
    if (text) {
      if (masked) {
        sendInput(text + '\r');
      } else if (channel === 'terminal') {
        agentDeckWS.send('terminal:pasteSubmit', { agentId, text, mode: 'bracketed-paste' });
      } else {
        agentDeckWS.send('shell:input', { agentId, data: `\x1b[200~${text}\x1b[201~\r` });
      }
      setValue('');
      setRect(term.cursorRect()); // unfreeze: resume tracking from the live cursor
    } else {
      sendInput('\r');
    }
  }, [agentId, channel, masked, sendInput, term]);

  // Insert text (a clipboard paste from the mobile toolbar) into the draft at the
  // caret, then restore focus + caret. Goes through the same draft the user submits.
  const insert = useCallback((text: string) => {
    if (!text) return;
    const ta = taRef.current;
    const base = valueRef.current;
    const start = ta ? ta.selectionStart : base.length;
    const end = ta ? ta.selectionEnd : base.length;
    const next = base.slice(0, start) + text + base.slice(end);
    setValue(next);
    const caret = start + text.length;
    caretRef.current = caret;
    preeditLenRef.current = 0;
    requestAnimationFrame(() => {
      const t = taRef.current;
      if (t) { t.focus({ preventScroll: true }); t.setSelectionRange(caret, caret); }
    });
  }, []);

  // Replace the draft and place the caret, synchronously for the refs (the next
  // deck key may arrive before the re-render) and on the textarea after it.
  const setDraft = useCallback((next: string, caret: number) => {
    valueRef.current = next;
    caretRef.current = caret;
    setValue(next);
    if (!next) setRect(term.cursorRect());
    requestAnimationFrame(() => taRef.current?.setSelectionRange(caret, caret));
  }, [term]);

  const compose = useCallback((commit: string, preedit: string) => {
    const base = valueRef.current;
    const end = Math.min(caretRef.current, base.length);
    const start = Math.max(0, end - preeditLenRef.current);
    preeditLenRef.current = preedit.length;
    setDraft(base.slice(0, start) + commit + preedit + base.slice(end), start + commit.length + preedit.length);
  }, [setDraft]);

  // Key handling shared by physical keys (keydown) and deck keys (pressKey).
  // Returns whether the key was consumed; an unconsumed physical key falls
  // through to the textarea (typing, caret moves while drafting).
  const handleKey = useCallback((k: string, mods: KeyMods, deck: boolean): boolean => {
    const hasDraft = valueRef.current.length > 0;
    if (k === 'Enter' && !mods.shift) { submit(); return true; }
    if (k === 'Enter') return false; // newline into the draft (textarea default)

    // Ctrl+<key> → control byte, always to the PTY (signals / readline shortcuts).
    if (mods.ctrl && !mods.alt) {
      if (k.length === 1) {
        const code = k.toLowerCase().charCodeAt(0);
        if (code >= 97 && code <= 122) { sendInput(String.fromCharCode(code - 96)); return true; }
        if (CTRL_SYM[k]) { sendInput(CTRL_SYM[k]); return true; }
      }
      if (deck && !hasDraft && CTRL_ARROW[k]) { sendInput(CTRL_ARROW[k]); return true; }
    }
    // Alt (Meta) prefixes ESC, as terminals do for the Meta key.
    if (deck && mods.alt && !mods.ctrl) {
      if (k.length === 1) { sendInput('\x1b' + k); return true; }
      const nav = (term.applicationCursorKeys ? APP_CURSOR : NORMAL_CURSOR)[k];
      if (nav && !hasDraft) { sendInput('\x1b' + nav); return true; }
    }

    if (k === 'Escape') { sendInput('\x1b'); return true; }

    // Cursor / Backspace: drive the app only when there's no draft; with a draft
    // they edit the draft (the textarea does it for physical keys).
    if (!hasDraft) {
      const nav = (term.applicationCursorKeys ? APP_CURSOR : NORMAL_CURSOR)[k];
      if (nav) { sendInput(nav); return true; }
      if (k === 'Backspace') { sendInput('\x7f'); return true; }
    } else if (deck) {
      const v = valueRef.current;
      const at = Math.min(caretRef.current, v.length);
      preeditLenRef.current = 0;
      switch (k) {
        case 'Backspace': if (at > 0) setDraft(v.slice(0, at - 1) + v.slice(at), at - 1); return true;
        case 'ArrowLeft': setDraft(v, Math.max(0, at - 1)); return true;
        case 'ArrowRight': setDraft(v, Math.min(v.length, at + 1)); return true;
        case 'Home': setDraft(v, 0); return true;
        case 'End': setDraft(v, v.length); return true;
        case 'ArrowUp': case 'ArrowDown': return true;
      }
    }

    if (k === 'Tab') { sendInput(mods.shift ? '\x1b[Z' : '\t'); return true; }
    const fixed = FIXED[k];
    if (fixed) { sendInput(fixed); return true; }
    return false;
  }, [term, submit, sendInput, setDraft]);

  const pressKey = useCallback((key: string, mods: KeyMods) => {
    preeditLenRef.current = 0;
    handleKey(key, mods, true);
  }, [handleKey]);

  const focusSystemKeyboard = useCallback(() => {
    const ta = taRef.current;
    if (!ta) return;
    // The attribute must change before focus() for iOS to raise its keyboard in
    // this same tap; React re-renders it to the same value right after.
    ta.inputMode = 'text';
    ta.blur();
    ta.focus({ preventScroll: true });
  }, []);

  useImperativeHandle(ref, () => ({
    submit, insert, compose, pressKey, focusSystemKeyboard,
    composing: () => preeditLenRef.current > 0,
  }), [submit, insert, compose, pressKey, focusSystemKeyboard]);

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
    preeditLenRef.current = 0;
    if (handleKey(e.key, { ctrl: e.ctrlKey && !e.metaKey, alt: e.altKey, shift: e.shiftKey }, false)) e.preventDefault();
    // Printable key → fall through; the character lands in the textarea (drafted).
  }, [handleKey]);

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
    // Opaque box only while drafting, so the terminal's underlying text (the app's
    // placeholder / prompt) is hidden behind the draft instead of bleeding through;
    // transparent when empty so the live cursor and prompt stay visible.
    background: value ? style.bg : 'transparent',
    color: style.color,
    caretColor: value ? 'var(--deck-accent, #6366f1)' : 'transparent',
    fontFamily: style.family,
    fontSize: style.size,
    lineHeight: `${rect.height}px`,
    whiteSpace: 'pre-wrap',
    overflow: 'hidden',
    // Hide the secret while keeping a real textarea (so the IME and the caret still
    // work). -webkit-text-security is non-standard but is what Safari/Chrome — the
    // browsers this deck actually runs in — implement; `textSecurity` is the
    // standards-track name, set alongside it so a future engine picks it up.
    ...(masked ? ({ WebkitTextSecurity: 'disc', textSecurity: 'disc' } as CSSProperties) : null),
    zIndex: 15,
  };

  return (
    <textarea
      ref={taRef}
      className="unified-input"
      value={value}
      onChange={(e) => {
        const v = e.target.value;
        setValue(v);
        // Draft just went empty → re-anchor to the live cursor so tracking resumes
        // from the app's real input position (it was frozen while drafting).
        if (!v) setRect(term.cursorRect());
        caretRef.current = e.target.selectionEnd;
        preeditLenRef.current = 0;
      }}
      // A tap that moves the caret ends any deck-keyboard syllable in progress.
      onSelect={(e) => {
        const at = e.currentTarget.selectionEnd;
        if (at !== caretRef.current) { caretRef.current = at; preeditLenRef.current = 0; }
      }}
      onKeyDown={onKeyDown}
      onCompositionStart={() => { composingRef.current = true; }}
      onCompositionEnd={() => { composingRef.current = false; }}
      autoComplete="off"
      autoCorrect="off"
      autoCapitalize="off"
      spellCheck={false}
      inputMode={deckKeyboard ? 'none' : 'text'}
      rows={lineCount}
      style={boxStyle}
    />
  );
});
