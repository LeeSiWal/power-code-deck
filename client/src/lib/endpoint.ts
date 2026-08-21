// Which PowerCodeDeck server this client is talking to.
//
// PowerCodeDeck's server is not an API — it IS the machine: it spawns the PTYs,
// runs git in the working directory, and holds the source. So "connect to a remote
// server" means "drive another workstation", and the client has to be able to name
// more than one of them. Everything else in this file follows from that.
//
// The default endpoint has an empty baseUrl, meaning the current origin. That is
// deliberate: a browser loading the page the server itself serves keeps behaving
// exactly as it did before this module existed — same URLs, same storage, no
// migration. Remote endpoints are additive.

const LIST_KEY = 'pcd:endpoints';
const CURRENT_KEY = 'pcd:endpoints:current';

/** The always-present endpoint: whatever origin served this page. */
export const LOCAL_ENDPOINT_ID = 'local';

export interface EndpointCapabilities {
  /** Web Push needs a service worker, which is bound to the page's own origin. A
   *  remote endpoint therefore cannot deliver it — the desktop shell uses native
   *  notifications there instead. */
  webPush: boolean;
  /** Whether the files this endpoint exposes live on the same machine as the UI.
   *  False for remote hosts: dropping a local file must UPLOAD, not path-reference. */
  localFiles: boolean;
}

export interface Endpoint {
  id: string;
  label: string;
  /** '' means the current origin. Otherwise an absolute http(s) origin, no trailing slash. */
  baseUrl: string;
  capabilities: EndpointCapabilities;
}

function safeGet(key: string): string | null {
  try {
    return localStorage.getItem(key);
  } catch {
    return null;
  }
}

function safeSet(key: string, value: string) {
  try {
    localStorage.setItem(key, value);
  } catch {
    /* storage disabled — the session still works, it just won't be remembered */
  }
}

function safeRemove(key: string) {
  try {
    localStorage.removeItem(key);
  } catch {
    /* ignore */
  }
}

function normalizeBaseUrl(raw: string): string {
  const trimmed = (raw || '').trim().replace(/\/+$/, '');
  return trimmed;
}

export function localEndpoint(): Endpoint {
  return {
    id: LOCAL_ENDPOINT_ID,
    label: 'This machine',
    baseUrl: '',
    capabilities: { webPush: true, localFiles: true },
  };
}

export function listEndpoints(): Endpoint[] {
  const raw = safeGet(LIST_KEY);
  if (!raw) return [localEndpoint()];
  try {
    const parsed = JSON.parse(raw) as Endpoint[];
    if (!Array.isArray(parsed) || parsed.length === 0) return [localEndpoint()];
    // The local endpoint is structural, not user data — always present, never
    // duplicated by a stored copy that might have drifted.
    return [localEndpoint(), ...parsed.filter((e) => e && e.id !== LOCAL_ENDPOINT_ID)];
  } catch {
    return [localEndpoint()];
  }
}

export function saveEndpoints(endpoints: Endpoint[]) {
  safeSet(LIST_KEY, JSON.stringify(endpoints.filter((e) => e.id !== LOCAL_ENDPOINT_ID)));
}

export function currentEndpointId(): string {
  return safeGet(CURRENT_KEY) || LOCAL_ENDPOINT_ID;
}

export function currentEndpoint(): Endpoint {
  const id = currentEndpointId();
  return listEndpoints().find((e) => e.id === id) || localEndpoint();
}

export function setCurrentEndpoint(id: string) {
  safeSet(CURRENT_KEY, id);
}

/** Absolute (or origin-relative) URL for an API path. */
export function apiUrl(path: string, endpoint: Endpoint = currentEndpoint()): string {
  const base = normalizeBaseUrl(endpoint.baseUrl);
  return `${base}/api${path}`;
}

/** WebSocket URL for an endpoint, with the scheme derived from its own origin —
 *  not from the page's, which may differ once the client is served separately. */
export function wsUrl(token: string, device: string, endpoint: Endpoint = currentEndpoint()): string {
  const base = normalizeBaseUrl(endpoint.baseUrl);
  let origin: string;
  if (base) {
    origin = base.replace(/^http:/, 'ws:').replace(/^https:/, 'wss:');
  } else {
    origin = `${location.protocol === 'https:' ? 'wss:' : 'ws:'}//${location.host}`;
  }
  return `${origin}/ws?token=${token}&device=${encodeURIComponent(device)}`;
}

// --- tokens ------------------------------------------------------------------
//
// Per endpoint, because they are different servers with different secrets. A
// remote endpoint expiring must not log the user out of the local one.

const LEGACY_ACCESS = 'accessToken';
const LEGACY_REFRESH = 'refreshToken';

function accessKey(id: string) {
  return `pcd:endpoints:${id}:accessToken`;
}

function refreshKey(id: string) {
  return `pcd:endpoints:${id}:refreshToken`;
}

// One-time move of the pre-endpoint keys onto the local endpoint. Without this an
// existing user is silently logged out by the upgrade.
let migrated = false;
function migrateLegacyTokens() {
  if (migrated) return;
  migrated = true;
  const access = safeGet(LEGACY_ACCESS);
  const refresh = safeGet(LEGACY_REFRESH);
  if (access && !safeGet(accessKey(LOCAL_ENDPOINT_ID))) {
    safeSet(accessKey(LOCAL_ENDPOINT_ID), access);
  }
  if (refresh && !safeGet(refreshKey(LOCAL_ENDPOINT_ID))) {
    safeSet(refreshKey(LOCAL_ENDPOINT_ID), refresh);
  }
  safeRemove(LEGACY_ACCESS);
  safeRemove(LEGACY_REFRESH);
}

export function getToken(id: string = currentEndpointId()): string | null {
  migrateLegacyTokens();
  return safeGet(accessKey(id));
}

export function getRefreshToken(id: string = currentEndpointId()): string | null {
  migrateLegacyTokens();
  return safeGet(refreshKey(id));
}

export function setTokens(access: string, refresh: string, id: string = currentEndpointId()) {
  migrateLegacyTokens();
  safeSet(accessKey(id), access);
  if (refresh) safeSet(refreshKey(id), refresh);
}

export function setAccessToken(access: string, id: string = currentEndpointId()) {
  migrateLegacyTokens();
  safeSet(accessKey(id), access);
}

export function clearTokens(id: string = currentEndpointId()) {
  safeRemove(accessKey(id));
  safeRemove(refreshKey(id));
}
