package handlers

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"powercodedeck/db"
	"powercodedeck/services"

	"github.com/gorilla/mux"
	_ "modernc.org/sqlite"
)

// intelligenceHandlerFixture wires the real service against a local provider that
// never answers until the caller gives up. Nothing is stubbed inside the service:
// the point of these tests is the boundary between an HTTP request and a run.
func intelligenceHandlerFixture(t *testing.T) (*services.IntelligenceService, *mux.Router) {
	t.Helper()
	database, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	database.SetMaxOpenConns(1)
	t.Cleanup(func() { database.Close() })
	if err := db.Migrate(database); err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\nfunc main() {}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(
		`INSERT INTO agents(id,preset,name,tmux_session,working_dir,command) VALUES('a1','codex-cli','n','pcd-a1',?,'codex')`,
		dir,
	); err != nil {
		t.Fatal(err)
	}

	// A provider that holds the connection open until its request context dies —
	// i.e. the slow local model this whole change exists for.
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	t.Cleanup(provider.Close)

	registry := services.NewProviderRegistry(database)
	if _, err := registry.Upsert(services.LocalProvider{
		Name: "slow", Type: "ollama", BaseURL: provider.URL, Model: "test-model",
		TimeoutMS: 60000, Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	svc := services.NewIntelligenceService(database, registry, services.NewAgentService(database, nil), nil)

	router := mux.NewRouter()
	router.HandleFunc("/intelligence/run", RunIntelligence(svc)).Methods("POST")
	router.HandleFunc("/intelligence/traces/{id}/cancel", CancelIntelligenceRun(svc)).Methods("POST")
	return svc, router
}

func postJSON(t *testing.T, router *mux.Router, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var payload []byte
	if body != nil {
		var err error
		if payload, err = json.Marshal(body); err != nil {
			t.Fatal(err)
		}
	}
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(payload))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

// The request must be answered while the local model is still thinking. Before
// this, the handler held the connection for the entire run — 38-59 seconds when it
// worked — and any hang-up in between took the run down with it.
func TestRunIntelligenceAcceptsWithoutWaitingForTheRun(t *testing.T) {
	_, router := intelligenceHandlerFixture(t)

	started := time.Now()
	rec := postJSON(t, router, "/intelligence/run", map[string]any{
		"agentId": "a1", "task": "explain main", "mode": "LOCAL_ONLY",
		"provider": "slow", "operation": "explain",
	})
	elapsed := time.Since(started)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status=%d, want 202: %s", rec.Code, rec.Body.String())
	}
	if elapsed > time.Second {
		t.Fatalf("handler waited %s for the run — it must accept and return", elapsed)
	}
	var body struct {
		Trace services.IntelligenceTrace `json:"trace"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Trace.Status != "RUNNING" || body.Trace.ID == "" {
		t.Fatalf("202 body did not describe a running trace: %#v", body.Trace)
	}
}

// Bad input is still the request's own problem, answered synchronously.
func TestRunIntelligenceRejectsInvalidRequest(t *testing.T) {
	_, router := intelligenceHandlerFixture(t)
	rec := postJSON(t, router, "/intelligence/run", map[string]any{
		"agentId": "a1", "task": "   ", "mode": "LOCAL_ONLY", "provider": "slow", "operation": "explain",
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d, want 400: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "\"trace\"") {
		t.Fatalf("rejection dropped the trace the client needs to explain itself: %s", rec.Body.String())
	}
}

func TestCancelIntelligenceRunStopsTheJob(t *testing.T) {
	svc, router := intelligenceHandlerFixture(t)
	rec := postJSON(t, router, "/intelligence/run", map[string]any{
		"agentId": "a1", "task": "explain main", "mode": "LOCAL_ONLY",
		"provider": "slow", "operation": "explain",
	})
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status=%d, want 202: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Trace services.IntelligenceTrace `json:"trace"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}

	cancel := postJSON(t, router, "/intelligence/traces/"+body.Trace.ID+"/cancel", nil)
	if cancel.Code != http.StatusNoContent {
		t.Fatalf("cancel status=%d, want 204: %s", cancel.Code, cancel.Body.String())
	}

	deadline := time.Now().Add(5 * time.Second)
	var final services.IntelligenceTrace
	for time.Now().Before(deadline) {
		trace, err := svc.Trace(body.Trace.ID)
		if err == nil {
			final = trace
			if trace.Status == "FAILED" {
				break
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	if final.Status != "FAILED" || final.ErrorCode != "LOCAL_REQUEST_CANCELED" {
		t.Fatalf("cancelled run did not end as a cancellation: %#v", final)
	}
}

func TestCancelIntelligenceRunReportsUnknownTrace(t *testing.T) {
	_, router := intelligenceHandlerFixture(t)
	rec := postJSON(t, router, "/intelligence/traces/PCD-NOPE/cancel", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status=%d, want 404", rec.Code)
	}
}
