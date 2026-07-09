import { useEffect } from 'react';
import { agentDeckWS } from '../lib/ws';
import { api } from '../lib/api';
import { useAppStore } from '../stores/appStore';

export function useWebSocket() {
  const { setAgents, addAgent, removeAgent, updateAgentStatus, isAuthenticated } = useAppStore();

  useEffect(() => {
    if (!isAuthenticated) {
      agentDeckWS.disconnect();
      return;
    }

    // The WebSocket always authenticates now — even in no-auth mode, where the
    // token is an anonymous one minted at boot. That token may still be minting
    // on first paint, so connect as soon as it appears rather than with an empty
    // token (which the server would reject).
    let cancelled = false;
    let poll: number | undefined;
    const connectWhenReady = () => {
      const token = api.getToken();
      if (token) {
        agentDeckWS.connect(token);
        return true;
      }
      return false;
    };
    if (!connectWhenReady()) {
      poll = window.setInterval(() => {
        if (cancelled || connectWhenReady()) window.clearInterval(poll);
      }, 300);
    }

    const unsubs = [
      agentDeckWS.on('agent:list', (agents) => setAgents(agents)),
      agentDeckWS.on('agent:created', (agent) => addAgent(agent)),
      agentDeckWS.on('agent:destroyed', ({ agentId }) => removeAgent(agentId)),
      agentDeckWS.on('agent:status', ({ agentId, status }) => updateAgentStatus(agentId, status)),
      // New meta/notification events
      agentDeckWS.on('agent:meta', (payload: any) => {
        useAppStore.getState().setAgentMeta(payload.agentId, {
          gitBranch: payload.gitBranch || '',
          gitDirty: payload.gitDirty || false,
          gitAhead: payload.gitAhead || 0,
          listeningPorts: payload.listeningPorts || [],
        });
      }),
      agentDeckWS.on('agent:meta:status', (payload: any) => {
        useAppStore.getState().updateAgentMetaStatus(payload.agentId, {
          key: payload.key, text: payload.text, color: payload.color,
        });
      }),
      agentDeckWS.on('agent:meta:progress', (payload: any) => {
        useAppStore.getState().updateAgentMetaProgress(payload.agentId, {
          value: payload.value, label: payload.label,
        });
      }),
      agentDeckWS.on('agent:notification', (payload: any) => {
        useAppStore.getState().addNotification({
          agentId: payload.agentId, reason: payload.reason,
          message: payload.message, timestamp: payload.timestamp,
        });
      }),
      agentDeckWS.on('agent:notification:clear', (payload: any) => {
        useAppStore.getState().clearNotifications(payload.agentId);
      }),
    ];

    return () => {
      cancelled = true;
      if (poll) window.clearInterval(poll);
      unsubs.forEach((fn) => fn());
    };
  }, [isAuthenticated]);

  return { ws: agentDeckWS };
}
