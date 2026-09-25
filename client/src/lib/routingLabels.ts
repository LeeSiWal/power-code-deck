// Human labels for routing reason codes and phases. Every server reason code
// maps to its own sentence so "not installed" never reads as "policy blocked".

export const REASON_LABELS: Record<string, string> = {
  not_installed: '설치되지 않음',
  wrong_binary: '에이전트 CLI가 아닌 실행 파일',
  not_authenticated: '로그인되지 않음(공식 CLI로 직접 로그인)',
  auth_expired: '인증 만료',
  consumer_auth_discontinued: '개인 Google 로그인 경로 종료(2026-06-18)',
  auth_method_mismatch: '인증 방식이 프로필과 다름',
  model_not_entitled: '이 계정에서 쓸 수 없는 모델',
  model_not_listed: 'CLI 모델 목록에 없음',
  effort_unsupported: '지원되지 않는 추론 강도',
  billing_unknown: '과금 경로 미확인',
  billing_not_allowed: '허용하지 않은 과금 경로',
  extra_usage_risk: '묻지 않고 추가 사용량/크레딧 과금 가능',
  inherited_billing_env: '서버 환경변수가 과금 경로를 바꿀 수 있음',
  not_implemented: '실행 어댑터 미구현',
  technical_unsupported: '필요한 프로토콜 기능 미지원',
  policy_review_required: '이용 조건 검토 대기',
  policy_blocked: '이용 조건상 차단',
  rate_limited: '일시적 사용 한도 초과',
  quota_exhausted: '쿼터 소진',
  health_degraded: '일시 장애',
  quality_unverified: '해당 작업군 품질 미검증',
  quality_failed: '해당 작업군 품질 평가 실패',
  not_allowlisted: '자동 선택 허용 목록에 없음',
  tier_not_mapped: '필요 등급에 매핑되지 않음',
  needs_tools: '도구 사용 불가',
  needs_edit: '파일 수정 불가',
  needs_enforced_read_only: '읽기 전용을 실제로 강제할 수 없음',
  context_too_small: '문맥 한도 부족',
  pinned_elsewhere: '다른 프로필/공급자로 고정됨',
  endpoint_unavailable: '로컬 서버 응답 없음',
  role_not_allowed: '이 역할에 허용되지 않은 프로필',
  decider_adapter_not_allowed: '판단용으로 허용하지 않은 공급자',
  decider_tools_not_disableable: '도구를 끌 수 없어 판단용 제외(읽기 전용 셸 허용 필요)',
};

// Skipping the decider is the normal, optimized path — not an error.
export const SKIP_LABELS: Record<string, string> = {
  strategy_not_commercial_llm: '판단 전략이 경량 LLM이 아님',
  mode_off: '라우팅 꺼짐',
  manual_or_pinned: '직접 선택·프로필 고정',
  no_candidates: '실행 가능한 후보 없음',
  single_distinct_candidate: '실제 후보가 하나뿐이라 바로 실행',
  rule_choice_clear: '규칙으로 명확히 선택 가능',
  fallback_predetermined: '정해진 대체 경로 사용',
  cached_verdict: '같은 질문의 유효한 판단 재사용',
  no_decider_available: '허용된 판단용 프로필 없음',
  local_only_no_commercial_decider: '로컬 전용: 상용 판단 호출 안 함',
  commercial_shadow_not_approved: '상용 Shadow 미승인(비용 발생 방지)',
  commercial_shadow_budget_exhausted: '상용 Shadow 예산 소진',
};

export function skipLabel(code: string): string {
  return SKIP_LABELS[code] ?? code;
}

export const STRATEGY_LABELS: Record<string, string> = {
  '': '설정 기본값',
  rules: '규칙',
  routellm: 'RouteLLM(로컬 BERT, 기록용)',
  commercial_llm: '경량 상용 LLM 판단',
};

export function reasonLabel(code: string): string {
  return REASON_LABELS[code] ?? code;
}

