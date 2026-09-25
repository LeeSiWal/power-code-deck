package handlers

import (
	"bytes"
	"database/sql"
	"net/http/httptest"
	"testing"

	"github.com/gorilla/mux"
	"powercodedeck/internal/orchestration"
)

func TestRunApplicationRequiresStrictReviewedTarget(t *testing.T) {
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
	r, err := s.Create("apply", t.TempDir(), "not verified", "codex")
	if err != nil {
		t.Fatal(err)
	}
	worker, err := orchestration.NewWorker(s, t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	router := mux.NewRouter()
	RegisterRunRoutes(router, s, worker)
	call := func(method, body string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(method, "/v2/runs/"+r.ID+"/plan/apply", bytes.NewBufferString(body)))
		return w
	}
	for _, body := range []string{`{}`, `{"integrationId":"i","branch":"refs/heads/main","baseCommit":"b","resultCommit":"r","path":"/tmp/other"}`, `{"integrationId":"i","branch":"refs/heads/main","baseCommit":"b","resultCommit":"r"}{}`, string(bytes.Repeat([]byte("x"), 4097))} {
		if w := call("POST", body); w.Code != 400 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	if w := call("GET", ""); w.Code != 409 {
		t.Fatal("unverified preview accepted", w.Code)
	}
	if w := call("POST", `{"integrationId":"i","branch":"refs/heads/main","baseCommit":"b","resultCommit":"r"}`); w.Code != 409 {
		t.Fatal("unverified application accepted", w.Code)
	}
	got, err := s.Get(r.ID)
	if err != nil || got.State != "queued" {
		t.Fatal("invalid request mutated Run", got, err)
	}
}
