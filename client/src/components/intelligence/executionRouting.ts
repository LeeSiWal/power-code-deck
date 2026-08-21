import type { IntelligenceMode, IntelligenceStartResult } from '../../lib/api';

export type NativeDriverName = 'codex' | 'claude';
export type LocalOperation = 'summarize' | 'explain' | 'classify' | 'log_analysis' | 'repository_question';

export interface NativeTaskRouteInput {
  agentId: string;
  driver: NativeDriverName;
  task: string;
  mode: IntelligenceMode;
  provider?: string;
  operation?: LocalOperation;
  hasAttachments?: boolean;
  isNativeCommand?: boolean;
}

export interface NativeTaskRouteDependencies {
  sendNative: (text: string) => void;
  runIntelligence: (request: {
    agentId: string;
    task: string;
    mode: IntelligenceMode;
    provider?: string;
    operation?: string;
  }) => Promise<IntelligenceStartResult>;
}

// The intelligence paths return an ACCEPTED run, not a finished one: the outcome
// arrives later on the intelligence:trace event, keyed by start.trace.id.
export type NativeTaskRouteResult =
  | { path: 'cloud'; attachmentFallback: boolean; commandBypass: boolean }
  | { path: 'hybrid' | 'local'; start: IntelligenceStartResult };

export function clientCommand(text: string): 'clear' | 'plugin' | 'native' | null {
  if (text === '/clear') return 'clear';
  if (/^\/plugins?(\s|$)/.test(text)) return 'plugin';
  if (/^[\/@][\w:-]+(?:\s|$)/.test(text)) return 'native';
  return null;
}

export async function routeNativeTask(
  input: NativeTaskRouteInput,
  dependencies: NativeTaskRouteDependencies,
): Promise<NativeTaskRouteResult> {
  const attachmentFallback = !!input.hasAttachments && input.mode !== 'CLOUD_ONLY';
  const commandBypass = !!input.isNativeCommand && input.mode !== 'CLOUD_ONLY';
  if (input.mode === 'CLOUD_ONLY' || attachmentFallback || commandBypass) {
    dependencies.sendNative(input.task);
    return { path: 'cloud', attachmentFallback, commandBypass };
  }
  if (!input.provider) throw new Error('LOCAL_PROVIDER_REQUIRED');
  if (input.mode === 'LOCAL_ONLY' && !input.operation) throw new Error('LOCAL_OPERATION_REQUIRED');

  const start = await dependencies.runIntelligence({
    agentId: input.agentId,
    task: input.task,
    mode: input.mode,
    provider: input.provider,
    operation: input.mode === 'LOCAL_ONLY' ? input.operation : undefined,
  });
  return { path: input.mode === 'LOCAL_ONLY' ? 'local' : 'hybrid', start };
}

export function cloudTargetName(driver: NativeDriverName): string {
  return driver === 'codex' ? 'Codex' : 'Claude Code';
}
