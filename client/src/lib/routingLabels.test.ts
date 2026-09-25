// Framework-free assertions (type-checked by tsc like the other *.test.ts files).
import { CLASS_LABELS, reasonLabel, tokens } from './routingLabels';

function equal(actual: unknown, expected: unknown, label: string) {
  if (actual !== expected) throw new Error(`${label}: expected ${String(expected)}, got ${String(actual)}`);
}

// Distinct gates must read differently.
const distinct = new Set(['not_installed', 'not_authenticated', 'policy_review_required', 'billing_unknown', 'rate_limited', 'consumer_auth_discontinued', 'quality_unverified', 'technical_unsupported', 'model_not_entitled'].map(reasonLabel));
equal(distinct.size, 9, 'reason labels are distinct');
equal(reasonLabel('something_new'), 'something_new', 'unknown codes pass through');
equal(tokens(null), '미보고', 'unreported usage is not zero');
equal(tokens(0), '0', 'reported zero stays zero');
equal(CLASS_LABELS['canceled'], '사용자 취소', 'cancel label');
