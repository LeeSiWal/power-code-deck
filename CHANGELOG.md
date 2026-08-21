# Changelog

All notable changes to this project are documented here.

## Unreleased

### Removed
- **Local Intelligence (로컬 LLM 전처리) 제거** — 클라우드로 보내기 전에 로컬 LLM이 저장소 컨텍스트를 압축하던 실험 기능입니다. 실측 결과 **클라우드 비용 절감이 없었고**(모드별 5회 중앙값 $1.1604 vs $1.2661 — 차이가 CLOUD_ONLY 자체 편차 ±20% 안), 대신 턴마다 10~46초가 더 걸렸습니다. 채팅의 실행 모드 선택기(Cloud/Local/Hybrid), 설정의 Local Intelligence 카드와 활동 패널이 사라집니다. 측정 근거와 원인 분석은 `docs/local-intelligence-poc-report.md` §7a~§7d에 남아 있습니다.
- 이미 등록한 로컬 프로바이더와 실행 기록은 **DB에서 지우지 않습니다**(`local_ai_providers`·`intelligence_traces`). 그 기록이 위 결론의 근거이고, 코드만 걷어냈습니다.

## v0.6.1 — 할일 사이드 패널 · 체크리스트 복구 · 권한 모드 제자리 전환

### Added
- **할일 사이드 패널** — 우측 패널 탭 위에 항상 고정되는 구역. 계획을 세운 뒤 구현하는 동안 남은 양을 다른 탭으로 이동해도 잃지 않습니다. 높이는 드래그로 조절하고 새로고침 후에도 유지됩니다. 스플리터는 Pointer Events 기반이라 마우스와 터치(iPad 손가락) 모두에서 동작합니다. 할일이 없으면 고정 구역도 스플리터도 보이지 않으며, 우측 패널이 닫혀도 중앙 스트립이 `☑ n/m`을 계속 표시합니다.

### Fixed
- **할일 체크리스트가 계속 비어 있던 문제** — v0.5.0(2026-07-26) 출하 이후 사실상 동작하지 않았습니다. CLI가 2026-07-20경 체크리스트 도구를 `TodoWrite`(전체 스냅샷)에서 `TaskCreate`/`TaskUpdate`(델타)로 교체했는데 서버 파서는 `TodoWrite`만 알고 있었습니다. 오류가 아니라 침묵이었으므로 타입 검사로는 잡히지 않았습니다. 이제 두 계열을 모두 지원합니다 — `TodoWrite`는 스냅샷 교체, `Task*`는 세션별 작업 표 유지(생성·상태 변경·삭제). 실제 트랜스크립트 437개를 스캔해 검증했습니다.
- **한 프로젝트에 세션 둘을 열면 서로의 할일 목록을 보던 문제** — 활동 워처가 에이전트별이지만 파일 지목을 프로젝트 최신 파일(`mtime` 기준)로 하고 있었습니다. 이제 에이전트의 `claude_session_id`를 알면 해당 파일을 직접 지목합니다. 모르는 경우(터미널 트랙 등)는 기존 동작으로 물러납니다.
- **권한 모드를 바꾸면 진행 중인 작업이 멈추던 문제** — `SetMode`가 매번 `restart()`를 거쳐 CLI 프로세스를 종료했습니다. 이제 살아 있는 세션에 `set_permission_mode` 요청을 보내고 CLI의 응답을 확인한 뒤 전환합니다. 제자리 전환이 불가능한 드라이버(Codex, 구버전 CLI)만 재시작으로 물러납니다.
- **전체 허용 모드에서 승인 요청 없이 도구 호출이 스킵되던 문제** — 드라이버가 `bypassPermissions`일 때 `--mcp-config`와 `--permission-prompt-tool`을 통째로 뗐습니다. CLI가 승인을 물어야 할 일이 생겨도 물어볼 상대가 없어 조용히 거부했습니다. 승인 브리지를 모든 모드에서 상시 연결로 바꾸고, `bypassPermissions`를 CLI에 넘기지 않는 대신 서버 정책으로 전환했습니다 — `autoDecide`가 그 모드에서 모든 요청을 허용합니다.

### Internal
- `TodoRows` 공용 컴포넌트 추출 — 세션 스트립과 사이드 패널이 같은 렌더 로직을 공유합니다. 두 곳에 복제되면 한쪽만 바뀌는 순간 서로 다른 목록으로 보입니다.
- 승인 브리지를 모든 모드에서 상시 연결함에 따라, 클라이언트의 "전체 허용은 브리지 부재가 정상" 예외를 제거했습니다. 브리지 부재는 어느 모드에서든 진짜 고장입니다.

