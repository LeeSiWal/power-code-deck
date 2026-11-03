// Read-only answers to voice questions grounded in the existing Control Room
// status endpoint. This is NOT a generative conversation engine and never
// invokes native:input or a coding tool.
export interface VoiceAgentStatus {
  agentId?: string;
  name?: string;
  status?: string;
  preset?: string;
  todoTotal?: number;
  todoCompleted?: number;
  todoActive?: string;
  lastTool?: string;
  lastTarget?: string;
  attention?: { primary?: string };
}

export interface VoiceStatusAnswer {
  text: string;
  needsCodingScreen: boolean;
}

const safeText = (value: unknown, limit = 100) =>
  typeof value === 'string' ? value.replace(/[\\\r\n]+/g, ' ').trim().slice(0, limit) : '';

export function answerVoiceStatus(question: string, status: VoiceAgentStatus | undefined): VoiceStatusAnswer {
  if (!status) return { text: '아직 이 세션의 작업 상태를 가져오지 못했어. 연결 상태를 확인해 줘.', needsCodingScreen: false };
  const q = question.replace(/\s+/g, '').toLowerCase();
  const project = safeText(status.name, 40) || '현재 에이전트';
  const progress = Math.max(0, Number(status.todoCompleted) || 0);
  const total = Math.max(0, Number(status.todoTotal) || 0);
  const current = safeText(status.todoActive, 110);
  const state = safeText(status.status, 40).toLowerCase();
  const attention = safeText(status.attention?.primary, 40);
  const running = state === 'running' || state === 'active';
  if (/승인|허가|확인해줘|막혔|대기/.test(q)) {
    if (attention === 'approval' || attention === 'needs_approval') {
      return { text: `${project}에 승인을 기다리는 요청이 있어. 내용은 코딩 화면에서 확인하고 승인해 줘.`, needsCodingScreen: true };
    }
    return { text: '현재 작업 요약에서 승인 대기 여부를 확정할 수 없어. 코딩 화면의 승인 요청을 확인해 줘.', needsCodingScreen: true };
  }
  if (/완료|끝났|할일|진행률|진척/.test(q)) {
    const summary = total > 0 ? `등록된 작업 ${total}개 중 ${Math.min(progress,total)}개가 완료됐어.` : '등록된 할 일의 완료 개수는 확인되지 않아.';
    return { text: `${summary} ${current ? `현재 항목은 ${current}야.` : ''}`.trim(), needsCodingScreen: false };
  }
  if (/무엇|뭐|작업|상태|어디까지|진행|하고있/.test(q)) {
    const phase = running ? '현재 실행 중으로 표시돼.' : `현재 상태는 ${state || '확인되지 않음'}이야.`;
    return { text: `${project}은 ${phase} ${current ? `진행 중인 항목은 ${current}야.` : '구체적인 수정 내용은 코딩 화면에서 확인할 수 있어.'}`, needsCodingScreen: false };
  }
  if (/코드|수정|테스트|빌드|파일|오류|에러/.test(q)) {
    return { text: '코드와 로그는 음성으로 읽지 않을게. 이 요청을 코딩 작업으로 옮기면 전체 내용을 화면에서 확인할 수 있어.', needsCodingScreen: true };
  }
  return { text: '지금은 작업 상태, 완료된 항목, 승인 대기를 물어볼 수 있어. 일반적인 AI 음성 대화는 아직 연결되지 않았어.', needsCodingScreen: false };
}
