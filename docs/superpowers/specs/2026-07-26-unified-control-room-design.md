# 통합 관제실 + 계층 네비게이션 설계

- 작성일: 2026-07-26
- 상태: 구현됨 (v0.4.0)

## 배경

에이전트 목록을 보여주는 화면이 둘로 갈라져 있다.

- `/dashboard` (`DashboardPage`, 107줄) — 평면 목록, 에이전트 생성/삭제, 터미널 스냅샷, git·포트 메타
- `/control` (`ControlRoomPage`, 513줄) — 프로젝트별 그룹, attention 트리아지, 전역 승인 큐

둘 다 "내 에이전트들이 지금 뭘 하고 있나"에 답하려 하지만 어느 쪽도 완결되지 않는다. 승인이 뜨면 컨트롤 룸으로, 새 세션을 만들려면 대시보드로, 포트 번호를 보려면 다시 대시보드로 가야 한다. 화면 전환이 정보 획득 비용이 되고 있다.

코드에도 이미 흔적이 있다. `ControlRoomPage.tsx:354`가 대시보드를 "classic dashboard ↗"라고 부른다 — 후속 화면이라고 선언해 놓고 정작 `/dashboard`가 로그인 후 기본 착지점이고 생성 경로를 독점하고 있다.

동시에 뒤로 가기 플로우가 어긋나 있다. `useGoBack.ts:17`은 `navigate(-1)`, 즉 "내 마지막 이동을 되돌린다"인데 사용자가 기대하는 것은 "한 단계 위로"다. 두 화면 중 하나가 사라지면 히스토리 스택 모양이 통째로 바뀌므로, 통합과 네비게이션은 한 스펙으로 묶는다.

## 목표

- 에이전트를 보는 화면을 하나로 만든다
- 뒤로 가기가 진입 경로와 무관하게 결정론적으로 동작하게 한다
- 승인 피드가 상시 노출되는 표면이 되므로, 거기에 뜨면 안 되는 항목을 걷어낸다
- `ControlRoomPage.tsx`를 작업 중에 분할한다 (현재 513줄, 통합 후 더 커짐)

## 비목표 (YAGNI)

- 서버 `AgentSummary` 구조 확장
- 벽 화면 검색·필터
- 프로젝트별 로그 필터링
- 스와이프 백 제스처 (`useSwipe.ts`는 구현돼 있으나 현재 아무도 사용하지 않음 — 계층 네비게이션이 자리잡은 뒤 별도로 판단)
- 터미널 스냅샷 (아래 "보류된 기능" 참고)

## 결정 요약

| 항목 | 결정 |
|---|---|
| 통합 방향 | 컨트롤 룸이 대시보드를 흡수 |
| 터미널 스냅샷 | 화면에서 제외, 코드는 보존 |
| 펼치기 UI | 없음 — 메타를 타일에 인라인 |
| 생성 진입점 | 헤더 고정 버튼 |
| `/dashboard` | `/control`로 리다이렉트 |
| 뒤로 가기 | 히스토리 기반 → 계층 기반 |
| AskUserQuestion 승인 | 서버에서 auto-allow (승인 UI 미노출) |

---

## 1. 통합 화면 (`/control`)

```
┌────────────────────────────────────────────────┬──────────┐
│ ⌘ PCD  /control      [+ 프로젝트 추가]  ● 연결  │          │
├────────────────────────────────────────────────┤ 승인 대기 │
│ AttentionRail (승인/에러/정체 있을 때만)         │  (290px) │
├────────────────────────────────────────────────┤  전역     │
│ ▸ power-code-deck        3 agents · 1 working  │  피드     │
│   [tile] [tile] [tile]                         │          │
│ ▸ saju                   1 agent               │          │
│   [tile]                                       │          │
└────────────────────────────────────────────────┴──────────┘
```

모바일은 타일 1컬럼 + 승인 바텀시트 + BottomNav로, 현행 컨트롤 룸 구조를 그대로 유지한다.

### 타일 구성

```
● claude-main  [CLAUDE]              작업 중
━━━━━━━━━━━━━━━━━  (working bar, 작업 중일 때만)
tool  : Edit
target: AgentMeta.tsx
×12 · 3초 전
⎇ main (+2) ●    🔌 :5173 :8080      ← AgentMeta (포트 클릭 가능)
✓ 완료 3   ⚠ 에러 1
[열기] [재시작] [정지]
```

기존 컨트롤 룸 타일에 `AgentMeta`(full)를 추가한 형태다. `AgentMeta.tsx:35-77`의 full 변형은 git 한 줄 + 포트 한 줄 + 선택적 상태/진행바가 전부여서(11px 텍스트 3~4줄) 타일 안에 그대로 들어간다.

