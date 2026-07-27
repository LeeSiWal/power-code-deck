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
