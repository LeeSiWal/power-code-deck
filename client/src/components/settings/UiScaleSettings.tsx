import { useAppStore } from '../../stores/appStore';
import { USER_SCALE_MIN, USER_SCALE_MAX } from '../../lib/uiScale';

/**
 * UI size control — a manual zoom applied on top of the automatic resolution scaling.
 * Drives the same --ui-zoom the whole app scales by, so moving this grows/shrinks
 * every surface (chat, control room, panels) uniformly and live.
 */
export function UiScaleSettings() {
  const { uiScale, setUiScale } = useAppStore();
  const pct = Math.round(uiScale * 100);
  const min = Math.round(USER_SCALE_MIN * 100);
  const max = Math.round(USER_SCALE_MAX * 100);

  return (
    <div className="p-3 card space-y-3">
      <div className="flex items-center justify-between gap-3">
        <div className="min-w-0">
          <div className="text-sm font-medium">UI 크기</div>
          <div className="text-xs text-deck-text-dim">전체 화면 배율 — 해상도 자동 스케일 위에 적용됩니다</div>
        </div>
        <div className="flex items-center gap-2 shrink-0">
          <button
            onClick={() => setUiScale(uiScale - 0.05)}
            className="w-7 h-7 rounded bg-deck-bg border border-deck-border text-deck-text-dim hover:text-deck-text"
            aria-label="작게"
          >
            −
          </button>
          <span className="w-12 text-center text-sm tabular-nums">{pct}%</span>
          <button
            onClick={() => setUiScale(uiScale + 0.05)}
            className="w-7 h-7 rounded bg-deck-bg border border-deck-border text-deck-text-dim hover:text-deck-text"
            aria-label="크게"
          >
            ＋
          </button>
        </div>
      </div>

      <input
        type="range"
        min={min}
        max={max}
        step={5}
        value={pct}
        onChange={(e) => setUiScale(parseInt(e.target.value, 10) / 100)}
        className="w-full accent-deck-accent"
      />

      <div className="flex items-center justify-between">
        <span className="text-xs text-deck-text-dim">작게</span>
        <button onClick={() => setUiScale(1)} className="text-xs text-deck-accent hover:underline">
          기본값 (100%)
        </button>
        <span className="text-xs text-deck-text-dim">크게</span>
      </div>
    </div>
  );
}
