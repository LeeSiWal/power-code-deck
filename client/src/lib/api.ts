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

export class ApiError extends Error {
  constructor(message: string, readonly status: number, readonly data?: unknown) {
    super(message);
    this.name = 'ApiError';
  }
}

// ---- Model routing (v2). Mirrors server/internal/routing JSON. ----
export type RoutingMode = 'off' | 'manual' | 'shadow' | 'auto';
export interface RoutingObservation { value: string; source?: string; detail?: string; scope?: string; checkedAt?: string }
export interface RoutingModelInfo { id: string; displayName?: string; efforts?: string[]; vendor?: string; isDefault?: boolean }
export interface RoutingAdapterStatus {
  adapterId: string; executable?: string; version?: string; authMethod?: string;
  installation: RoutingObservation; auth: RoutingObservation; entitlement: RoutingObservation; billing: RoutingObservation;
  technical: RoutingObservation; policy: RoutingObservation; health: RoutingObservation;
  models?: RoutingModelInfo[]; inheritedEnv?: string[]; validatedVersion?: string; needsRevalidation?: boolean;
}
export interface RoutingExclusion { reason: string; detail?: string }
export interface RoutingProfile {
  id: string; adapter: string; model?: string; modelVendor?: string; effort?: string; accountRef?: string; authMethod?: string;
  billing?: string; extraUsageRisk?: boolean; quotaBucket?: string; tiers?: string[]; allow: boolean; generated?: boolean;
  quality?: Record<string, { status: string; evidence?: string }>;
}
export interface RoutingCapabilities { tools: boolean; editFiles: boolean; readOnlyEnforced: boolean; multiTurn: boolean; nativeResume: boolean; approvalBroker: boolean; interrupt: boolean; forceModel: boolean; forceEffort: boolean; observedModel: string; subcallsVisible: boolean; contextTokens?: number }
export interface RoutingProfileView { profile: RoutingProfile; manualExcluded: RoutingExclusion[] | null; manualWarnings: RoutingExclusion[] | null; automaticExcluded: RoutingExclusion[] | null; capabilities: RoutingCapabilities; policy: Record<string, RoutingObservation> }
export interface RoutingSnapshot {
  mode: RoutingMode; configError?: string; fingerprint: string;
  spend: Record<string, boolean | string>; switching: { maxSwitchesPerRun: number; maxAttemptsPerRun: number; maxRunMinutes: number; autoEscalate: boolean; stickiness: number };
  adapters: RoutingAdapterStatus[]; profiles: RoutingProfileView[] | null; tiers: Record<string, string[]>; routellm: Record<string, unknown>;
  policy: { reviewedAt: string; sources: Record<string, string> };
  decider: DeciderView;
  tierJudge?: { endpointRef: string; model?: string; timeoutMs?: number };
}
export type RoutingStrategy = 'rules' | 'routellm' | 'commercial_llm';
export interface DeciderCall { profileId: string; purpose: string; latencyMs: number; usage?: RoutingUsage; observedModel?: string; errorClass?: string; error?: string; valid: boolean }
export interface DeciderRecord { consulted: boolean; skip?: string; requestId?: string; calls?: DeciderCall[]; verdict?: { action: string; profile_id?: string; reason_code: string; evidence_refs?: string[]; needs?: string[] }; applied: boolean; note?: string }
export interface RoleUsage { role: string; calls: number; reported: number; inputTokens: number; outputTokens: number; cacheReadTokens: number; complete: boolean }
export interface DeciderStats { calls: number; valid: number; p50Ms: number; p95Ms: number; meanTokens: number }
export interface DeciderView {
  strategy: RoutingStrategy; limits: { timeoutSeconds: number; maxOutputBytes: number; maxFormatRetries: number; maxContextRounds: number; shadowMaxCallsPerDay: number; allowReadOnlyShell?: boolean; profile?: string; fallback?: string };
  ordered: string[] | null; candidates: { profile: RoutingProfile; excluded?: RoutingExclusion[] }[] | null; stats: Record<string, DeciderStats>;
  shadowCallsLast24h: number; callable: boolean; roles: Record<string, Record<string, string>>;
}
export interface RouterVerdict { score: number; choice: string; threshold: number; checkpoint: string; package: string; device: string; latencyMs: number; cached: boolean }
export interface RoutingDecision {
  id: string; mode: RoutingMode; stage: string; kind: string; ruleTier: string; ruleWhy?: string[];
  router?: RouterVerdict; routerPair?: { weak: string; strong: string; threshold: number; calibration?: string }; routerError?: string;
  selected?: string; source: string; reason: string; candidates: { profile: RoutingProfile; excluded?: RoutingExclusion[] }[];
  createdAt: string; durationMs: number; ruleTie?: string[]; decider?: DeciderRecord; strategy?: RoutingStrategy;
}
// POST /agents/{id}/route: the auto session bound to its routed (or fallback) tool.
export interface RouteAgentResult {
  agent: any;
  profile?: { id: string; adapter: string; model?: string; effort?: string };
  ruleTier?: string;
  source?: string;
  fallback?: 'routing_off' | 'no_profile' | 'routing_error' | 'unsupported_adapter';
}
// POST /agents/{id}/route-turn: whether a later auto turn moved models.
export interface RouteTurnResult {
  applied: boolean;
  reason: string;
  from?: { id: string; adapter: string; model?: string; effort?: string };
  to?: { id: string; adapter: string; model?: string; effort?: string };
  ruleTier?: string;
  // Tool switch (reason tool_switch / confirm_tool_switch).
  agent?: any;
  handoffTurns?: number;
  handoffTokens?: number;
  // reason "delegating": the hard request went to a stronger model for advice.
  delegate?: DelegateJob;
}
export interface DelegateJob {
  id: string; agentId: string; status: 'running' | 'done' | 'failed' | 'canceled';
  target: { id: string; adapter: string; model?: string; effort?: string }; briefTokens: number; error?: string;
}
// Recorded turns of "자동" sessions (GET /agents/{id}/auto-usage, /auto-usage).
// Token sums cover only the turns whose CLI reported usage (reported ≤ turns).
export interface AutoModelUsage { tool: string; model: string; effort: string; turns: number; reported: number; input: number; output: number; cacheCreation: number; cacheRead: number }
export interface AutoIdleBucket { label: string; minSeconds: number; turns: number; reported: number; cacheRead: number; inputTotal: number }
export interface AutoUsage { turns: number; models: AutoModelUsage[]; modelSwitches: number; toolSwitches: number; delegations: number; handoffTokens: number; idle: AutoIdleBucket[]; idleThresholdSeconds: number }
// Settings → 로컬 모델 (GET/PUT/DELETE /v2/routing/local).
export interface LocalModelEntry {
  id: string; url: string; kind: 'openai' | 'ollama' | string; model: string;
  status: string; detail?: string; models: string[];
  useForEasy: boolean; useAsJudge: boolean; profileId?: string; tiers?: string[];
}
export interface LocalModelInput { url: string; kind: string; model: string; useForEasy: boolean; useAsJudge: boolean }
export interface RoutingChoice { decision: RoutingDecision; profile?: RoutingProfile }
export interface RoutingRunState { runId: string; mode: RoutingMode; phase: string; epoch: number; currentProfile: string; currentExecution: string; pendingProfile: string; pendingWhen: string; pinProfile: string; pinAdapter: string; switches: number; attempts: number }
export interface RoutingUsage { scope: string; source: string; inputTokens: number | null; outputTokens: number | null; cacheReadTokens: number | null; cacheCreationTokens: number | null; thinkingTokens: number | null; totalTokens: number | null; local: boolean }
export interface RoutingAttempt {
  executionId: string; profileId: string; adapter: string; model: string; effort: string; decisionId: string; inheritedFrom: string;
  continuation?: { kind: string; reason: string }; class: string; action?: { kind: string; reason: string }; startedAt: string; finishedAt: string;
  report?: { providerStatus: string; observedModel?: string; failedChecks?: string[]; quiesceVerified: boolean; quiesceDetail?: string; usage?: RoutingUsage;
    timings: { routingMs: number; queueMs: number; handoffMs: number; execMs: number; verifyMs: number };
    reviewer?: string; reviewerModel?: string; reviewUsage?: RoutingUsage };
}
export interface RoutingTransition { seq: number; from: string; to: string; cause: string; detail: string; executionId: string; createdAt: string }
export interface RoutingTimeline {
  state: RoutingRunState; attempts: RoutingAttempt[]; decisions: RoutingDecision[]; transitions: RoutingTransition[];
  strategy: RoutingStrategy; options: { strategy: RoutingStrategy | ''; commercialShadow: boolean }; usage: RoleUsage[]; commercialUsage: RoleUsage;
}

