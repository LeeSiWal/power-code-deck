# Runtime extraction validation

## Live automatic planning — 2026-09-09

`TestAntigravityGeneratedPlanLive` passed in **200.60 seconds** against installed
agy 1.1.28. A natural-language request alone produced the plan; the generated
plan drove execution, per-task review and final integration with no manual
editing or frozen plan. This is the coverage `TestAntigravityPlanIntegrationLive`
skipped by starting from a frozen plan.

- The planner chose two tasks, `task_2` depending on `task_1`, and concurrency 1.
- Both tasks passed project checks and independent review.
- Integration passed `diff_check`, `project_test` and a separate independent
  review; the Run reached `succeeded` and the retained commit contents matched
  the request exactly.
- Source HEAD, clean status and original contents were unchanged.

Three defects were found by running it, each reproduced before being fixed:

1. Planning was impossible in headless mode. The planner prompt told the model to
   inspect the repository itself, so agy requested the `command` permission that
   headless mode cannot prompt for and auto-denied. Two runs failed identically
   at 23.94 s and 24.22 s. The planner now receives host-collected evidence, the
   way review already did, and is told to call no tools at all. Unlike the review
   prompt it gets no read-only tool fallback, because headless auto-denial has
   also been observed for `read_file`.
2. `--json-schema` made the CLI wrap the response in its structured-output tool
   and leak `toolAction` and `toolSummary` presentation fields, which the strict
   decoder correctly rejected. The plan content itself was already correct. The
   optional schema hint is now omitted for planning, matching the earlier review
   fix; the decoder is unchanged and new cases keep rejecting the observed shape.
   `PlanJSONSchema` stays declared and unused, as `ReviewJSONSchema` already is.
3. A rejected draft discarded what the CLI actually returned, leaving
   `unknown field "toolAction"` as the only detail an operator would see. The
   failure now retains a bounded 2 KiB copy of the planner output.

Plan evidence carries the complete tracked listing plus file contents chosen
smallest first, within 64 KiB serialized, 2000 files and 8 KiB per file. A plan
must remain possible for a repository larger than the budget, so contents are
best-effort while `omitted_contents` names every excluded file and why; nothing
is truncated silently. Symlinks are never followed and are recorded as omitted.
Repositories above the file limit, with no tracked files, or whose listing alone
exceeds the budget are rejected explicitly. A unit test caught a real budget
defect during implementation: omission reasons are part of the payload and were
not reserved, so a 201-file repository serialized past 64 KiB.

Validation passed: `go test -race ./...`, `go vet ./...`, targeted plan evidence,
decoder and planner tests, and `git diff --check`. UI code is unchanged, so no
browser build or browser smoke is claimed. `dist/pcd.exe` was not rebuilt, which
matches every earlier commit on this branch; it still reflects main.

Limits: one request shape on a disposable two-file repository, concurrency 1, and
one CLI version. Automatic planning against a large real repository, parallel
branches, conflict repair and application of results are still unproven live, and
planner output remains dependent on CLI response shape.

## Live dependency plan and final integration — 2026-09-09

`TestAntigravityPlanIntegrationLive` passed in **136.69 seconds** against the
installed/authenticated Antigravity CLI. Two implementation executions and three
fresh review executions ran against a disposable Git repository. The test uses a
frozen two-task dependency plan, not generated planning output.

- The first task wrote a fresh random marker. Before the second provider started,
  the host verified that its worktree already contained that marker.
- The second task read the dependency output and created a derived file. Both
  tasks passed project checks and independent review; their review inputs were
  retained, and the second task patch did not repeat the first task's edit.
- The Run reached `awaiting_integration`, then a separate integration passed
  `diff_check`, `project_test`, and a new independent review, reaching `succeeded`.
- The retained integration ref resolved to the recorded commit. Both committed
  file contents matched exactly, the combined patch was available, and integration
  review input was retained.
- Source HEAD, clean status and original contents remained unchanged. The test
  does not apply or publish the result.

Validation also passed: `TMPDIR=/private/tmp go test -race ./...`, `go vet ./...`
and `git diff --check`. The normal suite exercises negative helper cases without
model usage. Production code and UI were unchanged in this validation step.

Next live coverage: natural-language automatic planning, followed by planned
execution/integration. Parallel branches, conflict repair and explicit result
application remain separate scenarios; this run used concurrency one.


