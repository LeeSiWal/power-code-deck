import { useState, useEffect } from 'react';
import { useAppStore } from '../../stores/appStore';
import { PixelSprite } from './PixelSprite';
import { getSpritePreset, type CharacterTheme } from './sprites/presets';
import { generatePalette } from '../../lib/paletteGenerator';
import { STATUS_COLOR, nodeSpriteType } from './activityVisual';

interface SubAgentBarProps {
  agentId: string;
}

/**
 * Compact one-line activity readout shown above the terminal: the main agent plus any
 * running sub-agents, each with a live status dot and its current tool. Driven by the
 * transcript-based activity snapshot.
 */
export function SubAgentBar({ agentId }: SubAgentBarProps) {
  const { activity, agents, characterTheme } = useAppStore();
  const snap = activity.get(agentId);
  const agent = agents.find((a) => a.id === agentId);
  const palette = generatePalette(agent?.colorHue ?? 220);
  const theme = characterTheme as CharacterTheme;

  const live = (snap?.nodes || []).filter((n) => n.status === 'working' || n.status === 'thinking');

  // Tick to advance the current-tool timing display.
  const [, setTick] = useState(0);
  useEffect(() => {
    if (live.length === 0) return;
    const id = setInterval(() => setTick((t) => t + 1), 1000);
    return () => clearInterval(id);
  }, [live.length]);

  if (live.length === 0) return null;

  const subActive = live.filter((n) => n.kind === 'subagent').length;

  return (
    <div className="flex items-center gap-3 px-3 py-1.5 overflow-x-auto scrollbar-hide border-b border-deck-border/50">
      {live.map((node) => {
        const working = node.status === 'working';
        const preset = getSpritePreset(nodeSpriteType(node), theme);
        const label = working && node.currentTool ? node.currentTool : node.label;
        return (
          <div
            key={node.id}
            className="flex items-center gap-1.5 shrink-0"
            title={`${node.label}${node.currentTool ? ` · ${node.currentTool}` : ''} (${node.status})`}
          >
            <span
              className="h-1.5 w-1.5 rounded-full shrink-0"
              style={{ backgroundColor: STATUS_COLOR[node.status] }}
            />
            <PixelSprite
              preset={preset}
              palette={palette}
              state={working ? 'active' : 'idle'}
              size={20}
              glow={working}
              glowColor={palette[2]}
            />
            <span className="text-[10px] font-medium" style={{ color: working ? palette[3] : palette[1] }}>
              {node.kind === 'subagent' ? node.label : label}
            </span>
          </div>
        );
      })}
      {subActive > 0 && (
        <span className="text-[10px] text-deck-text-dim ml-auto whitespace-nowrap shrink-0">
          {subActive} sub-agent{subActive > 1 ? 's' : ''}
        </span>
      )}
    </div>
  );
}
