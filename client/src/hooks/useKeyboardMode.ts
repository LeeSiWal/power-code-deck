import { useCallback, useEffect, useState } from 'react';

/** deck = the on-screen deck keyboard; system = the iOS keyboard (or a physical one). */
export type KeyboardMode = 'deck' | 'system';

const MODE_KEY = 'pcd:keyboardMode';
const NOTICE_MS = 4000;

function readMode(): KeyboardMode {
  try { return localStorage.getItem(MODE_KEY) === 'system' ? 'system' : 'deck'; } catch { return 'deck'; }
}

/**
 * Which keyboard a terminal session types with on an iPad. The user's choice is
 * remembered; a physical keyboard switches to system mode for this visit only
 * (no event comes from the deck keyboard or a hidden iOS keyboard, so a real
 * keydown while the deck keyboard is in use means a physical keyboard).
 */
export function useKeyboardMode(available: boolean) {
  const [mode, setMode] = useState<KeyboardMode>(readMode);
  const [hidden, setHidden] = useState(false);
  const [notice, setNotice] = useState('');
  const deck = available && mode === 'deck';

  const choose = useCallback((m: KeyboardMode) => {
    setMode(m);
    setHidden(false);
    try { localStorage.setItem(MODE_KEY, m); } catch { /* private mode */ }
  }, []);

  const switchToDeck = useCallback(() => {
    // Put the iOS keyboard away: the input stays, with inputmode="none".
    (document.activeElement as HTMLElement | null)?.blur?.();
    choose('deck');
  }, [choose]);
  const switchToSystem = useCallback(() => choose('system'), [choose]);

  useEffect(() => {
    if (!deck) return;
    const onKey = (e: KeyboardEvent) => {
      if (!e.isTrusted || e.isComposing || !e.key || e.key === 'Unidentified') return;
      setMode('system');
      setNotice('물리 키보드가 연결되어 기본 키보드 모드로 바꿨습니다');
      window.setTimeout(() => setNotice(''), NOTICE_MS);
    };
    document.addEventListener('keydown', onKey, true);
    return () => document.removeEventListener('keydown', onKey, true);
  }, [deck]);

  return { deck, hidden, setHidden, switchToDeck, switchToSystem, notice };
}
