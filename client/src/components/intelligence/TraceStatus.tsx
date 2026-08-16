import type { IntelligenceTrace } from '../../lib/api';

const STATUS: Record<string, { symbol: string; label: string; tone: string }> = {
  SUCCESS: { symbol: '✓', label: 'Completed locally', tone: 'text-deck-success' },
  CLOUD_COMPLETED: { symbol: '✓', label: 'Completed', tone: 'text-deck-success' },
  CLOUD_COMPLETED_WITH_FALLBACK: { symbol: '↪', label: 'Completed with cloud fallback', tone: 'text-deck-warning' },
  CLOUD_DISPATCHED: { symbol: '○', label: 'Cloud running', tone: 'text-deck-accent-light' },
  CLOUD_DISPATCHING: { symbol: '○', label: 'Starting cloud run', tone: 'text-deck-accent-light' },
  FALLBACK_CLOUD_DISPATCHED: { symbol: '↪', label: 'Cloud fallback running', tone: 'text-deck-warning' },
  FAILED: { symbol: '×', label: 'Local preprocessing failed', tone: 'text-deck-danger' },
  STARTED: { symbol: '○', label: 'Running', tone: 'text-deck-accent-light' },
};

export function traceStatusInfo(status: string) {
  return STATUS[status] || { symbol: '○', label: status.replace(/_/g, ' ').toLowerCase(), tone: 'text-deck-text-dim' };
}

export function TraceStatus({ trace, compact = false }: { trace: IntelligenceTrace; compact?: boolean }) {
  const info = traceStatusInfo(trace.status);
  return (
    <span className={`inline-flex min-w-0 items-center gap-1.5 ${info.tone} ${compact ? 'text-[11px]' : 'text-xs'}`}>
      <span aria-hidden="true" className="shrink-0 font-semibold">{info.symbol}</span>
      <span className="truncate">{info.label}</span>
    </span>
  );
}
