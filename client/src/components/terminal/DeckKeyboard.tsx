import { useCallback, useEffect, useRef, useState } from 'react';
import { DECK_ROWS, deckMetrics, type DeckKey, type Modifier } from '../../lib/deckKeyboard';
import { HangulComposer, jamoFor } from '../../lib/hangul';
import { IconCopy, IconPaste } from '../icons';
import { useUiZoom } from '../../lib/uiScale';

export interface DeckKeyMods { ctrl?: boolean; alt?: boolean; shift?: boolean }

/** What the deck keyboard types into: the terminal's UnifiedInput, or a chat
 *  composer through lib/textareaDeckTarget. */
export interface DeckTarget {
  /** Replace the syllable being composed (if any) with `commit` followed by the
   *  new in-progress `preedit`, at the caret. */
  compose: (commit: string, preedit: string) => void;
  /** Whether the last `preedit` is still in place (the caret has not moved). */
  composing: () => boolean;
  /** A non-text key, as if pressed on a physical keyboard. */
  pressKey: (key: string, mods: DeckKeyMods) => void;
  /** Focus the input so the system soft keyboard comes up (call from a tap). */
  focusSystemKeyboard: () => void;
}

/** 0 off, 1 the next key only, 2 locked. */
type ModState = 0 | 1 | 2;
type Lang = 'en' | 'ko';

const LANG_KEY = 'pcd:deckLang';
const REPEAT_DELAY_MS = 450;
const REPEAT_EVERY_MS = 60;

interface DeckKeyboardProps {
  /** The input the keys type into (TerminalHandle.input, or a chat composer). */
  target: () => DeckTarget | null;
  width: number;
  height: number;
  /** Switch to the system soft keyboard (the input is focused in the same tap). */
  onSystemKeyboard: () => void;
  /** Hide the deck keyboard; the system keyboard stays down. */
  onHide: () => void;
  onPaste: () => Promise<boolean>;
  onCopy: () => Promise<boolean>;
  /** Pad for the home indicator (false when the container already does). */
  insetBottom?: boolean;
}

function readLang(): Lang {
  try { return localStorage.getItem(LANG_KEY) === 'ko' ? 'ko' : 'en'; } catch { return 'en'; }
}

/**
 * The deck keyboard: a full on-screen keyboard (Esc, Tab, Ctrl, Alt, Fn,
 * arrows, every symbol on one page, 한/영) that replaces the iOS soft keyboard
 * on an iPad without a physical keyboard. Text goes into the terminal's
 * cursor-anchored draft (Hangul composed here, see lib/hangul.ts); control and
 * navigation keys go through UnifiedInput.pressKey exactly as a physical key.
 */