## Server-collected review evidence — 2026-09-09

Authenticated `TestAntigravityRunLive` **passed in 26.55 seconds** with installed
agy 1.1.27. The real implementation changed only the disposable tracked file;
`diff_check`, the project test checking exact marker contents, and independent
review all passed. The Run reached `succeeded`; the verified patch contained the
marker and the original source file remained unchanged. No global permission
settings or blanket command approvals were added.

The shared review boundary now persists and transmits a complete bounded JSON
input rather than asking the reviewer to run Git. Tests use real repositories to
cover combined staged/unstaged changes, new staged files, untracked files with
unusual names, deletions, input persistence/transmission, and record-write failure.
Binary, oversized (including JSON escaping), untracked symlink, changed submodule
and invalid-base inputs reject before provider startup. Existing reviewer mutation
and project-check gates remain in place.

Live attempts exposed unreliable CLI schema output: presentation fields, duplicate
JSON objects and nested verdict text; one attempt also hit a read permission
failure. These attempts correctly failed. The final solution omits the optional
CLI schema hint for review only, requests one JSON object, and retains the server's
unchanged strict decoder. No extra-field or duplicate-response normalization is
shipped. New strict-decoder cases retain rejection of these observed shapes.

Validation passed: `TMPDIR=/private/tmp go test -race ./...`, the targeted final
strict-decoder cases, `go vet ./...`, and `git diff --check`. UI code is unchanged;
no browser build or new browser smoke is claimed for this backend change.

The input limit is 64 KiB serialized JSON and 256 untracked files. Binary,
submodule and larger reviews remain unsupported and fail explicitly; they need a
separate evidence delivery design. A simple live Run passing does not establish
live multi-task planning, conflict resolution or application of results. Next:
validate a multi-file dependency plan through independent review and integration,
then expose the saved review input in the Run interface where useful.


## Antigravity implementation mode — 2026-09-08

V2 implementation executions now request `accept-edits`; planning, review and
legacy chat configuration are unchanged. The live fixture explicitly asks for
file tools rather than commands for its single-file edit; host project checks
and the independent review procedure are not weakened.

Authenticated production Worker validation reached `diff_check` and
`project_test`, both passing with the exact required file contents. Independent
review failed on headless `command` permission after 33.13 seconds total, so the
Run correctly remained failed. End-to-end success is still unverified.

The adapter now also interprets observed structured `denied_actions`, preserving
their names without relying on stderr. Subprocess tests cover empty denial,
nonempty recovery, and a subsequent process crash with both diagnostics retained.
The live test checks preservation of the original file on failed runs too.

`TMPDIR=/private/tmp go test -race ./...`, `go vet ./...` and `git diff --check`
passed. The final live-test cleanup change was compiled with live execution
disabled; the authenticated result above precedes that cleanup-only change.

The installed CLI help exposes no per-invocation command allow-list flag. Its
documented rules live in global settings and distinguish literal prefixes from
`regex:` matching. No blanket Git allow-rule or global settings change was made:
arbitrary Git options and hooks must not be assumed harmless. The next review
integration should provide host-collected bounded diff evidence, or use a
supported execution-scoped approval API when available, while retaining source
fingerprints and mandatory tests. Do not advertise `plan` as OS-enforced read-only.

## Antigravity workspace and permission follow-up — 2026-09-08

The adapter now passes its validated absolute cwd as `--add-dir`, including for
resumed executions. The subprocess regression fixture uses a directory with
spaces and verifies the exact workspace argument, cwd, explicit resume, sandbox
flag and absence of a permission bypass together. Existing execution modes and
global CLI configuration are unchanged.

Validation with installed/authenticated `agy 1.1.27` in a disposable repository:

- Explicit workspace + default policy: reading `tracked.txt` succeeded and
  returned its original contents with no stderr.
- Production `TestAntigravityRunLive` with the new workspace argument: failed
  after 22.47 seconds on headless `command` permission, correctly surfaced as
  `permission_denied`. Implementation/test/review success is **not established**.
- File-tool-only write under default policy: `write_file` was denied; no output
  file was created. The CLI returned empty `SUCCESS`, stderr denial and a
  structured `denied_actions` list, confirming that exit zero alone is unsafe.
