# 엔드포인트 추상화 · 크로스오리진 태세 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 클라이언트에서 same-origin 가정을 걷어내고, 서버가 크로스오리진 클라이언트를 1급으로 받게 한다. **이것 하나가 데스크탑-원격과 브라우저-크로스오리진을 동시에 연다.**

**Architecture:** 지금 클라이언트는 서버와 같은 오리진에서만 산다 — `API_BASE = '/api'`(`api.ts:1`), `location.host`(`ws.ts:63`). 서버 태세도 대칭이다: `AllowedOrigins()`는 루프백 4종만 무조건 허용하고(`config.go:165`), `HostCheck`는 비루프백 Host를 명시 설정 시에만 통과시키며(`config.go:190`), `checkOrigin`은 빈 Origin을 통과시킨다(`ws/hub.go:50`).

핵심은 **엔드포인트를 런타임 값으로 만드는 것**이고, `baseUrl`이 비면 현재 오리진 — 즉 **오늘의 동작이 기본값으로 보존된다.** 서버 쪽은 새 가드를 더하는 게 아니라 세 가드를 **하나의 소스에서 파생**시킨다. `main.go:38-48`의 주석이 이미 경고한다: 가드끼리 어긋나면 "페이지는 뜨는데 붙지는 않는" 상태가 된다.

이 계획은 데스크탑 셸을 만들지 않는다. 끝나면 **셸 없이 브라우저만으로 원격 접속이 검증된다.**

**Tech Stack:** Go 1.25 (표준 라이브러리만), React + TypeScript, Zustand, react-router.

## Global Constraints

- 스펙: `docs/superpowers/specs/2026-08-21-desktop-and-remote-design.md` §1·§2. 그 문서의 비목표(릴레이, 중앙 집계, 계정/다중 사용자, 소스 동기화)는 여기서도 건드리지 않는다.
- **인증 강화는 이 계획이 아니다.** 기기 자격증명·페어링·노출도 파생 기동거부는 스펙 §3, 별도 계획이다. 여기서는 **토큰 저장을 엔드포인트별로 쪼개는 것까지만** 한다.
- **루프백 단독 사용자는 아무것도 달라지지 않아야 한다.** 이것이 회귀 판정의 첫 기준이다.
- 새 Go 모듈·npm 패키지 금지.
- 서버 검증: `cd server && CGO_ENABLED=0 go build ./... && CGO_ENABLED=0 go test ./...`
- 클라이언트 검증: `cd client && ./node_modules/.bin/tsc --noEmit`
- `go vet ./...`은 이미 실패한다(`services/claude_resume_live_test.go:105`). 새로 늘리지 말 것.
- 마지막에 `dist/pcd.exe` 재빌드.

## File Structure

| 파일 | 책임 | 상태 |
|---|---|---|
| `client/src/lib/endpoint.ts` | 엔드포인트 레코드 + 현재 선택 + 토큰 저장 (단일 출처) | 생성 |
| `client/src/lib/endpoint.test.ts` | 기본값=현재 오리진, URL 조립 규칙 | 생성 |
| `client/src/lib/api.ts` | `API_BASE` 상수 → 엔드포인트 조회 | 수정 |
| `client/src/lib/ws.ts` | `location.host` → `endpoint.wsUrl` | 수정 |
| `client/src/components/browser/BrowserPanel.tsx` | `/api/proxy` 직접 fetch 제거 | 수정 |
| `client/src/App.tsx` | 401 → 앱 전체 로그인 이동을 엔드포인트 단위로 | 수정 |
| `server/config/config.go` | `ClientOrigins` 단일 소스 → 세 가드 파생 | 수정 |
| `server/config/origins_test.go` | 세 가드 정합성 고정 | 생성 |
| `server/main.go` | CORS에 파생값 전달 | 수정 |
| `server/handlers/auth_handler.go` | 핸드오프/익명 토큰의 쿠키 의존 제거 | 수정 |

---

### Task 1: 엔드포인트 모듈 — 단일 출처

