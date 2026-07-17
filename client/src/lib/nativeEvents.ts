/**
 * Turning Claude Code's stream-json events into something a chat UI can render.
 *
 * The shapes here were captured from the real CLI (2.1.212), not copied from docs
 * — the CLI wire format isn't published. The server forwards each event's raw JSON
 * untouched, so anything it hasn't been taught about still arrives here.
 *
 * The rendering model is deliberately NOT "append every event". A turn emits the
 * same tool call twice (once as `tool_use`, once as a `tool_result` on a later
 * `user` event), so the UI folds them into ONE item that starts pending and then
 * resolves. That's the whole reason a chat UI beats a terminal here: we can update
 * a past line, which a terminal can only do by redrawing the screen.
 */

export interface StreamEvent {
  type: string;
  subtype?: string;
  session_id?: string;
  /** stream_event only (--include-partial-messages): the raw Anthropic SSE frame. */
  event?: {
    type?: string; // message_start | content_block_start | content_block_delta | …
    index?: number;
    delta?: { type?: string; text?: string };
    message?: { id?: string };
  };
  message?: {
    role?: string;
    content?: ContentBlock[];
  };
  parent_tool_use_id?: string | null;
  // system/init
  model?: string;
  cwd?: string;
  tools?: string[];
  mcp_servers?: { name: string; status: string }[];
  capabilities?: string[];
  claude_code_version?: string;
  permissionMode?: string;
  // result
  result?: string;
  is_error?: boolean;
  num_turns?: number;
  total_cost_usd?: number;
  permission_denials?: { tool_name: string; tool_use_id: string }[];
}

export interface ContentBlock {
  type: string;
  text?: string;
  id?: string;
  name?: string;
  input?: Record<string, unknown>;
  tool_use_id?: string;
  is_error?: boolean;
  content?: unknown;
}

export type ChatItem =
  | { kind: 'session'; id: string; model?: string; cwd?: string; version?: string; bridgeOk: boolean }
  | { kind: 'user'; id: string; text: string }
  | { kind: 'assistant'; id: string; text: string; streaming?: boolean }
  | {
      kind: 'tool';
      id: string; // the tool_use id — the key the result arrives under
      name: string;
      input: Record<string, unknown>;
      status: 'pending' | 'ok' | 'error';
      result?: string;
      /** Set when this call belongs to a sub-agent (Task), not the main thread. */
      subagent: boolean;
    }
  | { kind: 'result'; id: string; text: string; denied: string[]; costUsd?: number; turns?: number };

/** Flatten a tool_result's `content` — a string, or an array of blocks. */
function resultText(content: unknown): string {
  if (typeof content === 'string') return content;
  if (Array.isArray(content)) {
    return content
      .map((b) => (typeof b === 'string' ? b : typeof b?.text === 'string' ? b.text : ''))
      .filter(Boolean)
      .join('\n');
  }
  if (content && typeof content === 'object' && typeof (content as any).text === 'string') {
    return (content as any).text;
  }
  return '';
}

/**
 * Fold a stream of events into chat items.
 *
 * Kept as a pure function (events in → items out) so it can be tested without a
 * DOM and re-run over history on reconnect: replay is just calling this again.
 */