- The same disposable file-tool-only probe with explicit `--mode accept-edits`
  succeeded: exact `PCD_AGY_FILE_OK\n` contents, `DONE` response, no stderr, and
  the original tracked file unchanged. This mode was used only for the probe,
  not made the production default. The probe file was then removed.

`TMPDIR=/private/tmp go test -race ./...`, `go vet ./...` and `git diff --check`
passed. UI sources were unchanged. No global permission rules were written.

Next: explicitly configure implementation editing mode and narrowly scoped
read-only Git command permissions for live review, then rerun the production
worker test through all gates. Workspace registration alone does not grant
command permissions or prove isolation from every other configured workspace.
The adapter's empty-success denial fallback currently recognizes the observed
stderr notice; decoding structured `denied_actions` remains a separate follow-up.

Host: macOS arm64, Go 1.26.1. Baseline: `de39831` (VERSION 0.6.1).

## Baseline observations

- A fresh clone has no `server/static`, so `go test ./...` initially fails the root package's embed directive. Build the client and copy its dist first, as the existing Makefile expects.
- `pnpm install --frozen-lockfile` succeeds, but `pnpm build` fails because the client imports `@wterm/core` without declaring it directly. This is an existing dependency/lockfile issue; no frontend dependency files were changed. `npm ci --ignore-scripts --no-audit --no-fund` using the checked-in npm lockfile followed by `npm run build` passes. The two lockfiles resolve different versions (observed Vite 6.4.1 vs 6.4.3); standardizing them is separate work.
- Default macOS temp paths cause the pre-existing `TestIsSensitivePath` failure: the candidate is symlink-canonicalized but the fake home used by the test is not. Using `TMPDIR=/private/tmp` passes the baseline service, WebSocket, handler, auth, middleware, config, DB and CLI suites. This does not fix path canonicalization; track that as an existing security-path correctness issue. The tests themselves were not weakened or skipped.

## Final results

| Check | Result |
| --- | --- |
| Existing UI type check + Vite production build using npm lockfile | PASS; existing eval/chunk-size/mixed import warnings |
| `TMPDIR=/private/tmp go test -count=1 ./...` | PASS across all packages |
| `TMPDIR=/private/tmp go test -race -count=1 ./...` | PASS across all packages |
| macOS arm64 server build with actual embedded UI | PASS |
| Built binary `version` | `pcd v0.6.1` |
| Windows amd64, CGO disabled, full server cross-build | PASS |
| Linux amd64, CGO disabled, full server cross-build | PASS |
| `go list -deps ./internal/runtime/...` | Only the new session/runtime packages from this module; no services/handlers/ws/db imports |
| Launch helpers and terminal helper source comparison | Existing bodies preserved; package names and gofmt comment/spacing changes only |

## Regression coverage

Retained the legacy constructor integration tests for Create/Write/output/replay,
last-viewer detach, Kill/Restart, natural exit, ring capacity and restoring terminal
modes after buffer eviction. Existing locale, launch/bootstrap, HTTP and WebSocket
write-gate/native-identity/attach tests still run through the compatibility layer.

Moved flow-control, terminal-mode, terminal-query and split UTF-8 tests with their
private implementation. New independent runtime tests import no services package:

- A logical provider command resolves to a real shell through the launch preparer;
  argument boundaries, environment and working directory reach the process.
- Metadata retains the requested command and args, not the resolved shell.
- The session runs while detached, and replay catches subsequent output.
- Restart calls the preparer again and starts the next invocation.
- Missing preparer, preparer error and missing executable leave no registered
  session. Wrapped errors preserve `errors.Is`.

## Reproduce

From the repository root (Go toolchain and npm available):

```sh
cd client
npm ci --ignore-scripts --no-audit --no-fund
npm run build
mkdir -p ../server/static
cp -R dist/. ../server/static/
cd ../server
TMPDIR=/private/tmp go test -count=1 ./...
TMPDIR=/private/tmp go test -race -count=1 ./...
CGO_ENABLED=0 go build -o /tmp/pcd-runtime-refactor .
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -o /tmp/pcd-runtime-refactor.exe .
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o /tmp/pcd-runtime-refactor-linux .
go list -deps ./internal/runtime/...
```

`TMPDIR=/private/tmp` is the macOS baseline workaround; use a real canonical temp
directory on other operating systems. Generated assets/binaries are not committed.
No deployed server or existing database was used or modified.

