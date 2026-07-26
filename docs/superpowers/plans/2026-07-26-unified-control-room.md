# 통합 관제실 + 계층 네비게이션 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 대시보드를 컨트롤 룸으로 흡수해 에이전트 화면을 하나로 만들고, 뒤로 가기를 계층 기반으로 바꾸며, AskUserQuestion에 승인 카드가 뜨는 버그를 없앤다.

**Architecture:** 서버 변경 두 건(승인 필터, 접속 시 메타 폴링)을 먼저 독립적으로 끝낸다. 그다음 `ControlRoomPage.tsx`(513줄)를 동작 변경 없이 컴포넌트로 분할하고, 분할된 작은 파일 위에서 기능 변경을 얹는다. 마지막에 라우팅·네비게이션을 정리하고 죽은 파일을 지운다.

**Tech Stack:** Go (net/http, gorilla/websocket) · React 18 + TypeScript 5.7 · react-router-dom 6 · Zustand 4 · Tailwind 3 · Vite 6

## Global Constraints

- **클라이언트에 테스트 프레임워크가 없다.** `client/package.json`에 vitest/jest가 없고 `client/src`에 테스트 파일이 0개다. 클라이언트 작업의 자동 검증은 `cd client && ./node_modules/.bin/tsc --noEmit`가 전부이며, 나머지는 명시된 수동 확인으로 대체한다. **이 계획에서 vitest를 새로 도입하지 않는다** (스펙의 비목표 범위 밖).
- **서버는 테스트가 있다.** `server/handlers/native_approve_test.go`, `server/ws/hub_test.go` 등. 서버 작업은 실제 TDD로 진행한다.
- 빌드 명령: `cd server && CGO_ENABLED=0 go build ./...` / `cd client && ./node_modules/.bin/tsc --noEmit` (pnpm이 아니라 바이너리 직접 호출)
- `dist/pcd.exe`는 git 추적 대상이며 클라이언트 변경 시 재빌드해야 한다 (마지막 태스크)
- 커밋 메시지는 기존 컨벤션을 따른다: `feat(scope): 한국어 설명` / `fix(scope): …` / `refactor(scope): …`
- 작업 브랜치: `feat/unified-control-room` (이미 생성됨, 스펙 커밋 2건 존재)

## 스펙 대비 정정 사항

계획 작성 중 코드를 읽고 확인한 결과, 스펙의 두 서술이 사실과 달랐다. **아래가 정확한 내용이며 계획은 이쪽을 따른다.**

1. **`CreateAgentSheet`는 이미 별도 파일이다** (`client/src/components/agent/CreateAgentSheet.tsx`). 스펙의 "DashboardPage에서 추출" 작업은 불필요하다.
2. **그리고 이미 죽은 코드다.** `DashboardPage.tsx:16`의 `showCreate`가 `true`가 되는 경로가 없다 — `setShowCreate(false)`만 호출된다(`DashboardPage.tsx:102`). 실제 생성 진입점은 `<Link to="/?new=1">`(`DashboardPage.tsx:56-62`)이며 `ProjectSelectPage`로 간다. 따라서 통합 화면도 이 링크를 그대로 옮기고, `CreateAgentSheet`는 마운트하지 않고 파일째 삭제한다.

## File Structure

**생성**

| 파일 | 책임 |
|---|---|
| `client/src/components/control/liveState.ts` | `LiveState` 타입, `liveState()`, `WORKING_WINDOW_MS`, `STATE_CHIP`, `timeAgo()`, `attnClasses()`, `attnLabel()`, `kindGlyph()` — 여러 컴포넌트가 공유하는 순수 함수 |
| `client/src/components/control/LiveDot.tsx` | `LiveDot`, `WorkingBar`, `Badge`, `ActBtn` — 프레젠테이션 프리미티브 |
| `client/src/components/control/AttentionRail.tsx` | attention 레일 |
| `client/src/components/control/AgentTile.tsx` | 타일 하나 (상태·메트릭·메타·액션) |
| `client/src/components/control/ProjectGroup.tsx` | 프로젝트 그룹 헤더 + 타일 그리드 |
| `client/src/components/control/ApprovalFeed.tsx` | `ApprovalCard` + `ApprovalFeed` |
| `client/src/hooks/useGoUp.ts` | 계층 기반 상위 이동 |

**수정**

| 파일 | 변경 |
|---|---|
| `server/handlers/native_approve.go` | AskUserQuestion auto-allow |
| `server/ws/hub.go` | 접속 시 즉시 메타 폴링 + 3초 디바운스 |
| `client/src/pages/ControlRoomPage.tsx` | 513줄 → 오케스트레이션만 |
| `client/src/App.tsx` | `/dashboard` 리다이렉트, `*` 폴백 |
| `client/src/components/layout/BottomNav.tsx` | 4탭 → 3탭, `replace` |
| `client/src/pages/TerminalPage.tsx` | 관제실 아이콘 제거, `useGoUp`, 에러 리다이렉트 |
| `client/src/pages/LogsPage.tsx` · `SettingsPage.tsx` · `AgentLauncherPage.tsx` | `useGoUp` 전환 |
| `client/src/pages/ProjectSelectPage.tsx` | 자동 리다이렉트 목적지 |
| `client/src/components/terminal/TerminalSnapshot.tsx` | 의도 주석만 추가 |

**삭제**

`client/src/pages/DashboardPage.tsx` · `components/agent/AgentCard.tsx` · `components/agent/AgentGrid.tsx` · `components/agent/AgentList.tsx` · `components/agent/CreateAgentSheet.tsx`

---

## Task 1: AskUserQuestion 승인 auto-allow (서버)

**Files:**
- Modify: `server/handlers/native_approve.go:53`
- Test: `server/handlers/native_approve_test.go`

**Interfaces:**
- Consumes: `services.PermissionBroker`, `services.ApproveTokenStore`, `services.PermissionDecision{Behavior, Message, UpdatedInput}` — 모두 기존
- Produces: 없음 (핸들러 내부 동작 변경)

- [ ] **Step 1: 실패하는 테스트를 쓴다**

`server/handlers/native_approve_test.go` 맨 아래에 추가한다. 기존 `post` 헬퍼(파일 14행)를 그대로 쓴다.

