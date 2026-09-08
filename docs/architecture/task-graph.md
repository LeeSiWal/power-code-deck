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

Next integration steps:

1. Add versioned plan/task/dependency storage without replacing current Run tables.
2. Persist a validated plan before dispatch and bind it to the Run's base revision.
3. Claim ready tasks transactionally; give each attempt its own execution identity,
   worktree, evidence and approval scope.
4. Define how verified dependency changes enter a downstream worktree. Retain
   original patches and report conflicts instead of discarding changes.
5. Complete a Run only after terminal verification of the integrated result.
6. Show plans, blocked tasks, active attempts and approvals in the Run UI.

No automatic LLM decomposition, multi-task persistence, parallel CLI dispatch or
patch integration is enabled by this foundation alone.
