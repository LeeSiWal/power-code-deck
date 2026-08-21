# Local Intelligence 잡 전환 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 로컬 추론을 HTTP 요청의 수명에서 떼어낸다. 지금은 브라우저나 리버스 프록시가 요청을 끊으면 추론이 통째로 죽는다 — 프로덕션 트레이스 5건이 정확히 이것이다.

**Architecture:** `handlers/intelligence.go:69`가 `svc.Run(r.Context(), req)`로 **HTTP 요청 컨텍스트를 그대로** 서비스에 넘긴다. 성공한 로컬 추론도 38–59초가 걸리므로(실측 8건) 이건 LAN에서도 아슬아슬하고 원격에서는 성립하지 않는다.

`Run`을 두 조각으로 가른다: 검증·트레이스 생성은 요청 안에서 동기로 끝내고(잘못된 입력은 즉시 400), 실행은 요청과 무관한 컨텍스트에서 고루틴으로 돈다. 진행 상황은 **이미 있는 WS 허브**로 나간다 — `ControlRoomService.SetEmitter` → `hub.BroadcastAll`(`main.go:133-135`)이 그대로 쓸 수 있는 선례다. 새 전송로도, 폴링도, 새 의존성도 없다.

부수 효과가 요구사항이다: **모바일에서 실행을 걸고 화면을 꺼도 완주한다.**

**Tech Stack:** Go 1.25 (표준 라이브러리만), React + TypeScript, Zustand.

## Global Constraints

- 스펙: `docs/superpowers/specs/2026-08-21-desktop-and-remote-design.md` §5.2.
- **모드 의미론을 바꾸지 않는다.** `CLOUD_ONLY`는 여전히 작업을 바이트 그대로 넘기고(회귀테스트가 고정), `LOCAL_ONLY` 허용목록·폴백 정책·`validContextPack` 판정도 그대로다. 이 계획은 **실행 수명만** 바꾼다.
- 작업트리에 미커밋 변경이 있다(`intelligence.go`의 `ErrRequestCanceled`, `maxContextBytes` 64KB 등). **먼저 커밋하거나 스태시하고 시작할 것** — 이 계획은 그 위에 올라간다.
- 새 Go 모듈·npm 패키지 금지.
- 서버 검증: `cd server && CGO_ENABLED=0 go build ./... && CGO_ENABLED=0 go test ./...`
- 클라이언트 검증: `cd client && ./node_modules/.bin/tsc --noEmit`
- `go vet ./...`은 이미 실패한다(`services/claude_resume_live_test.go:105`). 새로 늘리지 말 것.
- 마지막에 `dist/pcd.exe` 재빌드.

## File Structure

| 파일 | 책임 | 상태 |
|---|---|---|
| `server/services/intelligence.go` | `Start`(비동기 진입) + 실행 레지스트리 + emitter | 수정 |
| `server/services/intelligence_job_test.go` | 요청 취소가 실행을 죽이지 않음을 고정 | 생성 |
| `server/ws/message.go` | `EventIntelligenceTrace` 상수 + 페이로드 | 수정 |
| `server/handlers/intelligence.go` | 202 반환, cancel 엔드포인트 | 수정 |
| `server/main.go` | emitter 배선 + 라우팅 | 수정 |
| `client/src/lib/ws.ts` · `hooks/useWebSocket.ts` | `intelligence:trace` 구독 | 수정 |
| `client/src/components/native/NativeChat.tsx` | fetch 대기 → 이벤트 구독 | 수정 |
| `client/src/lib/api.ts` | `runIntelligence` 반환 타입, `cancelIntelligence` 추가 | 수정 |

---

### Task 1: 실행을 요청 컨텍스트에서 분리한다

**Files:**
- Modify: `server/services/intelligence.go` (`Run` ~line 678, `IntelligenceService` ~line 646)
- Test: `server/services/intelligence_job_test.go` (생성)

