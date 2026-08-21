import type { IntelligenceTrace } from '../../lib/api';
import { compressedEstimatedTokens, formatEstimatedTokens } from './savings';

export function SavingsBar({ trace, localOnly = false }: { trace: IntelligenceTrace; localOnly?: boolean }) {
  const compressed = compressedEstimatedTokens(trace);
  if (compressed === null) return null;
  const optimizedWidth = Math.max(3, (trace.optimizedEstimatedTokens / trace.rawEstimatedTokens) * 100);

  return (
    <div className="space-y-2" aria-label="Estimated context comparison">
      <div className="grid grid-cols-[4.5rem_minmax(0,1fr)_auto] items-center gap-2 text-[11px]">
        <span className="text-deck-text-dim">Raw</span>
        <div className="h-2 overflow-hidden rounded-full bg-deck-border" aria-hidden="true">
          <div className="h-full w-full rounded-full bg-deck-text-faint" />
        </div>
        <span title={`${trace.rawEstimatedTokens.toLocaleString()} estimated tokens`} className="tabular-nums">
          {formatEstimatedTokens(trace.rawEstimatedTokens)}
        </span>
      </div>
      <div className="grid grid-cols-[4.5rem_minmax(0,1fr)_auto] items-center gap-2 text-[11px]">
        <span className="text-deck-text-dim">Optimized</span>
        <div className="h-2 overflow-hidden rounded-full bg-deck-border" aria-hidden="true">
          <div className="h-full rounded-full bg-deck-accent" style={{ width: `${optimizedWidth}%` }} />
        </div>
        <span title={`${trace.optimizedEstimatedTokens.toLocaleString()} estimated tokens`} className="tabular-nums">
          {formatEstimatedTokens(trace.optimizedEstimatedTokens)}
        </span>
      </div>
      <div className="text-right text-[10px] text-deck-text-dim">
        Compressed locally: {formatEstimatedTokens(compressed)} est.
      </div>
    </div>
  );
}
