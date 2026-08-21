import { clientCommand } from './nativeCommands';

// Same convention as endpoint.test.ts: module-scope assertions, no runner. This
// repo has no client test framework, so tsc --noEmit type-checks this file and the
// assertions document the contract rather than execute in CI.
function equal(actual: unknown, expected: unknown, label: string) {
  if (actual !== expected) throw new Error(`${label}: expected ${String(expected)}, got ${String(actual)}`);
}

// These assertions moved here with clientCommand when the Local Intelligence POC was
// removed — the classifier survived that removal because it has nothing to do with
// local inference. It decides which typed lines the DECK answers itself instead of
// forwarding: /clear must start a genuinely new session (sent to the CLI it drops the
// context while leaving the transcript on screen), and /plugin must open the deck's
// own panel (the CLI answers "isn't available" over the stream).
equal(clientCommand('/clear'), 'clear', '/clear is intercepted');
equal(clientCommand('/plugin'), 'plugin', '/plugin is intercepted');
equal(clientCommand('/plugin install x@y'), 'plugin', '/plugin install is intercepted');
equal(clientCommand('/help'), 'native', 'other slash commands keep the native path');
equal(clientCommand('@review task'), 'native', 'agent commands keep the native path');
equal(clientCommand('normal task'), null, 'normal task is forwarded as a turn');