```go
// AskUserQuestion은 권한 게이트 대상이 아니다. 선택지는 클라이언트가 직접 렌더하고
// (lib/nativeEvents.ts) 사용자는 승인이 아니라 답변으로 응한다. 여기서 끊지 않으면
// 승인 카드와 선택지가 동시에 뜨고, BroadcastAll 때문에 컨트롤 룸 피드까지 오염된다.
func TestApproveAutoAllowsAskUserQuestion(t *testing.T) {
	broker := services.NewPermissionBroker()
	tokens := services.NewApproveTokenStore()
	tok, _ := tokens.Issue("s1")
	h := NativeApprove(broker, tokens)

	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		done <- post(t, h, tok, `{"sessionId":"s1","toolName":"AskUserQuestion","toolUseId":"toolu_ask","input":{"questions":[]}}`)
	}()

	select {
	case w := <-done:
		var got services.PermissionDecision
		json.Unmarshal(w.Body.Bytes(), &got)
		if got.Behavior != "allow" {
			t.Fatalf("behavior = %q, want allow without anyone answering", got.Behavior)
		}
		// allow는 updatedInput을 실어야 한다 — CLI가 하나의 모양만 기대한다.
		if string(got.UpdatedInput) != `{"questions":[]}` {
			t.Fatalf("updatedInput = %s, want the original input echoed back", got.UpdatedInput)
		}
	case <-time.After(time.Second):
		t.Fatal("handler blocked on AskUserQuestion; it must answer immediately")
	}

	if len(broker.Pending("s1")) != 0 {
		t.Fatal("AskUserQuestion must not park an approval prompt on the session")
	}
}

// 게이트를 약화시키면 안 된다: 일반 도구는 여전히 사람을 기다려야 한다.
func TestApproveStillBlocksNormalToolsAfterAskFilter(t *testing.T) {
	broker := services.NewPermissionBroker()
	tokens := services.NewApproveTokenStore()
	tok, _ := tokens.Issue("s1")
	h := NativeApprove(broker, tokens)

	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		done <- post(t, h, tok, `{"sessionId":"s1","toolName":"Bash","toolUseId":"toolu_b","input":{"command":"ls"}}`)
	}()

	deadline := time.Now().Add(time.Second)
	for len(broker.Pending("s1")) == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	select {
	case <-done:
		t.Fatal("Bash answered without a human — the permission gate was weakened")
	case <-time.After(50 * time.Millisecond):
	}
	broker.Resolve("toolu_b", services.PermissionDecision{Behavior: "allow"})
	<-done
}
```

- [ ] **Step 2: 테스트가 실패하는지 확인한다**

```bash
cd server && go test ./handlers/ -run TestApproveAutoAllowsAskUserQuestion -v
```

Expected: FAIL — `handler blocked on AskUserQuestion; it must answer immediately` (1초 타임아웃)

- [ ] **Step 3: 최소 구현을 넣는다**

`server/handlers/native_approve.go`에서 `id := req.ToolUseID` 블록(48-51행) **다음**, `decision, err := broker.Ask(`(53행) **직전**에 삽입한다.

```go
	// AskUserQuestion은 게이팅 대상이 아니다: 파일을 쓰지도 명령을 실행하지도 않고,
	// 사용자는 승인이 아니라 "답변"으로 응한다. 선택지 버튼은 클라이언트가 tool_use
	// 입력에서 직접 렌더하므로(lib/nativeEvents.ts), 여기서 끊지 않으면 선택지와
	// 허용/거부 카드가 동시에 뜬다. 게다가 native:approval은 BroadcastAll이라
	// 컨트롤 룸 승인 피드와 "승인 필요" 푸시까지 오염된다.
	if req.ToolName == askUserQuestionTool {
		writeJSON(w, http.StatusOK, services.PermissionDecision{
			Behavior:     "allow",
			UpdatedInput: req.Input,
		})
		return
	}
```

같은 파일 `import` 블록 아래(23행 `func NativeApprove` 직전)에 상수를 선언한다. 문자열을 인라인하지 않는 이유는, CLI가 도구 이름을 바꾸면 승인 카드가 조용히 다시 나타나기 때문에 grep 가능한 한 지점에 모아두기 위해서다.

```go
// CLI가 이 이름을 바꾸면 필터가 조용히 뚫린다(승인 카드가 슬그머니 부활).
// 회귀 확인 항목: 선택지가 뜰 때 허용/거부 카드가 없어야 한다.
const askUserQuestionTool = "AskUserQuestion"
```

- [ ] **Step 4: 테스트가 통과하는지 확인한다**

```bash
cd server && go test ./handlers/ -v
```

Expected: PASS — 신규 2개 포함 전부 통과. 특히 `TestApproveBlocksUntilAnswered`(기존 회귀)가 여전히 통과해야 한다.

- [ ] **Step 5: 커밋한다**

```bash
git add server/handlers/native_approve.go server/handlers/native_approve_test.go
git commit -m "fix(approve): AskUserQuestion에 승인 카드가 뜨지 않게 auto-allow

선택지 버튼과 허용/거부 카드가 동시에 뜨던 버그. 브리지가 모든 도구를
승인 엔드포인트로 넘기는데 도구 이름 필터가 없었음. broker.Ask() 직전에
끊어 컨트롤 룸 피드와 푸시 알림 오염도 함께 제거."
```

---

## Task 2: 접속 시 즉시 메타 폴링 (서버)

**Files:**
- Modify: `server/ws/hub.go` — Hub 구조체 필드 추가, `pollMeta()` 수정, `pollMetaSoon()` 신규, `HandleWebSocket()` 수정

**Interfaces:**
- Consumes: 기존 `h.agentSvc`, `h.gitSvc`, `h.portScanner`, `h.BroadcastAll`
- Produces: `func (h *Hub) pollMetaSoon()` — 비공개, Task 3 이후 클라이언트가 이 효과에 의존

**배경:** `agent:meta`의 유일한 발신 경로가 `Run()`의 10초 티커(`hub.go:104`)다. `HandleWebSocket`(180행)은 접속 시 아무것도 푸시하지 않으므로, 새 페이지 로드는 최대 10초간 git·포트가 빈 채로 렌더된다.

