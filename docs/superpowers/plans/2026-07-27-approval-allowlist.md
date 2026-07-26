# 승인 허용 목록 ("앞으로도 허용") Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 같은 명령을 한 번 허용하면 그 프로젝트에서 다시 묻지 않는다.

**Architecture:** 규칙은 `(working_dir, tool_name, target)` 완전 일치이며 새 테이블 `approval_rules`에 저장한다. 순수 함수 `autoDecide`는 그대로 두고, DB에 닿는 규칙 검사는 이미 세션 정책을 들고 있는 `NativeService.autoDecision`에 얹는다 — 모드 정책이 먼저, 규칙이 나중이다. 위험 판정은 저장·적용 양쪽에서 수행한다.

**Tech Stack:** Go (net/http, modernc SQLite) · React 18 + TypeScript 5.7 · Zustand 4 · Tailwind 3

## Global Constraints

- 빌드/테스트: `cd server && CGO_ENABLED=0 go build ./... && go test ./...` / `cd client && ./node_modules/.bin/tsc --noEmit` (pnpm 스크립트가 아니라 바이너리 직접 호출)
- **클라이언트에 테스트 프레임워크가 없다.** vitest/jest 미설치, 테스트 파일 0개. 클라이언트 검증은 `tsc --noEmit` + 명시된 수동 확인이 전부이며 **이 계획에서 도입하지 않는다.**
- **서버는 테스트가 있다.** `services/permission_policy_test.go` 등. 서버 작업은 실제 TDD로 진행한다.
- 마이그레이션은 기존 관례를 따른다 — `db.Exec()` 비치명적 호출, 버전 테이블 없음
- 커밋 메시지: `feat(scope): 한국어 설명` / `fix(scope): …`
- `dist/pcd.exe`는 git 추적 대상이며 클라이언트 변경 시 재빌드한다 (마지막 태스크)
- 작업 브랜치: `feat/approval-allowlist` (Task 1에서 생성)

## 스펙 대비 정정 사항

스펙 §1은 choke point를 `autoDecide`로 지목했으나, 그 함수는 **DB에 접근할 수 없는 순수 함수**이고 기존 테스트가 그 순수성에 의존한다(`permission_policy_test.go`는 서비스 없이 함수를 직접 호출).

따라서 규칙 검사는 **`NativeService.autoDecision`** 에 넣는다. 이 메서드는 `autoDecide`를 호출한 직후에 위치하므로 스펙 §4가 요구한 순서(모드 정책 → 규칙)가 그대로 유지되고, 순수 함수와 그 테스트는 손대지 않는다.

이 배치의 부작용 하나: `autoDecide`가 `false`를 반환하는 모든 모드가 규칙 검사에 도달하므로, `plan` 모드를 **명시적으로 제외**해야 한다(스펙 §4의 "규칙은 모드를 이기지 않는다"). Task 3이 이를 다룬다.

## File Structure

**생성**

| 파일 | 책임 |
|---|---|
| `server/services/approval_rules.go` | `ApprovalRuleStore` — 규칙 CRUD + `target` 추출/정규화 |
| `server/services/approval_rules_test.go` | 위 단위 테스트 |
| `client/src/components/settings/ApprovalRules.tsx` | 설정의 규칙 관리 목록 |

**수정**

| 파일 | 변경 |
|---|---|
| `server/db/migrations.go` | `approval_rules` 테이블 |
| `server/services/native_service.go` | `autoDecision`에 규칙 검사, 스토어 주입 |
| `server/services/permission_policy.go` | `IsSafeToolCall` 공개 (저장 시점 검사용) |
| `server/main.go` | 스토어 생성·주입, REST 라우트 |
| `server/handlers/approval_rules.go` | 목록·삭제 핸들러 |
| `server/ws/message.go` | `NativeDecidePayload.Remember` |
| `server/ws/hub.go` | `remember` 시 규칙 저장 |
| `client/src/lib/api.ts` | 규칙 목록·삭제 |
| `client/src/components/native/NativeChat.tsx` | 승인 카드에 `항상 허용` |
| `client/src/components/control/ApprovalFeed.tsx` | 같은 버튼 |
| `client/src/pages/SettingsPage.tsx` | 규칙 관리 섹션 마운트 |

---

## Task 1: 규칙 저장소 (서버, TDD)

**Files:**
- Create: `server/services/approval_rules.go`
- Create: `server/services/approval_rules_test.go`
- Modify: `server/db/migrations.go`

**Interfaces:**
- Consumes: `database/sql`
- Produces — 이후 태스크가 이 시그니처에 의존한다:
  - `type ApprovalRule struct { ID int64; WorkingDir, ToolName, Target, CreatedAt string }`
  - `func NewApprovalRuleStore(db *sql.DB) *ApprovalRuleStore`
  - `func (s *ApprovalRuleStore) Allows(workingDir, tool string, input json.RawMessage) bool`
  - `func (s *ApprovalRuleStore) Save(workingDir, tool string, input json.RawMessage) error`
  - `func (s *ApprovalRuleStore) List() ([]ApprovalRule, error)`
  - `func (s *ApprovalRuleStore) Delete(id int64) error`
  - `func RuleTarget(tool string, input json.RawMessage, cwd string) (string, bool)`

- [ ] **Step 1: 브랜치를 만든다**

```bash
cd /home/siwal/code/power-code-deck
git checkout -b feat/approval-allowlist
```

- [ ] **Step 2: 마이그레이션을 추가한다**

`server/db/migrations.go`의 `Migrate()` 안, 기존 `ALTER TABLE agents` 루프 **다음**에 삽입한다.

```go
	// 승인 허용 목록. 에이전트가 아니라 프로젝트(작업 디렉토리)에 속하므로 외래키를
	// 걸지 않는다 — 세션을 지우고 다시 만들어도 규칙은 살아남아야 한다.
	// UNIQUE가 중복 저장을 막는다(INSERT OR IGNORE와 짝).
	db.Exec(`CREATE TABLE IF NOT EXISTS approval_rules (
		id          INTEGER PRIMARY KEY AUTOINCREMENT,
		working_dir TEXT NOT NULL,
		tool_name   TEXT NOT NULL,
		target      TEXT NOT NULL DEFAULT '',
		created_at  TEXT DEFAULT (datetime('now')),
		UNIQUE(working_dir, tool_name, target)
	)`)
```

