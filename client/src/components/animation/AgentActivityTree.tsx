import { useState, useEffect } from 'react';
import { AgentActivity, ActivityNode, useAppStore } from '../../stores/appStore';
import { PixelSprite } from './PixelSprite';
import { getSpritePreset, type CharacterTheme } from './sprites/presets';
import { STATUS_COLOR, STATUS_LABEL, nodeSpriteType, formatDuration } from './activityVisual';

interface Props {
  activity: AgentActivity;
  palette: string[];
}

/**
 * Live tree of the main agent + its sub-agents. Answers "are the agents actually
 * working?" at a glance: a status dot (working / thinking / idle / done), what tool
 * each node is running right now, and how long it has been at it.
 */
export function AgentActivityTree({ activity, palette }: Props) {
  const { characterTheme } = useAppStore();
  const theme = characterTheme as CharacterTheme;

  // Local 1s tick so elapsed timers advance smoothly between server snapshots.
  const [, setTick] = useState(0);
  const anyLive = activity.nodes.some((n) => n.status === 'working' || n.status === 'thinking');
  useEffect(() => {
    if (!anyLive) return;
    const id = setInterval(() => setTick((t) => t + 1), 1000);
    return () => clearInterval(id);
  }, [anyLive]);

  const main = activity.nodes.find((n) => n.kind === 'main');
  const subs = activity.nodes.filter((n) => n.kind === 'subagent');

  return (
    <div className="space-y-1">
      {main && <NodeRow node={main} palette={palette} theme={theme} depth={0} />}
      {subs.map((n) => (
        <NodeRow key={n.id} node={n} palette={palette} theme={theme} depth={1} />
      ))}
    </div>
  );
}

function NodeRow({
  node,
  palette,
  theme,
  depth,
}: {
  node: ActivityNode;
  palette: string[];
  theme: CharacterTheme;
  depth: number;
}) {
  const active = node.status === 'working';
  const dotColor = STATUS_COLOR[node.status];
  const preset = getSpritePreset(nodeSpriteType(node), theme);

  const now = Date.now();
  const elapsedMs = node.status === 'done'
    ? node.lastActivityAt - node.startedAt
    : now - (active ? node.lastActivityAt : node.startedAt);

  // Right-side descriptor: what it's doing / its state.
  let detail = STATUS_LABEL[node.status] as string;
  if (active && node.currentTool) {
    detail = node.currentTarget ? `${node.currentTool} · ${node.currentTarget}` : node.currentTool;
  }

  return (
    <div
      className="flex items-center gap-2 py-1 rounded-md"
      style={{ paddingLeft: depth * 16 }}
    >
      {depth > 0 && <span className="text-deck-text-dim/50 text-xs select-none">└─</span>}

      {/* status dot with pulse when working */}
      <span className="relative flex h-2.5 w-2.5 shrink-0">
        {active && (
          <span
            className="absolute inline-flex h-full w-full rounded-full opacity-60 animate-ping"
            style={{ backgroundColor: dotColor }}
          />
        )}
        <span className="relative inline-flex rounded-full h-2.5 w-2.5" style={{ backgroundColor: dotColor }} />
      </span>

      {/* character avatar */}
      <PixelSprite
        preset={preset}
        palette={palette}
        state={active ? 'active' : 'idle'}
        size={depth > 0 ? 20 : 24}
        glow={active}
        glowColor={palette[2]}
        className={node.status === 'idle' || node.status === 'done' ? 'opacity-40' : ''}
      />

      {/* label + detail */}
      <div className="min-w-0 flex-1">
        <div className="flex items-baseline gap-2">
          <span
            className={`truncate ${depth === 0 ? 'text-xs font-semibold' : 'text-[11px] font-medium'}`}
            style={{ color: active ? palette[3] : undefined }}
          >
            {node.label}
          </span>
          {node.kind === 'subagent' && (
            <span className="text-[9px] uppercase tracking-wider text-deck-text-dim shrink-0">sub</span>
          )}
        </div>
        <div className="truncate text-[10px] text-deck-text-dim" style={{ color: active ? dotColor : undefined }}>
          {detail}
        </div>
      </div>

      {/* right meta: tool count + elapsed */}
      <div className="flex flex-col items-end shrink-0">
        <span className="text-[10px] tabular-nums" style={{ color: dotColor }}>
          {formatDuration(elapsedMs)}
        </span>
        <span className="text-[9px] text-deck-text-dim tabular-nums">{node.toolCount} tools</span>
      </div>
    </div>
  );
}
