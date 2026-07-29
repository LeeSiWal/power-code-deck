package ws

// native:decide + Remember=true → 규칙 저장 경로 테스트
//
// 이 테스트가 커버하는 구간:
//   - Decide 호출 전에 pending 스냅샷을 찍는다 (스냅샷 순서)
//   - ruleInput != nil 가드
//   - cwd != "" 가드
//   - ApprovalRuleStore.Save 호출
//
// ApprovalRuleStore.Save 자체는 서비스 패키지에서 충분히 검증되어 있으므로
// 이 테스트는 허브의 "배선"이 올바른지만 확인한다.
// Decide 가 pending 에서 요청을 꺼낸 뒤 스냅샷하면 ruleInput 이 nil 로 남아
// 규칙이 저장되지 않는다 — 이 테스트로 그 회귀를 잡는다.

import (
	"database/sql"
	"encoding/json"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"powercodedeck/db"
	"powercodedeck/services"
)

// bashInput은 Bash 도구 입력 JSON을 만드는 헬퍼다.
func bashInput(cmd string) json.RawMessage {
	b, _ := json.Marshal(map[string]string{"command": cmd})
	return b
}

// testRuleStore는 인메모리 SQLite 기반 ApprovalRuleStore를 만든다.
func testRuleStore(t *testing.T) *services.ApprovalRuleStore {
	t.Helper()
	conn, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	if err := db.Migrate(conn); err != nil {
		t.Fatal(err)
	}
	return services.NewApprovalRuleStore(conn)
}

// TestNativeDecideRememberSavesRule verifies that a native:decide message with
// Remember=true on a pending allow request results in a stored approval rule,
// and that the snapshot is taken BEFORE Decide resolves the request.
//
// 검증 포인트:
//  1. Remember=true + allow → ApprovalRuleStore에 규칙이 저장된다.
//  2. 스냅샷은 Decide 호출 전에 찍는다 → Decide 후에 찍으면 pending에서 이미
//     제거되어 ruleInput이 nil이 되고 규칙이 저장되지 않는다.
func TestNativeDecideRememberSavesRule(t *testing.T) {
	const (
		agentID = "agent-1"
		reqID   = "req-abc-123"
		cwd     = "/home/user/myproject"
		tool    = "Bash"
	)
	input := bashInput("go test ./...")

	// --- 준비: NativeService (실제 구현) + ApprovalRuleStore ---
	native := services.NewNativeService("http://127.0.0.1:0")

	// cwd 기반 규칙 스코핑을 위해 session policy 주입
	native.SetPolicyForTest(agentID, cwd)

	// 핸들러를 비워서 브로드캐스트 부재로 panic 나지 않게 한다
	native.SetHandlers(func(string, *services.StreamEvent) {}, func(services.PermissionRequest) {})

	store := testRuleStore(t)

	// --- Hub 구성: native + rules 만 필요하다 (engine 등은 nil이어도 된다) ---
	h := &Hub{
		native: native,
		rules:  store,
	}

	// --- Broker에 pending 요청을 심는다 (실제 Ask는 goroutine을 블록하므로
	//     answer 채널을 버퍼링해 직접 inject한다) ---
	answerCh := native.Broker().InjectPendingForTest(services.PermissionRequest{
		ID:        reqID,
		SessionID: agentID,
		ToolName:  tool,
		Input:     input,
		AskedAt:   time.Now().UTC(),
	})
	// 결정이 전달되면 채널을 소비해 goroutine 누수를 막는다
	t.Cleanup(func() {
		select {
		case <-answerCh:
		default:
		}
	})

	// --- 동작: native:decide 메시지를 허브에 전달한다 ---
	h.handleMessage(
		&Client{send: make(chan []byte, 8)},
		msg(t, EventNativeDecide, NativeDecidePayload{
			AgentID:  agentID,
			ID:       reqID,
			Behavior: "allow",
			Remember: true,
		}),
	)

	// --- 검증: 규칙이 저장됐는지 확인한다 ---
	if !store.Allows(cwd, tool, input) {
		t.Fatal("native:decide Remember=true 후 규칙이 저장되지 않았다 — " +
			"스냅샷이 Decide 이후에 찍히거나 Save 호출이 누락됐을 가능성이 있다")
	}
}

// TestNativeDecideRememberRequiresCwd verifies that the rule is NOT saved when
// the session has no cwd — the hub guards on cwd != "" before calling Save.
// cwd가 없으면 프로젝트를 특정할 수 없으므로 규칙을 저장해선 안 된다.
func TestNativeDecideRememberRequiresCwd(t *testing.T) {
	const (
		agentID = "agent-2"
		reqID   = "req-xyz-456"
		tool    = "Bash"
	)
	input := bashInput("go test ./...")

	native := services.NewNativeService("http://127.0.0.1:0")
	// cwd를 빈 문자열로 설정 — SessionCwd가 "" 를 반환한다
	native.SetPolicyForTest(agentID, "")
	native.SetHandlers(func(string, *services.StreamEvent) {}, func(services.PermissionRequest) {})

	store := testRuleStore(t)
	h := &Hub{native: native, rules: store}

	answerCh := native.Broker().InjectPendingForTest(services.PermissionRequest{
		ID:        reqID,
		SessionID: agentID,
		ToolName:  tool,
		Input:     input,
		AskedAt:   time.Now().UTC(),
	})
	t.Cleanup(func() {
		select {
		case <-answerCh:
		default:
		}
	})

	h.handleMessage(
		&Client{send: make(chan []byte, 8)},
		msg(t, EventNativeDecide, NativeDecidePayload{
			AgentID:  agentID,
			ID:       reqID,
			Behavior: "allow",
			Remember: true,
		}),
	)

	rules, err := store.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(rules) != 0 {
		t.Fatalf("cwd가 없는 세션에서 규칙이 %d개 저장됐다 — cwd != \"\" 가드가 없는 것", len(rules))
	}
}
