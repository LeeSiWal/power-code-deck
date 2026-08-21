# Local Intelligence 절감 측정 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 하이브리드 모드의 절감을 **클라우드가 실제로 소비한 양**으로 측정한다. 지금 UI에 뜨는 "감축 96%"는 `raw − optimized`인데, `raw`(pcd가 조립한 후보 컨텍스트)는 `CLOUD_ONLY`에서 **애초에 전송되지 않는 값**이라 절감액이 아니다.

**Architecture:** 계측 지점은 이미 하나로 좁혀져 있다 — `IntelligenceService.observeNativeEvent`(`services/intelligence.go:882`)가 `result` StreamEvent를 잡아 트레이스를 `CLOUD_COMPLETED`로 닫는다. 여기서 클라우드 소비량을 같이 적으면 된다. 문제는 **드라이버 두 개의 상태가 다르다**는 것:

- **Claude**: `ParseStreamEvent`가 CLI의 진짜 result JSON을 파싱하므로 `TotalCostUSD`가 이미 필드로 있다(`claude_stream.go:67`). `usage`는 `Raw`에 들어 있으나 아직 구조체 필드가 없다 → 필드 추가만 하면 된다.
- **Codex**: `turn/completed`가 `nativeResultEvent()`로 매핑되는데, 그 함수는 `{"type":"result"}`만 만든다(`native_service.go:34`). `handleNotification`이 `p.Turn`을 RawMessage로 이미 언마샬해 두고도 **turn/completed에서는 쓰지 않고 버린다**(`codex_driver.go:440-445`). 페이로드에 usage가 있는지부터 실측해야 한다.

그래서 순서는 (1) Claude 계측 → (2) Codex 페이로드 실측 → (3) 트레이스에 기록 → (4) 비교 실행. 새 의존성도, 새 서비스도 없다.

**Tech Stack:** Go 1.25 (표준 라이브러리만), SQLite(modernc), React + TypeScript.

## Global Constraints

- 스펙: `docs/superpowers/specs/2026-08-21-desktop-and-remote-design.md` §9 "0단계 — 절감 측정".
- **이 계획은 라우팅을 만들지 않는다.** 자동 라우팅·비용 추적 UI는 측정 결과가 나온 뒤에 판단한다(POC 리포트 §12).
- 새 Go 모듈·npm 패키지 금지. 단일 바이너리 배포다.
- 프롬프트·저장소 내용·응답은 **여전히 저장하지 않는다**. 트레이스는 상태 전이와 측정치만 담는다(리포트 §5). 이 계획이 추가하는 것도 숫자뿐이다.
- 서버 검증: `cd server && CGO_ENABLED=0 go build ./... && CGO_ENABLED=0 go test ./...`
- 클라이언트 검증: `cd client && ./node_modules/.bin/tsc --noEmit`
- `go vet ./...`은 이 리포에서 이미 실패한다(`services/claude_resume_live_test.go:105`). 고치지 말고 새로 늘리지도 말 것.
- 클라이언트가 바뀌면 마지막에 `dist/pcd.exe`를 재빌드한다(git 추적 대상).

## File Structure

| 파일 | 책임 | 상태 |
|---|---|---|
| `server/services/claude_stream.go` | result 이벤트의 `usage` 파싱 (`StreamUsage`) | 수정 |
| `server/services/codex_driver.go` | `turn/completed`의 turn 페이로드를 result 이벤트로 전달 | 수정 |
| `server/services/native_service.go` | `nativeResultEvent`에 usage 인자 추가 | 수정 |
| `server/db/migrations.go` | `intelligence_traces`에 cloud_* 컬럼 추가 | 수정 |
| `server/services/intelligence.go` | `observeNativeEvent`에서 클라우드 소비 기록 | 수정 |
| `server/services/intelligence_savings_test.go` | 계측 회귀 테스트 | 생성 |
| `server/services/codex_usage_live_test.go` | Codex turn 페이로드 실측(opt-in) | 생성 |
| `client/src/lib/api.ts` · `components/intelligence/` | 새 필드 노출, "감축 %"의 의미 정정 | 수정 |
| `docs/local-intelligence-poc-report.md` | §6·§7 갱신 + 측정 결과 | 수정 |

---

### Task 1: Claude result 이벤트의 usage를 파싱한다