export interface RunSummary {
  id: string;
  path: string;
  prompt: string;
  provider: string;
  state: string;
}

export interface RunApproval {
  id: string;
  sessionId: string;
  toolName: string;
  input: unknown;
}

export interface PlanSnapshot {
  applications: RunApplication[];
  integrations: RunExecution[];
  concurrency: number;
  tasks: { id: string; prompt: string; provider: string; dependsOn: string[]; state: string; attemptId: string; detail: string; artifacts: RunArtifact[]; checks: RunCheck[] }[];
  selection: { ready: { id: string }[]; blocked: string[]; complete: boolean };
}

export interface PlanTask { id: string; prompt: string; provider: string; dependsOn: string[] }
export interface TaskPlan { concurrency: number; tasks: PlanTask[] }
export interface PlanDraft { id: string; state: string; detail: string; plan: TaskPlan | null }
export interface DraftHistory { drafts: PlanDraft[]; nextCursor: string }
export interface CleanupPreview { candidates: { id: string; kind: string; state: string; eligible: boolean; reason: string; fingerprint?: string }[]; nextCursor: string }
export interface TaskHistory { attempts: RunExecution[]; nextCursor: string }
export interface ConflictVersion { artifact?: string; unavailable?: string; mode?: string }
export interface ConflictReport {
  fingerprint?: string;
  baseCommit: string; incomingCommit: string; truncated: boolean;
  files: { path: string; base: ConflictVersion; current: ConflictVersion; incoming: ConflictVersion }[];
}
export interface ResolvedFile { path: string; content: string; delete: boolean }

