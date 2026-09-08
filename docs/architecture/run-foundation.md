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
| GET `/api/v2/runs` | Latest 100 Run summaries, newest first. |
| GET `/api/v2/runs/{id}` | Run, workspace, task ID, execution attempts and their checks. |
| POST `/api/v2/runs/{id}/start` | Explicitly dispatch Claude, Codex or Antigravity; returns 202 with execution ID. One active slot. |
| GET/POST `/api/v2/runs/{id}/approvals` | List pending requests for the active execution; decide one with `id` and `behavior` (`allow` or `deny`). Decisions are scoped to that Run and execution. |
| POST `/api/v2/runs/{id}/cancel` | Mark eligible work canceled and cancel the owned active process. |
| GET `/api/v2/runs/{id}/executions/{execution}/artifact?kind=...` | Read a registered text evidence file. The worker rejects workspace artifacts, cross-Run IDs, paths outside its root, symlinks, non-regular files and files over 8 MiB. |

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

`worker.go` executes an explicit start using the selected provider adapter. The
source must be a clean Git repository root with a commit. It allocates a detached
worktree at that HEAD and retains it for review. The first attempt pins that base
commit; retries fail if the source branch has advanced. Source changes are not copied or
stashed. Worktree creation disables checkout hooks; inherited Git environment
overrides are removed from worker Git commands. A worktree isolates repository
changes, not operating-system permissions or access by the model's tools.

`PCD_V2_WORK_ROOT` overrides the artifact/worktree directory; by default it is
`powercodedeck/runs` under the OS user cache directory. Keep this directory outside
source repositories. Each attempt records its workspace path and base commit,
then saves `status.txt` and `changes.patch` after provider completion. The patch
contains tracked changes relative to the base, including provider-created
commits. Untracked files are listed in status and retained in the worktree, not
included in that patch. Evidence files are created exclusively so a provider
cannot pre-place a symlink at an artifact name. Output capture is bounded to 8 MiB.

Worker dispatch sets required checks `diff_check` and `review`. A provider success
runs `git diff --check`, followed by the repository checks declared in
`.powercodedeck/checks.json`, followed by an independent Antigravity execution in
`plan` mode. Every phase records immutable evidence on the active attempt. The
Run becomes succeeded only when all required checks pass. No endpoint lets a
client declare checks passed or work completed.

The optional project manifest is a bounded JSON file committed at the repository
root. Commands are argv arrays and run directly without implicit shell parsing.
The worker limits the manifest to 16 checks, validates each working directory
inside the worktree, gives each check a bounded timeout and log, and supplies a
small environment with an isolated HOME. A check that changes tracked or
non-ignored source files is rejected; ignored build output is allowed.

```json
{
  "version": 1,
  "checks": [
    {
      "name": "server_test",
      "cwd": "server",
      "argv": ["go", "test", "./..."],
      "timeoutSeconds": 600
    },
    {
      "name": "client_build",
      "cwd": "client",
      "argv": ["npm", "run", "build"],
      "timeoutSeconds": 600
    }
  ]
}
```

The reviewer receives a fresh provider execution with Antigravity `plan` mode,
terminal sandboxing, a strict read-only review prompt, and a final-result JSON
schema. It must return one JSON verdict. The worker fingerprints HEAD, tracked
changes and non-ignored untracked files before and after review, so a reviewer
that changes or commits work cannot approve its own mutation.

The authenticated web UI exposes `/runs`: select Claude, Codex or Antigravity,
create and start a Run, answer Claude/Codex permission requests,
watch state updates, retry or cancel eligible work, inspect check evidence, and
open registered patch and log artifacts. It handles the feature-off 404 as an
inactive 2.0 runtime rather than breaking the legacy Control Room.

One worker slot is available, with a 30-minute attempt timeout. Cancel sends
context cancellation through the event reader and calls provider Stop. Server
shutdown stops accepting dispatch and gives worker cleanup five seconds. Abrupt
server death cannot guarantee child-process termination; recovered work is never
automatically retried and old worktrees remain available. Multiple tasks and dependencies, scheduling
budgets, automatic merge, worktree retention/cleanup, execution-history controls
in the UI and approval audit are not implemented.

`services/run_providers.go` is the migration boundary around existing Claude and
Codex drivers. It creates no legacy agent records, resumes no past conversations,
and wraps each driver in the provider-neutral execution adapter. Run approvals
use a separate broker/token store. Claude's MCP bridge calls the token-protected
`/internal/runs/approve`; Codex uses its app-server approval requests with the
default workspace-write/on-request policy. Stop revokes the token and cancels
pending requests. No persistent allow rules or permission bypass are enabled by
the Run integration. All three providers currently use a fresh Antigravity
execution for independent review, so Antigravity must also be installed and
authenticated for Claude/Codex Runs to pass review. Live model/browser validation
is still required; automated tests use local fixtures and fake executions.

Rollback: disable `PCD_V2_ENABLED` and restart. The additive tables remain for
future re-enablement; existing chat data and routes remain unchanged.
