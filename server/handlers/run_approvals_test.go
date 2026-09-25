package handlers

import (
	"bytes"
	"database/sql"
	"github.com/gorilla/mux"
	"net/http/httptest"
	"powercodedeck/internal/orchestration"
	"powercodedeck/internal/orchestration/taskgraph"
	"powercodedeck/services"
	"testing"
)

func TestPlannedApprovalsAreScopedToRunningAttempts(t *testing.T) {
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
	r, err := s.Create("plan", t.TempDir(), "plan", "codex")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SavePlan(r.ID, orchestration.TaskPlan{Concurrency: 2, Tasks: []taskgraph.Task{{ID: "a", Provider: "codex", Prompt: "a"}, {ID: "b", Provider: "claude", Prompt: "b"}}}); err != nil {
		t.Fatal(err)
	}
	tasks, err := s.ClaimTasks(r.ID)
	if err != nil {
		t.Fatal(err)
	}
	broker := services.NewPermissionBroker()
	broker.InjectPendingForTest(services.PermissionRequest{ID: "a-tool", SessionID: tasks[0].AttemptID, ToolName: "Edit"})
	broker.InjectPendingForTest(services.PermissionRequest{ID: "b-tool", SessionID: tasks[1].AttemptID, ToolName: "Edit"})
	broker.InjectPendingForTest(services.PermissionRequest{ID: "other-tool", SessionID: "outside", ToolName: "Edit"})
	router := mux.NewRouter()
	RegisterRunApprovalRoutes(router, s, broker)
	call := func(method, body string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(method, "/v2/runs/"+r.ID+"/approvals", bytes.NewBufferString(body)))
		return w
	}
	w := call("GET", "")
	if w.Code != 200 || !bytes.Contains(w.Body.Bytes(), []byte("a-tool")) || !bytes.Contains(w.Body.Bytes(), []byte("b-tool")) || bytes.Contains(w.Body.Bytes(), []byte("other-tool")) {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := call("POST", `{"id":"other-tool","behavior":"allow"}`); w.Code != 409 {
		t.Fatal("foreign request accepted", w.Code)
	}
	if err := s.FinishPlannedAttempt(tasks[0].AttemptID, true, "done"); err != nil {
		t.Fatal(err)
	}
	if w := call("POST", `{"id":"a-tool","behavior":"allow"}`); w.Code != 409 {
		t.Fatal("finished attempt accepted", w.Code)
	}
	if w := call("POST", `{"id":"b-tool","behavior":"deny"}`); w.Code != 204 {
		t.Fatal(w.Code, w.Body.String())
	}
	if err := s.Cancel(r.ID); err != nil {
		t.Fatal(err)
	}
	if w := call("GET", ""); w.Code != 409 {
		t.Fatal("canceled plan exposed requests", w.Code)
	}
}

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
