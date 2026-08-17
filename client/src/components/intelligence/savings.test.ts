import type { IntelligenceTrace } from '../../lib/api';
import {
  aggregateSavings,
  cloudFailureCode,
  formatEstimatedTokens,
  formatReduction,
  hasValidReduction,
  localFailureCode,
  savedEstimatedTokens,
  savingsState,
} from './savings';

function trace(overrides: Partial<IntelligenceTrace> = {}): IntelligenceTrace {
  return {
    id: 'PCD-TEST', mode: 'LOCAL_PREPROCESS_CLOUD', status: 'CLOUD_COMPLETED',
    rawEstimatedTokens: 28_322, optimizedEstimatedTokens: 4_318, localTokens: 900,
    latencyMs: 5_200, reductionPercent: 84.8, fallback: false, events: [],
    createdAt: '2026-08-16T00:00:00Z', ...overrides,
  };
}

function equal(actual: unknown, expected: unknown, label: string) {
  if (actual !== expected) throw new Error(`${label}: expected ${String(expected)}, got ${String(actual)}`);
}

equal(savedEstimatedTokens(trace()), 24_004, 'saved tokens');
equal(hasValidReduction(trace()), true, 'valid reduction');
equal(hasValidReduction(trace({ rawEstimatedTokens: 0 })), false, 'zero raw is invalid');
equal(hasValidReduction(trace({ optimizedEstimatedTokens: 0 })), false, 'zero optimized is invalid');
equal(hasValidReduction(trace({ optimizedEstimatedTokens: 28_322 })), false, 'non-reduction is invalid');
equal(savingsState(trace({ fallback: true, errorCode: 'LOCAL_PROVIDER_UNREACHABLE' })), 'fallback', 'fallback state');
equal(savedEstimatedTokens(trace({ fallback: true })), null, 'fallback hides savings');
equal(savingsState(trace({ mode: 'CLOUD_ONLY', rawEstimatedTokens: 0, optimizedEstimatedTokens: 0, reductionPercent: 0 })), 'cloud-only', 'cloud-only state');
equal(savingsState(trace({ mode: 'LOCAL_ONLY' })), 'local-only', 'local-only state');
equal(formatEstimatedTokens(832), '832', 'small token format');
equal(formatEstimatedTokens(8_400), '8.4k', 'thousand token format');
equal(formatEstimatedTokens(1_200_000), '1.2M', 'million token format');
equal(formatReduction(84.76), '84.8%', 'reduction format');

const doubleFailure = trace({
  status: 'FAILED', fallback: true, errorCode: 'CLOUD_EXECUTION_FAILED',
  events: [
    { at: '2026-08-16T00:00:01Z', stage: 'local_processing', status: 'FAILED', details: { errorCode: 'CONTEXT_BUILD_FAILED' } },
    { at: '2026-08-16T00:00:02Z', stage: 'cloud_execution', status: 'FAILED', details: { errorCode: 'CLOUD_EXECUTION_FAILED' } },
  ],
});
equal(localFailureCode(doubleFailure), 'CONTEXT_BUILD_FAILED', 'local failure remains visible');
equal(cloudFailureCode(doubleFailure), 'CLOUD_EXECUTION_FAILED', 'cloud fallback failure remains visible');
equal(cloudFailureCode(trace({
  status: 'FAILED', fallback: false, errorCode: 'NATIVE_SESSION_NOT_READY', events: [],
})), 'NATIVE_SESSION_NOT_READY', 'native readiness is a cloud-side failure');

const aggregate = aggregateSavings([
  trace(),
  trace({ id: 'fallback', fallback: true }),
  trace({ id: 'cloud', mode: 'CLOUD_ONLY' }),
]);
equal(aggregate?.runCount, 1, 'aggregate only includes validated hybrid');
equal(aggregate?.totalSaved, 24_004, 'aggregate saved tokens');

console.log('Savings calculation tests passed.');
