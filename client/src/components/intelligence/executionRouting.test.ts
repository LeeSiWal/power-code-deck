import type { IntelligenceStartResult, IntelligenceTrace } from '../../lib/api';
import { clientCommand, routeNativeTask, type NativeDriverName } from './executionRouting';

function trace(overrides: Partial<IntelligenceTrace> = {}): IntelligenceTrace {
  return {
    id: 'PCD-ROUTE', mode: 'LOCAL_PREPROCESS_CLOUD', status: 'CLOUD_DISPATCHED',
    rawEstimatedTokens: 1000, optimizedEstimatedTokens: 200, localTokens: 50,
    latencyMs: 100, reductionPercent: 80, fallback: false, events: [],
    createdAt: '2026-08-16T00:00:00Z', ...overrides,
  };
}

function equal(actual: unknown, expected: unknown, label: string) {
  if (actual !== expected) throw new Error(`${label}: expected ${String(expected)}, got ${String(actual)}`);
}

async function verifyDriver(driver: NativeDriverName) {
  let nativeCalls = 0;
  let intelligenceCalls = 0;
  // POST /intelligence/run answers 202 with a RUNNING trace and nothing else —
  // the outcome arrives later on the intelligence:trace event.
  const result: IntelligenceStartResult = { trace: trace({ status: 'RUNNING' }) };
  const dependencies = {
    sendNative: () => { nativeCalls += 1; },
    runIntelligence: async () => { intelligenceCalls += 1; return result; },
  };

  await routeNativeTask({ agentId: 'a1', driver, task: 'cloud task', mode: 'CLOUD_ONLY' }, dependencies);
  equal(nativeCalls, 1, `${driver} Cloud Only sends native once`);
  equal(intelligenceCalls, 0, `${driver} Cloud Only skips intelligence`);

  nativeCalls = 0;
  intelligenceCalls = 0;
  await routeNativeTask({
    agentId: 'a1', driver, task: 'hybrid task', mode: 'LOCAL_PREPROCESS_CLOUD', provider: 'Mac Studio',
  }, dependencies);
  equal(nativeCalls, 0, `${driver} Hybrid skips direct native input`);
  equal(intelligenceCalls, 1, `${driver} Hybrid calls intelligence once`);
}

async function run() {
  await verifyDriver('codex');
  await verifyDriver('claude');

  let nativeCalls = 0;
  let intelligenceCalls = 0;
  const fallback: IntelligenceStartResult = { trace: trace({ status: 'RUNNING' }) };
  const fallbackResult = await routeNativeTask({
    agentId: 'a1', driver: 'claude', task: 'fallback task', mode: 'LOCAL_PREPROCESS_CLOUD', provider: 'offline',
  }, {
    sendNative: () => { nativeCalls += 1; },
    runIntelligence: async () => { intelligenceCalls += 1; return fallback; },
  });
  equal(fallbackResult.path, 'hybrid', 'a hybrid run stays on the intelligence route');
  equal(nativeCalls, 0, 'the client never sends a second cloud task of its own');
  equal(intelligenceCalls, 1, 'the backend is asked exactly once');

  equal(clientCommand('/clear'), 'clear', '/clear is intercepted');
  equal(clientCommand('/plugin'), 'plugin', '/plugin is intercepted');
  equal(clientCommand('/plugin install x@y'), 'plugin', '/plugin install is intercepted');
  equal(clientCommand('/help'), 'native', 'other slash commands keep the native path');
  equal(clientCommand('@review task'), 'native', 'agent commands keep the native path');
  equal(clientCommand('normal task'), null, 'normal task is routable');

  nativeCalls = 0;
  intelligenceCalls = 0;
  const attachmentResult = await routeNativeTask({
    agentId: 'a1', driver: 'codex', task: 'task with attachment', mode: 'LOCAL_PREPROCESS_CLOUD',
    provider: 'Mac Studio', hasAttachments: true,
  }, {
    sendNative: () => { nativeCalls += 1; },
    runIntelligence: async () => { intelligenceCalls += 1; return fallback; },
  });
  equal(attachmentResult.path, 'cloud', 'attachments use Cloud Only');
  equal(nativeCalls, 1, 'attachment fallback sends native once');
  equal(intelligenceCalls, 0, 'attachment fallback skips intelligence');

  nativeCalls = 0;
  intelligenceCalls = 0;
  const commandResult = await routeNativeTask({
    agentId: 'a1', driver: 'claude', task: '/help', mode: 'LOCAL_PREPROCESS_CLOUD',
    provider: 'Mac Studio', isNativeCommand: true,
  }, {
    sendNative: () => { nativeCalls += 1; },
    runIntelligence: async () => { intelligenceCalls += 1; return fallback; },
  });
  equal(commandResult.path, 'cloud', 'native command bypasses intelligence');
  equal(nativeCalls, 1, 'native command is sent exactly once');
  equal(intelligenceCalls, 0, 'native command skips intelligence');

  console.log('Native execution routing tests passed.');
}

void run();