export const PHASE_LABELS: Record<string, string> = {
  idle: '대기',
  routing: '라우팅',
  handoff: '인계 준비',
  running: '실행 중',
  quiescing: '종료 확인 중',
  checkpointed: '체크포인트 저장',
  waiting_approval: '승인 대기',
  waiting_policy: '이용 조건 대기',
  waiting_billing: '과금 확인 대기',
  waiting_user: '사용자 선택 대기',
  blocked_environment: '환경 문제',
  reconcile: '작업 상태 확인 필요',
  failed: '실패',
  canceled: '취소됨',
  succeeded: '완료',
};

export function phaseLabel(p: string): string {
  return PHASE_LABELS[p] ?? p;
}

export const MODE_LABELS: Record<string, string> = {
  off: 'Off · 기존 실행',
  manual: 'Manual · 직접 선택',
  shadow: 'Shadow · 판단만 기록',
  auto: 'Auto · 자동 선택',
};

export const CLASS_LABELS: Record<string, string> = {
  '': '성공',
  quality: '품질 부족(검증 실패)',
  availability: '한도·장애',
  auth: '인증',
  entitlement: '모델 권한',
  environment: '환경 오류',
  permission: '권한·안전 거부',
  unknown_side_effect: '결과 불명(확인 필요)',
  canceled: '사용자 취소',
  review_blocked: '허용된 리뷰어 없음(수동 리뷰 필요)',
};

// Unknown usage stays "미보고", never 0.
export function tokens(n: number | null | undefined): string {
  return n === null || n === undefined ? '미보고' : n.toLocaleString();
}

// ---- Human names for profiles/models. Raw ids like "claude-sonnet5-low" or
// "gpt-5.6-luna" stay available as tooltips, never as the primary text.

const ADAPTER_NAMES: Record<string, string> = {
  claude: 'Claude',
  codex: 'Codex',
  antigravity: 'Antigravity',
  gemini: 'Gemini',
};

export function adapterName(id: string): string {
  return ADAPTER_NAMES[id] ?? (id ? id[0].toUpperCase() + id.slice(1) : id);
}

export const EFFORT_LABELS: Record<string, string> = {
  minimal: '최소',
  low: '낮음',
  medium: '중간',
  high: '높음',
  xhigh: '매우 높음',
  max: '최대',
};

// "claude-opus-5-5" → "Opus 5.5", "claude-haiku-4-5-20251001" → "Haiku 4.5",
// "gpt-5.6-luna" → "GPT-5.6 Luna". CLI-reported display names win when known.
export function modelName(model: string, displayNames?: Record<string, string>): string {
  if (!model) return '기본 모델';
  if (displayNames?.[model]) return displayNames[model];
  const ctx = /\[(\d+)m\]$/i.exec(model);
  if (ctx) return `${modelName(model.slice(0, ctx.index), displayNames)} · ${ctx[1]}M`;
  const gpt = /^gpt-([\d.]+)(?:-(.+))?$/i.exec(model);
  if (gpt) return `GPT-${gpt[1]}${gpt[2] ? ' ' + gpt[2].split('-').map(cap).join(' ') : ''}`;
  const parts = model.replace(/^(claude|gemini)-/i, '').split('-').filter((p) => !/^\d{8}$/.test(p));
  const out: string[] = [];
  for (const p of parts) {
    const prev = out[out.length - 1];
    if (/^\d+$/.test(p) && prev && /^\d+(\.\d+)*$/.test(prev)) out[out.length - 1] = `${prev}.${p}`;
    else out.push(/^\d/.test(p) ? p : cap(p));
  }
  return out.join(' ') || model;
}

function cap(s: string): string {
  return s ? s[0].toUpperCase() + s.slice(1) : s;
}

export interface NamedProfile { id: string; adapter: string; model?: string; effort?: string }

