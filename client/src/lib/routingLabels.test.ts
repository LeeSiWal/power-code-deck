// Framework-free assertions (type-checked by tsc like the other *.test.ts files).
import { CLASS_LABELS, compactTokens, modelUsageLine, autoPickNote, autoSwitchNote, toolSwitchNote, SKIP_LABELS, modelName, parseRoleLabel, profileName, reasonLabel, skipLabel, tokens } from './routingLabels';

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

// Every decider skip reason has its own sentence; single-candidate reads as an optimization.
equal(new Set(Object.values(SKIP_LABELS)).size, Object.keys(SKIP_LABELS).length, 'skip labels are distinct');
equal(skipLabel('single_distinct_candidate').includes('바로 실행'), true, 'single candidate is shown as direct execution');
equal(CLASS_LABELS['review_blocked'].includes('수동 리뷰'), true, 'missing reviewer is a manual-review state');

// Model/profile names read like product names, not ids.
equal(modelName('claude-opus-5-5'), 'Opus 5.5', 'opus name');
equal(modelName('claude-sonnet-5'), 'Sonnet 5', 'sonnet name');
equal(modelName('claude-haiku-4-5-20251001'), 'Haiku 4.5', 'dated haiku name');
equal(modelName('gpt-5.6-luna'), 'GPT-5.6 Luna', 'codex name');
equal(modelName('claude-opus-4-8[1m]'), 'Opus 4.8 · 1M', '1M context suffix');
equal(modelName(''), '기본 모델', 'empty model is the CLI default');
equal(profileName({ id: 'claude-sonnet5-low', adapter: 'claude', model: 'claude-sonnet-5', effort: 'low' }), 'Claude · Sonnet 5 · 추론 낮음', 'profile name');
equal(profileName(undefined, 'raw-id'), 'raw-id', 'unknown profile falls back to id');
const role = parseRoleLabel('unavailable: antigravity-default: billing_unknown,policy_review_required; codex-luna-medium: role_not_allowed');
equal(role.ok, false, 'unavailable role parsed');
equal(!role.ok && role.blocked.length, 1, 'role_not_allowed-only profiles are dropped');
const okRole = parseRoleLabel('claude-sonnet5-low (claude claude-sonnet-5; different provider from the executor)');
equal(okRole.ok && okRole.id, 'claude-sonnet5-low', 'available role id');
equal(profileName(undefined, 'gemini-default'), 'Gemini · 기본 모델', 'missing default profile is named by adapter');
equal(autoPickNote({ id: 'claude-opus55-high', adapter: 'claude', model: 'claude-opus-5-5', effort: 'high' }, 'HIGH', 'rule'), '자동 선택: Claude · Opus 5.5 · 추론 높음 (예상 난이도: 어려움)', 'auto pick note');
equal(autoSwitchNote({ model: 'claude-sonnet-5', effort: 'low' }, { model: 'claude-opus-5-5', effort: 'high' }, 'HIGH'), '모델 변경: Sonnet 5 · 추론 낮음 → Opus 5.5 · 추론 높음 (예상 난이도: 어려움)', 'auto switch note');
equal(toolSwitchNote({ adapter: 'claude' }, { adapter: 'codex', model: 'gpt-5.6-sol' }, 'HIGH', 4), '도구 전환: Claude → Codex · GPT-5.6 Sol (예상 난이도: 어려움) · 이전 대화 4턴 인계', 'tool switch note');
equal(compactTokens(950), '950', 'small tokens');
equal(compactTokens(12345), '12k', 'k tokens');
equal(compactTokens(1234), '1.2k', 'k tokens with decimal');
equal(modelUsageLine({ tool: 'codex', model: 'gpt-5.6-sol', effort: '', turns: 2, reported: 0, input: 0, output: 0, cacheCreation: 0, cacheRead: 0 }), 'Codex · GPT-5.6 Sol — 2턴 · 사용량 미보고', 'unreported is not zero');
equal(modelUsageLine({ tool: 'claude', model: 'claude-sonnet-5', effort: 'low', turns: 3, reported: 3, input: 100, output: 20, cacheCreation: 0, cacheRead: 900 }), 'Claude · Sonnet 5 · 추론 낮음 — 3턴 · 입력 1.0k · 출력 20 · 캐시 90%', 'usage line');
equal(modelName('oss:mac:mlx-community/Qwen3-30B-A3B-Instruct-2507-4bit'), '로컬 · Qwen3 30B A3B', 'local model name');
