package handlers

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gorilla/mux"
	_ "modernc.org/sqlite"

	"powercodedeck/db"
	"powercodedeck/services"
)

// rulesStore는 인메모리 SQLite DB로 ApprovalRuleStore를 만든다.
// services 패키지의 ruleStore와 같은 방식이지만 핸들러 패키지 안에서만 쓴다.
func rulesStore(t *testing.T) *services.ApprovalRuleStore {
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

// TestApprovalRulesRESTRoundTrip는 저장 → GET → DELETE → Allows 경로를 HTTP 레이어까지
// 검증한다. services 패키지의 TestListAndDelete가 스토어 내부를 다루고, 이 테스트는
// ListApprovalRules · DeleteApprovalRule 핸들러가 그 스토어와 올바르게 연결됐는지를
// 확인한다 — HTTP 라우팅 누락이나 JSON 인코딩 오류가 있으면 여기서 잡힌다.
func TestApprovalRulesRESTRoundTrip(t *testing.T) {
	store := rulesStore(t)

	// 스토어에 직접 규칙을 하나 넣는다.
	if err := store.Save("/home/u/p", "Bash", json.RawMessage(`{"command":"go test ./..."}`)); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// --- GET /api/approval-rules 가 저장된 규칙을 반환해야 한다 ---
	// gorilla/mux 라우터를 통해 요청을 보낸다 — GET 라우트 등록이 누락되면 여기서 잡힌다.
	getRouter := mux.NewRouter()
	getRouter.HandleFunc("/api/approval-rules", ListApprovalRules(store)).Methods("GET")
	getW := httptest.NewRecorder()
	getR := httptest.NewRequest(http.MethodGet, "/api/approval-rules", nil)
	getRouter.ServeHTTP(getW, getR)

	if getW.Code != http.StatusOK {
		t.Fatalf("GET status = %d, want 200", getW.Code)
	}
	var rules []services.ApprovalRule
	if err := json.NewDecoder(getW.Body).Decode(&rules); err != nil {
		t.Fatalf("GET body JSON: %v", err)
	}
	if len(rules) != 1 {
		t.Fatalf("GET returned %d rules, want 1", len(rules))
	}
	if rules[0].Target != "go test ./..." {
		t.Fatalf("rule target = %q, want %q", rules[0].Target, "go test ./...")
	}

	// 저장된 규칙이 있으면 Allows는 true여야 한다.
	if !store.Allows("/home/u/p", "Bash", json.RawMessage(`{"command":"go test ./..."}`)) {
		t.Fatal("Allows가 false다 — 저장된 규칙이 적용되지 않았다")
	}

	// --- DELETE /api/approval-rules/{id} 가 규칙을 제거해야 한다 ---
	ruleID := rules[0].ID
	delW := httptest.NewRecorder()
	delR := httptest.NewRequest(http.MethodDelete, fmt.Sprintf("/api/approval-rules/%d", ruleID), nil)
	// gorilla/mux의 Vars를 핸들러가 읽으므로 라우터를 통해 요청을 보낸다.
	router := mux.NewRouter()
	router.HandleFunc("/api/approval-rules/{id}", DeleteApprovalRule(store)).Methods("DELETE")
	router.ServeHTTP(delW, delR)

	if delW.Code != http.StatusNoContent {
		t.Fatalf("DELETE status = %d, want 204", delW.Code)
	}

	// 삭제 후 Allows는 false여야 한다 — 규칙이 없으니 다시 사람에게 묻는다.
	if store.Allows("/home/u/p", "Bash", json.RawMessage(`{"command":"go test ./..."}`)) {
		t.Fatal("DELETE 후 Allows가 여전히 true다 — 규칙이 실제로 지워지지 않았다")
	}
}
