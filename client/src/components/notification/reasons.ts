// Shared vocabulary for notification kinds.
//
// The toaster and the notification centre must name and colour the same event
// identically — a "작업 완료" toast that turns into "완료됨" in the list reads like
// two different events. Keeping the mapping in one module is what stops that drift.
//
// The keys are the server's `reason` strings (services/notification.go); anything
// unrecognised falls back to a neutral label rather than rendering blank.

export const NOTIFICATION_LABEL: Record<string, string> = {
  permission_request: '승인 필요',
  task_complete: '작업 완료',
  error: '오류',
  stalled: '무응답',
  waiting_input: '입력 대기',
};

/** Left accent colour per reason (used as a thick coloured left edge). */
export const NOTIFICATION_ACCENT: Record<string, string> = {
  permission_request: 'border-l-deck-accent',
  task_complete: 'border-l-deck-success',
  error: 'border-l-deck-danger',
  stalled: 'border-l-deck-warning',
  waiting_input: 'border-l-deck-accent',
};

export function notificationLabel(reason: string): string {
  return NOTIFICATION_LABEL[reason] || '알림';
}

export function notificationAccent(reason: string): string {
  return NOTIFICATION_ACCENT[reason] || 'border-l-deck-border';
}
