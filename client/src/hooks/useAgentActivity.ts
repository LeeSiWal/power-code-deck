import { useEffect } from 'react';
import { useAppStore, AgentActivity } from '../stores/appStore';
import { agentDeckWS } from '../lib/ws';

/**
 * Subscribes to the server's structured `agent:activity` snapshots for one agent and
 * mirrors the latest snapshot into the store. The server derives these from the Claude
 * Code transcript (real tool_use / sub-agent events), so this replaces the old
 * terminal-scraping heuristic that could not reliably detect agents.
 */
export function useAgentActivity(agentId: string | null) {
  const setActivity = useAppStore((s) => s.setActivity);
  const activity = useAppStore((s) => (agentId ? s.activity.get(agentId) : undefined));

  useEffect(() => {
    if (!agentId) return;
    const unsub = agentDeckWS.on('agent:activity', (payload: AgentActivity) => {
      if (payload?.agentId === agentId) setActivity(agentId, payload);
    });
    return () => {
      unsub();
    };
  }, [agentId, setActivity]);

  return { activity };
}