## Limits and next checks

Cross-builds prove compilation, not Windows ConPTY/Linux runtime behavior.
Browser-driven mobile handoff and real authenticated Claude/Codex live tests were
not run. Real-model tests remain opt-in and no model CLI was installed or invoked
for an agent turn. Before release, run platform PTY smoke tests and a browser
reconnect/handoff check. Before provider extraction, define provider-neutral events
and execution IDs, then test fake/recorded protocol streams and permission paths.

The runtime retains existing limitations: process lifetime is bound to the server,
one output callback needs explicit fan-out for multiple consumers, and callers must
serialize lifecycle operations for the same session ID (duplicate Create and
concurrent Restart/Kill are not made transactional by this extraction).

## Planned Task execution — 2026-09-08

Added a separate plan worker with per-task worktrees and provider lifecycles,
incremental dependency snapshots, mandatory project checks and independent
review. Existing single-task dispatch shares only its Run slot and reusable
review/artifact helpers. New persistence is additive.

Validation passed: `go test -race ./...`, `go vet ./...` and `npm run build`.
Fake-provider tests use real temporary Git repositories and check subprocesses:

- A diamond dependency graph transfers newly created files and applies shared
  ancestors once. Result refs survive as reachable objects, while source HEAD,
  index and working files remain unchanged.
- Conflicting branches fail before dependent provider execution; explicit retry
  allocates a new attempt. Failed/mutating checks block dependents.
- Review mutations cannot publish successful results. A reviewer and project
  checks are required before reserving execution.
- Cancellation stops the provider and rejects late results/artifacts. Single-task
  and planned execution cannot occupy the same worker simultaneously.
- Approval listing includes concurrent tasks from the selected Run only;
  foreign, finished and canceled attempts cannot be approved in these cases.

The frontend build retains existing eruda eval, mixed-import and chunk-size
warnings. No authenticated live provider or browser E2E run was performed in
this slice. The earlier Antigravity headless permission issue remains open.
Final integration/verification of terminal branch results is still required;
all verified tasks move the parent Run only to `awaiting_integration`.

## Final Run integration — 2026-09-08

Added a separate integration worker/store and an explicit Run UI action. All
verified task results are combined in a new worktree. Original project checks
and an independent review run against that complete result. Only the current
integration's checks and retained result can mark the Run complete; the source
branch remains unchanged.

Automated regression cases cover disconnected branches, a diamond graph with
one shared ancestor, source HEAD/index preservation, retained result refs,
cross-Run artifact isolation, conflicting branches, combined-only check
failure, failed/mutating final review, cancellation during review, duplicate
dispatch, missing required evidence, immutable checks, changed source revision,
explicit retries and restart recovery without inherited evidence.

Passed: `go test -race ./...`, `go vet ./...`, `npm run build` and
`git diff --check`. Existing frontend dependency/chunk warnings remain.

Live provider permissions and browser end-to-end behavior remain unverified in
this slice. Applying a verified result to the user's source branch, conflict
resolution and cleanup remain separate work.

## Explicit branch application — 2026-09-08

Added preview/confirm application of the exact verified integration, an additive
application journal and read-only reconciliation after uncertain outcomes.
Tests use real throwaway Git repositories; no user's project was targeted by
the application feature during validation.

Coverage includes successful fast-forward, retained base, disabled merge/ref
hooks, idempotent repeated requests, foreign Run rejection, stale payload,
branch/HEAD changes, detached HEAD, ongoing merge, dirty/staged/untracked files,
assume-unchanged/skip-worktree flags, tampered result refs, ignored-file collision,
strict request decoding, and restart before/after Git mutation without replay.

Passed: server-wide `go test -race ./...`, targeted application regression tests
after the final hook change, `go vet ./...`, `npm run build`, and
`git diff --check`. Existing frontend dependency/chunk warnings remain.

The feature does not coordinate with external Git processes or multiple server
owners. No authenticated live provider or browser E2E check was run here.
Automatic plan generation/editing, conflict resolution and retained artifact
cleanup remain open.

## Request-to-plan generation and editor — 2026-09-08

Added an Antigravity plan-mode draft generator, durable generation attempts,
bounded strict plan validation, isolated repository inspection, and a separate
editable plan component. The Runs form supports plan-first and existing direct
execution. Finalizing a plan does not dispatch implementation tasks.