### Tests
- `services/activity_task_test.go` — `TaskCreate`/`TaskUpdate` 파싱(생성·상태 변경·삭제), `TodoWrite`와 혼용, 서브에이전트 무시, 437개 트랜스크립트 기반 검증.
- `services/activity_target_test.go` — `claude_session_id` 기반 파일 지목, 모르는 경우 폴백.
- `services/native_mode_switch_test.go` — `SetPermissionMode` 런타임 전환, 제자리 전환 실패 시 재시작 폴백.

## v0.6.0 — 승인 허용 목록

### Added
- **"항상 허용"** — 같은 명령을 한 번 허용하면 그 프로젝트에서 다시 묻지 않습니다. 규칙은 작업 디렉토리·도구·대상의 완전 일치이며, 설정에서 확인하고 지울 수 있습니다.
  - 패턴이 아니라 완전 일치인 이유는 예측 가능성입니다. 무엇이 허용될지 예측할 수 없는 규칙은 감사할 수 없습니다. 실제 성가심의 대부분은 반복되는 동일 명령이고, 인자가 매번 다른 호출은 오히려 확인해야 하는 쪽입니다.
  - 단, `Read`·`Grep`·`Glob`·`LS` 등 "대상"이 의미 없는 도구는 도구 단위 규칙이 됩니다. `Read /secret/creds.json`을 허용하면 그 프로젝트 안의 모든 `Read` 호출이 자동 허용됩니다 — 특정 파일이 아니라 도구 자체를 신뢰한다는 뜻입니다. 승인 카드가 "이 프로젝트에서 Read 도구 전체를 다시 묻지 않습니다"라고 미리 알리는 것은 이 때문입니다.
  - **위험한 호출은 버튼 자체가 없습니다.** 서버가 호출 시 `CanRemember`를 계산해 페이로드에 포함시키고, 클라이언트는 그 값이 false이면 버튼을 렌더하지 않습니다. 규칙으로 저장할 수 없는 호출에는 버튼도 보이지 않으니, 사용자가 실수로 눌러 저장을 시도할 일이 없습니다. 저장 요청이 오더라도 서버는 한 번 더 거부합니다 — 클라이언트 우회에 대한 방어선입니다. `rm`·`sudo`·`git push`에 영구 규칙을 허용하면 승인 게이트의 존재 이유가 사라지기 때문입니다.
  - **규칙은 모드를 이기지 않습니다.** 플랜 모드에서는 규칙이 있어도 실행하지 않습니다.

### Tests
- `services/approval_rules_test.go` — 규칙 저장·완전 일치·공백 정규화·프로젝트 범위·위험 명령 저장 거부·적용 시점 재검사·plan 모드 우선·nil 스토어 안전, 총 11개.
- `handlers/approval_rules_test.go` — REST 왕복: 저장 → `GET /api/approval-rules` → `DELETE /api/approval-rules/{id}` → `Allows` false 확인.

## v0.5.0 — 할일 체크리스트 · 알림 센터 · 동반 셸

### Added
- **할일 체크리스트** — Claude가 `TodoWrite`로 세운 계획을 세 곳에서 보여줍니다. 관제실 타일의 한 줄(`☑ 3/7 · 진행 중인 항목`), 세션 하단의 접히는 스트립, 그리고 채팅 안의 접힌 카드. 서버가 이미 읽고 있던 트랜스크립트에서 파싱하므로 새 데이터 소스가 없고, **터미널(PTY) 세션에서도 동작**합니다. 매 호출이 전체 스냅샷이라 병합 로직도 없습니다.
  - 스트립은 활동 노드의 20초 정리 규칙을 따르지 않습니다 — 에이전트가 잠깐 조용할 때 체크리스트가 사라지면 안 되기 때문입니다.
  - 서브에이전트가 부르는 `TodoWrite`는 무시합니다. 그러지 않으면 하위 작업의 목록이 메인 목록을 덮어씁니다.
- **알림 센터** (`/notifications`) — 지난 알림을 모아 봅니다. 읽음/안읽음 구분, 종류별 색, 세션 이름과 시각. 탭하면 그 세션으로 이동합니다. 그동안 알림은 사라지는 토스트와 배지 숫자뿐이라 토스트를 놓치면 그대로 사라졌습니다.
  - 진입점 네 곳: 모바일 하단 탭(Alerts) · 모바일 세션 헤더 · 데스크톱 관제실 헤더 · 데스크톱 세션 헤더. 세션 화면에는 하단 탭이 없어 그동안 세션 안에서는 알림으로 갈 길이 아예 없었습니다.
  - 모바일 세션 헤더의 자리는 핸드오프 버튼에서 가져왔습니다. 핸드오프는 데스크톱 세션을 QR로 폰에 넘기는 기능이라 폰에서는 자기 자신에게 넘기는 셈입니다(데스크톱에는 그대로 유지).
