# Dream Team Mode Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let any native Claude or Codex session (the conductor) call other CLIs (Codex, Claude Sonnet/Haiku) as read-only team members mid-turn through the `pcd` MCP server. A per-session toggle needs no CLI restart, and token guards are built in. agy joins as a copy-paste-only member.

**Architecture:** `pcd mcp-approve` already runs as Claude's stdio MCP server. It gains two tools, `ask_member` and `handoff_to_agy`, which forward over loopback to a new `/internal/native/team` endpoint. A new `TeamService` gates each call on the agent's DB toggle and caps, runs the member through `RunProviders` (read-only, resumable per agent+member), truncates the answer, records usage in `auto_turns`, and returns a JSON result. Codex conductors get the same MCP server through `codex app-server -c mcp_servers.pcd.*`. The client renders the two tools as cards and adds a composer toggle and a Settings panel.

**Tech Stack:** Go (server, `database/sql` + SQLite, gorilla/mux), React + TypeScript (client), Claude Code CLI, codex-cli 0.164.0 app-server.

**Spec:** `docs/superpowers/specs/2026-11-20-dream-team-mode-design.md`

## Global Constraints

- The toggle defaults to **off** per agent. When off, no member process ever starts.
- Default members: `codex` (Codex, account default model), `claude-sonnet` (`claude-sonnet-5-5`), `claude-haiku` (`claude-haiku-5-5`). `claude-fable` (`claude-fable-5-2`) exists but is **disabled by default** because headless Fable bills usage credits.
- agy is **never** spawned by team code. Its card copies text to the clipboard only and never writes into an agy PTY.
- Members always run read-only: mode `"plan"` (Claude plan mode / Codex read-only sandbox). Any approval a member asks for is denied immediately.
- Never invent usage numbers. Codex usage is `null` in results and UI text says "알 수 없음".
- Toggling never restarts the conductor CLI.
- Token secrets never go in process argv.
- UI checks never start real CLIs. Use fake `claude`/`codex` scripts and an isolated server (`env -i HOME=<tmp> PATH=<fake-bin>:/usr/bin:/bin`, own DB, port 33099).
- Build with `cd server && CGO_ENABLED=0 go build ./...` and `cd client && ./node_modules/.bin/tsc --noEmit` (not pnpm). Rebuild the tracked `dist/pcd.exe` (cross-compile) before the final commit.
- Defaults (configurable in Settings): `ResultMaxChars=6000`, `MaxCallsPerTurn=3`, `SessionTokenCap=300000` (input+output+cache_creation of Claude members), `TimeoutSec=180`. The conductor's `MCP_TOOL_TIMEOUT` is `(TimeoutSec+60)*1000`.

## Review Focus

1. Toggle switched off while a member is running: the running call finishes and returns normally, and the *next* call is refused. (Task 3 test `TestAskToggleOffMidCall`.)
2. The user interrupts the conductor while `ask_member` is waiting: the HTTP request context closes and the member process is stopped within 2s. (Task 4 test `TestTeamHandlerCancelStopsMember`.)
3. A stored member resume id is stale (after a server restart or deleted conversation): the call retries once fresh, without resume, and stores the new id. (Task 3 test `TestAskStaleResumeRetriesFresh`.)
4. Bad input: an empty task, an unknown member, a disabled member, or `files` containing absolute paths or `..` leading outside the project. Each is refused with a reason and nothing spawns. (Task 3 test `TestAskRejectsBadInput`.)
5. The conductor calls `ask_member` several times in parallel in one turn: the per-turn cap counts atomically, so exactly `MaxCallsPerTurn` run and the rest are refused with `limit`. (Task 3 test `TestAskParallelCallsRespectTurnCap`.)

---

### Task 1: Storage, settings and toggle API

**Files:**
- Modify: `server/db/migrations.go` (agents ALTER list near line 180)
- Create: `server/services/team_settings.go`
- Modify: `server/services/agent.go` (add accessors next to `NativeOptions`, ~line 630)
- Create: `server/handlers/team.go`
- Modify: `server/main.go` (route registration near line 396)
- Test: `server/services/team_settings_test.go`, `server/handlers/team_test.go`

