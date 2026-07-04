import { useEffect, useRef } from 'react';
import { agentDeckWS } from '../lib/ws';
import { api } from '../lib/api';
import { useAppStore } from '../stores/appStore';

export function useWebSocket() {
  const connected = useRef(false);
  const { setAgents, addAgent, removeAgent, updateAgentStatus, isAuthenticated, authConfig } = useAppStore();
  const authEnabled = authConfig?.authEnabled ?? true;

  useEffect(() => {
    if (isAuthenticated) return;
    agentDeckWS.disconnect();
    connected.current = false;
  }, [isAuthenticated]);

  useEffect(() => {
    if (!isAuthenticated) return;

    // In no-auth mode there is no token; the server accepts the WS anyway.
    const token = api.getToken();
    if ((authEnabled && !token) || connected.current) return;

    agentDeckWS.connect(token || '');
    connected.current = true;

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
      unsubs.forEach((fn) => fn());
    };
  }, [isAuthenticated, authEnabled]);

  return { ws: agentDeckWS };
}
