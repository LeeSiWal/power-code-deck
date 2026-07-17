# 터미널 안정화와 네이티브 UI — 설계 문서

**상태**: 설계 (미구현) · **작성**: 2026-07-17 · **근거**: VS Code / xterm.js 소스, 각 CLI 실측, 우리 코드 인벤토리

---

## 0. 한 줄 요약

우리 터미널의 문제 대부분은 **서버에 이뮬레이터가 없다**는 단일 원인에서 나온다. VS Code는 이 설계를 이미 **폐기**했다 — 그들은 raw 바이트를 replay하지 않고, pty 호스트에서 headless xterm.js를 돌려 **화면 상태를 직렬화**한다. 한편 네이티브 UI(터미널 없는 채팅 UI)는 Claude와 Codex에는 가능하지만 **agy에는 불가능**하다. 따라서 하이브리드는 취향이 아니라 제약이다.

## 1. 목표

"공식 Claude Code VS Code 확장 수준의 UI/UX 안정성을, Claude·Codex·Antigravity 셋 다에서."

이 목표는 **그대로는 달성 불가능**하다. 공식 확장이 안정적인 이유는 터미널을 잘 만들어서가 아니라 **터미널을 쓰지 않기 때문**이다 (공식 문서: "The VS Code extension provides a native graphical interface"; 터미널 모드는 `claudeCode.useTerminal`로 **옵트인**, 기본 false). 그리고 agy는 네이티브 경로에 필요한 입력 채널이 없다.

따라서 목표를 둘로 쪼갠다.

- **터미널 트랙** — "쓸 만한 터미널". agy와 임의의 CLI/셸을 담당. 공식 수준은 목표가 아님.
- **네이티브 트랙** — "공식 수준 UX". Claude·Codex 담당. 터미널을 경유하지 않음.

## 2. 조사 결과 (근거)

### 2.1 VS Code는 raw 바이트를 replay하지 않는다

`ptyService.ts`는 pty 호스트 안에서 `@xterm/headless`를 돌린다. 모든 pty 바이트를 이 headless 터미널에 먹이고(`handleData(data) { this._xterm.write(data) }`), attach 시점에 **SerializeAddon으로 화면 상태 문자열을 생성**한다.

```ts
async generateReplayEvent(normalBufferOnly?, restoreToLastReviveBuffer?) {
  const serialize = new (await this._getSerializeConstructor());
  this._xterm.loadAddon(serialize);
  const options = { scrollback: this._xterm.options.scrollback };
  if (normalBufferOnly) { options.excludeAltBuffer = true; options.excludeModes = true; }
  ...
}
```

두 경로를 구분한다: **reconnect**(창 리로드 — alt 버퍼·DEC 모드 **포함**), **revive**(앱 재시작 — 죽은 vim의 alt 화면을 새 셸에 복원하면 안 되므로 **제외**).

레거시 raw 레코더(`terminalRecorder.ts`)는 아직 존재하지만 **replay 경로에 없다**. 그 코드의 eviction이 `substr()`로 이스케이프 시퀀스를 고려하지 않고 자르는 것 — **우리 링 버퍼가 지금 하는 바로 그것**이고, 우리 메모리에 기록된 `?1000h` eviction 버그가 정확히 이 실패 모드다.

> **함의**: 우리는 그들이 폐기한 설계를 쓰고 있다. 그리고 우리는 이미 서버에서 ghostty core를 돌린 전례가 있어, 이 도약은 그들보다 **싸다**.

### 2.2 Flow control — 우리 구현은 방향이 맞다

VS Code 상수 (`FlowControlConstants`):

| | VS Code | 우리 (`flow_control.go`) |
|---|---|---|
| 일시정지 임계 | 100,000 chars | 256KB |
| 재개 임계 | 5,000 chars | 64KB |
| ACK 단위 | 5,000 chars | 16KB |
| ACK 시점 | xterm `write()` 콜백 = **파싱 완료** | 동일 ✅ |
| replay 시 미확인 카운트 | `clearUnacknowledgedChars()` | `setViewers()`로 리셋 ✅ |

제약 하나가 중요하다: **ACK 단위 ≤ 재개 임계** ("must be ≤ LowWatermarkChars or the terminal will never unpause"). 우리는 16KB ≤ 64KB로 만족한다.

xterm의 `WriteBuffer`가 이 설계의 나머지 반쪽이다 — `WRITE_TIMEOUT_MS = 12`로 12ms마다 이벤트 루프에 양보하고, `DISCARD_WATERMARK = 50MB`를 넘기면 **예외를 던진다**("use flow control to avoid losing data"). ACK가 "수신"이 아니라 "파싱"을 의미하는 이유 — 두 메커니즘은 한 시스템이다.