**Interfaces:**
- Produces:
  - Columns `agents.team_mode INTEGER DEFAULT 0` and `agents.team_sessions TEXT DEFAULT ''` (JSON `{"<memberID>":"<conversationID>"}`).
  - `type TeamMember struct { ID, Adapter, Model, Label string; Enabled bool }` (json camelCase)
  - `type TeamSettings struct { Members []TeamMember; ResultMaxChars, MaxCallsPerTurn, SessionTokenCap, TimeoutSec int }`
  - `func DefaultTeamSettings() TeamSettings`
  - `func LoadTeamSettings(db *sql.DB) TeamSettings` reads `app_config` key `team_settings`. Missing or bad JSON returns defaults. Zero or negative numbers fall back to defaults. Unknown member ids are dropped and missing default members are appended.
  - `func SaveTeamSettings(db *sql.DB, s TeamSettings) error`
  - `func (s *AgentService) TeamMode(id string) bool`, `SetTeamMode(id string, on bool) error`
  - `func (s *AgentService) TeamSessions(id string) map[string]string`, `SetTeamSession(id, memberID, conv string) error`
  - HTTP: `GET /api/agents/{id}/team` returns `{"on":bool}`. `PUT /api/agents/{id}/team` takes `{"on":bool}`. `GET /api/team/settings` and `PUT /api/team/settings` use the TeamSettings JSON.

- [ ] **Step 1: Write failing tests**
  - `TestDefaultTeamSettings`: 4 members. `codex`, `claude-sonnet` and `claude-haiku` are Enabled. `claude-fable` has model `claude-fable-5-2` and Enabled=false. Defaults are 6000/3/300000/180.
  - `TestLoadTeamSettingsFallbacks`: with `{"members":[{"id":"bogus"}],"maxCallsPerTurn":0}` stored, Load returns 4 default members and MaxCallsPerTurn=3.
  - `TestTeamModeRoundTrip`: a new agent reads false. After `SetTeamMode(id,true)` it reads true. After `SetTeamSession(id,"codex","t1")`, `TeamSessions(id)["codex"]=="t1"`.
  - `TestTeamToggleHandler`: PUT `{"on":true}` gives 200, then GET returns `{"on":true}`. An unknown agent gives 404.
- [ ] **Step 2: Run** `cd server && go test ./services/ ./handlers/ -run 'Team'` and confirm the expected FAIL (undefined).
- [ ] **Step 3: Implement** the columns, settings and accessors, the handlers, and the routes. Follow the existing ALTER-list and best-effort accessor style.
- [ ] **Step 4: Run** the same command and confirm PASS.
- [ ] **Step 5: Commit** `feat(team): storage, settings and per-agent toggle`

### Task 2: Resumable, deny-all member executions

**Files:**
- Modify: `server/services/run_providers.go`
- Modify: `server/services/claude_permission.go`
- Test: `server/services/run_providers_test.go`

**Interfaces:**
- Produces:
  - `func (p *RunProviders) NewRoleResume(provider providers.ID, id, cwd, model, effort, mode, resume string) (providers.Execution, error)` passes `ResumeID: resume` to both drivers. `NewRole` becomes a call to it with `resume=""`.
  - `func NewDenyingBroker(message string) *PermissionBroker` resolves every `Ask` immediately with `{Behavior:"deny", Message: message}`, using `SetAskHandler` and resolving the request id.

- [ ] **Step 1: Write failing tests**
  - `TestNewRoleResumePassesResumeID`: use the existing fake-CLI pattern in this test file. For Claude the argv contains `--resume conv-1`. For Codex, `thread/resume` is sent with threadId `conv-1`.
  - `TestDenyingBrokerAnswersImmediately`: `Ask` returns deny with the message within 100ms.
- [ ] **Step 2: Run** `go test ./services/ -run 'NewRoleResume|DenyingBroker'` and confirm FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** it again and confirm PASS. Also run `go test ./services/ -run Delegat` to confirm the Delegator is unaffected.
- [ ] **Step 5: Commit** `feat(team): resumable read-only member executions`

### Task 3: TeamService (gating, caps, run, truncate, record)

**Files:**
- Create: `server/services/team.go`
- Modify: `server/services/auto_usage.go` (add `RecordTeam`)
- Test: `server/services/team_test.go`

