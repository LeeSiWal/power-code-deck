package handlers

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/mux"
	_ "modernc.org/sqlite"
	"powercodedeck/db"
	"powercodedeck/services"
)

func TestTeamToggleHandler(t *testing.T) {
	database, err := sql.Open("sqlite", t.TempDir()+"/agents.db")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	database.SetMaxOpenConns(1)
	if err := db.Migrate(database); err != nil {
		t.Fatal(err)
	}
	svc := services.NewAgentService(database, nil)
	a, err := svc.Create(services.CreateAgentRequest{Preset: services.AutoPreset, Name: "t", Command: "claude", WorkingDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	r := mux.NewRouter()
	r.HandleFunc("/api/agents/{id}/team", TeamModeGet(svc)).Methods("GET")
	r.HandleFunc("/api/agents/{id}/team", TeamModeSet(svc)).Methods("PUT")
	do := func(method, path, body string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(method, path, strings.NewReader(body)))
		return w
	}
	if w := do("PUT", "/api/agents/"+a.ID+"/team", `{"on":true}`); w.Code != http.StatusOK {
		t.Fatalf("put: %d %s", w.Code, w.Body)
	}
	w := do("GET", "/api/agents/"+a.ID+"/team", "")
	if w.Code != 200 || strings.TrimSpace(w.Body.String()) != `{"on":true}` {
		t.Fatalf("get: %d %s", w.Code, w.Body)
	}
	if w := do("PUT", "/api/agents/nope/team", `{"on":true}`); w.Code != http.StatusNotFound {
		t.Fatalf("unknown agent: %d", w.Code)
	}
	if w := do("GET", "/api/agents/nope/team", ""); w.Code != http.StatusNotFound {
		t.Fatalf("unknown agent get: %d", w.Code)
	}
}