**`SubAgentBar`는 서브에이전트가 실제로 존재할 때만 렌더한다.** 타일에 이미 `tool: Edit`이 있어 메인 에이전트만 도는 상황에서는 같은 정보가 두 번 표시된다. SubAgentBar의 고유 가치는 여러 노드가 동시에 도는 경우뿐이다.

### 액션 배치

실행 상태에 따라 세 번째 버튼을 교체한다.

- 실행 중 → `[열기] [재시작] [정지]`
- 정지됨 → `[열기] [재시작] [삭제]`

되돌릴 수 없는 삭제가 실행 중인 세션에 노출되지 않고, 버튼 개수도 늘지 않으며, 정지된 레코드가 무한히 쌓이는 문제도 해결된다.

기존 타일의 "로그" 버튼은 **제거한다.** 필터링 없이 전역 `/logs`로 갈 뿐이라 밀도만 소모한다. 로그 진입은 BottomNav(모바일)와 헤더(데스크톱)로 충분하다.

### 생성 진입점

헤더 우측 고정 `+ 프로젝트 추가` 버튼. 현재 대시보드의 동일 버튼(`DashboardPage.tsx:56-62`)을 그대로 옮긴다 — 이 버튼은 모달이 아니라 `<Link to="/?new=1">`로 `ProjectSelectPage`에 간다.

`CreateAgentSheet`는 마운트하지 않는다. `DashboardPage`가 이 컴포넌트를 import하고는 있으나 `showCreate`가 `true`가 되는 경로가 없어(`DashboardPage.tsx:16`, `102`) **이미 도달 불가능한 죽은 UI**다. 통합 시 함께 삭제한다.

---

## 2. 컴포넌트 구조

`ControlRoomPage.tsx`가 이미 513줄이므로 통합과 함께 분할한다.

| 파일 | 역할 |
|---|---|
| `pages/ControlRoomPage.tsx` | 데이터 수집 + 오케스트레이션만 (~130줄 목표) |
| `components/control/AttentionRail.tsx` | 추출 (로직 변경 없음) |
| `components/control/ProjectGroup.tsx` | 그룹 헤더 + 타일 그리드 |
| `components/control/AgentTile.tsx` | 타일 (LiveDot/상태칩/워킹바/메트릭/메타/액션) |
| `components/control/ApprovalFeed.tsx` | 추출 (데스크톱 사이드바 / 모바일 시트 공용) |

대시보드에서 그대로 재사용: `AgentMeta`(full), `SubAgentBar`.

---

## 3. 데이터 흐름

`AgentMeta`는 스토어에서 직접 읽는다 (`AgentMeta.tsx:10` — `useAppStore((s) => s.agentMeta.get(agentId))`). 따라서 타일은 `summaries`로 그리고 메타는 컴포넌트가 알아서 가져간다. **summaries ↔ agents 조인은 필요 없다.**

`workingDir` 등 `Agent` 객체가 필요했던 것은 스냅샷을 띄우려던 경우뿐인데, 스냅샷을 빼기로 했으므로 `listAgents()` 호출도 불필요하다.

### 확인된 문제: 첫 진입 시 메타 공백

서버 조사 결과:

- `agent:meta`의 유일한 발신 지점은 `ws/hub.go:176`의 `BroadcastAll`이며, 이는 `pollMeta()` 안에 있다
- `pollMeta()`는 `ws/hub.go:104`의 **10초 티커**로만 호출된다
- WS 접속 시 초기 스냅샷을 보내는 핸드셰이크가 없다 (`ws/hub.go:180-200`)
- `appStore.ts:301`의 `agentMeta`는 빈 Map으로 시작하고, REST로 시딩되지 않는다
- `GET /api/agents`는 메타를 포함하지 않는다 (`handlers/agents.go:19-30`)

결과적으로 `/control`을 새로 열면 git·포트가 **최대 10초간 비어 있다.** 대시보드에서는 스냅샷과 상태가 먼저 채워져 이 공백이 가려져 있었다.

### 해결: 접속 시 즉시 메타 폴링

WS 클라이언트가 hub에 등록되는 시점에 `pollMeta()`를 한 번 즉시 실행한다.

- `handlers/meta.go:14`의 `GET /api/agents/{id}/meta`를 클라이언트에서 에이전트마다 호출하는 방식은 벽 화면에서 N개 요청이 되므로 채택하지 않는다
- 매 틱 이미 `BroadcastAll`이므로 다른 클라이언트에게 중복 전달되어도 무해하다
- `/control`뿐 아니라 모든 화면이 함께 고쳐진다