export function DeckKeyboard({ target, width, height, onSystemKeyboard, onHide, onPaste, onCopy, insetBottom = true }: DeckKeyboardProps) {
  const [mods, setMods] = useState<Record<Modifier, ModState>>({ shift: 0, ctrl: 0, alt: 0, fn: 0 });
  const modsRef = useRef(mods);
  modsRef.current = mods;
  const [lang, setLang] = useState<Lang>(readLang);
  const langRef = useRef(lang);
  langRef.current = lang;
  const composer = useRef(new HangulComposer()).current;
  const [pressed, setPressed] = useState<string | null>(null);
  const repeatTimer = useRef<number | null>(null);
  const [flash, setFlash] = useState<{ id: 'paste' | 'copy'; ok: boolean } | null>(null);
  // Sizes are for the real screen; the app's UI zoom (Settings, large displays)
  // would scale them again, so take it back out.
  const zoom = useUiZoom();
  const raw = deckMetrics(width, height);
  const m = {
    keyHeight: raw.keyHeight / zoom, gap: raw.gap / zoom, fontSize: raw.fontSize / zoom,
    padX: raw.padX / zoom, stripHeight: raw.stripHeight / zoom,
  };

  // Commit the syllable in progress (it is already in the draft; this only ends it).
  const flush = useCallback((t: DeckTarget) => {
    if (composer.active && t.composing()) t.compose(composer.flush(), '');
    composer.reset();
  }, [composer]);

  // A modifier tapped once applies to one key, then lets go.
  const releaseOnce = useCallback(() => {
    const cur = modsRef.current;
    if (Object.values(cur).some((v) => v === 1)) {
      const next = { ...cur };
      (Object.keys(next) as Modifier[]).forEach((k) => { if (next[k] === 1) next[k] = 0; });
      setMods(next);
      modsRef.current = next;
    }
  }, []);

  const activate = useCallback((k: DeckKey) => {
    const t = target();
    if (!t) return;
    const cur = modsRef.current;
    const shift = cur.shift > 0, ctrl = cur.ctrl > 0, alt = cur.alt > 0, fn = cur.fn > 0;
    // The user moved the caret or typed elsewhere: the syllable has ended.
    if (composer.active && !t.composing()) composer.reset();

    if (k.ch !== undefined && !(fn && k.fnKey)) {
      const ch = shift ? (k.shiftCh ?? k.ch) : k.ch;
      if (ctrl || alt) {
        flush(t);
        t.pressKey(ctrl ? k.ch : ch, { ctrl, alt, shift });
      } else if (langRef.current === 'ko' && k.ch !== ' ' && jamoFor(k.ch, shift)) {
        const r = composer.type(jamoFor(k.ch, shift)!);
        t.compose(r.commit, r.preedit);
      } else {
        flush(t);
        t.compose(ch, '');
      }
      releaseOnce();
      return;
    }
    const name = (fn && k.fnKey) || k.key;
    if (!name) return;
    if (name === 'Backspace' && composer.active) {
      t.compose('', composer.backspace().preedit);
      return;
    }
    flush(t);
    if (name === 'Enter' && shift) t.compose('\n', ''); // a newline inside the draft
    else t.pressKey(name, { ctrl, alt, shift });
    releaseOnce();
  }, [target, composer, flush, releaseOnce]);

  const stopRepeat = useCallback(() => {
    if (repeatTimer.current !== null) {
      window.clearTimeout(repeatTimer.current);
      window.clearInterval(repeatTimer.current);
      repeatTimer.current = null;
    }
    setPressed(null);
  }, []);
  useEffect(() => stopRepeat, [stopRepeat]);

  const onKeyDown = (k: DeckKey) => (e: React.PointerEvent) => {
    // Keep focus where it is: a focused button would blur the input, and a
    // focused input with inputmode="text" would raise the system keyboard.
    e.preventDefault();
    stopRepeat();
    setPressed(k.id);
    if (k.mod) {
      const cur = modsRef.current;
      const next = { ...cur, [k.mod]: ((cur[k.mod] + 1) % 3) as ModState };
      setMods(next);
      modsRef.current = next;
      return;
    }
    if (k.lang) {
      const t = target();
      if (t) flush(t);
      const next: Lang = langRef.current === 'ko' ? 'en' : 'ko';
      setLang(next);
      langRef.current = next;
      try { localStorage.setItem(LANG_KEY, next); } catch { /* private mode */ }
      return;
    }
    activate(k);
    if (k.repeat) {
      repeatTimer.current = window.setTimeout(() => {
        repeatTimer.current = window.setInterval(() => activate(k), REPEAT_EVERY_MS);
      }, REPEAT_DELAY_MS);
    }
  };

  const showFlash = (id: 'paste' | 'copy', ok: boolean) => {
    setFlash({ id, ok });
    window.setTimeout(() => setFlash(null), 1300);
  };

  const fnOn = mods.fn > 0;
  const shiftOn = mods.shift > 0;
  const label = (k: DeckKey): string => {
    if (fnOn && k.fnLabel) return k.fnLabel;
    if (k.lang) return lang === 'ko' ? '한' : 'EN';
    if (k.label !== undefined) return k.label;
    if (k.ch && lang === 'ko' && /[a-z]/.test(k.ch)) return jamoFor(k.ch, shiftOn) ?? k.ch;
    return shiftOn ? (k.shiftCh ?? k.ch ?? '') : (k.ch ?? '');
  };
  // Symbol keys show their shifted character small above, like a printed keycap.
  const hint = (k: DeckKey): string | null => {
    if (shiftOn || fnOn || !k.ch || !k.shiftCh || /[a-z ]/.test(k.ch)) return null;
    return k.shiftCh;
  };

  const stripBtn = 'h-full px-3 rounded-md text-xs flex items-center gap-1.5 select-none touch-manipulation active:opacity-70';

  return (
    <div
      className="shrink-0 bg-deck-surface border-t border-deck-border select-none"
      style={{ paddingLeft: m.padX, paddingRight: m.padX, paddingBottom: insetBottom ? `calc(${m.gap}px + env(safe-area-inset-bottom))` : m.gap, touchAction: 'manipulation' }}
      onContextMenu={(e) => e.preventDefault()}
    >
      <div className="flex items-center gap-2 py-1" style={{ height: m.stripHeight + m.gap }}>
        {/* Clipboard reads must come from a click on iOS (not pointerdown). */}
        <button
          onPointerDown={(e) => e.preventDefault()}
          onClick={() => { const t = target(); if (t) flush(t); onPaste().then((ok) => showFlash('paste', ok)); }}
          className={`${stripBtn} bg-deck-bg ${flash?.id === 'paste' ? (flash.ok ? 'text-green-400' : 'text-red-400') : 'text-deck-text'}`}
        >
          <IconPaste size={14} /> 붙여넣기
        </button>
        <button
          onPointerDown={(e) => { e.preventDefault(); onCopy().then((ok) => showFlash('copy', ok)); }}
          className={`${stripBtn} bg-deck-bg ${flash?.id === 'copy' ? (flash.ok ? 'text-green-400' : 'text-red-400') : 'text-deck-text'}`}
        >
          <IconCopy size={14} /> 복사
        </button>
        <span className="flex-1" />
        <button
          onPointerDown={(e) => e.preventDefault()}
          // Focus inside the click so iOS raises its keyboard in this tap.
          onClick={() => { const t = target(); if (t) { flush(t); t.focusSystemKeyboard(); } onSystemKeyboard(); }}
          className={`${stripBtn} bg-deck-bg text-deck-text-dim`}
          title="iOS 기본 키보드로 전환"
        >
          ⌨︎ 기본 키보드
        </button>
        <button
          onPointerDown={(e) => { e.preventDefault(); const t = target(); if (t) flush(t); onHide(); }}
          className={`${stripBtn} bg-deck-bg text-deck-text-dim`}
          title="키보드 숨기기"
        >
          ▾ 숨기기
        </button>
      </div>
      <div className="flex flex-col" style={{ gap: m.gap }}>
        {DECK_ROWS.map((row, i) => (
          <div key={i} className="flex" style={{ gap: m.gap, height: m.keyHeight }}>
            {row.map((k) => {
              const modState = k.mod ? mods[k.mod] : 0;
              // Modifiers and word keys are dimmed; arrows, ⌫ and characters are not.
              const special = !!(k.mod || k.lang || (k.key && /^[a-z]{2,}$/.test(k.label ?? '')));
              const accent = k.key === 'Enter' || modState > 0 || (k.lang && lang === 'ko');
              const h = hint(k);
              return (
                <button
                  key={k.id}
                  onPointerDown={onKeyDown(k)}
                  onPointerUp={stopRepeat}
                  onPointerCancel={stopRepeat}
                  onPointerLeave={stopRepeat}
                  className={`relative min-w-0 rounded-lg font-mono flex flex-col items-center justify-center leading-none transition-colors ${
                    accent ? 'bg-deck-accent/25 text-deck-accent' : special ? 'bg-deck-bg/70 text-deck-text-dim' : 'bg-deck-bg text-deck-text'
                  } ${pressed === k.id ? 'brightness-150 bg-deck-border' : ''} ${modState === 2 ? 'ring-2 ring-deck-accent' : ''}`}
                  // Word labels (esc, ctrl, return, F5) are set smaller; symbols (⌫ ↑ ⇧) and characters full size.
                  style={{ flex: `${k.w} 1 0`, fontSize: /^[a-zA-Z/0-9]{2,}$/.test(label(k)) ? m.fontSize * 0.75 : m.fontSize }}
                  aria-label={k.id}
                >
                  {h && <span className="opacity-50" style={{ fontSize: Math.round(m.fontSize * 0.6), marginBottom: 2 }}>{h}</span>}
                  {label(k)}
                </button>
              );
            })}
          </div>
        ))}
      </div>
    </div>
  );
}
