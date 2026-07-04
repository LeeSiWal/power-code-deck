# PowerCodeDeck Architecture

PowerCodeDeck v0.2.0 (구 AgentDeck) — 프로젝트 터미널 + AI 코딩 에이전트 웹 콘솔. Go 단일 바이너리(`pcd`) + 임베디드 React SPA.

```
총 코드: ~11,400줄 (Go 4,313 + TS/CSS 7,132)
서버 파일: 34개  |  클라이언트 파일: 69개
```

---

## 시스템 개요

```
┌─────────────────────────────────────────────────────────┐
│                    브라우저 (React SPA)                    │
│  ┌──────────┬──────────────┬───────────────┐            │
│  │ 파일탐색기 │    터미널     │  서브에이전트   │            │
│  │          │  (xterm.js)  │  / 브라우저     │            │
│  └──────────┴──────────────┴───────────────┘            │
│         ↕ REST API          ↕ WebSocket                  │
├─────────────────────────────────────────────────────────┤
│                   Go 서버 (단일 바이너리)                   │
│  ┌────────┐ ┌────────┐ ┌─────────────┐ ┌──────────────┐ │
│  │ Router │→│Handler │→│SessionEngine│→│ tmux + PTY   │ │
│  └────────┘ └────────┘ └─────────────┘ └──────────────┘ │
│       ↕            ↕         ↕                          │
│  ┌────────┐  ┌─────────┐ ┌───────┐                     │
│  │  Auth  │  │ WS Hub  │ │SQLite │                     │
│  │ (JWT)  │  │(브로드캐스트)│ │(WAL)  │                     │
│  └────────┘  └─────────┘ └───────┘                     │
└─────────────────────────────────────────────────────────┘
```

> **세션 엔진(SessionEngine):** 핸들러·WS Hub·에이전트 서비스는 tmux/PTY를 직접
> 호출하지 않고 `SessionEngine` 인터페이스만 사용합니다. 현재 구현은 tmux 기반
> `TmuxSessionEngine`이며, 이후 in-process PTY(ConPTY 포함) 또는 별도 `pcd-sessiond`
> 데몬으로 교체 가능합니다. 핵심 불변식은 **"Detach ≠ Kill"** — 브라우저가 나가도
> 에이전트 프로세스는 죽지 않습니다. 자세한 내용: [docs/session-engine.md](docs/session-engine.md).

---

## 프로젝트 구조