**Files:**
- Create: `client/src/lib/endpoint.ts`, `client/src/lib/endpoint.test.ts`

**Interfaces:**
```ts
export interface Endpoint {
  id: string;            // 'local' = 현재 오리진
  label: string;
  baseUrl: string;       // '' 이면 현재 오리진
  capabilities: { webPush: boolean; localFiles: boolean };
}
export function currentEndpoint(): Endpoint;
export function apiUrl(path: string): string;      // baseUrl + '/api' + path
export function wsUrl(token: string, device: string): string;
export function getToken(id?: string): string | null;
export function setTokens(access: string, refresh: string, id?: string): void;
export function clearTokens(id?: string): void;
```

- [x] **Step 1: 실패하는 테스트 작성.** 고정할 규칙:
  - `baseUrl: ''` → `apiUrl('/agents')` === `'/api/agents'` (**오늘과 글자 그대로 같아야 한다**)
  - `baseUrl: 'https://pcd.example.com'` → `'https://pcd.example.com/api/agents'`
  - 후행 슬래시가 있는 baseUrl에서도 `//api`가 생기지 않는다
  - `wsUrl`: `https://` → `wss://`, `http://` → `ws://`, `baseUrl: ''` → `location` 기반
  - 토큰 키가 엔드포인트별로 분리된다 (`pcd:endpoints:<id>:accessToken`)
- [x] **Step 2: 구현.** 저장은 `localStorage['pcd:endpoints']`(목록) + 엔드포인트별 토큰 키. **기존 `accessToken`/`refreshToken` 키가 있으면 `local` 엔드포인트로 1회 이관**한다 — 기존 사용자가 로그아웃되면 안 된다.
- [x] **Step 3: 검증.** **이 리포에는 클라이언트 테스트 러너가 없다** — `package.json`에 `test` 스크립트도, vitest/jest 의존성도 없다. 유일한 선례인 `client/src/components/intelligence/savings.test.ts`는 **프레임워크 없이 모듈 스코프에서 단언하고 실패 시 throw하는** 파일이고, `tsconfig.json`의 `include: ["src"]`에 걸려 **`tsc --noEmit`으로 타입만 검사**된다(자동 실행되지 않는다). 같은 관례를 따르되, 이 파일의 단언이 실제로 돌지 않는다는 것을 알고 있을 것 — 진짜 검증은 Task 6이다. 러너를 새로 도입하지 말 것(Global Constraints의 npm 패키지 금지).

---

### Task 2: `api.ts`를 엔드포인트 위로 옮긴다

**Files:**
- Modify: `client/src/lib/api.ts` (`API_BASE` line 1, `getToken`/`setTokens`/`clearTokens` line 65-77, `refreshToken` line 79-96, `apiFetch` line 98-131, `rawFileObjectURL` line 278-288)

- [x] **Step 1: 상수 제거.** `const API_BASE = '/api'`를 지우고 `apiUrl(path)` 호출로 바꾼다. **`API_BASE`가 남아 있으면 이 태스크가 끝나지 않은 것이다** — `grep -n "API_BASE" client/src/lib/api.ts`가 0줄이어야 한다.
- [x] **Step 2: 토큰 함수 위임.** `api.ts`의 로컬 `getToken`/`setTokens`/`clearTokens`를 `endpoint.ts`로 위임한다. 저장 키 문자열이 두 파일에 흩어지면 안 된다.
- [x] **Step 3: `rawFileObjectURL` 봉합.** `api.ts:281`은 `apiFetch`를 우회해 fetch를 직접 조립한다. Bearer 첨부와 URL 조립을 공유 헬퍼로 빼서 **인증/오리진 규칙이 한 곳에만 있게** 한다. blob을 받아야 해서 `apiFetch`(JSON 전제)를 못 쓰는 것이므로, `apiRequest(path, init): Promise<Response>`를 만들고 `apiFetch`가 그 위에 앉게 하는 게 맞다.
- [x] **Step 4: 401 처리.** `api.ts:118`의 `window.location.href = '/login'`을 지운다. 대신 `ApiError`에 `endpointId`를 실어 던지고, 라우팅 결정은 App이 한다(Task 5). **원격 하나가 만료됐다고 앱 전체를 로그인 화면으로 던지면 안 된다.**
- [x] **Step 5: 검증.** `cd client && ./node_modules/.bin/tsc --noEmit`

