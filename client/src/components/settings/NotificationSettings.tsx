import { useCallback, useEffect, useState } from 'react';
import { disablePush, enablePush, pushState, type PushState } from '../../lib/push';

/**
 * Push-notification opt-in. One toggle, plus honest messaging for the two states a
 * user can't just toggle out of: iOS-not-installed (must Add to Home Screen first)
 * and permission-denied (must re-allow in browser settings).
 */
export function NotificationSettings() {
  const [state, setState] = useState<PushState>('off');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');

  const refresh = useCallback(() => {
    pushState().then(setState).catch(() => setState('off'));
  }, []);

  useEffect(refresh, [refresh]);

  const toggle = useCallback(async () => {
    setError('');
    setBusy(true);
    try {
      if (state === 'on') {
        await disablePush();
      } else {
        await enablePush();
      }
      await pushState().then(setState);
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  }, [state]);

  const on = state === 'on';
  const toggleable = state === 'on' || state === 'off';

  return (
    <div className="p-3 card space-y-2">
      <div className="flex items-center justify-between">
        <div className="min-w-0">
          <div className="text-sm font-medium">알림 (Web Push)</div>
          <div className="text-xs text-deck-text-dim">
            승인 요청·작업 완료를 이 기기로 백그라운드 알림
          </div>
        </div>
        <button
          onClick={toggle}
          disabled={busy || !toggleable}
          role="switch"
          aria-checked={on}
          className={`shrink-0 w-11 h-6 rounded-full transition-colors relative disabled:opacity-40 ${
            on ? 'bg-deck-accent' : 'bg-deck-border'
          }`}
        >
          <span
            className={`absolute top-0.5 left-0.5 w-5 h-5 rounded-full bg-white shadow-sm transition-transform ${
              on ? 'translate-x-5' : 'translate-x-0'
            }`}
          />
        </button>
      </div>

      {state === 'needs-install' && (
        <div className="text-xs text-amber-400 leading-snug">
          iOS/iPadOS는 <b>홈 화면에 추가</b>한 뒤에만 알림을 받을 수 있어요. 공유 → “홈 화면에
          추가”로 설치한 다음, 설치된 앱에서 이 설정을 다시 켜주세요.
        </div>
      )}
      {state === 'unsupported' && (
        <div className="text-xs text-deck-text-dim">이 브라우저는 웹 푸시를 지원하지 않습니다.</div>
      )}
      {state === 'denied' && (
        <div className="text-xs text-amber-400 leading-snug">
          알림 권한이 차단되어 있어요. 브라우저 사이트 설정에서 알림을 허용으로 바꾼 뒤 다시
          시도해주세요.
        </div>
      )}
      {error && <div className="text-xs text-red-400 leading-snug">{error}</div>}
    </div>
  );
}