### 2.3 쿼리 응답 — 우리 구현이 오히려 낫다

xterm.js `InputHandler.ts` 실측:

| 질의 | 응답? | 비고 |
|---|---|---|
| DA1 `CSI c` | ✅ | `termName`이 xterm/rxvt-unicode/screen/linux로 시작하지 않으면 **조용히 무응답** |
| DA2 `CSI > c` | ✅ | |
| DSR `CSI 6n` | ✅ | |
| DECRQM `CSI ?Ps $p` | ✅ 항상 | 2026 지원, **2027 case 없음 → NOT_RECOGNIZED(0)** |
| XTVERSION | ✅ | |
| XTGETTCAP | ❌ | 보안 이슈 이후 의도적 무시 |
| kitty `CSI ? u` | ❌ 기본 off | |
| OSC 4/10/11 | 브라우저 전용 | headless엔 themeService가 없어 무응답 |

**우리 `terminal_queries.go`는 xterm.js와 바이트 단위로 동일하게 동작한다** (2026 → 지원/reset, 2027 → 미인식, kitty → 무응답). 우연이 아니라 같은 결론에 독립적으로 도달한 것.

그리고 **VS Code도 detach 중엔 아무도 답하지 않는다**: headless xterm은 write-only로 쓰이고 `onData`를 아무도 구독하지 않아, 계산된 응답이 리스너 없는 이벤트로 버려진다. 오히려 그들은 이 문제를 알고 있다 — `_reviveTerminalProcess`의 주석: *"Don't start the process here as there's no terminal to answer CPR"* (conpty의 CPR에 답할 주체가 없으면 conhost가 멈춤).

> **함의**: 우리의 "파이프에서 답한다"는 접근은 VS Code보다 싸고 강하다. 단 **이중 응답**을 조심해야 한다 — 프론트가 붙으면 클라이언트 xterm도 같은 질의에 답한다. (ConPTY 전환기에 실제로 `\e[6n` 하나에 CPR이 두 번 오는 버그가 있었다.)

### 2.4 유니코드 — 우리는 V6로 돌고 있다 🔴

- xterm.js 코어는 **UnicodeV6만 내장**하고, 첫 등록 provider가 기본 활성이다 → **설정 안 하면 Unicode 6**.
- VS Code는 `terminal.integrated.unicodeVersion` **기본 `'11'`**, unicode11 애드온을 로드한 뒤 `activeVersion = '11'`을 **명시적으로 설정**한다 (애드온 로드만으로는 활성화되지 않음 — 흔한 통합 버그).
- VS Code는 이 버전을 **pty 호스트에도 전파**하고(`setUnicodeVersion`), **세션과 함께 영속화**한다(`ISerializedTerminalState.unicodeVersion`). 직렬화기와 렌더러의 폭 테이블이 다르면 복원된 버퍼가 깨지기 때문.

**우리 코드에는 `unicode`/`activeVersion`을 설정하는 곳이 없다** (grep 결과 0건, `@xterm/addon-unicode11` 미설치). 즉 CJK 렌더링에 그렇게 공을 들이면서 **폭 계산은 Unicode 6**으로 하고 있다.

깨지는 메커니즘: 앱과 이뮬레이터가 각자 wcwidth를 계산하고 **서로 대조하지 않는다**. 한 칸이라도 어긋나면 이후 모든 커서 상대 연산이 빗나간다 — 백스페이스가 너무 많이/적게 지우고, 상태바가 어긋나고, 최악은 앱이 줄바꿈했다고 믿는데 터미널은 안 해서 `isWrapped`가 거짓이 되고 → **다음 리사이즈의 리플로우가 거짓 위에서 수행되어 영구히 망가진다.**

### 2.5 리플로우

- `isWrapped`는 "이 줄은 윗줄의 연속"이라는 **줄당 불리언 하나**이고, 리플로우의 **유일한 근거**다.
- **cols가 바뀔 때만** 리플로우한다 (`if (this._cols === newCols) return`). rows 변경은 공짜.
- 그래서 VS Code는 **cols만 100ms 디바운스**하고 rows는 즉시 적용한다 (버퍼 200줄 초과 시).
- **alt 버퍼는 리플로우하지 않는다** (`new Buffer(false, …)` → scrollback 없음 → 리플로우 비활성). TUI는 SIGWINCH 받고 스스로 다시 그리므로 옳은 설계. **agy·Claude는 alt 화면에 산다 → 리플로우는 그들에게 대체로 무관.**
- 기본값 `reflowCursorLine: false` — 셸이 알아서 고칠 거라 믿고 커서 줄은 건드리지 않는다.
- CJK 경계 처리는 **추측**이다: `getWrappedLineTrimmedLength`의 주석이 *"we can be pretty sure"*. 줄 끝이 null이고 다음 줄이 wide로 시작하면 wrap 패딩으로 간주 — 진짜 공백이면 틀린다.
- `newCols === 1`이면 **무한 루프** (그래서 터미널이 cols ≥ 2를 강제한다).