- **동반 셸** — 네이티브 채팅을 유지한 채 같은 작업 디렉토리에서 명령을 직접 실행합니다. 패널을 닫거나 다른 화면을 다녀와도 출력과 현재 위치가 유지됩니다.
- 최신 Codex 모델(GPT-5.6 sol/terra/luna, 5.5, 5.4/5.4-mini). 카탈로그에서 사라진 모델 slug가 저장돼 있으면 기본값으로 되돌립니다 — 낡은 slug로 app-server를 띄우면 재시작이 실패해 채팅이 빈 화면으로 보였습니다.

### Changed
- **앱을 보고 있는 기기에는 Web Push를 보내지 않습니다.** 인앱 토스트가 이미 같은 알림을 띄우므로 한 사건에 두 번 알리던 것을 하나로 줄입니다. 브라우저가 `visibilitychange`를 명시적으로 보고하며, WebSocket 연결이 살아 있는 것으로 판정하지 않습니다 — 잠긴 폰도 소켓은 한동안 유지되므로 그걸 근거로 삼으면 정작 필요한 순간에 억제됩니다. 억제는 보수적입니다: 보고가 없으면 보내고, 소켓이 끊기면 즉시 해제합니다.
- 관제실 타일에서 **리스닝 포트 표시를 제거**했습니다. 표시된 번호가 이 에이전트의 것이 아니라 머신 전체의 LISTEN 포트였습니다(실측 25개, `pcd` 자신·ssh·postgres 포함). 링크도 원격 접속에서는 `localhost`가 접속한 기기 자신을 가리켜 동작하지 않았습니다. 실제로 동작하는 경로는 세션 안의 Browser 패널입니다.

### Fixed
- **셸에서 암호를 입력할 수 없던 문제** — `sudo`·`ssh`·`git`의 암호 프롬프트에서 입력이 화면에 그대로 보이고, 입력해도 통과하지 못했습니다. 입력을 bracketed paste로 감싸 보내는데 그 모드는 애플리케이션이 켜야 성립하며 암호 프롬프트는 켜지 않으므로, 래퍼 바이트가 비밀번호의 일부로 전달돼 조용히 틀린 암호가 됐습니다. 이제 암호 프롬프트를 감지해 화면에서 가리고 평문으로 보냅니다.
- **전체 허용 모드에서 "승인 브리지가 연결되지 않았습니다" 오탐 배너** — 그 모드에서는 브리지를 걸지 않는 것이 정상입니다(플래그와 승인 툴이 함께 있으면 CLI가 오히려 거부합니다). 아무것도 거부되지 않는데 "전부 자동 거부"라 경고해 세션이 고장난 것처럼 보였습니다. 다른 모드에서는 실제로 거부되므로 경고를 유지합니다.
- **전체 허용·자동 모드에서 모드를 바꾼 직후 작업이 멈추던 문제** — 모드 전환은 세션을 재시작하는데, 그 사이 권한 정책을 읽을 수 없어 요청이 사람에게 넘어갔습니다. 전체 허용 사용자는 승인 카드를 보고 있지 않고 승인 대기에는 타임아웃이 없어 도구 호출이 무기한 매달렸습니다. 권한 모드를 세션 객체와 분리해 보관합니다 — 모드는 프로세스가 아니라 사용자 선택에 속하며 재시작이 그것을 바꾸지 않습니다.
- 데스크톱·태블릿 관제실 헤더에서 **로그·설정 진입점 복구**. v0.4.0에서 대시보드를 흡수할 때 함께 사라져, 하단 탭이 없는 화면에서는 갈 방법이 없었습니다.
- **세션을 다시 열면 활동 표시가 비어 보이던 문제** — 붙는 시점에 서버가 최신 활동 스냅샷을 직접 밀어줍니다. 그 전에는 다음 폴링 틱까지 기다려야 했습니다.
- 채팅 입력창이 **비어 있어도 스크롤바가 보이던 문제**.
- 화면 첫 진입 시 git·포트가 최대 10초간 비어 있던 문제.

### Internal
- 알림에 `ref_type`/`ref_id` 참조 필드 추가 — 알림에서 해당 작업으로 바로 이동하는 기능의 선행 작업입니다.
- 알림 라벨·색을 한 모듈로 공유합니다. 같은 이벤트가 토스트와 목록에서 다른 이름으로 보이면 서로 다른 사건처럼 읽힙니다.