```
power-code-deck/                # 저장소 디렉터리
├── Makefile                    # build, dev, clean, setup
├── pcd                         # 컴파일된 바이너리 (구 agentdeck)
├── server/                     # Go 백엔드
│   ├── main.go                 # 엔트리포인트, 라우터, 서버 기동, 배너
│   ├── go.mod / go.sum
│   ├── static/                 # 빌드 시 client/dist 복사됨 (embed)
│   ├── version/
│   │   └── version.go          # 제품명/버전 상수 (PowerCodeDeck v0.2.0)
│   ├── config/
│   │   └── config.go           # 이중 prefix env 로드, 최초 실행 인증 마법사(none/pin/password)
│   ├── auth/
│   │   ├── auth.go             # JWT 발급/검증, VerifyCredential(pin/password)
│   │   ├── password.go         # stdlib salted-iterated SHA-256 해시
│   │   ├── middleware.go       # Bearer 토큰 미들웨어 (인증 비활성 시 통과)
│   │   └── ws_auth.go          # WebSocket 쿼리파라미터 토큰 검증
│   ├── db/
│   │   ├── sqlite.go           # SQLite 초기화 (WAL, 커넥션 풀)
│   │   └── migrations.go       # 스키마: agents, recent_projects, logs, notifications
│   ├── middleware/
│   │   ├── cors.go             # CORS
│   │   ├── helmet.go           # 보안 헤더 (CSP 포함)
│   │   └── ratelimit.go        # 요율 제한
│   ├── handlers/
│   │   ├── agents.go           # 에이전트 CRUD + 슬래시커맨드
│   │   ├── files.go            # 파일 트리/읽기/쓰기/삭제/이름변경
│   │   ├── projects.go         # 프로젝트 탐색/생성/삭제
│   │   ├── logs.go             # 로그 검색 (FTS5)
│   │   ├── auth_handler.go     # 로그인/토큰 갱신
│   │   ├── meta.go             # 에이전트 메타(git/포트), send, status/progress/log
│   │   ├── notification.go     # 알림 목록/읽음처리
│   │   ├── proxy.go            # 외부 URL 리버스 프록시 (X-Frame-Options 우회, iPad Safari bypass JS 주입)
│   │   └── helpers.go          # jsonResponse, jsonError 유틸
│   ├── services/
│   │   ├── agent.go            # 에이전트 비즈니스 로직, 색상 배정, SendKeys (SessionEngine 경유)
│   │   ├── session_engine.go        # SessionEngine 인터페이스 + 타입 (세션 조작의 유일한 경계)
│   │   ├── session_engine_tmux.go   # TmuxSessionEngine — tmux/PTY 래핑, viewer 추적, Detach≠Kill
│   │   ├── tmux.go             # tmux 세션 생성/종료/키전송, alternate-screen 비활성화 (smcup@:rmcup@)
│   │   ├── pty.go              # PTY 연결, 읽기/쓰기/리사이즈 (엔진 내부에서 사용)
│   │   ├── file.go             # 파일시스템 조작, 경로 보안검증
│   │   ├── project.go          # 프로젝트 탐색, 최근 프로젝트 DB
│   │   ├── watcher.go          # fsnotify 파일감시 → WS 브로드캐스트
│   │   ├── git.go              # git branch/dirty/ahead 폴링 (10초)
│   │   ├── port_scanner.go     # TCP 리스닝 포트 감지 (lsof/ss)
│   │   └── notification.go     # 알림 CRUD (SQLite)
│   ├── ws/
│   │   ├── hub.go              # WS 허브, 메타 폴링 루프, 브로드캐스트
│   │   ├── client.go           # 커넥션별 read/write 펌프, ping/pong
│   │   └── message.go          # 이벤트 상수 + 페이로드 구조체
│   └── cli/
│       ├── root.go             # 서브커맨드 라우터, 헬프, 플래그 파싱
│       ├── agents.go           # list/create/delete/send/status/ping/open
│       └── auth.go             # login, 토큰 저장 (OS별 경로, 0600)
│
├── client/                     # React 프론트엔드
│   ├── package.json
│   ├── vite.config.ts          # 포트 5173, 프록시 → localhost:33033
│   ├── tailwind.config.js      # deck-* 커스텀 컬러, 커스텀 애니메이션
│   ├── tsconfig.json
│   └── src/
│       ├── main.tsx            # React 18 렌더, SW 등록, CSS import, Eruda (?debug)
│       ├── App.tsx             # BrowserRouter, AuthGuard, CommandPalette, 모바일 키보드 대응
│       │
│       ├── pages/
│       │   ├── LoginPage.tsx       # PIN 입력 → JWT 토큰
│       │   ├── ProjectSelectPage.tsx # 프로젝트 선택/검색/생성 (에이전트 있으면 대시보드로 리다이렉트, ?new=1로 우회)
│       │   ├── AgentLauncherPage.tsx # 프리셋 선택 → 에이전트 생성
│       │   ├── DashboardPage.tsx    # 에이전트 그리드/리스트, 프로젝트 추가 버튼
│       │   ├── TerminalPage.tsx     # 3패널 레이아웃 (파일/터미널/서브에이전트+브라우저), 패널 줌, 모바일 바텀시트
│       │   ├── LogsPage.tsx         # 로그 검색
│       │   └── SettingsPage.tsx     # 캐릭터 테마, 사운드
│       │
│       ├── components/
│       │   ├── CommandPalette.tsx       # Cmd+K 글로벌 커맨드 팔레트 (퍼지 검색)
│       │   ├── icons.tsx               # SVG 아이콘 + 파일확장자 아이콘맵
│       │   ├── agent/
│       │   │   ├── AgentCard.tsx        # 데스크톱: 카드 + 미니터미널 + 알림링 + 메타
│       │   │   ├── AgentGrid.tsx        # 반응형 그리드
│       │   │   ├── AgentList.tsx        # 모바일: 리스트 + 알림링 + 메타(compact)
│       │   │   ├── AgentLauncher.tsx    # 프리셋 선택 UI
│       │   │   └── CreateAgentSheet.tsx # 에이전트 생성 바텀시트
│       │   ├── terminal/
│       │   │   ├── TerminalView.tsx     # xterm.js 래퍼(항상 interactive), safeFit 패턴 (ResizeObserver + pageshow + visualViewport + fonts.ready), 포커스 가드, 터치 한글 직접입력 감지
│       │   │   ├── PromptBar.tsx        # 한글/긴 프롬프트 입력바 (IME 조합 처리, Send=pasteSubmit / Paste=pasteOnly / Clear / 터미널 조작) — 데스크톱 선택·모바일/iPad 필수
│       │   │   ├── TerminalKeyBar.tsx    # PTY 제어키 바 (방향키·Enter·Esc·Tab·⇧Tab·y/n·Ctrl+C/D) — 데스크톱·모바일 공용
│       │   │   └── MobileToolbar.tsx    # 모바일 하단 툴바 (Prompt 입력 버튼 + TerminalKeyBar 제어키)
│       │   ├── file/
│       │   │   ├── FileExplorer.tsx     # 트리뷰 + 검색 + 컨텍스트메뉴 (depth 10)
│       │   │   ├── FilePreview.tsx      # 파일 미리보기 (마크다운: Raw/Preview 토글)
│       │   │   ├── FileEditor.tsx       # 파일 편집기
│       │   │   ├── MarkdownPreview.tsx  # react-markdown + remark-gfm 렌더러
│       │   │   └── FileBottomSheet.tsx  # 모바일 파일 피커
│       │   ├── animation/
│       │   │   ├── SubAgentPanel.tsx    # 우측패널: 오비탈 + 타임라인 + 설정
│       │   │   ├── SubAgentBar.tsx      # 수평 픽셀스프라이트 바
│       │   │   ├── SubAgentOrbital.tsx  # 궤도 애니메이션
│       │   │   ├── SubAgentTimeline.tsx # 타임라인 뷰
│       │   │   ├── SubAgentParticles.tsx# 파티클 이펙트
│       │   │   ├── SubAgentIcon.tsx     # 개별 스프라이트 아이콘
│       │   │   ├── PixelSprite.tsx      # 도트 캐릭터 렌더러
│       │   │   └── sprites/
│       │   │       ├── presets.ts        # Default 테마 스프라이트
│       │   │       ├── catPresets.ts     # Cat 테마 스프라이트
│       │   │       └── types.ts          # 스프라이트 타입 정의
│       │   ├── browser/
│       │   │   └── BrowserPanel.tsx     # iframe 브라우저: localhost 직접연결 + 외부 URL 프록시, iPad Safari bypass, 포트 자동감지
│       │   ├── notification/
│       │   │   ├── NotificationRing.tsx # 알림 링 애니메이션 래퍼
│       │   │   └── NotificationBadge.tsx# 읽지않은 알림 카운트 뱃지
│       │   ├── sidebar/
│       │   │   └── AgentMeta.tsx        # git브랜치/포트/상태/진행률 표시
│       │   ├── layout/
│       │   │   ├── Sidebar.tsx          # 데스크톱 네비게이션 + 알림뱃지
│       │   │   ├── BottomNav.tsx        # 모바일 하단 네비 + 에이전트추가 버튼
│       │   │   ├── StatusBadge.tsx      # running/stopped 상태 표시
│       │   │   └── DeleteConfirmDialog.tsx
│       │   ├── auth/
│       │   │   └── PinInput.tsx         # 6자리 PIN 입력
│       │   ├── project/
│       │   │   ├── ProjectSelector.tsx  # 최근 프로젝트 + 디렉토리 브라우저
│       │   │   └── CreateProjectSheet.tsx
│       │   └── settings/
│       │       ├── CharacterThemeSettings.tsx
│       │       └── SoundSettings.tsx
│       │
│       ├── hooks/
│       │   ├── useWebSocket.ts         # WS 싱글턴 연결 + 이벤트 구독 (meta/notification 포함)
│       │   ├── useAgents.ts            # 에이전트 CRUD
│       │   ├── useAuth.ts              # 로그인/로그아웃
│       │   ├── useDevice.ts            # 디바이스 감지 (mobile/tablet/desktop/isTouchDevice)
│       │   ├── useFileExplorer.ts      # 파일트리 + 열기/저장/삭제 + 파일감시
│       │   ├── useSubAgents.ts         # 터미널 출력에서 서브에이전트 파싱
│       │   ├── useSubAgentSound.ts     # 서브에이전트 사운드 이펙트
│       │   ├── useAgentNotification.ts # 터미널 출력 → 알림 감지 + 브라우저 Notification
│       │   ├── useProjects.ts          # 프로젝트 CRUD
│       │   ├── useProjectLauncher.ts   # 프로젝트 선택 플로우
│       │   ├── useSwipe.ts             # 모바일 스와이프 제스처
│       │   └── useTerminal.ts          # (deprecated)
│       │
│       ├── stores/
│       │   └── appStore.ts             # Zustand: agents, notifications, meta, zoom, commandPalette
│       │
│       ├── lib/
│       │   ├── ws.ts                   # AgentDeckWS 싱글턴 (자동 재연결 3초, 중복연결 방지)
│       │   ├── api.ts                  # REST 클라이언트 (401 자동 갱신, 35개 메서드)
│       │   ├── paletteGenerator.ts     # HSL→RGB 에이전트 컬러
│       │   ├── soundManager.ts         # Web Audio API
│       │   └── subAgentSounds.ts       # 사운드 정의
│       │
│       └── styles/
│           ├── scroll.css              # 스크롤바 스타일, xterm pointer-events/touch-action (세분화)
│           ├── globals.css             # 기본 스타일, body/#root 높이(100dvh), 컴포넌트 클래스
│           ├── animations.css          # 키프레임: orbital, particle, glow, slide
│           └── notifications.css       # 알림 링 펄스 애니메이션 (4종)
```

