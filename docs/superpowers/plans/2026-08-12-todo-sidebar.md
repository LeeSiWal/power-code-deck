# 할일 사이드 패널 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 우측 패널의 탭 콘텐츠 위에 항상 보이는 할일 구역을 붙여, 계획을 세운 뒤 구현하는 동안 남은 양이 찾아보지 않아도 보이게 한다.

**Architecture:** 데이터는 이미 있다 — `ActivityManager`가 트랜스크립트를 파싱해 `agent:activity` 스냅샷의 `todos`로 내려주고 있고, 2026-08-12에 `TaskCreate`/`TaskUpdate`까지 인식하도록 고쳤다. 그래서 이 작업은 (1) 워처가 프로젝트 최신 파일 대신 그 에이전트의 트랜스크립트를 지목하게 하는 서버 수정 하나와, (2) 우측 패널에 고정 구역 + 세로 크기 조절 스플리터를 붙이는 클라이언트 작업이다. 새 테이블도, 새 WS 이벤트도, 새 의존성도 없다.

**Tech Stack:** Go 1.26 (표준 라이브러리만), React + TypeScript, Tailwind, Zustand. 빌드는 `go build` / `vite` / `tsc` 바이너리 직접 호출 (pnpm 아님).

## Global Constraints

- 스펙: `docs/superpowers/specs/2026-08-12-todo-sidebar-design.md`. 그 문서의 비목표(사용자 직접 편집, 프로젝트 단위 영속화, 세션 간 집계, 서브에이전트 할일 분리, 기존 좌우 스플리터의 터치 대응)는 이 계획에서도 건드리지 않는다.
- 새 Go 모듈·npm 패키지를 추가하지 않는다. 이 리포는 단일 바이너리 배포다.
- 주석과 UI 문구는 한국어/영어 혼용 관례를 따른다 — 기존 파일의 톤에 맞춘다. 새 UI 문구는 한국어.
- 모바일(`w < 768`) 경로는 건드리지 않는다. `TerminalPage.tsx:306`의 `if (isMobile)` 분기 안쪽은 이번 작업에서 수정 대상이 아니다.
- 서버 검증: `cd server && CGO_ENABLED=0 go build ./... && CGO_ENABLED=0 go test ./...`
- 클라이언트 검증: `cd client && ./node_modules/.bin/tsc --noEmit`
- `go vet ./...`은 이 리포에서 이미 실패한다(`services/claude_resume_live_test.go:105`, `sync.Once` 복사). 이 계획의 범위가 아니므로 고치지 말고, 새로 실패를 늘리지도 말 것.
- 클라이언트가 바뀌는 태스크가 하나라도 있으면 마지막에 `dist/pcd.exe`를 재빌드한다 (git 추적 대상).

## File Structure

| 파일 | 책임 | 상태 |
|---|---|---|
| `server/services/activity.go` | 트랜스크립트 지목(`targetTranscript`) + 세션 id 게터 보관 | 수정 |
| `server/services/activity_target_test.go` | 지목 규칙 단위·회귀 테스트 | 생성 |
| `server/main.go` | `SetSessionIDLookup` 배선 | 수정 |
| `client/src/components/terminal/TodoRows.tsx` | 할일 목록 행 렌더링 (글리프·색·activeForm 규칙의 유일한 출처) | 생성 |
| `client/src/components/terminal/TodoStrip.tsx` | 접히는 한 줄 스트립 — 행 렌더링은 `TodoRows`에 위임 | 수정 |
| `client/src/components/terminal/TodoPanel.tsx` | 우측 패널 상단 고정 구역 (항상 펼침) | 생성 |
| `client/src/pages/TerminalPage.tsx` | 패널 배치 + Pointer Events 세로 스플리터 + 높이 기억 | 수정 |

---

### Task 1: 워처가 에이전트의 트랜스크립트를 지목

지금 `poll()`은 프로젝트 폴더에서 mtime이 가장 최신인 `.jsonl`을 따라간다. 워처는 에이전트별로 만들어지지만 폴더는 프로젝트 단위라, 한 프로젝트에 세션이 둘이면 서로의 할일 목록을 본다. 한 줄 스트립에서는 넘어갔지만 상시 패널에서는 "시키지도 않은 항목"으로 바로 드러난다.

