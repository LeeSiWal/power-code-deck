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
| POST `/api/v2/runs/{id}/start` | Explicitly dispatch one attempt; returns 202 with execution ID. Currently Antigravity only, one active slot. |
| POST `/api/v2/runs/{id}/cancel` | Mark eligible work canceled and cancel the owned active process. |

The worker claims queued/failed/interrupted work transactionally.
Only one caller can claim an attempt. Each retry receives a new execution ID,
and a stale or canceled attempt cannot report a late success. Provider success
moves the task to `awaiting_checks`, not `succeeded`. An explicit Complete call
requires at least one passing check, all configured required checks, and no failed checks on the active attempt.
A failing check fails the Run and allows a new attempt; its evidence remains
attached to the previous execution. Checks cannot be overwritten.

Recovery runs once before routes/workers start. Previously running attempts,
tasks and Runs become interrupted; no retry or CLI spawn occurs. Queued work and
completed evidence remain available. Single-server ownership is assumed; leases
for multiple server processes are not implemented.

`worker.go` now executes an explicit start using the Antigravity adapter. The
source must be a clean Git repository root with a commit. It allocates a detached
worktree at that HEAD and retains it for review. Source changes are not copied or
stashed. Worktree creation disables checkout hooks; inherited Git environment
overrides are removed from worker Git commands. A worktree isolates repository
changes, not operating-system permissions or access by the model's tools.

`PCD_V2_WORK_ROOT` overrides the artifact/worktree directory; by default it is
`powercodedeck/runs` under the OS user cache directory. Keep this directory outside
source repositories. Each attempt records its workspace path and base commit,
then saves `status.txt` and `changes.patch` after provider completion. The patch
contains tracked changes relative to the base, including provider-created
commits. Untracked files are listed in status and retained in the worktree, not
included in that patch. Output capture is bounded to 8 MiB per Git operation.

Worker dispatch sets required checks `diff_check` and `review`. A provider success
runs `git diff --check`; it does not run the project's tests or satisfy review.
The Run therefore stays awaiting checks until independent evidence is recorded.
No endpoint lets a client declare checks passed or work completed. Review/test
execution and Run UI remain the next slice; no automatic merge or cleanup occurs.

One worker slot is available, with a 30-minute attempt timeout. Cancel sends
context cancellation through the event reader and calls provider Stop. Server
shutdown stops accepting dispatch and gives worker cleanup five seconds. Abrupt
server death cannot guarantee child-process termination; recovered work is never
automatically retried and old worktrees remain available. Claude/Codex workers
still require their approval integration; unsupported provider starts return an
error before claiming an attempt. Multiple tasks/dependencies, project-specific
required tests, scheduling budgets, cleanup and approval audit are not implemented.

Rollback: disable `PCD_V2_ENABLED` and restart. The additive tables remain for
future re-enablement; existing chat data and routes remain unchanged.