- [ ] **Step 3: 실패하는 테스트를 쓴다**

`server/services/approval_rules_test.go`를 새로 만든다. 기존 `permission_policy_test.go`의 `bash()` / `edit()` 헬퍼가 같은 패키지에 있으므로 그대로 쓴다.

```go
package services

import (
	"database/sql"
	"encoding/json"
	"testing"

	_ "modernc.org/sqlite"

	"powercodedeck/db"
)

func ruleStore(t *testing.T) *ApprovalRuleStore {
	t.Helper()
	conn, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	if err := db.Migrate(conn); err != nil {
		t.Fatal(err)
	}
	return NewApprovalRuleStore(conn)
}

// 규칙이 없으면 사람에게 물어야 한다 — 조회 결과가 없다고 허용으로 넘어가면
// 게이트가 통째로 열린다.
func TestAllowsIsFalseWithoutRule(t *testing.T) {
	s := ruleStore(t)
	if s.Allows("/home/u/p", "Bash", bash("go test ./...")) {
		t.Fatal("저장된 규칙이 없는데 허용됐다")
	}
}

func TestSaveThenAllowsExactCommand(t *testing.T) {
	s := ruleStore(t)
	if err := s.Save("/home/u/p", "Bash", bash("go test ./...")); err != nil {
		t.Fatal(err)
	}
	if !s.Allows("/home/u/p", "Bash", bash("go test ./...")) {
		t.Fatal("저장한 것과 같은 명령이 허용되지 않았다")
	}
}

// 완전 일치가 설계 결정이다. 인자가 다른 호출은 여전히 물어야 한다.
func TestDifferentArgsStillAsk(t *testing.T) {
	s := ruleStore(t)
	s.Save("/home/u/p", "Bash", bash("go test ./..."))
	if s.Allows("/home/u/p", "Bash", bash("go test ./services/")) {
		t.Fatal("인자가 다른 명령까지 허용됐다 — 완전 일치가 아니다")
	}
}

// 정규화는 공백 정리까지만. 이게 없으면 사람이 같다고 보는 명령이 안 걸린다.
func TestWhitespaceNormalized(t *testing.T) {
	s := ruleStore(t)
	s.Save("/home/u/p", "Bash", bash("go test ./..."))
	if !s.Allows("/home/u/p", "Bash", bash("  go   test   ./...  ")) {
		t.Fatal("공백만 다른 같은 명령이 걸리지 않았다")
	}
}

// 신뢰는 레포 단위다. 다른 프로젝트의 규칙이 새면 안 된다.
func TestRuleIsScopedToProject(t *testing.T) {
	s := ruleStore(t)
	s.Save("/home/u/p", "Bash", bash("go test ./..."))
	if s.Allows("/home/u/other", "Bash", bash("go test ./...")) {
		t.Fatal("다른 작업 디렉토리에 규칙이 적용됐다")
	}
}

// 위험 명령은 저장 자체가 거부돼야 한다. 저장되면 승인 게이트의 존재 이유가 사라진다.
func TestSaveRefusesDangerousCommand(t *testing.T) {
	s := ruleStore(t)
	if err := s.Save("/home/u/p", "Bash", bash("rm -rf /tmp/x")); err == nil {
		t.Fatal("위험 명령이 규칙으로 저장됐다")
	}
	if s.Allows("/home/u/p", "Bash", bash("rm -rf /tmp/x")) {
		t.Fatal("위험 명령이 허용됐다")
	}
}

// 저장 시점 검사만으로는 부족하다: 나중에 위험 목록이 넓어지면 이미 저장된 규칙이
// 그 확장을 우회한다. 그래서 적용 시점에도 다시 판정한다.
func TestAllowsRechecksSafetyAtApplyTime(t *testing.T) {
	s := ruleStore(t)
	// 안전 판정을 우회해 직접 넣는다 — 과거에 저장됐던 규칙을 재현한다.
	if _, err := s.db.Exec(
		`INSERT INTO approval_rules (working_dir, tool_name, target) VALUES (?, ?, ?)`,
		"/home/u/p", "Bash", "sudo reboot",
	); err != nil {
		t.Fatal(err)
	}
	if s.Allows("/home/u/p", "Bash", bash("sudo reboot")) {
		t.Fatal("저장돼 있던 위험 규칙이 적용 시점에 걸러지지 않았다")
	}
}

// 대상을 뽑을 수 없으면 저장하지 않는다. 빈 target으로 저장하면 그 도구 전체를
// 허용하는 규칙이 되어 사용자가 의도한 것보다 훨씬 넓어진다.
func TestSaveRefusesWhenTargetMissing(t *testing.T) {
	s := ruleStore(t)
	if err := s.Save("/home/u/p", "Bash", json.RawMessage(`{}`)); err == nil {
		t.Fatal("command가 없는 Bash 입력이 저장됐다")
	}
}

func TestEditRuleUsesCleanedAbsolutePath(t *testing.T) {
	s := ruleStore(t)
	if err := s.Save("/home/u/p", "Write", edit("/home/u/p/a.txt")); err != nil {
		t.Fatal(err)
	}
	if !s.Allows("/home/u/p", "Write", edit("/home/u/p/./a.txt")) {
		t.Fatal("경로 정리 후 같은 파일이 걸리지 않았다")
	}
}

func TestListAndDelete(t *testing.T) {
	s := ruleStore(t)
	s.Save("/home/u/p", "Bash", bash("go test ./..."))
	rules, err := s.List()
	if err != nil || len(rules) != 1 {
		t.Fatalf("List = %v, err=%v; want 1 rule", rules, err)
	}
	if err := s.Delete(rules[0].ID); err != nil {
		t.Fatal(err)
	}
	if s.Allows("/home/u/p", "Bash", bash("go test ./...")) {
		t.Fatal("삭제한 규칙이 여전히 허용한다")
	}
}

// 같은 규칙을 두 번 저장해도 조용히 넘어가야 한다(UNIQUE + INSERT OR IGNORE).
func TestSaveIsIdempotent(t *testing.T) {
	s := ruleStore(t)
	s.Save("/home/u/p", "Bash", bash("go test ./..."))
	if err := s.Save("/home/u/p", "Bash", bash("go test ./...")); err != nil {
		t.Fatalf("중복 저장이 에러가 됐다: %v", err)
	}
	rules, _ := s.List()
	if len(rules) != 1 {
		t.Fatalf("중복 저장으로 규칙이 %d개가 됐다", len(rules))
	}
}
```