- [ ] **Step 1: Hub 구조체에 디바운스 필드를 추가한다**

`Hub` 구조체에서 기존 `statusMu` 필드 옆에 추가한다 (같은 패턴).

```go
	metaMu       sync.Mutex
	lastMetaPoll time.Time
```

`sync`와 `time`은 이미 import되어 있다 (`statusMu`, `time.NewTicker` 사용 중).

- [ ] **Step 2: `pollMeta()` 상단에 타임스탬프를 찍는다**

`func (h *Hub) pollMeta() {` 바로 다음 줄에 삽입한다.

```go
	h.metaMu.Lock()
	h.lastMetaPoll = time.Now()
	h.metaMu.Unlock()
```

- [ ] **Step 3: `pollMetaSoon()`을 추가한다**

`pollMeta()` 함수 정의 **바로 아래**에 넣는다.

```go
// pollMetaSoon은 새 클라이언트가 붙었을 때 메타를 즉시 한 번 밀어준다.
//
// agent:meta의 유일한 발신 경로가 10초 티커라, 이게 없으면 새로 연 화면은 git 브랜치와
// 포트가 최대 10초간 빈 채로 렌더된다. 대시보드에서는 다른 정보가 먼저 채워져 가려져
// 있었지만, 통합 관제실은 타일에 메타를 직접 얹기 때문에 공백이 그대로 보인다.
//
// 여러 기기가 동시에 붙을 때 git·포트 스캔이 반복되지 않도록 3초 디바운스를 건다.
// 건너뛰어도 정규 티커가 곧 처리하므로 공백은 최대 3초로 제한된다.
func (h *Hub) pollMetaSoon() {
	h.metaMu.Lock()
	recent := time.Since(h.lastMetaPoll) < 3*time.Second
	h.metaMu.Unlock()
	if recent {
		return
	}
	h.pollMeta()
}
```

- [ ] **Step 4: 접속 지점에서 호출한다**

`HandleWebSocket`의 `h.clients.Store(client, true)` **다음**, `go client.writePump()` 직전에 삽입한다.

```go
	// 새 화면이 붙었다 — 10초 티커를 기다리지 말고 메타를 지금 밀어준다.
	go h.pollMetaSoon()
```

`go`로 띄우는 이유: `pollMeta`가 agent마다 git·포트 스캔을 돌리므로 업그레이드 핸들러를 블로킹하면 접속이 느려진다.

- [ ] **Step 5: 빌드와 기존 테스트를 확인한다**

```bash
cd server && CGO_ENABLED=0 go build ./... && go test ./ws/ ./handlers/ ./services/
```

Expected: 빌드 성공, 전부 PASS

> **수동 검증 안내:** 이 변경의 실제 효과(첫 진입 시 메타 즉시 표시)는 Task 4에서 타일에 메타가 붙은 뒤에야 눈으로 확인할 수 있다. Task 8 최종 검증에서 확인한다.

- [ ] **Step 6: 커밋한다**

```bash
git add server/ws/hub.go
git commit -m "fix(ws): WS 접속 시 agent:meta를 즉시 1회 푸시

agent:meta 발신 경로가 10초 티커뿐이라 새로 연 화면은 최대 10초간
git·포트가 비어 있었음. 접속 시 즉시 폴링하되 3초 디바운스로 다중
접속 시 스캔 반복을 막음."
```

---

## Task 3: ControlRoomPage 분할 (동작 변경 없음)

**Files:**
- Create: `client/src/components/control/liveState.ts`
- Create: `client/src/components/control/LiveDot.tsx`
- Create: `client/src/components/control/AttentionRail.tsx`
- Create: `client/src/components/control/AgentTile.tsx`
- Create: `client/src/components/control/ProjectGroup.tsx`
- Create: `client/src/components/control/ApprovalFeed.tsx`
- Modify: `client/src/pages/ControlRoomPage.tsx`

**Interfaces:**
- Consumes: `AgentSummary`, `PendingApproval` (`stores/appStore`)
- Produces — 이후 태스크가 이 시그니처에 의존한다:
  - `liveState.ts`: `type LiveState = 'working' | 'idle' | 'stopped'`, `liveState(s: AgentSummary): LiveState`, `STATE_CHIP: Record<LiveState, {label: string; cls: string}>`, `timeAgo(ms: number): string`, `attnClasses(primary: string): string`, `attnLabel(r: {kind: string; count?: number}): string`, `kindGlyph(preset: string): string`, `WORKING_WINDOW_MS: number`
  - `LiveDot.tsx`: `LiveDot({hue, state}: {hue: number; state: LiveState})`, `WorkingBar()`, `Badge({children}: {children: React.ReactNode})`, `ActBtn({children, onClick, danger?, disabled?})`, `dot(hue: number, hollow?: boolean)`
  - `AttentionRail.tsx`: `AttentionRail({items, onOpen}: {items: AgentSummary[]; onOpen: (agentId: string) => void})`
  - `AgentTile.tsx`: `AgentTile({s, onOpen, onRestart, onStop}: {s: AgentSummary; onOpen: (id: string) => void; onRestart: (id: string) => void; onStop: (s: AgentSummary) => void})`
  - `ProjectGroup.tsx`: `ProjectGroup({label, agents, onOpen, onRestart, onStop}: {label: string; agents: AgentSummary[]} & AgentTile의 핸들러 3종)`
  - `ApprovalFeed.tsx`: `ApprovalFeed({approvals, summaries, onDecide, onOpen}: {approvals: PendingApproval[]; summaries: Record<string, AgentSummary>; onDecide: (a: PendingApproval, behavior: 'allow' | 'deny') => void; onOpen: (agentId: string) => void})`

> **이 태스크는 순수 리팩터다.** 화면 결과가 1픽셀도 바뀌면 안 된다. 코드는 옮기기만 하고 고치지 않는다. 기능 변경은 Task 4부터다.

- [ ] **Step 1: `liveState.ts`로 순수 함수를 옮긴다**

`ControlRoomPage.tsx`에서 아래를 **그대로 잘라내** 새 파일에 붙이고 각각 `export`를 붙인다.
- `ATTN_ORDER` (14행) — 페이지가 정렬에 쓰므로 export
- `attnClasses` (16-27행), `attnLabel` (29-32행), `kindGlyph` (34-39행), `timeAgo` (41-51행)
- `LiveState` 타입 (67행), `WORKING_WINDOW_MS` (69행), `liveState` (71-75행), `STATE_CHIP` (77-81행)