`StreamEvent`는 `TotalCostUSD`·`NumTurns`·`DurationMS`까지 파싱하면서 `usage`만 빠져 있다. 비용(USD)만으로도 비교는 되지만, 토큰 단위 비교와 캐시 적중 구분을 하려면 usage가 필요하다.

**Files:**
- Modify: `server/services/claude_stream.go` (`StreamEvent` ~line 62-72)
- Test: `server/services/intelligence_savings_test.go` (생성)

**Interfaces:**
- Produces: `type StreamUsage struct { InputTokens, OutputTokens, CacheCreationInputTokens, CacheReadInputTokens int }`
- Produces: `StreamEvent.Usage *StreamUsage` (`json:"usage"`)

- [x] **Step 1: 실패하는 테스트 작성**

`server/services/intelligence_savings_test.go`에 실제 Claude result 라인 형태로:

```go
func TestParseStreamEventCapturesResultUsage(t *testing.T) {
	line := []byte(`{"type":"result","subtype":"success","total_cost_usd":0.0421,
		"usage":{"input_tokens":1200,"output_tokens":830,
		"cache_creation_input_tokens":15000,"cache_read_input_tokens":42000}}`)
	ev, err := ParseStreamEvent(line)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if ev.Usage == nil {
		t.Fatal("result event carried usage but StreamEvent dropped it")
	}
	if ev.Usage.InputTokens != 1200 || ev.Usage.CacheReadInputTokens != 42000 {
		t.Fatalf("usage mismatch: %+v", ev.Usage)
	}
	if ev.TotalCostUSD != 0.0421 {
		t.Fatalf("cost mismatch: %v", ev.TotalCostUSD)
	}
}
```

- [x] **Step 2: 통과시키기.** `StreamEvent`의 `// result` 구역에 `Usage *StreamUsage \`json:"usage"\`` 를 더하고 `StreamUsage`를 정의한다. 포인터인 이유: **usage가 없는 것과 0인 것을 구분해야 한다** — Codex 경로가 정확히 "없음"이다.
- [x] **Step 3: 검증.** `cd server && CGO_ENABLED=0 go test ./services/ -run TestParseStreamEventCapturesResultUsage -v`

---

### Task 2: Codex `turn/completed` 페이로드에 usage가 있는지 실측한다

**이 태스크는 코드를 고치기 전에 사실부터 확인한다.** Codex app-server의 `turn/completed` 알림이 usage를 싣는지 우리는 모른다 — `codex_driver.go:440-445`가 `p.Turn`을 통째로 버리고 있어 확인된 적이 없다.

**Files:**
- Test: `server/services/codex_usage_live_test.go` (생성)
- Modify: `server/services/codex_driver.go` (`handleNotification` ~line 440-445)
- Modify: `server/services/native_service.go` (`nativeResultEvent` line 34-37)

**Interfaces:**
- Changes: `nativeResultEvent()` → `nativeResultEvent(usage *StreamUsage)`. 호출부는 `codex_driver.go` 하나뿐이므로 파급이 없다 — 변경 전에 `grep -rn "nativeResultEvent" server/`로 확인할 것.

- [x] **Step 1: 실측 테스트 작성.** `codex_driver_live_test.go`의 opt-in 관례(`PCD_LIVE_CODEX=1`)를 그대로 따라 `TestCodexTurnCompletedPayload`를 만든다. 라이브 턴을 한 번 돌리고 **`turn/completed`의 raw JSON을 통째로 `t.Logf`로 찍는다.** 단언하지 않는다 — 이 스텝의 산출물은 PASS가 아니라 **실제 페이로드 한 덩어리**다.
- [x] **Step 2: 실행하고 결과를 이 문서에 기록.**

```bash
cd server && PCD_LIVE_CODEX=1 PCD_LIVE_CODEX_CWD=/home/siwal/code/power-code-deck CGO_ENABLED=0 go test ./services -run TestCodexTurnCompletedPayload -v -count=1
```

- [x] **Step 3: 분기.**
  - **usage가 있으면** — `handleNotification`의 `case "turn/completed"`에서 `p.Turn`을 언마샬해 `StreamUsage`로 매핑하고 `nativeResultEvent(usage)`로 넘긴다. Step 1의 테스트에 단언을 추가한다.
  - **usage가 없으면** — `nativeResultEvent(nil)`을 유지하고, **그 사실을 코드 주석과 이 문서에 남긴다.** 그러면 Codex 경로는 측정 불가이고, Task 4의 측정은 **Claude 세션으로만** 한다. 없는 것을 있는 척 추정하지 않는다.