- [ ] **Step 4: 테스트가 실패하는지 확인한다**

```bash
cd /home/siwal/code/power-code-deck/server && go test ./services/ -run "Rule|Allows|Save" 2>&1 | head -20
```

Expected: 컴파일 실패 — `undefined: ApprovalRuleStore`, `undefined: NewApprovalRuleStore`

- [ ] **Step 5: 저장소를 구현한다**

`server/services/approval_rules.go`를 새로 만든다.

```go
package services

import (
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
)

// ApprovalRuleStore는 "앞으로도 허용" 규칙을 보관한다.
//
// 규칙은 (작업 디렉토리, 도구, 대상) 완전 일치다. 패턴을 쓰지 않는 이유는 예측
// 가능성이다 — 규칙이 무엇을 허용할지 예측할 수 없으면 감사할 수도 없다. 실제
// 성가심의 대부분은 반복되는 동일 명령(go test ./..., npm run build)이고, 인자가
// 매번 다른 호출(git commit -m "…")은 오히려 확인해야 하는 쪽이다.
type ApprovalRuleStore struct {
	db *sql.DB
}

type ApprovalRule struct {
	ID         int64  `json:"id"`
	WorkingDir string `json:"workingDir"`
	ToolName   string `json:"toolName"`
	Target     string `json:"target"`
	CreatedAt  string `json:"createdAt"`
}

// ErrUnsafeRule은 위험 판정된 호출을 규칙으로 저장하려 할 때 반환된다.
var ErrUnsafeRule = errors.New("위험한 호출은 규칙으로 저장할 수 없습니다")

// ErrNoRuleTarget은 입력에서 대상을 뽑지 못했을 때 반환된다.
var ErrNoRuleTarget = errors.New("규칙 대상을 확인할 수 없습니다")

func NewApprovalRuleStore(db *sql.DB) *ApprovalRuleStore {
	return &ApprovalRuleStore{db: db}
}

// RuleTarget은 이 호출을 식별하는 문자열을 뽑는다. 두 번째 반환값이 false면 규칙을
// 만들 수 없다 — 빈 대상으로 저장하면 그 도구 전체를 허용하는 규칙이 되어 사용자가
// 의도한 것보다 훨씬 넓어진다.
//
// 정규화는 최소한만 한다: 셸 명령은 공백 정리, 경로는 절대경로 + Clean. 그 이상
// 영리해질수록 무엇이 허용되는지 예측하기 어려워진다.
func RuleTarget(tool string, input json.RawMessage, cwd string) (string, bool) {
	switch tool {
	case "Bash", "BashOutput":
		cmd := firstStringField(input, "command")
		cmd = strings.Join(strings.Fields(cmd), " ")
		if cmd == "" {
			return "", false
		}
		return cmd, true
	case "Write", "Edit", "MultiEdit", "NotebookEdit":
		p := firstStringField(input, "file_path", "notebook_path", "path")
		if p == "" {
			return "", false
		}
		if !filepath.IsAbs(p) {
			p = filepath.Join(cwd, p)
		}
		return filepath.Clean(p), true
	}
	// 대상 개념이 없는 도구는 도구 이름 자체가 최소 단위다.
	return "", true
}

// Allows는 저장된 규칙이 이 호출을 허용하는지 본다.
//
// 규칙이 있어도 안전 판정을 다시 한다. 저장 시점 검사만 두면, 나중에 위험 목록이
// 넓어졌을 때 이미 저장된 규칙이 그 확장을 조용히 우회한다.
func (s *ApprovalRuleStore) Allows(workingDir, tool string, input json.RawMessage) bool {
	dir := filepath.Clean(workingDir)
	if !IsSafeToolCall(tool, input, dir) {
		return false
	}
	target, ok := RuleTarget(tool, input, dir)
	if !ok {
		return false
	}
	var n int
	err := s.db.QueryRow(
		`SELECT COUNT(*) FROM approval_rules WHERE working_dir = ? AND tool_name = ? AND target = ?`,
		dir, tool, target,
	).Scan(&n)
	// 조회 실패는 "규칙 없음"으로 처리한다 — 사람에게 묻는 쪽으로 실패한다.
	return err == nil && n > 0
}

func (s *ApprovalRuleStore) Save(workingDir, tool string, input json.RawMessage) error {
	dir := filepath.Clean(workingDir)
	if !IsSafeToolCall(tool, input, dir) {
		return ErrUnsafeRule
	}
	target, ok := RuleTarget(tool, input, dir)
	if !ok {
		return ErrNoRuleTarget
	}
	_, err := s.db.Exec(
		`INSERT OR IGNORE INTO approval_rules (working_dir, tool_name, target) VALUES (?, ?, ?)`,
		dir, tool, target,
	)
	return err
}

func (s *ApprovalRuleStore) List() ([]ApprovalRule, error) {
	rows, err := s.db.Query(
		`SELECT id, working_dir, tool_name, target, COALESCE(created_at, '')
		   FROM approval_rules ORDER BY working_dir, tool_name, target`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ApprovalRule{}
	for rows.Next() {
		var r ApprovalRule
		if err := rows.Scan(&r.ID, &r.WorkingDir, &r.ToolName, &r.Target, &r.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *ApprovalRuleStore) Delete(id int64) error {
	_, err := s.db.Exec(`DELETE FROM approval_rules WHERE id = ?`, id)
	return err
}
```

- [ ] **Step 6: `isSafeToolCall`을 공개한다**

`server/services/permission_policy.go`에서 함수 이름을 `isSafeToolCall` → `IsSafeToolCall`로 바꾸고, 같은 파일 안의 호출부(`autoDecide` 내부)도 함께 바꾼다. `permission_policy_test.go`에 호출부가 있으면 그것도 바꾼다.

```bash
cd /home/siwal/code/power-code-deck/server
grep -rn "isSafeToolCall" services/
```

나온 모든 지점을 `IsSafeToolCall`로 바꾼다. 공개하는 이유를 선언부 주석 첫 줄에 덧붙인다:

```go
// IsSafeToolCall classifies a call as safe-to-auto-approve for "auto" mode. Exported
// because ApprovalRuleStore applies the same judgement when saving and when matching a
// rule — a permanent rule for a dangerous call would defeat the approval gate.
```

- [ ] **Step 7: 테스트가 통과하는지 확인한다**