---

## DB 스키마

```sql
agents (id, preset, name, tmux_session, working_dir, command, args,
        status, color_hue, color_name, created_at, updated_at)

recent_projects (id, path, name, last_opened_at, last_agent_preset, open_count)

logs (id, agent_id FK, data, created_at)
logs_fts (FTS5 가상테이블 — 전문검색)

notifications (id, agent_id FK CASCADE, reason, message, read, created_at)
  ↳ idx_notifications_unread (agent_id, read)
```

---

## REST API (36개 엔드포인트)

| Method | Path | 설명 |
|--------|------|------|
| GET | `/api/auth/health` · `/api/health` | 헬스체크 + 인증설정 (appName/version/authEnabled/authMethod, 인증 불필요) |
| POST | `/api/auth/login` | PIN/비밀번호 로그인 → JWT |
| POST | `/api/auth/refresh` | 토큰 갱신 |
| GET | `/api/agents` | 에이전트 목록 |
| POST | `/api/agents` | 에이전트 생성 |
| GET | `/api/agents/{id}` | 에이전트 조회 |
| DELETE | `/api/agents/{id}` | 에이전트 삭제 |
| POST | `/api/agents/{id}/restart` | 재시작 |
| GET | `/api/agents/slash-commands` | 슬래시 커맨드 목록 |
| POST | `/api/agents/{id}/send` | 에이전트에 텍스트 전송 |
| GET | `/api/agents/{id}/meta` | git/포트/알림 메타데이터 |
| POST | `/api/agents/{id}/meta/status` | 커스텀 상태 설정 |
| POST | `/api/agents/{id}/meta/progress` | 진행률 설정 |
| POST | `/api/agents/{id}/meta/log` | 로그 추가 |
| POST | `/api/agents/{id}/notifications/read` | 알림 읽음처리 |
| GET | `/api/files/tree` | 파일 트리 (기본 depth 10) |
| GET | `/api/files/read` | 파일 읽기 |
| PUT | `/api/files/write` | 파일 쓰기 |
| POST | `/api/files/mkdir` | 디렉토리 생성 |
| DELETE | `/api/files/delete` | 파일 삭제 |
| PATCH | `/api/files/rename` | 파일 이름변경 |
| GET | `/api/files/stat` | 파일 메타 |
| GET | `/api/projects/recent` | 최근 프로젝트 |
| DELETE | `/api/projects/recent/{id}` | 최근 프로젝트 삭제 |
| GET | `/api/projects/browse` | 디렉토리 탐색 |
| GET | `/api/projects/detect` | 프로젝트 감지 |
| GET | `/api/projects/search` | 프로젝트 검색 |
| POST | `/api/projects/create` | 프로젝트 생성 |
| DELETE | `/api/projects/delete` | 프로젝트 삭제 |
| PATCH | `/api/projects/rename` | 프로젝트 이름변경 |
| GET | `/api/logs` | 로그 검색 (FTS) |
| GET | `/api/logs/{agentId}` | 에이전트별 로그 |
| GET | `/api/notifications` | 읽지 않은 알림 |
| POST | `/api/notifications/clear` | 알림 전체 읽음 |
| GET | `/api/proxy?url=` | 외부 URL 프록시 (X-Frame-Options 제거, iPad Safari JS 주입) |
| WS | `/ws?token=` | WebSocket 연결 |

