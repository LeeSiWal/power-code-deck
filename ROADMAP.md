# Roadmap

## v0.3.0 — Control Room

PowerCodeDeck의 다음 주요 기능은 **멀티 에이전트 관제실(Control Room)**입니다.
여러 에이전트 세션을 한눈에 관리하는 관제실 화면입니다.

### 목표
- 여러 에이전트 세션을 한 화면에서 관리
- 프로젝트별 세션 그룹핑
- 실행 중 / 종료됨 / 주의 필요 상태 표시
- Claude / Codex / Shell 세션 빠른 진입
- 세션별 최근 출력 요약
- 세션 종료 / 재시작 / 로그 보기
- 승인 대기나 장시간 무응답 같은 주의 상태 표시

### 이번 버전(v0.2.0) 범위와의 관계
- v0.2.0에서는 기존 멀티 에이전트 대시보드를 **크게 수정하지 않고 유지**합니다(README의 Experimental로 분류).
- Control Room은 **v0.3.0 작업**으로 남깁니다. 큰 UI 재설계는 이번 버전에서 수행하지 않습니다.

## 방향 전환 (2026-07-17) — 네이티브 UI로

**결정: Antigravity(agy) 프리셋을 뺀다. Claude와 Codex에 집중한다.**

조사 결과 agy는 대화형 헤드리스 인터페이스가 없다 — 이벤트 스트림
(`--output-format stream-json`, 문서에 없는 숨은 플래그)은 있지만 **단방향**이라
승인도 후속 입력도 중단도 불가능하고, CLI 자신이 *"permission that headless mode
cannot prompt for"* 라고 말한다. 게다가 승인에 막혀 아무 일도 안 한 실행을
`status: SUCCESS`로 보고한다. ACP 지원 요청(이슈 #31)은 메인테이너 무응답 상태다.

반면 Claude와 Codex는 **양방향 구조화 스트림**이 있다:
- Claude — `-p --input-format stream-json --output-format stream-json` + `canUseTool`
  (허용/입력 수정 후 허용/규칙 저장/거부+메시지, `AskUserQuestion`, **`defer`**)
- Codex — `codex app-server` (JSON-RPC 2.0 / stdio, 공식 VS Code 확장이 쓰는 것)
  + `serverRequest` 승인

그래서 터미널로 TUI를 흉내내는 대신 **구조화 이벤트를 네이티브 UI로 그린다.**
그러면 우리 갭 목록의 대부분(폭 테이블, DEC 모드, 리플로우, 쿼리 응답, replay
직렬화, 마우스, 커서)이 고쳐지는 게 아니라 **존재하지 않게 된다.** 무엇보다
채팅 UI는 폰 너비로 리플로우된다 — TUI는 고정 격자라 그게 안 된다.

자세한 근거와 설계: [docs/terminal-stability-and-native-ui.md](docs/terminal-stability-and-native-ui.md)

### 다음
- [ ] Claude 네이티브 드라이버 (stream-json + 승인 + defer) — 스파이크
- [ ] 네이티브 채팅 UI (메시지/도구 호출/승인 버튼)
- [ ] Codex 드라이버 (`app-server`, `generate-json-schema`로 타입 생성)
- [ ] grace-time 재접속 (60초 → 6초, 두 트랙 공용)

### 터미널 트랙 — 셸용으로 동결
셸·vim·임의 CLI를 위해 남기지만, TUI 앱을 호스팅하기 위한 투자는 하지 않는다.
따라서 **서버 이뮬레이터(node 사이드카)는 불필요**하고 `pcd` 단일 바이너리를
유지한다. 마우스 전체 지원·커서 상태·스크롤백 DOM 재작성도 하지 않는다 —
전부 TUI 앱을 위한 비용이었다.

이미 들어간 것(유지):
- **DECRQM 쿼리 응답** — xterm.js와 동일한 올바른 동작. 어떤 TUI에도 필요하다.
- **ACK 기반 flow control** — 대량 출력 백프레셔 (VS Code와 같은 설계)
- **유니코드 V11** — 이모지 폭 1셀→2셀 (한글은 V6에서도 2셀이라 무관했다)
- **쓰기 권한 게이트** — attach된 뷰어만 세션에 쓸 수 있다
- **빈 화면 계측** — 렌더 정지의 원인(런 수/시간/DOM vs 버퍼)을 화면에 표시

## 완료됨

### Session Handoff (Continue on Mobile) — 구현 완료
- 데스크톱 세션을 모바일 / iPad로 이어받는 핸드오프 기능. **구현 완료.**
- QR 코드 + **일회용 토큰**(SHA-256 해시 저장, 기본 10분 TTL, 단일 사용, 세션 바인딩)으로 인계.
- Public URL / LAN 핸드오프 지원, 바인드 호스트 설정 가능(기본 `127.0.0.1`).
- 도착 시 모바일 Prompt Bar 자동 확장. 자세한 내용은 [README](README.md#session-handoff) · [CHANGELOG.md](CHANGELOG.md) 참고.

### v0.2.0 — PowerCodeDeck Renewal
- AgentDeck → PowerCodeDeck 리브랜딩, 버전 관리 도입
- 선택형 인증(none/PIN/password), 기본값 인증 없음
- `POWERCODEDECK_*` 환경변수 + `AGENTDECK_*` 하위 호환
- 자세한 내용은 [CHANGELOG.md](CHANGELOG.md) 참고
