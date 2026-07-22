import { create } from 'zustand';

export interface Agent {
  id: string;
  preset: string;
  name: string;
  tmuxSession: string;
  workingDir: string;
  command: string;
  args: string[];
  status: string;
  colorHue: number;
  colorName: string;
  createdAt: string;
  updatedAt: string;
}

export interface SubAgent {
  id: string;
  name: string;
  type: string;
  status: 'running' | 'completed' | 'error';
  startedAt: number;
  completedAt?: number;
}

// Structured agent activity derived server-side from the Claude Code transcript
// (replaces the old terminal-scraping SubAgent heuristic).
export interface ActivityNode {
  id: string;
  kind: 'main' | 'subagent';
  label: string;
  status: 'working' | 'thinking' | 'idle' | 'done';
  currentTool?: string;
  currentTarget?: string;
  toolCount: number;
  startedAt: number;
  lastActivityAt: number;
  parent?: string;
}

export interface ActivityEvent {
  node: string;
  tool: string;
  target?: string;
  sidechain: boolean;
  startedAt: number;
  endedAt?: number;
}

export interface AgentActivity {
  agentId: string;
  nodes: ActivityNode[];
  recent: ActivityEvent[];
}

export interface AgentNotification {
  id?: number;
  agentId: string;
  reason: 'permission_request' | 'waiting_input' | 'error' | 'task_complete';
  message: string;
  timestamp: string;
}

export interface AgentMeta {
  gitBranch: string;
  gitDirty: boolean;
  gitAhead: number;
  listeningPorts: number[];
  customStatus?: { key: string; text: string; color?: string };
  progress?: { value: number; label?: string };
}

// --- Control Room (v0.3.0) ---
// Server-computed per-session projection. The client never assembles these; it
// applies snapshots (REST) and deltas (agent:summaries), guarding on revision so a
// late/out-of-order batch can't overwrite newer state.
export interface AttentionReason {
  kind: 'approval' | 'error' | 'stalled';
  since: number;
  count?: number;
}

export interface AgentAttention {
  primary: '' | 'approval' | 'error' | 'stalled';
  reasons: AttentionReason[];
}

export interface AgentSummary {
  agentId: string;
  revision: number;
  status: string;
  preset: string;
  name: string;
  colorHue: number;
  projectKey: string;
  projectLabel: string;
  attention: AgentAttention;
  lastTool?: string;
  lastTarget?: string;
  toolCount: number;
  lastActivityAt: number;
  unread: { total: number; completed: number; errors: number };
}

export interface PendingApproval {
  requestId: string;
  agentId: string;
  toolName: string;
  input?: any;
  askedAt: string;
}

export interface AuthConfig {
  appName: string;
  version: string;
  authEnabled: boolean;
  authMethod: 'none' | 'pin' | 'password';
  handoffEnabled: boolean;
}

interface AppState {
  // Auth
  isAuthenticated: boolean;
  setAuthenticated: (v: boolean) => void;
  authConfig: AuthConfig | null;
  authReady: boolean;
  setAuthConfig: (c: AuthConfig) => void;

  // Agents
  agents: Agent[];
  setAgents: (agents: Agent[]) => void;
  addAgent: (agent: Agent) => void;
  removeAgent: (id: string) => void;
  updateAgentStatus: (id: string, status: string) => void;

  // Current agent
  currentAgentId: string | null;
  setCurrentAgentId: (id: string | null) => void;

  // Sub-agents (legacy terminal-scraping heuristic — kept for non-Claude presets)
  subAgents: Map<string, SubAgent[]>;
  setSubAgents: (agentId: string, subs: SubAgent[]) => void;
  addSubAgent: (agentId: string, sub: SubAgent) => void;
  updateSubAgent: (agentId: string, subId: string, updates: Partial<SubAgent>) => void;
  cleanupSubAgents: (agentId: string, maxAge: number) => void;

  // Structured agent activity (transcript-based)
  activity: Map<string, AgentActivity>;
  setActivity: (agentId: string, a: AgentActivity) => void;

  // Settings
  soundEnabled: boolean;
  setSoundEnabled: (v: boolean) => void;
  characterTheme: string;
  setCharacterTheme: (v: string) => void;

  // UI state
  sidebarOpen: boolean;
  setSidebarOpen: (v: boolean) => void;

  // Notifications
  notifications: Map<string, AgentNotification[]>;
  addNotification: (n: AgentNotification) => void;
  clearNotifications: (agentId: string) => void;

  // Agent meta
  agentMeta: Map<string, AgentMeta>;
  setAgentMeta: (agentId: string, meta: AgentMeta) => void;
  updateAgentMetaStatus: (agentId: string, status: { key: string; text: string; color?: string }) => void;
  updateAgentMetaProgress: (agentId: string, progress: { value: number; label?: string }) => void;

  // Control Room (v0.3.0)
  summaries: Record<string, AgentSummary>;
  setSummaries: (list: AgentSummary[]) => void;      // REST snapshot (replace)
  applySummaries: (list: AgentSummary[]) => void;    // WS delta (revision-guarded)
  removeSummary: (agentId: string) => void;
  approvals: PendingApproval[];
  setApprovals: (list: PendingApproval[]) => void;   // REST snapshot (replace)
  addApproval: (a: PendingApproval) => void;
  removeApproval: (requestId: string) => void;

  // Panel zoom
  zoomedPanel: 'terminal' | 'files' | 'subagent' | 'browser' | null;
  setZoomedPanel: (panel: 'terminal' | 'files' | 'subagent' | 'browser' | null) => void;

  // Command palette
  commandPaletteOpen: boolean;
  setCommandPaletteOpen: (v: boolean) => void;
}

