import { useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { useAgents } from '../hooks/useAgents';
import { useDevice } from '../hooks/useDevice';
import { AgentGrid } from '../components/agent/AgentGrid';
import { AgentList } from '../components/agent/AgentList';
import { CreateAgentSheet } from '../components/agent/CreateAgentSheet';
import { BottomNav } from '../components/layout/BottomNav';
import { IconPlus, IconSettings } from '../components/icons';
import { Link } from 'react-router-dom';

export function DashboardPage() {
  const { agents, createAgent, deleteAgent } = useAgents();
  const { isMobile } = useDevice();
  const navigate = useNavigate();
  const [showCreate, setShowCreate] = useState(false);

  const runningAgents = agents.filter(a => a.status === 'running');

  const handleCreate = async (data: { preset: string; name: string; workingDir: string; command?: string; args?: string[] }) => {
    try {
      const PRESET_COMMANDS: Record<string, { command: string; args: string[] }> = {
        'claude-code': { command: 'claude', args: [] },
        'codex-cli': { command: 'codex', args: [] },
      };
      const presetConfig = PRESET_COMMANDS[data.preset];
      const command = data.command || presetConfig?.command || data.preset;
      const args = [...(data.args || []), ...(presetConfig?.args || [])];

      const agent = await createAgent({
        preset: data.preset,
        name: data.name,
        workingDir: data.workingDir,
        command,
        args,
      });
      if (agent?.id) {
        navigate(`/agents/${agent.id}`);
      }
    } catch (err: any) {
      console.error('Failed to create agent:', err);
      alert(`에이전트 실행 실패 / Failed to launch agent:\n${err?.message || err}`);
    }
  };

  return (
    <div className="flex flex-col h-full safe-top bg-deck-bg overflow-hidden">
      <header className="flex items-center justify-between px-4 py-2 bg-deck-surface border-b border-deck-border shrink-0">
        <div>
          <span className="text-sm font-medium">Dashboard</span>
          <span className="text-xs text-deck-text-dim ml-2">
            {runningAgents.length} running
          </span>
        </div>
        <div className="flex items-center gap-2">
          <Link
            to="/?new=1"
            className="flex items-center gap-1.5 px-3 py-2 rounded-lg bg-deck-accent text-white text-xs font-medium active:opacity-80"
          >
            <IconPlus size={14} />
            <span>프로젝트 추가</span>
          </Link>
          {/* Desktop/iPad have no BottomNav (md:hidden), so this is their path to
              settings — 알림 토글 included. Shown on md+ to avoid doubling the
              mobile BottomNav's Settings entry. */}
          <Link
            to="/settings"
            className="hidden md:inline-flex items-center p-2 rounded-lg text-deck-text-dim hover:bg-deck-border/30"
            title="설정"
          >
            <IconSettings size={16} />
          </Link>
        </div>
      </header>

      <main className="flex-1 overflow-y-auto min-h-0">
        {isMobile ? (
          <AgentList agents={agents} onDestroy={deleteAgent} />
        ) : (
          <AgentGrid agents={agents} onDestroy={deleteAgent} />
        )}
      </main>

      <BottomNav />

      <CreateAgentSheet
        open={showCreate}
        onClose={() => setShowCreate(false)}
        onCreate={handleCreate}
      />
    </div>
  );
}