Server tests cover draft generation without execution/freeze, editing before
save, source isolation, invalid JSON/graphs/provider selection, forged state,
source mutation, identity mismatch, cancellation, shared worker capacity,
restart recovery, and pinned/dirty source behavior. Full server race tests pass.
`go vet ./...`, `npm run build`, and `git diff --check` also pass; existing
frontend dependency/chunk warnings remain.

No authenticated Antigravity planning call or browser E2E was run in this slice.
Existing Antigravity permission failures are not bypassed. Manual planning is
available when generation fails; CLI/provider availability and live planning
quality remain to be checked. Local manual edits are not persisted until plan
confirmation; generated drafts are persisted separately.

## Task history and conflict evidence — 2026-09-08

Added scoped history pagination, captured Git conflict stages and a UI for
historical checks/logs and conflict comparison. A resolution action prepares a
new request including the saved task plan; it does not mutate the failed Run.

Regression coverage includes twelve retries paged without missing/duplicate
attempts, invalid/foreign cursors, hidden host paths, dependency and integration
conflict capture, read isolation across Runs, immutable snapshots after manual
workspace edits and retry, tab/newline filenames, binary data, large blobs and
deleted incoming stages. Existing records without stage snapshots remain valid.

Passed: server-wide `go test -race ./...`, targeted history/conflict tests after
the final assertions, `go vet ./...`, `npm run build`, and `git diff --check`.
Existing frontend dependency/chunk warnings remain; no browser E2E was run.

Direct conflict editing/resume, draft history browsing, cleanup and authenticated
provider/browser validation remain separate work. This slice does not establish
that a newly generated follow-up plan will resolve a real-world conflict.

## Final integration text repair — 2026-09-08

Added explicit conflict text editing/deletion and a bounded resolution recipe.
Each submission creates a fresh integration attempt from the pinned source,
replays all Task results and earlier resolutions, and reruns checks and review.
Original failed attempts remain available. Only complete regular-text conflict
evidence from the latest failed final integration can seed a repair.

Regression tests exercise two consecutive conflicts followed by a later Task,
retained failure evidence, stale/foreign attempts, forged fingerprints, invalid
paths/text/size/deletion input, symlink target rejection, explicit deletion,
combined project check failure, independent review rejection and cancellation
with late-result rejection. Tests use real temporary Git repositories and fake
providers; they do not establish live provider permissions or UI behavior.

Validation: server `go test -race ./...`, `go vet ./...`, client `npm run build`
and `git diff --check`. Existing eruda, mixed import and bundle-size build
warnings remain. Browser E2E and authenticated provider validation remain
pending, including the previous Antigravity headless permission denial.
Task dependency repair, draft history browsing and retained workspace cleanup
remain separate work.

## Plan generation history — 2026-09-08

Added a read-only paginated draft history endpoint and Runs panel. Existing
generation records need no conversion; an additive index supports scoped paging.
Generated plans remain separate from edits made before confirmation.

Coverage includes empty/missing Runs, more than one page, new generation between
page reads, invalid and foreign cursors, success/failure payloads, hidden workspace
fields, active/canceled/interrupted records, rejected late completion, and preserved
draft content after plan editing and confirmation. HTTP tests check empty JSON
arrays and 400/404 boundaries. The UI ignores obsolete requests and supports
explicit refresh and retry after fetch errors.

Validation: targeted draft history tests and full server `go test -race ./...`,
`go vet ./...`, client `npm run build`, and `git diff --check`. Existing frontend
dependency/import/chunk warnings remain. No browser E2E or live provider call
was run. Task dependency repair, workspace cleanup and live validation remain.

## Task dependency text repair — 2026-09-08

Extended explicit resolution to the current failed Task's dependency input.
The shared editor posts a bounded recipe; atomic reservation preserves the old
attempt and the provider runs anew only after input reconstruction. Repaired
attempts review and diff-check the full result against the original Run base,
while retained result commits stay incremental against their resolved input.

Tests cover repaired input reaching implementation, complete repair/implementation
diff evidence, incremental parentage, retained failures, later final integration
with explicit resolution, stale/foreign Task attempts, invalid fingerprints and
paths, intervening normal retries, independent review failure, cancellation and
late success, shared capacity, and chained dependency conflicts before provider
execution. Existing final integration repair tests also pass.