- [x] **Step 4: 검증.** `CGO_ENABLED=0 go build ./... && CGO_ENABLED=0 go test ./services/`

---

### Task 3: 트레이스가 클라우드 소비를 기록한다

**Files:**
- Modify: `server/db/migrations.go` (additive ALTER 목록 관례, ~line 142-159 / `intelligence_traces` 블록 ~line 204-221)
- Modify: `server/services/intelligence.go` (`IntelligenceTrace` ~line 613, `saveTrace` ~line 906, `Traces`/`Trace` 스캔, `observeNativeEvent` ~line 882)
- Test: `server/services/intelligence_savings_test.go` (추가)

**Interfaces:**
- Produces: `IntelligenceTrace.CloudCostUSD float64` / `CloudInputTokens, CloudOutputTokens, CloudCacheReadTokens int` / `CloudUsageKnown bool`
- 컬럼: `cloud_cost_usd REAL DEFAULT 0`, `cloud_input_tokens`, `cloud_output_tokens`, `cloud_cache_read_tokens` INTEGER DEFAULT 0, `cloud_usage_known BOOLEAN DEFAULT FALSE`

- [x] **Step 1: 실패하는 테스트 작성.** `observeNativeEvent`에 usage를 실은 result 이벤트를 넣으면 트레이스에 값이 남고 `CloudUsageKnown`이 true여야 한다. **usage가 nil인 result에서는 `CloudUsageKnown`이 false로 남아야 한다** — 이 두 번째 단언이 Codex 경로를 정직하게 만든다.
- [x] **Step 2: 마이그레이션.** `migrations.go`의 "may already exist, non-fatal" ALTER 목록 관례를 그대로 쓴다:

```go
// Cloud consumption on the closing result event. Measuring hybrid savings needs
// what the CLOUD actually spent — raw−optimized compares against a baseline that
// CLOUD_ONLY never sends. cloud_usage_known separates "the driver reported none"
// (Codex today) from "it reported zero".
for _, stmt := range []string{
	"ALTER TABLE intelligence_traces ADD COLUMN cloud_cost_usd REAL DEFAULT 0",
	"ALTER TABLE intelligence_traces ADD COLUMN cloud_input_tokens INTEGER DEFAULT 0",
	"ALTER TABLE intelligence_traces ADD COLUMN cloud_output_tokens INTEGER DEFAULT 0",
	"ALTER TABLE intelligence_traces ADD COLUMN cloud_cache_read_tokens INTEGER DEFAULT 0",
	"ALTER TABLE intelligence_traces ADD COLUMN cloud_usage_known BOOLEAN DEFAULT FALSE",
} {
	db.Exec(stmt)
}
```

- [x] **Step 3: `observeNativeEvent` 기록.** 지금 `ev.Type != StreamTypeResult`면 바로 리턴하는 구조 그대로 두고, 트레이스를 닫기 직전에 `ev.Usage`/`ev.TotalCostUSD`를 옮겨 담는다. `addTrace(&t, "cloud_execution", "COMPLETED", …)`의 details에도 함께 싣는다.
- [x] **Step 4: `saveTrace`/`Traces`/`Trace`의 컬럼 목록에 다섯 개를 추가.** `INSERT OR REPLACE`의 자리표시자 개수를 반드시 맞출 것 — 조용히 어긋나면 런타임에만 터진다.
- [x] **Step 5: 검증.** `CGO_ENABLED=0 go test ./services/ ./db/`

---

### Task 4: 실제로 측정하고, UI의 "감축 %"를 정정한다

**Files:**
- Modify: `client/src/lib/api.ts` (`IntelligenceTrace` 인터페이스 ~line 28-45)
- Modify: `client/src/components/intelligence/SavingsSummary.tsx`, `TraceDetail.tsx`, `savings.ts`
- Modify: `docs/local-intelligence-poc-report.md` (§6·§7·§11·Acceptance)