파일 상단에 import를 넣는다:

```ts
import type { AgentSummary } from '../../stores/appStore';
```

- [ ] **Step 2: `LiveDot.tsx`로 프리미티브를 옮긴다**

`dot` (53-61행), `LiveDot` (85-98행), `WorkingBar` (102-108행), `ActBtn` (479-505행), `Badge` (507-513행)를 그대로 옮기고 export한다. 상단 import:

```tsx
import type { LiveState } from './liveState';
```

- [ ] **Step 3: `ApprovalFeed.tsx`로 승인 UI를 옮긴다**

`ApprovalCard` (296-333행)와 `ApprovalFeed` (335-345행)를 옮긴다. 원본은 클로저로 `summaries`·`decide`·`navigate`를 잡고 있었으므로 props로 바꾼다.

```tsx
import type { AgentSummary, PendingApproval } from '../../stores/appStore';
import { timeAgo } from './liveState';

export function ApprovalFeed({
  approvals,
  summaries,
  onDecide,
  onOpen,
}: {
  approvals: PendingApproval[];
  summaries: Record<string, AgentSummary>;
  onDecide: (a: PendingApproval, behavior: 'allow' | 'deny') => void;
  onOpen: (agentId: string) => void;
}) {
  return (
    <>
      {/* 원본 335-345행의 JSX를 그대로. approvals.map은 아래 ApprovalCard를 호출 */}
    </>
  );
}
```

`ApprovalCard`는 같은 파일 안의 비공개 함수로 두고, 원본 296-333행 JSX를 그대로 쓰되 `decide(a, 'allow')` → `onDecide(a, 'allow')`, `navigate(\`/agents/${a.agentId}\`)` → `onOpen(a.agentId)`, `summaries[a.agentId]?.name` → props의 `summaries` 사용으로 바꾼다.

- [ ] **Step 4: `AgentTile.tsx`로 타일을 옮긴다**

`QuickActions` (223-230행)와 `Tile` (232-294행)을 하나의 `AgentTile`로 합쳐 옮긴다. `navigate`/`restart`/`stop` 클로저를 props 핸들러로 바꾼다. **이 단계에서는 로그 버튼을 그대로 둔다** (Task 4에서 제거).

```tsx
import type { AgentSummary } from '../../stores/appStore';
import { liveState, STATE_CHIP, attnClasses, attnLabel, kindGlyph, timeAgo } from './liveState';
import { LiveDot, WorkingBar, Badge, ActBtn } from './LiveDot';

export function AgentTile({
  s,
  onOpen,
  onRestart,
  onStop,
  onLogs,
}: {
  s: AgentSummary;
  onOpen: (id: string) => void;
  onRestart: (id: string) => void;
  onStop: (s: AgentSummary) => void;
  onLogs: () => void;
}) {
  // 원본 232-294행 본문 그대로. QuickActions는 이 컴포넌트 안에 인라인.
}
```

- [ ] **Step 5: `ProjectGroup.tsx`와 `AttentionRail.tsx`를 만든다**

`ProjectGroup`은 원본 416-441행(그룹 하나를 그리는 `groups.map` 콜백 본문)을 옮긴다. 그룹 내 "N 작업 중" 계산(424-432행)도 함께 옮긴다.

`AttentionRail`은 원본 376-408행을 옮기고, `navigate(...)`를 `onOpen(s.agentId)`로 바꾼다.

- [ ] **Step 6: `ControlRoomPage.tsx`를 오케스트레이션만 남기고 정리한다**

남는 것: `useAppStore` 구독, `connected`/`toast`/`sheetOpen`/`forceTick` 상태, resync `useEffect`(130-154행), `showToast`, `list`/`attention`/`groups` `useMemo`, `decide`/`restart`/`stop` 핸들러, 그리고 header/main/aside/BottomNav/시트/토스트 레이아웃. 나머지는 전부 import로 대체한다.

- [ ] **Step 7: 타입 검사와 육안 확인**

```bash
cd client && ./node_modules/.bin/tsc --noEmit
```

Expected: 에러 0건

수동 확인 — `/control`을 열고 **분할 전과 화면이 동일한지** 본다:
- 타일 레이아웃·색·상태 칩이 그대로인지
- attention 레일이 뜨는지 (승인 대기 중인 세션이 있을 때)
- 데스크톱 우측 승인 피드, 모바일 "승인" 바텀시트가 열리는지
- 열기/재시작/정지/로그 버튼이 전부 동작하는지

- [ ] **Step 8: 커밋한다**

```bash
git add client/src/components/control client/src/pages/ControlRoomPage.tsx
git commit -m "refactor(control): ControlRoomPage를 컴포넌트로 분할

동작 변경 없음. 513줄 단일 파일을 liveState/LiveDot/AttentionRail/
AgentTile/ProjectGroup/ApprovalFeed로 나누고 페이지는 오케스트레이션만
남김. 이후 기능 추가를 작은 파일 위에서 하기 위한 준비."
```

---

## Task 4: 타일에 메타 추가 + 액션 재배치

**Files:**
- Modify: `client/src/components/control/AgentTile.tsx`
- Modify: `client/src/pages/ControlRoomPage.tsx`

**Interfaces:**
- Consumes: Task 3의 `AgentTile` props
- Produces: `AgentTile`의 props가 바뀐다 — `onLogs` 제거, `onDestroy: (s: AgentSummary) => void` 추가

- [ ] **Step 1: `AgentTile`에 `AgentMeta`와 `SubAgentBar`를 넣는다**

import를 추가한다.

```tsx
import { AgentMeta } from '../sidebar/AgentMeta';
import { SubAgentBar } from '../animation/SubAgentBar';
```

`Badge` 줄(원본 287-290행의 `<div className="flex gap-1.5 mt-2">`) **직전**에 삽입한다.

```tsx
        <AgentMeta agentId={s.agentId} />
```

`AgentMeta`는 스토어에서 직접 읽고 데이터가 없으면 `null`을 반환하므로(`AgentMeta.tsx:12`) 별도 조건 분기가 필요 없다.