### Tests
- `ws/hub_foreground_test.go` — 푸시 억제의 비대칭 고정(미보고=보냄, 기기별 격리, 소켓 종료 시 해제).
- `services/native_restart_policy_test.go` — 재시작 구간에도 권한 모드가 유지되는지, 그리고 모르는 세션은 여전히 사람에게 묻는지.
- `services/activity_test.go` — `TodoWrite` 파싱(최신 스냅샷 유지, 서브에이전트·잘못된 입력 무시, 유휴에도 목록 유지).

## v0.4.0 — 통합 관제실 (Unified Control Room)

> **에이전트 목록 화면이 하나로 합쳐집니다.** `/dashboard`는 `/control`로 리다이렉트되며, 북마크·핸드오프 링크·설치된 PWA는 그대로 동작합니다.

### Added
- **관제실 타일에 git·포트 메타 표시** — 브랜치·ahead 커밋 수·dirty 표시와 리스닝 포트(클릭하면 `http://localhost:{port}` 새 탭)가 타일에 직접 나옵니다. 대시보드를 따로 열 필요가 없어졌습니다.
- **관제실 헤더에 프로젝트 추가 버튼** — 대시보드가 갖고 있던 유일한 생성 진입점을 옮겼습니다.
- **정지된 세션 삭제** — 타일 액션이 실행 상태에 따라 바뀝니다. 실행 중이면 되돌릴 수 있는 **정지**, 이미 멈춘 세션이면 **삭제**(확인 후). 되돌릴 수 없는 삭제가 살아 있는 세션에 노출되지 않으므로 밀도 높은 화면에서 오클릭으로 세션을 잃지 않습니다.

### Changed
- **대시보드가 관제실에 흡수됨** — 같은 데이터를 보여주는 화면이 둘이라 승인은 관제실, 생성은 대시보드, 포트 확인은 다시 대시보드로 오가야 했습니다. 이제 `/control` 하나입니다.
- **뒤로 가기가 히스토리 기반에서 계층 기반으로** — `navigate(-1)`("마지막 이동 취소")을 화면마다 부모를 하나씩 선언하는 방식으로 교체했습니다. 세션·로그·설정·프로젝트 선택의 부모는 `/control`, 런처의 부모는 `/`입니다. 탭 이동이 히스토리에 쌓여 엉뚱한 곳으로 가던 문제와, 관제실 진입 경로가 둘이라 뒤로 가기가 터미널로 되돌아오던 루프가 사라집니다. 딥링크·PWA 콜드 스타트에서도 동일하게 동작합니다.
- **모바일 하단 탭 4개 → 3개** (`Deck` / `Logs` / `Settings`) — 통합으로 `Home`과 `Control`이 같은 화면이 됐습니다. 알림 배지는 `Deck` 탭으로 옮겼고, 탭 전환은 `replace`라 히스토리가 쌓이지 않습니다.
- **터미널 헤더의 관제실 아이콘 제거** — 뒤로 가기가 이제 관제실로 가므로 같은 목적지 버튼이 둘이었습니다.
- 커맨드 팔레트의 `대시보드` 항목이 `관제실`로 바뀌고 `/control`을 직접 가리킵니다(`⌘D` 유지).

### Fixed
- **`AskUserQuestion`에 승인 카드가 뜨던 문제** — Claude가 선택지를 물으면 선택지 버튼과 허용/거부 카드가 **동시에** 떴습니다. 승인 브리지가 모든 도구 호출을 권한 게이트로 넘기는데 도구 이름 필터가 없었기 때문입니다. `AskUserQuestion`은 파일을 쓰지도 명령을 실행하지도 않는 신호이고 사용자는 승인이 아니라 답변으로 응하므로, 권한 브로커에 닿기 전에 서버가 즉시 허용합니다. 관제실 승인 피드 오염과 "승인 필요" 푸시 알림도 함께 사라집니다. 일반 도구(Bash/Write 등)의 승인 게이트는 그대로입니다.
- **화면 첫 진입 시 git·포트가 최대 10초간 비어 있던 문제** — `agent:meta`를 보내는 경로가 10초 티커뿐이고 접속 시점 스냅샷이 없었습니다. WebSocket 클라이언트가 붙으면 즉시 한 번 보내되, 여러 기기가 동시에 접속할 때 git·포트 스캔이 반복되지 않도록 3초 디바운스를 겁니다.
- **프로젝트 선택 화면의 막다른 길** — 관제실의 "프로젝트 추가"로 들어가면 이 화면에는 뒤로 버튼도 하단 탭도 없어 설정·로그아웃 말고는 나갈 길이 없었습니다. 상위 이동 버튼을 모든 화면 크기에서 보이도록 추가했습니다.