**Files:**
- Modify: `server/services/activity.go` (`transcriptWatcher` 구조체 ~line 175-195, `poll()` ~line 210-215, `ActivityManager` ~line 22-28, `Start()` ~line 47-75)
- Modify: `server/main.go:68-69`
- Test: `server/services/activity_target_test.go` (생성)

**Interfaces:**
- Consumes: `AgentService.ClaudeSessionID(id string) string` (`services/agent.go:343`) — 이미 존재. 없으면 `""`.
- Produces:
  - `func (m *ActivityManager) SetSessionIDLookup(fn func(agentID string) string)`
  - `func (w *transcriptWatcher) targetTranscript() string`

- [ ] **Step 1: 실패하는 테스트 작성**

`server/services/activity_target_test.go` 생성:

```go
package services

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// writeTranscript는 mtime을 지정해 트랜스크립트 파일을 만든다. 지목 규칙이 mtime
// 순서에 의존하므로 순서를 테스트가 직접 정해야 한다.
func writeTranscript(t *testing.T, dir, name string, mod time.Time) string {
	t.Helper()
	p := filepath.Join(dir, name+".jsonl")
	if err := os.WriteFile(p, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(p, mod, mod); err != nil {
		t.Fatal(err)
	}
	return p
}

// 에이전트의 claude_session_id가 있으면 그 트랜스크립트를 본다 — 같은 프로젝트에서
// 더 최근에 쓰인 다른 세션의 파일이 있어도 마찬가지다. 이것이 교차오염을 막는 규칙이다.
func TestTargetTranscriptPrefersTheAgentsOwnSession(t *testing.T) {
	dir := t.TempDir()
	mine := writeTranscript(t, dir, "mine", time.Now().Add(-time.Hour))
	writeTranscript(t, dir, "someone-else", time.Now())

	w := &transcriptWatcher{
		agentID:      "a1",
		dir:          dir,
		sessionIDFor: func(string) string { return "mine" },
	}
	if got := w.targetTranscript(); got != mine {
		t.Fatalf("targetTranscript() = %q, want the agent's own file %q", got, mine)
	}
}

// 두 에이전트가 한 프로젝트에 있어도 각자 자기 파일을 본다.
func TestTargetTranscriptDoesNotCrossBetweenAgents(t *testing.T) {
	dir := t.TempDir()
	a := writeTranscript(t, dir, "sid-a", time.Now().Add(-time.Hour))
	b := writeTranscript(t, dir, "sid-b", time.Now())

	wa := &transcriptWatcher{agentID: "a", dir: dir, sessionIDFor: func(string) string { return "sid-a" }}
	wb := &transcriptWatcher{agentID: "b", dir: dir, sessionIDFor: func(string) string { return "sid-b" }}
	if got := wa.targetTranscript(); got != a {
		t.Fatalf("agent a targeted %q, want %q", got, a)
	}
	if got := wb.targetTranscript(); got != b {
		t.Fatalf("agent b targeted %q, want %q", got, b)
	}
}

// id를 아직 모르는 경우(네이티브는 system/init 전, 터미널 트랙은 영영)에는 예전처럼
// 최신 파일로 물러난다. 아무것도 안 보여주는 것보다 낫다.
func TestTargetTranscriptFallsBackToNewest(t *testing.T) {
	dir := t.TempDir()
	writeTranscript(t, dir, "old", time.Now().Add(-time.Hour))
	newest := writeTranscript(t, dir, "new", time.Now())

	for name, lookup := range map[string]func(string) string{
		"게터 없음":     nil,
		"빈 id":      func(string) string { return "" },
		"파일 없는 id": func(string) string { return "does-not-exist" },
	} {
		w := &transcriptWatcher{agentID: "a1", dir: dir, sessionIDFor: lookup}
		if got := w.targetTranscript(); got != newest {
			t.Fatalf("%s: targetTranscript() = %q, want newest %q", name, got, newest)
		}
	}
}
```

- [ ] **Step 2: 테스트를 돌려 실패를 확인**

Run: `cd server && CGO_ENABLED=0 go test ./services/ -run TestTargetTranscript`
Expected: FAIL — `w.sessionIDFor undefined` / `w.targetTranscript undefined` (빌드 실패)

- [ ] **Step 3: 워처에 필드와 지목 함수 추가**

