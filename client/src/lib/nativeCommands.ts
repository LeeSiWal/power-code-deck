export type NativeDriverName = 'codex' | 'claude';

export function clientCommand(text: string): 'clear' | 'plugin' | 'native' | null {
  if (text === '/clear') return 'clear';
  if (/^\/plugins?(\s|$)/.test(text)) return 'plugin';
  if (/^[\/@][\w:-]+(?:\s|$)/.test(text)) return 'native';
  return null;
}
