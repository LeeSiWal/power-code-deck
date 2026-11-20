package handlers

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/mux"
	_ "modernc.org/sqlite"
	"powercodedeck/db"
	"powercodedeck/internal/providers"
	"powercodedeck/services"
)

// hangExec is a member execution that never answers until its context ends.
type hangExec struct{ stopped atomic.Bool }

func (f *hangExec) Identity() providers.Identity         { return providers.Identity{} }
func (f *hangExec) Capabilities() providers.Capabilities { return providers.Capabilities{} }
func (f *hangExec) Start() error                         { return nil }
func (f *hangExec) Send(string) error                    { return nil }
func (f *hangExec) Interrupt() error                     { return nil }
func (f *hangExec) ConversationID() string               { return "" }
func (f *hangExec) Stop()                                { f.stopped.Store(true) }
func (f *hangExec) SetPermissionMode(string) error       { return nil }
func (f *hangExec) Next(ctx context.Context) (providers.Event, error) {
	<-ctx.Done()
	return providers.Event{}, ctx.Err()
}

type teamEnv struct {
	agents *services.AgentService
	team   *services.TeamService
	tokens services.ApproveTokenStore
	agent  string
	token  string
	execs  chan *hangExec
}

func newTeamEnv(t *testing.T) *teamEnv {
	t.Helper()
	database, err := sql.Open("sqlite", t.TempDir()+"/agents.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	database.SetMaxOpenConns(1)
	if err := db.Migrate(database); err != nil {
		t.Fatal(err)
	}
	tokens := services.NewApproveTokenStore()
	e := &teamEnv{agents: services.NewAgentService(database, nil), tokens: tokens, execs: make(chan *hangExec, 4)}
	cwd := t.TempDir()
	a, err := e.agents.Create(services.CreateAgentRequest{Preset: services.AutoPreset, Name: "t", Command: "claude", WorkingDir: cwd})
	if err != nil {
		t.Fatal(err)
	}
	e.agent = a.ID
	if err := e.agents.SetTeamMode(a.ID, true); err != nil {
		t.Fatal(err)
	}
	e.token, _ = tokens.Issue(a.ID)
	e.team = services.NewTeamService(services.TeamDeps{
		DB: database, Agents: e.agents,
		NewExec: func(providers.ID, string, string, string, string, string) (providers.Execution, error) {
			x := &hangExec{}
			e.execs <- x
			return x, nil
		},
		Conductor: func(string) (string, string, string, bool) { return "claude", "claude-opus-5-5", cwd, true },
	})
	return e
}

func (e *teamEnv) post(ctx context.Context, token, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPost, "/internal/native/team", bytes.NewBufferString(body)).WithContext(ctx)
	r.Header.Set("X-PCD-Approve-Token", token)
	w := httptest.NewRecorder()
	NativeTeam(e.team, e.agents, e.tokens)(w, r)
	return w
}

func TestTeamHandlerTokenRequired(t *testing.T) {
	e := newTeamEnv(t)
	w := e.post(context.Background(), "wrong", `{"sessionId":"`+e.agent+`","tool":"ask_member","arguments":{"member":"codex","task":"x"}}`)
	if w.Code != http.StatusForbidden {
		t.Fatalf("code = %d, want 403", w.Code)
	}
}

func TestTeamHandlerCancelStopsMember(t *testing.T) {
	e := newTeamEnv(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		done <- e.post(ctx, e.token, `{"sessionId":"`+e.agent+`","tool":"ask_member","arguments":{"member":"codex","task":"x"}}`)
	}()
	var x *hangExec
	select {
	case x = <-e.execs:
	case <-time.After(2 * time.Second):
		t.Fatal("member never started")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("handler did not return after cancel")
	}
	deadline := time.Now().Add(2 * time.Second)
	for !x.stopped.Load() && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if !x.stopped.Load() {
		t.Fatal("member execution was not stopped")
	}
}

func TestTeamHandlerHandoff(t *testing.T) {
	e := newTeamEnv(t)
	body := `{"sessionId":"` + e.agent + `","tool":"handoff_to_agy","arguments":{"task":"이미지"}}`
	var got struct{ Status, Text string }
	w := e.post(context.Background(), e.token, body)
	json.Unmarshal(w.Body.Bytes(), &got)
	if w.Code != 200 || got.Status != "handed_off" || !strings.Contains(got.Text, "agy 카드") {
		t.Fatalf("on: %d %s", w.Code, w.Body)
	}
	e.agents.SetTeamMode(e.agent, false)
	w = e.post(context.Background(), e.token, body)
	json.Unmarshal(w.Body.Bytes(), &got)
	if got.Status != "refused" {
		t.Fatalf("off: %s", w.Body)
	}
	if w := e.post(context.Background(), e.token, `{"sessionId":"`+e.agent+`","tool":"nope","arguments":{}}`); w.Code != http.StatusBadRequest {
		t.Fatalf("unknown tool: %d", w.Code)
	}
}

func TestTeamResultAndRaiseRoutes(t *testing.T) {
	e := newTeamEnv(t)
	r := mux.NewRouter()
	r.HandleFunc("/api/agents/{id}/team/results/{callId}", TeamResultGet(e.team)).Methods("GET")
	r.HandleFunc("/api/agents/{id}/team/raise", TeamRaise(e.team)).Methods("POST")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/api/agents/"+e.agent+"/team/results/tm_x", nil))
	if w.Code != http.StatusNotFound {
		t.Fatalf("missing result: %d", w.Code)
	}
	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("POST", "/api/agents/"+e.agent+"/team/raise", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("raise: %d", w.Code)
	}
}

func TestNativeApproveAutoAllowsTeamTools(t *testing.T) {
	broker := services.NewPermissionBroker()
	tokens := services.NewApproveTokenStore()
	tok, _ := tokens.Issue("s1")
	h := NativeApprove(broker, tokens)
	for _, tool := range []string{"mcp__pcd__ask_member", "mcp__pcd__handoff_to_agy"} {
		done := make(chan *httptest.ResponseRecorder, 1)
		go func() {
			done <- post(t, h, tok, `{"sessionId":"s1","toolName":"`+tool+`","toolUseId":"toolu_`+tool+`","input":{"task":"x"}}`)
		}()
		select {
		case w := <-done:
			var got services.PermissionDecision
			json.Unmarshal(w.Body.Bytes(), &got)
			if got.Behavior != "allow" || string(got.UpdatedInput) != `{"task":"x"}` {
				t.Fatalf("%s: %s", tool, w.Body)
			}
		case <-time.After(time.Second):
			t.Fatalf("%s reached the broker (still waiting)", tool)
		}
		if n := len(broker.Pending("s1")); n != 0 {
			t.Fatalf("%s: %d pending", tool, n)
		}
	}
}