`server/services/activity.go`의 `transcriptWatcher` 구조체에서 `agentID`/`dir` 근처에 필드를 추가한다:

```go
	// sessionIDFor는 이 에이전트의 claude_session_id를 매번 조회한다. 값이 아니라
	// 게터인 이유: id는 시작 시점에 없을 수 있다. 네이티브는 system/init이 와야
	// 알고(main.go의 SetPersistence), 이어하기는 시작 시점에 안다.
	// nil이거나 빈 문자열을 주면 최신 파일로 물러난다.
	sessionIDFor func(agentID string) string
```

`newestTranscript()` 바로 위에 추가한다:

```go
// targetTranscript는 이 워처가 따라갈 파일을 고른다.
//
// 워처는 에이전트별이지만 dir은 프로젝트 단위다. "가장 최근에 쓰인 파일"만 보면 한
// 프로젝트에 세션이 둘일 때 서로의 활동과 할일 목록을 보게 된다. 에이전트의 세션 id를
// 알면 그 파일을 직접 지목해 그 혼선을 없앤다.
//
// 알 수 없을 때(터미널 트랙에서 새로 띄운 claude는 자기 id를 우리에게 알려주지 않는다)
// 는 예전 동작으로 물러난다 — 틀린 목록을 보여줄 위험보다 아무것도 안 보여주는 쪽이
// 더 나쁘다.
func (w *transcriptWatcher) targetTranscript() string {
	if w.sessionIDFor != nil {
		if sid := w.sessionIDFor(w.agentID); sid != "" {
			p := filepath.Join(w.dir, sid+".jsonl")
			if st, err := os.Stat(p); err == nil && !st.IsDir() {
				return p
			}
		}
	}
	return w.newestTranscript()
}
```

- [ ] **Step 4: 테스트를 돌려 통과를 확인**

Run: `cd server && CGO_ENABLED=0 go test ./services/ -run TestTargetTranscript -v`
Expected: PASS (3개)

- [ ] **Step 5: `poll()`이 새 지목을 쓰게 한다**

`server/services/activity.go`의 `poll()` 첫 줄을 바꾼다:

```go
	path := w.newestTranscript()
```
→
```go
	path := w.targetTranscript()
```

- [ ] **Step 6: 매니저와 main.go 배선**

`ActivityManager` 구조체에 필드를 추가한다 (`emit` 아래):

```go
	sessionIDFor func(agentID string) string
```

`SetEmitter` 바로 아래에 추가한다:

```go
// SetSessionIDLookup wires how a watcher learns its agent's Claude session id, so it
// can follow that conversation's transcript instead of whichever file in the project
// was written last. Called once from main.go, like SetEmitter.
func (m *ActivityManager) SetSessionIDLookup(fn func(agentID string) string) {
	m.mu.Lock()
	m.sessionIDFor = fn
	m.mu.Unlock()
}
```

`Start()`에서 `emit := m.emit` 다음 줄에 조회를 꺼내고, 워처 리터럴에 넘긴다:

```go
	emit := m.emit
	lookup := m.sessionIDFor
	w := &transcriptWatcher{
		agentID:      agentID,
		dir:          claudeProjectDir(cwd),
		sessionIDFor: lookup,
		stop:         make(chan struct{}),
```

`server/main.go`의 69번 줄(`agentSvc.SetActivityManager(activitySvc)`) 바로 다음에 추가한다:

```go
	// 워처가 프로젝트에서 가장 최근에 쓰인 파일이 아니라 이 에이전트의 대화를 따라가게
	// 한다. 한 프로젝트에 세션이 둘일 때 서로의 활동·할일이 섞이는 것을 막는다.
	activitySvc.SetSessionIDLookup(agentSvc.ClaudeSessionID)
```

- [ ] **Step 7: 서버 전체 검증**

Run: `cd server && CGO_ENABLED=0 go build ./... && CGO_ENABLED=0 go test ./...`
Expected: 전부 PASS. 특히 기존 `TestTodoWrite*` / `TestTask*` / `activity` 관련 테스트가 깨지지 않아야 한다.

Run: `cd server && gofmt -l services/activity.go services/activity_target_test.go main.go`
Expected: 출력 없음

- [ ] **Step 8: 커밋**

