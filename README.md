# PowerCodeDeck

**PowerCodeDeck v0.2.0** — 브라우저에서 서버 프로젝트를 열고, 터미널과 AI 코딩 에이전트를 실행하는 개인용 웹 콘솔입니다.
*PowerCodeDeck is a self-hosted web console for project terminals and AI coding agents.*

Claude Code, Gemini CLI, Codex CLI 등 AI 코딩 에이전트를 한 화면에서 실행하고 모니터링합니다.
Go 단일 바이너리(`pcd`)로 빌드되어 설치가 간편합니다.

> **v0.1.0 (AgentDeck) → v0.2.0 (PowerCodeDeck) 리브랜딩 버전입니다.** 변경 내역은 [CHANGELOG.md](CHANGELOG.md), 다음 로드맵은 [아래 Roadmap](#roadmap) 참고.

![Version](https://img.shields.io/badge/version-0.2.0-6366f1)
![Go](https://img.shields.io/badge/Go-1.23+-00ADD8?logo=go&logoColor=white)
![React](https://img.shields.io/badge/React-18-61DAFB?logo=react&logoColor=black)
![SQLite](https://img.shields.io/badge/SQLite-embedded-003B57?logo=sqlite&logoColor=white)
[![License: AGPL v3](https://img.shields.io/badge/License-AGPL_v3-blue.svg)](https://www.gnu.org/licenses/agpl-3.0)

> 이 README 하나만 읽으면 PowerCodeDeck의 기능·사용법·아키텍처 전반을 파악할 수 있습니다.
> 코드 레벨의 상세 설계는 [ARCHITECTURE.md](ARCHITECTURE.md)를 참고하세요.

---

## 목차

- [주요 기능](#주요-기능)
- [기능 상태 (Stable / Experimental / Roadmap)](#기능-상태-stable--experimental--roadmap)
- [한눈에 보는 구조](#한눈에-보는-구조)
- [사전 요구사항](#사전-요구사항)
- [설치](#설치)
- [실행](#실행)
- [인증](#인증)
- [보안 주의](#보안-주의)
- [사용법](#사용법)
- [Session Handoff](#session-handoff)
- [설정](#설정)
- [CLI 커맨드](#cli-커맨드)
- [기술 스택](#기술-스택)
- [프로젝트 구조](#프로젝트-구조)
- [아키텍처](#아키텍처)
- [Roadmap](#roadmap)
- [라이선스](#라이선스)

---

## 주요 기능

- **멀티 에이전트** — 여러 AI 에이전트를 동시에 실행/모니터링 (Claude Code / Gemini CLI / Codex CLI / Custom)
- **웹 터미널** — xterm.js 기반 단일 Interactive Terminal + 디바이스별 Prompt Bar(한글/긴 프롬프트), 방향키 툴바, tmux 세션으로 프로세스 유지
- **대시보드** — 그리드/리스트 뷰로 전체 에이전트 상태를 한눈에, `+`로 즉시 생성
- **에이전트 메타** — Git 브랜치·변경 여부·ahead 커밋 수, 리스닝 포트 자동 감지 표시
- **파일 탐색기** — 프로젝트 파일 탐색/편집/생성/삭제/이름변경
- **내장 브라우저** — 에이전트가 띄운 로컬 포트를 iframe으로 미리보기 (외부 URL은 프록시 경유)
- **알림 센터** — 에이전트의 완료/대기/승인요청 등 이벤트를 수집·표시
- **로그 뷰어** — 에이전트별 출력 로그를 SQLite에 저장하고 검색
- **도트 캐릭터** — 에이전트 활동을 픽셀 애니메이션으로 시각화 (Default/Cat 테마)
- **슬래시 자동완성** — `~/.claude/commands`, `agents`, `skills` 자동 감지
- **사운드** — 레트로 게임 스타일 효과음 (도구별 고유 사운드)
- **선택형 인증** — 최초 실행 시 none/PIN/password 중 선택 (기본값: 인증 없음)
- **CLI** — 서버 실행 없이 터미널에서 에이전트 조작 (`pcd list/create/send` 등)
- **원클릭 실행** — 바이너리 실행 → 브라우저 자동 열기
- **단일 바이너리** — Go 바이너리(`pcd`)에 프론트엔드가 임베드, 외부 의존성 없음

---

## 기능 상태 (Stable / Experimental / Roadmap)

### Stable in v0.2
- Interactive Terminal (단일 터미널)
- Prompt Bar (한글/긴 프롬프트 — 모바일·iPad 필수, 데스크톱 선택)
- 방향키/제어키 툴바 (데스크톱·모바일)
- Project Sessions (프로젝트별 세션)
- File Explorer
- Claude / Gemini / Codex Launcher
- Optional Authentication (none / PIN / password)
- Local / Proxy-friendly deployment

### Experimental
아래 기능은 동작하지만 v0.2에서 크게 다듬지 않았고, 이후 버전에서 변경될 수 있습니다.
- Multi-agent dashboard (멀티 에이전트 대시보드)
- Browser preview (내장 브라우저)
- Notification center (알림 센터)
- Pixel character (도트 캐릭터)
- Sound effects (효과음)
- CLI subcommands

### Roadmap
- **v0.3.0 — Control Room**: 여러 에이전트 세션을 한눈에 관리하는 관제실. → [Roadmap 섹션](#roadmap)

---

## 한눈에 보는 구조

```
┌────────────────────────────────────────────────────────────┐
│  Browser (React SPA, 단일 바이너리에 임베드됨)              │
│  Dashboard · Terminal · Files · Browser · Logs · Settings  │
└───────────────┬────────────────────────────────────────────┘
                │  REST (HTTP/JWT)  +  WebSocket (터미널 스트림)
                ▼
┌────────────────────────────────────────────────────────────┐
│  Go Server (pcd 바이너리)                                  │
│  Router → Handlers → Services → SQLite                     │
│  WebSocket Hub → PTY → tmux                                │
└───────────────┬────────────────────────────────────────────┘
                │  PTY (pseudo-terminal)
                ▼
┌────────────────────────────────────────────────────────────┐
│  tmux 세션: Claude Code / Gemini CLI / Codex CLI / Custom  │
└────────────────────────────────────────────────────────────┘
```

핵심 아이디어: **각 AI CLI 에이전트는 독립된 tmux 세션에서 실행**되고, Go 서버가 PTY로 그 세션의 입출력을 중계합니다. 브라우저의 xterm.js는 WebSocket으로 이 스트림을 실시간 표시합니다. tmux 덕분에 브라우저를 닫아도 에이전트는 계속 실행됩니다.

---

## 사전 요구사항

PowerCodeDeck은 AI CLI 도구의 **런처**입니다. 사용하려는 CLI가 미리 설치되어 있어야 합니다.

| CLI | 설치 명령 |
|-----|----------|
| [Claude Code](https://docs.anthropic.com/ko/docs/claude-code) | `npm install -g @anthropic-ai/claude-code` |
| [Gemini CLI](https://github.com/google-gemini/gemini-cli) | `npm install -g @google/gemini-cli` |
| [OpenAI Codex CLI](https://github.com/openai/codex) | `npm install -g @openai/codex` |

> PowerCodeDeck 자체는 CLI를 설치하지 않습니다. 원하는 CLI를 먼저 설치한 뒤 실행하세요.
> `Custom` 프리셋을 쓰면 위 목록 외 임의의 커맨드도 실행할 수 있습니다.

---

## 설치

### 원클릭 설치 (권장)

**macOS / Linux:**
```bash
git clone https://github.com/LeeSiWal/power-code-deck.git
cd power-code-deck
bash install.sh
```

> `bash install.sh` 는 실행 권한이 없어도 동작합니다. `./install.sh` 로 실행했을 때 `permission denied` 가 뜨면 `chmod +x install.sh` 후 다시 시도하세요.

**Windows:** 아래 [Windows 설치 (비개발자용 단계별 가이드)](#windows-설치-비개발자용-단계별-가이드)를 따라 하세요.

설치 스크립트가 자동으로 처리하는 것:
- Homebrew (macOS) / WSL (Windows) 설치
- tmux, Go, Node.js, pnpm 설치
- 프로젝트 빌드
- `~/.powercodedeck/`에 바이너리 설치
- 바탕화면 바로가기 생성 (macOS: `.command` + `.app`, Windows: `.bat`)

### Windows 설치 (비개발자용 단계별 가이드)

> PowerCodeDeck 는 내부적으로 **WSL(Windows에 내장된 리눅스)** 위에서 동작합니다.
> 개발 경험이 없어도 아래 순서를 그대로 따라 하면 됩니다. **한 번만** 준비해 두면 다음부터는 실행만 하면 됩니다.

#### 1단계 · WSL(우분투) 준비 — 최초 1회만

1. 시작 메뉴에서 **PowerShell** 을 찾아 **마우스 오른쪽 클릭 → "관리자 권한으로 실행"**.
2. 아래를 입력하고 Enter:
   ```powershell
   wsl --install
   ```
3. 설치가 끝나면 **컴퓨터를 다시 시작(재부팅)** 합니다. ← 이 단계를 건너뛰면 다음이 진행되지 않습니다.
4. 재부팅 후 검은 창(Ubuntu)이 자동으로 열립니다. (안 열리면 시작 메뉴에서 **Ubuntu** 실행)
   여기서 사용할 **사용자 이름(username)** 과 **비밀번호(password)** 를 만듭니다.
   - 비밀번호 입력 시 화면에 아무것도 안 보이는 게 정상입니다. 그대로 입력하고 Enter 하세요.
5. `이름@컴퓨터:~$` 같은 표시가 나오면 준비 완료. 이 창은 닫아도 됩니다.

> 이미 WSL/Ubuntu 를 쓰고 있다면 1단계는 건너뛰어도 됩니다.
> 확인하려면 PowerShell에서 `wsl -l -v` 를 입력해 **Ubuntu** 가 보이는지 확인하세요.

#### 2단계 · 작업 폴더 만들고 프로젝트 내려받기

> ⚠️ `C:\Windows\System32` 같은 시스템 폴더에서 하면 **권한 오류(Permission denied)** 가 납니다.
> 반드시 **내 사용자 폴더(`C:\Users\내이름`) 아래** 에서 하세요.

**방법 A — ZIP 다운로드 (Git 설치 없이, 가장 쉬움)**

1. 파일 탐색기(폴더 아이콘)를 열고, 위쪽 **주소창에 `%USERPROFILE%`** 를 입력하고 Enter → 내 사용자 폴더(`C:\Users\내이름`)가 열립니다.
2. 그 안에 **새 폴더**를 하나 만듭니다. 예: `PowerCodeDeck`.
3. 브라우저에서 <https://github.com/LeeSiWal/power-code-deck> 접속 → 초록색 **`< > Code`** 버튼 → **Download ZIP**.
4. 내려받은 ZIP 파일을 방금 만든 `PowerCodeDeck` 폴더 안에 **압축 해제**합니다.
   폴더 안에 `install.bat` 파일이 보이면 성공입니다.

**방법 B — 명령어로 (Git for Windows 가 설치돼 있는 경우)**

```powershell
cd $HOME                # 내 사용자 폴더(C:\Users\내이름)로 이동
mkdir PowerCodeDeck -Force
cd PowerCodeDeck
git clone https://github.com/LeeSiWal/power-code-deck.git
cd power-code-deck
```

#### 3단계 · 설치 실행

- **가장 쉬운 방법:** 압축을 푼 폴더 안의 **`install.bat` 파일을 더블클릭** 합니다.
- 또는 PowerShell에서 그 폴더로 이동한 뒤:
  ```powershell
  .\install.bat
  ```
  PowerShell 에서는 반드시 앞에 **`.\`** 를 붙여야 합니다. (`install.bat` 만 입력하면 "인식되지 않습니다" 오류)

설치 도중 **우분투 비밀번호(1단계에서 만든 것)** 를 물어보면 입력하세요.
그 다음 tmux · Go · Node.js · pnpm 설치 → 빌드 → `~/.powercodedeck/` 설치까지 자동으로 진행됩니다. (몇 분 걸립니다.)

#### 4단계 · 실행 & 접속

설치가 끝나면 다음 명령으로 실행합니다(바탕화면 바로가기가 생겼다면 그걸 눌러도 됩니다):

```powershell
wsl -d Ubuntu -- bash -lc "cd ~/.powercodedeck && ./pcd"
```

그다음 웹 브라우저에서 **<http://localhost:33033>** 로 접속하면 PowerCodeDeck 이 열립니다. 🎉

<details>
<summary>고급: 2~4단계를 명령어 한 줄로 (Git for Windows 불필요)</summary>

WSL/Ubuntu 준비(1단계)가 끝난 상태라면, PowerShell에서 아래 한 줄이면 내려받기·설치까지 끝납니다:

```powershell
wsl -d Ubuntu -- bash -lc "sudo apt-get update && sudo apt-get install -y git && cd ~ && git clone https://github.com/LeeSiWal/power-code-deck.git && cd power-code-deck && bash install.sh"
```

</details>

#### 잘 안 될 때

| 증상 | 해결 |
|------|------|
| `install.bat : 인식되지 않습니다` | PowerShell에서는 `.\install.bat` 처럼 앞에 `.\` 를 붙이세요. |
| `could not create work tree dir ... Permission denied` | `C:\Windows\System32` 등 시스템 폴더에서 실행 중입니다. 2단계처럼 내 사용자 폴더에서 다시 하세요. |
| `No Linux distribution found` 가 반복됨 | 1단계 WSL 설치 후 **재부팅** 및 Ubuntu 첫 실행(계정 생성)이 안 끝난 것입니다. `wsl -l -v` 로 Ubuntu가 보이는지 확인하세요. |

### 수동 설치

필요 도구: `go 1.23+`, `pnpm`, `tmux`

```bash
git clone https://github.com/LeeSiWal/power-code-deck.git
cd power-code-deck
make setup    # 의존성 설치 (client pnpm install + go mod download)
make build    # client 빌드 → server/static/ 복사 → go build (→ ./pcd)
./pcd         # 실행
```

주요 Make 타깃: `make setup`, `make build`, `make dev`(개발 서버), `make clean`.

> 저장소는 `power-code-deck`, 바이너리는 `pcd`, 데이터 디렉터리는 `~/.powercodedeck/`입니다.

---

## 실행

### 첫 실행

첫 실행 시 **인증 사용 여부를 선택**합니다. 기본값은 **인증 없음**입니다.
(터미널이 아닌 방식 — PM2, `.app` 더블클릭 등 — 으로 실행되면 자동으로 인증 없음으로 설정됩니다.)

```
  PowerCodeDeck v0.2.0 first run
  ------------------------------------------------
  인증을 사용할까요?  (Choose authentication)
    [1] 사용 안 함 / none   (기본값, default)
    [2] PIN 사용 / pin
    [3] 비밀번호 사용 / password
  선택 (Enter = 1):
```

선택 후 서버가 시작되며 배너가 표시됩니다:

```
  ================================================
     PowerCodeDeck v0.2.0
     AI Coding Terminal Console
  ================================================

     URL  : http://localhost:33033
     Auth : disabled

  Warning:
  PowerCodeDeck authentication is disabled.
  Do not expose this service directly to the public internet.
  Use Caddy + Authelia, Tailscale, VPN, or SSH tunnel.

     Browser will open automatically.
     Press Ctrl+C to stop the server.

  ================================================
```

인증 없음 모드에서는 로그인 페이지 없이 바로 앱으로 진입합니다.

### 이후 실행

- **macOS:** 바탕화면 `PowerCodeDeck.command` 더블클릭 또는 `~/Applications`의 PowerCodeDeck 앱
- **Windows:** 바탕화면 `PowerCodeDeck.bat` 더블클릭
- **터미널:** `~/.powercodedeck/pcd` 또는 `./pcd`

인증 사용 시 한 번 로그인하면 7일간 재로그인 불필요 (JWT). WSL 환경에서는 Windows 기본 브라우저를 자동으로 엽니다.

---

## 인증

PowerCodeDeck의 자체 인증은 **선택 사항**이며 기본값은 사용 안 함입니다.
최초 실행 시 마법사에서 선택하거나, `.env`에서 직접 설정할 수 있습니다.

| 방법 | 동작 |
|------|------|
| **none** (기본) | 로그인 없이 즉시 진입. API·WebSocket 인증 통과. 시작 로그에 `Auth: disabled` + 보안 경고 |
| **pin** | 사용자가 직접 정한 PIN으로 로그인. PIN 값은 로그에 노출되지 않음 |
| **password** | 사용자가 정한 비밀번호로 로그인. 비밀번호는 평문이 아닌 해시로 저장 |

- PIN/비밀번호는 **자동 생성하지 않고 사용자가 직접 정합니다.**
- 인증 사용 시 JWT 서명 키(`*_JWT_SECRET`)가 자동 생성되어 `.env`에 저장됩니다.
- 비밀번호는 stdlib salted-iterated SHA-256 해시로 저장됩니다(외부 의존성 없이). 추후 bcrypt/argon2로 교체 가능합니다.

### 인증 방법 변경

`~/.powercodedeck/.env`를 편집한 뒤 재시작합니다:

```bash
# 인증 없음
POWERCODEDECK_AUTH_ENABLED=false
POWERCODEDECK_AUTH_METHOD=none

# PIN 사용
POWERCODEDECK_AUTH_ENABLED=true
POWERCODEDECK_AUTH_METHOD=pin
POWERCODEDECK_PIN=123456

# 재시작
~/.powercodedeck/pcd
```

> 비밀번호 해시는 UI에서 직접 만들 수 없으므로, password 방식은 최초 실행 마법사에서 설정하는 것을 권장합니다.

---

## 보안 주의

PowerCodeDeck은 **서버 터미널과 파일에 접근할 수 있는 도구**입니다.
인증 없음 모드는 로컬 실행, VPN, Tailscale, SSH 터널, 또는 Caddy + Authelia 같은 외부 인증 뒤에서 사용하는 것을 전제로 합니다.

권장:
- `127.0.0.1`에만 바인딩 (리버스 프록시 뒤에 배치)
- Caddy + Authelia 등 외부 인증 뒤에서 사용
- **공개 인터넷에 직접 노출 금지**
- 작업 가능한 루트 디렉터리 제한 (`POWERCODEDECK_WORKSPACE_ROOT`)

---

## 사용법

### 1. 프로젝트 선택

첫 화면에서 프로젝트를 선택합니다:
- **최근 프로젝트** — 이전에 열었던 프로젝트 바로 열기 (SQLite에 방문 이력 저장)
- **폴더 탐색** — 파일 브라우저로 선택
- **직접 입력** — 경로 직접 입력 (예: `~/code/my-project`)
- **새 프로젝트 만들기** — 폴더 생성

### 2. 에이전트 실행

프로젝트를 선택하면 에이전트 타입(프리셋)을 고릅니다:
- **Claude Code** — Anthropic의 AI 코딩 에이전트
- **Gemini CLI** — Google의 AI CLI
- **Codex CLI** — OpenAI의 AI CLI
- **Custom** — 원하는 커맨드 직접 입력

각 에이전트는 고유 색상(hue)과 도트 캐릭터를 부여받아 대시보드에서 구분됩니다.

### 3. 터미널 사용

PowerCodeDeck은 하나의 **Interactive Terminal**을 기본으로 사용합니다. 별도의 Chat/Raw 모드는 없습니다. 한글·긴 프롬프트는 **Prompt Bar**에서 작성해 현재 터미널로 전송합니다.

**Terminal (직접 입력)**
- 명령어 실행, Claude의 선택지·승인(y/n)·방향키·Tab·Esc·Ctrl+C 등 조작을 담당합니다.
- 하단 **방향키/제어키 툴바**로 `↑ ↓ ← → Enter Esc Tab ⇧Tab y n Ctrl+C Ctrl+D`를 전송할 수 있습니다. 물리 키보드가 없는 모바일에서 대화형 메뉴 조작에 특히 유용합니다.

**Prompt Bar (한글/긴 프롬프트)**
- textarea에서 IME 조합이 정상 동작하므로 **한글이 자모 분리 없이** 입력됩니다. 완성된 문자열만 터미널로 붙여넣습니다.
- **Send** — 붙여넣고 Enter까지 전송 (Cmd/Ctrl+Enter) · **Paste** — 붙여넣기만 (Enter 없음) · **Clear** — 지우기 · **터미널 조작** — 터미널로 포커스 이동
- `Shift+Enter`로 줄바꿈. 한글 조합 중 Enter는 Send로 처리되지 않습니다.
- **데스크톱**: 선택형 — 상단 **Prompt** 버튼(모든 OS) 또는 `⌘K` / `⌘P`(macOS)로 열고, `Esc`로 닫습니다.
- **모바일 / iPad**: 항상 표시(접기만 가능) — xterm에 한글을 직접 입력하면 자모가 분리되므로 Prompt Bar 사용을 기본으로 합니다.

> Terminal은 명령·방향키·승인 조작, Prompt Bar는 한글·긴 문장·멀티라인 — 라는 단순한 역할 분리입니다.

### 4. 패널

- **Files** — 좌측 파일 탐색기 (파일 보기/편집/생성/삭제/이름변경, 실시간 변경 감지)
- **Browser** — 에이전트가 띄운 로컬 포트를 iframe으로 미리보기 (외부 URL은 프록시 경유)
- **Anim** — 우측 애니메이션 패널 (도트 캐릭터 + 캐릭터 테마 설정)
- 패널 크기 드래그로 조절 가능

### 5. 대시보드

여러 에이전트를 동시에 관리:
- 그리드/리스트 뷰
- `+` 버튼으로 에이전트 바로 생성
- 에이전트 상태·Git 브랜치·리스닝 포트 실시간 표시
- 커맨드 팔레트(⌘/Ctrl+K 계열)로 빠른 이동

### 6. 알림 · 로그

- **알림 센터** — 에이전트 완료/대기/승인요청 등 이벤트를 모아 표시, 읽음 처리
- **로그 뷰어** — 에이전트별 출력 로그 검색 (SQLite 저장)

---

## Session Handoff

데스크톱에서 실행 중인 터미널 또는 Claude 코딩 세션을 **모바일 / iPad로 그대로 이어받는** 기능입니다.
QR 코드 한 번 스캔으로, 같은 tmux 세션에 그대로 붙어 이어서 작업할 수 있습니다.

1. 데스크톱에서 터미널 또는 Claude 세션을 엽니다.
2. **Continue on Mobile**(모바일에서 이어하기) 버튼을 클릭합니다.
3. 화면에 표시된 QR 코드를 휴대폰/iPad로 스캔합니다.
4. 모바일 / iPad에서 같은 세션을 이어서 사용합니다 — 한글·긴 프롬프트 입력을 위해 **Prompt Bar가 자동으로 펼쳐집니다.**

### 일회용 토큰 (one-time token)

핸드오프 링크에 담기는 토큰은 **일회용**이며, 다음 규칙으로 보호됩니다:

- **만료** — 기본 10분(`POWERCODEDECK_HANDOFF_TOKEN_TTL_SECONDS=600`) 후 자동 만료됩니다.
- **단일 사용(single-use)** — 한 번 사용(redeem)되면 즉시 무효화됩니다.
- **세션 바인딩** — 발급 시점의 특정 세션에만 연결되어, 다른 에이전트/세션으로는 사용할 수 없습니다.
- **원문 미저장** — 원문 토큰(raw token)은 데이터베이스에 저장되지 않습니다. **SHA-256 해시만** 저장하고 대조합니다.

### Public URL

리버스 프록시나 도메인 뒤에서 운영한다면, QR 코드에 넣을 외부 접근 주소를 지정합니다:

```env
POWERCODEDECK_PUBLIC_URL=https://pcd.example.com
```

### Local Wi-Fi Handoff

같은 Wi-Fi(LAN)에서 휴대폰으로 바로 이어받으려면, **서버가 휴대폰에서 접근 가능해야** 합니다.
기본값(`127.0.0.1`)은 로컬 전용이므로, LAN 핸드오프를 쓰려면 바인드 호스트와 LAN 주소를 지정합니다:

```env
POWERCODEDECK_BIND_HOST=0.0.0.0
POWERCODEDECK_LAN_HANDOFF_ENABLED=true
POWERCODEDECK_LAN_URL=http://192.168.0.25:33033
```

> `POWERCODEDECK_LAN_URL`의 IP는 데스크톱의 실제 LAN IP로 바꿔주세요.

### 보안 경고

핸드오프는 서버를 휴대폰에서 접근 가능하게 만들 수 있으므로, **인증 없이 PowerCodeDeck을 직접 노출하지 마세요.**

- PIN / password 인증을 켜거나, Caddy + Authelia, Tailscale, VPN, SSH 터널 뒤에 배치하세요.
- 특히 **인증이 꺼져 있고(`AUTH_ENABLED=false`) LAN 핸드오프가 켜져 있으면**(`LAN_HANDOFF_ENABLED=true`, `BIND_HOST=0.0.0.0`), 같은 네트워크의 누구나 세션에 접근할 수 있습니다. 신뢰할 수 있는 네트워크에서만 사용하세요.

### 관련 환경변수

| 변수 | 기본값 | 설명 |
|------|--------|------|
| `POWERCODEDECK_PUBLIC_URL` | (빈 값) | QR/핸드오프 링크에 사용할 외부 공개 URL |
| `POWERCODEDECK_HANDOFF_ENABLED` | `true` | Session Handoff 기능 사용 여부 |
| `POWERCODEDECK_HANDOFF_TOKEN_TTL_SECONDS` | `600` | 일회용 토큰 만료 시간(초, 기본 10분) |
| `POWERCODEDECK_LAN_HANDOFF_ENABLED` | `false` | 같은 LAN에서의 핸드오프 허용 여부 |
| `POWERCODEDECK_LAN_URL` | (빈 값) | LAN 핸드오프 시 QR에 사용할 주소 (예: `http://192.168.0.25:33033`) |
| `POWERCODEDECK_BIND_HOST` | `127.0.0.1` | 서버 바인드 호스트. LAN 핸드오프에는 `0.0.0.0` 필요 |

> `POWERCODEDECK_*` prefix를 권장하며, 기존 `AGENTDECK_*` 환경변수도 하위 호환을 위해 계속 지원됩니다.

---

## 설정

### 환경변수 (`.env`)

`POWERCODEDECK_*` prefix를 권장합니다.

| 변수 | 기본값 | 설명 |
|------|--------|------|
| `POWERCODEDECK_AUTH_ENABLED` | `false` | PowerCodeDeck 자체 인증 사용 여부 |
| `POWERCODEDECK_AUTH_METHOD` | `none` | `none`, `pin`, `password` |
| `POWERCODEDECK_PIN` | (빈 값) | PIN 인증 사용 시 PIN |
| `POWERCODEDECK_PASSWORD_HASH` | (빈 값) | password 인증 사용 시 비밀번호 해시 |
| `POWERCODEDECK_JWT_SECRET` | (자동) | 인증 사용 시 JWT 서명 키 |
| `POWERCODEDECK_PORT` | `33033` | 서버 포트 |
| `POWERCODEDECK_DB_PATH` | `./powercodedeck.db` | SQLite 데이터베이스 경로 |
| `POWERCODEDECK_CORS_ORIGINS` | `http://localhost:33033` | CORS 허용 origin |
| `POWERCODEDECK_WORKSPACE_ROOT` | (빈 값) | 프로젝트 탐색 기본 루트 |
| `POWERCODEDECK_BIND_HOST` | `127.0.0.1` | 서버 바인드 호스트. LAN 핸드오프에는 `0.0.0.0` 필요 |
| `POWERCODEDECK_PUBLIC_URL` | (빈 값) | QR/핸드오프 링크에 사용할 외부 공개 URL |
| `POWERCODEDECK_HANDOFF_ENABLED` | `true` | Session Handoff(모바일에서 이어하기) 사용 여부 |
| `POWERCODEDECK_HANDOFF_TOKEN_TTL_SECONDS` | `600` | 일회용 핸드오프 토큰 만료 시간(초) |
| `POWERCODEDECK_LAN_HANDOFF_ENABLED` | `false` | 같은 LAN에서의 핸드오프 허용 여부 |
| `POWERCODEDECK_LAN_URL` | (빈 값) | LAN 핸드오프 시 QR에 사용할 주소 |

> 기존 `AGENTDECK_*` 환경변수도 하위 호환을 위해 계속 지원됩니다.
> 동일한 값이 함께 존재하면 `POWERCODEDECK_*`가 우선됩니다.
>
> **하위 호환 규칙**: `AUTH_ENABLED`/`AUTH_METHOD`가 없고 `AGENTDECK_PIN`만 있어도 기존 PIN 인증으로 동작합니다.

첫 실행 시 선택한 설정이 `.env`에 저장되며, 직접 편집도 가능합니다. 인증 변경은 [인증 섹션](#인증) 참고.

### PM2로 상시 실행

```bash
pm2 start ./pcd --name pcd
pm2 save
pm2 startup  # 재부팅 후 자동 시작
```

`ecosystem.config.js`가 함께 제공됩니다.

---

## CLI 커맨드

서버가 실행 중일 때, 같은 바이너리를 서브커맨드와 함께 호출하면 터미널에서 에이전트를 조작할 수 있습니다.

```bash
pcd                    # (인자 없음) 서버 시작
pcd login              # CLI 인증 (토큰을 OS 설정 폴더에 저장)
pcd list               # 에이전트 목록
pcd create --preset claude-code --dir ~/code/app --name "내 에이전트"  # 에이전트 생성
pcd send <id> "메시지"  # 에이전트에 텍스트 전송
pcd status [id]        # 상태 확인
pcd delete <id>        # 에이전트 삭제
pcd open               # 브라우저 열기
pcd ping               # 서버 상태 확인
pcd version            # 버전 (pcd v0.2.0)
pcd help               # 도움말
```

CLI 토큰 저장 위치: macOS `~/Library/Application Support/powercodedeck/`, Linux `~/.config/powercodedeck/`, Windows `%APPDATA%\powercodedeck\`.

---

## 기술 스택

### 서버 (Go 1.23)
- **Gorilla Mux** — HTTP 라우터
- **Gorilla WebSocket** — 실시간 터미널 스트림
- **SQLite** (mattn/go-sqlite3, WAL 모드) — 에이전트/프로젝트/로그/알림 저장
- **creack/pty** — 터미널 PTY 관리
- **tmux** — 에이전트 세션 관리 (외부 바이너리)
- **fsnotify** — 파일 변경 감지
- **golang-jwt/jwt v5** — 인증
- **joho/godotenv** — `.env` 로드
- 내장 서비스: Git 상태, 포트 스캐너, 알림, 파일 감시

### 클라이언트 (React 18 + TypeScript)
- **Vite 6** — 빌드 도구 (`eruda`로 모바일 디버깅)
- **React Router v6** — 클라이언트 라우팅
- **Zustand** — 상태 관리 (`appStore`)
- **xterm.js** (@xterm/xterm + addon-fit / unicode11 / web-links) — 웹 터미널
- **react-markdown + remark-gfm** — 마크다운 렌더링
- **Tailwind CSS** — 스타일링
- **Web Audio API** — 효과음 (`soundManager`, `subAgentSounds`)

### 빌드 결과물
- Go 바이너리 1개 (프론트엔드 `embed.FS`로 임베드)
- SQLite DB 파일 1개 (`powercodedeck.db` + WAL/SHM)
- `.env` 설정 파일 1개

---

## 프로젝트 구조

```
power-code-deck/
├── server/                 # Go 백엔드
│   ├── main.go            # 엔트리포인트: 서비스 조립, 라우터, static 임베드, 배너
│   ├── cli/               # 서브커맨드 CLI (root, agents, auth)
│   ├── auth/              # JWT 발급/검증, HTTP·WS 인증 미들웨어
│   ├── version/           # 제품명/버전 상수 (PowerCodeDeck v0.2.0)
│   ├── config/            # 이중 prefix env 로드 + 최초 실행 인증 마법사
│   ├── db/                # SQLite 초기화 + 마이그레이션
│   ├── handlers/          # HTTP 핸들러
│   │   ├── agents.go      #   에이전트 CRUD, send, slash-commands
│   │   ├── auth_handler.go#   login / refresh
│   │   ├── files.go       #   파일 tree/read/write/mkdir/delete/rename/stat
│   │   ├── projects.go    #   최근/탐색/검색/생성/삭제/이름변경
│   │   ├── logs.go        #   로그 검색/조회
│   │   ├── notification.go#   알림 목록/클리어/읽음
│   │   ├── meta.go        #   에이전트 메타(git/포트) 및 상태/진행/로그 갱신
│   │   ├── proxy.go       #   외부 URL 프록시 (iframe X-Frame-Options 우회)
│   │   └── helpers.go     #   공통 응답 헬퍼
│   ├── middleware/        # CORS, Helmet(CSP), Rate Limiter
│   ├── services/          # 비즈니스 로직
│   │   ├── agent.go       #   에이전트 생성/삭제/재시작 (tmux+pty 연동)
│   │   ├── tmux.go        #   tmux 세션 관리
│   │   ├── pty.go         #   PTY 읽기/쓰기 펌프
│   │   ├── file.go        #   파일시스템 접근
│   │   ├── watcher.go     #   fsnotify 파일 변경 감시
│   │   ├── project.go     #   프로젝트/최근 목록
│   │   ├── git.go         #   git 브랜치·dirty·ahead 조회
│   │   ├── port_scanner.go#   에이전트 리스닝 포트 감지
│   │   └── notification.go#   알림 저장/조회
│   ├── ws/                # WebSocket 허브
│   │   ├── hub.go         #   연결·메시지 라우팅·브로드캐스트
│   │   ├── client.go      #   read/write pump
│   │   └── message.go     #   이벤트/페이로드 타입 정의
│   └── static/            # 임베드된 Vite 빌드 산출물 (make build가 생성)
├── client/                 # React 프론트엔드
│   └── src/
│       ├── pages/         # 라우트 페이지
│       │   ├── ProjectSelectPage.tsx
│       │   ├── AgentLauncherPage.tsx
│       │   ├── DashboardPage.tsx
│       │   ├── TerminalPage.tsx
│       │   ├── LogsPage.tsx
│       │   ├── SettingsPage.tsx
│       │   └── LoginPage.tsx
│       ├── components/
│       │   ├── agent/     # 에이전트 카드, 런처, 생성 시트
│       │   ├── terminal/  # 터미널 뷰, 입력, 모바일 툴바
│       │   ├── file/      # 파일 탐색기, 에디터, 프리뷰
│       │   ├── browser/   # 내장 브라우저 패널
│       │   ├── animation/ # 도트 캐릭터, 오비탈, 타임라인, sprites
│       │   ├── notification/ # 알림 센터
│       │   ├── project/   # 프로젝트 선택 UI
│       │   ├── settings/  # 설정 패널
│       │   ├── sidebar/   # 사이드바
│       │   ├── layout/    # 네비게이션, 레이아웃
│       │   ├── auth/      # 로그인/PIN 입력
│       │   ├── CommandPalette.tsx
│       │   └── icons.tsx
│       ├── hooks/         # React 훅
│       ├── lib/           # api.ts, ws.ts, soundManager, subAgentSounds, paletteGenerator
│       ├── stores/        # appStore.ts (Zustand)
│       └── styles/        # 전역 스타일
├── install.sh                # 설치 스크립트 (macOS·Linux)
├── install.ps1 / install.bat # 설치 스크립트 (Windows)
├── Makefile               # 빌드 커맨드
├── ecosystem.config.js    # PM2 설정
├── ARCHITECTURE.md        # 상세 설계 문서
└── .env.example           # 환경변수 예시
```

---

## 아키텍처

### 데이터 흐름

```
┌─────────────────────────────────────────────────────────────┐
│  Browser                                                     │
│                                                              │
│  ┌──────────┐    ┌──────────────┐    ┌───────────────────┐  │
│  │ React    │    │ AgentDeckWS  │    │ xterm.js          │  │
│  │ App      │───▶│ (WebSocket)  │◀──▶│ Terminal Emulator │  │
│  │ Zustand  │    │ ws.ts        │    │ TerminalView.tsx  │  │
│  └──────────┘    └──────┬───────┘    └───────────────────┘  │
└─────────────────────────┼───────────────────────────────────┘
                          │ WebSocket (JSON)
                          │ ws://host:33033/ws?token=JWT
                          ▼
┌─────────────────────────────────────────────────────────────┐
│  Go Server (단일 바이너리)                                    │
│                                                              │
│  ┌──────────┐    ┌──────────────┐    ┌──────────────────┐   │
│  │ HTTP     │    │ WebSocket    │    │ Static Files     │   │
│  │ Handlers │    │ Hub          │    │ (embed.FS)       │   │
│  │ (API)    │    │              │    │ Vite Build       │   │
│  └──────────┘    └──────┬───────┘    └──────────────────┘   │
│                         │                                    │
│  ┌──────────┐    ┌──────┴───────┐    ┌──────────────────┐   │
│  │ Agent    │    │ PTY Service  │    │ Watcher Service  │   │
│  │ Service  │    │ (creack/pty) │    │ (fsnotify)       │   │
│  └──────────┘    └──────┬───────┘    └──────────────────┘   │
│                         │                                    │
│  ┌──────────┐    ┌──────┴───────┐    ┌──────────────────┐   │
│  │ SQLite   │    │ Tmux Service │    │ Git / PortScan / │   │
│  │ (WAL)    │    │              │    │ Notify / Auth    │   │
│  └──────────┘    └──────┬───────┘    └──────────────────┘   │
└─────────────────────────┼───────────────────────────────────┘
                          │ PTY (pseudo-terminal)
                          ▼
┌─────────────────────────────────────────────────────────────┐
│  tmux session                                                │
│  ┌───────────────────────────────────────────────────────┐  │
│  │  Claude Code / Gemini CLI / Codex CLI / Custom        │  │
│  │  (alternate screen buffer + mouse tracking)           │  │
│  └───────────────────────────────────────────────────────┘  │
└─────────────────────────────────────────────────────────────┘
```

### 터미널 I/O 흐름

**입력 (사용자 → 에이전트):**
```
Browser: TerminalInput/키보드 → agentDeckWS.send('terminal:input', data)
Server:  hub.handleMessage() → ptySvc.Write(agentId, data)
PTY:     session.Ptmx.Write() → tmux → Claude Code stdin
```

**출력 (에이전트 → 사용자):**
```
PTY:     session.Ptmx.Read() → ReadPump callback
Server:  hub.BroadcastToAgent(agentId, 'terminal:output', data)
Browser: agentDeckWS.on('terminal:output') → xterm.write(data)
```

출력은 동시에 SQLite `logs` 테이블에도 저장되어 로그 뷰어에서 검색됩니다.

### DB 스키마 (SQLite, WAL)

| 테이블 | 주요 컬럼 | 용도 |
|--------|-----------|------|
| `agents` | id, preset, name, tmux_session, working_dir, command, args, status, color_hue, color_name | 에이전트 레코드 |
| `recent_projects` | path, name, last_opened_at, last_agent_preset, open_count | 최근 프로젝트 이력 |
| `logs` | agent_id, data, created_at | 터미널 출력 로그 (agent 삭제 시 CASCADE) |
| `notifications` | agent_id, reason, message, read, created_at | 알림 (agent 삭제 시 CASCADE) |

### REST API (주요 엔드포인트)

모든 `/api/*`는 `Authorization: Bearer <JWT>` 필요 (auth·health 제외).

```
POST /api/auth/login            PIN → JWT + Refresh Token
POST /api/auth/refresh          토큰 갱신
GET  /api/auth/health           헬스체크 (무인증)

GET    /api/agents              에이전트 목록
POST   /api/agents              생성
GET    /api/agents/{id}         조회
DELETE /api/agents/{id}         삭제
POST   /api/agents/{id}/restart 재시작
POST   /api/agents/{id}/send    텍스트 전송
GET    /api/agents/{id}/meta    git/포트 메타
GET    /api/agents/slash-commands  슬래시 커맨드 목록

GET  /api/files/tree|read|stat            파일 조회
PUT  /api/files/write   POST /api/files/mkdir
DELETE /api/files/delete  PATCH /api/files/rename

GET /api/projects/recent|browse|detect|search
POST /api/projects/create  DELETE /api/projects/delete  PATCH /api/projects/rename

GET  /api/logs   GET /api/logs/{agentId}          로그 검색/조회
GET  /api/notifications  POST /api/notifications/clear
GET  /api/proxy?url=...                            외부 URL 프록시

GET  /ws?token=<JWT>                               WebSocket
```

### WebSocket 이벤트

| 방향 | 이벤트 | 설명 |
|------|--------|------|
| C→S | `terminal:attach` / `terminal:detach` | 터미널 연결/해제 (agentId, cols, rows) |
| C→S | `terminal:input` | 키 입력 전송 (xterm 직접 입력) |
| C→S | `terminal:pasteSubmit` | Prompt Bar 텍스트를 붙여넣고 Enter 전송 (서버가 bracketed paste로 래핑) |
| C→S | `terminal:pasteOnly` | Prompt Bar 텍스트를 붙여넣기만 (Enter 없음) |
| C→S | `terminal:resize` | 터미널 크기 변경 |
| C→S | `file:watch` / `file:unwatch` | 파일 감시 시작/중지 |
| S→C | `terminal:output` | 터미널 출력 스트림 |
| S→C | `agent:list` / `status` / `created` / `destroyed` | 에이전트 상태 변경 |
| S→C | `file:changed` / `file:tree` | 파일 변경/트리 갱신 |
| S→C | `agent:meta` | git 브랜치·dirty·ahead, 리스닝 포트 |
| S→C | `agent:meta:status` / `:progress` / `:log` | 커스텀 상태·진행률·로그 |
| S→C | `agent:notification` / `:clear` | 알림 도착/해제 |

### 내장 브라우저 프록시

에이전트가 로컬 포트(예: 개발 서버)를 띄우면 포트 스캐너가 감지해 iframe으로 바로 붙입니다.
외부 URL은 `X-Frame-Options`/CSP로 iframe 로드가 막히므로 `/api/proxy`가 서버에서 대신 가져와(최대 10MB, 리다이렉트 5회 제한) 렌더링합니다.

### 빌드 파이프라인

```
client/src/ ──(vite build)──▶ client/dist/
                                    │
                              (cp → server/static/)
                                    │
server/*.go + server/static/ ──(go build + embed.FS)──▶ ./pcd
                                                        │
                                                  (pm2 restart)
```

### 인증 (선택 사항)

```
부팅 시 GET /api/auth/health → { authEnabled, authMethod, version }
  authEnabled=false → 로그인 건너뛰고 진입, WS 토큰 없이 연결 (서버가 통과)
  authEnabled=true  → 로그인 필요:
     PIN/비밀번호 입력 → POST /api/auth/login → JWT (7일) + Refresh Token (30일)
                       → localStorage 저장
                       → API: Authorization: Bearer <token>
                       → WS:  /ws?token=<token>
                       → 만료 시 자동 refresh
                       → 로그인 엔드포인트는 Rate Limiter(분당 10회)로 보호
```

---

## Roadmap

### v0.3.0 — Control Room

PowerCodeDeck의 다음 주요 기능은 **멀티 에이전트 관제실(Control Room)**입니다.
(이번 v0.2.0에서는 구현하지 않고 로드맵으로만 정의합니다.)

목표:
- 여러 에이전트 세션을 한 화면에서 관리
- 프로젝트별 세션 그룹핑
- 실행 중 / 종료됨 / 주의 필요 상태 표시
- Claude / Gemini / Codex / Shell 세션 빠른 진입
- 세션별 최근 출력 요약
- 세션 종료 / 재시작 / 로그 보기
- 승인 대기나 장시간 무응답 같은 주의 상태 표시

v0.2.0에서는 기존 멀티 에이전트 대시보드를 크게 수정하지 않고(Experimental로 분류), Control Room은 다음 버전 작업으로 남깁니다. 상세 로드맵은 [ROADMAP.md](ROADMAP.md) 참고.

---

## 라이선스

PowerCodeDeck은 듀얼 라이선스로 배포됩니다:

- **오픈소스**: [AGPL-3.0](LICENSE) — 오픈소스 프로젝트는 무료
- **상업용**: [Commercial License](LICENSE-COMMERCIAL.md) — SaaS/클로즈드소스 제품에 사용 시

상업용 라이선스 문의: GitHub 이슈에 `commercial-license` 레이블로 남겨주세요.
