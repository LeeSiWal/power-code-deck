package handlers

import (
	"bytes"
	"database/sql"
	"github.com/gorilla/mux"
	"net/http/httptest"
	"powercodedeck/internal/orchestration"
	"powercodedeck/services"
	"testing"
)

func TestRunApprovalsRejectCrossRunAndStaleDecisions(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	s, err := orchestration.New(db)
	if err != nil {
		t.Fatal(err)
	}
	a, err := s.Create("a", t.TempDir(), "task a", "codex")
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.Create("b", t.TempDir(), "task b", "claude")
	if err != nil {
		t.Fatal(err)
	}
	aExec, err := s.StartAttempt(a.ID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.StartAttempt(b.ID)
	if err != nil {
		t.Fatal(err)
	}
	broker := services.NewPermissionBroker()
	answer := broker.InjectPendingForTest(services.PermissionRequest{ID: "tool-a", SessionID: aExec, ToolName: "Edit"})
	router := mux.NewRouter()
	RegisterRunApprovalRoutes(router, s, broker)
	call := func(method, id, body string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(method, "/v2/runs/"+id+"/approvals", bytes.NewBufferString(body)))
		return w
	}
	if w := call("GET", a.ID, ""); w.Code != 200 || !bytes.Contains(w.Body.Bytes(), []byte("tool-a")) {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := call("GET", b.ID, ""); bytes.Contains(w.Body.Bytes(), []byte("tool-a")) {
		t.Fatal("cross-run disclosure")
	}
	for _, body := range []string{`{"id":"tool-a","behavior":"always"}`, `{"id":"tool-a","behavior":"allow","extra":true}`, `{"id":"tool-a","behavior":"allow"}{}`} {
		if w := call("POST", a.ID, body); w.Code != 400 {
			t.Fatal("invalid decision accepted", w.Code)
		}
	}
	if w := call("POST", b.ID, `{"id":"tool-a","behavior":"allow"}`); w.Code != 409 {
		t.Fatal("cross-run decision", w.Code)
	}
	if w := call("POST", a.ID, `{"id":"tool-a","behavior":"deny"}`); w.Code != 204 {
		t.Fatal(w.Code)
	}
	if decision := <-answer; decision.Behavior != "deny" {
		t.Fatal(decision)
	}
	if w := call("POST", a.ID, `{"id":"tool-a","behavior":"allow"}`); w.Code != 409 {
		t.Fatal("stale decision", w.Code)
	}
	if err := s.Cancel(a.ID); err != nil {
		t.Fatal(err)
	}
	if w := call("GET", a.ID, ""); w.Code != 409 {
		t.Fatal("canceled run approvals", w.Code)
	}
}