- [ ] **Step 2: `SubAgentBar`를 서브에이전트가 있을 때만 렌더한다**

`AgentMeta` 바로 아래에 넣는다.

```tsx
        {/* 타일에 이미 `tool: …` 줄이 있어 메인 에이전트만 돌 때는 같은 정보가
            두 번 나온다. SubAgentBar의 고유 가치는 여러 노드가 동시에 돌 때뿐. */}
        <SubAgentBar agentId={s.agentId} onlyWhenMultiple />
```

`SubAgentBar`가 `onlyWhenMultiple` prop을 지원하지 않으면, `components/animation/SubAgentBar.tsx`를 열어 활성 노드 개수를 세는 지점을 찾아 다음을 추가한다.

```tsx
  if (onlyWhenMultiple && nodes.length < 2) return null;
```

그리고 props 타입에 `onlyWhenMultiple?: boolean`을 더한다. 기본값이 `false`이므로 기존 호출부는 영향받지 않는다.

- [ ] **Step 3: 로그 버튼을 빼고 정지/삭제를 상태에 따라 교체한다**

`AgentTile`의 액션 영역을 아래로 교체한다. `onLogs` prop과 그 버튼은 삭제한다.

```tsx
      <div className="flex flex-wrap gap-1.5 mt-2 pt-2 border-t border-deck-border-soft">
        <ActBtn onClick={() => onOpen(s.agentId)}>열기</ActBtn>
        <ActBtn onClick={() => onRestart(s.agentId)}>재시작</ActBtn>
        {/* 되돌릴 수 있는 정지는 실행 중일 때만, 되돌릴 수 없는 삭제는 이미 멈춘
            세션에만 노출한다 — 밀도 높은 벽에서 오클릭으로 살아있는 세션을
            날리는 일이 없도록. */}
        {s.status === 'running' ? (
          <ActBtn onClick={() => onStop(s)}>정지</ActBtn>
        ) : (
          <ActBtn danger onClick={() => onDestroy(s)}>삭제</ActBtn>
        )}
      </div>
```

props 타입에서 `onLogs: () => void`를 지우고 `onDestroy: (s: AgentSummary) => void`를 추가한다.

- [ ] **Step 4: 페이지에 `destroy` 핸들러를 추가하고 배선한다**

`ControlRoomPage.tsx`의 `stop` 함수 아래에 추가한다.

```tsx
  // 되돌릴 수 없는 삭제 — 정지된 세션에서만 호출된다(AgentTile). 목록에서 사라지는
  // 것은 서버의 agent:destroyed 이벤트가 처리한다.
  async function destroy(s: AgentSummary) {
    if (!window.confirm(`${s.name} 세션을 삭제할까요? 되돌릴 수 없습니다.`)) return;
    try {
      await api.deleteAgent(s.agentId);
      showToast(`${s.name} 삭제됨`);
    } catch (e: any) {
      showToast('삭제 실패: ' + (e?.message || ''));
    }
  }
```

`ProjectGroup`에 넘기는 핸들러에서 `onLogs`를 빼고 `onDestroy={destroy}`를 추가한다. `ProjectGroup.tsx`의 props도 같이 고친다.

- [ ] **Step 5: 타입 검사와 육안 확인**

```bash
cd client && ./node_modules/.bin/tsc --noEmit
```

Expected: 에러 0건

수동 확인:
- `/control`을 **새로고침**했을 때 git 브랜치·포트가 **10초 기다리지 않고 바로** 뜨는지 (Task 2의 실제 검증)
- 포트 링크를 누르면 `http://localhost:<port>`가 새 탭으로 열리는지
- 실행 중 타일에 `[정지]`, 정지된 타일에 `[삭제]`가 뜨는지
- `[삭제]`가 확인 창을 띄우고, 확인하면 타일이 사라지는지
- 서브에이전트가 없는 세션에 SubAgentBar가 **안 뜨는지**

- [ ] **Step 6: 커밋한다**

```bash
git add client/src/components/control client/src/components/animation/SubAgentBar.tsx client/src/pages/ControlRoomPage.tsx
git commit -m "feat(control): 타일에 git·포트 메타와 삭제 액션 추가

대시보드가 갖고 있던 AgentMeta를 타일에 인라인하고, 로그 버튼을 빼고,
정지(되돌림 가능)와 삭제(되돌림 불가)를 실행 상태에 따라 교체 노출.
SubAgentBar는 서브에이전트가 실제로 있을 때만."
```

---

## Task 5: 헤더에 생성 진입점 추가 + classic dashboard 링크 제거

**Files:**
- Modify: `client/src/pages/ControlRoomPage.tsx:353-358`

**Interfaces:**
- Consumes: `IconPlus` (`components/icons`)
- Produces: 없음

- [ ] **Step 1: "classic dashboard ↗" 버튼을 생성 링크로 교체한다**

`ControlRoomPage.tsx` 353-358행의 버튼을 통째로 아래로 바꾼다.

```tsx
        <div className="flex-1" />
        {/* 생성은 ProjectSelectPage가 담당한다. ?new=1은 "실행 중이면 바로 세션으로"
            자동 리다이렉트를 건너뛰기 위한 우회 파라미터. */}
        <Link
          to="/?new=1"
          className="flex items-center gap-1.5 px-2.5 py-1 rounded-lg bg-deck-accent text-white text-[11px] font-medium active:opacity-80"
        >
          <IconPlus size={13} />
          <span>프로젝트 추가</span>
        </Link>
```

기존 353-358행 버튼 뒤에 있던 `<div className="flex-1" />`(359행)는 위로 올라갔으므로 **원래 자리에서 삭제한다** (중복 방지).

- [ ] **Step 2: import를 추가한다**

`ControlRoomPage.tsx` 상단 import를 수정한다.

```tsx
import { useNavigate, Link } from 'react-router-dom';
import { IconPlus } from '../components/icons';
```

- [ ] **Step 3: 타입 검사와 육안 확인**

```bash
cd client && ./node_modules/.bin/tsc --noEmit
```

수동 확인: `/control` 헤더에 "프로젝트 추가"가 보이고, 누르면 `/?new=1`로 이동해 프로젝트 선택 화면이 뜨는지. "classic dashboard ↗"가 사라졌는지.