---

### Task 3: `ws.ts`와 마지막 새는 곳

**Files:**
- Modify: `client/src/lib/ws.ts` (`connect` ~line 48-66)
- Modify: `client/src/components/browser/BrowserPanel.tsx:47`

- [x] **Step 1: `ws.ts` 전환.** `${protocol}//${location.host}/ws?...` → `wsUrl(token, getDeviceId())`. 재연결 로직·전송 큐·`wake()` 경로는 **건드리지 않는다** — 모바일 사파리 프리즈 대응이 거기 들어 있다.
- [x] **Step 2: 엔드포인트 전환 시 재연결.** 현재 엔드포인트가 바뀌면 소켓을 끊고 새로 연다. `connect(token)`이 이미 "같은 토큰이면 무시" 가드를 갖고 있으므로(line 49-54), 그 가드가 **엔드포인트도 함께 비교**하도록 고친다. 안 그러면 다른 서버로 옮겼는데 옛 소켓을 재사용한다.
- [x] **Step 3: `BrowserPanel.tsx:47` 봉합.** `fetch('/api/proxy?url=…')`를 `api`의 헬퍼로 바꾼다. 이 파일과 `rawFileObjectURL`이 **apiFetch를 우회하는 유일한 두 곳**이므로(`grep -rn "fetch(\`/api\|fetch('/api" client/src`로 확인), 여기까지 하면 클라이언트에 same-origin 가정이 남지 않는다.
- [x] **Step 4: 검증.** `grep -rn "location.host\|'/api\|\`/api" client/src --include=*.ts --include=*.tsx` 결과에 `endpoint.ts` 외의 히트가 없어야 한다. + `tsc --noEmit`

---

### Task 4: 서버 — 세 가드를 하나의 소스에서 파생

**Files:**
- Modify: `server/config/config.go` (`AllowedOrigins` line 165, `AllowedHosts` line 190)
- Create: `server/config/origins_test.go`
- Modify: `server/main.go` (line 176-179)

**Interfaces:**
- Produces: `POWERCODEDECK_CLIENT_ORIGINS` (기존 `CORS_ORIGINS`는 별칭으로 계속 동작)
- Produces: `func (c *Config) ClientOrigins() []string` — `AllowedOrigins()`·CORS·`checkOrigin`이 **모두 이걸 쓴다**

- [x] **Step 1: 실패하는 정합성 테스트 작성.** `server/config/origins_test.go`:

```go
// 세 가드는 같은 설정에서 같은 판단을 해야 한다. main.go:38-48이 기록한 실패가
// 정확히 이 어긋남이었다 — Host 가드는 LAN IP를 자동 감지해 통과시키는데 Origin
// 허용목록은 아니어서, 페이지는 뜨지만 토큰 발급과 WS 핸드셰이크가 거부됐다.
func TestGuardsAgreeOnEveryConfiguredOrigin(t *testing.T) {
	cfg := &Config{Port: "33033", PublicURL: "https://pcd.example.com",
		LanURL: "http://192.168.1.50:33033", CORSOrigins: "tauri://localhost"}
	for _, origin := range cfg.ClientOrigins() {
		if !contains(cfg.AllowedOrigins(), origin) {
			t.Errorf("origin %q allowed by ClientOrigins but not AllowedOrigins", origin)
		}
		if host := hostOf(origin); !hostAllowed(cfg.AllowedHosts(), host) {
			t.Errorf("origin %q allowed but its Host %q is rejected by HostCheck", origin, host)
		}
	}
}

// 데스크탑 셸의 Origin은 명시 허용이어야 한다. checkOrigin의 "빈 Origin 통과"에
// 기대면 나중에 그 예외를 못 막는다.
func TestDesktopShellOriginsAreExplicit(t *testing.T) { ... }

// 루프백 단독 사용자는 달라지는 게 없어야 한다.
func TestLoopbackOnlyConfigIsUnchanged(t *testing.T) { ... }
```