**Interfaces:**
- Produces: `func (s *IntelligenceService) Start(req IntelligenceRunRequest) (IntelligenceTrace, error)` — 동기 검증만 하고 즉시 반환
- Produces: `func (s *IntelligenceService) Cancel(traceID string) bool` — 사용자의 명시적 취소
- Keeps: `Run(ctx, req)` 는 **테스트와 동기 호출자를 위해 남긴다.** `Start`가 `Run`을 고루틴에서 부른다. 기존 테스트 전부가 그대로 산다.

- [x] **Step 1: 실패하는 테스트 작성**

`server/services/intelligence_job_test.go`:

```go
// 요청 컨텍스트가 취소돼도 실행은 계속돼야 한다. 이것이 프로덕션 트레이스 5건
// (latency 124,98x ms, "Post ...: context canceled")의 직접 원인이었다.
func TestStartSurvivesRequestCancellation(t *testing.T) {
	// 느린 프로바이더(응답 전 200ms 대기)를 세우고, Start 직후 요청 컨텍스트를
	// 취소한 뒤에도 트레이스가 로컬 응답 단계까지 진행되는지 본다.
	...
	trace, err := svc.Start(req)   // 즉시 반환
	if err != nil { t.Fatalf("start: %v", err) }
	if trace.Status != "RUNNING" { t.Fatalf("status=%q, want RUNNING", trace.Status) }
	cancelTheRequest()             // 브라우저가 탭을 닫은 상황
	waitForTerminal(t, svc, trace.ID)
	final, _ := svc.Trace(trace.ID)
	if final.ErrorCode == ErrRequestCanceled {
		t.Fatal("request cancellation killed the run")
	}
}

// 검증 실패는 여전히 동기적으로, 즉시 에러여야 한다 (빈 task, 없는 agent,
// 알 수 없는 mode, 허용목록 밖의 LOCAL_ONLY operation).
func TestStartRejectsInvalidRequestSynchronously(t *testing.T) { ... }
```

- [x] **Step 2: `Start` 구현.** `Run` 앞머리의 검증 블록(task/agentID 확인, mode 확인, agent 조회, LOCAL_ONLY 허용목록, 네이티브 세션 준비 확인)을 `Start`로 끌어올린다. 통과하면 `RUNNING` 트레이스를 저장하고 `context.WithCancel(context.Background())`로 만든 컨텍스트를 레지스트리에 넣은 뒤 `go s.Run(runCtx, req)`.
- [x] **Step 3: 취소 레지스트리.** `s.mu`가 이미 지키는 `pending` 옆에 `running map[string]context.CancelFunc`를 둔다. `Cancel(traceID)`는 그 함수를 부르고, 실행 고루틴은 종료 시 반드시 지운다(`defer`).
- [x] **Step 4: 검증.** `CGO_ENABLED=0 go test ./services/ -run 'TestStart|TestHybrid|TestClassify' -v`

**주의:** `dispatchCloud`는 이미 서버 안에서 `native.SendWithDisplayText`를 부르므로 구조 변경이 아니다. `pending[agentID]` 등록/해제 순서(`intelligence.go:842-857`의 레이스 주석)를 **건드리지 말 것** — 빠른 드라이버가 Send 반환 전에 result를 뱉는 경우를 막는 코드다.

---

### Task 2: 트레이스 진행 상황을 WS로 방송한다

**Files:**
- Modify: `server/ws/message.go` (서버→클라이언트 이벤트 구역 ~line 69-81)
- Modify: `server/services/intelligence.go` (`SetEmitter` + `addTrace`/`saveTrace` 경로)
- Modify: `server/main.go` (~line 123 배선부)

**Interfaces:**
- Produces: `EventIntelligenceTrace = "intelligence:trace"`
- Produces: `type IntelligenceTracePayload struct { Trace services.IntelligenceTrace \`json:"trace"\` }`
- Produces: `func (s *IntelligenceService) SetEmitter(fn func(IntelligenceTrace))`

- [x] **Step 1: 이벤트 상수 + 페이로드 추가.** `EventAgentSummaries`(line 56) 옆의 관례를 따른다.
- [x] **Step 2: emitter 호출.** `saveTrace`가 **유일한 영속화 지점**이므로 여기 한 곳에서 emitter를 부르면 모든 상태 전이가 자동으로 방송된다. 새 호출부를 만들지 말 것.
- [x] **Step 3: 배선.** `main.go`에서 `ControlRoom`의 선례 그대로:

```go
intelligenceSvc.SetEmitter(func(t services.IntelligenceTrace) {
	hub.BroadcastAll(ws.EventIntelligenceTrace, ws.IntelligenceTracePayload{Trace: t})
})
```

`BroadcastAll`인 이유: 트레이스는 에이전트에 묶이지만 설정 화면의 트레이스 목록은 에이전트 화면 밖에서도 열린다. 페이로드에 `agentId`가 이미 있으므로 수신 측이 거른다.

- [x] **Step 4: 검증.** `CGO_ENABLED=0 go build ./... && CGO_ENABLED=0 go test ./ws/ ./services/`

---

### Task 3: 핸들러가 즉시 반환하고, 취소 엔드포인트가 생긴다

**Files:**
- Modify: `server/handlers/intelligence.go` (`RunIntelligence` ~line 63-79)
- Modify: `server/main.go` (라우팅 ~line 278)
- Test: `server/handlers/intelligence_test.go` (생성)

**Interfaces:**
- `POST /api/intelligence/run` → **202 Accepted** + `{trace}` (`RUNNING` 상태)
- `POST /api/intelligence/traces/{id}/cancel` → 204 / 404

- [x] **Step 1: 실패하는 테스트.** 느린 프로바이더에 대고 `POST /run`이 **1초 안에** 202로 돌아오는지. 지금은 추론이 끝날 때까지 붙잡혀 있다.
- [x] **Step 2: 핸들러 전환.** `svc.Start(req)`를 부르고, 검증 에러는 400, 성공은 202 + 트레이스. **`r.Context()`를 서비스에 넘기지 않는다** — 이 계획의 핵심 한 줄이다.
- [x] **Step 3: cancel 라우트 추가.** `api.HandleFunc("/intelligence/traces/{id}/cancel", …).Methods("POST")`. 인증 미들웨어는 `api` 라우터에 이미 걸려 있다(`main.go:214`).
- [x] **Step 4: 검증.** `CGO_ENABLED=0 go test ./handlers/`

**하위호환:** 202는 `res.ok`이므로 기존 `apiFetch`가 그대로 통과한다. 다만 반환 본문의 의미가 "완료된 결과"에서 "접수된 트레이스"로 바뀌므로 Task 4를 같은 배포에 실어야 한다.

---

### Task 4: 클라이언트가 fetch 대기 대신 이벤트를 구독한다

**Files:**
- Modify: `client/src/lib/api.ts` (`runIntelligence` ~line 353, `IntelligenceRunResult` ~line 47)
- Modify: `client/src/hooks/useWebSocket.ts`
- Modify: `client/src/components/native/NativeChat.tsx` (실행 호출부)
- Modify: `client/src/components/intelligence/TraceDetail.tsx`

- [x] **Step 1: `runIntelligence` 반환 타입 정정.** 이제 `{trace}`만 돌아온다 — `contextPack`·`files`·`cloudDispatched`는 완료 이벤트에서 온다. 타입을 `IntelligenceStartResult`로 분리해 **컴파일러가 모든 사용처를 잡게 한다.**
- [x] **Step 2: 구독.** `intelligence:trace`를 받아 스토어의 트레이스 맵을 갱신한다. 같은 id의 늦은 이벤트가 새 상태를 덮지 않도록 **단조 진행만 허용**한다(터미널 상태에 도달한 트레이스는 되돌리지 않는다).
- [x] **Step 3: NativeChat.** 실행 버튼은 202를 받는 즉시 "진행 중"으로 바뀌고, 이후 상태는 이벤트가 민다. **탭을 닫았다 돌아와도 진행 중 트레이스가 보여야 한다** — 재연결 시 `GET /api/intelligence/traces?limit=…`로 한 번 채운다(WS 재연결 훅에 이미 자리가 있다).
- [x] **Step 4: 취소 버튼.** 진행 중 트레이스에 취소를 붙인다. 취소는 `LOCAL_REQUEST_CANCELED`로 끝나야 한다 — 미커밋 diff가 추가한 그 에러코드가 **이제 제 이름값을 한다**(브라우저 사고가 아니라 사용자 의도).
- [x] **Step 5: 검증.** `cd client && ./node_modules/.bin/tsc --noEmit`

