import { useEffect, useRef } from 'react';
import { agentDeckWS } from '../lib/ws';
import { useAppStore } from '../stores/appStore';
import type { AgentNotification } from '../stores/appStore';

const PERMISSION_PATTERNS = [/Allow/i, /Y\/n/i, /Do you want/i, /\(y\/N\)/i];
const ERROR_PATTERNS = [/Error/i, /FAILED/i, /panic:/];
const COMPLETE_PATTERNS = [/\u2713/, /Done/i, /Success/i, /Complete/i];
const PROMPT_ENDINGS = ['> ', '$ ', '% ', '# ', '\u276F '];

function detectNotification(text: string): { reason: AgentNotification['reason']; message: string } | null {
  for (const p of PERMISSION_PATTERNS) {
    if (p.test(text)) {
      const line = text.split('\n').find(l => p.test(l)) || text.slice(0, 80);
      return { reason: 'permission_request', message: line.trim().slice(0, 120) };
    }
  }
  for (const p of ERROR_PATTERNS) {
    if (p.test(text)) {
      const line = text.split('\n').find(l => p.test(l)) || text.slice(0, 80);
      return { reason: 'error', message: line.trim().slice(0, 120) };
    }
  }
  for (const p of COMPLETE_PATTERNS) {
    if (p.test(text)) {
      return { reason: 'task_complete', message: 'Task completed' };
    }
  }
  const lastLine = text.trimEnd().split('\n').pop() || '';
  for (const ending of PROMPT_ENDINGS) {
    if (lastLine.endsWith(ending.trim())) {
      return { reason: 'waiting_input', message: 'Waiting for input' };
    }
  }
  return null;
}

export function useAgentNotification(agentId: string | null) {
  const lastNotifRef = useRef<string>('');
  const debounceRef = useRef<ReturnType<typeof setTimeout>>();

  useEffect(() => {
    if (!agentId) return;

    const unsub = agentDeckWS.on('terminal:output', (payload: any) => {
      if (payload.agentId !== agentId) return;

      clearTimeout(debounceRef.current);
      debounceRef.current = setTimeout(() => {
        const result = detectNotification(payload.data);
        if (!result) return;

        const key = `${result.reason}:${result.message}`;
        if (key === lastNotifRef.current) return;
        lastNotifRef.current = key;
        setTimeout(() => { lastNotifRef.current = ''; }, 10000);

        useAppStore.getState().addNotification({
          agentId,
          reason: result.reason,
          message: result.message,
          timestamp: new Date().toISOString(),
        });

        if (document.hidden && Notification.permission === 'granted') {
          const n = new Notification(`PowerCodeDeck: ${result.reason}`, {
            body: result.message,
            tag: `powercodedeck-${agentId}-${result.reason}`,
            requireInteraction: result.reason === 'permission_request',
          });
          n.onclick = () => { window.focus(); n.close(); };
        }
      }, 500);
    });

    return () => {
      unsub();
      clearTimeout(debounceRef.current);
    };
  }, [agentId]);
}
