// The coding tools a session can run. Starting a project no longer asks which
// one: it uses the last tool picked (Claude by default, or 자동), and the chat's
// tool menu starts a new session with another one in the same folder.

export interface SessionTool {
  preset: string;
  name: string;
  command: string;
  driver: 'claude' | 'codex' | 'antigravity' | 'auto';
}

export const SESSION_TOOLS: SessionTool[] = [
  { preset: 'claude-code', name: 'Claude Code', command: 'claude', driver: 'claude' },
  { preset: 'codex-cli', name: 'Codex', command: 'codex', driver: 'codex' },
  { preset: 'antigravity', name: 'Antigravity', command: 'agy', driver: 'antigravity' },
];

const LAST_TOOL_KEY = 'pcd:lastTool';

// "자동": a session with no tool yet. Its first message routes it to a tool and
// model (server: POST /agents/{id}/route), and the same session continues there.
export const AUTO = 'auto';
export const AUTO_TOOL: SessionTool = { preset: AUTO, name: '자동', command: '', driver: 'auto' };

function savedTool(): string | null {
  try { return localStorage.getItem(LAST_TOOL_KEY); } catch { return null; /* storage may be blocked */ }
}

export function prefersAuto(): boolean {
  return savedTool() === AUTO;
}

export function defaultTool(): SessionTool {
  const saved = savedTool();
  return SESSION_TOOLS.find((t) => t.preset === saved) ?? SESSION_TOOLS[0];
}

export function rememberTool(preset: string): void {
  if (preset !== AUTO && !SESSION_TOOLS.some((t) => t.preset === preset)) return;
  try { localStorage.setItem(LAST_TOOL_KEY, preset); } catch { /* ignore */ }
}

export function toolForAdapter(adapter: string): SessionTool | undefined {
  return SESSION_TOOLS.find((t) => t.driver === adapter);
}

// An auto session's first message, handed from its pre-routing chat to the real
// chat so it is sent once the session is open, plus a line saying what was picked.
// afterSwitch: the session moved to another tool and keeps its history on screen,
// so the message is sent even though the chat is not empty.
export interface PendingStart { message: string; note: string; afterSwitch?: boolean }

export function setPendingStart(agentId: string, p: PendingStart): void {
  try { sessionStorage.setItem(`pcd:pendingStart:${agentId}`, JSON.stringify(p)); } catch { /* ignore */ }
}

export function takePendingStart(agentId: string): PendingStart | null {
  try {
    const key = `pcd:pendingStart:${agentId}`;
    const raw = sessionStorage.getItem(key);
    sessionStorage.removeItem(key);
    return raw ? JSON.parse(raw) as PendingStart : null;
  } catch { return null; }
}

export function toolForDriver(driver: SessionTool['driver']): SessionTool {
  if (driver === 'auto') return AUTO_TOOL;
  return SESSION_TOOLS.find((t) => t.driver === driver) ?? SESSION_TOOLS[0];
}

export function sessionName(tool: SessionTool, workingDir: string): string {
  return `${tool.name} - ${workingDir.split('/').filter(Boolean).pop() || workingDir}`;
}

// The project path rides in the query string, not the URL path: an encoded "/"
// (%2F) in the path is cleaned into a real "/" by the server's 301 redirect on a
// reload, which no longer matched the route and landed on the Control Room.
export function launchUrl(projectPath: string): string {
  return `/launch?path=${encodeURIComponent(projectPath)}`;
}
