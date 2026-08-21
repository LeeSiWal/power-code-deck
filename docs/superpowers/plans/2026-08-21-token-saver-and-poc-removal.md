# 로컬 인텔리전스 제거와 토큰 세이버 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 측정이 값어치를 부정한 것(로컬 LLM 전처리)을 걷어내고, 측정이 값어치를 증명한 것(탐색 상한 지시문)만 남긴다.

**Architecture:** 2026-08-21 측정(리포트 §7a~§7d)의 결론은 셋이다. 하이브리드 전처리는 절감이 없다. 절감을 만든 것은 **지시문 한 문단**이다(−40%, 로컬 모델 없이, 지연 추가 0). 그리고 결정론적 지도조차 비용을 못 줄였다.

그래서 이 계획은 두 단계다. **먼저 제거하고, 그 다음에 넣는다.** 순서가 뒤집히면 안 된다 — 지금 `native:input` 전송 경로에는 전처리 배너·트레이스 구독·취소 버튼·모드 게이팅이 얽혀 있고, 토큰 세이버가 건드릴 곳이 정확히 거기다. 단순해진 경로에 넣는 것이 얽힌 경로에 하나 더 얹는 것보다 안전하다.

토큰 세이버는 새 배관을 만들지 않는다. `NativeService.SendWithDisplayText(sessionID, driverText, displayText)`가 **"보낼 프롬프트와 화면에 띄울 텍스트를 분리"**하는 일을 이미 한다(하이브리드가 쓰던 그 함수이고, 하이브리드가 사라져도 이 함수는 남는다).

**Tech Stack:** Go 1.25 표준 라이브러리, React 18 + TypeScript, Zustand. 새 의존성 0.

## Global Constraints

- 근거: `docs/local-intelligence-poc-report.md` §7a~§7d. **리포트와 계획 문서는 지우지 않는다** — 왜 뺐는지의 기록이고, 그게 없으면 6개월 뒤에 같은 걸 다시 만든다.
- **DB 테이블은 건드리지 않는다.** `local_ai_providers`·`intelligence_traces`는 그대로 둔다. 그 트레이스가 **리포트 결론의 증거**다. 코드만 걷어내고 테이블은 남긴다. 파괴적 마이그레이션 금지.
- **집중 모드는 기본 꺼짐이다.** 측정한 것은 설명형 질문 하나다. "이 기능 구현해줘"에 파일 3개 상한을 걸면 망가진다.
- `CLOUD_ONLY`가 작업을 바이트 그대로 넘긴다는 성질은 **집중 모드가 꺼져 있을 때 그대로 유지**된다. 켰을 때만 감싼다.
- 새 Go 모듈·npm 패키지 금지.
- 서버 검증: `cd server && CGO_ENABLED=0 go build ./... && CGO_ENABLED=0 go test ./...`
- 클라이언트 검증: `cd client && ./node_modules/.bin/tsc --noEmit`
- 마지막에 `dist/pcd.exe` 재빌드.

## 남기는 것 / 걷는 것

이 작업으로 태어났지만 **인텔리전스와 무관하게 값이 있는 것은 남긴다:**

| 남김 | 이유 |
|---|---|
| `StreamUsage` 파싱 (`claude_stream.go`) | 턴 비용을 읽는 유일한 경로. 세션 누적 비용이 이걸 쓴다 |
| Codex stderr 꼬리 (`codex_driver.go`) | 기동 실패 원인을 보이게 한 것. 인텔리전스와 무관 |
| `nativeResultEventWithUsage` | Codex가 턴을 닫는 경로. 인자는 항상 nil이 되지만 함수는 남는다 |
| 파생 세션 CLI 수정 (`sessions.go`) | 별개 버그 수정 |
| 드라이버 재시도 통합 (`native_service.go`) | 별개 버그 수정 |
| `SendWithDisplayText` | **토큰 세이버가 쓴다** |
| 리포트·계획·스펙 문서 | 제거의 근거 기록 |

## File Structure