### Removed
- `DashboardPage` · `AgentCard` · `AgentGrid` · `AgentList` · `CreateAgentSheet` · `useAgents` · `useGoBack` 삭제. `CreateAgentSheet`는 이미 도달 불가능한 UI였습니다(열리는 경로가 없었음).
- 관제실 타일의 **로그** 버튼 제거 — 필터 없이 전역 로그로 갈 뿐이라 밀도만 소모했습니다. 로그는 하단 탭과 헤더로 갑니다.
- 타일의 서브에이전트 표시 제거 — 활동 스트림은 세션을 열어야만(`watchingAgent` 필터) 도달하므로 관제실에서는 구조적으로 항상 비어 있었습니다. 되살리려면 요약 데이터에 활동 정보를 실어야 합니다.

### Internal
- `ControlRoomPage.tsx` 513줄 → 225줄. `liveState` / `LiveDot` / `AttentionRail` / `AgentTile` / `ProjectGroup` / `ApprovalFeed`로 분리했습니다.
- `TerminalSnapshot`은 미사용 상태로 보존하되 파일 상단에 의도와 되살리는 방법을 명시했습니다(headless 터미널 인스턴스 관리·스로틀 페인트 로직이 재유도하기 번거롭기 때문). 되살릴 때는 `terminal:output` 이벤트의 이름·페이로드 변화를 먼저 확인해야 합니다 — 타입 검사로는 잡히지 않습니다.
- `pnpm-lock.yaml`을 `package.json`에 동기화했습니다(wterm 전환 이후 어긋나 있었음).

### Tests
- `handlers/native_approve_test.go` — `AskUserQuestion` 즉시 허용, 그리고 일반 도구가 여전히 사람을 기다리는지(권한 게이트 약화 회귀 가드).
- 서버 전체 스위트 + `-race` 통과, 클라이언트 `tsc --noEmit` 무오류.

## v0.3.0 — Control Room (멀티 세션 관제실)

> 이 릴리스는 CHANGELOG에 기록되지 않은 채 배포됐습니다. v0.4.0 작업 중 발견해 소급 정리합니다.

### Added
- **Control Room (`/control`)** — 여러 에이전트 세션을 한 화면에서 관리합니다. 프로젝트별 그룹핑, 동작중/대기/정지 3상태 애니메이션, 세션별 최근 도구·대상·활동 시각 요약.
- **Attention 레일** — 승인 대기·에러·정체 세션을 긴급도순(승인 > 에러 > 정체, 같으면 오래 기다린 순)으로 상단에 모아 보여줍니다.
- **전역 승인 큐** — 여러 세션의 승인 요청을 한곳에서 처리합니다. 세션을 열지 않아도 되며, 데스크톱은 우측 패널, 모바일은 하단 시트로 나옵니다.
- **세션 정지 / 재시작** — 정지는 레코드를 남기는 되돌릴 수 있는 동작입니다.
- **플러그인 관리** — `/plugin` 마켓플레이스 검색·설치·활성화.
- **UI 크기 조절** — 해상도 비례 자동 스케일 + 설정의 수동 배율.

## v0.2.6 — Codex 네이티브 UI

### Added
- **Codex `app-server` 드라이버** — JSON-RPC 2.0/stdio로 Codex 스레드 시작·재개, 턴 전송·중단, 명령/파일 변경 승인 응답을 처리합니다.
- **Claude·Codex 공통 네이티브 세션 인터페이스** — 기존 Claude 네이티브 채팅의 반응형 메시지, 도구 호출/결과, 승인 카드, 모델·권한 모드, 대화 재접속 경험을 Codex에도 적용합니다.
- Codex 명령 실행, 파일 변경, MCP 도구 호출을 기존 네이티브 도구 카드 형식으로 정규화합니다.

### Changed
- Codex 프리셋은 기본적으로 PTY TUI 대신 네이티브 채팅을 사용합니다. URL에 `?terminal`을 붙이면 기존 터미널 경로를 사용할 수 있습니다.
- 프로젝트 한 줄 소개와 README 아키텍처를 터미널 중심 설명에서 Claude·Codex 구조화 네이티브 워크스페이스 중심으로 갱신했습니다.

### Tests
- Go 서비스/웹소켓/핸들러 테스트와 TypeScript/Vite 프로덕션 빌드를 검증했습니다.

## v0.2.5 — 리버스 프록시 접속 복구 (fix)

> **v0.2.4에서 리버스 프록시/도메인으로 접속하던 배포가 깨졌던 문제를 수정합니다.** 도메인만 `CORS_ORIGINS`에 설정한 사용자는 v0.2.4로 올리면 에이전트가 "connecting"에서 멈췄습니다 — 아래 수정으로 해결됩니다.

