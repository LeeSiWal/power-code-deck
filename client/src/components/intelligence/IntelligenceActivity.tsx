import { useCallback, useEffect, useMemo, useState } from 'react';
import { api, type IntelligenceTrace } from '../../lib/api';
import { useAppStore } from '../../stores/appStore';
import { IconChevronRight, IconRefresh } from '../icons';
import { SavingsSummary } from './SavingsSummary';
import { TraceDetail } from './TraceDetail';
import { TraceStatus } from './TraceStatus';
import {
  aggregateSavings,
  formatEstimatedTokens,
  formatMode,
  formatReduction,
  formatRelativeTime,
  savingsState,
} from './savings';

function recentResult(trace: IntelligenceTrace): string {
  const state = savingsState(trace);
  if (state === 'validated') return `${formatReduction(trace.reductionPercent)} saved`;
  if (state === 'local-only') return 'Local result';
  if (state === 'fallback') return 'Fallback';
  if (state === 'cloud-only') return 'Cloud Only';
  return 'Unavailable';
}

export function IntelligenceActivity() {
  const [traces, setTraces] = useState<IntelligenceTrace[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [selected, setSelected] = useState<IntelligenceTrace | null>(null);
  const [detailLoading, setDetailLoading] = useState(false);
  const [detailError, setDetailError] = useState('');

  const load = useCallback(async () => {
    setLoading(true);
    setError('');
    try {
      setTraces(await api.intelligenceTraces(20));
    } catch {
      setError('Unable to load intelligence traces.');
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => { void load(); }, [load]);

  const viewTrace = async (trace: IntelligenceTrace) => {
    if (selected?.id === trace.id) {
      setSelected(null);
      return;
    }
    setDetailLoading(true);
    setDetailError('');
    try {
      setSelected(await api.intelligenceTrace(trace.id));
    } catch {
      setSelected(null);
      setDetailError('Unable to load trace detail.');
    } finally {
      setDetailLoading(false);
    }
  };

  // The REST list is a snapshot; runs keep moving after it. Overlay whatever the
  // socket has pushed since — including runs that started after this panel loaded,
  // which is the normal case when a run is kicked off from a chat in another tab.
  const liveRuns = useAppStore((s) => s.intelligenceRuns);
  const merged = useMemo(() => {
    const byId = new Map(traces.map((item) => [item.id, item]));
    for (const run of liveRuns.values()) byId.set(run.trace.id, run.trace);
    return [...byId.values()]
      .sort((a, b) => b.createdAt.localeCompare(a.createdAt))
      .slice(0, 20);
  }, [traces, liveRuns]);

  const aggregate = aggregateSavings(merged);
  const latest = merged[0];

  return (
    <div className="card p-3">
      <div className="flex items-start justify-between gap-3">
        <div>
          <div className="text-sm font-medium">Local Intelligence Activity</div>
          <div className="mt-1 text-xs text-deck-text-dim">Verified context measurements from recent runs.</div>
        </div>
        <button onClick={() => void load()} disabled={loading} aria-label="Refresh Local Intelligence activity" className="flex h-9 w-9 shrink-0 items-center justify-center rounded-lg text-deck-text-dim hover:bg-deck-border/50 disabled:opacity-50">
          <IconRefresh size={14} className={loading ? 'animate-spin' : ''} />
        </button>
      </div>

      <div className="mt-3 rounded-lg border border-deck-border/70 bg-deck-bg/40 px-3 py-2 text-[10px] leading-relaxed text-deck-text-dim">
        Estimated context tokens measure PowerCodeDeck's pre-cloud context reduction. They do not represent Codex's complete internal token usage or billing.
      </div>

      {loading && merged.length === 0 ? (
        <div className="py-6 text-center text-xs text-deck-text-dim">Loading Local Intelligence data…</div>
      ) : error ? (
        <div className="py-5 text-center">
          <div className="text-xs text-deck-danger">{error}</div>
          <button onClick={() => void load()} className="mt-2 min-h-9 rounded-lg border border-deck-border px-3 text-xs">Retry</button>
        </div>
      ) : merged.length === 0 ? (
        <div className="py-6 text-center">
          <div className="text-xs font-medium">No Local Intelligence runs yet.</div>
          <div className="mt-1 text-xs text-deck-text-dim">Run a Hybrid task to measure context savings.</div>
        </div>
      ) : (
        <div className="mt-3 space-y-4">
          <section>
            <div className="mb-2 flex items-center justify-between gap-3">
              <div className="text-xs font-medium">Latest run</div>
              <button onClick={() => void viewTrace(latest)} className="min-h-9 rounded-lg px-2 text-[11px] text-deck-accent-light hover:bg-deck-border/30">View trace</button>
            </div>
            <SavingsSummary trace={latest} />
          </section>

          {aggregate && (
            <section className="rounded-lg border border-deck-border p-3">
              <div className="flex items-baseline justify-between gap-2">
                <div className="text-xs font-medium">Estimated context reduction</div>
                <div className="text-[10px] text-deck-text-faint">{aggregate.runCount} validated Hybrid {aggregate.runCount === 1 ? 'run' : 'runs'}</div>
              </div>
              <div className="mt-3 grid grid-cols-2 gap-x-4 gap-y-3 text-xs">
                <div><div className="text-[10px] text-deck-text-faint">Raw context</div><div className="font-medium tabular-nums">{formatEstimatedTokens(aggregate.totalRaw)} est.</div></div>
                <div><div className="text-[10px] text-deck-text-faint">Optimized</div><div className="font-medium tabular-nums">{formatEstimatedTokens(aggregate.totalOptimized)} est.</div></div>
                <div><div className="text-[10px] text-deck-text-faint">Compressed</div><div className="font-medium tabular-nums text-deck-success">{formatEstimatedTokens(aggregate.totalCompressed)} est.</div></div>
                <div><div className="text-[10px] text-deck-text-faint">Reduction</div><div className="font-medium tabular-nums">{formatReduction(aggregate.overallReduction)}</div></div>
              </div>
            </section>
          )}

          <section>
            <div className="mb-1 text-xs font-medium">Recent runs</div>
            <div className="divide-y divide-deck-border/60">
              {merged.map((trace) => (
                <button key={trace.id} onClick={() => void viewTrace(trace)} className="flex min-h-12 w-full min-w-0 items-center gap-2 py-2 text-left hover:bg-deck-border/20">
                  <div className="min-w-0 flex-1">
                    <div className="flex min-w-0 items-center gap-2">
                      <span className="shrink-0 text-xs font-medium">{recentResult(trace)}</span>
                      <span className="truncate text-[10px] text-deck-text-faint">{formatMode(trace.mode)}</span>
                    </div>
                    <div className="mt-0.5 flex min-w-0 items-center gap-2">
                      <TraceStatus trace={trace} compact />
                      <span className="shrink-0 text-[10px] text-deck-text-faint">{formatRelativeTime(trace.createdAt)}</span>
                    </div>
                  </div>
                  <IconChevronRight size={13} className="shrink-0 text-deck-text-faint" />
                </button>
              ))}
            </div>
          </section>
        </div>
      )}

      {detailLoading && <div className="mt-3 text-center text-xs text-deck-text-dim">Loading trace detail…</div>}
      {detailError && <div className="mt-3 text-xs text-deck-danger">{detailError}</div>}
      {selected && !detailLoading && <TraceDetail trace={selected} onClose={() => setSelected(null)} />}
    </div>
  );
}
