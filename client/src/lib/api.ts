import {
  apiUrl,
  clearTokens,
  currentEndpointId,
  getRefreshToken,
  getToken,
  setAccessToken,
  setTokens,
} from './endpoint';

// Thrown when an endpoint's credentials expired. It names WHICH endpoint so the
// router can decide: re-authenticate only if it is the one being viewed. Sending
// the whole app to /login because a background remote host expired was the old
// behaviour and is wrong once more than one server exists.
export class AuthExpiredError extends Error {
  constructor(readonly endpointId: string) {
    super('Session expired');
    this.name = 'AuthExpiredError';
  }
}

let authExpiredHandler: ((endpointId: string) => void) | null = null;

// Registered by the app shell. A callback rather than a store import keeps this
// module free of UI layering, and — more importantly — it fires even when the
// caller swallows the rejection. Plenty of call sites end in `.catch(() => {})`
// because their panel is supplemental; expiry still has to be noticed.
export function setAuthExpiredHandler(fn: ((endpointId: string) => void) | null) {
  authExpiredHandler = fn;
}

export interface LocalProvider {
  name: string;
  type: string;
  baseUrl: string;
  model: string;
  timeoutMs: number;
  enabled: boolean;
  updatedAt?: string;
}

export interface ProviderHealth {
  provider: string;
  reachable: boolean;
  apiHealthy: boolean;
  modelAvailable: boolean;
  generationTest: boolean;
  latencyMs: number;
  errorCode?: string;
  error?: string;
}

export type IntelligenceMode = 'CLOUD_ONLY' | 'LOCAL_PREPROCESS_CLOUD' | 'LOCAL_ONLY';

export interface IntelligenceTraceEvent {
  at: string;
  stage: string;
  status: string;
  details?: Record<string, unknown>;
}

export interface IntelligenceTrace {
  id: string;
  agentId?: string;
  mode: IntelligenceMode;
  status: string;
  provider?: string;
  model?: string;
  rawEstimatedTokens: number;
  optimizedEstimatedTokens: number;
  localTokens: number;
  latencyMs: number;
  reductionPercent: number;
  errorCode?: string;
  fallback: boolean;
  events: IntelligenceTraceEvent[];
  createdAt: string;
  // What the CLOUD actually spent on the dispatched turn — the only honest basis
  // for a savings claim. rawEstimatedTokens is the candidate context PowerCodeDeck
  // assembled, which CLOUD_ONLY never sends, so raw−optimized measures local
  // compression rather than saving.
  //
  // cloudUsageKnown is false when the driver reports no usage at all (Codex —
  // verified against codex-cli 0.146.0). The numbers below are then "not measured",
  // never "measured as zero", and must not be rendered as a value.
  cloudCostUsd?: number;
  cloudInputTokens?: number;
  cloudOutputTokens?: number;
  cloudCacheReadTokens?: number;
  cloudUsageKnown?: boolean;
}

// What POST /intelligence/run answers with: the run was accepted, here is the id
// to watch. Nothing else can be known yet — local inference takes 38-59 seconds
// when it succeeds, and the request no longer waits for it.
export interface IntelligenceStartResult {
  trace: IntelligenceTrace;
}

// What arrives on the `intelligence:trace` socket event. Progress updates carry
// only the trace; the update that ends a run also carries the pieces that are
// never stored server-side — the generated context pack and the files it was
// built from. LOCAL_ONLY output exists nowhere else, so this event is the only
// place it can be read.
export interface IntelligenceRunResult {
  trace: IntelligenceTrace;
  contextPack?: string;
  files?: string[];
  cloudDispatched?: boolean;
}

// Statuses a trace never leaves. Used to keep a late update from dragging a
// finished run back into "running".
const TERMINAL_TRACE_STATUS = new Set([
  'SUCCESS', 'FAILED', 'CLOUD_COMPLETED', 'CLOUD_COMPLETED_WITH_FALLBACK',
]);

