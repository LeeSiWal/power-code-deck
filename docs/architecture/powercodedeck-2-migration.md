# PowerCodeDeck 2.0: runtime-first migration

## Baseline and decision

Inspected upstream `main` at `de39831` (product VERSION `0.6.1`; called 1.x in the product discussion). Work branch: `codex/powercodedeck-2-runtime-foundation`.

Keep the Go module and shipped application intact. Extract cohesive runtime packages behind source-compatible adapters, then build the orchestration application alongside the session application. Do not copy the old services tree or rename every existing route. This first slice is the PTY/session runtime, not a functioning orchestrator.

## Reuse inventory and coupling

| Concern | Existing implementation | Reuse and boundary |
| --- | --- | --- |
| Session/process ownership | `server/services/session_engine{,_internal}.go` | Extract contract and PTY implementation. Preserve attach/detach, restart, exit status, viewer membership and output callback. Processes survive browser detach, but not a server exit; no separate daemon exists. |
| Terminal mechanics | `ring_buffer.go`, `flow_control.go`, `terminal_modes.go`, `terminal_queries.go` | Move with PTY implementation: bounded replay, UTF-8 carry, DEC mode restoration, DECRQM replies and ACK backpressure. These have no database/UI dependency. |
| Launch/platform policy | Helpers in `session_engine_internal.go`, `cli_capabilities.go`; `go-pty`; Windows/WSL install scripts | Keep CLI discovery/version selection, npm bootstrap/login, PATH, locale and Windows shim policy in the legacy service adapter for now. Inject prepared command/args/env into the runtime. go-pty remains the Unix PTY/Windows ConPTY abstraction. WSL uses Linux behavior; scripts are packaging, not a runtime interface. |
| Native agent execution | `native_driver.go`, `claude_driver.go`, `codex_driver.go`, `native_service.go`, `claude_stream.go` | Reuse drivers later through provider adapters. Current normalized events include UI/raw wire shape, and NativeService binds agent IDs, options, approvals and history. Do not make these the new domain model unchanged. |
| WebSocket | `server/ws/{hub,client,message}.go` | Preserve transport and security/write-gate tests. Hub mixes terminals, exclusive viewing, companion shells, file watching, approvals and control-room projections; split transport from application handlers later. Single PTY output handler remains hub-owned until an explicit fan-out adapter exists. |
| Security/handoff | `server/auth`, `server/middleware`, origin config, `services/handoff.go`, approval broker/rules/tokens | Reuse JWT/Host/Origin guards, one-use handoff tokens, approval decisions and path validation. Keep routing/middleware order and wire schemas stable. Browser detach is not permission revocation or process cancellation. |
| Persistence/history | `server/db`, `agent.go`, `session_history.go`, `project_key.go` | Keep current tables and transcript formats. Agent records combine UI metadata and lifecycle, and agent ID often equals session ID. New run/task/execution IDs must be distinct with explicit legacy mappings. |
| Project/Git | `project.go`, `git.go`, `file.go`, `watcher.go` | Reuse discovery, status and guarded file access. GitService polls status; it does not implement worktree scheduling, isolation or merge arbitration. |
| UI/control room | `client/src/pages/{TerminalPage,ControlRoomPage}.tsx`, stores, `services/{control_room,attention,activity}.go` | Keep as the legacy application. Attention and transcript activity are projections, not authoritative task completion. Reuse terminal components as execution detail views later. `client/src/lib/endpoint.ts` is reusable connection plumbing. |

## Module direction

Implemented in this slice:

```text
server/
  internal/runtime/
    session/       # transport-neutral lifecycle contract and DTOs
    pty/           # process/PTY ownership and terminal mechanics
  services/
    session_engine.go           # legacy public type aliases
    session_engine_internal.go  # legacy constructor delegating to runtime
    session_launch.go           # existing CLI/platform launch policy
  ws/, handlers/, auth/, middleware/, db/  # existing application remains live
```

Next packages (planned, not empty scaffolding):

```text
internal/orchestration/     Workspace -> Run -> Task -> Execution; Artifact/Check/Decision
internal/providers/        Claude, Codex; structured events independent of chat UI
internal/workspace/        worktree allocation, file ownership, merge coordination
internal/transport/        versioned API/event adapters
internal/persistence/      additive orchestration records
```

Dependency rule: orchestration consumes runtime/provider contracts; runtime must not import services, HTTP/WebSocket, DB, UI, or orchestration. The legacy app imports runtime through its compatibility surface. A terminal session is an optional execution resource, not a Run.

## First refactor scope and invariants

Move the existing session interface/types to `internal/runtime/session`, PTY code and private mechanics to `internal/runtime/pty`. Use aliases to preserve existing Go call sites and mocks. The service constructor injects the exact existing launch policy. The new runtime requires an explicit launch preparer; it does not silently install or authenticate a model CLI. Preparation errors must leave no process/session behind. Retain requested metadata separately from resolved launch details; restart prepares a fresh launch from the original request.

No database migration, UI/HTTP/WS schema change, default-mode switch, model invocation, or CLI dependency upgrade belongs to this slice. Preserve default replay size, locale/PATH, dimensions, terminal capability replies, ACK semantics, detached execution and all existing security wiring.

## Staged rollout and acceptance gates

1. **Runtime extraction (this branch).** Existing constructor must exercise the new implementation. Move internal unit tests with their owner, retain legacy integration tests, add launch-injection and error-path tests. Gate on Go tests/race checks, production build and Windows/Linux compile checks; report baseline failures separately.
2. **Provider boundary.** Extract provider-neutral events and lifecycle/cancellation contracts, wrap current drivers, preserve native UI events in an adapter. Test recorded/fake CLI protocol streams, approval correlation and stale-process handling before real-model smoke tests. Do not assign providers permanently to architect/coder roles.
3. **Single-task run.** Add Workspace/Run/Task/Execution records with explicit IDs and states; idempotency, restart recovery and approval audit. Add new routes behind an opt-in flag. Execute one task through a provider before implementing a planner. Existing agents and sessions need no destructive conversion.
4. **Isolated scheduling.** Add task dependencies, concurrency/budget limits, isolated worktrees, cancellation and bounded retries. Test dependency failure, conflicting files, stale workers and server restart. A process exit or quiet terminal is not proof of task success.
5. **Review/integration.** Persist test results, artifacts and decisions. Merge only after checks and configured approvals; preserve a reversible branch per run. Cost is reported from supported provider usage, not guessed from terminal text.
6. **Run UI and migration.** Introduce opt-in Run views with terminal/native execution details. Run legacy and new views together until lifecycle, resume, permissions and platform smoke tests pass. Retire compatibility aliases only when no callers depend on them.

Rollback: revert the extraction commit and rebuild using the same configuration and database. This slice makes no durable format changes. Deploy only during a normal restart window: live PTYs are in-process and will not survive replacement of the server binary. Future data migrations must be additive and keep rollback-readable legacy tables; back up before changing schemas.

## Validation notes

See `powercodedeck-2-validation.md` for commands, observed baseline limitations and final results. Cross-compilation is not a Windows/Linux runtime test. Real authenticated Claude/Codex tests and push delivery remain opt-in.
