# Roadmap

## v0.3.0 — Control Room

PowerCodeDeck의 다음 주요 기능은 **멀티 에이전트 관제실(Control Room)**입니다.
여러 에이전트 세션을 한눈에 관리하는 관제실 화면입니다.

### 목표
- 여러 에이전트 세션을 한 화면에서 관리
- 프로젝트별 세션 그룹핑
- 실행 중 / 종료됨 / 주의 필요 상태 표시
- Claude / Antigravity / Codex / Shell 세션 빠른 진입
- 세션별 최근 출력 요약
- 세션 종료 / 재시작 / 로그 보기
- 승인 대기나 장시간 무응답 같은 주의 상태 표시

### 이번 버전(v0.2.0) 범위와의 관계
- v0.2.0에서는 기존 멀티 에이전트 대시보드를 **크게 수정하지 않고 유지**합니다(README의 Experimental로 분류).
- Control Room은 **v0.3.0 작업**으로 남깁니다. 큰 UI 재설계는 이번 버전에서 수행하지 않습니다.

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