export function isTraceTerminal(trace: IntelligenceTrace): boolean {
  return TERMINAL_TRACE_STATUS.has(trace.status);
}

export function isTraceRunning(trace: IntelligenceTrace): boolean {
  return !isTraceTerminal(trace);
}

// The LOCAL phase only: true while the local model still has the task. Once the
// run is dispatched to the cloud the trace is still open (it closes when the cloud
// turn ends), but the local wait — the part a composer blocks on — is over.
export function isLocalPhaseRunning(trace: IntelligenceTrace): boolean {
  return trace.status === 'RUNNING' || trace.status === 'STARTED';
}

export class ApiError extends Error {
  constructor(message: string, readonly status: number, readonly data?: unknown) {
    super(message);
    this.name = 'ApiError';
  }
}

async function refreshToken(): Promise<boolean> {
  const rt = getRefreshToken();
  if (!rt) return false;

  try {
    const res = await fetch(apiUrl('/auth/refresh'), {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ refreshToken: rt }),
    });
    if (!res.ok) return false;
    const data = await res.json();
    setAccessToken(data.accessToken);
    return true;
  } catch {
    return false;
  }
}

// apiRequest is the single place that knows how to reach an endpoint: which origin,
// which credentials, and what to do when they expire. apiFetch (JSON) sits on top.
// Nothing else in the client may assemble an /api URL or attach a Bearer header —
// that duplication is exactly what made the client same-origin-only.
export async function apiRequest(path: string, options: RequestInit = {}): Promise<Response> {
  const token = getToken();
  const headers: Record<string, string> = { ...(options.headers as Record<string, string>) };
  if (token) {
    headers['Authorization'] = `Bearer ${token}`;
  }

  let res = await fetch(apiUrl(path), { ...options, headers });

  // Auto-refresh on 401
  if (res.status === 401 && token) {
    const refreshed = await refreshToken();
    if (refreshed) {
      headers['Authorization'] = `Bearer ${getToken()}`;
      res = await fetch(apiUrl(path), { ...options, headers });
    } else {
      const endpointId = currentEndpointId();
      clearTokens(endpointId);
      authExpiredHandler?.(endpointId);
      throw new AuthExpiredError(endpointId);
    }
  }
  return res;
}

async function apiFetch<T = any>(path: string, options: RequestInit = {}): Promise<T> {
  const res = await apiRequest(path, {
    ...options,
    headers: { 'Content-Type': 'application/json', ...(options.headers as Record<string, string>) },
  });

  if (!res.ok) {
    const err = await res.json().catch(() => ({ error: res.statusText }));
    throw new ApiError(err.error || res.statusText, res.status, err);
  }

  if (res.status === 204) return undefined as T;
  return res.json();
}