---

## WebSocket 이벤트

| 방향 | 이벤트 | 페이로드 |
|------|--------|---------|
| C→S | `terminal:attach` | agentId, cols, rows |
| C→S | `terminal:detach` | agentId |
| C→S | `terminal:input` | agentId, data (xterm 직접 입력) |
| C→S | `terminal:pasteSubmit` | agentId, text, mode (서버가 bracketed paste + Enter로 변환) |
| C→S | `terminal:pasteOnly` | agentId, text, mode (bracketed paste, Enter 없음) |
| C→S | `terminal:resize` | agentId, cols, rows |
| C→S | `file:watch` | agentId, path |
| C→S | `file:unwatch` | agentId |
| S→C | `terminal:output` | agentId, data |
| S→C | `agent:list` | agents[] |
| S→C | `agent:created` | agent |
| S→C | `agent:destroyed` | agentId |
| S→C | `agent:status` | agentId, status |
| S→C | `file:changed` | path, event |
| S→C | `agent:meta` | agentId, gitBranch, gitDirty, gitAhead, listeningPorts |
| S→C | `agent:meta:status` | agentId, key, text, color |
| S→C | `agent:meta:progress` | agentId, value, label |
| S→C | `agent:meta:log` | agentId, level, message, timestamp |
| S→C | `agent:notification` | agentId, reason, message, timestamp |
| S→C | `agent:notification:clear` | agentId |