**Interfaces:**
- Consumes: Task 1 settings and accessors, Task 2 `NewRoleResume`, `AutoUsage.recordAux`.
- Produces:
  ```go
  type TeamDeps struct {
      DB        *sql.DB
      Agents    *AgentService
      Usage     *AutoUsage
      NewExec   func(provider providers.ID, id, cwd, model, mode, resume string) (providers.Execution, error) // prod: RunProviders.NewRoleResume with effort ""
      Conductor func(agentID string) (adapter, model, cwd string, ok bool) // prod: NativeService session kind/model + agent workdir
  }
  type TeamCall struct { AgentID, Member, Task string; Files []string }
  type TeamResult struct {
      Status    string `json:"status"` // done | refused | limit | failed | timeout | not_installed | not_logged_in | quota
      Member    string `json:"member"`
      Label     string `json:"label"`
      Text      string `json:"text"`
      Truncated bool   `json:"truncated"`
      CallID    string `json:"callId"`
      ElapsedMs int64  `json:"elapsedMs"`
      Tokens    *struct{ In, Out int } `json:"tokens"` // nil = not reported
      Reason    string `json:"reason,omitempty"`
  }
  func NewTeamService(d TeamDeps) *TeamService
  func (t *TeamService) Ask(ctx context.Context, c TeamCall) TeamResult
  func (t *TeamService) ResetTurn(agentID string)
  func (t *TeamService) RaiseCap(agentID string) // session-only: +SessionTokenCap
  func (t *TeamService) FullText(agentID, callID string) (string, bool) // kept 1h
  func TeamBrief(s TeamSettings, conductorAdapter, conductorModel string) string // Korean: enabled members (minus self), when to delegate (big reads/independent checks, not small tasks), members are read-only, keep briefs short, app-only features (image gen, YouTube) → write a paste-ready request instead, agy → handoff_to_agy only
  func (u *AutoUsage) RecordTeam(agentID string, m TeamMember, briefTokens int, usage *providers.Usage) // switch_kind "team"
  ```
- Rules that `Ask` must follow, in order:
  1. Toggle off → `refused`, reason `"드림팀 모드가 꺼져 있습니다. 사용자에게 켜 달라고 하지 말고 직접 처리하세요."`
  2. Empty task, or an unknown or disabled member → `refused`.
  3. Self-exclusion → `refused`. This applies when the member's adapter equals the conductor's adapter and the member's model is `""` or equals the conductor's model. The conductor is read at call time, so "자동" sessions are judged by the current turn.
  4. A file is rejected (`refused`) if it is absolute or resolves outside the conductor's cwd after `filepath.Clean`.
  5. The per-turn counter is checked and incremented under a mutex. At or over `MaxCallsPerTurn` → `limit`. Session tokens at or over the cap → `limit`. The reason includes `used/cap`. Codex members only check the turn cap.
  6. Brief = task, plus `"\n\n참고할 파일(직접 읽으세요):\n- a\n- b"` when files are present, plus `"\n\n답은 핵심만 요약해서 {ResultMaxChars}자 이내로 주세요. 파일을 수정하지 마세요."`
  7. Run with `ctx` limited to `TimeoutSec`, mode `"plan"`, resuming `TeamSessions[member]`. Collect the answer the same way `Delegator.ask` does. On success store `ConversationID()`.
  8. **At most one automatic retry per call.** A failed resumed run (not a timeout) retries fresh (resume `""`). A failed fresh run retries once unless the error is a timeout, login or quota error.
  9. Classify errors. Deadline → `timeout`. Text containing `login`, `authenticat` or `not logged` → `not_logged_in`. Text containing `rate limit`, `usage limit` or `quota` → `quota`. A missing binary → `not_installed`. Anything else → `failed`. On timeout, call `exec.Stop()`, which kills the process group.
  10. Truncate `Text` to `ResultMaxChars` runes and set `Truncated`. Keep the full text for `FullText`. Record usage with `RecordTeam` and add Claude member tokens to the session total.

