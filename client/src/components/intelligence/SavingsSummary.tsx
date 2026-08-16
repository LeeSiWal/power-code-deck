import type { IntelligenceTrace } from '../../lib/api';
import { SavingsBar } from './SavingsBar';
import { TraceStatus } from './TraceStatus';
import {
  formatEstimatedTokens,
  formatLatency,
  formatReduction,
  savedEstimatedTokens,
  savingsState,
} from './savings';

function Metric({ label, value, exact }: { label: string; value: string; exact?: string }) {
  return (
    <div className="min-w-0">
      <div className="text-[10px] uppercase tracking-wide text-deck-text-faint">{label}</div>
      <div title={exact} className="mt-0.5 truncate text-sm font-medium tabular-nums">{value}</div>
    </div>
  );
}

export function SavingsSummary({ trace }: { trace: IntelligenceTrace }) {
  const state = savingsState(trace);

  if (state === 'fallback') {
    return (
      <div className="rounded-lg border border-deck-warning/30 bg-deck-warning/5 p-3">
        <div className="text-xs font-medium text-deck-warning">Local optimization unavailable</div>
        <div className="mt-2 grid grid-cols-2 gap-2 text-xs">
          <span className="text-deck-text-dim">Fallback</span><span>Cloud only</span>
          <span className="text-deck-text-dim">Reason</span><span className="break-all font-mono text-[11px]">{trace.errorCode || 'Unknown'}</span>
        </div>
        <div className="mt-2"><TraceStatus trace={trace} /></div>
      </div>
    );
  }

  if (state === 'cloud-only') {
    return (
      <div className="rounded-lg border border-deck-border bg-deck-bg/40 p-3">
        <div className="text-xs font-medium">Cloud Only</div>
        <div className="mt-1 text-xs text-deck-text-dim">No local context optimization</div>
        <div className="mt-2"><TraceStatus trace={trace} /></div>
      </div>
    );
  }

  if (state === 'unavailable') {
    return (
      <div className="rounded-lg border border-deck-border bg-deck-bg/40 p-3">
        <div className="text-xs font-medium">Savings unavailable</div>
        <div className="mt-1 text-xs text-deck-text-dim">No validated reduction</div>
        <div className="mt-2"><TraceStatus trace={trace} /></div>
      </div>
    );
  }

  const saved = savedEstimatedTokens(trace)!;
  const localOnly = state === 'local-only';
  return (
    <div className="rounded-lg border border-deck-accent/25 bg-deck-accent/5 p-3 space-y-3">
      <div className="flex min-w-0 items-start justify-between gap-3">
        <div className="min-w-0">
          <div className="text-xs font-medium">{localOnly ? 'Local context reduction' : 'Local Intelligence'}</div>
          <div className="mt-0.5 truncate text-[11px] text-deck-text-dim" title={[trace.provider, trace.model].filter(Boolean).join(' / ')}>
            {[trace.provider, trace.model].filter(Boolean).join(' / ') || 'Local provider'}
          </div>
        </div>
        <TraceStatus trace={trace} compact />
      </div>

      <div className="grid grid-cols-3 gap-3">
        <Metric label="Raw context" value={`${formatEstimatedTokens(trace.rawEstimatedTokens)} est.`} exact={`${trace.rawEstimatedTokens.toLocaleString()} estimated tokens`} />
        <Metric label="Optimized" value={`${formatEstimatedTokens(trace.optimizedEstimatedTokens)} est.`} exact={`${trace.optimizedEstimatedTokens.toLocaleString()} estimated tokens`} />
        <Metric label={localOnly ? 'Reduced' : 'Saved'} value={`${formatEstimatedTokens(saved)} est.`} exact={`${saved.toLocaleString()} estimated tokens`} />
      </div>

      <SavingsBar trace={trace} localOnly={localOnly} />

      <div className="grid grid-cols-2 gap-3 border-t border-deck-border/70 pt-2">
        <Metric label="Reduction" value={formatReduction(trace.reductionPercent)} />
        <Metric label="Local latency" value={formatLatency(trace.latencyMs)} />
      </div>
    </div>
  );
}
