import { useCallback } from 'react';
import { useNavigate } from 'react-router-dom';
import { api } from '../lib/api';
import { defaultTool, prefersAuto, rememberTool, sessionName, type SessionTool } from '../lib/sessionTools';

export function useProjectLauncher() {
  const navigate = useNavigate();

  const launchAgent = useCallback(
    async (preset: string, name: string, workingDir: string, command: string, args: string[]) => {
      const agent = await api.createAgent({ preset, name, workingDir, command, args });
      rememberTool(preset);
      navigate(`/agents/${agent.id}`);
      return agent;
    },
    [navigate]
  );

  // Starts a session straight away — no tool picker. The launcher page stays
  // reachable for custom commands and as the fallback if creation fails.
  const launchProject = useCallback(
    async (projectPath: string, tool?: SessionTool) => {
      if (!tool && prefersAuto()) {
        navigate(`/start/${encodeURIComponent(projectPath)}`);
        return;
      }
      tool = tool ?? defaultTool();
      try {
        await launchAgent(tool.preset, sessionName(tool, projectPath), projectPath, tool.command, []);
      } catch {
        navigate(`/launch/${encodeURIComponent(projectPath)}`);
      }
    },
    [launchAgent, navigate]
  );

  return { launchProject, launchAgent };
}
