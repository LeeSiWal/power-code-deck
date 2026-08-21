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

      // Control Room (v0.3.0) deltas — kept in the store globally (cheap) so /control
      // has live state whenever it mounts. Initial snapshots are fetched via REST by
      // the page itself.
      agentDeckWS.on('agent:summaries', (payload: any) => {
        useAppStore.getState().applySummaries(payload?.summaries || []);
      }),
      // Local Intelligence runs are server-side jobs: they keep going when the tab
      // closes, so their progress arrives here rather than as the answer to the
      // request that started them.
      agentDeckWS.on('intelligence:trace', (payload: any) => {
        if (payload?.trace?.id) useAppStore.getState().applyIntelligenceRun(payload);
      }),
      // (Re)connect: fill in whatever ran while this client was away. Without this a
      // run started before a reload is invisible until it happens to emit again —
      // and a run that finished in the meantime never would.
      agentDeckWS.on('open', () => {
        void api.intelligenceTraces(20)
          .then((traces) => useAppStore.getState().seedIntelligenceRuns(traces))
          .catch(() => { /* the traces panel surfaces its own load errors */ });
      }),
      agentDeckWS.on('native:approval', (payload: any) => {
        // Same event the native chat uses; here it feeds the global approval queue.
        //
        // 주의: payload.id → requestId 로 이름이 바뀐다. 그래서 단순 스프레드를
        // 쓸 수 없고, 필드를 명시적으로 나열해야 한다. 서버가 NativeApprovalPayload에
        // 필드를 추가할 때마다 이 목록도 함께 업데이트해야 한다 — 이 매핑이 누락된
        // 필드의 전형적인 은닉처다(canRemember·rememberTarget 이 6번의 리뷰를 통과한
        // 이유가 정확히 이것이다).
        useAppStore.getState().addApproval({
          requestId: payload.id,
          agentId: payload.agentId,
          toolName: payload.toolName,
          input: payload.input,
          askedAt: payload.askedAt,
          canRemember: payload.canRemember,
          rememberTarget: payload.rememberTarget,
        });
      }),
      agentDeckWS.on('approval:resolved', (payload: any) => {
        useAppStore.getState().removeApproval(payload.requestId);
      }),
      // A destroyed agent leaves no tile behind.
      agentDeckWS.on('agent:destroyed', ({ agentId }: any) => {
        useAppStore.getState().removeSummary(agentId);
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