- [x] **Step 2: `ClientOrigins()` 구현.** 루프백 4종 + `PublicURL` + `LanURL` + `CLIENT_ORIGINS`/`CORS_ORIGINS` + 데스크탑 셸 기본값(`tauri://localhost`, `https://tauri.localhost`). `AllowedOrigins()`는 이걸 반환하게 한다.
- [x] **Step 3: CORS 배선.** `main.go:179`의 `middleware.CORS(cfg.CORSOrigins)`가 **문자열 원본**을 받고 있다 — `PublicURL`/`LanURL`은 들어가지 않는다. `middleware.CORS`가 `[]string`을 받게 고치고 `cfg.ClientOrigins()`를 넘긴다. **이게 지금 어긋나 있는 지점이다.**
- [x] **Step 4: 검증.** `CGO_ENABLED=0 go test ./config/ ./middleware/ ./ws/`

---

### Task 5: 쿠키 의존 제거 + 엔드포인트 단위 재인증

**Files:**
- Modify: `client/src/lib/api.ts` (`handoffExchange` line 148, `getAnonymousToken` line 162)
- Modify: `server/handlers/auth_handler.go`
- Modify: `client/src/App.tsx` (`apply` ~line 49-73)

- [x] **Step 1: `credentials: 'same-origin'` 두 곳 제거.** 크로스오리진에서는 성립하지 않는다.
  - `getAnonymousToken` — 쿠키를 쓰지 않는다. Origin 허용목록이 이미 가드다. `credentials`만 떼면 된다.
  - `handoffExchange` — httpOnly 핸드오프 쿠키에 의존한다(`auth.go:117`). **크로스오리진 클라이언트에서는 이 흐름을 쓸 수 없다.** QR은 서버가 서빙한 페이지에서 열리므로 same-origin이 유지되고, 오늘의 동작은 그대로 산다. 여기서는 **쿠키가 없을 때 명확한 에러로 끝나게만** 하고, 헤더 기반 상환은 스펙 §3(기기 자격증명) 계획으로 넘긴다. **이 경계를 코드 주석에 남길 것.**
- [x] **Step 2: `App.tsx` 부팅 흐름.** `authReady`/`isAuthenticated`가 앱 전역 플래그다. 현재 엔드포인트 기준으로 읽도록 바꾸고, 엔드포인트가 바뀌면 다시 확인한다. **no-auth 모드의 "부팅 시 항상 새 익명 토큰 발급"(`App.tsx:56-65`의 주석이 이유를 설명한다)은 엔드포인트별로 유지**해야 한다 — 서버마다 인메모리 시크릿이 다르다.
- [x] **Step 3: 401 라우팅.** Task 2 Step 4에서 던지는 `endpointId`를 받아, 그 엔드포인트가 현재 것이면 로그인으로 보내고 아니면 배너로만 표시한다.
- [x] **Step 4: 검증.** `tsc --noEmit` + 서버 테스트 전체.

---

### Task 6: 실물 확인 — 다른 오리진에서 서빙

**이 태스크가 이 계획의 성공 판정이다.** 단위 테스트로는 크로스오리진을 증명할 수 없다.