```bash
git add server/services/activity.go server/services/activity_target_test.go server/main.go
git commit -m "fix(activity): 워처가 프로젝트 최신 파일 대신 에이전트의 트랜스크립트를 지목

한 프로젝트에 세션이 둘이면 서로의 활동과 할일 목록을 봤다. 워처는
에이전트별이지만 dir은 프로젝트 단위이고 newestTranscript()가 mtime
최신을 고르기 때문이다. 에이전트의 claude_session_id를 알면 그 파일을
직접 지목한다.

값이 아니라 게터로 받는다 — id는 시작 시점에 없을 수 있다. 네이티브는
system/init이 와야 알고, 이어하기는 시작 시점에 안다. 끝내 모르는
경우(터미널 트랙에서 새로 띄운 claude)는 예전 동작으로 물러난다."
```

---

### Task 2: 할일 행 렌더링을 `TodoRows`로 추출

세 표면(스트립·사이드 패널·채팅 카드)이 같은 목록을 다르게 그리면 사용자는 서로 다른 목록으로 읽는다. 글리프·색·`in_progress`일 때 `activeForm`을 쓰는 규칙이 한 곳에만 있어야 한다. 이 태스크는 **동작을 바꾸지 않는 순수 추출**이다.

**Files:**
- Create: `client/src/components/terminal/TodoRows.tsx`
- Modify: `client/src/components/terminal/TodoStrip.tsx`

**Interfaces:**
- Consumes: `ActivityTodo` from `client/src/stores/appStore` (`{ content: string; status: string; activeForm?: string }`)
- Produces: `export function TodoRows({ todos }: { todos: ActivityTodo[] }): JSX.Element`

- [ ] **Step 1: `TodoRows` 생성**

`client/src/components/terminal/TodoRows.tsx`:

```tsx
import type { ActivityTodo } from '../../stores/appStore';

/**
 * 할일 한 줄씩. 스트립과 사이드 패널이 함께 쓴다.
 *
 * 글리프·색·취소선, 그리고 진행 중일 때 activeForm을 쓰는 규칙이 여기 한 곳에만
 * 있어야 같은 목록이 어느 표면에서든 같게 읽힌다. 두 곳에 복제하면 한쪽만 바뀌는
 * 순간 사용자에게는 서로 다른 목록으로 보인다.
 */
export function TodoRows({ todos }: { todos: ActivityTodo[] }) {
  return (
    <>
      {todos.map((todo, index) => (
        <div key={`${index}-${todo.content}`} className="flex gap-2 text-xs">
          <span className={
            todo.status === 'completed'
              ? 'text-emerald-400'
              : todo.status === 'in_progress'
                ? 'text-deck-accent'
                : 'text-deck-text-faint'
          }>
            {todo.status === 'completed' ? '✓' : todo.status === 'in_progress' ? '▸' : '○'}
          </span>
          <span className={todo.status === 'completed' ? 'text-deck-text-faint line-through' : 'text-deck-text-dim'}>
            {todo.status === 'in_progress' ? todo.activeForm || todo.content : todo.content}
          </span>
        </div>
      ))}
    </>
  );
}
```

- [ ] **Step 2: `TodoStrip`이 그것을 쓰게 한다**

`client/src/components/terminal/TodoStrip.tsx`의 import에 추가:

```tsx
import { TodoRows } from './TodoRows';
```

펼침 블록(현재 20-39행)을 통째로 교체:

```tsx
      {open && (
        <div className="max-h-44 overflow-y-auto px-3 pb-2 space-y-1">
          <TodoRows todos={todos} />
        </div>
      )}
```

- [ ] **Step 3: 타입 검사**

Run: `cd client && ./node_modules/.bin/tsc --noEmit`
Expected: 출력 없음 (exit 0)

- [ ] **Step 4: 커밋**

```bash
git add client/src/components/terminal/TodoRows.tsx client/src/components/terminal/TodoStrip.tsx
git commit -m "refactor(todo): 할일 행 렌더링을 TodoRows로 추출

곧 사이드 패널이 같은 목록을 그린다. 글리프·색·activeForm 규칙이 두 곳에
복제되면 한쪽만 바뀌는 순간 사용자에게 서로 다른 목록으로 보인다. 동작
변화 없음."
```

---