- [ ] **Step 1: Write failing tests** using a fake `NewExec` that returns scripted executions:
  - `TestAskRefusedWhenOff`, `TestAskRejectsBadInput` (empty task, unknown member, disabled `claude-fable`, `../x`, `/etc/passwd`; the fake factory is never called).
  - `TestAskSelfExclusion`: conductor `claude/claude-sonnet-5-5` with member `claude-sonnet` → refused. With member `claude-haiku` → done. Conductor `codex/""` with member `codex` → refused.
  - `TestAskTruncatesAndKeepsFull`: a 10000-rune answer → `len([]rune(Text))==6000`, `Truncated`, and `FullText` returns 10000 runes.
  - `TestAskParallelCallsRespectTurnCap`: 5 goroutines → exactly 3 `done` and 2 `limit`. After `ResetTurn`, the next call is `done`.
  - `TestAskSessionTokenCap`: with the cap at 100 and a member reporting 150 tokens, the second call is `limit`. After `RaiseCap`, it is `done`.
  - `TestAskStaleResumeRetriesFresh`: the resume run fails and the fresh run succeeds. The factory sees resume `"old"` and then `""`, and the stored session becomes the new id.
  - `TestAskTimeout`: a fake that never finishes with `TimeoutSec=1` → `timeout`, and `Stop` was called.
  - `TestAskToggleOffMidCall`: switch the toggle off while the fake is blocked, then release it. The first call is `done` and the second is `refused`.
  - `TestAskCodexTokensNil`: a Codex member with no usage → `Tokens==nil`, and the `auto_turns` row has NULL input_tokens.
- [ ] **Step 2: Run** `go test ./services/ -run 'TestAsk'` and confirm FAIL.
- [ ] **Step 3: Implement** `team.go` and `RecordTeam`.
- [ ] **Step 4: Run** `go test ./services/ -run 'TestAsk' -race` and confirm PASS.
- [ ] **Step 5: Commit** `feat(team): TeamService with gating, caps and usage`

### Task 4: MCP tools and the loopback endpoint (Claude conductor)

**Files:**
- Modify: `server/cli/mcp_approve.go`
- Create: `server/handlers/native_team.go`
- Modify: `server/handlers/native_approve.go` (auto-allow)
- Modify: `server/services/claude_driver.go` (`mcpConfigJSON`, process env)
- Modify: `server/services/native_service.go` (`Send` calls a reset hook. Add `AppendPrefix`.)
- Modify: `server/main.go` (create the team `RunProviders` with `NewDenyingBroker("읽기 전용 직원은 승인이 필요한 작업을 할 수 없습니다.")`, its own token store, and approve URL `/internal/team/approve` → `NativeApprove(teamBroker, teamTokens)`. **Do this outside the `PCD_V2_ENABLED` block.** Then wire TeamService and the routes.)
- Test: `server/cli/mcp_approve_test.go`, `server/handlers/native_team_test.go`, `server/services/claude_driver_test.go`

**Interfaces:**
- Consumes: Task 3 `TeamService`, `TeamBrief`.
- Produces:
  - Env `PCD_MCP_TOOLS` is a comma list over `approve` and `team`. If unset, it means `approve`, so old spawns behave the same. Claude conductors get `approve,team` plus `PCD_TEAM_URL=<base>/internal/native/team`.
  - MCP tool `ask_member` takes `{member: string, task: string, files?: string[]}`. `member` is a plain string, validated by the server, so a later Settings change needs no respawn. MCP tool `handoff_to_agy` takes `{task: string}`. Keep the descriptions short (≤300 chars each). Measure the `tools/list` byte size in the test and record it in the commit message.
  - `POST /internal/native/team` takes `{sessionId, tool, arguments}` with header `X-PCD-Approve-Token`, checked by the native token store. `ask_member` returns the `TeamResult` JSON. `handoff_to_agy` returns `{"status":"handed_off","text":"사용자에게 agy 카드를 보여 줬습니다. 사용자가 결과를 붙여 넣을 때까지 이 일은 기다리세요."}` when the toggle is on, and `refused` when it is off. The handler passes `r.Context()` into `Ask`.
  - `GET /api/agents/{id}/team/results/{callId}` returns `{"text": full}`. `POST /api/agents/{id}/team/raise` calls `RaiseCap`.
  - `GET /api/agents/{id}/team` now returns `{"on":bool,"ready":bool}`. `ready` is true when the running conductor process was spawned with the team tools (NativeService records this at spawn). Sessions started before this release report `ready:false` until their next restart.
  - On toggle on (PUT from Task 1), the handler calls `nativeSvc.AppendPrefix(id, TeamBrief(...))`. Toggling off adds nothing.
  - `NativeApprove` returns allow without a card for `mcp__pcd__ask_member` and `mcp__pcd__handoff_to_agy`.
  - The Claude conductor process env gets `MCP_TOOL_TIMEOUT=(TimeoutSec+60)*1000`.

