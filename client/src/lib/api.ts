const API_BASE = '/api';

function getToken(): string | null {
  return localStorage.getItem('accessToken');
}

function setTokens(access: string, refresh: string) {
  localStorage.setItem('accessToken', access);
  localStorage.setItem('refreshToken', refresh);
}

function clearTokens() {
  localStorage.removeItem('accessToken');
  localStorage.removeItem('refreshToken');
}

async function refreshToken(): Promise<boolean> {
  const rt = localStorage.getItem('refreshToken');
  if (!rt) return false;

  try {
    const res = await fetch(`${API_BASE}/auth/refresh`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ refreshToken: rt }),
    });
    if (!res.ok) return false;
    const data = await res.json();
    localStorage.setItem('accessToken', data.accessToken);
    return true;
  } catch {
    return false;
  }
}

async function apiFetch<T = any>(path: string, options: RequestInit = {}): Promise<T> {
  const token = getToken();
  const headers: Record<string, string> = {
    'Content-Type': 'application/json',
    ...(options.headers as Record<string, string>),
  };
  if (token) {
    headers['Authorization'] = `Bearer ${token}`;
  }

  let res = await fetch(`${API_BASE}${path}`, { ...options, headers });

  // Auto-refresh on 401
  if (res.status === 401 && token) {
    const refreshed = await refreshToken();
    if (refreshed) {
      headers['Authorization'] = `Bearer ${getToken()}`;
      res = await fetch(`${API_BASE}${path}`, { ...options, headers });
    } else {
      clearTokens();
      window.location.href = '/login';
      throw new Error('Session expired');
    }
  }

  if (!res.ok) {
    const err = await res.json().catch(() => ({ error: res.statusText }));
    throw new Error(err.error || res.statusText);
  }

  if (res.status === 204) return undefined as T;
  return res.json();
}

