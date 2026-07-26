import type { AgentSummary } from '../../stores/appStore';

export const ATTN_ORDER: Record<string, number> = { approval: 0, error: 1, stalled: 2 };

export function attnClasses(primary: string): string {
  switch (primary) {
    case 'approval':
      return 'border-deck-warning text-deck-warning';
    case 'error':
      return 'border-deck-danger text-deck-danger';
    case 'stalled':
      return 'border-deck-text-dim text-deck-text-dim';
    default:
      return 'border-deck-border text-deck-text-dim';
  }
}

export function attnLabel(r: { kind: string; count?: number }): string {
  const base = r.kind;
  return r.count && r.count > 1 ? `${base} ·${r.count}` : base;
}

export function kindGlyph(preset: string): string {
  const p = (preset || '').toLowerCase();
  if (p.includes('codex')) return 'codex';
  if (p.includes('claude')) return 'claude';
  return 'shell';
}

export function timeAgo(ms: number): string {
  if (!ms) return '—';
  const s = Math.max(0, Math.floor((Date.now() - ms) / 1000));
  if (s < 5) return 'now';
  if (s < 60) return `${s}s`;
  const m = Math.floor(s / 60);
  if (m < 60) return `${m}m`;
  const h = Math.floor(m / 60);
  if (h < 24) return `${h}h`;
  return `${Math.floor(h / 24)}d`;
}

// Three live states, the thing the overview must make obvious at a glance:
//   working — running AND produced activity in the last ~30s (a tool is moving)
//   idle    — running but quiet (alive, on standby)
//   stopped — not running
export type LiveState = 'working' | 'idle' | 'stopped';

export const WORKING_WINDOW_MS = 30_000;

export function liveState(s: AgentSummary): LiveState {
  if (s.status !== 'running') return 'stopped';
  if (s.lastActivityAt > 0 && Date.now() - s.lastActivityAt < WORKING_WINDOW_MS) return 'working';
  return 'idle';
}

export const STATE_CHIP: Record<LiveState, { label: string; cls: string }> = {
  working: { label: '작업 중', cls: 'border-deck-accent/50 text-deck-accent-light bg-deck-accent/10' },
  idle: { label: '대기', cls: 'border-deck-success/40 text-deck-success' },
  stopped: { label: '정지', cls: 'border-deck-border text-deck-text-faint' },
};
