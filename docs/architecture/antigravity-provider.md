# Antigravity Provider

PowerCodeDeck 2.0 uses Google Antigravity CLI (`agy`) as the Gemini-family provider.
Google documents Gemini CLI migration to Antigravity and describes `agy` headless
mode as a machine-readable `stream-json` process with `init`, `step_update`, and
`result` events. See the [migration guide](https://www.antigravity.google/docs/cli/gcli-migration/)
and [headless protocol](https://antigravity.google/docs/cli/headless/).

The implementation is in `server/internal/providers/antigravity`. It launches an
explicit `agy` executable, passes prompts as one argument, supports explicit
conversation IDs, model selection, streamed text/tool events, usage metadata,
bounded stderr diagnostics, interruption, process-failure reporting, execution
mode, terminal sandboxing, and an optional final-result JSON schema.

Each process also receives `--add-dir` with its validated absolute working
directory. Merely setting the process cwd did not reliably make disposable
worktrees available to headless file tools in local validation. This explicitly
registers the execution workspace; it is not an exclusive filesystem sandbox or
a command allow-rule. Existing CLI permission policy still applies, including
explicit denials. The adapter does not edit global settings or select another
project implicitly. See the [CLI workspace reference](https://antigravity.google/docs/cli/reference/)
and [permission policy](https://antigravity.google/docs/cli/permissions).

The v2 implementation factory explicitly selects `accept-edits`, because an
authorized implementation Run must be able to edit its disposable worktree.
Planner and reviewer factories retain `plan` plus terminal sandboxing; legacy
chat still inherits its CLI mode. The [execution mode documentation](https://antigravity.google/docs/cli/modes/)
distinguishes file editing from shell permissions: enabling edits does not grant
commands. These modes are not themselves filesystem isolation guarantees.

Structured `result.denied_actions` are preserved in outcome diagnostics even
without stderr. Empty `SUCCESS` with named denials becomes `permission_denied`;
a nonempty answer retains its CLI outcome for independent worker verification.
Process failures and cancellation still take precedence. Older CLI no-output
stderr notices remain supported as a fallback.

Antigravity headless mode is intentionally modeled as a single-turn execution.
The upstream CLI can keep a stream session open, but its documented stdin
protocol accepts text user events only and rejects control request/response
messages. Therefore PowerCodeDeck does not pretend that its application approval
broker controls Antigravity. Approval is governed by Antigravity's configured
policy; a soft-denied tool is retained in stderr diagnostics and does not imply
task success. A later adapter can add a policy bridge if Antigravity exposes a
supported approval protocol.

Every process attempt receives a distinct execution ID. A conversation ID is
provider state and may be reused for an explicit resume; it is never used as the
execution identity or as a task-success signal. A `SUCCESS` result still requires
the future orchestration layer's tests and review checks.

The existing `services` layer continues to own Claude/Codex approval wiring and
legacy UI events. `internal/providers/native` converts those events to the same
provider-neutral contract while preserving the original wire event for the old
chat. Antigravity uses the same contract but a separate parser because its wire
schema is different.

## Sequential conversation runtime

`server/internal/runtime/conversation` now coordinates successive single-turn
executions without depending on the legacy chat wire. A server-owned factory
receives a fresh execution ID and the last explicit conversation ID. Each
`StartTurn` returns a stream whose provider identity, sequence, diagnostics, and
conversation-scoped usage are preserved unchanged.

Only one turn may be active. The caller must consume its terminal outcome before
submitting a follow-up; overlapping submissions return `ErrBusy` rather than
silently queueing or interrupting work. Canceling a `Next` wait does not cancel the
turn. `Interrupt` requests cancellation and retains the terminal outcome; after
consuming that outcome, another turn can resume the conversation. `Stop` closes
the conversation permanently. EOF without a terminal outcome is an error.

Startup failures clean up the attempted execution and retain the saved resume
ID. Failures do not implicitly retry without that ID: the caller must decide
whether a new conversation is appropriate. No history, credentials, approval
policy, or model configuration is copied across providers by this runtime.

This module is validated with subprocess fixtures using the actual Antigravity
adapter. `services/antigravity_chat.go` now connects it to `NativeService` through
an application-owned compatibility envelope. The provider parser and conversation
runtime remain independent of legacy chat events. Canonical events retain their
per-process identities and full usage; legacy result events omit cumulative usage
and unreported cost. Text deltas, tool calls/results, interruptions, failures, and
stderr notices are projected into the existing chat.

The launcher offers preset `antigravity` with command `agy` and no custom argv.
Creation only stores the agent; native open validates the installed CLI and a
prompt starts the process. There is no duplicate interactive PTY. WebSocket open
resolves provider and working directory from the stored agent, and resume IDs
remain server-owned. Antigravity does not inherit Claude/Codex model or policy
settings. Its chat uses the CLI model and permission configuration; Claude-only
mode, effort, options, plugin management and transcript-history controls are not
offered. Concurrent prompts are rejected while a turn is active.

The application now persists the latest 2,000 Antigravity chat events per agent
in the additive `native_history` SQLite table. The opaque JSON journal lives in
`internal/history`; `services/native_history.go` owns compatibility decoding and
replay. Event and explicit resume ID updates commit together. Agent deletion
cascades to journal rows, and providers cannot read each other's event namespace.

Native open restores the journal before publishing the session. An unfinished
saved turn receives one durable "completion unknown" result, preventing a stale
working indicator without asserting that the CLI completed the task. Failed
loads abort opening rather than silently replacing history. Failed writes keep
the live chat available and show one storage warning for the session. Simultaneous
Antigravity opens serialize preparation/replay so they cannot duplicate recovery
markers or publish competing chat adapters.

Records created before this journal was enabled cannot be reconstructed from the
old database, and events beyond the 2,000-event retention window are removed.
Importing older CLI transcripts remains separate future work. The CLI must
already be installed and authenticated on the server host. No installer, login
automation, permission bypass, or browser approval bridge is added.

Validation uses fake subprocesses through the real adapter and NativeService,
including sequential prompts, identity/usage preservation, interruption, user
message ordering, diagnostics, and rejection of unsupported settings. Browser
event-folding tests execute separately from the TypeScript production build.
Live authenticated model execution is not part of these tests.

Manual connection check on 2026-09-08: installed `agy 1.1.27` completed one
short marker-only request in an isolated working directory, returning exit code
0, a `SUCCESS` result, the requested `PCD_AGY_SMOKE_OK` marker, and no stderr.
This verifies basic authenticated headless connectivity; it does not replace
the fake-process cancellation/permission tests or prove an end-to-end browser
workflow against a live coding task.

## Review evidence and structured output — 2026-09-09

The orchestration layer now supplies a server-collected JSON review input:
resolved base commit, task, combined tracked diff and complete non-ignored
untracked text files. Reviewers are instructed to use this evidence and read-only
file tools for more context, without running shell/Git commands. This applies to
single-run, planned-task and integration reviews through their shared boundary.
The caller-selected verification base and worktree fingerprints are retained.

The serialized input is limited to 64 KiB and 256 untracked files. Oversized,
binary, invalid UTF-8, changed submodule and non-regular untracked inputs fail
before launching a reviewer. Nothing is silently truncated. Untracked reads use
an OS-rooted file handle to prevent symlink escapes. The exact JSON is recorded as
`review_input` (`review-input.json`) before provider invocation. Existing review
mutation detection, JSON verdict validation and project checks still gate success.
This bounded text path is not yet a general large-diff or binary review solution.

The review factory omits the optional CLI `--json-schema` hint because observed
agy 1.1.27 responses added presentation fields, concatenated objects, and nested
the verdict inside another summary. Review prompts still request one JSON object;
the server's strict verdict decoder remains authoritative and unchanged. It
rejects extra fields, trailing output, missing/invalid verdicts and empty summaries.
No compatibility normalization of these malformed responses is retained. Planner
configuration and generic provider schema support remain unchanged.