| 파일 | 책임 | 상태 |
|---|---|---|
| `server/services/intelligence.go` | — | **삭제** |
| `server/handlers/intelligence.go` | — | **삭제** |
| `server/services/intelligence_test.go` · `intelligence_job_test.go` · `intelligence_savings_test.go` · `intelligence_savings_live_test.go` · `codex_usage_live_test.go` | — | **삭제** |
| `server/handlers/intelligence_test.go` | — | **삭제** |
| `server/main.go` | 라우트·emitter 배선 제거 | 수정 |
| `server/ws/message.go` | `EventIntelligenceTrace` 제거, `NativeInputPayload.Focused` 추가 | 수정 |
| `server/ws/hub.go` | 집중 모드 래핑 | 수정 |
| `server/services/focus_prompt.go` | 지시문 한 곳 | 생성 |
| `server/services/focus_prompt_test.go` | 지시문 계약 고정 | 생성 |
| `client/src/components/intelligence/**` · `settings/LocalIntelligenceSettings.tsx` | — | **삭제** |
| `client/src/components/native/NativeChat.tsx` | 인텔리전스 결합 해제 + 집중 토글 + 세션 비용 | 수정 |
| `client/src/lib/api.ts` · `stores/appStore.ts` · `hooks/useWebSocket.ts` · `pages/SettingsPage.tsx` | 인텔리전스 제거 | 수정 |
| `client/src/lib/ws.ts` | `focused` 전달 | 수정 |

---

### Task 1: 서버에서 인텔리전스를 걷는다

**Files:**
- Delete: `server/services/intelligence.go`, `server/handlers/intelligence.go`, `server/services/intelligence_test.go`, `server/services/intelligence_job_test.go`, `server/services/intelligence_savings_test.go`, `server/services/intelligence_savings_live_test.go`, `server/services/codex_usage_live_test.go`, `server/handlers/intelligence_test.go`
- Modify: `server/main.go`, `server/ws/message.go`

- [ ] **Step 1: 삭제**

```bash
cd server
git rm services/intelligence.go handlers/intelligence.go \
  services/intelligence_test.go services/intelligence_job_test.go \
  services/intelligence_savings_test.go services/intelligence_savings_live_test.go \
  services/codex_usage_live_test.go handlers/intelligence_test.go
```

- [ ] **Step 2: 컴파일러에게 나머지를 찾게 한다**

Run: `cd server && CGO_ENABLED=0 go build ./... 2>&1 | head -20`
Expected: `main.go`에서 `services.NewIntelligenceService`·`handlers.RunIntelligence` 등 미정의 에러.

- [ ] **Step 3: `main.go` 정리**

지운다:

```go
	intelligenceSvc := services.NewIntelligenceService(database, providerRegistry, agentSvc, nativeSvc)

	intelligenceSvc.SetEmitter(func(r services.IntelligenceRunResult) {
		hub.BroadcastAll(ws.EventIntelligenceTrace, ws.IntelligenceTracePayload(r))
	})
```

그리고 라우트 여섯 줄 전부:

```go
	api.HandleFunc("/intelligence/providers", …)
	api.HandleFunc("/intelligence/providers/{name}", …)          // PUT
	api.HandleFunc("/intelligence/providers/{name}", …)          // DELETE
	api.HandleFunc("/intelligence/providers/{name}/health", …)
	api.HandleFunc("/intelligence/run", …)
	api.HandleFunc("/intelligence/traces", …)
	api.HandleFunc("/intelligence/traces/{id}", …)
	api.HandleFunc("/intelligence/traces/{id}/cancel", …)
```

`providerRegistry := services.NewProviderRegistry(database)`도 지운다 — 유일한 사용처가 인텔리전스다.

- [ ] **Step 4: `ws/message.go` 정리**

`EventIntelligenceTrace` 상수와 `IntelligenceTracePayload` 타입 별칭을 지운다.

- [ ] **Step 5: 빌드와 테스트**

Run: `cd server && CGO_ENABLED=0 go build ./... && CGO_ENABLED=0 go test ./...`
Expected: 전 패키지 ok

- [ ] **Step 6: 마이그레이션은 손대지 않았는지 확인**

Run: `git diff --stat server/db/`
Expected: 출력 없음. **테이블은 남는다** — 그 안의 트레이스가 리포트 결론의 증거다.

