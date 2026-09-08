package handlers

import (
	"bytes"
	"database/sql"
	"github.com/gorilla/mux"
	"net/http/httptest"
	"powercodedeck/internal/orchestration"
	"testing"
)

func TestRunPlanRoutesValidateAndDoNotDispatch(t *testing.T) {
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
	r, err := s.Create("plan", t.TempDir(), "task", "codex")
	if err != nil {
		t.Fatal(err)
	}
	router := mux.NewRouter()
	RegisterRunRoutes(router, s)
	call := func(method, suffix, body string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(method, "/v2/runs/"+r.ID+suffix, bytes.NewBufferString(body)))
		return w
	}
	for _, body := range []string{
		`{"concurrency":1,"tasks":[]}`,
		`{"concurrency":1,"tasks":[{"id":"a","prompt":"task","provider":"codex","state":"succeeded"}]}`,
		`{"concurrency":1,"tasks":[{"id":"a","prompt":"task","provider":"codex","dependsOn":["missing"]}]}`,
	} {
		if w := call("PUT", "/plan", body); w.Code != 400 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	valid := `{"concurrency":1,"tasks":[{"id":"a","prompt":"task","provider":"codex"}]}`
	for i := 0; i < 2; i++ {
		if w := call("PUT", "/plan", valid); w.Code != 204 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	if w := call("GET", "/plan", ""); w.Code != 200 || !bytes.Contains(w.Body.Bytes(), []byte(`"state":"pending"`)) {
		t.Fatal(w.Code, w.Body.String())
	}
	got, err := s.Get(r.ID)
	if err != nil || got.State != "planned" || len(got.Executions) != 0 {
		t.Fatal("saving plan dispatched execution", got, err)
	}
	for _, suffix := range []string{"/plan/claim", "/plan/complete", "/plan/checks"} {
		if w := call("POST", suffix, `{}`); w.Code != 404 {
			t.Fatal("internal scheduler API exposed", w.Code)
		}
	}
	if w := call("POST", "/cancel", ""); w.Code != 204 {
		t.Fatal(w.Code)
	}
}
