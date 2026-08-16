import { useEffect, useState } from 'react';
import { api, type IntelligenceTrace } from '../../lib/api';
import { IconChevronDown, IconChevronRight } from '../icons';
import { TraceDetail } from './TraceDetail';
import { TraceStatus } from './TraceStatus';
import {
  formatEstimatedTokens,
  formatReduction,
  savedEstimatedTokens,
  savingsState,
} from './savings';
import { cloudTargetName, type NativeDriverName } from './executionRouting';

function sessionOutcome(trace: IntelligenceTrace): string {
  const state = savingsState(trace);
  if (state === 'validated') return `${formatReduction(trace.reductionPercent)} saved`;
  if (state === 'local-only') return 'Local result';
  if (state === 'fallback') return 'Fallback';
  if (state === 'cloud-only') return 'Cloud Only';
  return 'Savings unavailable';
}

export function SessionSavingsSummary({
  agentId, refreshKey, driver,
}: { agentId: string; refreshKey: number; driver: NativeDriverName }) {
  const [trace, setTrace] = useState<IntelligenceTrace | null>(null);
  const [detail, setDetail] = useState<IntelligenceTrace | null>(null);
  const [detailLoading, setDetailLoading] = useState(false);

  useEffect(() => {
    let active = true;
    // The server fans the native result out just before its trace observer persists
    // CLOUD_COMPLETED. A short delay on result-triggered refreshes avoids reading the
    // preceding CLOUD_DISPATCHED snapshot in that tiny ordering window.
    const timer = window.setTimeout(() => {
      api.intelligenceTraces(50)
        .then((traces) => {
          if (active) setTrace(traces.find((item) => item.agentId === agentId) || null);
        })
        .catch(() => {
          // This panel is supplemental. Settings keeps the full retryable error state;
          // a failed activity request must never disturb the active coding session.
        });
    }, refreshKey > 0 ? 250 : 0);
    return () => {
      active = false;
      window.clearTimeout(timer);
    };
  }, [agentId, refreshKey]);

  if (!trace) return null;
  const state = savingsState(trace);
  const saved = savedEstimatedTokens(trace);

  const toggleDetail = async () => {
    if (detail) {
      setDetail(null);
      return;
    }
    setDetailLoading(true);
    try {
      setDetail(await api.intelligenceTrace(trace.id));
    } catch {
      // Keep the compact, already-loaded summary usable when detail loading fails.
    } finally {
      setDetailLoading(false);
    }
  };

  return (
    <div className="mb-3 rounded-xl border border-deck-accent/25 bg-deck-surface">
      <button onClick={() => void toggleDetail()} className="flex min-h-12 w-full min-w-0 items-center gap-2 rounded-xl px-3 py-2 text-left hover:bg-deck-border/20">
        <span className={`h-2 w-2 shrink-0 rounded-full ${state === 'validated' || state === 'local-only' ? 'bg-deck-success' : state === 'fallback' ? 'bg-deck-warning' : 'bg-deck-text-faint'}`} />
        <span className="min-w-0 flex-1">
          <span className="flex min-w-0 items-center gap-2">
            <span className="shrink-0 text-xs font-medium">Local Intelligence</span>
            <span className="truncate text-[11px] text-deck-accent-light">{sessionOutcome(trace)}</span>
          </span>
          <span className="mt-0.5 flex min-w-0 items-center gap-2">
            {trace.provider && (
              <span className="min-w-0 truncate text-[10px] text-deck-text-dim">
                {trace.provider} → {trace.mode === 'LOCAL_ONLY' ? 'Local result' : cloudTargetName(driver)}
              </span>
            )}
            {saved !== null && (
              <span className="shrink-0 text-[10px] text-deck-text-dim">
                {formatEstimatedTokens(trace.rawEstimatedTokens)} → {formatEstimatedTokens(trace.optimizedEstimatedTokens)} est. context
              </span>
            )}
            <TraceStatus trace={trace} compact />
          </span>
        </span>
        {detail ? <IconChevronDown size={13} className="shrink-0 text-deck-text-faint" /> : <IconChevronRight size={13} className="shrink-0 text-deck-text-faint" />}
      </button>
      {detailLoading && <div className="px-3 pb-3 text-center text-[11px] text-deck-text-dim">Loading trace detail…</div>}
      {detail && <div className="px-2 pb-2"><TraceDetail trace={detail} onClose={() => setDetail(null)} /></div>}
    </div>
  );
}