- [ ] **Step 7: 커밋**

```bash
git add -A server/
git commit -m "refactor(server): 로컬 인텔리전스 POC 제거 (측정 결과 §7a-§7d)"
```

---

### Task 2: 클라이언트에서 인텔리전스를 걷는다

**Files:**
- Delete: `client/src/components/intelligence/` (전체), `client/src/components/settings/LocalIntelligenceSettings.tsx`
- Modify: `client/src/components/native/NativeChat.tsx`, `client/src/lib/api.ts`, `client/src/stores/appStore.ts`, `client/src/hooks/useWebSocket.ts`, `client/src/pages/SettingsPage.tsx`

- [ ] **Step 1: 삭제**

```bash
git rm -r client/src/components/intelligence
git rm client/src/components/settings/LocalIntelligenceSettings.tsx
```

- [ ] **Step 2: 컴파일러에게 나머지를 찾게 한다**

Run: `cd client && ./node_modules/.bin/tsc --noEmit 2>&1 | head -30`
Expected: `NativeChat.tsx`·`SettingsPage.tsx`·`useWebSocket.ts`의 import 에러.

- [ ] **Step 3: `SettingsPage.tsx`**

`LocalIntelligenceSettings`와 `IntelligenceActivity`의 import와 사용을 지운다.

- [ ] **Step 4: `useWebSocket.ts`**

`intelligence:trace` 구독과, 재연결 시 `api.intelligenceTraces(20)`를 부르는 `'open'` 핸들러를 지운다.

- [ ] **Step 5: `appStore.ts`**

`intelligenceRuns`·`applyIntelligenceRun`·`seedIntelligenceRuns`와 인터페이스 선언, 그리고 `isTraceTerminal` import를 지운다.

- [ ] **Step 6: `api.ts`**

지운다: `IntelligenceMode`·`IntelligenceTrace`·`IntelligenceTraceEvent`·`IntelligenceStartResult`·`IntelligenceRunResult`·`LocalProvider` 타입, `isTraceTerminal`·`isTraceRunning`·`isLocalPhaseRunning`·`TERMINAL_TRACE_STATUS`, 그리고 `api` 객체의 `localProviders`·`putLocalProvider`·`deleteLocalProvider`·`localProviderHealth`·`runIntelligence`·`cancelIntelligence`·`intelligenceTraces`·`intelligenceTrace`.

- [ ] **Step 7: `NativeChat.tsx` 결합 해제**

지우는 것: `intelligenceMode`·`localProviders`·`providersLoading`·`providersError`·`localProvider`·`localOperation`·`activeTraceId`·`intelligenceStarting`·`intelligenceNotice`·`localOutput`·`intelligenceRefreshKey`·`restoreOnFailure` 상태, `savedIntelligenceMode`·`savedLocalOperation`·`traceFromApiError`·`localErrorLabel`·`failRun`·`cancelIntelligenceRun`, 트레이스 결과 effect, 전처리 배너, `<ExecutionModeControl>`·`<SessionSavingsSummary>` 렌더, 그리고 `send`의 인텔리전스 분기 전체.

`send`는 이렇게 남는다 — 첨부·클라이언트 명령 처리는 그대로고, 라우팅만 사라진다:

```tsx
    const msg = attachments.length
      ? (text ? text + '\n\n' : '') + '첨부 파일 (Read 도구로 확인해줘):\n' + attachments.map((a) => a.path).join('\n')
      : text;
    sendText(msg);
```

`cloudTargetName`은 `executionRouting.ts`와 함께 사라지므로, 쓰이는 곳이 남아 있으면 NativeChat 안에 인라인한다:

```tsx
const cloudTargetName = (d: NativeDriverName) => (d === 'codex' ? 'Codex' : 'Claude Code');
```

`clientCommand`도 `executionRouting.ts`에 있었다. **이건 인텔리전스와 무관한 로직**(`/clear`·`/plugin` 가로채기)이므로 `client/src/lib/nativeCommands.ts`로 옮긴다:

```ts
export type NativeDriverName = 'codex' | 'claude';

export function clientCommand(text: string): 'clear' | 'plugin' | 'native' | null {
  if (text === '/clear') return 'clear';
  if (/^\/plugins?(\s|$)/.test(text)) return 'plugin';
  if (/^[\/@][\w:-]+(?:\s|$)/.test(text)) return 'native';
  return null;
}
```

- [ ] **Step 8: 타입 검사**

Run: `cd client && ./node_modules/.bin/tsc --noEmit`
Expected: PASS

- [ ] **Step 9: 잔여 확인**

Run: `grep -rn "intelligence\|Intelligence" client/src --include=*.ts --include=*.tsx | grep -v "lib/nativeCommands"`
Expected: 출력 없음

- [ ] **Step 10: 커밋**

```bash
git add -A client/
git commit -m "refactor(client): 로컬 인텔리전스 UI 제거"
```

---

### Task 3: 집중 모드 지시문 — 서버

**Files:**
- Create: `server/services/focus_prompt.go`, `server/services/focus_prompt_test.go`
- Modify: `server/ws/message.go`, `server/ws/hub.go`

**Interfaces:**
- Produces: `func FocusedPrompt(task string) string`
- Produces: `NativeInputPayload.Focused bool` (`json:"focused"`)

- [ ] **Step 1: 실패하는 테스트 작성**

`server/services/focus_prompt_test.go`:

```go
package services

import (
	"strings"
	"testing"
)

// Measured 2026-08-21 (report §7c): this instruction alone took a turn from 25 tool
// calls / $1.1949 to 10-13 calls / $0.7113 — no local model, no added latency, and
// the answers were checked against the source. Cost is Sum over steps of
// (conversation re-read), so cutting the survey is what cuts the bill.
func TestFocusedPromptKeepsTheTaskAndCapsTheSurvey(t *testing.T) {
	const task = "이 저장소의 승인 흐름을 설명해줘"
	prompt := FocusedPrompt(task)

	if !strings.Contains(prompt, task) {
		t.Fatalf("the user's task must survive verbatim: %q", prompt)
	}
	if !strings.Contains(prompt, "at most 3 files") {
		t.Fatalf("no read cap: %q", prompt)
	}
	if !strings.Contains(prompt, "Do not survey") {
		t.Fatalf("nothing stops the survey, which is the whole saving: %q", prompt)
	}
	// The safety catch: a cheap wrong answer is the risk of this framing, and it can
	// only be judged if the model admits when the budget was not enough.
	if !strings.Contains(prompt, "do not guess") {
		t.Fatalf("no honesty requirement: %q", prompt)
	}
}

// Empty in, empty out: the caller must not be able to turn "nothing typed" into a
// turn that spends money on an instruction with no task.
func TestFocusedPromptRefusesEmptyTask(t *testing.T) {
	if got := FocusedPrompt("   "); got != "" {
		t.Fatalf("FocusedPrompt(blank) = %q, want empty", got)
	}
}
```

- [ ] **Step 2: 실패 확인**

Run: `cd server && CGO_ENABLED=0 go test ./services/ -run TestFocusedPrompt`
Expected: FAIL — `undefined: FocusedPrompt`

- [ ] **Step 3: 구현**

`server/services/focus_prompt.go`:

```go
package services

import "strings"

// FocusedPrompt wraps a task so the agent answers it instead of surveying the
// repository for it.
//
// Measured 2026-08-21, same repo and question, fresh session per run
// (docs/local-intelligence-poc-report.md §7c):
//
//	no cap   25 tool calls   cache read 875,958   $1.1949
//	capped   10-13 calls     cache read ~429,903  $0.7113
//
// A turn's bill is the sum over steps of the conversation re-read from cache, so a
// file opened early is paid for again on every later step. Capping the reads cuts
// that multiplication; the answers stayed correct and roughly the same length.
//
// The last clause is the safety catch, not politeness. The risk of this framing is a
// cheap WRONG answer, and the only way to tell one apart is to require the model to
// say when the budget was not enough. In the measured runs it did exactly that once,
// and that run was right to refuse.
//
// This is opt-in per turn. What was measured is an explanatory question; a task that
// must edit code needs to open whatever it needs, and a 3-file cap would damage it.
func FocusedPrompt(task string) string {
	trimmed := strings.TrimSpace(task)
	if trimmed == "" {
		return ""
	}
	return "Answer the user task from this repository. You may open at most 3 files, " +
		"and only ones you have concrete reason to believe are relevant. Do not survey " +
		"the repository.\nIf you cannot answer confidently within that budget, say so " +
		"explicitly and list what you would need to read — do not guess.\n\nUSER TASK\n" +
		trimmed
}
```

