// Dream team: reading the conductor's ask_member / handoff_to_agy tool calls.
// Pure helpers so the parsing is testable without a DOM (teamTools.test.ts).
import { compactTokens } from './routingLabels';

/** What ask_member returns (server/services/team.go TeamResult), or a handoff result. */
export interface TeamResult {
  status: string; // done | refused | limit | failed | timeout | not_installed | not_logged_in | quota | handed_off
  member?: string;
  label?: string;
  text?: string;
  truncated?: boolean;
  callId?: string;
  elapsedMs?: number;
  /** null = the member did not report usage (Codex). Never shown as zero. */
  tokens?: { in: number; out: number } | null;
  reason?: string;
}

export type TeamToolCall = { kind: 'ask' | 'agy'; args: Record<string, unknown> };

const TOOL_KINDS: Record<string, TeamToolCall['kind']> = { ask_member: 'ask', handoff_to_agy: 'agy' };

/**
 * Whether a chat tool item is one of the team tools. A Claude conductor names it
 * `mcp__pcd__<tool>`; a Codex conductor's item is a generic "MCP" call with
 * {server, tool, arguments} (codex_driver.go emitItem).
 */
export function teamToolCall(name: string, input: Record<string, unknown>): TeamToolCall | null {
  if (name.startsWith('mcp__pcd__')) {
    const kind = TOOL_KINDS[name.slice('mcp__pcd__'.length)];
    return kind ? { kind, args: input ?? {} } : null;
  }
  if (name === 'MCP' && input?.server === 'pcd' && typeof input.tool === 'string') {
    const kind = TOOL_KINDS[input.tool];
    if (!kind) return null;
    const a = input.arguments;
    return { kind, args: a && typeof a === 'object' && !Array.isArray(a) ? a as Record<string, unknown> : {} };
  }
  return null;
}

/**
 * Parse a tool_result's text into a TeamResult. Accepts the bare JSON (Claude
 * flattens the MCP content to its text) or the whole MCP CallToolResult (Codex
 * forwards it raw). Anything else — an error string, foreign JSON — is null, and
 * the card shows the raw text instead.
 */
export function parseTeamResult(raw: string | undefined): TeamResult | null {
  if (!raw) return null;
  let v: unknown;
  try { v = JSON.parse(raw); } catch { return null; }
  if (v && typeof v === 'object' && Array.isArray((v as { content?: unknown }).content)) {
    const text = ((v as { content: unknown[] }).content)
      .map((b) => (b && typeof b === 'object' && typeof (b as { text?: unknown }).text === 'string' ? (b as { text: string }).text : ''))
      .join('');
    return parseTeamResult(text);
  }
  if (!v || typeof v !== 'object' || typeof (v as { status?: unknown }).status !== 'string') return null;
  const o = v as Record<string, unknown>;
  const t = o.tokens as { in?: unknown; out?: unknown } | null | undefined;
  return {
    status: o.status as string,
    member: typeof o.member === 'string' ? o.member : undefined,
    label: typeof o.label === 'string' ? o.label : undefined,
    text: typeof o.text === 'string' ? o.text : undefined,
    truncated: o.truncated === true,
    callId: typeof o.callId === 'string' ? o.callId : undefined,
    elapsedMs: typeof o.elapsedMs === 'number' ? o.elapsedMs : undefined,
    tokens: t && typeof t.in === 'number' && typeof t.out === 'number' ? { in: t.in, out: t.out } : null,
    reason: typeof o.reason === 'string' ? o.reason : undefined,
  };
}

const STATUS_LABELS: Record<string, string> = {
  done: '완료',
  refused: '거절됨',
  limit: '상한 도달',
  failed: '실패',
  timeout: '시간 초과',
  not_installed: '설치 안 됨',
  not_logged_in: '로그인 필요',
  quota: '사용량 한도 소진',
  handed_off: 'agy에게 넘김',
};

export function teamStatusLabel(status: string): string {
  return STATUS_LABELS[status] ?? status;
}

/** Tokens as reported; unreported usage is "알 수 없음", never 0. */
export function teamTokensLabel(t: TeamResult['tokens']): string {
  if (!t) return '알 수 없음';
  return `입력 ${compactTokens(t.in)} · 출력 ${compactTokens(t.out)}`;
}

export function elapsedLabel(ms: number): string {
  if (ms < 1000) return `${(ms / 1000).toFixed(1)}초`;
  const s = Math.round(ms / 1000);
  return s < 60 ? `${s}초` : `${Math.floor(s / 60)}분 ${s % 60}초`;
}

/** The message that hands agy's answer back to the conductor. */
export function agyPasteMessage(text: string): string {
  return `[agy 결과]\n${text.trim()}`;
}

interface AgentLike { id: string; preset?: string; command?: string; workingDir?: string; status?: string }

/**
 * The agy terminal to reuse in a project: a session running `agy` that renders as
 * a terminal. A native Antigravity session (preset "antigravity") always renders
 * as chat, so it never qualifies. Running sessions win over stopped ones.
 */
export function pickAgyTerminal<A extends AgentLike>(agents: A[], cwd: string): A | undefined {
  const candidates = agents.filter((a) => a.command === 'agy' && a.preset !== 'antigravity' && a.workingDir === cwd);
  return candidates.find((a) => a.status === 'running') ?? candidates[0];
}

export type TeamLimitKey = 'resultMaxChars' | 'maxCallsPerTurn' | 'sessionTokenCap' | 'timeoutSec';

/** The server's TimeoutSec cap (team_settings.go TeamMaxTimeoutSec). */
export const TEAM_MAX_TIMEOUT_SEC = 600;

/**
 * Turns a Settings limit input's text into the value to save. Empty, garbage or
 * ≤0 → 0, which the server replaces with the default; the timeout is capped.
 */
export function teamLimitValue(key: TeamLimitKey, text: string): number {
  const n = Math.max(0, Math.floor(Number(text.trim()) || 0));
  return key === 'timeoutSec' ? Math.min(n, TEAM_MAX_TIMEOUT_SEC) : n;
}