VS Code `PersistentTerminalProcess.resize`에서 훔칠 것 셋:
```ts
resize(cols, rows, ...) {
  if (this._inReplay) return;              // ① replay 중 리사이즈 무시
  this._serializer.handleResize(cols, rows); // ② 리사이즈를 replay 스트림에 기록
  this._bufferer.flushBuffer(...);          // ③ 리사이즈 시 출력 버퍼 flush
  ...
}
```
그리고 **리사이즈는 flow control 일시정지와 무관하게 통과한다** — 즉 옛 폭으로 생성된 10만 자가 큐에 남은 채 SIGWINCH가 나갈 수 있다. 구조적 오염원. 우리는 flow control이 있으므로 이 상호작용을 **의식적으로 결정**해야 한다(사고로 두지 말 것).

### 2.6 렌더러 — DOM은 후퇴가 아니라 정답

- **Canvas 렌더러는 없어졌다** (VS Code 1.89 deprecated → **1.90에서 제거**). 남은 건 DOM과 WebGL뿐. `gpuAcceleration`은 `'auto'|'on'|'off'`.
- 알려진 **잔상(ghosting) 원인은 전부 GPU 경로 전용**이다 — 텍스처 아틀라스 stale 비트맵, 멀티 터미널 아틀라스 경합, 슬립/복귀 시 텍스처 손상. 구조적으로 잔상은 프레임 간 픽셀을 소유해야 생기고, **DOM은 소유하지 않는다.**
- DOM의 진짜 약점은 다른 데 있다: **#791** — 비용이 문자 수가 아니라 **줄당 같은 속성 런 수**에 비례 (색이 화려한 풀스크린 TUI 리페인트가 최악), **#3807** — 셀보다 넓은 글리프가 셀 경계에서 클리핑, **#4133** — `<style>` 주입이 엄격한 CSP와 충돌.

> **agy 빈 화면**: 위 잔상 클래스는 전부 GPU 전용이라 **우리 증상을 설명하지 못한다**. 억지로 연결하지 않는다. 다만 DOM 경로에서 실제로 의심할 만한 건 **#791(런 수 폭발 — "아무것도 안 그려진 것처럼 보이는" 가장 유력한 실패)** 과 **#3807(D2Coding + CJK 2셀이면 클리핑 가능)**. 여전히 **라이브 브라우저 디버깅이 필요**하다.

### 2.7 에이전트별 구조화 스트림

