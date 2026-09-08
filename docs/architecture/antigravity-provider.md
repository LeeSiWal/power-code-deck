# Antigravity Provider

PowerCodeDeck 2.0 uses Google Antigravity CLI (`agy`) as the Gemini-family provider.
Google documents Gemini CLI migration to Antigravity and describes `agy` headless
mode as a machine-readable `stream-json` process with `init`, `step_update`, and
`result` events. See the [migration guide](https://www.antigravity.google/docs/cli/gcli-migration/)
and [headless protocol](https://antigravity.google/docs/cli/headless/).

The implementation is in `server/internal/providers/antigravity`. It launches an
explicit `agy` executable, passes prompts as one argument, supports explicit
conversation IDs, model selection, streamed text/tool events, usage metadata,
bounded stderr diagnostics, interruption, and process-failure reporting.

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
adapter. It is not yet wired into `NativeService` or the launcher. The next slice
must map neutral text/tool/outcome events to legacy chat rendering, resolve
provider and workspace from the stored agent, avoid launching a duplicate PTY,
and hide unsupported mode/effort/plugin controls. Antigravity's cumulative usage
must not be exposed as per-turn usage or accumulated again on each follow-up.