---

### Task 5: 실물 확인 · 문서 · 바이너리

- [x] **Step 1: 재현 시나리오.** 실패대 컨텍스트(raw ≈ 34k)로 하이브리드를 걸고 **실행 중 브라우저 탭을 닫는다.** 다시 열었을 때:
  - 트레이스가 진행 중이거나 완주해 있어야 한다
  - `LOCAL_PROVIDER_UNREACHABLE` / `context canceled`가 **나오지 않아야 한다**
  - 오늘 이 시나리오는 125초 취소로 끝난다 — 그게 이 계획의 성공 판정 기준이다

  **실측(2026-08-21, 실제 바이너리):** 하이브리드가 아니라 `LOCAL_ONLY`로 확인했다 — 하이브리드는
  살아 있는 네이티브 CLI 세션이 필요해서 스크립트로 세울 수 없다. 요청 수명 분리는 두 모드가
  같은 경로(`Start` → 잡 고루틴)를 쓰므로 이 확인으로 덮인다.
  - 6초짜리 스텁 프로바이더에 `POST /run` → **202를 0.9ms에 반환**, 클라이언트는 즉시 연결 종료
  - 그럼에도 실행은 완주: `SUCCESS`, 로컬 지연 6,018ms, 3,944 → 28 추정 토큰
  - `intelligence:trace` 이벤트 4건 수신(직접 붙인 WS 클라이언트): `RUNNING`×2 → `SUCCESS`(트레이스만)
    → `SUCCESS`(컨텍스트 팩 + 파일 9개). 저장되지 않는 팩이 종료 이벤트로만 나온다는 설계가 실제로 성립
  - 취소: `204` → `FAILED` / `LOCAL_REQUEST_CANCELED`, `fallback=false`(클라우드 미발송), 재취소는 `404`
  - 검증 실패는 여전히 요청 안에서 `400` + 트레이스
- [x] **Step 2: 프로바이더 타임아웃 자체는 그대로 살아 있는지.** 죽은 엔드포인트에 걸면
  `LOCAL_PROVIDER_UNREACHABLE`로 정상 종료했고, 하이브리드 데드라인 폴백은
  `TestStartStillHonorsLocalTimeout`이 고정한다(취소만 폴백하지 않는다). 진짜로 죽은 프로바이더에 대고 걸면 `LOCAL_TIMEOUT`으로 정상 종료해야 한다. **취소 분리가 타임아웃까지 없애면 안 된다.**
- [x] **Step 3: 문서.** `docs/local-intelligence-poc-report.md` §11(Known limitations)에서 요청 수명 문제를 지우고, 실행 모델이 잡으로 바뀐 사실을 §5에 적는다.
- [x] **Step 4: `dist/pcd.exe` 재빌드** (클라이언트가 바뀌었으므로 필수).

## Self-Review

- **이 계획은 실패 원인을 없애지, 분류를 고치지 않는다.** 미커밋 diff의 `ErrRequestCanceled`는 증상에 이름을 붙이는 것이고, 여기서 그 증상 자체가 사라진다. 두 변경은 충돌하지 않는다 — 취소 코드는 이제 *사용자 취소*를 뜻하게 된다.
- **`Run`을 지우지 않는다.** 기존 테스트 다수가 동기 `Run`에 걸려 있다. `Start`는 그 위의 얇은 층이다.
- `saveTrace` 한 곳에서만 emit하는 설계가 중요하다 — 상태 전이마다 방송을 흩뿌리면 반드시 빠뜨리는 경로가 생긴다.
- 이 계획은 스펙 §1(엔드포인트 추상화)과 **독립**이며 그것을 기다리지 않는다. 다만 원격 시나리오에서 진짜 값어치가 나온다.
- 지연 38–59초 자체는 이 계획이 줄이지 않는다. 완화일 뿐 해결이 아니라는 점을 스펙 §10에 이미 적어뒀다.