export function foldEvents(events: StreamEvent[]): ChatItem[] {
  const items: ChatItem[] = [];
  const toolIndex = new Map<string, number>(); // tool_use id -> index in items
  // Index of the assistant bubble currently being streamed into, or -1.
  //
  // With --include-partial-messages the CLI sends each token as a
  // stream_event/content_block_delta AND then repeats the finished text as a whole
  // `assistant` message. Rendering both would print every answer twice, so the
  // deltas build a bubble in place and the final message replaces its text rather
  // than appending a new one.
  let streamAt = -1;

  for (const ev of events) {
    if (ev.type === 'stream_event') {
      const inner = ev.event;
      if (inner?.type === 'content_block_delta' && inner.delta?.type === 'text_delta') {
        const chunk = inner.delta.text ?? '';
        if (!chunk) continue;
        if (streamAt >= 0 && items[streamAt]?.kind === 'assistant') {
          const cur = items[streamAt] as Extract<ChatItem, { kind: 'assistant' }>;
          items[streamAt] = { ...cur, text: cur.text + chunk };
        } else {
          streamAt = items.length;
          items.push({ kind: 'assistant', id: `s${items.length}`, text: chunk, streaming: true });
        }
      } else if (inner?.type === 'message_stop') {
        streamAt = -1; // the turn's text is done; a later message starts a new bubble
      }
      continue;
    }

    if (ev.type === 'system' && ev.subtype === 'init') {
      // Whether OUR permission bridge is connected decides if approvals are even
      // possible. Without it the CLI denies every gated tool and still reports the
      // turn as a success — so this is worth showing, not hiding.
      const bridgeOk = (ev.mcp_servers ?? []).some((m) => m.name === 'pcd' && m.status === 'connected');
      items.push({
        kind: 'session',
        id: ev.session_id ?? 'init',
        model: ev.model,
        cwd: ev.cwd,
        version: ev.claude_code_version,
        bridgeOk,
      });
      continue;
    }

    if (ev.type === 'assistant' && ev.message?.content) {
      const subagent = !!ev.parent_tool_use_id;
      for (const b of ev.message.content) {
        if (b.type === 'text' && b.text?.trim()) {
          if (streamAt >= 0 && items[streamAt]?.kind === 'assistant') {
            // Same text we just streamed — settle the bubble, don't duplicate it.
            const cur = items[streamAt] as Extract<ChatItem, { kind: 'assistant' }>;
            items[streamAt] = { ...cur, text: b.text, streaming: false };
            streamAt = -1;
          } else {
            items.push({ kind: 'assistant', id: `${items.length}`, text: b.text });
          }
        } else if (b.type === 'tool_use' && b.id) {
          // A tool call ends the text bubble that preceded it.
          streamAt = -1;
          toolIndex.set(b.id, items.length);
          items.push({
            kind: 'tool',
            id: b.id,
            name: b.name ?? '?',
            input: (b.input as Record<string, unknown>) ?? {},
            status: 'pending',
            subagent,
          });
        }
      }
      continue;
    }

    if (ev.type === 'user' && ev.message?.content) {
      for (const b of ev.message.content) {
        if (b.type === 'tool_result' && b.tool_use_id) {
          // Resolve the pending call in place rather than appending — this is the
          // move a terminal can't make.
          const at = toolIndex.get(b.tool_use_id);
          if (at !== undefined && items[at]?.kind === 'tool') {
            const t = items[at] as Extract<ChatItem, { kind: 'tool' }>;
            items[at] = { ...t, status: b.is_error ? 'error' : 'ok', result: resultText(b.content) };
          }
        } else if (b.type === 'text' && b.text?.trim()) {
          items.push({ kind: 'user', id: `${items.length}`, text: b.text });
        }
      }
      continue;
    }

    if (ev.type === 'result') {
      items.push({
        kind: 'result',
        id: `${items.length}`,
        text: ev.result ?? '',
        // A turn ends subtype:"success" even when every tool was blocked — the
        // word describes the turn, not the work. Surface the denials so the UI
        // can't imply something happened when nothing did.
        denied: (ev.permission_denials ?? []).map((d) => d.tool_name),
        costUsd: ev.total_cost_usd,
        turns: ev.num_turns,
      });
    }
    // Everything else (rate_limit_event, stream_event, …) is intentionally ignored.
  }

  return items;
}

/** A one-line summary of a tool call, for the collapsed row. */
export function toolSummary(name: string, input: Record<string, unknown>): string {
  const s = (v: unknown) => (typeof v === 'string' ? v : '');
  switch (name) {
    case 'Bash':
      return s(input.command);
    case 'Read':
    case 'Write':
    case 'Edit':
      return s(input.file_path);
    case 'Glob':
    case 'Grep':
      return s(input.pattern) + (input.path ? ` in ${s(input.path)}` : '');
    case 'Task':
      return s(input.description);
    case 'WebFetch':
      return s(input.url);
    default: {
      const first = Object.values(input).find((v) => typeof v === 'string');
      return typeof first === 'string' ? first : '';
    }
  }
}