```bash
cd /home/siwal/code/power-code-deck/server && go test ./services/ -v -run "Rule|Allows|Save" 2>&1 | tail -30
```

Expected: 신규 10개 전부 PASS

- [ ] **Step 8: 전체 스위트로 회귀를 확인한다**

```bash
cd /home/siwal/code/power-code-deck/server && CGO_ENABLED=0 go build ./... && go test ./...
```

Expected: 전부 PASS. 특히 `permission_policy_test.go`가 이름 변경 후에도 통과해야 한다.

- [ ] **Step 9: 커밋한다**

```bash
git add server/db/migrations.go server/services/approval_rules.go server/services/approval_rules_test.go server/services/permission_policy.go
git commit -m "feat(approval): 허용 목록 저장소

(working_dir, tool, target) 완전 일치 규칙을 approval_rules 테이블에 보관한다.
패턴이 아닌 완전 일치인 이유는 예측 가능성이다 — 무엇이 허용될지 예측할 수
없는 규칙은 감사할 수 없다.

위험 판정은 저장과 적용 양쪽에서 한다. 저장 시점만 검사하면 나중에 위험 목록이
넓어졌을 때 이미 저장된 규칙이 그 확장을 우회한다. isSafeToolCall을 공개해
같은 판단을 재사용한다."
```

---

## Task 2: 결정 경로에 규칙 검사 연결 (서버, TDD)

**Files:**
- Modify: `server/services/native_service.go`
- Test: `server/services/approval_rules_test.go` (추가)

**Interfaces:**
- Consumes: Task 1의 `ApprovalRuleStore.Allows`
- Produces: `func (s *NativeService) SetApprovalRules(store *ApprovalRuleStore)`

- [ ] **Step 1: 실패하는 테스트를 쓴다**

`server/services/approval_rules_test.go` 맨 아래에 추가한다.

```go
// 규칙은 모드를 이기지 않는다. plan 모드의 약속은 "실행하지 않는다"이고, 규칙이
// 그것을 뒤집으면 모드 자체를 신뢰할 수 없게 된다.
func TestRuleDoesNotOverridePlanMode(t *testing.T) {
	store := ruleStore(t)
	store.Save("/tmp/proj", "Bash", bash("go test ./..."))

	s := NewNativeService("http://127.0.0.1:0")
	s.SetApprovalRules(store)
	s.mu.Lock()
	s.policies["a1"] = sessionPolicy{mode: "plan", cwd: "/tmp/proj"}
	s.mu.Unlock()

	if _, ok := s.autoDecision(PermissionRequest{
		SessionID: "a1", ToolName: "Bash", Input: bash("go test ./..."),
	}); ok {
		t.Fatal("plan 모드에서 규칙이 실행을 허용했다")
	}
}

// 수동(기본) 모드에서 규칙이 있으면 사람을 거치지 않는다 — 이 기능의 목적이다.
func TestRuleAutoAllowsInManualMode(t *testing.T) {
	store := ruleStore(t)
	store.Save("/tmp/proj", "Bash", bash("go test ./..."))

	s := NewNativeService("http://127.0.0.1:0")
	s.SetApprovalRules(store)
	s.mu.Lock()
	s.policies["a1"] = sessionPolicy{mode: "", cwd: "/tmp/proj"}
	s.mu.Unlock()

	d, ok := s.autoDecision(PermissionRequest{
		SessionID: "a1", ToolName: "Bash", Input: bash("go test ./..."),
	})
	if !ok || d.Behavior != "allow" {
		t.Fatalf("규칙이 있는데 허용되지 않았다: ok=%v d=%+v", ok, d)
	}
}

// 규칙이 없는 호출은 그대로 사람에게 간다.
func TestUnmatchedCallStillAsks(t *testing.T) {
	store := ruleStore(t)
	store.Save("/tmp/proj", "Bash", bash("go test ./..."))

	s := NewNativeService("http://127.0.0.1:0")
	s.SetApprovalRules(store)
	s.mu.Lock()
	s.policies["a1"] = sessionPolicy{mode: "", cwd: "/tmp/proj"}
	s.mu.Unlock()

	if _, ok := s.autoDecision(PermissionRequest{
		SessionID: "a1", ToolName: "Bash", Input: bash("npm publish"),
	}); ok {
		t.Fatal("규칙이 없는 호출이 자동 결정됐다")
	}
}

// 스토어가 주입되지 않은 배포에서도 동작해야 한다(nil 안전).
func TestNilStoreDoesNotPanic(t *testing.T) {
	s := NewNativeService("http://127.0.0.1:0")
	s.mu.Lock()
	s.policies["a1"] = sessionPolicy{mode: "", cwd: "/tmp/proj"}
	s.mu.Unlock()
	if _, ok := s.autoDecision(PermissionRequest{
		SessionID: "a1", ToolName: "Bash", Input: bash("ls"),
	}); ok {
		t.Fatal("스토어 없이 자동 결정됐다")
	}
}
```

- [ ] **Step 2: 테스트가 실패하는지 확인한다**

```bash
cd /home/siwal/code/power-code-deck/server && go test ./services/ -run "TestRule|TestUnmatched|TestNilStore" 2>&1 | head
```

Expected: 컴파일 실패 — `s.SetApprovalRules undefined`

- [ ] **Step 3: 스토어 필드와 주입 메서드를 추가한다**

`server/services/native_service.go`의 `NativeService` 구조체에서 `policies` 필드 **바로 아래**에 추가한다.

```go
	// rules는 "앞으로도 허용" 규칙이다. nil일 수 있다 — 주입되지 않은 배포에서는
	// 규칙 검사가 통째로 생략되고 기존 동작이 그대로 유지된다.
	rules *ApprovalRuleStore
```

같은 파일 아무 곳(다른 Set* 메서드 근처)에 추가한다.

```go
// SetApprovalRules wires the persistent allowlist. Injected like the other stores so
// this service keeps owning processes, not rows.
func (s *NativeService) SetApprovalRules(store *ApprovalRuleStore) {
	s.mu.Lock()
	s.rules = store
	s.mu.Unlock()
}
```

- [ ] **Step 4: `autoDecision`에 규칙 검사를 얹는다**

`server/services/native_service.go`의 `autoDecision`을 아래로 교체한다.

