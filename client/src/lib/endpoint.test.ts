// Framework-free assertions at module scope, matching savings.test.ts. There is no
// client test runner in this repo (no `test` script, no vitest); tsconfig's
// include:["src"] means tsc type-checks this file, which is what keeps the
// contract honest at build time.
import {
  LOCAL_ENDPOINT_ID,
  apiUrl,
  localEndpoint,
  wsUrl,
  type Endpoint,
} from './endpoint';

function equal(actual: unknown, expected: unknown, label: string) {
  if (actual !== expected) throw new Error(`${label}: expected ${String(expected)}, got ${String(actual)}`);
}

function remote(baseUrl: string): Endpoint {
  return { id: 'r', label: 'r', baseUrl, capabilities: { webPush: false, localFiles: false } };
}

// The load-bearing property: the default endpoint produces exactly the URLs the
// client used before this module existed. If this breaks, every same-origin user
// is affected by a change that was supposed to be additive.
equal(apiUrl('/agents', localEndpoint()), '/api/agents', 'local endpoint keeps origin-relative /api');
equal(localEndpoint().id, LOCAL_ENDPOINT_ID, 'local endpoint id');
equal(localEndpoint().capabilities.webPush, true, 'same-origin can use web push');

equal(apiUrl('/agents', remote('https://pcd.example.com')), 'https://pcd.example.com/api/agents', 'remote api url');
// A trailing slash must not produce '//api', which some proxies reject outright.
equal(apiUrl('/agents', remote('https://pcd.example.com/')), 'https://pcd.example.com/api/agents', 'trailing slash normalized');
equal(apiUrl('/agents', remote('  https://pcd.example.com  ')), 'https://pcd.example.com/api/agents', 'whitespace normalized');

// The WS scheme follows the ENDPOINT's origin, not the page's: once the client is
// served separately the two can differ, and guessing from the page gives wss:// to
// a plain-http LAN box.
equal(wsUrl('t', 'd', remote('https://pcd.example.com')), 'wss://pcd.example.com/ws?token=t&device=d', 'https endpoint uses wss');
equal(wsUrl('t', 'd', remote('http://192.168.1.50:33033')), 'ws://192.168.1.50:33033/ws?token=t&device=d', 'http endpoint uses ws');
equal(wsUrl('t', 'a b', remote('http://x.test')), 'ws://x.test/ws?token=t&device=a%20b', 'device id is encoded');

export {};