export interface ApplyTarget {
  integrationId: string;
  branch: string;
  baseCommit: string;
  resultCommit: string;
}
export interface ApplyPreview extends ApplyTarget { summary: string }
export interface RunApplication extends ApplyTarget { id: string; state: string; detail: string }

export interface RunArtifact {
  kind: string;
  baseCommit: string;
}

export interface RunCheck {
  name: string;
  passed: boolean;
  detail: string;
}

export interface RunExecution {
  id: string;
  state: string;
  detail: string;
  checks: RunCheck[];
  artifacts: RunArtifact[];
}

export interface Run extends RunSummary {
  workspaceId: string;
  baseCommit: string;
  taskId: string;
  requiredChecks: string[];
  executions: RunExecution[];
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

// An opt-in API that is not registered on the server (e.g. 2.0 without
// PCD_V2_ENABLED) answers 404, or — through the SPA fallback — 200 with HTML,
// which fails JSON parsing. Both mean "feature off", not an error.
export function isFeatureOff(err: unknown): boolean {
  return (err instanceof ApiError && err.status === 404) || err instanceof SyntaxError;
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
  routeTurn: (id: string, goal: string, confirmTool = false) =>
    apiFetch<RouteTurnResult>(`/agents/${encodeURIComponent(id)}/route-turn`, { method: 'POST', body: JSON.stringify({ goal, confirmTool }) }),
  autoUsage: (id: string) => apiFetch<AutoUsage>(`/agents/${encodeURIComponent(id)}/auto-usage`),
  autoUsageRecent: (days = 7) => apiFetch<AutoUsage>(`/auto-usage?days=${days}`),
  delegateStatus: (id: string, job: string) => apiFetch<DelegateJob>(`/agents/${encodeURIComponent(id)}/delegate/${encodeURIComponent(job)}`),
  cancelDelegate: (id: string, job: string) => apiFetch<void>(`/agents/${encodeURIComponent(id)}/delegate/${encodeURIComponent(job)}`, { method: 'DELETE' }),
  escalate: (id: string, goal: string) =>
    apiFetch<RouteTurnResult>(`/agents/${encodeURIComponent(id)}/escalate`, { method: 'POST', body: JSON.stringify({ goal }) }),
  clearAutoProfile: (id: string) => apiFetch<void>(`/agents/${encodeURIComponent(id)}/auto-profile`, { method: 'DELETE' }),
  routeAgent: (id: string, goal: string, adapter = '') =>
    apiFetch<RouteAgentResult>(`/agents/${encodeURIComponent(id)}/route`, { method: 'POST', body: JSON.stringify({ goal, adapter }) }),
  deleteAgent: (id: string) => apiFetch(`/agents/${id}`, { method: 'DELETE' }),
  restartAgent: (id: string) => apiFetch(`/agents/${id}/restart`, { method: 'POST' }),
  // Stop a session but KEEP the agent (reversible "정지"), unlike deleteAgent which
  // removes the record. Stops both the native session and the PTY.
  stopAgent: (id: string) => apiFetch(`/agents/${id}/stop`, { method: 'POST' }),

  // Durable 2.0 Runs. These routes remain opt-in on the server until the runtime
  // is ready to replace the legacy session-first flow.
  listRuns: () => apiFetch<{ runs: RunSummary[] }>('/v2/runs'),
  getRunPlan: (id: string) => apiFetch<PlanSnapshot>(`/v2/runs/${encodeURIComponent(id)}/plan`),
  startRunPlan: (id: string) => apiFetch(`/v2/runs/${encodeURIComponent(id)}/plan/start`, { method: 'POST' }),
  generateRunPlan: (id: string) => apiFetch<{ draftId: string }>(`/v2/runs/${encodeURIComponent(id)}/plan/draft/generate`, { method: 'POST' }),
  getRunPlanDraft: (id: string) => apiFetch<PlanDraft>(`/v2/runs/${encodeURIComponent(id)}/plan/draft`),
  getDraftHistory: (id: string, before = '') => apiFetch<DraftHistory>(`/v2/runs/${encodeURIComponent(id)}/plan/drafts?before=${encodeURIComponent(before)}`),
  previewCleanup: (id: string, before = '') => apiFetch<CleanupPreview>(`/v2/runs/${encodeURIComponent(id)}/cleanup?before=${encodeURIComponent(before)}`),
  cleanupWorkspace: (id: string, attempt: string, fingerprint: string) => apiFetch<void>(`/v2/runs/${encodeURIComponent(id)}/cleanup/${encodeURIComponent(attempt)}`, { method: 'POST', body: JSON.stringify({ fingerprint }) }),
  getTaskHistory: (id: string, task: string, before = '') => apiFetch<TaskHistory>(`/v2/runs/${encodeURIComponent(id)}/plan/tasks/${encodeURIComponent(task)}/attempts?${new URLSearchParams({ before })}`),
  resolveIntegration: (id: string, attempt: string, fingerprint: string, files: ResolvedFile[]) => apiFetch<{ executionId: string }>(`/v2/runs/${encodeURIComponent(id)}/plan/integrations/${encodeURIComponent(attempt)}/resolve`, { method: 'POST', body: JSON.stringify({ fingerprint, files }) }),
  resolveTask: (id: string, task: string, attempt: string, fingerprint: string, files: ResolvedFile[]) => apiFetch<void>(`/v2/runs/${encodeURIComponent(id)}/plan/tasks/${encodeURIComponent(task)}/attempts/${encodeURIComponent(attempt)}/resolve`, { method: 'POST', body: JSON.stringify({ fingerprint, files }) }),
  saveRunPlan: (id: string, plan: TaskPlan) => apiFetch(`/v2/runs/${encodeURIComponent(id)}/plan`, { method: 'PUT', body: JSON.stringify(plan) }),
  integrateRunPlan: (id: string) => apiFetch<{ executionId: string }>(`/v2/runs/${encodeURIComponent(id)}/plan/integrate`, { method: 'POST' }),
  previewRunApplication: (id: string) => apiFetch<ApplyPreview>(`/v2/runs/${encodeURIComponent(id)}/plan/apply`),
  applyRunResult: (id: string, target: ApplyTarget) => apiFetch<RunApplication>(`/v2/runs/${encodeURIComponent(id)}/plan/apply`, { method: 'POST', body: JSON.stringify(target) }),
  reconcileRunApplication: (id: string) => apiFetch<RunApplication>(`/v2/runs/${encodeURIComponent(id)}/plan/apply/reconcile`, { method: 'POST' }),
  retryRunTask: (id: string, task: string) => apiFetch(`/v2/runs/${encodeURIComponent(id)}/plan/tasks/${encodeURIComponent(task)}/retry`, { method: 'POST' }),
  runApprovals: (id: string) => apiFetch<RunApproval[]>(`/v2/runs/${encodeURIComponent(id)}/approvals`),
  decideRunApproval: (run: string, id: string, behavior: 'allow' | 'deny') =>
    apiFetch(`/v2/runs/${encodeURIComponent(run)}/approvals`, { method: 'POST', body: JSON.stringify({ id, behavior }) }),
  getRun: (id: string) => apiFetch<Run>(`/v2/runs/${encodeURIComponent(id)}`),
  routingSnapshot: () => apiFetch<RoutingSnapshot>('/v2/routing'),
  routingChoose: (goal: string) => apiFetch<RoutingChoice>('/v2/routing/choose', { method: 'POST', body: JSON.stringify({ goal }) }),
  localModels: () => apiFetch<{ endpoints: LocalModelEntry[] }>('/v2/routing/local'),
  testLocalModel: (url: string, kind: string) => apiFetch<{ models: string[] }>('/v2/routing/local/test', { method: 'POST', body: JSON.stringify({ url, kind }) }),
  saveLocalModel: (id: string, body: LocalModelInput) => apiFetch<{ endpoints: LocalModelEntry[] }>(`/v2/routing/local/${encodeURIComponent(id)}`, { method: 'PUT', body: JSON.stringify(body) }),
  deleteLocalModel: (id: string) => apiFetch<void>(`/v2/routing/local/${encodeURIComponent(id)}`, { method: 'DELETE' }),
  routingRefresh: () => apiFetch<RoutingSnapshot>('/v2/routing/refresh', { method: 'POST' }),
  routingMarkValidated: (adapter: string) => apiFetch<void>(`/v2/routing/adapters/${encodeURIComponent(adapter)}/validated`, { method: 'POST' }),
  runRouting: (id: string) => apiFetch<RoutingTimeline>(`/v2/runs/${encodeURIComponent(id)}/routing`),
  setRunRouting: (id: string, body: { mode: RoutingMode; pinProfile: string; pinAdapter: string; strategy?: RoutingStrategy | ''; commercialShadow?: boolean }) =>
    apiFetch<RoutingRunState>(`/v2/runs/${encodeURIComponent(id)}/routing`, { method: 'PUT', body: JSON.stringify(body) }),
  forgetRunRouting: (id: string) => apiFetch<void>(`/v2/runs/${encodeURIComponent(id)}/routing`, { method: 'DELETE' }),
  previewRunRouting: (id: string, profile = '') => apiFetch<RoutingDecision>(`/v2/runs/${encodeURIComponent(id)}/routing/preview?${new URLSearchParams({ profile })}`),
  startRoutedRun: (id: string, profile = '', reevaluate = false) =>
    apiFetch<{ executionId: string; decision: RoutingDecision; launchedProfile: string }>(`/v2/runs/${encodeURIComponent(id)}/routing/start`, { method: 'POST', body: JSON.stringify({ profile, reevaluate }) }),
  switchRunProfile: (id: string, profile: string, when: 'boundary' | 'now') =>
    apiFetch<RoutingRunState>(`/v2/runs/${encodeURIComponent(id)}/routing/switch`, { method: 'POST', body: JSON.stringify({ profile, when }) }),
  createRun: (data: { path: string; prompt: string; provider: string }, key: string) =>
    apiFetch<Run>('/v2/runs', {
      method: 'POST',
      headers: { 'Idempotency-Key': key },
      body: JSON.stringify(data),
    }),
  startRun: (id: string) =>
    apiFetch<{ executionId: string }>(`/v2/runs/${encodeURIComponent(id)}/start`, { method: 'POST' }),
  cancelRun: (id: string) =>
    apiFetch(`/v2/runs/${encodeURIComponent(id)}/cancel`, { method: 'POST' }),
  readRunArtifact: async (runId: string, executionId: string, kind: string): Promise<string> => {
    const query = new URLSearchParams({ kind });
    const res = await apiRequest(
      `/v2/runs/${encodeURIComponent(runId)}/executions/${encodeURIComponent(executionId)}/artifact?${query}`,
    );
    if (!res.ok) throw new ApiError('결과 파일을 불러오지 못했습니다', res.status);
    return res.text();
  },

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