---

## 사용자 플로우

```
로그인 → 프로젝트 선택 → 프리셋 선택 → 에이전트 생성 → 터미널 뷰
                ↑                                          ↓
           프로젝트 추가 ← 대시보드 ← ── ── ── ── ── (뒤로가기)
           (?new=1)         ↕
                        에이전트 카드
                      (알림링 + 메타 + 미니터미널)
```

## 데이터 흐름

```
[tmux 세션] → PTY → WS Hub → terminal:output → [xterm.js]
     ↑                                              ↓
 SendKeys ← terminal:input          ← [xterm 직접 입력 / TerminalKeyBar 제어키]
         ← terminal:pasteSubmit/Only ← [PromptBar] (서버가 bracketed paste 래핑)

[fsnotify] → WatcherService → file:changed → [FileExplorer 갱신]

[10초 타이머] → GitService.Poll → agent:meta → [AgentMeta UI]
              → PortScanner.Poll ↗

[터미널 출력] → useAgentNotification(클라이언트 파싱)
             → addNotification → NotificationRing 애니메이션
             → Browser Notification (탭 비활성 시)

[내장 브라우저]
  localhost → iframe 직접연결 (포트 자동감지)
  외부 URL  → Go 프록시 → HTML fetch → iPad bypass JS/CSS 주입 → srcdoc iframe
```

---

## 터미널 초기화 (safeFit 패턴)

xterm.js 초기화 시 iPad Safari에서 "새로고침 후 프리징" 문제를 해결하기 위한 multi-source refit:

```
1. terminal.open(container)
2. safeFit() — double-RAF + 크기 0 체크 + 이전 크기와 비교
3. 4개 이벤트 소스에서 반복 호출:
   - ResizeObserver (컨테이너 크기 변화)
   - pageshow (초기 로드 + bfcache 복원)
   - visualViewport.resize (키보드/주소창, 100ms debounce)
   - document.fonts.ready (웹폰트 로딩 완료)
4. pointerdown → terminal.focus() (터치/클릭 시 포커스 재획득)
5. 상태 오버레이에 pointer-events: none (xterm 이벤트 차단 방지)
```

---

## tmux 설정

```go
// 세션 생성 시 자동 적용:
tmux set-option -t {session} terminal-overrides "xterm*:smcup@:rmcup@"  // alternate screen 비활성화 (tmux 3.x)
tmux set-option -t {session} mouse off                                   // 마우스 이벤트를 xterm.js로 전달
```

