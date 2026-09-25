import { useCallback } from 'react';
import { useNavigate } from 'react-router-dom';
import { api } from '../lib/api';
import { AUTO_TOOL, defaultTool, launchUrl, prefersAuto, rememberTool, sessionName, type SessionTool } from '../lib/sessionTools';

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
      tool = tool ?? (prefersAuto() ? AUTO_TOOL : defaultTool());
      try {
        await launchAgent(tool.preset, sessionName(tool, projectPath), projectPath, tool.command, []);
      } catch {
        navigate(launchUrl(projectPath));
      }
    },
    [launchAgent, navigate]
  );

  return { launchProject, launchAgent };
}