```go
func (s *NativeService) autoDecision(req PermissionRequest) (PermissionDecision, bool) {
	s.mu.RLock()
	pol, ok := s.policies[req.SessionID]
	rules := s.rules
	s.mu.RUnlock()
	if !ok {
		return PermissionDecision{}, false
	}
	if d, decided := autoDecide(pol.mode, req.ToolName, req.Input, pol.cwd); decided {
		return d, true
	}
	// 모드 정책이 결정하지 못했을 때만 규칙을 본다. 순서가 중요하다 — 규칙이 모드보다
	// 먼저 오면 plan 모드에서도 발동해 "실행하지 않는다"는 약속이 깨진다.
	//
	// plan은 그래서 명시적으로 제외한다: autoDecide는 plan에서 false를 반환하므로
	// 이 지점에 도달하지만, 규칙이 그 모드를 뒤집어서는 안 된다.
	if rules == nil || pol.mode == PlanMode {
		return PermissionDecision{}, false
	}
	if rules.Allows(pol.cwd, req.ToolName, req.Input) {
		return PermissionDecision{Behavior: "allow"}, true
	}
	return PermissionDecision{}, false
}
```

- [ ] **Step 5: `PlanMode` 상수를 추가한다**

`server/services/permission_policy.go`의 기존 상수 블록에 더한다.

```go
const (
	AutoMode   = "auto"
	BypassMode = "bypassPermissions"
	// PlanMode는 탐색만 하고 실행하지 않는 모드다. 허용 목록 규칙이 이 약속을
	// 뒤집지 않도록 이름을 상수로 둔다.
	PlanMode = "plan"
)
```

기존 블록에 이미 `AutoMode`/`BypassMode`가 있으므로 `PlanMode` 줄과 그 주석만 추가하면 된다.

- [ ] **Step 6: 테스트가 통과하는지 확인한다**

```bash
cd /home/siwal/code/power-code-deck/server && go test ./services/ -v -run "TestRule|TestUnmatched|TestNilStore" 2>&1 | tail -15
```

Expected: 4개 전부 PASS

- [ ] **Step 7: 전체 스위트를 돌린다**

```bash
cd /home/siwal/code/power-code-deck/server && go test ./... && go test -race ./services/
```

Expected: 전부 PASS. 특히 `TestBypassPolicySurvivesRestartWindow` 등 기존 권한 테스트가 통과해야 한다.

- [ ] **Step 8: 커밋한다**

```bash
git add server/services/native_service.go server/services/permission_policy.go server/services/approval_rules_test.go
git commit -m "feat(approval): 결정 경로에 규칙 검사 연결

autoDecision이 모드 정책 다음에 규칙을 본다. 순서가 중요하다 — 규칙이 모드보다
먼저 오면 plan 모드에서도 발동해 '실행하지 않는다'는 약속이 깨진다. plan은
명시적으로 제외한다.

스토어는 nil일 수 있고 그때는 검사가 생략돼 기존 동작이 그대로 유지된다."
```

---

## Task 3: 저장 경로 — `remember` 전달과 REST (서버)

**Files:**
- Modify: `server/ws/message.go`
- Modify: `server/ws/hub.go`
- Create: `server/handlers/approval_rules.go`
- Modify: `server/main.go`

**Interfaces:**
- Consumes: Task 1의 `ApprovalRuleStore`, Task 2의 `SetApprovalRules`
- Produces:
  - WS: `NativeDecidePayload.Remember bool` (`json:"remember,omitempty"`)
  - REST: `GET /api/approval-rules` → `[]ApprovalRule`, `DELETE /api/approval-rules/{id}`

- [ ] **Step 1: 페이로드에 필드를 더한다**

`server/ws/message.go`의 `NativeDecidePayload`를 아래로 교체한다.

```go
type NativeDecidePayload struct {
	AgentID      string          `json:"agentId"`
	ID           string          `json:"id"`
	Behavior     string          `json:"behavior"`
	UpdatedInput json.RawMessage `json:"updatedInput,omitempty"`
	Message      string          `json:"message,omitempty"`
	// Remember는 "앞으로도 허용"이다. omitempty라 낡은 클라이언트는 영향받지 않는다.
	// 허용일 때만 의미가 있으며, 서버가 저장 전에 안전 판정을 다시 확인한다.
	Remember bool `json:"remember,omitempty"`
}
```

- [ ] **Step 2: hub에 스토어를 붙인다**

`server/ws/hub.go`의 `Hub` 구조체에 필드를 더한다(기존 `activity` 필드 근처).

```go
	rules *services.ApprovalRuleStore
```

주입 메서드를 `SetActivityManager` 근처에 추가한다.

```go
// SetApprovalRules wires the allowlist so a "항상 허용" decision can persist.
func (h *Hub) SetApprovalRules(store *services.ApprovalRuleStore) {
	h.rules = store
}
```

`encoding/json`은 `ws/hub.go:6`에 이미 import돼 있다.

- [ ] **Step 3: cwd 조회 경로를 만든다**

승인 요청에는 `cwd`가 실려오지 않는다. `NativeService`가 `policies[sessionID].cwd`로 들고 있으므로 노출한다. `server/services/native_service.go`에 추가한다.

```go
// SessionCwd reports a session's working directory, which the approval rule store
// needs to scope a rule to its project. Empty when the session is unknown.
func (s *NativeService) SessionCwd(sessionID string) string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.policies[sessionID].cwd
}
```

`NativeService.Broker()`는 `services/native_service.go:133`에 이미 있으므로 그대로 쓴다.

- [ ] **Step 4: 결정 핸들러에서 규칙을 저장한다**

`server/ws/hub.go`의 `EventNativeDecide` 핸들러에서 기존 `ok := h.native.Decide(...)` 부터 `h.NoteAgentChange(payload.AgentID)` 까지를 아래로 교체한다.

`Resolve`가 pending에서 요청을 지우므로 **결정 전에** 도구 이름과 입력을 스냅샷해야 한다 — 결정 후에는 무엇을 저장할지 알 수 없다.

