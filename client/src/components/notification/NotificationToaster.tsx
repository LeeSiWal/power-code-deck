import { useEffect, useRef, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { agentDeckWS } from '../../lib/ws';
import { useAppStore } from '../../stores/appStore';

// Foreground in-app notification banner.
//
// iOS delivers Web Push SILENTLY while the installed PWA is focused — the push
// reaches the service worker and shows in Notification Center, but no banner/sound
// interrupts you. That's the "test works but real ones don't show" gap. This fills
// it: when the app is visible we render our own banner (and optional sound) from the
// same agent:notification event; when hidden we stay quiet and let the OS push do it,
// so the two never double up.

type Toast = { id: number; agentId: string; reason: string; message: string; name: string };

const LABEL: Record<string, string> = {
  permission_request: '승인 필요',
  task_complete: '작업 완료',
  error: '오류',
  stalled: '무응답',
  waiting_input: '입력 대기',
};

// Left accent colour per reason (thin full border + a thick coloured left edge).
const ACCENT: Record<string, string> = {
  permission_request: 'border-l-deck-accent',
  task_complete: 'border-l-deck-success',
  error: 'border-l-deck-danger',
  stalled: 'border-l-deck-warning',
  waiting_input: 'border-l-deck-accent',
};

let seq = 0;
let audioCtx: AudioContext | null = null;

// A short WebAudio chime — two tones for the urgent kinds, one soft tone otherwise.
// No audio asset needed; gated by the user's sound setting and wrapped so a blocked
// AudioContext (iOS without a prior gesture) never breaks the visual banner.
function beep(reason: string) {
  try {
    const Ctx = window.AudioContext || (window as any).webkitAudioContext;
    if (!Ctx) return;
    audioCtx = audioCtx || new Ctx();
    const ctx = audioCtx;
    if (ctx.state === 'suspended') ctx.resume().catch(() => {});
    const urgent = reason === 'permission_request' || reason === 'stalled' || reason === 'error';
    const tones = urgent ? [880, 1175] : [660];
    tones.forEach((f, i) => {
      const o = ctx.createOscillator();
      const g = ctx.createGain();
      o.type = 'sine';
      o.frequency.value = f;
      o.connect(g);
      g.connect(ctx.destination);
      const t = ctx.currentTime + i * 0.14;
      g.gain.setValueAtTime(0.0001, t);
      g.gain.exponentialRampToValueAtTime(0.15, t + 0.02);
      g.gain.exponentialRampToValueAtTime(0.0001, t + 0.13);
      o.start(t);
      o.stop(t + 0.15);
    });
  } catch {
    /* audio unavailable — the visual banner still shows */
  }
}

export function NotificationToaster() {
  const navigate = useNavigate();
  const [toasts, setToasts] = useState<Toast[]>([]);
  const soundRef = useRef(useAppStore.getState().soundEnabled);

  // Track the live sound setting without re-subscribing the WS handler.
  useEffect(() => useAppStore.subscribe((s) => { soundRef.current = s.soundEnabled; }), []);

  useEffect(() => {
    const off = agentDeckWS.on('agent:notification', (p: any) => {
      if (!p?.agentId) return;
      // Foreground only — see the component note above.
      if (document.visibilityState !== 'visible') return;
      const st = useAppStore.getState();
      const name =
        st.summaries[p.agentId]?.name ||
        st.agents.find((a) => a.id === p.agentId)?.name ||
        '에이전트';
      const id = ++seq;
      // Keep at most 4 on screen; newest at the bottom of the stack.
      setToasts((t) => [...t.slice(-3), { id, agentId: p.agentId, reason: p.reason, message: p.message, name }]);
      if (soundRef.current) beep(p.reason);
      window.setTimeout(() => setToasts((t) => t.filter((x) => x.id !== id)), 6000);
    });
    return off;
  }, []);

  if (toasts.length === 0) return null;

  return (
    <div
      className="fixed top-0 left-0 right-0 z-[55] px-2 flex flex-col items-center gap-2 pointer-events-none"
      style={{ paddingTop: 'calc(env(safe-area-inset-top) + 0.5rem)' }}
    >
      {toasts.map((t) => (
        <button
          key={t.id}
          onClick={() => {
            navigate(`/agents/${t.agentId}`);
            setToasts((x) => x.filter((y) => y.id !== t.id));
          }}
          className={`pointer-events-auto w-full max-w-md flex items-center gap-3 px-3 py-2.5 rounded-lg bg-deck-raised border border-deck-border border-l-4 ${ACCENT[t.reason] || 'border-l-deck-accent'} shadow-lg text-left animate-slide-down`}
        >
          <div className="min-w-0 flex-1">
            <div className="flex items-center gap-2">
              <span className="text-xs font-semibold">{LABEL[t.reason] || '알림'}</span>
              <span className="text-[11px] text-deck-text-dim font-mono truncate">{t.name}</span>
            </div>
            <div className="text-xs text-deck-text-dim truncate mt-0.5">{t.message}</div>
          </div>
        </button>
      ))}
    </div>
  );
}
