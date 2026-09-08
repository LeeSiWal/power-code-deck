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
