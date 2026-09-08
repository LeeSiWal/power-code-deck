package handlers

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/gorilla/mux"
	_ "modernc.org/sqlite"
	"powercodedeck/internal/orchestration"
)

func TestRunAPIQueuesAndRejectsClientExecutionClaims(t *testing.T) {
	database, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	database.SetMaxOpenConns(1)
	store, err := orchestration.New(database)
	if err != nil {
		t.Fatal(err)
	}
	router := mux.NewRouter()
	RegisterRunRoutes(router, store)
	path := t.TempDir()
	call := func(method, url, key string, payload any) *httptest.ResponseRecorder {
		body, _ := json.Marshal(payload)
		req := httptest.NewRequest(method, url, bytes.NewReader(body))
		req.Header.Set("Idempotency-Key", key)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		return w
	}
	body := map[string]any{"path": path, "prompt": "a task", "provider": "antigravity"}
	w := call("POST", "/v2/runs", "key", body)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var r orchestration.Run
	if err := json.Unmarshal(w.Body.Bytes(), &r); err != nil {
		t.Fatal(err)
	}
	if r.State != "queued" || len(r.Executions) != 0 {
		t.Fatal("create ran a process")
	}
	w = call("POST", "/v2/runs", "key", body)
	var again orchestration.Run
	json.Unmarshal(w.Body.Bytes(), &again)
	if again.ID != r.ID {
		t.Fatal("duplicate request created another run")
	}
	if w := call("GET", "/v2/runs/"+r.ID, "", nil); w.Code != 200 {
		t.Fatal(w.Code)
	}
	body["state"] = "succeeded"
	if w := call("POST", "/v2/runs", "other", body); w.Code != 400 {
		t.Fatal("client state accepted", w.Code)
	}
	delete(body, "state")
	if w := call("POST", "/v2/runs", "", body); w.Code != 400 {
		t.Fatal("missing idempotency accepted", w.Code)
	}
	if w := call("POST", "/v2/runs/"+r.ID+"/complete", "", nil); w.Code != 404 {
		t.Fatal("completion route exposed", w.Code)
	}
	if w := call("POST", "/v2/runs/"+r.ID+"/cancel", "", nil); w.Code != 204 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := call("GET", "/v2/runs/missing", "", nil); w.Code != 404 {
		t.Fatal(w.Code)
	}
}