- [x] **Step 1: 측정 실행.** 같은 저장소·같은 비파괴 작업(예: "이 저장소의 승인 흐름을 설명해줘")을 **Claude 네이티브 세션**에서:
  - `CLOUD_ONLY` 5회
  - `LOCAL_PREPROCESS_CLOUD` 5회
  - 컨텍스트 크기 두 구간에서 각각 — 성공대(raw ≈ 14k)와 실패대(raw ≈ 34k). 실패대는 지금 100% 폴백하므로 **"폴백 시 순손실"**(로컬에 태운 시간 + 클라우드는 그대로)이 수치로 남는다.
  **실측(2026-08-21):** 모드별 5회. 계획이 요구한 두 구간 중 **실패대(raw ≈ 34k)는 이제 만들 수 없다** —
  `maxContextBytes`가 64KB로 줄면서 raw가 16k대에서 상한을 치기 때문이다. 그래서 성공대에서만 측정했다.

- [x] **Step 2: 집계.** 트레이스에서 `cloud_cost_usd`·`cloud_input_tokens`의 모드별 중앙값을 뽑는다. 판정 기준:
  - **하이브리드 승** = 클라우드 비용 중앙값이 유의미하게 낮고, 지연 증가(38–59초)를 감수할 만한가
  - **무승부/패배** = 스펙 §5(LLM 축) 전체가 재검토 대상. 그 결론도 리포트에 그대로 적는다
  **결과: 무승부(사실상 패배).** 중앙값 $1.1604(CLOUD_ONLY) vs $1.2661(하이브리드, +9.1%)인데
  CLOUD_ONLY 자체가 $0.94~$1.37로 흔들려 차이가 노이즈 안에 있다. 대신 하이브리드는 13~46초를
  확실히 더 쓴다. 원인까지 측정했다 — 프리픽스 24,052토큰, 실제 턴의 툴 호출 25회, 비캐시 입력
  22~28토큰. 비용은 (프리픽스 × 스텝)이고 팩(약 1,010토큰)은 청구서의 0.1%다. 판정 기준대로
  스펙 §5(LLM 축)는 재검토 대상이며, 그 결론을 리포트 §7a·§12·Acceptance에 그대로 적었다.

- [x] **Step 3: UI 정정.** 지금 화면의 "감축 96%"는 **로컬 압축률**이지 절감이 아니다. 라벨을 그렇게 바꾸고(예: "컨텍스트 압축률"), 클라우드 실측이 있는 트레이스에만 별도로 "클라우드 소비"를 보여준다. `cloud_usage_known=false`(Codex)면 **숫자를 지어내지 말고 "이 드라이버는 사용량을 보고하지 않음"으로 표시**한다.
- [x] **Step 4: 리포트 갱신.** §6 "Remote Mac Studio E2E: BLOCKED"는 사실이 아니다 — 프로바이더가 등록돼 있고(`Mac Studio` / `192.168.1.22` / `qwen3-coder:30b`) 트레이스 18건이 있다. 성공 8 / 로컬실패 8의 raw 크기 분리(중앙값 14,535 vs 34,379)와 Step 2의 측정 결과로 §6·§7·§11·Acceptance 표를 다시 쓴다.
- [x] **Step 5: 검증.** `cd client && ./node_modules/.bin/tsc --noEmit`, 서버 테스트 전체, 그리고 `dist/pcd.exe` 재빌드.

**주의:** `client/src/components/intelligence/savings.test.ts`는 `savings.ts`의 계약을 모듈 스코프 단언으로 고정한다. **이 리포에는 클라이언트 테스트 러너가 없어서**(`package.json`에 `test` 스크립트도 vitest도 없다) 그 파일은 `tsc --noEmit`으로 타입만 검사되고 실행되지는 않는다. Step 3에서 `savings.ts`의 시그니처를 바꾸면 이 파일도 같이 고쳐야 타입이 통과한다. 러너를 새로 도입하지 말 것.

## Self-Review

- **측정 없이 UI만 고치면 안 된다.** Task 4 Step 1이 이 계획의 유일한 산출물이고 나머지는 그걸 가능하게 하는 배관이다.
- **Codex가 usage를 안 준다면 그렇게 적는다.** 추정으로 채우면 이 계획의 목적(존재하지 않는 베이스라인과의 비교를 없애는 것)을 그대로 반복하게 된다.
- 프롬프트·저장소 내용은 여전히 저장하지 않는다. 추가되는 건 숫자 다섯 개뿐이다.
- 이 계획은 스펙 §5.2(잡 전환)와 **독립**이다. 다만 실패대(raw≈34k) 측정은 잡 전환 전에는 125초 취소로 끝나므로, 그 구간의 수치는 "폴백 순손실"로만 읽어야 한다.
