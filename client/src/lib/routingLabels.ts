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
