# Task graph foundation

`server/internal/orchestration/taskgraph` provides a pure, provider-neutral plan
validator and ready-task selector. It is separate from SQL, CLI lifecycle, chat,
and HTTP. The existing single-task Run remains the execution path.

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

This layer reserves work; it does not launch a process. Planned Runs cannot use
the legacy single-task start path. There is no browser route to claim tasks,
submit passing checks or complete a plan.

Remaining integration steps:

1. Bind the plan to the source revision before first execution.
2. Connect reserved attempts to isolated worktrees, provider lifecycles and
   per-attempt approval scopes; add the runtime-wide concurrency limit.
3. Define how verified dependency changes enter a downstream worktree. Retain
   original patches and report conflicts instead of discarding changes.
4. Complete a Run only after terminal verification of the integrated result.
5. Add plan generation/editing and per-attempt evidence/approvals to the Run UI.

Automatic LLM decomposition, parallel CLI dispatch and patch integration are not
enabled by plan persistence alone.
