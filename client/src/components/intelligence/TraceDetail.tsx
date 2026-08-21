import type { IntelligenceTrace, IntelligenceTraceEvent } from '../../lib/api';
import { IconClose } from '../icons';
import { SavingsSummary } from './SavingsSummary';
import { TraceStatus } from './TraceStatus';
import {
  cloudFailureCode, formatCreatedAt, formatEstimatedTokens, formatLatency, formatMode,
  formatReduction, localFailureCode,
} from './savings';

const STAGE_LABELS: Record<string, string> = {
  task_received: 'Task received',
  validation: 'Validation',
  repository_scan: 'Repository scan',
  local_request: 'Local request',
  local_response: 'Local response',
  local_processing: 'Local processing',
  context_measurement: 'Context measurement',
  cloud_execution: 'Cloud execution',
  fallback: 'Cloud fallback',
  result: 'Result',
};

const SAFE_DETAIL_KEYS: Record<string, string[]> = {
  task_received: ['mode'],
  repository_scan: ['source', 'candidateFiles', 'contextBytes', 'rawEstimatedTokens'],
  local_request: ['provider', 'model', 'timeoutMs'],
  local_response: ['latencyMs', 'localTokens'],
  context_measurement: ['rawEstimatedTokens', 'optimizedEstimatedTokens', 'reductionPercent'],
  local_processing: ['errorCode', 'reason'],
  cloud_execution: ['driver', 'nativeTurnBoundary', 'errorCode', 'reason'],
  fallback: ['reason'],
  result: ['cloudExecution'],
};

function detailLabel(key: string): string {
  const labels: Record<string, string> = {
    candidateFiles: 'Candidate files', rawEstimatedTokens: 'Raw', optimizedEstimatedTokens: 'Optimized',
    reductionPercent: 'Reduction', latencyMs: 'Latency', localTokens: 'Local output',
    nativeTurnBoundary: 'Turn completed', cloudExecution: 'Cloud execution',
    contextBytes: 'Context bytes', errorCode: 'Error code', source: 'Source', reason: 'Reason',
    timeoutMs: 'Timeout',
  };
  return labels[key] || key.replace(/([A-Z])/g, ' $1').replace(/^./, (char) => char.toUpperCase());
}

function detailValue(key: string, value: unknown): string {
  if (typeof value === 'boolean') return value ? 'Yes' : 'No';
  if (typeof value === 'number') {
    if (key === 'reductionPercent') return formatReduction(value);
    if (key === 'latencyMs') return formatLatency(value);
    if (key === 'localTokens') return value.toLocaleString();
    if (key.toLowerCase().includes('tokens')) return `${value.toLocaleString()} est.`;
    return value.toLocaleString();
  }
  return String(value);
}

function safeDetails(event: IntelligenceTraceEvent): [string, unknown][] {
  const allowed = SAFE_DETAIL_KEYS[event.stage] || [];
  return allowed.flatMap((key) => event.details && event.details[key] !== undefined
    ? [[key, event.details[key]] as [string, unknown]] : []);
}

function EventRow({ event }: { event: IntelligenceTraceEvent }) {
  const failed = event.status === 'FAILED';
  const pending = event.status === 'STARTED' || event.status === 'DISPATCHED';
  const details = safeDetails(event);
  return (
    <li className="relative pl-6 pb-4 last:pb-0">
      <span className={`absolute left-0 top-0.5 flex h-4 w-4 items-center justify-center rounded-full border text-[9px] ${
        failed ? 'border-deck-danger text-deck-danger' : pending
          ? 'border-deck-accent text-deck-accent-light' : 'border-deck-success text-deck-success'
      }`}>{failed ? '×' : pending ? '○' : '✓'}</span>
      <div className="flex flex-wrap items-baseline justify-between gap-x-2">
        <span className="text-xs font-medium">{STAGE_LABELS[event.stage] || event.stage.replace(/_/g, ' ')}</span>
        <span className="font-mono text-[10px] text-deck-text-faint">{event.status}</span>
      </div>
      {details.length > 0 && (
        <div className="mt-1 grid grid-cols-[minmax(0,1fr)_auto] gap-x-3 gap-y-0.5 text-[11px]">
          {details.map(([key, value]) => (
            <div key={key} className="contents">
              <span className="truncate text-deck-text-dim">{detailLabel(key)}</span>
              <span className="max-w-[12rem] break-words text-right font-mono">{detailValue(key, value)}</span>
            </div>
          ))}
        </div>
      )}
    </li>
  );
}

