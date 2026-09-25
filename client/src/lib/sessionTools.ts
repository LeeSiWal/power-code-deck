// The coding tools a session can run. Starting a project no longer asks which
// one: it uses the last tool picked (Claude by default), and the chat's tool
// menu starts a new session with another one in the same folder.

export interface SessionTool {
  preset: string;
  name: string;
  command: string;
  driver: 'claude' | 'codex' | 'antigravity';
}

export const SESSION_TOOLS: SessionTool[] = [
  { preset: 'claude-code', name: 'Claude Code', command: 'claude', driver: 'claude' },
  { preset: 'codex-cli', name: 'Codex', command: 'codex', driver: 'codex' },
  { preset: 'antigravity', name: 'Antigravity', command: 'agy', driver: 'antigravity' },
];

const LAST_TOOL_KEY = 'pcd:lastTool';

export function defaultTool(): SessionTool {
  let saved: string | null = null;
  try { saved = localStorage.getItem(LAST_TOOL_KEY); } catch { /* storage may be blocked */ }
  return SESSION_TOOLS.find((t) => t.preset === saved) ?? SESSION_TOOLS[0];
}

export function rememberTool(preset: string): void {
  if (!SESSION_TOOLS.some((t) => t.preset === preset)) return;
  try { localStorage.setItem(LAST_TOOL_KEY, preset); } catch { /* ignore */ }
}

export function toolForDriver(driver: SessionTool['driver']): SessionTool {
  return SESSION_TOOLS.find((t) => t.driver === driver) ?? SESSION_TOOLS[0];
}

export function sessionName(tool: SessionTool, workingDir: string): string {
  return `${tool.name} - ${workingDir.split('/').filter(Boolean).pop() || workingDir}`;
}
