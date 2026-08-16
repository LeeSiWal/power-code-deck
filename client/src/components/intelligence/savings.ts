import type { IntelligenceTrace } from '../../lib/api';

export type SavingsState = 'validated' | 'fallback' | 'cloud-only' | 'local-only' | 'unavailable';

export interface SavingsAggregate {
  runCount: number;
  totalRaw: number;
  totalOptimized: number;
  totalSaved: number;
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

export function savedEstimatedTokens(trace: IntelligenceTrace): number | null {
  if (!hasValidReduction(trace) || trace.fallback || trace.mode === 'CLOUD_ONLY') return null;
  return trace.rawEstimatedTokens - trace.optimizedEstimatedTokens;
}

export function aggregateSavings(traces: IntelligenceTrace[]): SavingsAggregate | null {
  const eligible = traces.filter((trace) =>
    trace.mode === 'LOCAL_PREPROCESS_CLOUD' && !trace.fallback && hasValidReduction(trace));
  if (eligible.length === 0) return null;

  const totalRaw = eligible.reduce((total, trace) => total + trace.rawEstimatedTokens, 0);
  const totalOptimized = eligible.reduce((total, trace) => total + trace.optimizedEstimatedTokens, 0);
  const totalSaved = totalRaw - totalOptimized;
  return {
    runCount: eligible.length,
    totalRaw,
    totalOptimized,
    totalSaved,
    overallReduction: totalRaw > 0 ? (totalSaved * 100) / totalRaw : 0,
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