// "Claude · Sonnet 5 · 낮음". Unknown profiles fall back to their id.
export function profileName(p: NamedProfile | undefined, id = '', displayNames?: Record<string, string>): string {
  if (!p) {
    // Profiles missing from the snapshot (e.g. an uninstalled CLI's default).
    const m = /^([a-z]+)-default$/.exec(id);
    return m ? `${adapterName(m[1])} · 기본 모델` : id;
  }
  const effort = p.effort ? ` · ${EFFORT_LABELS[p.effort] ?? p.effort}` : '';
  return `${adapterName(p.adapter)} · ${modelName(p.model || '', displayNames)}${effort}`;
}

// Adapter observation values (installation/auth/billing/…) in plain words.
export const OBSERVATION_LABELS: Record<string, string> = {
  installed: '설치됨',
  authenticated: '로그인됨',
  subscription_included: '구독 포함',
  local: '로컬',
  supported: '지원',
  unsupported: '미지원',
  healthy: '정상',
  entitled: '사용 가능',
  allowed_for_scope: '허용',
  unknown: '확인 안 됨',
};

export function observationLabel(v: string): string {
  if (!v) return OBSERVATION_LABELS.unknown;
  return OBSERVATION_LABELS[v] ?? REASON_LABELS[v] ?? v;
}

// Server role labels are strings: "<id> (<adapter> <model>; same provider …)"
// or "unavailable: <id>: code,code; <id>: code". Parse them back into parts so
// the UI can name profiles and translate codes instead of printing the string.
export type RoleLabel =
  | { ok: true; id: string; sameProvider: boolean }
  | { ok: false; blocked: { id: string; reasons: string[] }[]; note: string };

export function parseRoleLabel(label: string): RoleLabel {
  if (!label.startsWith('unavailable')) {
    const id = label.split(' (')[0];
    return { ok: true, id, sameProvider: label.includes('same provider') };
  }
  const rest = label.replace(/^unavailable:\s*/, '');
  const blocked: { id: string; reasons: string[] }[] = [];
  const notes: string[] = [];
  for (const part of rest.split('; ')) {
    const m = /^([\w.-]+): ([\w,]+)$/.exec(part.trim());
    if (m) blocked.push({ id: m[1], reasons: m[2].split(',').filter((r) => r !== 'role_not_allowed') });
    else if (part.trim()) notes.push(part.trim());
  }
  return { ok: false, blocked: blocked.filter((b) => b.reasons.length > 0), note: notes.join('; ') };
}

export const TIER_LABELS: Record<string, string> = {
  VERY_EASY: '매우 쉬움',
  EASY: '쉬움',
  MEDIUM: '보통',
  HIGH: '어려움',
  ULTRA: '매우 어려움',
};

// One line for the chat: what "자동" picked and the rule behind it.
export function autoPickNote(p: NamedProfile, ruleTier: string, source: string): string {
  const why = source === 'only_candidate' ? '쓸 수 있는 모델이 하나뿐' : `예상 난이도: ${TIER_LABELS[ruleTier] ?? ruleTier}`;
  return `자동 선택: ${profileName(p)} (${why})`;
}

// Why an auto session started on Claude Code instead of a routed pick.
export function autoFallbackNote(code: string): string {
  const why: Record<string, string> = {
    routing_off: '라우팅 기능이 꺼져 있어',
    no_profile: '조건에 맞는 모델이 없어',
    routing_error: '자동 선택에 실패해',
    unsupported_adapter: '고른 도구를 이 채팅에서 쓸 수 없어',
  };
  return `${why[code] ?? '자동 선택을 쓸 수 없어'} Claude Code로 시작했습니다.`;
}

// One line for the chat when a later "자동" turn moved to another model.
export function autoSwitchNote(from: { model?: string; effort?: string } | undefined, to: { model?: string; effort?: string }, ruleTier: string): string {
  const name = (p: { model?: string; effort?: string }) => modelName(p.model || '') + (p.effort ? ` · ${EFFORT_LABELS[p.effort] ?? p.effort}` : '');
  const tier = TIER_LABELS[ruleTier] ?? ruleTier;
  return `모델 변경: ${from ? name(from) + ' → ' : ''}${name(to)}${tier ? ` (예상 난이도: ${tier})` : ''}`;
}