- [ ] **Step 4: 통과 확인**

Run: `cd server && CGO_ENABLED=0 go test ./services/ -run TestFocusedPrompt -v`
Expected: PASS (2건)

- [ ] **Step 5: 페이로드에 플래그 추가**

`server/ws/message.go`의 `NativeInputPayload`:

```go
type NativeInputPayload struct {
	AgentID string `json:"agentId"`
	Text    string `json:"text"`
	// Focused wraps the turn with a read cap before sending it (services.FocusedPrompt).
	// Off by default: the cap was measured on an explanatory question and is wrong for
	// a task that must edit code.
	Focused bool `json:"focused"`
}
```

- [ ] **Step 6: 허브에서 감싼다**

`server/ws/hub.go`의 `case EventNativeInput`에서 `h.native.Send(...)` 한 줄을 바꾼다:

```go
		// The user sees what they typed; the agent gets the capped version. This is
		// the same split SendWithDisplayText was built for, so the transcript never
		// shows scaffolding the user did not write.
		sendErr := h.native.Send(payload.AgentID, payload.Text)
		if payload.Focused {
			if wrapped := services.FocusedPrompt(payload.Text); wrapped != "" {
				sendErr = h.native.SendWithDisplayText(payload.AgentID, wrapped, payload.Text)
			}
		}
		if err := sendErr; err != nil {
```

**주의:** 위처럼 쓰면 `Send`가 **먼저 실행돼 버린다.** 실제 코드는 분기를 먼저 정하고 한 번만 보내야 한다:

```go
		var sendErr error
		if wrapped := services.FocusedPrompt(payload.Text); payload.Focused && wrapped != "" {
			// The user sees what they typed; the agent gets the capped version — the
			// same split SendWithDisplayText was built for, so the transcript never
			// shows scaffolding the user did not write.
			sendErr = h.native.SendWithDisplayText(payload.AgentID, wrapped, payload.Text)
		} else {
			sendErr = h.native.Send(payload.AgentID, payload.Text)
		}
		if err := sendErr; err != nil {
```

- [ ] **Step 7: 빌드와 전체 테스트**

Run: `cd server && CGO_ENABLED=0 go build ./... && CGO_ENABLED=0 go test ./...`
Expected: 전 패키지 ok

- [ ] **Step 8: 커밋**

```bash
git add server/services/focus_prompt.go server/services/focus_prompt_test.go server/ws/message.go server/ws/hub.go
git commit -m "feat(native): 집중 모드 — 탐색 상한 지시문 (기본 꺼짐)"
```

---

### Task 4: 집중 모드 토글 — 클라이언트

**Files:**
- Modify: `client/src/lib/ws.ts`, `client/src/components/native/NativeChat.tsx`

- [ ] **Step 1: `ws.ts`가 플래그를 전달하는지 확인**

Run: `grep -n "send(event" -A 6 client/src/lib/ws.ts | head -12`
Expected: `send(event, payload)`가 payload를 그대로 직렬화한다 — **ws.ts는 수정이 필요 없다.**

- [ ] **Step 2: 상태와 전송**

`NativeChat.tsx`에 추가한다. 저장 키는 기존 모드 저장 관례를 따른다:

```tsx
  const [focused, setFocused] = useState(
    () => localStorage.getItem(`pcd:focused:${agentId}`) === '1',
  );

  const toggleFocused = useCallback(() => {
    setFocused((on) => {
      localStorage.setItem(`pcd:focused:${agentId}`, on ? '0' : '1');
      return !on;
    });
  }, [agentId]);
```

`sendText`가 플래그를 함께 보내게 한다:

```tsx
  const sendText = useCallback((text: string) => {
    if (!text.trim()) return;
    setError('');
    agentDeckWS.send('native:input', { agentId, text, focused });
    markJustSent();
  }, [agentId, focused, markJustSent]);
```