- [ ] **Step 4: 커밋한다**

```bash
git add client/src/pages/ControlRoomPage.tsx
git commit -m "feat(control): 헤더에 프로젝트 추가 버튼, classic dashboard 링크 제거"
```

---

## Task 6: 라우팅 리다이렉트 + 죽은 파일 삭제

**Files:**
- Modify: `client/src/App.tsx:10,162,170`
- Modify: `client/src/pages/ProjectSelectPage.tsx` (`navigate('/dashboard', …)` 지점)
- Modify: `client/src/pages/TerminalPage.tsx:198`
- Delete: `client/src/pages/DashboardPage.tsx`, `client/src/components/agent/AgentCard.tsx`, `client/src/components/agent/AgentGrid.tsx`, `client/src/components/agent/AgentList.tsx`, `client/src/components/agent/CreateAgentSheet.tsx`

**Interfaces:**
- Consumes: 없음
- Produces: `/dashboard`가 `/control`로 리다이렉트된다 — Task 7의 BottomNav가 이에 의존하지 않지만, 북마크·핸드오프 링크 호환을 위해 유지

- [ ] **Step 1: 삭제 대상이 정말 참조되지 않는지 확인한다**

```bash
cd client/src && grep -rn "DashboardPage\|AgentCard\|AgentGrid\|AgentList\|CreateAgentSheet" --include=*.tsx --include=*.ts .
```

Expected: `App.tsx`의 `DashboardPage` import/route와, 삭제 대상 파일들끼리의 상호 참조만 나와야 한다. **그 외 참조가 하나라도 나오면 삭제를 멈추고 해당 사용처를 먼저 처리한다.**

- [ ] **Step 2: `App.tsx`의 라우트를 바꾼다**

10행의 import를 삭제한다.

```tsx
import { DashboardPage } from './pages/DashboardPage';
```

162행 라우트를 교체한다.

```tsx
            {/* 통합 관제실이 대시보드를 흡수했다. 북마크·핸드오프로 들어온
                기존 링크가 죽지 않도록 리다이렉트로 남긴다. */}
            <Route path="/dashboard" element={<Navigate to="/control" replace />} />
```

170행 폴백을 바꾼다.

```tsx
          <Route path="*" element={<Navigate to="/control" replace />} />
```

- [ ] **Step 3: `ProjectSelectPage`의 자동 리다이렉트 목적지를 바꾼다**

```tsx
          navigate('/control', { replace: true });
```

바로 위 주석의 "dashboard" 표현도 고친다:

```tsx
  // Auto-redirect to the control room if agents are running
```

- [ ] **Step 4: `TerminalPage`의 에러 리다이렉트를 바꾼다**

198행:

```tsx
        .catch(() => navigate('/control'));
```

- [ ] **Step 5: 파일 5개를 삭제한다**

```bash
git rm client/src/pages/DashboardPage.tsx \
       client/src/components/agent/AgentCard.tsx \
       client/src/components/agent/AgentGrid.tsx \
       client/src/components/agent/AgentList.tsx \
       client/src/components/agent/CreateAgentSheet.tsx
```

`CreateAgentSheet`도 지우는 이유: `DashboardPage.tsx:16`의 `showCreate`가 `true`가 되는 경로가 없어 **이미 죽은 UI**였고, 유일한 import처가 `DashboardPage`였다.

- [ ] **Step 6: 타입 검사와 육안 확인**

```bash
cd client && ./node_modules/.bin/tsc --noEmit
```

Expected: 에러 0건. 에러가 나면 Step 1에서 놓친 참조가 있다는 뜻이다.

수동 확인:
- 주소창에 `/dashboard`를 직접 치면 `/control`로 튕기는지
- 존재하지 않는 경로(`/nope`)가 `/control`로 가는지
- 에이전트가 실행 중일 때 `/`로 들어가면 마지막 세션 또는 `/control`로 가는지
- `/?new=1`은 여전히 프로젝트 선택 화면에 머무는지

- [ ] **Step 7: 커밋한다**

```bash
git add -A client/src
git commit -m "feat(nav): /dashboard를 /control로 리다이렉트하고 대시보드 파일 삭제

DashboardPage/AgentCard/AgentGrid/AgentList 삭제. CreateAgentSheet는
showCreate가 true가 되는 경로가 없어 이미 죽은 UI였으므로 함께 삭제.
북마크 호환을 위해 /dashboard 경로 자체는 리다이렉트로 유지."
```

---

## Task 7: 계층 네비게이션 전환

**Files:**
- Create: `client/src/hooks/useGoUp.ts`
- Delete: `client/src/hooks/useGoBack.ts`
- Modify: `client/src/pages/TerminalPage.tsx:75,307-320,537-551`
- Modify: `client/src/pages/LogsPage.tsx:5,18`
- Modify: `client/src/pages/SettingsPage.tsx:8,14`
- Modify: `client/src/pages/AgentLauncherPage.tsx:5,9`
- Modify: `client/src/components/layout/BottomNav.tsx`

**Interfaces:**
- Consumes: 없음
- Produces: `useGoUp(parent: string): () => void`

- [ ] **Step 1: `useGoUp.ts`를 만든다**

```ts
import { useCallback } from 'react';
import { useNavigate } from 'react-router-dom';

/**
 * 계층("위로") 네비게이션. 각 화면은 부모를 정확히 하나 선언하고, 뒤로 가기는
 * 진입 경로와 무관하게 항상 그 부모로 간다.
 *
 * 이전 구현(useGoBack)은 navigate(-1), 즉 "마지막 이동 취소"였다. 사용자가 뒤로
 * 가기에 기대하는 "한 단계 위로"와는 일직선으로 들어왔을 때만 우연히 일치한다.
 * 실제로는 두 가지가 어긋났다:
 *   - BottomNav 탭 이동이 히스토리에 쌓여, 터미널 → 설정 → 로그에서 뒤로를 누르면
 *     작업하던 터미널이 아니라 설정으로 갔다
 *   - 관제실 진입 경로가 둘이라, 관제실 → 터미널 → (헤더 아이콘) 관제실 → 뒤로가
 *     터미널로 되돌아오는 루프가 생겼다
 *
 * 계층 방식은 히스토리를 보지 않으므로 딥링크와 PWA 콜드 스타트에서도 동일하게
 * 동작한다(예전에는 idx === 0이라 fallback으로 샜다).
 *
 * 대가: 터미널 → 로그 → 뒤로는 그 터미널이 아니라 /control로 간다. 주 루프가
 * 벽↔세션이고 로그·설정은 간헐적 경유지라 클릭 한 번을 받아들인 결정이다.
 */
export function useGoUp(parent: string) {
  const navigate = useNavigate();
  return useCallback(() => navigate(parent), [navigate, parent]);
}
```