---

## 내장 브라우저 프록시

```
GET /api/proxy?url=https://example.com

동작:
1. Go 서버가 외부 URL을 fetch
2. X-Frame-Options, CSP, COEP, COOP 헤더 제거
3. HTML이면:
   - <base href> 주입 (상대 경로 해결)
   - iPad Safari Link Preview bypass JS 주입:
     * CSS: -webkit-touch-callout:none, touch-action:manipulation, -webkit-user-drag:none
     * JS: touchend에서 이동거리 < 10px → closest('a') → location.href 직접 변경
     * JS: click capture에서도 동일 처리 (fallback)
   - 상대 URL을 절대 URL로 변환
   - JSON { html, statusCode, url }로 응답 → srcdoc iframe
4. 비-HTML이면: 헤더 정리 후 pass-through

보안: GET만 허용, 10MB 제한, 15초 타임아웃, JWT 인증 필수
```

---

## CLI 커맨드

```bash
pcd                     # 서버 시작 (기본)
pcd list                # 에이전트 목록
pcd create --preset claude-code --dir ~/code/project
pcd delete <id>
pcd send <id> "텍스트"   # 에이전트에 입력 전송
pcd send <id> --ctrl-c  # Ctrl+C 전송
pcd status [id]         # 상태 조회 (메타 포함)
pcd login               # PIN 인증 → 토큰 저장
pcd open                # 브라우저 열기
pcd ping                # 서버 상태 확인
pcd version             # pcd v0.2.0
pcd help
```

---

## 빌드

```bash
make setup          # pnpm install + go mod download
make dev            # Go 서버(33033) + Vite 데브서버(5173) 동시 실행
make build          # pnpm build → dist/ → server/static/ → go build → ./pcd
make build-client   # React 빌드만
make build-server   # Go 빌드만 (static/ 임베드)
```

단일 바이너리: `go:embed all:static` 으로 프론트엔드 정적파일을 바이너리에 포함.

---

## 의존성

**Go:** gorilla/mux, gorilla/websocket, creack/pty, fsnotify, go-sqlite3, golang-jwt, godotenv

**Node:** react 18, react-router-dom 6, zustand 4, @xterm/xterm 5, @xterm/addon-fit, react-markdown, remark-gfm, tailwindcss 3, vite 6, typescript 5, eruda (dev)

---

## 스크롤/터치 아키텍처

```css
/* 루트 높이 체인: CSS만, JS 의존 없음 */
body, #root { height: 100vh; height: 100dvh; overflow: hidden; }
/* 모바일 키보드: visualViewport.height < 85%일 때만 #root.style.height override */

/* 페이지: h-full (부모 100% 상속) + overflow-hidden */
/* 스크롤 영역: flex-1 + overflow-y-auto + min-h-0 */
/* 터미널: flex-1 + min-h-0 (absolute inset-0 사용 안 함) */
```

```css
/* 터미널 touch-action (세분화) */
.xterm-viewport { touch-action: auto; overflow-y: auto !important; }
.xterm-screen   { touch-action: pan-y; }
.terminal-shell { touch-action: pan-y pinch-zoom; overscroll-behavior-y: contain; }
```

```
xterm 옵션: scrollback 3000, scrollSensitivity 1.1~1.25, smoothScrollDuration 90~245ms
```

---

## 핵심 설계 패턴

- **tmux + PTY**: 에이전트별 분리된 쉘 세션, 웹에서 실시간 I/O
- **WS Hub**: 싱글턴 허브가 모든 클라이언트에 이벤트 브로드캐스트
- **safeFit 패턴**: 4개 이벤트 소스에서 xterm fit/resize 재실행 (iPad Safari 대응)
- **클라이언트 알림 감지**: 서버 부하 없이 터미널 출력 패턴 매칭
- **서버 메타 폴링**: git/포트 정보는 서버에서 수집 후 WS 푸시
- **프록시 + iPad bypass**: 외부 URL iframe 로딩 + touchend JS 주입
- **반응형**: useDevice 훅으로 mobile/tablet/desktop 분기
- **색상 자동배정**: HSL 거리 최대화로 에이전트별 고유 컬러
- **디버그**: `?debug` 쿼리로 Eruda 모바일 콘솔 활성화