### Fixed
- **Host 검증이 `CORS_ORIGINS`를 존중** — v0.2.4의 DNS-rebinding Host 가드는 `PUBLIC_URL`/`LAN_URL`/`BIND_HOST`/`ALLOWED_HOSTS`만 허용 목록으로 읽어, 도메인을 `CORS_ORIGINS`에만 설정한 리버스 프록시 배포가 모든 요청에서 403 "forbidden host"를 받고 WebSocket이 연결되지 않았습니다(에이전트 목록/터미널이 "connecting"에서 멈춤). 이미 신뢰하는 Origin의 Host도 신뢰하도록 `AllowedHosts()`가 `CORS_ORIGINS`의 호스트를 포함합니다. 기본값(빈 `CORS_ORIGINS`)에서는 loopback 전용 동작이 그대로라 보안 약화가 없습니다.

### Tests
- `config/config_test.go` — `CORS_ORIGINS` 도메인이 Host 허용 목록에 포함되는지, 그리고 미설정 시 loopback 전용 기본값이 유지되는지 확인.

## v0.2.4 — 보안 강화 (security hardening)

> **v0.2.3 이하는 아래 취약점이 있으므로 업그레이드가 필요합니다.** 특히 무인증(기본값) 모드에서 로컬에 열려 있으면 악성 웹페이지가 접근할 수 있었습니다.

### Security
- **WebSocket Origin 검증** — `/ws`가 모든 Origin을 허용하던 것을 허용 목록 기반 검증으로 교체. 임의의 웹페이지가 `ws://localhost/ws`에 붙어 터미널에 명령을 주입하는 drive-by 공격을 차단합니다(브라우저가 아닌 CLI 등 Origin 헤더가 없는 클라이언트는 허용).
- **WebSocket 토큰 상시 요구** — 무인증 모드에서도 `/ws`가 항상 유효한 토큰을 요구합니다. 로컬 브라우저는 `POST /api/auth/anonymous`(무인증 모드 + 로컬 Origin 한정)에서 익명 토큰을 발급받아 사용합니다. 기존 무인증 UX(로그인 화면 없음)는 그대로 유지됩니다.
- **파일 API 경로 검증 상시 적용** — `agentId`를 생략하면 경로 검증을 건너뛰어 임의 절대경로 read/write/delete/rename이 가능하던 문제를 수정. 이제 모든 파일 작업이 허용 base(에이전트 작업 디렉토리, 또는 워크스페이스 루트/홈 + 최근 프로젝트) 안으로 제한되며, `~/.ssh`·`~/.aws`·`~/.gnupg` 등 민감 디렉토리는 명시적으로 차단됩니다.
- **ValidatePath prefix 우회 버그 수정** — `/base`가 `/base-evil`도 통과시키던 `HasPrefix` 검사를 `filepath.Rel` 기반으로 교체하고, 아직 존재하지 않는 쓰기 경로는 가장 가까운 실제 부모를 심링크 해석해 base 이탈을 차단합니다.
- **Refresh 토큰의 access 통용 차단** — access 토큰에 `type:"access"` 클레임을 추가하고 인증·WS 검증에서 타입을 확인합니다. 30일짜리 refresh 토큰을 API 자격증명으로 쓸 수 없습니다(v0.2.4 이전 발급된 무타입 토큰은 만료까지 access로 허용하는 마이그레이션 유예 포함).
- **Host 헤더 검증(DNS rebinding 방지)** — Host가 localhost/127.0.0.1/`[::1]`(및 PUBLIC_URL/LAN_URL/BIND_HOST/`ALLOWED_HOSTS`) 허용 목록에 없으면 403. 모든 라우트에 전역 적용됩니다.

### Changed
- **Graceful shutdown** — `http.ListenAndServe` 대신 `http.Server`를 사용해 SIGINT/SIGTERM 시 5초 컨텍스트로 in-flight 요청을 정리한 뒤 DB를 닫습니다. 활성 PTY 세션은 의도적으로 유지(Detach ≠ Kill)되며 프로세스 종료와 함께 정리됩니다.
- `POWERCODEDECK_ALLOWED_HOSTS`(쉼표 구분) 환경변수 추가 — 리버스 프록시 도메인이나 커스텀 호스트로 접근할 때 Host 검증 허용 목록에 추가합니다.

### Tests
- `services/file_test.go` — ValidatePath 테이블 테스트(정상/`..` traversal/prefix 우회/심링크 이탈/미존재 쓰기 경로) 및 민감 디렉토리 차단.
- `auth/auth_test.go` — access/refresh 타입 분리 및 레거시 무타입 토큰 허용.
- `ws/hub_test.go`, `middleware/hostcheck_test.go` — Origin/Host 허용·차단.