export const api = {
  // Auth
  // Public health/config endpoint — no token required. Lets the client learn
  // whether auth is enabled (and which method) so it can skip the login page.
  getAuthConfig: () =>
    fetch(apiUrl('/auth/health')).then((r) => r.json()) as Promise<{
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
    // credentials are NOT sent cross-origin: this flow rides an httpOnly cookie
    // the server set when the QR was redeemed, so it only works when the page was
    // served by that same server. A cross-origin client (desktop shell, separately
    // hosted UI) gets a clean failure here rather than a silent one — header-based
    // redemption belongs with device credentials (spec §3), not here.
    fetch(apiUrl('/auth/handoff/exchange'), { method: 'POST', credentials: 'include' })
      .then(async (r) => {
        if (!r.ok) throw new Error('handoff exchange failed');
        return r.json() as Promise<{ accessToken: string; refreshToken: string; sessionId: string }>;
      })
      .then((data) => {
        setTokens(data.accessToken, data.refreshToken);
        return data;
      }),

  // No-auth mode: mint an anonymous access/refresh token so the WebSocket (which
  // always authenticates now) can connect without a login. The server only
  // issues this when auth is disabled and the caller's Origin is local.
  getAnonymousToken: () =>
    fetch(apiUrl('/auth/anonymous'), { method: 'POST' })
      .then(async (r) => {
        if (!r.ok) throw new Error('anonymous token failed');
        return r.json() as Promise<{ accessToken: string; refreshToken: string }>;
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
  // Pass the agent so its project's own .claude/commands are included — Claude Code
  // expands those exactly like the ones in $HOME. Built-ins (/help, /clear …) are
  // deliberately absent: they are interactive-mode only and answer "isn't available
  // in this environment" when sent over the stream protocol we drive.
  slashCommands: (agentId?: string) =>
    apiFetch<{ name: string; type: string; description?: string; scope?: string }[]>(
      `/agents/slash-commands${agentId ? `?agentId=${agentId}` : ''}`,
    ),
  listAgents: () => apiFetch<any[]>('/agents'),
  createAgent: (data: any) => apiFetch('/agents', { method: 'POST', body: JSON.stringify(data) }),
  getAgent: (id: string) => apiFetch(`/agents/${id}`),
  deleteAgent: (id: string) => apiFetch(`/agents/${id}`, { method: 'DELETE' }),
  restartAgent: (id: string) => apiFetch(`/agents/${id}/restart`, { method: 'POST' }),
  // Stop a session but KEEP the agent (reversible "정지"), unlike deleteAgent which
  // removes the record. Stops both the native session and the PTY.
  stopAgent: (id: string) => apiFetch(`/agents/${id}/stop`, { method: 'POST' }),

  // Past-session history (Claude Code transcripts for the agent's project).
  listSessions: (id: string) =>
    apiFetch<{ id: string; startedAt: string; lastAt: string; messageCount: number; preview: string }[]>(
      `/agents/${id}/sessions`,
    ),
  getSession: (id: string, sid: string) =>
    apiFetch<{ role: 'user' | 'assistant'; text: string; timestamp: string }[]>(`/agents/${id}/sessions/${sid}`),
  deleteSession: (id: string, sid: string) => apiFetch(`/agents/${id}/sessions/${sid}`, { method: 'DELETE' }),
  resumeSession: (id: string, sid: string) => apiFetch(`/agents/${id}/sessions/${sid}/resume`, { method: 'POST' }),
  newSession: (id: string) => apiFetch<{ id: string }>(`/agents/${id}/sessions/new`, { method: 'POST' }),

  // Plugins — the deck's own install/enable path. The CLI's `/plugin` is
  // interactive-only and answers "isn't available" over the stream protocol, so
  // these hit our server, which mirrors what the CLI installer does (fetch into the
  // plugin cache + flip enabledPlugins in settings.json).
  listPlugins: () =>
    apiFetch<
      {
        ref: string;
        name: string;
        marketplace: string;
        description: string;
        category?: string;
        installed: boolean;
        enabled: boolean;
        supported: boolean;
        skills?: number;
        commands?: number;
      }[]
    >('/plugins'),
  installPlugin: (ref: string) =>
    apiFetch<{ ref: string; installed: boolean; enabled: boolean }>('/plugins/install', {
      method: 'POST',
      body: JSON.stringify({ ref }),
    }),
  togglePlugin: (ref: string, enabled: boolean) =>
    apiFetch<{ ref: string; enabled: boolean }>('/plugins/toggle', {
      method: 'POST',
      body: JSON.stringify({ ref, enabled }),
    }),

  // Upload a file into the agent's project (.pcd-attachments/) so a chat message
  // can reference it and Claude can Read it. Multipart, so it bypasses apiFetch's
  // JSON content-type (the browser must set the multipart boundary itself).
  attachFile: async (id: string, file: File): Promise<{ path: string; name: string }> => {
    const fd = new FormData();
    fd.append('file', file);
    // apiRequest, not apiFetch: FormData must set its own multipart boundary, so
    // the JSON content-type has to stay off. Auth and origin still come from the
    // one place that owns them.
    const res = await apiRequest(`/agents/${id}/attach`, { method: 'POST', body: fd });
    if (!res.ok) throw new Error((await res.json().catch(() => ({}))).error || '업로드 실패');
    return res.json();
  },

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
  // Fetch a file's raw bytes as an object URL (authenticated) — for rendering
  // images / PDFs / video / audio the browser can display natively. Caller must
  // URL.revokeObjectURL() it when done.
  rawFileObjectURL: async (path: string, agentId?: string): Promise<string> => {
    const q = new URLSearchParams({ path, ...(agentId ? { agentId } : {}) });
    const res = await apiRequest(`/files/raw?${q.toString()}`);
    if (!res.ok) throw new Error('파일을 불러오지 못했습니다');
    return URL.createObjectURL(await res.blob());
  },

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

  // Approval rules ("항상 허용")
  // 규칙 목록은 페이지 로드 시 한 번, 삭제 후 낙관적으로 제거하므로 재로드 없이 동기화된다.
  listApprovalRules: () => apiFetch<any[]>('/approval-rules'),
  deleteApprovalRule: (id: number) => apiFetch(`/approval-rules/${id}`, { method: 'DELETE' }),

  // Local Intelligence POC. Provider settings are server-side runtime data;
  // changing a remote endpoint never requires rebuilding the app.
  listLocalProviders: () => apiFetch<LocalProvider[]>('/intelligence/providers'),
  putLocalProvider: (provider: LocalProvider) =>
    apiFetch<LocalProvider>(`/intelligence/providers/${encodeURIComponent(provider.name)}`, {
      method: 'PUT', body: JSON.stringify(provider),
    }),
  deleteLocalProvider: (name: string) =>
    apiFetch(`/intelligence/providers/${encodeURIComponent(name)}`, { method: 'DELETE' }),
  localProviderHealth: (name: string) =>
    apiFetch<ProviderHealth>(`/intelligence/providers/${encodeURIComponent(name)}/health`, { method: 'POST' }),
  runIntelligence: (request: {
    agentId: string; task: string; mode: IntelligenceMode;
    provider?: string; operation?: string;
  }) => apiFetch<IntelligenceStartResult>('/intelligence/run', { method: 'POST', body: JSON.stringify(request) }),
  // Stops a run someone no longer wants. 404 means it already finished — the
  // caller treats that as success, because the goal (not running) is met.
  cancelIntelligence: (id: string) =>
    apiFetch<void>(`/intelligence/traces/${encodeURIComponent(id)}/cancel`, { method: 'POST' }),
  intelligenceTraces: (limit = 50) =>
    apiFetch<IntelligenceTrace[]>(`/intelligence/traces?limit=${limit}`),
  intelligenceTrace: (id: string) =>
    apiFetch<IntelligenceTrace>(`/intelligence/traces/${encodeURIComponent(id)}`),

  // Notifications
  listNotifications: (agentId?: string) =>
    apiFetch(`/notifications${agentId ? `?agentId=${agentId}` : ''}`),
  clearNotifications: (agentId?: string) =>
    apiFetch(`/notifications/clear${agentId ? `?agentId=${agentId}` : ''}`, { method: 'POST' }),
  markNotificationsRead: (agentId: string) =>
    apiFetch(`/agents/${agentId}/notifications/read`, { method: 'POST' }),

  // Web Push — VAPID key + subscription lifecycle.
  pushVapidKey: () => apiFetch<{ enabled: boolean; publicKey: string }>('/push/vapid'),
  pushSubscribe: (sub: PushSubscriptionJSON & { deviceId?: string }) =>
    apiFetch('/push/subscribe', { method: 'POST', body: JSON.stringify(sub) }),
  pushUnsubscribe: (endpoint: string) =>
    apiFetch('/push/unsubscribe', { method: 'POST', body: JSON.stringify({ endpoint }) }),

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

  // Control Room (v0.3.0) — initial snapshots. Live deltas arrive over the WS
  // (agent:summaries, native:approval, approval:resolved).
  controlSummaries: () => apiFetch<any[]>('/control/summaries'),
  listApprovals: () => apiFetch<any[]>('/approvals'),

  getToken,
  clearTokens,
};