- [ ] **Step 3: 토글 UI**

컴포저의 전송 버튼 왼쪽에 놓는다. **측정 사실을 title에 적는다** — 하이브리드에 붙인 것과 같은 규율이다:

```tsx
            <button
              type="button"
              onClick={toggleFocused}
              aria-pressed={focused}
              className={`shrink-0 min-h-8 rounded-lg px-2 text-[11px] transition-colors ${
                focused ? 'bg-deck-accent text-white' : 'border border-deck-border text-deck-text-dim'
              }`}
              title={focused
                ? '집중: 파일 3개까지만 열고 저장소를 훑지 않습니다. 설명·질문에 적합하고, 코드를 고치는 작업에는 끄세요.'
                : '집중 모드 — 측정 기준 툴 호출 25→11회, 비용 −40% (설명형 질문 기준)'}
            >
              집중
            </button>
```

- [ ] **Step 4: 켜져 있을 때 한 줄 안내**

컴포저 위, 기존 배너들과 같은 자리에:

```tsx
        {focused && (
          <div className="mx-2 mt-2 rounded-lg border border-deck-accent/20 bg-deck-accent/5 px-3 py-1.5 text-[10px] leading-relaxed text-deck-accent-light">
            집중 모드: 파일 3개까지만 열고, 부족하면 추측 대신 무엇이 필요한지 말합니다.
            코드를 수정하는 작업에는 끄세요.
          </div>
        )}
```

- [ ] **Step 5: 타입 검사**

Run: `cd client && ./node_modules/.bin/tsc --noEmit`
Expected: PASS

- [ ] **Step 6: 커밋**

```bash
git add client/src/components/native/NativeChat.tsx
git commit -m "feat(native): 집중 모드 토글"
```

---

### Task 5: 세션 누적 비용

**Files:**
- Modify: `client/src/components/native/NativeChat.tsx`

턴마다 숫자를 뿌리는 것은 이미 한 번 노이즈로 판정돼 제거됐다(`NativeChat.tsx`의 result 렌더 주석). **합계 한 줄**은 다른 물건이다 — 교환 사이에 끼지 않고, 화면에 하나만 있는다.

- [ ] **Step 1: 이벤트에서 누적**

`costUsd`는 이미 파싱돼 있다(`lib/nativeEvents.ts`의 `kind: 'result'`). 채팅 아이템에서 합산한다:

```tsx
  // One number, not a per-turn counter: the per-turn row was removed as noise
  // between exchanges. This answers "what is this session costing me", which is the
  // question the focus toggle above is supposed to change the answer to.
  const sessionCostUsd = useMemo(
    () => items.reduce((sum, item) => (
      item.kind === 'result' && typeof item.costUsd === 'number' ? sum + item.costUsd : sum
    ), 0),
    [items],
  );
```

`items`는 이 컴포넌트가 이미 들고 있는 접힌 채팅 아이템 배열이다. 이름이 다르면 그 이름을 쓴다(`foldEvents`의 결과).

- [ ] **Step 2: 헤더에 한 줄**

세션 헤더(모델·모드 표시가 있는 줄) 오른쪽에 놓는다. 0이면 숨긴다 — 아직 아무 턴도 끝나지 않았다는 뜻이고, `$0.00`은 사실이 아니다:

```tsx
        {sessionCostUsd > 0 && (
          <span
            className="shrink-0 text-[10px] tabular-nums text-deck-text-faint"
            title="이 세션에서 끝난 턴들의 비용 합계입니다. 드라이버가 비용을 보고할 때만 셉니다 (Codex는 보고하지 않습니다)."
          >
            ${sessionCostUsd.toFixed(2)}
          </span>
        )}
```

- [ ] **Step 3: 타입 검사**

Run: `cd client && ./node_modules/.bin/tsc --noEmit`
Expected: PASS

- [ ] **Step 4: 커밋**

```bash
git add client/src/components/native/NativeChat.tsx
git commit -m "feat(native): 세션 누적 비용 한 줄"
```

---

### Task 6: 실물 확인 · 문서 · 바이너리

