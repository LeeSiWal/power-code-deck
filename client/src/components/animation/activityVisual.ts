import type { ActivityNode } from '../../stores/appStore';

// Maps a Claude Code tool name to one of the sprite type keys in SPRITE_PRESET_MAP.
export function toolSpriteType(tool?: string): string {
  switch (tool) {
    case 'Read':
      return 'read';
    case 'Write':
    case 'Edit':
    case 'MultiEdit':
    case 'NotebookEdit':
      return 'write';
    case 'Bash':
    case 'BashOutput':
    case 'KillShell':
      return 'bash';
    case 'Grep':
    case 'Glob':
      return 'search';
    case 'WebFetch':
    case 'WebSearch':
      return 'web';
    case 'TodoWrite':
    case 'ExitPlanMode':
      return 'think';
    case 'Task':
    case 'Agent':
      return 'linker';
    default:
      return 'linker';
  }
}

// Semantic colors for the live status dot — deliberately fixed (not the agent's
// palette) so "working / idle" always reads the same regardless of agent color.
export const STATUS_COLOR: Record<ActivityNode['status'], string> = {
  working: '#10b981', // emerald
  thinking: '#f59e0b', // amber
  idle: '#64748b', // slate
  done: '#475569', // dim slate
};

export const STATUS_LABEL: Record<ActivityNode['status'], string> = {
  working: 'working',
  thinking: 'thinking',
  idle: 'idle',
  done: 'done',
};

// A sprite type for a node: what it's currently doing, or its identity when idle.
export function nodeSpriteType(node: ActivityNode): string {
  if (node.currentTool) return toolSpriteType(node.currentTool);
  return node.kind === 'subagent' ? 'linker' : 'think';
}

export function formatDuration(ms: number): string {
  if (ms < 0) ms = 0;
  const s = Math.floor(ms / 1000);
  if (s < 60) return `${s}s`;
  const m = Math.floor(s / 60);
  const rem = s % 60;
  return `${m}m${rem.toString().padStart(2, '0')}s`;
}
