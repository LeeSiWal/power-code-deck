// Framework-free assertions (type-checked by tsc like the other *.test.ts files).
import {
  agyPasteMessage, elapsedLabel, parseTeamResult, pickAgyTerminal, teamLimitValue, teamStatusLabel, teamTokensLabel, teamToolCall,
} from './teamTools';

function equal(actual: unknown, expected: unknown, label: string) {
  if (actual !== expected) throw new Error(`${label}: expected ${String(expected)}, got ${String(actual)}`);
}

// Claude conductor: the MCP tool name carries the server prefix.
const ask = teamToolCall('mcp__pcd__ask_member', { member: 'codex', task: 'look' });
equal(ask?.kind, 'ask', 'claude ask_member recognised');
equal(ask?.args.member, 'codex', 'claude args kept');
equal(teamToolCall('mcp__pcd__handoff_to_agy', { task: 't' })?.kind, 'agy', 'claude handoff recognised');
equal(teamToolCall('Bash', { command: 'ls' }), null, 'other tools ignored');
// Codex conductor: one generic "MCP" item with server/tool/arguments.
const codexAsk = teamToolCall('MCP', { server: 'pcd', tool: 'ask_member', arguments: { member: 'claude-haiku', task: 'x' } });
equal(codexAsk?.kind, 'ask', 'codex ask_member recognised');
equal(codexAsk?.args.member, 'claude-haiku', 'codex args unwrapped');
equal(teamToolCall('MCP', { server: 'other', tool: 'ask_member', arguments: {} }), null, 'other MCP servers ignored');

// A done result, as the Claude tool_result text.
const done = JSON.stringify({ status: 'done', member: 'codex', label: 'Codex', text: '요약', truncated: true, callId: 'tm_1', elapsedMs: 12345, tokens: null });
const r = parseTeamResult(done);
equal(r?.status, 'done', 'status parsed');
equal(r?.truncated, true, 'truncated parsed');
equal(r?.tokens, null, 'null tokens stay null');
// Codex wraps it in the MCP CallToolResult.
const wrapped = JSON.stringify({ content: [{ type: 'text', text: done }], isError: false });
equal(parseTeamResult(wrapped)?.callId, 'tm_1', 'MCP CallToolResult unwrapped');
// Malformed or foreign JSON is not a TeamResult.
equal(parseTeamResult('not json'), null, 'garbage rejected');
equal(parseTeamResult('{"foo":1}'), null, 'object without status rejected');
equal(parseTeamResult(undefined), null, 'missing result');
equal(parseTeamResult(JSON.stringify({ status: 'handed_off', text: 'x' }))?.status, 'handed_off', 'handoff result parsed');

// Usage is never invented.
equal(teamTokensLabel(null), '알 수 없음', 'unreported tokens');
equal(teamTokensLabel(undefined), '알 수 없음', 'missing tokens');
equal(teamTokensLabel({ in: 1200, out: 30 }), '입력 1.2k · 출력 30', 'reported tokens');
equal(teamTokensLabel({ in: 0, out: 0 }), '입력 0 · 출력 0', 'reported zero stays zero');

equal(elapsedLabel(800), '0.8초', 'sub-second');
equal(elapsedLabel(12345), '12초', 'seconds');
equal(elapsedLabel(65000), '1분 5초', 'minutes');

const statuses = ['done', 'refused', 'limit', 'failed', 'timeout', 'not_installed', 'not_logged_in', 'quota'];
equal(new Set(statuses.map(teamStatusLabel)).size, statuses.length, 'status labels distinct');
equal(teamStatusLabel('something_new'), 'something_new', 'unknown status passes through');

equal(agyPasteMessage('  결과  '), '[agy 결과]\n결과', 'paste prefix');

// The agy tab: a terminal session running agy in the same folder. A native
// Antigravity session (preset "antigravity") always renders as chat, so it is
// never picked.
const agents = [
  { id: 'n1', preset: 'antigravity', command: 'agy', workingDir: '/p', status: 'stopped' },
  { id: 't0', preset: 'custom', command: 'agy', workingDir: '/other', status: 'running' },
  { id: 't1', preset: 'custom', command: 'agy', workingDir: '/p', status: 'stopped' },
  { id: 't2', preset: 'custom', command: 'agy', workingDir: '/p', status: 'running' },
];
equal(pickAgyTerminal(agents, '/p')?.id, 't2', 'running terminal agy preferred');
equal(pickAgyTerminal(agents.slice(0, 3), '/p')?.id, 't1', 'stopped terminal agy reused');
equal(pickAgyTerminal(agents.slice(0, 2), '/p'), undefined, 'native agy never reused');

// Settings number inputs keep the typed string; it becomes a number only on
// blur/save. Empty or non-numbers → 0 (the server restores the default), the
// timeout is capped at the server's 600s.
equal(teamLimitValue('timeoutSec', ''), 0, 'empty limit → 0');
equal(teamLimitValue('timeoutSec', 'abc'), 0, 'garbage limit → 0');
equal(teamLimitValue('maxCallsPerTurn', ' 4.7 '), 4, 'fraction floored');
equal(teamLimitValue('maxCallsPerTurn', '-3'), 0, 'negative → 0');
equal(teamLimitValue('timeoutSec', '9999'), 600, 'timeout capped at 600');
equal(teamLimitValue('sessionTokenCap', '9999999'), 9999999, 'other limits not capped');