- [ ] **Step 1: Write failing tests**
  - `TestMCPToolsListByEnv`: unset → only `approve`. `approve,team` → three tools. `team` → `ask_member` and `handoff_to_agy` only.
  - `TestMCPAskMemberForwards`: a fake HTTP server receives `{tool:"ask_member", arguments:{member:"codex",...}}` with the token header. The MCP reply's `content[0].text` equals the server body.
  - `TestTeamHandlerTokenRequired`: a wrong token gives 403.
  - `TestTeamHandlerCancelStopsMember`: cancelling the request context stops the fake execution within 2s.
  - `TestNativeApproveAutoAllowsTeamTools`: no broker call happens, and the response is allow.
  - `TestClaudeDriverTeamEnv`: the mcp config env contains `PCD_MCP_TOOLS=approve,team` and `PCD_TEAM_URL`. The process env contains `MCP_TOOL_TIMEOUT=240000`. The token is not in argv except inside the `--mcp-config` JSON (existing behavior).
  - `TestSendResetsTeamTurn`: `NativeService.Send` invokes the hook with the session id.
- [ ] **Step 2: Run** `go test ./cli/ ./handlers/ ./services/ -run 'MCP|Team|ClaudeDriverTeam|SendResets'` and confirm FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** the same command and confirm PASS. Then run `CGO_ENABLED=0 go build ./... && go test ./...`.
- [ ] **Step 5: Commit** `feat(team): ask_member/handoff_to_agy MCP tools for Claude conductors`

### Task 5: Codex conductor MCP wiring

**Files:**
- Modify: `server/services/codex_driver.go` (`CodexConfig`, `Start()` ~line 90, `emitSystem` ~line 482, approval handling ~line 404)
- Modify: `server/services/native_service.go` (issue a token for `kind=="codex"` too, and pass it in)
- Test: `server/services/codex_driver_test.go`, plus a live test behind `RUN_CODEX_TEAM_LIVE=1`

