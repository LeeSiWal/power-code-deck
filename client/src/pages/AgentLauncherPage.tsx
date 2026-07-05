import { useParams, useNavigate } from 'react-router-dom';
import { AgentLauncher } from '../components/agent/AgentLauncher';
import { useProjectLauncher } from '../hooks/useProjectLauncher';
import { IconBack } from '../components/icons';

export function AgentLauncherPage() {
  const { encodedPath } = useParams<{ encodedPath: string }>();
  const navigate = useNavigate();
  const { launchAgent } = useProjectLauncher();

  const workingDir = decodeURIComponent(encodedPath || '');

  const handleLaunch = async (preset: string, name: string, command: string, args: string[]) => {
    try {
      await launchAgent(preset, name, workingDir, command, args);
    } catch (err: any) {
      alert(`에이전트 실행 실패 / Failed to launch agent:\n${err?.message || err}`);
    }
  };

  return (
    <div className="flex flex-col h-full safe-top bg-deck-bg overflow-hidden">
      <header className="flex items-center gap-3 px-4 py-2 bg-deck-surface border-b border-deck-border">
        <button onClick={() => navigate('/')} className="p-1 rounded hover:bg-deck-border/30">
          <IconBack size={16} />
        </button>
        <span className="text-sm font-medium">Launch Agent</span>
      </header>
      <main className="flex-1 overflow-y-auto p-4 max-w-lg mx-auto w-full">
        <AgentLauncher workingDir={workingDir} onLaunch={handleLaunch} />
      </main>
    </div>
  );
}