### Task 3: `TodoPanel` + Pointer Events 세로 스플리터

우측 패널의 탭 콘텐츠 **위에** 고정 구역을 둔다. 5번째 탭이 아닌 이유: 탭이면 동반 셸을 여는 순간 사라져서 "항상"이 깨진다 — 선행 설계서가 우측 탭을 거부한 것과 같은 논리다.

**Files:**
- Create: `client/src/components/terminal/TodoPanel.tsx`
- Modify: `client/src/pages/TerminalPage.tsx` (상수 ~line 66-67, `readPanelWidth` ~line 69, 상태 ~line 172-173, 우측 패널 JSX ~line 782-800)

**Interfaces:**
- Consumes: `TodoRows` (Task 2), `ActivityTodo` from `stores/appStore`, `activity` from `useAgentActivity(agentId)` (`TerminalPage.tsx:191`)
- Produces: `export function TodoPanel({ todos, height }: { todos?: ActivityTodo[]; height: number }): JSX.Element | null`

- [ ] **Step 1: `TodoPanel` 생성**

`client/src/components/terminal/TodoPanel.tsx`:

```tsx
import type { ActivityTodo } from '../../stores/appStore';
import { TodoRows } from './TodoRows';

/**
 * 우측 패널 맨 위에 고정되는 할일 구역. 항상 펼쳐져 있다 — 이 기능의 목적이
 * "찾아보지 않아도 남은 양을 아는 것"이라, 열어야 보이면 목적이 무너진다.
 *
 * 높이는 부모가 정한다(사용자가 드래그해 조절하고 localStorage에 기억한다).
 * 할일이 없으면 아무것도 그리지 않는다 — 빈 껍데기는 공간만 먹고 알려주는 게 없다.
 */
export function TodoPanel({ todos, height }: { todos?: ActivityTodo[]; height: number }) {
  if (!todos?.length) return null;
  const completed = todos.filter((todo) => todo.status === 'completed').length;
  const active = todos.find((todo) => todo.status === 'in_progress');

  return (
    <div
      className="shrink-0 flex flex-col overflow-hidden border-b border-deck-border bg-deck-surface"
      style={{ height: `${height}px` }}
    >
      <div className="shrink-0 flex items-center gap-2 px-3 py-1.5 text-xs">
        <span className="text-deck-accent">☑ {completed}/{todos.length}</span>
        {active && (
          <span className="truncate text-deck-text-dim">▸ {active.activeForm || active.content}</span>
        )}
      </div>
      <div className="flex-1 min-h-0 overflow-y-auto px-3 pb-2 space-y-1">
        <TodoRows todos={todos} />
      </div>
    </div>
  );
}
```

- [ ] **Step 2: `TerminalPage`에 상수·읽기 함수·상태 추가**

`client/src/pages/TerminalPage.tsx`의 `PANEL_MAX` 정의(67행) 다음에 추가:

```tsx
const TODO_MIN = 72;
// 고정 구역이 탭 콘텐츠를 완전히 밀어내지 못하게 막는다. 서브에이전트 패널이나
// 동반 셸이 한 줄만 남으면 그 탭은 없는 것과 같다.
const TODO_MAX_RATIO = 0.7;
const TODO_DEFAULT = 160;
```

`readPanelWidth` 함수 다음에 추가:

```tsx
// 읽는 시점에는 최소값만 자른다. 최대값은 우측 패널의 실제 높이에 비례하므로
// 렌더 전에는 알 수 없다 — 드래그 핸들러가 그때의 높이로 자른다.
function readTodoHeight(): number {
  const raw = Number(localStorage.getItem('pcd:panel:todos'));
  if (!Number.isFinite(raw) || raw <= 0) return TODO_DEFAULT;
  return Math.max(TODO_MIN, raw);
}
```

`readPanelWidth`가 `try`를 쓰지 않으므로 여기서도 쓰지 않는다 — 같은 파일 안에서 같은 일을 두 가지 방식으로 하지 않는다.

`rightWidth` 상태 선언(173행 부근) 다음에 추가:

```tsx
  const [todoHeight, setTodoHeight] = useState(readTodoHeight);
  const todoHeightRef = useRef(todoHeight);
  useEffect(() => { todoHeightRef.current = todoHeight; }, [todoHeight]);
```