## v0.2.3 — cgo-free, natively cross-compilable (incl. Windows .exe)

### Fixed
- **Native Windows: agents now launch.** Windows `CreateProcess` (used by ConPTY) can't run `.cmd`/`.bat`/`.ps1` shims directly, and npm-installed CLIs (`claude`, `gemini`, `codex`) are `.cmd` shims — so clicking Launch Agent silently did nothing. On Windows the engine now routes non-`.exe` commands through `cmd.exe /c`.
- Agent-launch failures are now surfaced to the user (alert) instead of only logged to the console, so a failed launch is no longer a silent "no-op".

### Changed
- **Visible data folder + shortcuts.** The install/data directory moved from the hidden `~/.powercodedeck` to **`~/PowerCodeDeck`** so non-developers can find it; existing installs are migrated automatically (the DB/.env move with it). On Windows the installer now creates a **Desktop + Start Menu shortcut** (launch and "데이터 폴더 열기" in Explorer) and a one-word **`pcd`** command, so no long WSL command is needed. The SQLite filename (`powercodedeck.db`) and bundle id are unchanged.
- **No more cgo / C toolchain.** SQLite driver switched from `mattn/go-sqlite3` (cgo) to **`modernc.org/sqlite`** (pure Go), and the PTY layer from `creack/pty` (Unix-only) to **`aymanbagabas/go-pty`** (Unix PTY on mac/Linux, **ConPTY on Windows**). `pcd` now builds with `CGO_ENABLED=0` — no gcc/build-essential required.
- `install.sh` no longer installs `build-essential`; it only needs `git`, `curl`, `ca-certificates`. Builds use `CGO_ENABLED=0`.

### Added
- **Terminal copy / paste.** xterm renders to a canvas, so its selection isn't a DOM selection and native Cmd+C copied nothing. Copy is now wired to **Cmd+C** (macOS) / **Ctrl+Shift+C**, with a floating **복사** button that appears while text is selected (also works on touch); paste to **Cmd+V** (native) / **Ctrl+Shift+V**. A bare Ctrl+C still sends SIGINT. Includes an execCommand fallback for non-secure contexts (LAN handoff over http).
- **Native Windows binary** — `make build-windows` produces `pcd.exe` (`GOOS=windows CGO_ENABLED=0`), a real PE32+ executable with no WSL and no cgo. The WSL installer remains the tested/recommended path until the native `.exe` is validated on Windows hardware.
- `Makefile` targets build with `CGO_ENABLED=0`; `make build-windows` for the native `.exe`.

## v0.2.2 — tmux-free session engine + easy Windows install

