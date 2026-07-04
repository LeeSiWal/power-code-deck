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

export interface AuthConfig {
  appName: string;
  version: string;
  authEnabled: boolean;
  authMethod: 'none' | 'pin' | 'password';
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

  // Sub-agents
  subAgents: Map<string, SubAgent[]>;
  setSubAgents: (agentId: string, subs: SubAgent[]) => void;
  addSubAgent: (agentId: string, sub: SubAgent) => void;
  updateSubAgent: (agentId: string, subId: string, updates: Partial<SubAgent>) => void;
  cleanupSubAgents: (agentId: string, maxAge: number) => void;

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

  soundEnabled: localStorage.getItem('soundEnabled') !== 'false',
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

  zoomedPanel: null,
  setZoomedPanel: (panel) => set({ zoomedPanel: panel }),

  commandPaletteOpen: false,
  setCommandPaletteOpen: (v) => set({ commandPaletteOpen: v }),
}));