```go
		// "항상 허용"이면 규칙으로 남긴다. Resolve가 pending에서 요청을 지우므로
		// 도구 이름과 입력을 결정 전에 스냅샷해 둔다.
		var ruleTool string
		var ruleInput json.RawMessage
		if payload.Remember && payload.Behavior == "allow" && h.rules != nil && h.native != nil {
			for _, p := range h.native.Broker().Pending(payload.AgentID) {
				if p.ID == payload.ID {
					ruleTool, ruleInput = p.ToolName, p.Input
					break
				}
			}
		}

		ok := h.native.Decide(payload.ID, services.PermissionDecision{
			Behavior:     payload.Behavior,
			UpdatedInput: payload.UpdatedInput,
			Message:      payload.Message,
		})
		result := "already_resolved"
		if ok {
			result = "accepted"
			// 저장 실패는 승인 자체를 실패시키지 않는다 — 부가 기능이 주 기능을
			// 막으면 안 된다. 안전 판정 재확인은 ApprovalRuleStore.Save가 한다.
			if ruleInput != nil {
				if cwd := h.native.SessionCwd(payload.AgentID); cwd != "" {
					if err := h.rules.Save(cwd, ruleTool, ruleInput); err != nil {
						log.Printf("approval rule: not saved: %v", err)
					}
				}
			}
			h.BroadcastAll(EventApprovalResolved, ApprovalResolvedPayload{
				RequestID: payload.ID,
				AgentID:   payload.AgentID,
				Result:    "accepted",
			})
			h.NoteAgentChange(payload.AgentID)
		}
```

- [ ] **Step 5: REST 핸들러를 만든다**

`server/handlers/approval_rules.go`를 새로 만든다.

```go
package handlers

import (
	"net/http"
	"strconv"

	"github.com/gorilla/mux"

	"powercodedeck/services"
)

// ListApprovalRules returns every saved "항상 허용" rule. Saved permissions the user
// cannot see become a liability over time, so this list is part of the feature, not
// an extra.
func ListApprovalRules(store *services.ApprovalRuleStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rules, err := store.List()
		if err != nil {
			http.Error(w, "failed to list rules", http.StatusInternalServerError)
			return
		}
		jsonResponse(w, rules)
	}
}

func DeleteApprovalRule(store *services.ApprovalRuleStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(mux.Vars(r)["id"], 10, 64)
		if err != nil {
			http.Error(w, "bad id", http.StatusBadRequest)
			return
		}
		if err := store.Delete(id); err != nil {
			http.Error(w, "failed to delete rule", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
```

`jsonResponse`는 `handlers/helpers.go:8`에 이미 있다.

- [ ] **Step 6: main.go에서 배선한다**

`server/main.go`에서 다른 서비스 생성부 근처에 스토어를 만들고 주입한다.

```go
	approvalRules := services.NewApprovalRuleStore(database)
```

DB 변수는 `database`다(`main.go:54`, `db.Init(cfg.DBPath)`). 네이티브 서비스와 hub 변수의 실제 이름은 그 파일에서 확인해 맞추고, 두 곳에 주입한다 — `SetApprovalRules(approvalRules)`를 네이티브 서비스와 hub 각각에.

라우트는 기존 `/notifications` 등록부(`main.go:253` 부근)에 더한다.

```go
	api.HandleFunc("/approval-rules", handlers.ListApprovalRules(approvalRules)).Methods("GET")
	api.HandleFunc("/approval-rules/{id}", handlers.DeleteApprovalRule(approvalRules)).Methods("DELETE")
```

- [ ] **Step 7: 빌드와 테스트**

```bash
cd /home/siwal/code/power-code-deck/server && CGO_ENABLED=0 go build ./... && go test ./... && go vet ./ws/ ./handlers/ ./services/
```

Expected: 전부 통과

- [ ] **Step 8: 커밋한다**

```bash
git add server/ws/message.go server/ws/hub.go server/services/native_service.go server/handlers/approval_rules.go server/main.go
git commit -m "feat(approval): remember 전달과 규칙 REST

native:decide에 remember를 실어 '항상 허용'을 저장한다. Resolve가 pending에서
요청을 지우므로 도구 이름과 입력을 결정 전에 스냅샷한다.

저장 실패는 승인을 실패시키지 않는다 — 부가 기능이 주 기능을 막으면 안 된다.

GET /api/approval-rules, DELETE /api/approval-rules/{id} — 저장한 권한을 볼 수
없으면 시간이 지나며 위험 요소가 되므로 관리 경로는 이 기능의 일부다."
```

---

## Task 4: 승인 카드에 "항상 허용" (클라이언트)

**Files:**
- Modify: `client/src/components/native/NativeChat.tsx`
- Modify: `client/src/components/control/ApprovalFeed.tsx`

**Interfaces:**
- Consumes: Task 3의 `remember` 필드
- Produces: 없음 (UI만)

- [ ] **Step 1: `NativeChat`의 `decide`가 remember를 받게 한다**

`client/src/components/native/NativeChat.tsx:433` 의 `decide`를 교체한다.

```tsx
  const decide = useCallback((id: string, behavior: 'allow' | 'deny', message?: string, remember?: boolean) => {
    agentDeckWS.send('native:decide', { agentId, id, behavior, message, remember });
    setPending((prev) => prev.filter((p) => p.id !== id));
  }, [agentId]);
```

- [ ] **Step 2: `ApprovalCard`에 버튼을 더한다**

`NativeChat.tsx`의 `ApprovalCard` 안, `허용` 버튼 바로 뒤에 넣는다. `onDecide`의 시그니처가 Step 1과 맞아야 한다.

```tsx
        <button
          onClick={() => onDecide(req.id, 'allow', undefined, true)}
          className="flex-1 py-2.5 rounded-lg bg-green-500/10 text-green-300 text-sm font-medium outline-none focus:ring-2 focus:ring-green-300"
          title="이 프로젝트에서 같은 호출을 다시 묻지 않습니다"
        >
          항상 허용
        </button>
```

`ApprovalCard`의 `onDecide` prop 타입도 함께 넓힌다.

```tsx
  onDecide: (id: string, behavior: 'allow' | 'deny', message?: string, remember?: boolean) => void;
```

- [ ] **Step 3: 무엇이 저장되는지 보여준다**

같은 카드의 버튼 줄 **아래**에 한 줄을 더한다. 무엇이 허용되는지 모르고 누르는 버튼은 신뢰할 수 없는 결정이 된다.

```tsx
      <div className="text-[10px] text-deck-text-faint">
        "항상 허용"은 이 프로젝트에서 <span className="font-mono">{req.toolName}</span> 의 같은 호출만 통과시킵니다.
      </div>
```

- [ ] **Step 4: 관제실 피드에도 같은 버튼을 넣는다**