- [ ] **Step 1: 집중 모드가 실제로 스텝을 줄이는지**

Claude 세션에서 같은 질문을 두 번 던진다 — **매번 새 세션으로**(이전 턴이 입력 토큰에 실려 비교가 오염된다).

1. 집중 끄고: "이 저장소의 승인 흐름을 설명해줘"
2. 집중 켜고: 같은 질문

확인할 것:
- 화면의 사용자 메시지가 **양쪽 다 원문 그대로**여야 한다. 지시문이 보이면 `SendWithDisplayText` 배선이 틀린 것이다.
- 집중 쪽 답변이 **어떤 파일을 열었는지 밝히고**, 부족하면 무엇이 필요한지 말해야 한다.
- 헤더의 세션 비용이 집중 쪽에서 **눈에 띄게 낮아야** 한다(측정 기준 $1.19 → $0.71).

- [ ] **Step 2: 회귀 — 집중이 꺼져 있으면 오늘과 같아야 한다**

첨부 파일이 있는 턴, `/clear`, `/plugin`, `@`로 시작하는 명령이 전부 이전처럼 동작하는지 확인한다. 이 넷은 `send`의 같은 경로를 지난다.

- [ ] **Step 3: 리포트에 결말을 적는다**

`docs/local-intelligence-poc-report.md` 최상단에 한 문단을 더한다:

```markdown
> **결말 (2026-08-21):** 이 POC의 코드는 제거됐다. §7a~§7d의 측정이 하이브리드 전처리에
> 절감이 없음을 보였고, 절감을 만든 것은 로컬 모델이 아니라 탐색 상한 지시문이었다
> (−40%, 지연 추가 0). 그 지시문만 "집중 모드"로 남았다(`services/focus_prompt.go`).
> `local_ai_providers`·`intelligence_traces` 테이블은 남겨뒀다 — 그 안의 트레이스가
> 이 결론의 증거다. 이 문서는 왜 뺐는지의 기록으로 남는다.
```

- [ ] **Step 4: README에서 사라진 기능 정리**

Run: `grep -n "Local Intelligence\|하이브리드\|Hybrid" README.md`
그 결과가 있으면 집중 모드 설명으로 대체하거나 지운다. **없는 기능을 문서가 광고하면 안 된다.**

- [ ] **Step 5: 재빌드**

```bash
cd client && ./node_modules/.bin/vite build && cd ..
rm -rf server/static && cp -r client/dist server/static
cd server && GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -o ../dist/pcd.exe .
```

- [ ] **Step 6: 커밋**

```bash
git add docs/local-intelligence-poc-report.md README.md dist/pcd.exe
git commit -m "docs: 로컬 인텔리전스 POC의 결말과 집중 모드"
```

## Self-Review

- **제거를 먼저 하는 것이 이 계획의 설계다.** 얽힌 전송 경로에 기능을 하나 더 얹는 것보다, 단순해진 경로에 넣는 것이 안전하다. Task 2 Step 7이 그 경로를 12개 상태에서 0개로 되돌린다.
- **테이블을 남기는 것은 게으름이 아니다.** 그 트레이스가 이 제거를 정당화하는 증거다. 코드를 지우면서 근거를 같이 지우면, 6개월 뒤에 같은 걸 다시 만든다.
- **집중 모드가 기본 꺼짐인 이유를 UI가 말한다.** 측정 범위는 저장소 하나·설명형 질문 하나·n=3이다. 코드를 고치는 작업에 3파일 상한은 해롭고, 토글의 title과 배너가 그걸 적는다.
- **`SendWithDisplayText`를 쓰는 것이 핵심이다.** 지시문이 사용자 메시지로 보이면 사용자는 자기가 쓰지 않은 문장을 자기 것으로 보게 된다. Task 6 Step 1이 그걸 첫 번째로 확인한다.
- 위험: Task 2 Step 7이 가장 크다. `send`는 첨부·클라이언트 명령·전송이 모두 지나는 자리이고, 인텔리전스 분기를 걷어내면서 그 셋 중 하나를 같이 건드리기 쉽다. Step 2의 회귀 확인이 그래서 넷을 다 밟는다.
