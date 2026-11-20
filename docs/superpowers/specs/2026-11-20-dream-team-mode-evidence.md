# 드림팀 모드 — 토큰 관문 evidence

설계 문서: `2026-11-20-dream-team-mode-design.md` §5.9. 이 파일은 출시 전 관문(모드 켬의 Claude 사용량 ≤ 모드 끔의 110%)의
절차와 결과를 남긴다. **실제 CLI 실행은 사용자의 한도를 쓰므로 사용자 동의 후에만 한다.** 숫자는 지어내지 않는다.

## 상태

- 절차: 작성됨 (아래).
- 결과: **미실행 — 사용자 동의 필요.**
- `TestCodexTeamLive` (`server/services/codex_team_live_test.go`, `RUN_CODEX_TEAM_LIVE=1`): **미실행 — 사용자 동의 필요.** 실제 Codex 지휘자가
  pcd MCP 서버로 `ask_member`를 부르는지 확인하는 테스트로, Codex 한도를 쓴다.

## 절차

### 준비
1. 최신 `feat/dream-team` 빌드로 pcd를 띄운다. `PCD_V2_ENABLED`는 결과에 영향이 없다.
2. Settings → 드림팀은 기본값 그대로 둔다(직원: Codex · Claude Sonnet · Claude Haiku 켬, Claude Fable 끔 / 결과 6000자 /
   턴당 3회 / 세션 토큰 300000 / 시간 제한 180초).
3. 지휘자는 **Claude Opus**로 고정한다. `auto_turns`는 "자동" 세션의 지휘자 턴만 기록하므로, 과제마다 **새 "자동" 세션**을 만들고
   모델을 Claude Opus로 고정해 둔다(턴 중 모델 전환이 일어나지 않도록 한 과제는 한 메시지로 보낸다).
4. 과제 하나당 세션 두 개: 모드 끔 세션, 모드 켬 세션(첫 메시지 전에 입력창의 드림팀 토글을 켠다). 같은 커밋에서 시작하고,
   과제 사이에 `git stash`/`git checkout .`로 작업 폴더를 되돌린다.

### 과제 (이 저장소에서)
| # | 과제 문구 |
|---|---|
| T1 | "승인 브리지가 cli/, handlers/, services/에 걸쳐 어떻게 동작하는지 요약하고, 그 과정에서 보이는 오타 하나를 고쳐 줘." |
| T2 | "services/의 auto_usage.go, delegate.go, team.go를 읽고 자동 세션 사용량이 어디서 어떻게 기록되는지 정리한 뒤, 주석이 실제 동작과 다른 곳 하나를 고쳐 줘." |
| T3 | "client/src/components/native/ 아래 컴포넌트들이 채팅 이벤트를 어떻게 그리는지 훑어보고 독립적인 리뷰 의견 세 가지를 낸 다음, 가장 작은 하나를 반영해 줘." |

### 측정
- 세션마다 `GET /api/agents/{id}/auto-usage`를 읽는다.
  - **지휘자(Claude Opus)**: `models[]`의 `input + output + cacheCreation` 합 (cacheRead는 따로 적는다).
  - **팀 직원**: 응답의 `team` 합계(`calls`, `reported`, `unknown`, `input`, `output`, `cacheCreation`). `switch_kind='team'`
    행은 턴 통계에서 빠져 있으므로 이중 집계가 없다. Claude 직원 토큰은 Claude 한도를 쓰므로 관문 합계에 **더한다**.
  - **Codex 직원**: 사용량을 보고하지 않는다 → 호출 횟수만 적고 토큰은 "알 수 없음"으로 둔다.
- 관문: `Σ(모드 켬: 지휘자 + Claude 직원) ≤ 1.10 × Σ(모드 끔: 지휘자)` (세 과제 합). `reported < turns`인 행이 있으면
  그 과제는 "보고 누락"으로 표시하고 합계에서 빼지 않는다(관문 판정 보류).
- 통과 못 하면 출시하지 않고, 어느 기본값(직원 목록·턴당 횟수·결과 길이)을 바꿀지 보고한다. 숫자는 고치지 않는다.

## 결과

| 과제 | 모드 | 지휘자 Claude 토큰 (in+out+cacheCreation) | cacheRead | Claude 직원 토큰 | Codex 호출 수 | 비고 |
|---|---|---|---|---|---|---|
| T1 | 끔 | 미실행 — 사용자 동의 필요 | | | | |
| T1 | 켬 | 미실행 — 사용자 동의 필요 | | | | |
| T2 | 끔 | 미실행 — 사용자 동의 필요 | | | | |
| T2 | 켬 | 미실행 — 사용자 동의 필요 | | | | |
| T3 | 끔 | 미실행 — 사용자 동의 필요 | | | | |
| T3 | 켬 | 미실행 — 사용자 동의 필요 | | | | |
| 합계 | 켬 / 끔 | 미실행 — 사용자 동의 필요 | | | | 관문: ≤ 110% |