`client/src/components/control/ApprovalFeed.tsx`의 `허용` 버튼 바로 뒤에 넣는다.

```tsx
        <button
          onClick={() => onDecide(a, 'allow', true)}
          className="px-2.5 py-1 rounded text-[10px] font-mono border border-deck-accent/60 text-deck-accent-light active:opacity-80"
          title="이 프로젝트에서 같은 호출을 다시 묻지 않습니다"
        >
          항상 허용
        </button>
```

`ApprovalFeed`의 `onDecide` prop 타입을 넓히고, `ControlRoomPage`의 `decide` 함수도 맞춘다.

`ApprovalFeed.tsx`:
```tsx
  onDecide: (a: PendingApproval, behavior: 'allow' | 'deny', remember?: boolean) => void;
```

`client/src/pages/ControlRoomPage.tsx`의 `decide`:
```tsx
  function decide(a: PendingApproval, behavior: 'allow' | 'deny', remember?: boolean) {
    agentDeckWS.send('native:decide', { agentId: a.agentId, id: a.requestId, behavior, remember });
    useAppStore.getState().removeApproval(a.requestId);
  }
```

- [ ] **Step 5: 타입 검사**

```bash
cd /home/siwal/code/power-code-deck/client && ./node_modules/.bin/tsc --noEmit
```

Expected: 에러 0건

- [ ] **Step 6: 커밋한다**

```bash
git add client/src/components/native/NativeChat.tsx client/src/components/control/ApprovalFeed.tsx client/src/pages/ControlRoomPage.tsx
git commit -m "feat(approval): 승인 카드에 '항상 허용'

세션 카드와 관제실 피드 양쪽에 넣는다 — 승인은 어느 쪽에서든 처리할 수 있어야
하고, 한쪽에만 있으면 '왜 여기선 안 되지'가 된다.

무엇이 저장되는지 카드에 적는다. 무엇을 허용하는지 모르고 누르는 버튼은
신뢰할 수 없는 결정이다."
```

---

## Task 5: 규칙 관리 화면 (클라이언트)

**Files:**
- Create: `client/src/components/settings/ApprovalRules.tsx`
- Modify: `client/src/lib/api.ts`
- Modify: `client/src/pages/SettingsPage.tsx`

**Interfaces:**
- Consumes: Task 3의 `GET /api/approval-rules`, `DELETE /api/approval-rules/{id}`
- Produces: `<ApprovalRules />`

- [ ] **Step 1: API 메서드를 더한다**

`client/src/lib/api.ts`의 `listNotifications` 근처에 추가한다.

```ts
  // Approval rules ("항상 허용")
  listApprovalRules: () => apiFetch<any[]>('/approval-rules'),
  deleteApprovalRule: (id: number) => apiFetch(`/approval-rules/${id}`, { method: 'DELETE' }),
```

- [ ] **Step 2: 컴포넌트를 만든다**

`client/src/components/settings/ApprovalRules.tsx`를 새로 만든다. 디렉토리가 없으면 만든다.

```tsx
import { useCallback, useEffect, useState } from 'react';
import { api } from '../../lib/api';
import { IconTrash } from '../icons';

/**
 * ApprovalRules — the list of "항상 허용" rules.
 *
 * Saved permissions the user cannot see become a liability over time, so this screen
 * is part of the feature rather than an extra: every rule that silences an approval
 * prompt must be visible and removable here.
 */

interface ApprovalRule {
  id: number;
  workingDir: string;
  toolName: string;
  target: string;
  createdAt: string;
}

export function ApprovalRules() {
  const [rules, setRules] = useState<ApprovalRule[]>([]);
  const [loading, setLoading] = useState(true);

  const load = useCallback(async () => {
    setLoading(true);
    try {
      const data = await api.listApprovalRules();
      setRules(Array.isArray(data) ? (data as ApprovalRule[]) : []);
    } catch (err) {
      console.error('Failed to load approval rules:', err);
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => { load(); }, [load]);

  const remove = useCallback(async (id: number) => {
    try {
      await api.deleteApprovalRule(id);
      setRules((prev) => prev.filter((r) => r.id !== id));
    } catch (err) {
      console.error('Failed to delete approval rule:', err);
    }
  }, []);

  // Group by project: trust is formed per repo, so that is how the list should read.
  const byDir = rules.reduce<Record<string, ApprovalRule[]>>((acc, r) => {
    (acc[r.workingDir] ||= []).push(r);
    return acc;
  }, {});

  return (
    <section className="space-y-2">
      <h3 className="text-sm font-medium">항상 허용 규칙</h3>
      {loading ? (
        <div className="text-xs text-deck-text-dim">불러오는 중…</div>
      ) : rules.length === 0 ? (
        <div className="text-xs text-deck-text-dim">
          저장된 규칙이 없습니다. 승인 카드에서 "항상 허용"을 누르면 여기에 쌓입니다.
        </div>
      ) : (
        Object.entries(byDir).map(([dir, list]) => (
          <div key={dir} className="rounded-lg border border-deck-border">
            <div className="px-3 py-1.5 text-[11px] font-mono text-deck-text-dim border-b border-deck-border/50 truncate">
              {dir}
            </div>
            {list.map((r) => (
              <div key={r.id} className="flex items-center gap-2 px-3 py-1.5 text-xs">
                <span className="font-mono text-deck-accent-light shrink-0">{r.toolName}</span>
                <span className="font-mono text-deck-text-dim truncate flex-1">{r.target || '(대상 없음)'}</span>
                <button
                  onClick={() => remove(r.id)}
                  className="shrink-0 p-1 rounded text-deck-text-dim hover:text-red-400"
                  title="이 규칙 삭제"
                >
                  <IconTrash size={13} />
                </button>
              </div>
            ))}
          </div>
        ))
      )}
    </section>
  );
}
```

- [ ] **Step 3: 설정 화면에 마운트한다**

`client/src/pages/SettingsPage.tsx`에서 기존 섹션들이 나열된 곳에 추가한다.

```tsx
import { ApprovalRules } from '../components/settings/ApprovalRules';
```

그리고 본문의 마지막 섹션 뒤에:

```tsx
        <ApprovalRules />
```

- [ ] **Step 4: 타입 검사**

```bash
cd /home/siwal/code/power-code-deck/client && ./node_modules/.bin/tsc --noEmit
```

Expected: 에러 0건

- [ ] **Step 5: 커밋한다**