After binding recipes to the specifically reserved attempt ID, targeted Task
repair/capacity tests and static checks were rerun successfully.

Validation: targeted repair tests, full server `go test -race ./...`, `go vet ./...`,
client `npm run build`, and `git diff --check`. Existing frontend build warnings
remain. Tests use temporary Git repositories and fake providers. Browser E2E and
authenticated Antigravity validation remain outstanding.

Task-local resolutions are not automatically reused by later Tasks or final
integration. Those stages may need a new explicit resolution. Retained workspace
cleanup is the next structural step; optional undo and multi-server ownership are
separate extensions.

## Explicit retained workspace cleanup — 2026-09-08

Added an owned-workspace registry preview and fingerprint-bound, per-workspace
cleanup. The UI requires a concrete selection and confirmation. The server keeps
evidence files and pins input/result commits before removing only the worktree.
No actual user Run workspace was deleted while implementing this feature.

Tests remove Task and integration workspaces in temporary repositories, preserve
their patch evidence, run final integration after Task cleanup, run Git GC and
then apply the reviewed result successfully. Other coverage includes draft
preservation, conflict exclusion, changed/forged previews, foreign attempts,
path traversal, manual edits, opposing staged/local edits, ignored and untracked
files, hidden index flags, locked worktrees, symlink replacement and active-worker
exclusion. Missing workspaces remain visible as unavailable metadata.

Validation: targeted cleanup tests, full server `go test -race ./...`,
`go vet ./...`, client `npm run build`, and `git diff --check`. Existing frontend
dependency/import/chunk warnings remain. Browser E2E and authenticated provider
validation are still outstanding. Evidence/ref expiry, dirty-workspace removal,
cleanup of interrupted Git metadata, and multiple server owners are not covered
by this feature.

## Browser smoke and real Antigravity denial — 2026-09-08

Used the embedded production UI on a loopback-only server with a separate SQLite
database, work root and Git fixture. Existing user repositories, databases and
CLI permission settings were not changed. Browser interactions used the actual
UI and server, not mocked API responses.

Observed browser results:

- Opened the existing home/control views and navigated to Runs.
- Created a Run and observed planning state, disabled editing and draft history.
- After live generation failed, edited two manual Tasks, set a dependency and
  confirmed the plan. Refreshing history preserved unsaved edits. A subsequent
  direct navigation/reload preserved the frozen plan and dependency.
- Attempting execution without a project check manifest was rejected before
  provider execution.
- Loaded cleanup candidates, selected the confirmation step and canceled it.
  No workspace was deleted through the browser during this check.

Two defects found and fixed:

1. Direct `/runs/...` navigation used FileServer with a rewritten `/index.html`
   path. Its redirect to `./` caused a loop. `handlers.AppShell` now serves the
   shell directly without rewriting the request. GET/HEAD, deep links, cache
   headers and missing-shell errors have regression coverage. Direct navigation
   and reload were confirmed in the browser after rebuilding.
2. Installed `agy --version` reported 1.1.27. The opt-in real Worker test failed
   because `read_file` permission was auto-denied in headless mode. The CLI still
   reported an empty successful result and exit zero; the project check caught
   the missing edit. The adapter now classifies the explicit no-output/headless
   auto-denial diagnostic as `permission_denied` when the final response is empty.
   Empty responses without that diagnostic and normal successful responses keep
   their existing semantics. CLI diagnostics remain available. No permission
   bypass or automatic allow rule was introduced.

The initial browser planning attempt exposed the same issue as `invalid plan
draft: EOF`. A second real browser planning attempt with the fixed adapter showed
the permission-denial explanation and retained a failed draft, with manual
editing available. The source fixture remained clean.

Passed: full server `go test -race ./...`, targeted adapter/handler regression
tests, `go vet ./...`, production server build and `npm run build`. Existing
frontend build warnings remain. A complete authenticated implementation/review
success path is still blocked by the configured CLI permissions. Browser conflict
repair, final integration/application, destructive cleanup and mobile handoff
still need dedicated end-to-end checks; earlier temporary-repository Go tests do
not substitute for those browser checks. Verification server processes were
stopped afterward; scratch fixtures and logs remain outside the repository.
