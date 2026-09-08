# Task graph foundation

`server/internal/orchestration/taskgraph` provides a pure, provider-neutral plan
validator and ready-task selector. It is separate from SQL, CLI lifecycle, chat,
and HTTP. Single-task Runs and planned Runs have separate dispatch paths.

Each Task contains an ID, prompt, provider and dependency IDs. Plans allow at
most 64 tasks, 64 KiB per prompt and 256 KiB of prompts in total. Validation
rejects unknown providers, duplicate IDs/dependencies, missing dependencies,
self-dependencies and cycles. Graph construction and selected results own copies
of dependency slices.

Selection takes server-owned task states and a concurrency limit. Pending tasks
become ready only when every dependency has verified success. Both running and
verifying tasks consume slots. Failure/cancellation blocks descendants while
independent branches remain runnable. Selection is deterministic in plan order;
it does not dispatch processes or retry failed tasks. A caller must transactionally
claim tasks before launch. Dependency independence alone does not guarantee that
two tasks may safely edit the same files.

## Durable plan reservations

`plan_store.go` persists plans in additive `v2_task_plans`, `v2_plan_tasks`,
`v2_plan_attempts` and `v2_plan_checks` tables. Existing single-task tables and
execution code remain separate. A queued Run with no attempts can accept one
immutable plan. Identical saves are idempotent; replacement requires a new Run.

Authenticated routes:

- `PUT /api/v2/runs/{id}/plan` accepts `tasks` and `concurrency`, validates the DAG,
  and changes the Run to `planned`. Request bodies are limited to 512 KiB and
  unknown fields or trailing objects are rejected.
- `GET /api/v2/runs/{id}/plan` returns tasks, states, active attempt IDs and the
  current ready/blocked selection. The Run screen displays this snapshot.
- Existing Run cancellation also cancels pending/running/verifying plan tasks.

Example request body:

```json
{
  "concurrency": 2,
  "tasks": [
    {"id": "api", "provider": "codex", "prompt": "Implement API", "dependsOn": []},
    {"id": "ui", "provider": "claude", "prompt": "Implement UI", "dependsOn": []},
    {"id": "integrate", "provider": "antigravity", "prompt": "Integrate and verify", "dependsOn": ["api", "ui"]}
  ]
}
```

`ClaimTasks` takes the SQLite writer lock before selecting ready tasks and stores
fresh attempt IDs in the same transaction. Concurrency is a per-plan limit,
not a global limit across Runs. Tests use separate database connections to check
that concurrent callers cannot duplicate tasks or oversubscribe capacity.

Only internal scheduler methods can claim tasks or record completion/evidence.
Provider success enters `verifying`; independent `tests` and `review` records
must both pass before the task succeeds. A failing check is immutable evidence.
Explicit retry of a failed task keeps old attempts/checks and allocates a fresh
attempt with no reused evidence. Stale results after cancellation/retry are rejected.
After all tasks succeed, the Run enters `awaiting_integration`, never `succeeded`.

Recovery marks active attempts interrupted and their tasks failed, keeping
partial evidence. The plan returns to `planned`; interrupted tasks require
explicit retry. No recovery path starts a CLI or automatically repeats edits.

Persistence alone never launches a process. Planned Runs cannot use the legacy
single-task start path. There is no browser route to claim attempts, submit
passing checks or complete a plan.

## Planned execution

`POST /v2/runs/{id}/plan/start` dispatches through `plan_worker.go`, sharing the
existing Worker's single active Run slot. Within a Run, ready tasks run in
batches bounded by the saved concurrency (including verification). This is a
single-server owner design, not a distributed process lease. All task providers
and an independent reviewer must be connected, and a nonempty project check
manifest is required. Start pins a clean repository's HEAD; subsequent dispatch
rejects a changed source revision. Checks are loaded from that source before
any agent edits, so an agent cannot replace the commands for its own check.

Each attempt receives a detached worktree. Verified ancestor results are applied
once in dependency order with `cherry-pick --no-commit`; conflicting changes fail
preparation before starting a provider. This includes transitive diamond graphs.
The prepared input and result are immutable Git commit objects, with the result
parent explicitly set to the input. `git add --all` captures new non-ignored
files as well as tracked edits/deletions. Hooks and commit signing are not used
for internal snapshots. Successful results are retained under
`refs/powercodedeck/attempts/{attemptId}`; no source branch or source index moves.
Worktrees and Git refs remain for inspection; automatic cleanup is not implemented.

Provider completion is followed by diff validation, every configured project
check, and a fresh review. Check/reviewer source mutations are rejected. Only
then can a result be used by dependents. `v2_plan_artifacts` keeps workspace
metadata, incremental patches, input/result commits and verification logs.
The existing artifact endpoint validates Run/attempt ownership and file bounds.
The plan snapshot exposes current attempt details, check results and artifact
metadata. The Run UI can start a saved plan, prepare a failed task for retry,
read evidence, and resolve approvals for the Run's currently running attempts.

`POST /v2/runs/{id}/plan/tasks/{task}/retry` only resets a failed task to pending;
it neither changes the frozen plan nor supplies verification evidence. Stopped
schedulers leave the Run `planned` unless canceled or awaiting integration.
Retrying a conflict repeats preparation in a fresh worktree and preserves the
old one; it does not automatically resolve the conflict. Cancel stops active
providers and rejects late results. Runtime worktrees are not OS sandboxes.

Remaining integration steps:

1. Combine all terminal branch results in a dedicated integration worktree and
   run final checks/review before allowing Run completion. Sibling branches with
   no common downstream task have not yet been combined.
2. Add automatic plan generation and plan creation/editing before saving in the UI.
3. Add prior-attempt browsing, conflict resolution and explicit retained worktree/
   ref cleanup, plus multi-server leases if deployment requires them.
4. Finish authenticated live provider/browser validation. The previous Antigravity
   headless permission denial remains unresolved; fake-provider tests do not
   establish live CLI permissions or browser end-to-end behavior.