`useState`/`useEffect`/`useCallback`/`useRef`는 1행에서 이미 import 되어 있다 — 추가할 것 없음.

- [ ] **Step 3: Pointer Events 드래그 핸들러 추가**

`handleMouseDown` 정의(253행) 바로 다음에 추가:

```tsx
  // 세로 스플리터는 Pointer Events로 만든다. 좌우 스플리터는 mousedown 기반이라
  // 터치에서 잡히지 않는데, 이 패널의 대상 기기에는 iPad가 포함된다(useDevice는
  // 768px 이상을 전부 데스크톱 레이아웃으로 보낸다). setPointerCapture 덕분에
  // 포인터가 핸들을 벗어나도 이동 이벤트가 계속 들어온다.
  const handleTodoResize = useCallback((e: React.PointerEvent<HTMLDivElement>) => {
    e.preventDefault();
    const handle = e.currentTarget;
    const panelHeight = handle.parentElement?.clientHeight ?? 600;
    const startY = e.clientY;
    const startHeight = todoHeightRef.current;
    const max = Math.max(TODO_MIN, Math.round(panelHeight * TODO_MAX_RATIO));
    handle.setPointerCapture(e.pointerId);
    let rafId = 0;

    const onMove = (ev: PointerEvent) => {
      cancelAnimationFrame(rafId);
      rafId = requestAnimationFrame(() => {
        setTodoHeight(Math.max(TODO_MIN, Math.min(max, startHeight + (ev.clientY - startY))));
      });
    };

    const onUp = () => {
      cancelAnimationFrame(rafId);
      // 드래그가 끝날 때 한 번만 저장한다 — 프레임마다 쓰면 수백 번이 되고,
      // 저장되는 값은 결국 마지막에 놓은 그 값이다.
      try {
        localStorage.setItem('pcd:panel:todos', String(todoHeightRef.current));
      } catch { /* private mode — 다음 번엔 기본값으로 시작한다 */ }
      handle.removeEventListener('pointermove', onMove);
      handle.removeEventListener('pointerup', onUp);
      handle.removeEventListener('pointercancel', onUp);
    };

    handle.addEventListener('pointermove', onMove);
    handle.addEventListener('pointerup', onUp);
    handle.addEventListener('pointercancel', onUp);
  }, []);
```

- [ ] **Step 4: 우측 패널 JSX에 끼워 넣는다**

`client/src/pages/TerminalPage.tsx`의 우측 패널 컨테이너(787행 부근)를 교체한다. 기존:

```tsx
            <div className="shrink-0 flex flex-col overflow-hidden min-h-0 border-l border-deck-border" style={{ width: `${rightWidth}px` }}>
              {rightTab === 'browser' ? (
                <BrowserPanel agentId={agentId} onClose={() => setRightPanelOpen(false)} />
              ) : rightTab === 'sessions' ? (
                <SessionHistory agentId={agentId} onClose={() => setRightPanelOpen(false)} />
              ) : rightTab === 'shell' ? (
                <CompanionShell agentId={agentId} onClose={() => setRightPanelOpen(false)} />
              ) : (
                <SubAgentPanel activity={activity} palette={generatePalette(agent?.colorHue ?? 220)} onClose={() => setRightPanelOpen(false)} />
              )}
            </div>
```

새 내용:

```tsx
            <div className="shrink-0 flex flex-col overflow-hidden min-h-0 border-l border-deck-border" style={{ width: `${rightWidth}px` }}>
              {/* 탭보다 위에 고정한다. 탭으로 만들면 동반 셸을 여는 순간 사라져서
                  "항상 보인다"는 약속이 깨진다. */}
              {!!activity?.todos?.length && (
                <>
                  <TodoPanel todos={activity.todos} height={todoHeight} />
                  <div
                    className="h-1 shrink-0 cursor-row-resize touch-none bg-purple-500 opacity-0 hover:opacity-100 transition-opacity"
                    onPointerDown={handleTodoResize}
                  />
                </>
              )}
              <div className="flex-1 min-h-0 overflow-hidden flex flex-col">
                {rightTab === 'browser' ? (
                  <BrowserPanel agentId={agentId} onClose={() => setRightPanelOpen(false)} />
                ) : rightTab === 'sessions' ? (
                  <SessionHistory agentId={agentId} onClose={() => setRightPanelOpen(false)} />
                ) : rightTab === 'shell' ? (
                  <CompanionShell agentId={agentId} onClose={() => setRightPanelOpen(false)} />
                ) : (
                  <SubAgentPanel activity={activity} palette={generatePalette(agent?.colorHue ?? 220)} onClose={() => setRightPanelOpen(false)} />
                )}
              </div>
            </div>
```