동시 접속이 몰릴 때 git·포트 스캔이 반복되지 않도록, **직전 폴링으로부터 3초 이내면 즉시 폴링을 건너뛴다.** 이 경우 정규 10초 티커가 곧 처리하므로 공백은 최대 3초로 제한된다.

### 수용하는 제약

`pollMeta()`는 `ws/hub.go:164`에서 `status != "running"`인 에이전트를 건너뛴다. 따라서 **정지된 에이전트 타일에는 메타 줄이 표시되지 않는다.** `AgentMeta`가 데이터 없을 때 `null`을 반환하므로(`AgentMeta.tsx:12`) 레이아웃은 깨지지 않는다. 정지된 세션의 포트는 어차피 의미가 없고 브랜치만으로 서버 동작을 바꿀 이유가 없으므로 현행 유지한다.

---

## 4. 계층 네비게이션

### 현재 문제

`useGoBack.ts:17`의 `navigate(-1)`은 "마지막 이동 취소"다. 사용자가 뒤로 가기에 기대하는 "한 단계 위로"와는 일직선으로 진입했을 때만 우연히 일치한다.

1. **탭 이동이 히스토리에 쌓인다.** `BottomNav.tsx:17-34`가 전부 push형 `<Link>`다. 터미널 → 설정 → 로그에서 뒤로 가기를 누르면 작업하던 터미널이 아니라 설정으로 간다.
2. **컨트롤 룸 진입 경로가 둘이라 루프가 생긴다.** `TerminalPage.tsx:314,546`의 컨트롤 룸 아이콘이 push다. 컨트롤 룸 → 터미널 → (아이콘) 컨트롤 룸 → 뒤로 → **터미널**로 돌아온다.
3. **fallback 목적지가 사라진다.** `useGoBack` 기본값, `TerminalPage.tsx:198`, `ProjectSelectPage.tsx:16-42`가 모두 `/dashboard`를 가리킨다.

### 화면 트리

```
/control  ← 루트 (덱)
├── /agents/:id   위로 → /control
├── /logs         위로 → /control
├── /settings     위로 → /control
└── /             위로 → /control     (프로젝트 선택 = 생성 하위 흐름)
    └── /launch/:path   위로 → /
```

모든 화면의 부모가 정확히 하나다. 뒤로 가기는 진입 경로와 무관하게 항상 같은 곳으로 간다.

`useGoBack(fallback)`을 `useGoUp(parent)`로 교체하고 `window.history.state.idx` 검사를 제거한다. 딥링크·PWA 콜드 스타트에서 `idx === 0`이라 fallback으로 새던 경로가 함께 사라진다.

**수용하는 대가:** 터미널 → 로그 → 뒤로 하면 그 터미널이 아니라 `/control`로 간다. 주 사용 루프는 벽↔세션이고 로그·설정은 간헐적 경유지이므로, 클릭 한 번의 비용이 결정론을 잃는 것보다 싸다. 실제로 거슬리면 `pcd:lastAgentId`(이미 존재)를 이용한 세션 앵커를 나중에 추가한다.

### 함께 정리되는 것

1. **BottomNav 탭 4개 → 3개.** `Home(/dashboard)`과 `Control(/control)`이 통합 후 같은 화면이 된다. `[덱] [로그] [설정]`으로 합치고, `/dashboard`에 붙어 있던 `NotificationBadge`를 덱 탭으로 옮긴다.
2. **TerminalPage의 컨트롤 룸 아이콘 제거.** 뒤로 가기가 `/control`로 가므로 같은 목적지 버튼이 헤더에 둘이 된다. 위 문제 2번의 원인이기도 하다.
3. **탭 전환은 `replace`로.** BottomNav의 `<Link>` → `<Link replace>`. 탭바 표준 동작이며 히스토리가 부풀지 않는다.

### 경로 상수 변경

| 위치 | 현재 | 변경 |
|---|---|---|
| `useGoBack.ts` 기본 fallback | `/dashboard` | (삭제 — `useGoUp`으로 대체) |
| `TerminalPage.tsx:198` 에러 리다이렉트 | `/dashboard` | `/control` |
| `ProjectSelectPage.tsx:16-42` 자동 리다이렉트 | `/dashboard` | `/control` |
| `App.tsx:170` 미지정 경로 | `/` | `/control` |
| `App.tsx` 라우트 | `/dashboard` → `DashboardPage` | `/dashboard` → `<Navigate to="/control" replace />` |

