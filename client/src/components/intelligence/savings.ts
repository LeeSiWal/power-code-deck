import type { IntelligenceTrace } from '../../lib/api';

export type SavingsState = 'validated' | 'fallback' | 'cloud-only' | 'local-only' | 'unavailable';

export interface SavingsAggregate {
  runCount: number;
  totalRaw: number;
  totalOptimized: number;
  totalCompressed: number;
  overallReduction: number;
}

export function hasValidReduction(trace: IntelligenceTrace): boolean {
  return Number.isFinite(trace.rawEstimatedTokens)
    && Number.isFinite(trace.optimizedEstimatedTokens)
    && Number.isFinite(trace.reductionPercent)
    && trace.rawEstimatedTokens > 0
    && trace.optimizedEstimatedTokens > 0
    && trace.optimizedEstimatedTokens < trace.rawEstimatedTokens
    && trace.reductionPercent > 0
    && trace.reductionPercent <= 100;
}

export function savingsState(trace: IntelligenceTrace): SavingsState {
  if (trace.fallback) return 'fallback';
  if (trace.mode === 'CLOUD_ONLY') return 'cloud-only';
  if (!hasValidReduction(trace)) return 'unavailable';
  if (trace.mode === 'LOCAL_ONLY') return 'local-only';
  return trace.mode === 'LOCAL_PREPROCESS_CLOUD' ? 'validated' : 'unavailable';
}

function eventErrorCode(trace: IntelligenceTrace, stage: string): string | undefined {
  const event = trace.events.find((item) => item.stage === stage && item.status === 'FAILED');
  const code = event?.details?.errorCode;
  return typeof code === 'string' && code ? code : undefined;
}

export function localFailureCode(trace: IntelligenceTrace): string | undefined {
  const local = eventErrorCode(trace, 'local_processing');
  if (local) return local;
  const fallback = trace.events.find((item) => item.stage === 'fallback')?.details?.reason;
  return typeof fallback === 'string' && fallback ? fallback : undefined;
}

export function cloudFailureCode(trace: IntelligenceTrace): string | undefined {
  const cloud = eventErrorCode(trace, 'cloud_execution');
  if (cloud) return cloud;
  if (trace.errorCode === 'CLOUD_EXECUTION_FAILED' || trace.errorCode === 'NATIVE_SESSION_NOT_READY') {
    return trace.errorCode;
  }
  return undefined;
}

// How much the LOCAL model compressed the candidate context. Deliberately NOT
// called "saved": the candidate context is assembled by PowerCodeDeck and is never
// sent in CLOUD_ONLY (which forwards the user's task byte-for-byte), so this
// difference is a compression ratio, not a cloud saving. Actual saving lives in
// cloudCostUsd / cloudInputTokens, measured on the closing result event.
export function compressedEstimatedTokens(trace: IntelligenceTrace): number | null {
  if (!hasValidReduction(trace) || trace.fallback || trace.mode === 'CLOUD_ONLY') return null;
  return trace.rawEstimatedTokens - trace.optimizedEstimatedTokens;
}

export type CloudSpend =
  | { known: true; costUsd: number; inputTokens: number; outputTokens: number; cacheReadTokens: number }
  | { known: false };

// A trace only carries cloud spend once its turn closed AND the driver reported
// usage. Codex reports none, so this returns {known:false} there — the caller must
// say so rather than print a zero.
export function cloudSpend(trace: IntelligenceTrace): CloudSpend {
  if (!trace.cloudUsageKnown) return { known: false };
  return {
    known: true,
    costUsd: trace.cloudCostUsd ?? 0,
    inputTokens: trace.cloudInputTokens ?? 0,
    outputTokens: trace.cloudOutputTokens ?? 0,
    cacheReadTokens: trace.cloudCacheReadTokens ?? 0,
  };
}

export function formatUSD(value: number): string {
  if (!Number.isFinite(value) || value < 0) return '—';
  if (value > 0 && value < 0.01) return '<$0.01';
  return `$${value.toFixed(2)}`;
}

export function aggregateSavings(traces: IntelligenceTrace[]): SavingsAggregate | null {
  const eligible = traces.filter((trace) =>
    trace.mode === 'LOCAL_PREPROCESS_CLOUD' && !trace.fallback && hasValidReduction(trace));
  // Note: this aggregate is COMPRESSION across runs, not saving. See
  // compressedEstimatedTokens for why the two are not the same number.
  if (eligible.length === 0) return null;

  const totalRaw = eligible.reduce((total, trace) => total + trace.rawEstimatedTokens, 0);
  const totalOptimized = eligible.reduce((total, trace) => total + trace.optimizedEstimatedTokens, 0);
  const totalCompressed = totalRaw - totalOptimized;
  return {
    runCount: eligible.length,
    totalRaw,
    totalOptimized,
    totalCompressed,
    overallReduction: totalRaw > 0 ? (totalCompressed * 100) / totalRaw : 0,
  };
}

function oneDecimal(value: number): string {
  return value.toFixed(1).replace(/\.0$/, '');
}

export function formatEstimatedTokens(value: number): string {
  if (!Number.isFinite(value) || value < 0) return '—';
  if (value >= 1_000_000) return `${oneDecimal(value / 1_000_000)}M`;
  if (value >= 1_000) return `${oneDecimal(value / 1_000)}k`;
  return Math.round(value).toLocaleString();
}

export function formatReduction(value: number): string {
  if (!Number.isFinite(value) || value < 0 || value > 100) return '—';
  return `${value.toFixed(1)}%`;
}

export function formatLatency(value: number): string {
  if (!Number.isFinite(value) || value <= 0) return '—';
  if (value < 1000) return `${Math.round(value)} ms`;
  return `${oneDecimal(value / 1000)} s`;
}

export function formatMode(mode: IntelligenceTrace['mode']): string {
  if (mode === 'LOCAL_PREPROCESS_CLOUD') return 'Hybrid';
  if (mode === 'LOCAL_ONLY') return 'Local Only';
  return 'Cloud Only';
}

function parseTimestamp(value: string): Date {
  const hasTimezone = /(?:Z|[+-]\d\d:\d\d)$/i.test(value);
  return new Date(hasTimezone ? value : `${value}Z`);
}

export function formatCreatedAt(value: string): string {
  const date = parseTimestamp(value);
  if (Number.isNaN(date.getTime())) return value;
  return date.toLocaleString(undefined, {
    year: 'numeric', month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit',
  });
}

export function formatRelativeTime(value: string, now = Date.now()): string {
  const date = parseTimestamp(value);
  if (Number.isNaN(date.getTime())) return 'Unknown time';
  const seconds = Math.max(0, Math.floor((now - date.getTime()) / 1000));
  if (seconds < 60) return 'just now';
  const minutes = Math.floor(seconds / 60);
  if (minutes < 60) return `${minutes}m ago`;
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return `${hours}h ago`;
  const days = Math.floor(hours / 24);
  return `${days}d ago`;
}