`touch-none`(= `touch-action: none`)이 없으면 iPad에서 드래그가 페이지 스크롤로 먹힌다. 탭 콘텐츠를 `flex-1 min-h-0`로 감싸는 이유는, 고정 구역이 형제로 들어오면서 기존 패널들이 남은 높이를 채우도록 명시해야 하기 때문이다.

import를 파일 상단에 추가한다:

```tsx
import { TodoPanel } from '../components/terminal/TodoPanel';
```

- [ ] **Step 5: 타입 검사**

Run: `cd client && ./node_modules/.bin/tsc --noEmit`
Expected: 출력 없음 (exit 0)

- [ ] **Step 6: 커밋**

```bash
git add client/src/components/terminal/TodoPanel.tsx client/src/pages/TerminalPage.tsx
git commit -m "feat(todo): 우측 패널 탭 위에 항상 보이는 할일 구역

계획을 세운 뒤 구현하는 동안 남은 양이 찾아보지 않아도 보이게 한다.
탭이 아니라 탭 위 고정 구역인 이유: 탭이면 동반 셸을 여는 순간 사라져서
'항상'이 깨진다.

높이는 드래그로 조절하고 localStorage에 기억한다. 좌우 스플리터를
복사하지 않고 Pointer Events로 새로 만든 이유는 그것들이 mousedown
전용이라 iPad에서 잡히지 않기 때문이다. 패널이 닫혀 있으면 중앙
스트립이 계속 숫자를 보여주므로 개수는 어느 상태에서도 잃지 않는다."
```

---

### Task 4: 실물 확인 · 문서 · `pcd.exe`

**Files:**
- Modify: `docs/superpowers/specs/2026-08-12-todo-sidebar-design.md` (상태 줄)
- Modify: `CHANGELOG.md`, `ROADMAP.md`
- Modify: `dist/pcd.exe` (재빌드)

**Interfaces:**
- Consumes: Task 1~3의 결과물 전부
- Produces: 없음 (출하 준비)

- [ ] **Step 1: 전체 자동 검증**

```bash
cd server && CGO_ENABLED=0 go build ./... && CGO_ENABLED=0 go test ./...
cd ../client && ./node_modules/.bin/tsc --noEmit
```
Expected: 서버 전 패키지 ok, tsc 출력 없음

- [ ] **Step 2: 실물 확인 — 사람이 직접**

배포한 뒤 브라우저에서 확인한다. 자동 테스트로는 잡히지 않는 것들이다.

- 계획을 세우는 세션에서 패널이 실시간으로 차오르는가 (`☑ 1/6` → `☑ 2/6` …)
- 탭을 동반 셸/브라우저로 바꿔도 할일 구역이 남는가
- **iPad에서 손가락으로** 스플리터를 잡아 높이가 조절되는가 — 마우스로만 되면 대상 기기의 절반에서 못 쓴다
- 높이가 새로고침 후에도 유지되는가
- 우측 패널을 닫으면 중앙 스트립이 `☑ n/m`을 계속 보여주는가
- 할일이 없는 세션에서 고정 구역도 스플리터도 뜨지 않는가
- 한 프로젝트에 세션 둘을 띄우고 각자 자기 목록만 보는가 (Task 1의 목적)
- 모바일에서 달라진 게 없는가

- [ ] **Step 3: 스펙 상태 줄 갱신**

`docs/superpowers/specs/2026-08-12-todo-sidebar-design.md`의 4행을 바꾼다:

```markdown
- 상태: 승인됨 (구현 대기)
```
→
```markdown
- 상태: 구현됨 (v0.6.1)
```

이 리포의 설계서 네 개가 전부 "구현 대기"로 남아 있어서, 출하된 기능을 미구현으로 오해하게 만든 적이 있다. 같은 부채를 새로 만들지 않는다.

- [ ] **Step 4: CHANGELOG / ROADMAP 갱신**