export const api = {
  // Auth
  // Public health/config endpoint — no token required. Lets the client learn
  // whether auth is enabled (and which method) so it can skip the login page.
  getAuthConfig: () =>
    fetch(`${API_BASE}/auth/health`).then((r) => r.json()) as Promise<{
      status: string;
      appName: string;
      version: string;
      authEnabled: boolean;
      authMethod: 'none' | 'pin' | 'password';
      handoffEnabled?: boolean;
    }>,

  // Trade a session-scoped handoff cookie (set after redeeming a QR) for normal
  // access/refresh tokens. Only needed when PowerCodeDeck auth is enabled.
  handoffExchange: () =>
    fetch(`${API_BASE}/auth/handoff/exchange`, { method: 'POST', credentials: 'same-origin' })
      .then(async (r) => {
        if (!r.ok) throw new Error('handoff exchange failed');
        return r.json() as Promise<{ accessToken: string; refreshToken: string; sessionId: string }>;
      })
      .then((data) => {
        setTokens(data.accessToken, data.refreshToken);
        return data;
      }),

  // Submit a PIN or password; the server accepts the credential in `secret`.
  login: (secret: string) =>
    apiFetch<{ accessToken: string; refreshToken: string }>('/auth/login', {
      method: 'POST',
      body: JSON.stringify({ secret }),
    }).then((data) => {
      setTokens(data.accessToken, data.refreshToken);
      return data;
    }),

  // Agents
  slashCommands: () => apiFetch<{ name: string; type: string; description?: string }[]>('/agents/slash-commands'),
  listAgents: () => apiFetch<any[]>('/agents'),
  createAgent: (data: any) => apiFetch('/agents', { method: 'POST', body: JSON.stringify(data) }),
  getAgent: (id: string) => apiFetch(`/agents/${id}`),
  deleteAgent: (id: string) => apiFetch(`/agents/${id}`, { method: 'DELETE' }),
  restartAgent: (id: string) => apiFetch(`/agents/${id}/restart`, { method: 'POST' }),

  // Session Handoff — issue a one-time "Continue on Mobile" token + QR URLs.
  createHandoff: (id: string) =>
    apiFetch<{
      token: string;
      sessionId: string;
      expiresAt: string;
      ttlSeconds: number;
      publicUrl: string;
      localUrl: string;
      lanEnabled: boolean;
      authEnabled: boolean;
      warning: string;
    }>(`/agents/${id}/handoff`, { method: 'POST' }),

  // Files
  fileTree: (agentId: string, depth?: number) =>
    apiFetch(`/files/tree?agentId=${agentId}${depth ? `&depth=${depth}` : ''}`),
  readFile: (path: string, agentId?: string) =>
    apiFetch(`/files/read?path=${encodeURIComponent(path)}${agentId ? `&agentId=${agentId}` : ''}`),
  writeFile: (path: string, content: string, agentId?: string) =>
    apiFetch(`/files/write${agentId ? `?agentId=${agentId}` : ''}`, {
      method: 'PUT',
      body: JSON.stringify({ path, content }),
    }),
  mkdir: (path: string, agentId?: string) =>
    apiFetch(`/files/mkdir${agentId ? `?agentId=${agentId}` : ''}`, {
      method: 'POST',
      body: JSON.stringify({ path }),
    }),
  deleteFile: (path: string, agentId?: string) =>
    apiFetch(`/files/delete?path=${encodeURIComponent(path)}${agentId ? `&agentId=${agentId}` : ''}`, {
      method: 'DELETE',
    }),
  renameFile: (oldPath: string, newPath: string, agentId?: string) =>
    apiFetch(`/files/rename${agentId ? `?agentId=${agentId}` : ''}`, {
      method: 'PATCH',
      body: JSON.stringify({ oldPath, newPath }),
    }),
  fileStat: (path: string) => apiFetch(`/files/stat?path=${encodeURIComponent(path)}`),

  // Projects
  recentProjects: (limit?: number) => apiFetch(`/projects/recent${limit ? `?limit=${limit}` : ''}`),
  deleteRecentProject: (id: number) => apiFetch(`/projects/recent/${id}`, { method: 'DELETE' }),
  browseDir: (path?: string) => apiFetch(`/projects/browse${path ? `?path=${encodeURIComponent(path)}` : ''}`),
  detectProject: (path: string) => apiFetch(`/projects/detect?path=${encodeURIComponent(path)}`),
  searchProjects: (q: string) => apiFetch(`/projects/search?q=${encodeURIComponent(q)}`),
  createProject: (parentDir: string, name: string) =>
    apiFetch('/projects/create', { method: 'POST', body: JSON.stringify({ parentDir, name }) }),
  deleteProject: (path: string) =>
    apiFetch(`/projects/delete?path=${encodeURIComponent(path)}`, { method: 'DELETE' }),
  renameProject: (oldPath: string, newName: string) =>
    apiFetch('/projects/rename', { method: 'PATCH', body: JSON.stringify({ oldPath, newName }) }),

  // Logs
  searchLogs: (q?: string, limit?: number) => {
    const params = new URLSearchParams();
    if (q) params.set('q', q);
    if (limit) params.set('limit', String(limit));
    return apiFetch(`/logs?${params}`);
  },
  agentLogs: (agentId: string, limit?: number) =>
    apiFetch(`/logs/${agentId}${limit ? `?limit=${limit}` : ''}`),

  // Notifications
  listNotifications: (agentId?: string) =>
    apiFetch(`/notifications${agentId ? `?agentId=${agentId}` : ''}`),
  clearNotifications: (agentId?: string) =>
    apiFetch(`/notifications/clear${agentId ? `?agentId=${agentId}` : ''}`, { method: 'POST' }),
  markNotificationsRead: (agentId: string) =>
    apiFetch(`/agents/${agentId}/notifications/read`, { method: 'POST' }),

  // Agent meta
  getAgentMeta: (id: string) => apiFetch(`/agents/${id}/meta`),
  sendToAgent: (id: string, data: string) =>
    apiFetch(`/agents/${id}/send`, { method: 'POST', body: JSON.stringify({ data }) }),
  setAgentStatus: (id: string, key: string, text: string, color?: string) =>
    apiFetch(`/agents/${id}/meta/status`, { method: 'POST', body: JSON.stringify({ key, text, color }) }),
  setAgentProgress: (id: string, value: number, label?: string) =>
    apiFetch(`/agents/${id}/meta/progress`, { method: 'POST', body: JSON.stringify({ value, label }) }),
  addAgentLog: (id: string, level: string, message: string) =>
    apiFetch(`/agents/${id}/meta/log`, { method: 'POST', body: JSON.stringify({ level, message }) }),

  getToken,
  clearTokens,
};