`ProjectSelectPage`의 `?new=1` 우회 경로는 유지한다 (자동 리다이렉트를 건너뛰고 프로젝트를 추가하기 위해 필요).

---

## 5. AskUserQuestion 승인 프롬프트 제거

### 증상

Claude가 `AskUserQuestion`으로 선택지를 물으면, 선택지 버튼과 **허용/거부 승인 카드가 동시에** 뜬다. 선택지에는 허용/거부가 필요 없다.

### 이 스펙에 포함하는 이유

통합 후 승인 피드는 데스크톱에서 상시 노출되는 1급 표면이 된다. 그런데 `ws/hub.go:282`가 `BroadcastAll`이므로 **이 유령 승인 카드가 컨트롤 룸 피드에도 뜬다.** 거기서 "허용"을 눌러도 실제 질문 버튼은 세션 안에 있어 아무 의미가 없다. 통합이 이 버그를 더 눈에 띄게 만들므로 함께 고친다.

### 원인

`pcd mcp-approve` 브리지(`cli/mcp_approve.go:147-168`)가 **모든** 도구 호출을 `POST /internal/native/approve`로 넘기고, `handlers/native_approve.go`에는 도구 이름 필터가 없다. 그래서 `AskUserQuestion`도 `broker.Ask()`(`native_approve.go:53`)를 타고 `native:approval` 브로드캐스트와 "승인 필요" 푸시(`ws/hub.go:282-298`)까지 발생한다.

클라이언트는 같은 tool_use를 두 경로로 처리한다:

- `lib/nativeEvents.ts:171-183` — `AskUserQuestion`을 `kind: 'ask'` 아이템으로 만들어 `AskRow`(`NativeChat.tsx:836`)가 선택지 버튼을 그림
- `NativeChat.tsx:284-286` — `native:approval` 이벤트를 받아 `ApprovalCard`(`NativeChat.tsx:1029`)를 그림

두 UI가 동시에 나타난다.

### 해결: 서버에서 auto-allow

`handlers/native_approve.go`의 `broker.Ask()` 호출(53행) **직전**에 도구 이름을 검사해 즉시 allow로 응답한다.

```go
// AskUserQuestion은 게이팅 대상이 아니다. 실제 동작이 없는 신호일 뿐이고,
// 사용자는 승인이 아니라 답변으로 응한다 (선택지는 클라이언트가 직접 렌더).
// 여기서 끊지 않으면 승인 카드와 선택지가 같이 뜨고, 컨트롤 룸 피드까지 오염된다.
if req.ToolName == "AskUserQuestion" {
    writeJSON(w, http.StatusOK, services.PermissionDecision{
        Behavior:     "allow",
        UpdatedInput: req.Input,
    })
    return
}
```

### 클라이언트 필터링을 쓰지 않는 이유

`NativeChat`에서 `toolName === 'AskUserQuestion'`을 걸러내는 방식은 **작동하지 않는다.** `broker.Ask()`가 HTTP 응답을 열어둔 채 사람의 답을 기다리므로(`native_approve.go:53-58`), 승인 카드를 그리지 않으면 아무도 답하지 않아 Claude가 무한 대기한다. 서버에서 끊어야 한다.

서버에서 고치면 부수적으로 함께 해결되는 것:

- 컨트롤 룸 승인 피드에 유령 항목이 들어오지 않음
- `GET /api/approvals` 큐가 깨끗해짐
- "승인 필요" 푸시 알림이 선택지마다 날아가지 않음

### 안전성

`AskUserQuestion`은 파일을 쓰거나 명령을 실행하지 않는다. headless 모드에서 CLI가 내부적으로 "The user did not answer the questions."로 답하고, 실제 선택은 다음 사용자 턴으로 전달된다(`nativeEvents.ts:171-183`의 주석, `services/claude_driver.go:129-137`의 시스템 프롬프트). 따라서 auto-allow는 no-op이며 권한 게이트를 약화시키지 않는다.

---

## 6. 파일 정리

### 삭제

| 파일 | 사유 |
|---|---|
| `pages/DashboardPage.tsx` | 화면 통합 |
| `components/agent/AgentCard.tsx` | 죽은 페이지에 묶인 레이아웃 껍데기 |
| `components/agent/AgentGrid.tsx` | 동일 |
| `components/agent/AgentList.tsx` | 동일 |
| `components/agent/CreateAgentSheet.tsx` | 이미 도달 불가능한 죽은 UI (위 "생성 진입점" 참고) |

삭제 전 각 파일이 다른 곳에서 import되지 않는지 확인한다.

### 보존 (미사용)

