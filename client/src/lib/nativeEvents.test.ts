import { foldEvents, isTurnActive, type StreamEvent } from './nativeEvents';

const delta = (text: string): StreamEvent => ({ type: 'stream_event', event: { type: 'content_block_delta', delta: { type: 'text_delta', text } } });
const items = foldEvents([
  { type: 'system', subtype: 'init', model: 'fake-agy', approval_handling: 'cli_settings' },
  { type: 'user', message: { content: [{ type: 'text', text: 'first' }] } },
  delta('one'), delta(' two'),
  { type: 'result', provider_notice: true, result: 'permission required' },
  { type: 'user', message: { content: [{ type: 'text', text: 'second' }] } },
  delta('three'),
  { type: 'result', provider_notice: true, is_error: true, result: 'interrupted' },
]);
const answers = items.filter((item) => item.kind === 'assistant');
if (answers.length !== 2 || answers[0].text !== 'one two' || answers[1].text !== 'three' || answers.some((item) => item.streaming)) {
  throw new Error('Antigravity turns were merged or left streaming after completion');
}
const session = items[0];
if (session.kind !== 'session' || session.approvalHandling !== 'cli_settings' || session.bridgeOk) {
  throw new Error('CLI policy was misrepresented as a connected approval bridge');
}
const notices = items.filter((item) => item.kind === 'result');
if (notices.length !== 2 || !notices.every((item) => item.notice && item.text && item.costUsd === undefined)) {
  throw new Error('Diagnostics lost or cost invented');
}

const storageFailure: StreamEvent[] = [
  { type: 'user', message: { content: [{ type: 'text', text: 'working' }] } },
  { type: 'storage_warning', result: 'Unable to save history' },
];
const storageItems = foldEvents(storageFailure);
if (!isTurnActive(storageFailure) || storageItems[1].kind !== 'result' || !storageItems[1].notice) {
  throw new Error('Storage failure was hidden or incorrectly ended the live turn');
}

// Codex approvals go through app-server itself, never through the pcd bridge;
// pcd only appears in its init when the team tools are configured. A Codex
// session without it must not raise the "bridge not connected" alarm, while a
// Claude session without it still must.
const codexInit = foldEvents([{ type: 'system', subtype: 'init', model: 'gpt-6.1-sol', claude_code_version: 'Codex app-server' }])[0];
if (codexInit.kind !== 'session' || !codexInit.bridgeOk) {
  throw new Error('Codex session without pcd raised the bridge alarm');
}
const claudeInit = foldEvents([{ type: 'system', subtype: 'init', model: 'claude-opus-5-5', claude_code_version: '2.1.212', mcp_servers: [] }])[0];
if (claudeInit.kind !== 'session' || claudeInit.bridgeOk) {
  throw new Error('Claude session without pcd hid the bridge alarm');
}