**Interfaces:**
- Consumes: Task 4 env names and endpoint.
- Produces:
  - `CodexConfig` gains `SelfPath, TeamURL, TeamToken string`. When all are set, `Start()` prepends `-c mcp_servers.pcd.command="<SelfPath>"`, `-c mcp_servers.pcd.args=["mcp-approve"]`, and `-c mcp_servers.pcd.env_vars=["PCD_MCP_TOOLS","PCD_TEAM_URL","PCD_APPROVE_TOKEN","PCD_APPROVE_SESSION"]`. The values go in the codex **process env**, never in argv. Do this before any `ossbridge` args, so the args don't conflict.
  - First step: confirm `env_vars` forwarding on the installed CLI with `codex app-server -c ... --print-startup-config` (no quota is used). **If 0.164.0 has no env forwarding, stop and report.** Do not fall back to putting the token in argv.
  - Codex conductors get `PCD_MCP_TOOLS=team` (Codex keeps its own approval path through the broker).
  - If Codex asks approval for an MCP tool call (an app-server request whose server is `pcd` and whose tool is `ask_member` or `handoff_to_agy`), answer accept without a card. Any other MCP call goes through the broker as today.
  - `emitSystem` reports `pcd` `connected` only when the MCP server was configured. Otherwise it omits it. (The client's `bridgeOk` check at `client/src/lib/nativeEvents.ts:152` must still work for Claude.)

- [ ] **Step 1: Write failing tests**
  - `TestCodexStartAddsPcdMCP`: with a fake codex, argv contains the three `-c mcp_servers.pcd.*` entries and never the token value. The process env contains `PCD_APPROVE_TOKEN`.
  - `TestCodexStartWithoutTeamConfigUnchanged`: argv is still `app-server --stdio` (plus oss args when the model is `oss:`).
  - `TestCodexAutoAcceptsTeamMCPApproval`: a scripted approval request for `pcd/ask_member` is accepted and the broker is not called.
  - `TestCodexInitReportsRealMCPStatus`.
- [ ] **Step 2: Run** `go test ./services/ -run 'Codex.*(MCP|Team|Init)'` and confirm FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** the tests and confirm PASS. Then run the live check **only after the user agrees** (it uses Codex quota): `RUN_CODEX_TEAM_LIVE=1 go test ./services/ -run TestCodexTeamLive`. It asks Codex to call `ask_member` once with the toggle off and asserts the `refused` result came back through MCP.
- [ ] **Step 5: Commit** `feat(team): pcd MCP server for Codex conductors`

### Task 6: Client (toggle, cards, Settings)

**Files:**
- Modify: `client/src/lib/api.ts` (types and calls)
- Create: `client/src/components/native/TeamCards.tsx`
- Modify: `client/src/components/native/NativeChat.tsx` (render hook next to line 1336, toggle near the composer at ~line 1062)
- Create: `client/src/components/settings/TeamPanel.tsx`, and register it where `AutoUsagePanel` is listed
- Test: `cd client && ./node_modules/.bin/tsc --noEmit` and a headless Playwright check (below)

**Interfaces:**
- Consumes: Task 1, 3 and 4 HTTP/JSON shapes (`TeamResult`, `/agents/{id}/team`, `/team/settings`, `/team/results/{callId}`, `/team/raise`).
- Produces:
  - `api.getTeam(id)`, `api.setTeam(id, on)`, `api.teamSettings()`, `api.saveTeamSettings(s)`, `api.teamResult(id, callId)`, `api.raiseTeamCap(id)`.
  - Composer toggle **👥 드림팀** (on/off pill). Optimistic update with rollback on error. No restart. When `ready:false`, the pill shows a hint "다음 세션 재시작부터 적용".
  - `MemberCard` for tool items named `mcp__pcd__ask_member`. While running it shows the label and a spinner. When done it shows label, elapsed time, tokens (or "알 수 없음") and the summary text. A `전체 보기` button appears when `truncated`. For `limit` it shows a `이번 세션 상한 올리기` button that calls `raiseTeamCap`. For `refused` and failure statuses it shows a muted line with the reason (Korean labels per status).
  - `AgyCard` for `mcp__pcd__handoff_to_agy`. It renders `input.task`, with `📋 복사하고 agy 탭 열기` (clipboard write, then open or create an Antigravity session in the same project **in terminal view**, never native chat) and `결과 붙여넣기 ▸` (a textarea, then send as the next user message prefixed `[agy 결과]\n`).
  - TeamPanel lists members with enable switches, a Fable warning line ("헤드리스 실행 시 사용량 크레딧이 청구됩니다"), and four number inputs for the caps. Availability uses the existing CLI capability data. There is no automatic test call. A `연결 확인` button per member is out of scope for v1 (say so in the PR).
  - The iPhone Duo composer layout must not regress (memory note). The toggle sits in the existing composer toolbar row, not a new row.

- [ ] **Step 1: Add types and calls**, then run `tsc --noEmit` and confirm it passes.
- [ ] **Step 2: Implement** cards, toggle and panel. Run `tsc --noEmit` and confirm it passes.
- [ ] **Step 3: Headless check**: run the isolated server (port 33099) with fake `claude`/`codex` scripts. The fake claude emits a scripted `tool_use mcp__pcd__ask_member` and a `tool_result` carrying a done `TeamResult`, plus a `handoff_to_agy` tool_use. Screenshot at 1280px and at 390px. Expected: both cards render, the toggle flips and GET `/team` reflects it, and the composer row height is unchanged at 390px.
- [ ] **Step 4: Commit** `feat(team): composer toggle, member/agy cards, Settings panel`

### Task 7: Token gate evidence, docs, binary

**Files:**
- Create: `docs/superpowers/specs/2026-11-20-dream-team-mode-evidence.md`
- Modify: `CHANGELOG.md`, `dist/pcd.exe`

- [ ] **Step 1:** Write the eval procedure in the evidence file. It has 3 tasks in this repo (e.g. "summarize how the approval bridge works across cli/, handlers/, services/ and fix one typo"). Run each task once with the toggle off and once with it on, using a Claude Opus conductor. Record the Claude-side usage from `auto_turns` plus the conductor's turn usage. Codex is recorded as call count only.
- [ ] **Step 2:** **Ask the user before running** (it uses real quota). Run it and fill in the table. The gate passes when Claude usage with the toggle on is ≤110% of usage with it off, for the summed tasks. If it fails, record it and stop: no release, and report which defaults to change. Do not edit the numbers.
- [ ] **Step 3:** Add a CHANGELOG entry. Cross-compile `dist/pcd.exe`. Run `cd server && go test ./... && CGO_ENABLED=0 go build ./...` and `cd client && ./node_modules/.bin/tsc --noEmit`.
- [ ] **Step 4: Commit** `docs(team): token gate evidence, changelog, rebuild pcd.exe`
