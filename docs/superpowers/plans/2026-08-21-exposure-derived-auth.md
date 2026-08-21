# 노출도 파생 기동 정책 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 인증 없이 열려 있는 덱이 **조용히** 뜨지 않게 한다. 지금 기본값은 인증 없음이고, 그걸 막아주던 것은 루프백 바인딩뿐이다 — 원격을 열면 그 방어가 사라지는데 화면에는 경고 한 줄만 지나간다.

**Architecture:** 스펙 §3.1은 "루프백 밖 + `auth=none` → 기동 거부"라고 적었다. **Tailscale을 권장 경로로 정하면 그 규칙은 그대로 쓸 수 없다** — tailnet 주소(`100.64.0.0/10`)에 바인딩하는 것도 비루프백이라 권장 구성이 거부에 걸린다.

그래서 노출도를 이진값이 아니라 **세 단계**로 파생시킨다. 판정은 `config` 안에서 순수 함수로 하고(테스트 가능), `main.go`는 그 결과를 읽어 거부하거나 경고만 한다. 새 의존성도, 네트워크 조회도 없다 — 바인딩 주소와 설정값만 본다.

**Tech Stack:** Go 1.25 표준 라이브러리(`net`, `net/netip`)만.

## Global Constraints

- 스펙: `docs/superpowers/specs/2026-08-21-desktop-and-remote-design.md` §3·§4.
- **이것은 동작을 깨는 변경이다.** 오늘 `BIND_HOST=0.0.0.0` + `auth=none`으로 LAN 핸드오프를 쓰는 사용자는 다음 실행에서 기동이 거부된다. 탈출구(`ALLOW_INSECURE_EXPOSURE`)와 **무엇을 해야 하는지 알려주는 메시지**가 이 계획의 절반이다.
- **기기 자격증명은 이 계획이 아니다.** 스펙 §3.2·§3.3(jti·기기 테이블·페어링)은 별도이고, tailnet-only 구성에서는 미룰 수 있다.
- 새 Go 모듈 금지.
- 서버 검증: `cd server && CGO_ENABLED=0 go build ./... && CGO_ENABLED=0 go test ./...`
- `go vet ./...`은 이미 실패한다(`services/claude_resume_live_test.go:105`). 새로 늘리지 말 것.

## File Structure

| 파일 | 책임 | 상태 |
|---|---|---|
| `server/config/exposure.go` | 노출도 판정(순수 함수) + 정책 | 생성 |
| `server/config/exposure_test.go` | 판정표 고정 | 생성 |
| `server/config/config.go` | `ALLOW_INSECURE_EXPOSURE` 읽기 | 수정 |
| `server/main.go` | 기동 거부 / 경고 배너 | 수정 |
| `README.md` | 원격 접속 절에 정책 명시 | 수정 |

---

### Task 1: 노출도 판정

**Files:**
- Create: `server/config/exposure.go`, `server/config/exposure_test.go`

**Interfaces:**
- Produces: `type Exposure string` — `ExposureLoopback` / `ExposurePrivateNetwork` / `ExposureOpen`
- Produces: `func (c *Config) Exposure() Exposure`

- [ ] **Step 1: 실패하는 테스트 작성**

`server/config/exposure_test.go`:

