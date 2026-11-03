import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { agentDeckWS } from '../lib/ws';
import { foldEvents, isTurnActive, type StreamEvent } from '../lib/nativeEvents';

type NativeProvider = 'claude' | 'codex';
type PendingApproval = { id: string; toolName?: string };

const lastResultIndex = (events: StreamEvent[]) => {
  for (let i = events.length - 1; i >= 0; i--) {
    if (events[i].type === 'result') return i;
  }
  return -1;
};

// Spoken updates never recite code, terminal output, file contents, paths or
// unchecked natural-language claims. They describe only structured turn events.
export function summarizeNativeTurn(events: StreamEvent[], provider: NativeProvider): string {
  const end = lastResultIndex(events);
  if (end < 0) return '';
  let start = -1;
  for (let i = end - 1; i >= 0; i--) {
    if (events[i].type === 'result') { start = i; break; }
  }
  const turn = events.slice(start + 1, end + 1);
  const result = turn[turn.length - 1];
  const toolCalls = foldEvents(turn).filter((item) => item.kind === 'tool');
  const failed = toolCalls.filter((item) => item.kind === 'tool' && item.status === 'error').length;
  const denied = result.permission_denials?.length ?? 0;
  const name = provider === 'codex' ? 'Codex' : 'Claude Code';
  if (result.is_error) return `${name} 작업이 오류로 종료됐어. 자세한 내용은 코딩 화면에서 확인해 줘.`;
  if (denied > 0) return `${name} 작업에서 권한 요청 ${denied}건이 거부됐어. 코딩 화면을 확인해 줘.`;
  if (failed > 0) return `${name} 작업이 끝났지만 실패한 도구 실행이 ${failed}건 있어. 코딩 화면에서 결과를 확인해 줘.`;
  return toolCalls.length
    ? `${name} 작업이 끝났어. 도구 작업 ${toolCalls.length}건이 기록됐어. 변경 내용과 테스트 결과는 코딩 화면에서 확인해 줘.`
    : `${name} 답변이 완료됐어. 자세한 내용은 코딩 화면에서 확인할 수 있어.`;
}

/**
 * An explicit voice-to-code bridge for an EXISTING native session.
 * Ordinary voice chat does not use this hook's sendTask. The caller must
 * obtain explicit user confirmation for every dispatched coding instruction.
 * Approval requests remain owned by the existing coding UI.
 */
export function useVoiceCodingSession(agentId: string, enabled: boolean) {
  const [events, setEvents] = useState<StreamEvent[]>([]);
  const [provider, setProvider] = useState<NativeProvider | null>(null);
  const [connected, setConnected] = useState(false);
  const [submitting, setSubmitting] = useState(false);
  const [pendingApprovals, setPendingApprovals] = useState<PendingApproval[]>([]);
  const [error, setError] = useState('');
  const [lastAnnouncement, setLastAnnouncement] = useState('');
  const lastAnnouncedRef = useRef(-1);
  const bootstrappedRef = useRef(false);
  const eventsRef = useRef<StreamEvent[]>([]);

  useEffect(() => {
    if (!enabled || !agentId) return;
    let active = true;
    const open = () => {
      // The server resolves driver and cwd from its persisted agent identity.
      // No arbitrary command, path, model or provider may be supplied here.
      agentDeckWS.send('native:open', { agentId });
    };
    const offHistory = agentDeckWS.on('native:history', (payload: any) => {
      if (!active || payload.agentId !== agentId) return;
      const incoming = Array.isArray(payload.events) ? payload.events as StreamEvent[] : [];
      eventsRef.current = incoming;
      setEvents(incoming);
      lastAnnouncedRef.current = lastResultIndex(incoming);
      setSubmitting(false);
      bootstrappedRef.current = true;
      setConnected(true);
    });
    const offEvent = agentDeckWS.on('native:event', (payload: any) => {
      if (!active || payload.agentId !== agentId || !bootstrappedRef.current) return;
      const event = payload.event as StreamEvent;
      const next = [...eventsRef.current, event];
      eventsRef.current = next;
      setEvents(next);
      if (event.type === 'user') setSubmitting(false);
      if (event.type === 'result') {
        setSubmitting(false);
        const at = next.length - 1;
        if (at > lastAnnouncedRef.current) {
          lastAnnouncedRef.current = at;
          const kind = providerRef.current;
          if (kind) setLastAnnouncement(summarizeNativeTurn(next, kind));
        }
      }
    });
    const offApproval = agentDeckWS.on('native:approval', (payload: any) => {
      if (active && payload.agentId === agentId) {
        setPendingApprovals((previous) => previous.some((p) => p.id === payload.id)
          ? previous : [...previous, { id: payload.id, toolName: payload.toolName }]);
      }
    });
    const offState = agentDeckWS.on('native:state', (payload: any) => {
      if (active && payload.agentId === agentId) {
        setPendingApprovals(Array.isArray(payload.pending) ? payload.pending : []);
      }
    });
    const offError = agentDeckWS.on('native:error', (payload: any) => {
      if (active && payload.agentId === agentId) {
        setSubmitting(false);
        setError(payload.message || '에이전트 연결 오류');
      }
    });
    const offEvicted = agentDeckWS.on('native:evicted', (payload: any) => {
      if (active && payload.agentId === agentId) {
        setConnected(false);
        setSubmitting(false);
        setError('다른 기기에서 세션이 열렸어. 이 화면을 새로고침해 다시 연결해 줘.');
      }
    });
    const offClose = agentDeckWS.on('close', () => {
      if (active) {
        setConnected(false);
        setSubmitting(false);
        bootstrappedRef.current = false;
      }
    });
    const offOpen = agentDeckWS.on('open', () => {
      bootstrappedRef.current = false;
      setConnected(false);
      open();
    });
    open();
    return () => {
      active = false;
      offHistory(); offEvent(); offApproval(); offState(); offError(); offEvicted(); offClose(); offOpen();
    };
  }, [agentId, enabled]);

  const providerRef = useRef<NativeProvider | null>(null);
  const configureProvider = useCallback((kind: NativeProvider | null) => {
    providerRef.current = kind;
    setProvider(kind);
  }, []);

  const busy = useMemo(() => isTurnActive(events), [events]);
  const sendTask = useCallback((text: string) => {
    if (!enabled || !connected || !agentDeckWS.connected || !providerRef.current || !text.trim()) return false;
    if (pendingApprovals.length || submitting || isTurnActive(eventsRef.current)) return false;
    setError('');
    setLastAnnouncement('');
    setSubmitting(true);
    agentDeckWS.send('native:input', { agentId, text: text.trim() });
    return true;
  }, [agentId, enabled, connected, pendingApprovals.length, submitting]);

  const interrupt = useCallback(() => {
    if (!enabled || !connected || !busy) return;
    agentDeckWS.send('native:interrupt', { agentId });
  }, [agentId, enabled, connected, busy]);

  return {
    provider, configureProvider, connected, busy, submitting, pendingApprovals,
    error, lastAnnouncement, sendTask, interrupt,
  };
}