- [x] **Step 1: 두 오리진 세팅.** 서버를 `POWERCODEDECK_BIND_HOST=0.0.0.0`으로 띄우고, `POWERCODEDECK_CLIENT_ORIGINS=http://localhost:5173`을 준다. 클라이언트를 `vite` 개발 서버(다른 포트)에서 띄운 뒤 baseUrl로 서버를 가리킨다.
- [x] **Step 2: 다섯 곳을 전부 확인.** **여기가 새는 지점이다:**
  - 터미널 입출력 (WS attach → 키 입력 → 출력)
  - 네이티브 채팅 (`native:open` → 전송 → 응답 스트림)
  - 파일 raw (이미지 미리보기 — `rawFileObjectURL`)
  - 브라우저 패널 (`/api/proxy`)
  - 파일 업로드 (`.pcd-attachments/`)
  **실측(2026-08-21, Windows Chrome → WSL, Tailscale 오리진):** UI를 vite(`:5173`), 서버를
  `:33209`로 **서로 다른 오리진**에 띄우고 클라이언트 엔드포인트를 서버 쪽으로 지정해 확인했다.
  다섯 곳 전부 통과 — REST 200 / WS open / `files/raw` 200(37,560바이트) /
  multipart 첨부 업로드(프리플라이트 통과, 서버 응답 수신) / `api/proxy` 200. no-auth
  모드의 익명 토큰도 **엔드포인트별로** 발급됐다. 부수 확인: Host 가드가 tailnet 주소를
  허용목록에 넣기 전에는 `forbidden host`로 정확히 거부했다.

- [x] **Step 3: 회귀 확인.** 같은 빌드를 **서버가 직접 서빙**하는 원래 방식으로 열어 위 다섯이 그대로 되는지. 루프백 단독 사용자에게 달라지는 게 없어야 한다.
  **실측:** 서버가 직접 서빙하는 페이지(`:33209`)에서 기본 `local` 엔드포인트로 REST 200 · WS open.
  레거시 `accessToken`/`refreshToken` 키를 심어두고 새로고침하면 엔드포인트별 키로 이관되고
  레거시 키는 사라진다 — 기존 사용자가 로그아웃되지 않는다.

- [x] **Step 4: 실패 모드 확인.** 허용목록에 없는 오리진에서 열면 **명확히 거부**돼야 한다 — 페이지는 뜨는데 WS만 조용히 안 붙는 상태가 되면 Task 4가 실패한 것이다.
  **실측:** 허용목록에서 UI 오리진을 뺀 채 재기동하니 REST·익명토큰이 `TypeError: Failed to fetch`
  (CORS 차단), WS는 즉시 거부. **셋이 함께 막히므로** "페이지는 뜨는데 WS만 조용히 안 붙는" 상태가
  아니다. curl 확인도 같음: WS 허용 오리진 101 / 허용목록 밖 403 / Origin 없는 네이티브 클라이언트 101,
  스푸핑된 비루프백 Host는 403.

- [x] **Step 5: 문서.** `README.md`에 원격 접속 절을 추가한다 — T1(LAN) / T2(리버스 프록시 + `PUBLIC_URL`) / T3(Tailscale). T2에는 **"WS 토큰이 쿼리스트링이라 프록시 액세스 로그에 남는다"**를 명시(스펙 §2).
- [x] **Step 6: `dist/pcd.exe` 재빌드.**

## Self-Review

- **`baseUrl: ''`이 기본값**이라 오늘의 동작이 보존된다. 이 성질이 깨지면 설계가 잘못된 것이다.
- **apiFetch 우회 두 곳(`api.ts:281`, `BrowserPanel.tsx:47`)이 이 계획의 실질적 난이도다.** 나머지는 상수 치환이다.
- CORS가 지금 `cfg.CORSOrigins` 원본 문자열만 받는 것(`main.go:179`)은 이미 존재하는 어긋남이다 — `PublicURL`로 접속하는 사용자는 CORS 프리플라이트를 통과하지 못한다. same-origin이라 아직 드러나지 않았을 뿐이다.
- 인증 강화를 여기 섞지 않는다. 토큰 저장 분리까지만 하고, 기기 자격증명·페어링은 스펙 §3 계획으로 남긴다.
- 데스크탑 셸을 만들지 않는다. 하지만 끝나면 셸은 "엔드포인트 하나를 자동으로 띄우는 껍데기"가 된다.