- [ ] **Step 2: 각 페이지를 전환한다**

`TerminalPage.tsx` — 21행 import와 75행:

```tsx
import { useGoUp } from '../hooks/useGoUp';
```
```tsx
  const goUp = useGoUp('/control');
```

`LogsPage.tsx` (5행, 18행), `SettingsPage.tsx` (8행, 14행)도 동일하게 `useGoUp('/control')`로 바꾼다.

`AgentLauncherPage.tsx` (5행, 9행)는 부모가 프로젝트 선택이다:

```tsx
  const goUp = useGoUp('/');
```

각 파일에서 `onClick={goBack}`을 `onClick={goUp}`으로 바꾼다 (`TerminalPage.tsx:307,537` · `LogsPage.tsx:43` · `SettingsPage.tsx:23` · `AgentLauncherPage.tsx:25`).

- [ ] **Step 3: `useGoBack.ts`를 삭제한다**

```bash
cd client/src && grep -rn "useGoBack" --include=*.tsx --include=*.ts .
```

Expected: 결과 없음. 그 뒤:

```bash
git rm client/src/hooks/useGoBack.ts
```

- [ ] **Step 4: TerminalPage의 관제실 아이콘을 제거한다**

`TerminalPage.tsx:313-320`(모바일)과 `545-551`(데스크톱)의 관제실 이동 버튼을 삭제한다. 뒤로 가기가 이제 `/control`로 가므로 같은 목적지 버튼이 헤더에 둘이 되고, 이 버튼이 위 주석에서 말한 루프의 원인이었다.

`IconDevices`가 `TerminalPage.tsx`에서 더 이상 쓰이지 않으면 22행 import 목록에서도 뺀다.

- [ ] **Step 5: BottomNav를 3탭으로 바꾼다**

`BottomNav.tsx`를 아래로 교체한다.

```tsx
import { Link, useLocation } from 'react-router-dom';
import { IconLog, IconSettings, IconDevices } from '../icons';
import { NotificationBadge } from '../notification/NotificationBadge';

// 통합 후 /dashboard와 /control은 같은 화면이므로 탭도 하나로 합쳤다.
// 프로젝트 추가는 관제실 헤더 버튼이 담당한다(탭이 아니라 행동이라서).
const NAV_ITEMS = [
  { href: '/control', label: 'Deck', Icon: IconDevices },
  { href: '/logs', label: 'Logs', Icon: IconLog },
  { href: '/settings', label: 'Settings', Icon: IconSettings },
];

export function BottomNav() {
  const { pathname } = useLocation();

  return (
    <nav className="md:hidden flex items-center justify-around safe-bottom bg-deck-surface border-t border-deck-border">
      {NAV_ITEMS.map((item) => {
        const active = pathname === item.href;
        return (
          <Link
            key={item.href}
            to={item.href}
            // 탭 전환은 스택을 쌓지 않는다 — 그래야 뒤로 가기가 탭 방문 순서를
            // 거꾸로 걷지 않는다.
            replace
            className="flex flex-col items-center gap-1 py-3 px-5 text-xs min-w-[56px]"
            style={{ color: active ? '#6366f1' : '#8791a4' }}
          >
            <div className="relative">
              <item.Icon size={22} />
              {item.href === '/control' && (
                <NotificationBadge className="absolute -top-1.5 -right-2.5" />
              )}
            </div>
            <span className="text-[10px]">{item.label}</span>
          </Link>
        );
      })}
    </nav>
  );
}
```

- [ ] **Step 6: 타입 검사와 육안 확인**

```bash
cd client && ./node_modules/.bin/tsc --noEmit
```

수동 확인:
- 터미널 → 뒤로 → `/control`
- 터미널 → 로그 → 뒤로 → `/control` (설정이 **아니라**)
- 모바일 BottomNav가 3탭(Deck/Logs/Settings)이고 Deck에 알림 배지가 붙는지
- 탭을 여러 번 옮겨다닌 뒤 뒤로 가기가 탭 순서를 거꾸로 걷지 **않는지**
- 터미널 헤더에 관제실 아이콘이 없는지 (모바일·데스크톱 둘 다)
- `/launch/...`에서 뒤로 가면 `/`로 가는지
- 앱을 완전히 새로 열어 `/agents/<id>`로 딥링크했을 때 뒤로가 `/control`로 가는지

- [ ] **Step 7: 커밋한다**

```bash
git add -A client/src
git commit -m "feat(nav): 뒤로 가기를 히스토리 기반에서 계층 기반으로 전환

useGoBack(navigate(-1))을 useGoUp(고정 부모)으로 교체. 탭 이동이
히스토리에 쌓여 엉뚱한 곳으로 가던 문제와, 관제실 진입 경로가 둘이라
생기던 루프를 함께 제거. BottomNav는 3탭 + replace 이동."
```

---

## Task 8: TerminalSnapshot 주석 + 최종 검증 + 바이너리 재빌드

**Files:**
- Modify: `client/src/components/terminal/TerminalSnapshot.tsx` (상단 주석만)
- Modify: `dist/pcd.exe` (재빌드 산출물)

**Interfaces:**
- Consumes: 없음
- Produces: 없음

- [ ] **Step 1: `TerminalSnapshot.tsx`에 의도 주석을 단다**

파일 맨 위(첫 import 앞)에 넣는다.