export const useAppStore = create<AppState>((set) => ({
  isAuthenticated: !!localStorage.getItem('accessToken'),
  setAuthenticated: (v) => set({ isAuthenticated: v }),
  authConfig: null,
  authReady: false,
  setAuthConfig: (c) =>
    set({
      authConfig: c,
      authReady: true,
      // No-auth mode: user is implicitly authenticated (skip login page).
      isAuthenticated: c.authEnabled ? !!localStorage.getItem('accessToken') : true,
    }),

  agents: [],
  setAgents: (agents) => set({ agents }),
  addAgent: (agent) => set((s) => ({ agents: [...s.agents, agent] })),
  removeAgent: (id) => set((s) => ({ agents: s.agents.filter((a) => a.id !== id) })),
  updateAgentStatus: (id, status) =>
    set((s) => ({
      agents: s.agents.map((a) => (a.id === id ? { ...a, status } : a)),
    })),

  currentAgentId: null,
  setCurrentAgentId: (id) => set({ currentAgentId: id }),

  subAgents: new Map(),
  setSubAgents: (agentId, subs) =>
    set((s) => {
      const m = new Map(s.subAgents);
      m.set(agentId, subs);
      return { subAgents: m };
    }),
  addSubAgent: (agentId, sub) =>
    set((s) => {
      const m = new Map(s.subAgents);
      const existing = m.get(agentId) || [];
      m.set(agentId, [...existing, sub]);
      return { subAgents: m };
    }),
  updateSubAgent: (agentId, subId, updates) =>
    set((s) => {
      const m = new Map(s.subAgents);
      const existing = m.get(agentId) || [];
      m.set(
        agentId,
        existing.map((sub) => (sub.id === subId ? { ...sub, ...updates } : sub))
      );
      return { subAgents: m };
    }),
  cleanupSubAgents: (agentId, maxAge) =>
    set((s) => {
      const m = new Map(s.subAgents);
      const existing = m.get(agentId) || [];
      const now = Date.now();
      const filtered = existing.filter((sub) => {
        if (sub.status === 'running') return true;
        return sub.completedAt ? (now - sub.completedAt) < maxAge : true;
      });
      m.set(agentId, filtered);
      return { subAgents: m };
    }),

  activity: new Map(),
  setActivity: (agentId, a) =>
    set((s) => {
      const m = new Map(s.activity);
      m.set(agentId, a);
      return { activity: m };
    }),

  soundEnabled: localStorage.getItem('soundEnabled') === 'true',
  setSoundEnabled: (v) => {
    localStorage.setItem('soundEnabled', String(v));
    set({ soundEnabled: v });
  },
  characterTheme: localStorage.getItem('characterTheme') || 'default',
  setCharacterTheme: (v) => {
    localStorage.setItem('characterTheme', v);
    set({ characterTheme: v });
  },

  sidebarOpen: false,
  setSidebarOpen: (v) => set({ sidebarOpen: v }),

  notifications: new Map(),
  addNotification: (n) =>
    set((s) => {
      const m = new Map(s.notifications);
      const existing = m.get(n.agentId) || [];
      m.set(n.agentId, [...existing.slice(-19), n]);
      return { notifications: m };
    }),
  clearNotifications: (agentId) =>
    set((s) => {
      const m = new Map(s.notifications);
      m.delete(agentId);
      return { notifications: m };
    }),

  agentMeta: new Map(),
  setAgentMeta: (agentId, meta) =>
    set((s) => {
      const m = new Map(s.agentMeta);
      const existing = m.get(agentId);
      m.set(agentId, { ...existing, ...meta } as AgentMeta);
      return { agentMeta: m };
    }),
  updateAgentMetaStatus: (agentId, status) =>
    set((s) => {
      const m = new Map(s.agentMeta);
      const existing = m.get(agentId) || { gitBranch: '', gitDirty: false, gitAhead: 0, listeningPorts: [] };
      m.set(agentId, { ...existing, customStatus: status });
      return { agentMeta: m };
    }),
  updateAgentMetaProgress: (agentId, progress) =>
    set((s) => {
      const m = new Map(s.agentMeta);
      const existing = m.get(agentId) || { gitBranch: '', gitDirty: false, gitAhead: 0, listeningPorts: [] };
      m.set(agentId, { ...existing, progress });
      return { agentMeta: m };
    }),

  summaries: {},
  setSummaries: (list) =>
    set(() => {
      const next: Record<string, AgentSummary> = {};
      for (const sum of list) next[sum.agentId] = sum;
      return { summaries: next };
    }),
  applySummaries: (list) =>
    set((s) => {
      const next = { ...s.summaries };
      for (const sum of list) {
        const prev = next[sum.agentId];
        // Revision guard: ignore a delta that isn't newer than what we already have,
        // so an out-of-order / duplicate batch can't roll a tile backwards.
        if (!prev || sum.revision >= prev.revision) next[sum.agentId] = sum;
      }
      return { summaries: next };
    }),
  removeSummary: (agentId) =>
    set((s) => {
      if (!(agentId in s.summaries)) return {} as any;
      const next = { ...s.summaries };
      delete next[agentId];
      return { summaries: next };
    }),

  approvals: [],
  setApprovals: (list) => set({ approvals: list }),
  addApproval: (a) =>
    set((s) => (s.approvals.some((x) => x.requestId === a.requestId) ? ({} as any) : { approvals: [...s.approvals, a] })),
  removeApproval: (requestId) =>
    set((s) => ({ approvals: s.approvals.filter((a) => a.requestId !== requestId) })),

  zoomedPanel: null,
  setZoomedPanel: (panel) => set({ zoomedPanel: panel }),

  commandPaletteOpen: false,
  setCommandPaletteOpen: (v) => set({ commandPaletteOpen: v }),
}));