### Changed
- **Session engine refactor** — terminal/agent sessions now go through a single `SessionEngine` interface. The web/API/WebSocket layers no longer touch the session runtime directly. The invariant **"Detach is not Kill"** is enforced: a browser disconnect only detaches the viewer; the shell/Claude process keeps running. Only Kill / Restart / Delete end the process.
- **tmux removed** — PowerCodeDeck now uses its own in-process `InternalPtySessionEngine` (owns each session's PTY process directly via `creack/pty`, with a per-session scrollback ring buffer replayed on reconnect). tmux is no longer a runtime dependency, is no longer installed by `install.sh`, and `TmuxSessionEngine`/`tmux.go`/`pty.go` were deleted. mac/Linux run natively without tmux; native Windows (go-pty/ConPTY) is future work.
- `POWERCODEDECK_SESSION_ENGINE` is **deprecated** — the internal engine is always used; a set value logs a warning and is otherwise ignored.

### Added
- **One-line Windows installer** — `iwr -useb .../win-install.ps1 | iex` sets up WSL + Ubuntu (no interactive account — runs as root), reboots with confirmation and **auto-resumes after login**, then builds PowerCodeDeck. Falls back to **WSL1 when CPU virtualization is off**, and prints ASCII/English so consoles don't show `???`.
- `POWERCODEDECK_SESSION_SCROLLBACK_BYTES` (default `524288`) — per-session replay buffer size.
- `docs/session-engine.md` documenting the engine, the Detach≠Kill rule, server-restart behavior, and the future `pcd-sessiond` split.
- Beginner-friendly Windows install guidance in the README.

### Notes
- If the `pcd` **server process** restarts, live sessions may stop (session lifetime is tied to the server for now); agents are not auto-respawned — press Restart. The legacy `tmux_session` DB column is kept but unused.

## v0.2.1 — Session Handoff

### Added
- **Session Handoff (Continue on Mobile)** — hand off an active terminal or Claude session from desktop to mobile / iPad by scanning a one-time QR code, attaching to the same tmux session.
  - One-time handoff tokens: **SHA-256 hashed** (raw tokens are never stored in the database), **10-minute TTL** by default, **single-use**, and **session-bound**.
  - New `POST /api/agents/:id/handoff` API to mint a handoff token/QR, and `GET /handoff/:token` redeem endpoint.
  - Session-scoped handoff cookie set on redeem so the mobile client lands on the correct session.
  - LAN + public URL support: QR encodes `POWERCODEDECK_PUBLIC_URL` (proxy/domain) or `POWERCODEDECK_LAN_URL` (same Wi-Fi) as configured.
  - Configurable server bind host via `POWERCODEDECK_BIND_HOST` (default `127.0.0.1`; set `0.0.0.0` for LAN handoff).
  - Mobile **Prompt Bar auto-expands** on handoff arrival for Korean / long prompts.
- New environment variables: `POWERCODEDECK_PUBLIC_URL`, `POWERCODEDECK_HANDOFF_ENABLED` (default `true`), `POWERCODEDECK_HANDOFF_TOKEN_TTL_SECONDS` (default `600`), `POWERCODEDECK_LAN_HANDOFF_ENABLED` (default `false`), `POWERCODEDECK_LAN_URL`, `POWERCODEDECK_BIND_HOST` (default `127.0.0.1`).

### Security
- Raw handoff tokens are never persisted — only their SHA-256 hash is stored and compared.
- Tokens expire (default 10 min), are single-use, and are bound to a specific session.
- Documentation warns against exposing PowerCodeDeck directly without authentication — especially when auth is disabled **and** LAN handoff is enabled — recommending PIN/password auth, Caddy + Authelia, Tailscale, VPN, or an SSH tunnel.

### Compatibility
- All new handoff variables honor the legacy `AGENTDECK_*` prefix as well; `POWERCODEDECK_*` wins when both are set.

## v0.2.0 — PowerCodeDeck Renewal

### Changed
- Renamed **AgentDeck → PowerCodeDeck**. Binary is now `pcd`.
- Introduced version management (`server/version`, startup banner, `pcd version`, `/api/auth/health`).
- Changed first-run authentication setup. Authentication is now **optional and disabled by default**.
- Added support for new `POWERCODEDECK_*` environment variables while keeping `AGENTDECK_*` compatibility.
- Unified terminal input around a **single interactive terminal** (from the earlier CHAT/RAW dual mode). The terminal handles commands, arrow-key menus, y/n approvals, Tab, Esc, and Ctrl+C directly.

### Added
- Device-aware **Prompt Bar** for Korean / long / multi-line prompts. Text is composed in a native textarea (correct IME composition) and pasted into the current terminal — **Send** adds Enter, **Paste** does not, plus **Clear** and **터미널 조작** (focus terminal). It never interprets Claude state or handles approvals; it only pastes text.
  - **Desktop**: optional overlay, toggled by the Prompt button or Cmd/Ctrl+K / Cmd/Ctrl+P; Esc closes.
  - **Mobile / iPad (touch)**: always shown and collapsible (never fully closed), because typing Korean directly into xterm splits the jamo (ㅇㅏㄴ instead of 안).
- On-screen PTY control-key bar (arrows, Enter, Esc, Tab, ⇧Tab, y/n, Ctrl+C/D) on desktop and mobile, so interactive CLI menus work without a physical keyboard.
- One-time hint when Korean is typed straight into the terminal on touch devices, pointing the user to the Prompt Bar.
- First-run authentication selection: **none**, **PIN**, or **password** (interactive wizard when run in a TTY).
- Startup security warning when authentication is disabled.
- `GET /api/health` (alias of `/api/auth/health`) exposing `appName`, `version`, `authEnabled`, `authMethod`.
- Password authentication with a dependency-free salted, iterated SHA-256 hash.
- Roadmap entry for **v0.3.0 Control Room**.

### Security
- Authentication is disabled by default for local / proxied deployments.
- Secrets (PIN / password) are never printed to the startup log.
- Passwords are stored hashed, never in plaintext.
- Added documentation warning not to expose PowerCodeDeck directly to the public internet.

### Compatibility
- Existing `AGENTDECK_*` environment variables remain supported; `POWERCODEDECK_*` wins when both are set.
- A legacy `.env` with only `AGENTDECK_PIN` is treated as PIN authentication.
- Data locations (`~/.agentdeck/`, `agentdeck.db`) are unchanged so existing installs keep their agents and settings.

## v0.1.0 — AgentDeck (MVP)
- Initial release: multi-agent web terminal for Claude Code / Gemini CLI / Codex CLI, file explorer, dashboard, PIN authentication with an auto-generated PIN.
