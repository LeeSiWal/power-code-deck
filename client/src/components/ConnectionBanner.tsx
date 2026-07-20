import { useEffect, useRef, useState } from 'react';
import { agentDeckWS } from '../lib/ws';
import { IconRefresh, IconWarning } from './icons';

/**
 * Global "connection lost" banner + reload button.
 *
 * The installed PWA — iOS especially — has no browser chrome, so when a session
 * wedges (the classic fix being a page refresh) there's no refresh button to reach
 * for. This surfaces one. It appears only when the WebSocket has been down for a
 * moment (so a blink during a normal reconnect doesn't flash it), and offers the
 * full reload the user would otherwise be stuck without.
 *
 * Mounted once at the app shell so it covers every screen — dashboard, terminal,
 * native chat — not just one page.
 */
export function ConnectionBanner() {
  const [down, setDown] = useState(false);
  const timer = useRef<number | null>(null);

  useEffect(() => {
    const show = () => {
      if (timer.current || down) return;
      // Hold off ~1.5s: the socket auto-reconnects every 3s, and a brief drop
      // shouldn't flash a scary banner.
      timer.current = window.setTimeout(() => {
        timer.current = null;
        setDown(true);
      }, 1500);
    };
    const hide = () => {
      if (timer.current) {
        clearTimeout(timer.current);
        timer.current = null;
      }
      setDown(false);
    };

    if (!agentDeckWS.connected) show();
    const offOpen = agentDeckWS.on('open', hide);
    const offClose = agentDeckWS.on('close', show);
    return () => {
      offOpen();
      offClose();
      if (timer.current) clearTimeout(timer.current);
    };
  }, [down]);

  if (!down) return null;

  return (
    <div className="fixed top-0 left-0 right-0 z-[60] safe-top pointer-events-none">
      <div className="mx-auto max-w-lg m-2 flex items-center gap-2 px-3 py-2 rounded-lg bg-amber-500/15 border border-amber-500/40 text-amber-300 text-xs shadow-lg pointer-events-auto">
        <IconWarning size={14} className="shrink-0" />
        <span className="flex-1 leading-snug">
          연결이 끊겼습니다 · 재연결 중… 계속 멈춰 있으면 새로고침하세요.
        </span>
        <button
          onClick={() => window.location.reload()}
          className="shrink-0 inline-flex items-center gap-1 px-2.5 py-1 rounded-md bg-amber-400/20 text-amber-200 font-medium active:scale-95"
        >
          <IconRefresh size={13} /> 새로고침
        </button>
      </div>
    </div>
  );
}
