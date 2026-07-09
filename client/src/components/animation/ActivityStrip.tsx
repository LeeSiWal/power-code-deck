import { useState, useEffect } from 'react';
import { AgentActivity } from '../../stores/appStore';
import { toolSpriteType, formatDuration } from './activityVisual';
import { getSpritePreset, type CharacterTheme } from './sprites/presets';
import { PixelSprite } from './PixelSprite';
import { useAppStore } from '../../stores/appStore';

interface Props {
  activity: AgentActivity;
  palette: string[];
}

/**
 * Reverse-chronological list of recent tool calls with durations. Lets you confirm
 * the agent keeps making progress rather than being stuck on one thing.
 */
export function ActivityStrip({ activity, palette }: Props) {
  const { characterTheme } = useAppStore();
  const theme = characterTheme as CharacterTheme;

  const hasRunning = activity.recent.some((e) => !e.endedAt);
  const [, setTick] = useState(0);
  useEffect(() => {
    if (!hasRunning) return;
    const id = setInterval(() => setTick((t) => t + 1), 1000);
    return () => clearInterval(id);
  }, [hasRunning]);

  if (activity.recent.length === 0) return null;

  const now = Date.now();

  return (
    <div>
      <h4 className="mb-1.5 text-[11px] font-medium uppercase tracking-wider text-deck-text-dim">Recent</h4>
      <div className="space-y-0.5">
        {activity.recent.map((e, i) => {
          const running = !e.endedAt;
          const preset = getSpritePreset(toolSpriteType(e.tool), theme);
          const dur = running ? now - e.startedAt : (e.endedAt as number) - e.startedAt;
          return (
            <div
              key={`${e.startedAt}-${i}`}
              className="flex items-center gap-2 rounded px-1 py-0.5 text-[11px]"
              style={{ opacity: running ? 1 : 0.7 }}
            >
              <PixelSprite
                preset={preset}
                palette={palette}
                state={running ? 'active' : 'idle'}
                size={16}
                glow={running}
                glowColor={palette[2]}
              />
              <span className="font-medium shrink-0" style={{ color: running ? palette[3] : palette[2] }}>
                {e.tool}
              </span>
              {e.sidechain && (
                <span className="text-[8px] uppercase tracking-wider text-deck-text-dim shrink-0">sub</span>
              )}
              <span className="truncate text-deck-text-dim">{e.target}</span>
              <span
                className="ml-auto shrink-0 tabular-nums text-[10px] text-deck-text-dim"
                style={running ? { color: palette[2] } : undefined}
              >
                {running ? `${formatDuration(dur)}…` : formatDuration(dur)}
              </span>
            </div>
          );
        })}
      </div>
    </div>
  );
}