`CHANGELOG.md` 맨 위에 v0.6.1 항목을 추가한다 — 할일 체크리스트 복구(`TaskCreate`/`TaskUpdate` 인식), 할일 사이드 패널, 워처 트랜스크립트 지목 수정, 권한 모드 제자리 전환.

`ROADMAP.md`에서 두 가지를 고친다:
1. 3행의 현재 버전을 v0.6.1로.
2. 73~82행의 낡은 서술 — "bypassPermissions에서는 승인 브리지를 의도적으로 넘기지 않는다", "autoDecide의 bypass 분기는 backstop", "전체 허용에서만 배너를 숨긴다" 세 가지가 2026-08-10 수정으로 전부 뒤집혔다. 지금은 브리지가 모든 모드에서 붙고, bypass는 CLI에 넘기지 않는 서버 정책이다.

- [ ] **Step 5: `pcd.exe` 재빌드**

```bash
cd client && ./node_modules/.bin/vite build
cd .. && find server/static -mindepth 1 -delete; rmdir server/static
cp -r client/dist server/static
cd server && GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -o ../dist/pcd.exe .
```

- [ ] **Step 6: 커밋**

```bash
git add docs/superpowers/specs/2026-08-12-todo-sidebar-design.md CHANGELOG.md ROADMAP.md dist/pcd.exe
git commit -m "docs(release): v0.6.1 — 할일 사이드 패널 · 체크리스트 복구 · 권한 모드 제자리 전환

ROADMAP의 낡은 서술도 바로잡는다: 승인 브리지를 bypass에서 떼는 것이
맞다고 적혀 있었으나, 2026-08-10 수정으로 브리지는 모든 모드에서 붙고
전체 허용은 CLI에 넘기지 않는 서버 정책이 됐다."
```

---

## Self-Review

**1. 스펙 커버리지**

| 스펙 섹션 | 태스크 |
|---|---|
| §1 서버 — 트랜스크립트 지목 (게터 주입, 폴백) | Task 1 |
| §1 수용하는 한계 (터미널 트랙) | Task 1 Step 3 주석 + 폴백 테스트 |
| §2 `TodoRows` 공용 추출 | Task 2 |
| §2 `TodoPanel` 탭 위 고정, 빈 목록이면 미렌더 | Task 3 Step 1·4 |
| §2 가로 스플리터, `pcd:panel:todos`, 최소 72px / 최대 70% | Task 3 Step 2·3 |
| §2 Pointer Events (좌우 스플리터 복사 금지) | Task 3 Step 3 |
| §3 패널 닫힘 → 중앙 스트립 유지 | 기존 동작 유지 (Task 3에서 중앙 `TodoStrip` 미변경), Task 4 Step 2에서 확인 |
| §3 모바일 무변경 | Global Constraints + Task 4 Step 2 |
| §4 에러 처리 (todos 없음 / 파일 없음 / 저장값 범위 밖) | Task 3 Step 1·2, Task 1 폴백 테스트 |
| §5 검증 | Task 4 Step 1·2 |
| §5 `dist/pcd.exe` 재빌드 | Task 4 Step 5 |

빠진 항목 없음.

**2. 플레이스홀더 점검**

"TBD"/"적절한 에러 처리"/"위와 비슷하게" 없음. 모든 코드 단계에 실제 코드가 들어 있다.

**3. 타입 일관성**

- `TodoRows({ todos: ActivityTodo[] })` — Task 2에서 정의, Task 3의 `TodoPanel`에서 같은 시그니처로 사용.
- `TodoPanel({ todos?: ActivityTodo[]; height: number })` — Task 3 Step 1에서 정의, Step 4에서 `todos={activity.todos} height={todoHeight}`로 호출. `todos`가 optional이라 `activity.todos`(optional)를 그대로 넘길 수 있다.
- `targetTranscript()`/`sessionIDFor` — Task 1 Step 1의 테스트가 쓰는 이름과 Step 3의 정의가 일치.
- `SetSessionIDLookup(func(agentID string) string)` — Step 6의 정의와 `agentSvc.ClaudeSessionID(id string) string`의 시그니처가 일치.
- `TODO_MIN`/`TODO_MAX_RATIO`/`TODO_DEFAULT` — Step 2에서 정의, Step 3에서 사용.