| | 양방향? | 인터페이스 | 승인 | 성숙도 | 판정 |
|---|---|---|---|---|---|
| **Claude** | ✅ | `-p --input-format stream-json --output-format stream-json`, Agent SDK | ✅ 최상 — `canUseTool`(허용/입력수정/규칙저장/거부+메시지), `AskUserQuestion`, **`defer`(무기한 보류)**, hooks | 안정 · 문서화 · **권장 모드** | **GO** |
| **Codex** | ✅ | **`codex app-server`** (JSON-RPC 2.0 / stdio) | ✅ `serverRequest` — 명령 승인(수락/거절/**샌드박스 수정 제안**), 파일 변경 승인 | "experimental" 딱지, 그러나 **공식 VS Code 확장이 전적으로 이것에 의존** | **GO** (stdio + 코어 메서드만) |
| Codex `exec --json` | ❌ 단방향 | JSONL | ❌ 사전 `--sandbox`만 | 안정 | ❌ CI용 |
| **agy** | ❌ **단방향** | `--output-format stream-json` (**문서에 없는 숨은 플래그**, 검증 없음, `--input-format` 없음) | ❌ CLI 자체가 *"permission that headless mode cannot prompt for"* | v1.1.3 · ACP 요청 이슈 무응답(2026-05-20~) | ❌ **PTY 유지** |

agy 실측에서 나온 위험 둘:
1. 승인이 필요한 쓰기를 시키면 **승인을 우회해 엉뚱한 경로**(`~/.gemini/.../scratch/`)에 썼다.
2. 피할 수 없는 승인이 걸린 실행은 **아무 일도 안 하고 `status: SUCCESS`로 보고**했다. → UI에 초록 체크가 뜨는데 실제론 아무것도 안 일어남.

## 3. 우리 갭 (증거 기반)

| # | 갭 | 증거 | 트랙 |
|---|---|---|---|
| 1 | **서버 이뮬레이터 없음** — replay가 raw 512KB 링 | `ring_buffer.go:9`, `session_engine_internal.go:258` | 터미널 |
| 2 | 유니코드 **V6** (VS Code는 V11) | 클라에 `activeVersion` 설정 0건 | 양쪽 |
| 3 | 쿼리 응답이 뷰어에 종속 (DA1/DSR 무응답) | 실측: 뷰어 없는 세션에서 둘 다 타임아웃 | 터미널 |
| 4 | 마우스 **휠만** — 클릭·드래그·모션 없음 | `TerminalView.tsx:519-534` | 터미널 |
| 5 | 커서 blink/hide **미동작** — `.wterm.focused`를 붙이는 코드 없음 → `cursorBlink` 死문자, `ESC[?25l` 무시 | `customTerm.css:91`, `CustomTerm.ts:324` | 터미널 |
| 6 | 스크롤백 DOM이 5000줄 포화 후 어긋남 + DOM 무한 증가 | `CustomTerm.ts:382` 조기 반환 | 터미널 |
| 7 | 전체 리페인트를 **PTY 크기를 속여서** 함 (rows ±1) | `TerminalView.tsx:231-243` | 터미널 |
| 8 | `terminal:input`에 **권한 체크 없음** — 아무나 아무 PTY에 씀 | `hub.go:186-191` (ack/resize는 체크하는데 input만 빠짐) | 양쪽 |
| 9 | 마우스 모드를 클라에서 **문자열 스캔**으로 감지 → 청크 경계에서 실패 | `TerminalView.tsx:180-188` | 터미널 |
| 10 | replay가 옛 `ESC[6n`을 재생 → 클라가 **낡은 값으로 다시 응답** | `session_engine_internal.go:258` | 터미널 |

## 4. 설계

### 4.1 터미널 트랙 — 서버 이뮬레이터로 전환

```
                 ┌──────────── 서버 (pcd) ────────────┐
  PTY 바이트 ───▶│  이뮬레이터 코어 (headless)         │
                 │   ├─ 화면 상태 + 스크롤백           │
                 │   ├─ DEC 모드 (13개 수동 추적 폐기) │
                 │   └─ 쿼리 응답 (뷰어 무관)          │
                 └───────────────┬────────────────────┘
                     attach 시   │ serialize()
                                 ▼
                        화면 상태 문자열 (raw 바이트 아님)
```

- **링 버퍼 replay 폐기.** 대신 `serialize()`. 갭 #1·#10이 사라지고, `terminal_modes.go`의 수동 13모드 추적이 **불필요해진다**(패치가 아니라 제거).
- reconnect는 alt 버퍼·모드 **포함**, 프로세스 재생(revive)은 **제외** — VS Code의 구분을 따른다.
- 쿼리 응답을 이뮬레이터가 맡으면 갭 #3이 구조적으로 닫힌다. **단 이중 응답 방지 필요** (프론트 xterm도 답하므로 — 클라 응답을 억제하거나 서버 응답을 뷰어 부착 시 끄거나, 택일해야 함).
- 리사이즈: **cols만 디바운스**(rows는 공짜), replay 중 무시, 리사이즈를 스트림에 기록, 리사이즈 시 flush.

**후보**: 서버 측 ghostty core 재활용(전례 있음) / Go VT 라이브러리 / node headless 사이드카(프로세스 추가 = 비용).

### 4.2 네이티브 트랙 — 공통 승인 인터페이스

Claude의 `canUseTool`과 Codex의 `serverRequest`는 **같은 모양**이다: *서버가 묻고, 클라가 답하고, 그동안 실행이 멈춘다.* 그래서 Go 인터페이스 하나로 둘 다 서빙한다.

```go
// 에이전트가 사용자에게 승인을 요청한다. 응답까지 실행은 멈춘다.
type ApprovalRequest struct {
    SessionID string
    Tool      string
    Input     json.RawMessage
    Kind      ApprovalKind // command | fileChange | question
}
type ApprovalResponse struct {
    Decision     Decision        // allow | deny | allowWithEdits | defer
    UpdatedInput json.RawMessage // allowWithEdits
    Message      string          // deny 사유 (에이전트가 읽고 적응함)
    Persist      bool            // "이 규칙 기억하기"
}
type AgentDriver interface {
    Start(ctx, SessionConfig) (<-chan Event, error)
    Send(ctx, UserInput) error
    Resolve(ctx, requestID string, ApprovalResponse) error
    Interrupt(ctx) error
}
```

- 구현체 둘: `ClaudeDriver`(stream-json stdio), `CodexDriver`(JSON-RPC stdio).
- **Codex 타입은 손으로 쓰지 않는다** — `codex app-server generate-json-schema`로 스키마를 뽑아 생성.
- **`defer`는 선택이 아니라 필수다.** 폰 사용자는 한 시간 뒤에 답한다. Claude의 `defer`와 Codex의 thread 영속성이 둘 다 이 이유로 존재한다.
- **함정**: Claude에서 자동 승인된 도구는 `canUseTool`에 **도달하지 않는다** → 우리 UI의 승인 화면을 조용히 우회. 매 호출 검사가 필요하면 `PreToolUse` 훅을 써야 한다.

### 4.3 두 트랙이 공유하는 것 / 나누는 것

| 공유 | 트랙별 |
|---|---|
| 세션 수명주기 (생성/영속/재개/삭제) | 렌더링 (터미널 격자 vs 채팅 뷰) |
| exclusive-viewer + 핸드오프 | 입력 (PTY 바이트 vs 구조화 메시지) |
| WS 허브 · 인증 · 프로젝트 | 승인 (터미널은 TUI가 그림 / 네이티브는 우리 UI) |
| 알림 · 로그 | |

## 5. 순서

**Tier 1 — 이것부터**
1. **agy 빈 화면 원인 규명** (라이브 브라우저 디버깅). 네이티브 트랙도 같은 브라우저·같은 렌더 경로를 쓴다. 원인 모른 채 트랙을 바꾸면 **버그를 데리고 이사한다.** 유력 후보: #791 런 수 폭발, #3807 셀 경계 클리핑.
2. **유니코드 V11 통일** — 싸고, 지금 틀렸고, 양 트랙 모두에 영향. 코어·직렬화기·렌더러가 한 테이블을 쓰고, 세션과 함께 영속화.
3. **`terminal:input` 권한 체크** — 지금 열려 있음.

**Tier 2**
4. 서버 이뮬레이터 + serialize replay (갭 #1·#3·#10 동시 해소, 수동 모드 추적 제거)
5. 네이티브 트랙 Claude 드라이버 (stream-json + canUseTool + defer)
6. grace-time 재접속 (60초 → reduceGraceTime 6초, **process time** 기준)

**Tier 3**
7. Codex 드라이버 (generate-json-schema 기반)
8. 마우스 전체 지원 / 커서 상태 / 스크롤백 DOM 재작성 — **agy를 위해서만 지불하는 비용**
9. 리사이즈 프로토콜 정리 (cols 디바운스, replay 중 무시, flow control과의 상호작용 명시)

**포팅하지 않을 것**: VS Code의 50자/5ms 붙여넣기 청킹 (재현 불가로 논쟁 중, 네트워크 너머에선 가혹).

## 6. 결정 사항

1. **agy를 위해 터미널 스택 전체를 유지할 가치가 있는가?** Tier 3 항목 8은 오직 agy를 위한 비용이다. agy를 포기하면 터미널 트랙은 "셸/임의 CLI용 기본기"만 남기고 동결할 수 있다.
2. **서버 이뮬레이터를 무엇으로 하나?** ghostty core 재활용 vs Go VT 라이브러리 vs node 사이드카.
3. **이중 응답을 어디서 막나?** 서버 응답을 뷰어 부착 시 끌 것인가, 클라 응답을 억제할 것인가.

## 7. 미확인 (정직하게)

- 공식 Claude 확장 패널이 내부적으로 쓰는 **정확한 와이어 프로토콜은 문서화돼 있지 않다.** stream-json이라는 건 정황 근거의 **추론**이지 확인된 사실이 아니다.
- CLI 레벨 `control_request`/`control_response` 스키마는 **서드파티 리버스 엔지니어링** 출처.
- Codex app-server는 실제 핸드셰이크까지 검증하지 않았다. **`generate-json-schema`로 우리가 직접 확정할 수 있다.**
- agy의 `--output-format`은 **문서에 없고 검증도 없다**(`bogus` 값을 줘도 조용히 평문으로 폴백). 한 번의 리팩터링에 예고 없이 사라질 수 있다.