```tsx
// 의도적으로 미사용 상태 — 지우다 만 파일이 아니다.
//
// 통합 관제실(2026-07)에서 타일 밀도를 위해 스냅샷을 화면에서 뺐다. 파일을 남긴
// 이유는 여기에 재유도하기 번거로운 로직이 있기 때문이다: headless wterm 인스턴스
// 관리, 300ms 스로틀 페인트, `open` 이벤트 시 재attach. import처가 없으므로 vite가
// 트리셰이킹해 번들 비용은 0이고, tsc는 계속 타입 검사를 한다.
//
// 되살리려면:
//   1. AgentTile에서 마운트하되 동시 인스턴스를 1개로 제한할 것
//      (expandedId: string | null — 에이전트마다 headless wterm이 하나씩 생긴다)
//   2. 스냅샷은 80칼럼이라 1/3 폭 타일에서는 읽히지 않는다. 펼친 타일을
//      col-span-full 행으로 전개해야 제 비율이 나온다(모바일 1컬럼에서는 무동작).
//   3. 먼저 확인할 것: tsc는 타입만 보므로 프로토콜 드리프트를 잡지 못한다.
//      `terminal:output` 이벤트 이름과 페이로드 모양이 그대로인지 확인할 것.
//      어긋나면 타입은 통과하는데 화면만 빈 채로 남는다.
```

- [ ] **Step 2: 전체 빌드와 테스트를 돌린다**

```bash
cd server && CGO_ENABLED=0 go build ./... && go test ./...
cd ../client && ./node_modules/.bin/tsc --noEmit
```

Expected: 전부 통과

- [ ] **Step 3: 통합 수동 검증을 한다**

앱을 실제로 띄우고 아래를 순서대로 확인한다. **하나라도 실패하면 그 태스크로 돌아간다.**

승인 필터 (Task 1):
- Claude에게 선택지를 묻게 하고 → **선택지 버튼만** 뜨고 허용/거부 카드는 없는지
- 같은 순간 `/control` 승인 피드가 **비어 있는지**
- 폰에 "승인 필요" 푸시가 **안 오는지**
- 선택지를 고르면 Claude가 정상적으로 다음 턴을 진행하는지
- **회귀:** 일반 도구(Bash/Write)는 여전히 허용/거부 카드가 뜨고 정상 동작하는지

메타 (Task 2, 4):
- `/control` 새로고침 시 git·포트가 10초 기다리지 않고 즉시 뜨는지

통합 화면 (Task 4, 5, 6):
- 실행 중 타일 `[정지]` / 정지된 타일 `[삭제]`
- `/dashboard` → `/control` 리다이렉트
- 헤더 "프로젝트 추가" → `/?new=1`

네비게이션 (Task 7):
- 터미널 → 로그 → 뒤로 → `/control`
- BottomNav 3탭, Deck 탭 배지
- 터미널 헤더에 관제실 아이콘 없음

- [ ] **Step 4: Windows 바이너리를 재빌드한다**

클라이언트가 바뀌었으므로 `dist/pcd.exe`를 갱신해야 한다. 저장소의 기존 크로스컴파일 절차를 따른다 (`docs/windows.md` 또는 루트의 빌드 스크립트 확인).

- [ ] **Step 5: 커밋한다**

```bash
git add client/src/components/terminal/TerminalSnapshot.tsx dist/pcd.exe
git commit -m "chore: TerminalSnapshot 보존 의도 주석 + pcd.exe 재빌드"
```

---

## Self-Review

**1. 스펙 커버리지**

| 스펙 섹션 | 태스크 |
|---|---|
| 1. 통합 화면 — 타일 구성 | Task 4 Step 1-2 |
| 1. 액션 배치 (정지/삭제, 로그 제거) | Task 4 Step 3-4 |
| 1. 생성 진입점 | Task 5 |
| 2. 컴포넌트 구조 | Task 3 |
| 3. 데이터 흐름 — 조인 불필요 | Task 4 (AgentMeta가 스토어 직접 구독) |
| 3. 메타 공백 해결 | Task 2 |
| 3. 수용하는 제약 (정지된 에이전트 메타 없음) | 변경 없음 — `pollMeta`의 `status != running` 스킵 유지 |
| 4. 계층 네비게이션 | Task 7 Step 1-2 |
| 4. BottomNav 3탭 / 관제실 아이콘 / replace | Task 7 Step 4-5 |
| 4. 경로 상수 변경 | Task 6 Step 2-4 |
| 5. AskUserQuestion 승인 제거 | Task 1 |
| 6. 파일 정리 (삭제 4개 + 보존) | Task 6 Step 5, Task 8 Step 1 |
| 7. 에러 처리 | Task 4 Step 4 (삭제 실패 토스트), 기존 동작 유지 |
| 8. 검증 | Task 8 Step 2-3 |

스펙 항목 중 태스크가 없는 것: 없음. **단, 스펙의 "CreateAgentSheet 추출" 항목은 사실 오류였으므로 삭제로 대체했다** (위 "스펙 대비 정정 사항").

**2. 플레이스홀더 스캔:** "TBD"/"적절히 처리"/"비슷하게" 없음. Task 3 Step 3-5는 기존 코드를 지정된 행 범위에서 옮기는 작업이라 전문을 반복하지 않았으나, 이동 대상과 바꿔야 할 식별자를 정확히 명시했다.

**3. 타입 일관성:** `AgentTile`의 props가 Task 3(`onLogs` 포함)에서 Task 4(`onLogs` 제거, `onDestroy` 추가)로 의도적으로 바뀐다 — Task 4 Interfaces에 명시했다. `ProjectGroup`도 같은 시점에 함께 바뀌므로 Task 4 Step 4에 포함했다. `liveState()`, `timeAgo()`, `attnClasses()` 이름은 원본과 동일하게 유지했다.

## 알려진 한계

- **클라이언트 자동 테스트가 없다.** Task 3~7의 회귀 안전망은 `tsc --noEmit`과 수동 확인뿐이다. 특히 Task 3(분할)은 "화면이 안 바뀌어야 한다"는 것을 기계적으로 검증할 방법이 없다. vitest + React Testing Library 도입은 별도 작업으로 다룰 가치가 있다.
- **Task 2의 디바운스는 단위 테스트가 없다.** `pollMeta`가 `agentSvc`/`gitSvc`/`portScanner`에 의존해 Hub 전체를 세워야 하는데, 그 비용이 얻는 확신보다 크다고 판단했다. 빌드 + 기존 ws 테스트 + Task 8의 수동 확인으로 대체한다.