```bash
git add client/src/components/settings/ApprovalRules.tsx client/src/lib/api.ts client/src/pages/SettingsPage.tsx
git commit -m "feat(approval): 설정에 규칙 관리 화면

프로젝트별로 묶어 도구·대상을 보여주고 개별 삭제. 저장한 권한을 볼 수 없으면
시간이 지나며 위험 요소가 되므로 관리 화면은 이 기능의 일부다."
```

---

## Task 6: 검증 · 문서 · 바이너리

**Files:**
- Modify: `CHANGELOG.md`, `ROADMAP.md`
- Modify: `dist/pcd.exe`

- [ ] **Step 1: 전체 검증**

```bash
cd /home/siwal/code/power-code-deck/server && CGO_ENABLED=0 go build ./... && go test ./... && go test -race ./services/
cd ../client && ./node_modules/.bin/tsc --noEmit
```

Expected: 전부 통과

- [ ] **Step 2: 실제 렌더 확인**

앱을 띄우고 아래를 확인한다. **하나라도 실패하면 해당 태스크로 돌아간다.**

- `go test ./...` 승인 카드에서 `항상 허용` → 같은 명령을 다시 실행하면 **카드가 안 뜨는지**
- 인자가 다른 명령(`go test ./services/`)은 **여전히 묻는지**
- `rm -rf /tmp/x` 승인 카드에 `항상 허용` 버튼이 있더라도 눌렀을 때 규칙이 **저장되지 않는지**(설정 목록에 안 나타남). 서버 로그에 `approval rule: not saved` 가 찍힌다
- 플랜 모드에서 규칙이 있는 명령이 **여전히 실행되지 않는지**
- 설정에서 규칙이 프로젝트별로 보이고, 삭제 후 **다시 묻는지**
- 관제실 승인 피드의 `항상 허용`도 동작하는지

- [ ] **Step 3: 문서를 갱신한다**

`CHANGELOG.md`의 최상단(`## v0.5.0` 위)에 새 섹션을 만든다.

```markdown
## v0.6.0 — 승인 허용 목록

### Added
- **"항상 허용"** — 같은 명령을 한 번 허용하면 그 프로젝트에서 다시 묻지 않습니다. 규칙은 작업 디렉토리·도구·대상의 완전 일치이며, 설정에서 확인하고 지울 수 있습니다.
  - 패턴이 아니라 완전 일치인 이유는 예측 가능성입니다. 무엇이 허용될지 예측할 수 없는 규칙은 감사할 수 없습니다. 실제 성가심의 대부분은 반복되는 동일 명령이고, 인자가 매번 다른 호출은 오히려 확인해야 하는 쪽입니다.
  - **위험한 호출은 규칙으로 저장되지 않습니다.** 한 번 허용은 되지만 영구 규칙은 만들 수 없습니다 — `rm`·`sudo`·`git push`에 영구 규칙을 허용하면 승인 게이트의 존재 이유가 사라집니다. 이 판정은 저장할 때와 적용할 때 양쪽에서 합니다.
  - **규칙은 모드를 이기지 않습니다.** 플랜 모드에서는 규칙이 있어도 실행하지 않습니다.
```

`ROADMAP.md`에서 `### 2. 승인 정책 — "앞으로도 허용"` 섹션을 `## 완료됨`의 v0.6.0 항목으로 옮기고, 남은 항목 번호를 다시 매긴다.

- [ ] **Step 4: 바이너리를 재빌드한다**

```bash
cd /home/siwal/code/power-code-deck && make build-windows && cp -f pcd.exe dist/pcd.exe
```

- [ ] **Step 5: 커밋한다**

```bash
git add CHANGELOG.md ROADMAP.md dist/pcd.exe
git commit -m "docs: 승인 허용 목록 반영 + pcd.exe 재빌드"
```

---

## Self-Review

**1. 스펙 커버리지**

| 스펙 섹션 | 태스크 |
|---|---|
| §1 규칙 키 · 정규화 | Task 1 (`RuleTarget`, 테스트 4종) |
| §2 프로젝트 범위 | Task 1 (`TestRuleIsScopedToProject`) |
| §3 위험 명령 저장 불가 (양쪽 검사) | Task 1 (`TestSaveRefusesDangerous`, `TestAllowsRechecksSafetyAtApplyTime`) |
| §4 결정 흐름 · 모드 우선 · plan 제외 | Task 2 |
| §5 저장소 스키마 | Task 1 Step 2 |
| §6 UI (카드 + 설명 + 관리 화면) | Task 4, Task 5 |
| §7 전송 (`Remember`) | Task 3 |
| §8 에러 처리 | Task 1(조회 실패=규칙 없음, target 없음=저장 안 함), Task 3(저장 실패가 승인을 막지 않음) |
| §9 검증 | Task 6 |

누락 없음. **단, 스펙 §1이 지목한 choke point(`autoDecide`)는 순수 함수라 DB에 닿을 수 없어 `autoDecision`으로 옮겼다** — 위 "스펙 대비 정정 사항" 참고. 순서(모드 → 규칙)는 그대로다.

**2. 플레이스홀더 스캔:** 없음. 초안에 있던 "이대로 쓰지 않는다" 중간 조각은 제거하고 Task 3을 최종 코드 하나로 정리했다. 구현자가 확인해야 했던 세 가지(`Broker()` 존재, `jsonResponse` 이름, `json` import)는 계획 작성 중 확인해 본문에 확정값으로 적었다.

**3. 타입 일관성:** `onDecide`가 두 컴포넌트에서 시그니처가 다르다 — `NativeChat`은 `(id, behavior, message?, remember?)`, `ApprovalFeed`는 `(approval, behavior, remember?)`. 각 컴포넌트의 기존 시그니처를 따른 것이며 Task 4에 양쪽 모두 명시했다. `IsSafeToolCall`은 Task 1 Step 6에서 공개되고 Task 1·2가 그 이름을 쓴다.

## 알려진 한계

- **클라이언트 자동 테스트가 없다.** Task 4·5의 안전망은 `tsc --noEmit`과 Task 6 Step 2의 수동 확인뿐이다.
- **위험 판정이 `isSafeToolCall`의 정확도에 의존한다.** 그 분류기가 안전하다고 본 명령은 영구 규칙이 될 수 있다. 분류기가 넓어지면 규칙도 넓어지므로, 적용 시점 재검사(Task 1)가 그 변화를 따라가는 장치다.
