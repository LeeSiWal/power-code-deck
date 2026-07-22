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
    // Inline padding-top (not the .safe-top utility): env(safe-area-inset-top) alone
    // sits the banner right at the notch/Dynamic-Island edge and clips it, so add a
    // fixed gap on top of the inset to clear it on every device. Side gutters via px-2
    // instead of an inner margin so the box can use its full flex width.
    <div
      className="fixed top-0 left-0 right-0 z-[60] px-2 pointer-events-none"
      style={{ paddingTop: 'calc(env(safe-area-inset-top) + 0.5rem)' }}
    >
      {/* OPAQUE background (bg-amber-950, not amber-500/15): the banner is a fixed
          overlay that lands on top of each page's own header, and a translucent fill
          let that header's title + icons bleed through — the two layers mixed into an
          unreadable garble. An opaque fill covers whatever is behind it cleanly.
          w-fit keeps it a centered toast rather than a full-width bar over the whole
          header row. */}
      <div className="mx-auto w-fit max-w-[92%] flex items-center gap-2 px-3 py-2 rounded-lg bg-amber-950 border border-amber-500/50 text-amber-200 text-xs shadow-lg pointer-events-auto">
        <IconWarning size={14} className="shrink-0" />
        {/* min-w-0 lets the text shrink/wrap inside flex instead of shoving the button
            off-screen; the button says 새로고침 already, so the message stays short. */}
        <span className="flex-1 min-w-0 leading-snug">연결이 끊겼습니다 · 재연결 중…</span>
        <button
          onClick={() => window.location.reload()}
          className="shrink-0 whitespace-nowrap inline-flex items-center gap-1 px-2.5 py-1 rounded-md bg-amber-400/20 text-amber-200 font-medium active:scale-95"
        >
          <IconRefresh size={13} /> 새로고침
        </button>
      </div>
    </div>
  );
}