```go
package config

import "testing"

// The three tiers exist because "not loopback" is too blunt once Tailscale is the
// recommended remote path: a tailnet address is not loopback, but the network
// itself is the credential. Collapsing it into "open" would make the recommended
// setup refuse to start.
func TestExposureTiers(t *testing.T) {
	cases := []struct {
		name string
		cfg  Config
		want Exposure
	}{
		{"default loopback", Config{BindHost: "127.0.0.1", Port: "33033"}, ExposureLoopback},
		{"ipv6 loopback", Config{BindHost: "::1", Port: "33033"}, ExposureLoopback},
		{"empty bind host defaults to loopback", Config{BindHost: "", Port: "33033"}, ExposureLoopback},
		{"tailscale CGNAT range", Config{BindHost: "100.116.117.48", Port: "33033"}, ExposurePrivateNetwork},
		{"tailnet lower bound", Config{BindHost: "100.64.0.0", Port: "33033"}, ExposurePrivateNetwork},
		{"tailnet upper bound", Config{BindHost: "100.127.255.255", Port: "33033"}, ExposurePrivateNetwork},
		{"just outside the tailnet range", Config{BindHost: "100.128.0.1", Port: "33033"}, ExposureOpen},
		{"LAN address", Config{BindHost: "192.168.0.25", Port: "33033"}, ExposureOpen},
		{"all interfaces", Config{BindHost: "0.0.0.0", Port: "33033"}, ExposureOpen},
		{"all interfaces v6", Config{BindHost: "::", Port: "33033"}, ExposureOpen},
		// A public URL means something in front of us is reachable from elsewhere,
		// whatever we bound to. That is exposure regardless of the bind address.
		{"public URL beats loopback", Config{BindHost: "127.0.0.1", Port: "33033", PublicURL: "https://deck.example.com"}, ExposureOpen},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.cfg.Exposure(); got != tc.want {
				t.Fatalf("Exposure() = %q, want %q", got, tc.want)
			}
		})
	}
}

// The policy, separate from the classification: only the open tier refuses, and
// only when nothing authenticates.
func TestStartupRefusal(t *testing.T) {
	cases := []struct {
		name       string
		cfg        Config
		wantRefuse bool
	}{
		{"loopback without auth is fine", Config{BindHost: "127.0.0.1", Port: "33033"}, false},
		{"tailnet without auth warns only", Config{BindHost: "100.116.117.48", Port: "33033"}, false},
		{"open without auth refuses", Config{BindHost: "0.0.0.0", Port: "33033"}, true},
		{"open with auth is fine", Config{BindHost: "0.0.0.0", Port: "33033", AuthEnabled: true, AuthMethod: "pin"}, false},
		{"open without auth, explicitly overridden", Config{BindHost: "0.0.0.0", Port: "33033", AllowInsecureExposure: true}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.cfg.CheckExposure()
			if tc.wantRefuse && err == nil {
				t.Fatal("CheckExposure() = nil, want a refusal")
			}
			if !tc.wantRefuse && err != nil {
				t.Fatalf("CheckExposure() = %v, want nil", err)
			}
		})
	}
}

// The refusal has to teach, not just refuse: it is the first thing a user sees when
// their working setup stops working.
func TestRefusalMessageNamesEveryWayOut(t *testing.T) {
	cfg := Config{BindHost: "0.0.0.0", Port: "33033"}
	err := cfg.CheckExposure()
	if err == nil {
		t.Fatal("expected a refusal")
	}
	for _, want := range []string{"AUTH_METHOD", "BIND_HOST", "Tailscale", "ALLOW_INSECURE_EXPOSURE"} {
		if !contains(err.Error(), want) {
			t.Errorf("refusal message does not mention %q:\n%s", want, err.Error())
		}
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (func() bool {
		for i := 0; i+len(needle) <= len(haystack); i++ {
			if haystack[i:i+len(needle)] == needle {
				return true
			}
		}
		return false
	})()
}
```

- [ ] **Step 2: 테스트가 실패하는지 확인**

Run: `cd server && CGO_ENABLED=0 go test ./config/ -run TestExposure`
Expected: FAIL — `undefined: ExposureLoopback` 등 컴파일 에러.

- [ ] **Step 3: 구현**

`server/config/exposure.go`:

```go
package config

import (
	"fmt"
	"net/netip"
	"strings"
)

// Exposure is how reachable this deck is, derived from how it was configured.
//
// Three tiers rather than two, because Tailscale is the recommended remote path
// (spec §4) and a tailnet address is not loopback. Folding it into "open" would
// refuse the very setup we tell people to use; folding it into "loopback" would
// pretend a shared network is a private one. It is its own tier.
type Exposure string

const (
	// ExposureLoopback: only this machine can reach the deck.
	ExposureLoopback Exposure = "loopback"
	// ExposurePrivateNetwork: reachable inside a tailnet. Membership in the network
	// is itself a credential, enforced by something other than us.
	ExposurePrivateNetwork Exposure = "private-network"
	// ExposureOpen: a LAN, every interface, or something publishing us from in front.
	// Who can reach the deck is no longer decided by us.
	ExposureOpen Exposure = "open"
)

// tailnet is Tailscale's slice of the CGNAT range (100.64.0.0/10). Tailscale hands
// out addresses from 100.64.0.0 to 100.127.255.255.
var tailnet = netip.MustParsePrefix("100.64.0.0/10")

func (c *Config) Exposure() Exposure {
	// Anything published in front of us is exposure, whatever we bound to.
	if strings.TrimSpace(c.PublicURL) != "" {
		return ExposureOpen
	}
	host := strings.TrimSpace(c.BindHost)
	if host == "" {
		host = "127.0.0.1" // the default applied in Load()
	}
	if host == "0.0.0.0" || host == "::" {
		return ExposureOpen
	}
	addr, err := netip.ParseAddr(strings.Trim(host, "[]"))
	if err != nil {
		// A name we cannot resolve to a literal (e.g. a hostname). Treat it as open:
		// guessing in the permissive direction is how a deck ends up on a LAN with no
		// authentication.
		return ExposureOpen
	}
	if addr.IsLoopback() {
		return ExposureLoopback
	}
	if addr.Is4() && tailnet.Contains(addr) {
		return ExposurePrivateNetwork
	}
	return ExposureOpen
}

// CheckExposure returns a refusal when the deck would be reachable by people it
// cannot identify. Loopback needs nothing. A tailnet is guarded by the tailnet.
// Everything else needs either authentication or a deliberate override.
func (c *Config) CheckExposure() error {
	if c.Exposure() != ExposureOpen || c.AuthEnabled || c.AllowInsecureExposure {
		return nil
	}
	return fmt.Errorf(`refusing to start: this deck would be reachable from other machines with no authentication.

  bind host : %s
  public URL: %s

A PowerCodeDeck session is a shell on this machine — anyone who can reach it can run
commands here. Pick one:

  1. Turn on auth        POWERCODEDECK_AUTH_METHOD=pin  POWERCODEDECK_PIN=…
  2. Stay on loopback    POWERCODEDECK_BIND_HOST=127.0.0.1  (put a reverse proxy in front)
  3. Use a private net   Tailscale: bind to the tailnet address (100.x.y.z), which is
                         reachable only by your own devices

If you understand the exposure and want it anyway:

  POWERCODEDECK_ALLOW_INSECURE_EXPOSURE=true`,
		defaultStr(c.BindHost, "127.0.0.1"), defaultStr(c.PublicURL, "(none)"))
}
```

`defaultStr`는 `config.go`에 이미 있다(`printEnv`가 쓴다). 없으면 같은 파일에 추가한다.

- [ ] **Step 4: `AllowInsecureExposure` 필드 추가**

`server/config/config.go`의 `Config` 구조체에 필드를 더하고 `Load()`에서 읽는다:

```go
	// AllowInsecureExposure lets a user start an unauthenticated deck that other
	// machines can reach. Deliberately verbose to type, and never defaulted on.
	AllowInsecureExposure bool
```

`Load()`의 다른 `envDual` 호출들 옆에:

```go
	cfg.AllowInsecureExposure = parseBool(envDual("ALLOW_INSECURE_EXPOSURE"))
```

- [ ] **Step 5: 테스트 통과 확인**

Run: `cd server && CGO_ENABLED=0 go test ./config/ -v -run 'TestExposure|TestStartup|TestRefusal'`
Expected: PASS (3개 테스트, 하위 케이스 전부)

- [ ] **Step 6: 커밋**

```bash
git add server/config/exposure.go server/config/exposure_test.go server/config/config.go
git commit -m "feat(config): 노출도 3단계 판정과 기동 정책"
```

---

### Task 2: 기동에 연결

**Files:**
- Modify: `server/main.go`

- [ ] **Step 1: 거부를 기동 앞으로**

`main.go`에서 설정을 읽은 직후, 서버를 띄우기 전에 넣는다. `authSvc := auth.NewAuthService(...)` (~line 77) **앞**이다 — 거부할 상황이면 아무것도 시작하지 않아야 한다.

```go
	// Refuse before anything starts: a deck that is reachable by strangers with no
	// authentication is a shell on this machine handed out for free. The message
	// tells the user how to fix it (see config.CheckExposure).
	if err := cfg.CheckExposure(); err != nil {
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, err.Error())
		fmt.Fprintln(os.Stderr)
		os.Exit(1)
	}
```

