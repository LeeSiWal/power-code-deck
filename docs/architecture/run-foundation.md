# Run foundation

The opt-in v2 application now owns Workspace, Run, one Task per Run, Execution
attempts and immutable Check records in separate `v2_*` SQLite tables.
`internal/orchestration` imports no legacy services, PTY, provider driver or HTTP
package. Legacy agents are neither converted nor started by these records.

Set `PCD_V2_ENABLED=1` when starting the server to create the additive tables,
recover interrupted work, and register the following routes under the existing
authenticated API router. Without this flag the new routes are absent.

| Endpoint | Behavior |
| --- | --- |
| POST `/api/v2/runs` | Queue a task with `path` (absolute existing directory), `prompt`, and `provider` (`claude`, `codex`, `antigravity`). Require `Idempotency-Key` (1–128 characters). Same normalized payload/key returns the original Run; reuse with another payload returns 409. |
| GET `/api/v2/runs` | Latest 100 Run IDs, newest first. |
| GET `/api/v2/runs/{id}` | Run, workspace, task ID, execution attempts and their checks. |
| POST `/api/v2/runs/{id}/cancel` | Mark eligible work canceled. No process is attached in this slice. |

The internal worker API claims queued/failed/interrupted work transactionally.
Only one caller can claim an attempt. Each retry receives a new execution ID,
and a stale or canceled attempt cannot report a late success. Provider success
moves the task to `awaiting_checks`, not `succeeded`. An explicit Complete call
requires at least one passing check and no failed checks on the active attempt.
A failing check fails the Run and allows a new attempt; its evidence remains
attached to the previous execution. Checks cannot be overwritten.

Recovery runs once before routes/workers start. Previously running attempts,
tasks and Runs become interrupted; no retry or CLI spawn occurs. Queued work and
completed evidence remain available. Single-server ownership is assumed; leases
for multiple server processes are not implemented.

This is durable task bookkeeping, not yet an executor or planner. There is no
worker consuming the queue, no new Run UI, no worktree allocation, and no API for
clients to assert completion. Required-check plans, artifacts, approval audit,
multiple tasks/dependencies, budgets and execution cancellation wiring are the
next slices. Keep the feature opt-in until the single-task worker can run in an
isolated workspace and close its lifecycle through checks.

Rollback: disable `PCD_V2_ENABLED` and restart. The additive tables remain for
future re-enablement; existing chat data and routes remain unchanged.
