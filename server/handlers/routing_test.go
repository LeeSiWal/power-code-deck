package handlers

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gorilla/mux"
	_ "modernc.org/sqlite"
	"powercodedeck/internal/orchestration"
	"powercodedeck/internal/routing"
	"powercodedeck/internal/routing/runroute"
)

type noCLIs struct{}

func (noCLIs) LookPath(string) (string, error) { return "", errors.New("none") }
func (noCLIs) Run(context.Context, string, []string, string) (string, error) {
	return "", errors.New("none")
}

func TestRoutingRoutesContract(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "r.db")+"?_pragma=foreign_keys(ON)&_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	defer db.Close()
	runs, _ := orchestration.New(db)
	rs, _ := routing.NewStore(db)
	w, _ := orchestration.NewWorker(runs, t.TempDir(), nil)
	pol, _ := routing.LoadPolicyEvidence()
	cfg := routing.DefaultConfig()
	cfg.Mode = routing.ModeAuto
	c, err := runroute.New(runroute.Options{Config: cfg, Store: rs, Runs: runs, Worker: w, Prober: &routing.Prober{Runner: noCLIs{}}, Policy: pol})
	if err != nil {
		t.Fatal(err)
	}
	r := mux.NewRouter()
	api := r.PathPrefix("/api").Subrouter()
	RegisterRoutingRoutes(api, c)
	run, err := runs.Create("h1", t.TempDir(), "fix it", "codex")
	if err != nil {
		t.Fatal(err)
	}
	do := func(method, path, body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(method, path, strings.NewReader(body)))
		return rec
	}
	if rec := do("GET", "/api/v2/routing", ""); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"adapters"`) {
		t.Fatalf("snapshot %d %s", rec.Code, rec.Body)
	}
	// Spend opt-ins and endpoints cannot be injected from the browser.
	if rec := do("PUT", "/api/v2/runs/"+run.ID+"/routing", `{"mode":"auto","pinProfile":"","pinAdapter":"","allowApiMetered":true}`); rec.Code != 400 {
		t.Fatalf("unknown field accepted: %d", rec.Code)
	}
	if rec := do("PUT", "/api/v2/runs/"+run.ID+"/routing", `{"mode":"yolo"}`); rec.Code != 400 {
		t.Fatalf("bad mode accepted: %d", rec.Code)
	}
	if rec := do("POST", "/api/v2/runs/"+run.ID+"/routing/switch", `{"profile":"x","when":"later"}`); rec.Code != 400 {
		t.Fatalf("bad timing accepted: %d", rec.Code)
	}
	// No executor installed: 409 with the recorded decision explaining why.
	rec := do("POST", "/api/v2/runs/"+run.ID+"/routing/start", `{}`)
	var body struct {
		Error    string           `json:"error"`
		Decision routing.Decision `json:"decision"`
	}
	json.NewDecoder(rec.Body).Decode(&body)
	if rec.Code != http.StatusConflict || body.Decision.ID == "" || body.Decision.Selected != "" {
		t.Fatalf("start %d %+v", rec.Code, body)
	}
	if rec := do("GET", "/api/v2/runs/run_missing/routing", ""); rec.Code != 404 {
		t.Fatalf("missing run: %d", rec.Code)
	}
	if rec := do("GET", "/api/v2/runs/"+run.ID+"/routing", ""); rec.Code != 200 || !strings.Contains(rec.Body.String(), "no_candidate") {
		t.Fatalf("timeline %d %s", rec.Code, rec.Body)
	}
}