export function TraceDetail({ trace, onClose }: { trace: IntelligenceTrace; onClose: () => void }) {
  const localError = localFailureCode(trace);
  const cloudError = cloudFailureCode(trace);
  return (
    <section className="mt-3 rounded-xl border border-deck-border bg-deck-raised p-3" aria-label={`Trace ${trace.id}`}>
      <div className="flex items-start justify-between gap-3">
        <div className="min-w-0">
          <div className="font-mono text-[10px] uppercase tracking-wide text-deck-accent-light">Trace {trace.id}</div>
          <div className="mt-1"><TraceStatus trace={trace} /></div>
        </div>
        <button onClick={onClose} aria-label="Close trace detail" className="-mr-1 -mt-1 flex h-9 w-9 shrink-0 items-center justify-center rounded-lg text-deck-text-dim hover:bg-deck-border/50">
          <IconClose size={14} />
        </button>
      </div>

      <div className="mt-3"><SavingsSummary trace={trace} /></div>

      <dl className="mt-3 grid grid-cols-2 gap-x-3 gap-y-2 text-xs">
        <div><dt className="text-[10px] text-deck-text-faint">Mode</dt><dd>{formatMode(trace.mode)}</dd></div>
        <div><dt className="text-[10px] text-deck-text-faint">Raw status</dt><dd className="break-all font-mono text-[10px]">{trace.status}</dd></div>
        <div><dt className="text-[10px] text-deck-text-faint">Created</dt><dd>{formatCreatedAt(trace.createdAt)}</dd></div>
        <div><dt className="text-[10px] text-deck-text-faint">Latency</dt><dd>{formatLatency(trace.latencyMs)}</dd></div>
        <div className="min-w-0"><dt className="text-[10px] text-deck-text-faint">Provider</dt><dd className="break-words">{trace.provider || '—'}</dd></div>
        <div className="min-w-0"><dt className="text-[10px] text-deck-text-faint">Model</dt><dd className="break-words">{trace.model || '—'}</dd></div>
        <div><dt className="text-[10px] text-deck-text-faint">Raw context</dt><dd>{trace.rawEstimatedTokens > 0 ? `${formatEstimatedTokens(trace.rawEstimatedTokens)} est.` : '—'}</dd></div>
        <div><dt className="text-[10px] text-deck-text-faint">Optimized</dt><dd>{trace.optimizedEstimatedTokens > 0 ? `${formatEstimatedTokens(trace.optimizedEstimatedTokens)} est.` : '—'}</dd></div>
        <div><dt className="text-[10px] text-deck-text-faint">Reduction</dt><dd>{trace.reductionPercent > 0 ? formatReduction(trace.reductionPercent) : '—'}</dd></div>
        {localError && <div><dt className="text-[10px] text-deck-text-faint">Local error</dt><dd className="break-all font-mono text-[10px] text-deck-danger">{localError}</dd></div>}
        {cloudError && <div><dt className="text-[10px] text-deck-text-faint">Cloud error</dt><dd className="break-all font-mono text-[10px] text-deck-danger">{cloudError}</dd></div>}
        {trace.errorCode && !localError && !cloudError && <div><dt className="text-[10px] text-deck-text-faint">Error code</dt><dd className="break-all font-mono text-[10px] text-deck-danger">{trace.errorCode}</dd></div>}
      </dl>

      <div className="mt-4 border-t border-deck-border pt-3">
        <div className="mb-3 text-xs font-medium">Execution stages</div>
        {trace.events.length > 0 ? (
          <ol>{trace.events.map((event, index) => <EventRow key={`${event.at}-${event.stage}-${index}`} event={event} />)}</ol>
        ) : (
          <div className="text-xs text-deck-text-dim">No execution events recorded.</div>
        )}
      </div>
    </section>
  );
}