- [ ] **Step 2: 배너에 노출도 표시**

`printBanner`의 `Auth :` 줄 아래에 추가한다:

```go
	fmt.Printf("     Reach: %s\n", cfg.Exposure())
```

그리고 기존의 조건 없는 인증 경고를, tailnet일 때는 **다른 문구**로 바꾼다. 지금은 루프백에서도 같은 경고가 떠서 경고가 값싸졌다:

```go
	switch {
	case !cfg.AuthEnabled && cfg.Exposure() == ExposurePrivateNetworkLabel:
		fmt.Println("  Note:")
		fmt.Println("  Authentication is off; your tailnet is what keeps this private.")
		fmt.Println("  Anyone on your tailnet can drive this machine.")
		fmt.Println()
	case !cfg.AuthEnabled && cfg.Exposure() != ExposureLoopbackLabel:
		fmt.Println("  Warning:")
		fmt.Printf("  %s authentication is disabled and this deck is reachable from other machines.\n", version.AppName)
		fmt.Println()
	}
```

**주의:** `main.go`는 `config` 패키지를 import하므로 상수는 `config.ExposurePrivateNetwork` / `config.ExposureLoopback`으로 참조한다. 위 스니펫의 `…Label` 이름은 그 참조를 뜻한다 — 실제 코드는 다음과 같다:

```go
	switch {
	case !cfg.AuthEnabled && cfg.Exposure() == config.ExposurePrivateNetwork:
		fmt.Println("  Note:")
		fmt.Println("  Authentication is off; your tailnet is what keeps this private.")
		fmt.Println("  Anyone on your tailnet can drive this machine.")
		fmt.Println()
	case !cfg.AuthEnabled && cfg.Exposure() != config.ExposureLoopback:
		fmt.Println("  Warning:")
		fmt.Printf("  %s authentication is disabled and this deck is reachable from other machines.\n", version.AppName)
		fmt.Println()
	}
```

기존의 `if !cfg.AuthEnabled { … "Do not expose this service directly to the public internet." … }` 블록은 이 switch로 **대체**한다.

- [ ] **Step 3: 빌드와 전체 테스트**

Run: `cd server && CGO_ENABLED=0 go build ./... && CGO_ENABLED=0 go test ./...`
Expected: 전 패키지 ok

- [ ] **Step 4: 손으로 네 가지 확인**

```bash
cd server && CGO_ENABLED=0 go build -o /tmp/pcd-exp .
# 1) 루프백: 그대로 뜬다
POWERCODEDECK_DB_PATH=/tmp/e.db /tmp/pcd-exp        # → Reach: loopback, 기동
# 2) 열림 + 인증 없음: 거부
POWERCODEDECK_BIND_HOST=0.0.0.0 POWERCODEDECK_DB_PATH=/tmp/e.db /tmp/pcd-exp   # → exit 1
# 3) 열림 + 탈출구: 경고와 함께 기동
POWERCODEDECK_BIND_HOST=0.0.0.0 POWERCODEDECK_ALLOW_INSECURE_EXPOSURE=true \
  POWERCODEDECK_DB_PATH=/tmp/e.db /tmp/pcd-exp
# 4) tailnet: 경고 아닌 안내와 함께 기동 (주소는 `tailscale ip -4`로 확인)
POWERCODEDECK_BIND_HOST=$(tailscale ip -4 2>/dev/null || echo 100.64.0.1) \
  POWERCODEDECK_DB_PATH=/tmp/e.db /tmp/pcd-exp
```

2번이 **거부 이유와 세 가지 해결책을 모두** 출력하는지 눈으로 확인한다.

- [ ] **Step 5: 커밋**

```bash
git add server/main.go
git commit -m "feat(server): 노출된 무인증 기동을 거부한다"
```

---

### Task 3: 문서

**Files:**
- Modify: `README.md`

- [ ] **Step 1: 원격 접속 절에 정책 표 추가**

`## 원격 접속` 절의 세 가드 설명 바로 아래에 넣는다:

```markdown
### 인증 정책 — 노출도에서 파생됩니다

서버는 자기가 얼마나 노출돼 있는지를 바인드 주소와 `PUBLIC_URL`에서 판정하고, 그에 따라 기동 여부를 정합니다.

| 노출도 | 조건 | 인증 없이 기동 |
|---|---|---|
| `loopback` | `127.0.0.1` / `::1` (기본값) | 됩니다 |
| `private-network` | tailnet 주소(`100.64.0.0/10`)에 바인드 | 됩니다 (안내만 출력) |
| `open` | LAN 주소 · `0.0.0.0` · `PUBLIC_URL` 설정 | **거부됩니다** |

`open`에서 인증 없이 띄우려면 세 가지 중 하나입니다: `POWERCODEDECK_AUTH_METHOD=pin`으로 인증을 켜거나,
루프백에 바인드하고 앞에 리버스 프록시를 두거나, tailnet 주소에 바인드하세요.
그래도 그냥 열겠다면 `POWERCODEDECK_ALLOW_INSECURE_EXPOSURE=true`가 탈출구입니다 —
**PowerCodeDeck 세션은 이 기계의 셸입니다. 닿을 수 있는 사람은 여기서 명령을 실행할 수 있습니다.**
```

- [ ] **Step 2: 환경변수 표에 한 줄**

```markdown
| `POWERCODEDECK_ALLOW_INSECURE_EXPOSURE` | `false` | 인증 없이 외부 접근 가능한 상태로 기동하는 것을 허용 (권장하지 않음) |
```

- [ ] **Step 3: 기존 T1(LAN) 예시 수정**

T1 절의 예시는 지금 `BIND_HOST=0.0.0.0`인데, 그대로면 **README를 따라 한 사용자가 기동 거부를 만난다.** 인증을 함께 켜도록 고친다:

```bash
POWERCODEDECK_BIND_HOST=0.0.0.0
POWERCODEDECK_LAN_URL=http://192.168.0.25:33033   # 이 서버의 LAN 주소
POWERCODEDECK_AUTH_METHOD=pin                      # open 노출도에서는 필수
POWERCODEDECK_PIN=…
```

- [ ] **Step 4: 커밋**

```bash
git add README.md
git commit -m "docs: 노출도 파생 인증 정책"
```

---

### Task 4: 이 변경이 깨는 것을 확인한다

- [ ] **Step 1: 기존 사용자 시나리오 재현**

오늘의 LAN 핸드오프 설정 그대로 띄운다:

```bash
POWERCODEDECK_BIND_HOST=0.0.0.0 POWERCODEDECK_LAN_HANDOFF_ENABLED=true \
  POWERCODEDECK_DB_PATH=/tmp/e.db /tmp/pcd-exp
```

Expected: **거부.** 이것이 의도된 동작이고, 메시지가 세 가지 해결책을 알려줘야 한다.

- [ ] **Step 2: CHANGELOG에 적는다**

`CHANGELOG.md` 최상단 섹션에:

```markdown
- ⚠️ **동작 변경** — 인증 없이 외부에서 접근 가능한 상태(`BIND_HOST`가 LAN/`0.0.0.0`이거나 `PUBLIC_URL` 설정)로는 기동하지 않습니다. 인증을 켜거나, 루프백에 바인드하거나, tailnet 주소를 쓰세요. 그대로 열려면 `POWERCODEDECK_ALLOW_INSECURE_EXPOSURE=true`.
```

- [ ] **Step 3: 커밋**

```bash
git add CHANGELOG.md
git commit -m "docs(changelog): 무인증 노출 기동 거부"
```

## Self-Review

- **이 계획의 핵심은 3단계 판정이다.** 스펙 §3.1의 이진 규칙을 그대로 구현하면 우리가 권장하는 Tailscale 구성이 거부에 걸린다. tailnet을 별도 티어로 두는 것은 완화가 아니라 **정확한 모델링**이다 — 네트워크 멤버십 자체가 자격증명이고, 그건 우리가 아니라 Tailscale이 강제한다.
- **해석 불가한 바인드 주소는 `open`으로 떨어뜨린다.** 관대한 쪽으로 추측하면 인증 없는 덱이 LAN에 올라간다.
- **탈출구는 반드시 있어야 한다.** 없으면 사용자는 우리를 우회하는 대신 이전 버전에 머문다.
- 기기 자격증명(스펙 §3.2·§3.3)은 여기 없다. tailnet-only 구성에서는 미룰 수 있고, 필요해지면 별도 계획이다.
