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
After all tasks succeed, task dispatch leaves the Run `awaiting_integration`.
Only a separate successful final integration can mark the Run `succeeded`.

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

## Final integration and completion

`POST /v2/runs/{id}/plan/integrate` explicitly starts final integration, using the
same single active Run slot. All tasks must have succeeded and have retained
result commits. The source must still be clean at the pinned revision, with a
nonempty project check manifest and a configured independent reviewer.

`integration_worker.go` creates a fresh detached worktree and applies every task
result in dependency order, including disconnected terminal branches. Shared
ancestors are applied once. Conflicts preserve the failed worktree and an error
log; there is no automatic resolution or source branch mutation.

The complete diff is checked, every check from the original source manifest is
run again, and a fresh reviewer evaluates the combined changes against the
original Run request. Source mutation by checks/review fails verification. The
original task checks cannot substitute for this final evidence. Success retains
a single combined result with the pinned Run base as its parent under
`refs/powercodedeck/integrations/{attemptId}`, then marks the Run `succeeded`.
This means a verified result is ready; it does not mean it has been applied to
the source branch or deployed.

Additive `v2_integrations`, `v2_integration_requirements`,
`v2_integration_checks` and `v2_integration_artifacts` tables preserve each
attempt and its frozen required checks. Completion requires every required
check and a retained result in that exact attempt. Clients cannot submit
checks or directly complete integrations. Cancellation rejects late evidence;
recovery marks an active integration interrupted and returns the Run to
`awaiting_integration` without launching a CLI. A failed integration leaves
`integration_failed`. Explicit retry creates a new attempt and workspace,
preserving all verified tasks and old integration evidence.

The Run UI exposes integration start/retry, cancellation, all integration
attempts, check results, combined patches, logs and the final result commit.

## Explicit source branch application

`GET /v2/runs/{id}/plan/apply` previews the exact verified integration, current
local branch, pinned base, result commit and diff summary. The UI also links to
the full verified patch. `POST` to the same route requires that exact reviewed
tuple; it accepts no client-supplied repository path or arbitrary result.

`application_worker.go` rechecks the completed integration, retained result ref,
single-parent ancestry and source state immediately before applying. Detached
HEAD, changed branch/revision, ongoing Git operations, uncommitted tracked or
untracked files, dirty submodules and hidden-change index flags are rejected.
Git uses fast-forward only, with autostash and repository hooks disabled and
ignored-file overwrite prohibited. There is no force reset, merge commit or
push. A backup base ref remains under
`refs/powercodedeck/applications/{applicationId}/base`.

The additive `v2_result_applications` journal records intent before source
mutation and distinguishes `applying`, `applied`, `failed`, and
`needs_attention`. Identical retries after success return the original record;
failed attempts remain visible and may be retried after resolving their cause.
A completed Run continues to describe verification, while the application
journal separately describes whether its result was applied.

Git and SQLite do not share a transaction. A timeout or interrupted database
write must not trigger an automatic repeat or rollback of source changes.
Startup marks unfinished applications `needs_attention`. Explicit
`POST /v2/runs/{id}/plan/apply/reconcile` reads the branch and clean worktree,
then updates only the journal: the exact result confirms applied; the exact
original base permits a fresh retry; anything else requires manual inspection.
The same action can inspect an idle application whose final database write
failed without restarting the server.

Application shares the Worker's mutex with execution and integration. This is
a single-server owner design. External editors and Git commands are not covered
by that lock; avoid concurrent Git operations during application. Post-operation
inspection reports uncertain outcomes without reverting possible user edits.
Sparse/hidden-change index setups must be normalized before using this action.

## Request-to-plan drafts and editing

The Runs form now defaults to creating a plan draft from the request. The
existing direct single-task path remains selectable. `PlanEditor.tsx` provides
task add/remove, prompt/provider editing, dependency checkboxes and concurrency
editing. Removing a task also removes references to that task. `계획 확정` uses
the existing validated PUT plan route; it freezes the edited plan without
starting tasks. The saved plan's execution action remains separate.

`POST /v2/runs/{id}/plan/draft/generate` starts an asynchronous planner only for a
queued Run without a saved plan or previous execution. It shares the existing
worker slot, pins a clean source revision and creates a detached inspection
worktree. Production uses Antigravity in plan mode with sandbox enabled and
`PlanJSONSchema`. The preferred implementation provider and configured adapter
names are supplied as planning context; actual CLI installation/authentication
are not established by having a configured adapter.

`planner_worker.go` requires a matching successful provider outcome, rejects
source mutations, strictly decodes bounded JSON, and applies the same task graph
validation as saved plans. Cycles, unknown dependencies/providers, forged state
fields, trailing content and size/concurrency violations fail generation.
A valid draft is a proposal, not verification evidence or task execution.

`v2_plan_drafts` retains generation attempts and validated output. The Run moves
`queued → planning → queued` on either success or failure. The UI reads the
latest attempt from `GET /v2/runs/{id}/plan/draft` and polls during generation,
including after temporary network errors. Generated drafts survive reload;
manual edits remain local until the user confirms the plan. Repeated reads of
the same generated draft do not overwrite in-progress edits. New generation
explicitly replaces the editable draft after success. Generation failure leaves
manual editing available.

Cancel stops the planner and rejects late drafts. Startup marks unfinished
drafts interrupted, returns planning Runs to queued and never restarts a CLI.
Regeneration requires explicit action and the same pinned source revision.
Generated worktrees/history remain retained for later cleanup. Plan mode plus
fingerprinting is not a replacement for OS isolation, and live Antigravity
permission behavior remains an outstanding validation item.

Remaining steps:

1. Add previous Task/draft attempt browsing and richer conflict resolution.
2. Add an explicit undo workflow for applied results if required.
3. Add retained worktree/ref cleanup, plus multi-server leases if deployment
   requires them.
4. Finish authenticated live provider/browser validation. The previous Antigravity
   headless permission denial remains unresolved; fake-provider tests do not
   establish live CLI permissions or browser end-to-end behavior.
