import { useEffect, useMemo, useState, useCallback } from 'react';
import { qrToSvgString } from '../../lib/qrcode';
import { api } from '../../lib/api';
import { IconClose } from '../icons';

interface HandoffResponse {
  token: string;
  sessionId: string;
  expiresAt: string;
  ttlSeconds: number;
  publicUrl: string;
  localUrl: string;
  lanEnabled: boolean;
  authEnabled: boolean;
  warning: string;
}

interface HandoffModalProps {
  agentId: string;
  agentName?: string;
  onClose: () => void;
}

/**
 * "Continue on Mobile" (모바일에서 이어하기) — shows a one-time QR that hands
 * this session off to a phone/iPad. The token is single-use and expires; the
 * modal can regenerate a fresh one.
 */
export function HandoffModal({ agentId, agentName, onClose }: HandoffModalProps) {
  const [data, setData] = useState<HandoffResponse | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);
  const [useLocal, setUseLocal] = useState(false);
  const [remaining, setRemaining] = useState<number>(0);
  const [copied, setCopied] = useState<'public' | 'local' | null>(null);

  const load = useCallback(() => {
    setLoading(true);
    setError(null);
    api
      .createHandoff(agentId)
      .then((res) => {
        setData(res);
        // Prefer the public URL when present; otherwise fall back to LAN.
        setUseLocal(!res.publicUrl && !!res.localUrl);
      })
      .catch((e) => setError(e.message || 'Failed to create handoff link'))
      .finally(() => setLoading(false));
  }, [agentId]);

  useEffect(() => {
    load();
  }, [load]);

  // Expiry countdown.
  useEffect(() => {
    if (!data) return;
    const tick = () => {
      const secs = Math.max(0, Math.round((new Date(data.expiresAt).getTime() - Date.now()) / 1000));
      setRemaining(secs);
    };
    tick();
    const id = window.setInterval(tick, 1000);
    return () => window.clearInterval(id);
  }, [data]);

  // Esc to close.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onClose();
    };
    document.addEventListener('keydown', onKey);
    return () => document.removeEventListener('keydown', onKey);
  }, [onClose]);

  const activeUrl = useLocal ? data?.localUrl : data?.publicUrl;
  const expired = data != null && remaining <= 0;

  const qrSvg = useMemo(() => {
    if (!activeUrl || expired) return '';
    try {
      return qrToSvgString(activeUrl, { border: 2, ecl: 'M' });
    } catch {
      return '';
    }
  }, [activeUrl, expired]);

  const copy = (which: 'public' | 'local') => {
    const url = which === 'local' ? data?.localUrl : data?.publicUrl;
    if (!url) return;
    navigator.clipboard?.writeText(url).then(
      () => {
        setCopied(which);
        window.setTimeout(() => setCopied(null), 1500);
      },
      () => {},
    );
  };

  const mmss = `${Math.floor(remaining / 60)}:${String(remaining % 60).padStart(2, '0')}`;

  return (
    <>
      <div className="fixed inset-0 bg-black/60 z-[60]" onClick={onClose} />
      <div
        className="fixed left-1/2 top-1/2 -translate-x-1/2 -translate-y-1/2 z-[61] w-[92%] max-w-[380px]
                   rounded-2xl bg-deck-surface border border-deck-border shadow-2xl overflow-hidden"
        role="dialog"
        aria-modal="true"
      >
        {/* Header */}
        <div className="flex items-center gap-2 px-4 py-3 border-b border-deck-border">
          <span className="text-base">📱</span>
          <div className="min-w-0">
            <div className="text-sm font-semibold text-deck-text">모바일에서 이어하기</div>
            <div className="text-[11px] text-deck-text-dim">Continue on Mobile</div>
          </div>
          <button onClick={onClose} className="ml-auto p-1.5 rounded hover:bg-deck-border/30" title="닫기 (Esc)">
            <IconClose size={14} />
          </button>
        </div>

        {/* Body */}
        <div className="px-4 py-4 flex flex-col items-center gap-3">
          <p className="text-xs text-center text-deck-text-dim leading-relaxed">
            현재 세션{agentName ? ` (${agentName})` : ''}을 모바일/iPad에서 이어서 열 수 있습니다.
            <br />
            QR 코드는 10분 동안 유효하며 한 번 사용하면 만료됩니다.
          </p>

          {/* QR / status area */}
          <div className="w-[220px] h-[220px] rounded-xl bg-white flex items-center justify-center overflow-hidden">
            {loading && <span className="text-xs text-gray-500">생성 중…</span>}
            {!loading && error && (
              <span className="text-xs text-red-500 px-4 text-center">{error}</span>
            )}
            {!loading && !error && expired && (
              <span className="text-xs text-gray-500 px-4 text-center">
                QR이 만료되었습니다.
                <br />
                다시 생성해 주세요.
              </span>
            )}
            {!loading && !error && !expired && qrSvg && (
              <div
                className="w-[204px] h-[204px] [&>svg]:w-full [&>svg]:h-full"
                // qrToSvgString returns a trusted, locally-generated SVG (no user HTML).
                dangerouslySetInnerHTML={{ __html: qrSvg }}
              />
            )}
          </div>

          {/* Expiry */}
          {!loading && !error && data && (
            <div className="text-[11px] text-deck-text-dim">
              {expired ? '만료됨 / expired' : <>유효시간 {mmss} 남음</>}
            </div>
          )}

          {/* URL source toggle (only when both are available) */}
          {data && data.publicUrl && data.localUrl && (
            <div className="flex w-full rounded-lg overflow-hidden border border-deck-border text-xs">
              <button
                onClick={() => setUseLocal(false)}
                className={`flex-1 py-1.5 ${!useLocal ? 'bg-deck-accent/20 text-deck-accent' : 'text-deck-text-dim'}`}
              >
                Public URL
              </button>
              <button
                onClick={() => setUseLocal(true)}
                className={`flex-1 py-1.5 ${useLocal ? 'bg-deck-accent/20 text-deck-accent' : 'text-deck-text-dim'}`}
              >
                Local Wi-Fi
              </button>
            </div>
          )}

          {/* Active URL */}
          {activeUrl && (
            <div className="w-full text-[11px] break-all text-center text-deck-text-dim bg-deck-bg rounded-lg px-3 py-2 border border-deck-border/60">
              {activeUrl}
            </div>
          )}

          {/* LAN security warning */}
          {data?.warning && (
            <div className="w-full text-[11px] leading-relaxed text-amber-400 bg-amber-500/10 border border-amber-500/30 rounded-lg px-3 py-2">
              ⚠ {data.warning}
            </div>
          )}
        </div>

        {/* Footer actions */}
        <div className="flex flex-wrap gap-2 px-4 py-3 border-t border-deck-border">
          <button
            onClick={() => copy('public')}
            disabled={!data?.publicUrl}
            className="flex-1 min-w-[calc(50%-4px)] px-3 py-2 rounded-lg text-xs bg-deck-bg text-deck-text-dim disabled:opacity-40 active:opacity-70"
          >
            {copied === 'public' ? '복사됨 ✓' : '공개 주소 복사'}
          </button>
          <button
            onClick={() => copy('local')}
            disabled={!data?.localUrl}
            className="flex-1 min-w-[calc(50%-4px)] px-3 py-2 rounded-lg text-xs bg-deck-bg text-deck-text-dim disabled:opacity-40 active:opacity-70"
          >
            {copied === 'local' ? '복사됨 ✓' : '로컬 주소 복사'}
          </button>
          <button
            onClick={load}
            disabled={loading}
            className="flex-1 min-w-[calc(50%-4px)] px-3 py-2 rounded-lg text-xs bg-deck-bg text-deck-text-dim disabled:opacity-40 active:opacity-70"
          >
            다시 생성
          </button>
          <button
            onClick={onClose}
            className="flex-1 min-w-[calc(50%-4px)] btn-primary px-3 py-2 rounded-lg text-xs font-medium"
          >
            닫기
          </button>
        </div>
      </div>
    </>
  );
}