`components/terminal/TerminalSnapshot.tsx`는 **삭제하지 않는다.**

기준: **알아내기 어려웠던 코드는 남기고, 배치일 뿐인 코드는 지운다.** 이 컴포넌트에는 headless wterm 인스턴스 관리, 300ms 스로틀 페인트, `open` 이벤트 시 재attach 같은 비자명한 로직이 있다. import하는 곳이 없으면 vite가 트리셰이킹하므로 번들 비용은 0이고, `tsc --noEmit`이 계속 타입 검사를 하므로 스토어·props 타입 변경은 여전히 잡힌다.

파일 상단에 의도 주석을 단다:

```
// 의도적으로 미사용 상태. 통합 관제실(2026-07)에서 타일 밀도를 위해 스냅샷을 뺐음.
// 되살리려면 AgentTile에서 마운트하되, 동시 인스턴스를 1개로 제한할 것 (headless wterm).
```

---

## 7. 에러 처리

- `controlSummaries()` 실패 → 기존 동작 유지
- `agent:meta` 미도착 → `AgentMeta`가 `null` 반환, 타일은 정상 렌더 (성능 저하 없음)
- 삭제 버튼 → 확인 단계를 거친 뒤 실행
- summaries의 `revision` 순서 역전 가드는 그대로 보존

---

## 8. 검증

- `cd client && ./node_modules/.bin/tsc --noEmit`
- `cd server && CGO_ENABLED=0 go build ./... && go test ./services/ ./ws/ ./handlers/`
- 실제 렌더 확인:
  - `/control` 첫 진입 시 git·포트가 **즉시** 보이는지 (10초 대기 없이)
  - `/dashboard` 접속 시 `/control`로 리다이렉트되는지
  - 터미널 → 로그 → 뒤로 → `/control`인지 (설정이 아니라)
  - 정지된 에이전트 타일에 `[삭제]`가, 실행 중 타일에 `[정지]`가 뜨는지
  - 모바일 BottomNav 3탭 + 덱 탭 알림 배지
  - **AskUserQuestion 호출 시 선택지 버튼만 뜨고 허용/거부 카드는 안 뜨는지**
  - 같은 상황에서 **컨트롤 룸 승인 피드가 비어 있는지**, "승인 필요" 푸시가 안 오는지
  - 선택지를 고르면 Claude가 정상적으로 다음 턴을 진행하는지 (auto-allow가 흐름을 막지 않는지)
  - 일반 도구(Bash/Write 등) 승인은 **여전히 정상 동작**하는지 — 회귀 확인
- `dist/pcd.exe` 재빌드 (클라이언트 변경이므로 필수)

---

## 9. 리스크

| 리스크 | 대응 |
|---|---|
| 접속 시 즉시 폴링이 다중 접속에서 git·포트 스캔을 반복 | 직전 폴링 3초 이내면 스킵 |
| 타일에 메타가 추가되어 벽 밀도가 떨어짐 | `AgentMeta`는 데이터 없으면 `null`이라 실제 증가분은 세션당 최대 2줄. 실사용 후 조정 |
| `AgentGrid`/`AgentList`가 예상 외의 곳에서 사용 중 | 삭제 전 import 확인 |
| 도구 이름 문자열 하드코딩(`"AskUserQuestion"`)이 CLI 변경 시 조용히 깨짐 | 상수로 분리하고, 회귀 검증 항목에 "선택지에 승인 카드가 안 뜨는지"를 포함 |

---

## 부록: 보류된 기능 — 터미널 스냅샷

타일에 실시간 터미널 미리보기를 띄우는 기능은 이번 범위에서 제외했다.

되살릴 경우:

1. `AgentTile`에서 `TerminalSnapshot`을 마운트하되 **동시 인스턴스를 1개로 제한**한다 (에이전트마다 headless wterm이 하나씩 생성됨). 펼침 상태 `expandedId: string | null`을 두고 펼친 타일에서만 렌더하는 방식.
2. 스냅샷은 80칼럼이므로 1/3 폭 타일에서는 가독성이 없다. 펼친 타일을 `col-span-full` 행으로 전개해야 제 비율이 나온다. 모바일(1컬럼)에서는 `col-span-full`이 무동작이라 동일한 메커니즘이 그대로 통한다.
3. **먼저 확인할 것:** `tsc`는 타입만 검사하므로 프로토콜 드리프트를 잡지 못한다. `terminal:output` 이벤트 이름과 페이로드 모양이 그대로인지 확인해야 한다. 어긋나면 타입은 통과하는데 화면만 빈 채로 남는다.
