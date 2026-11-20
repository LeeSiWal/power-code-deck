package services

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

// Live end-to-end against the REAL codex CLI and the REAL pcd bridge binary: a
// Codex conductor calls ask_member through the pcd MCP server while the agent's
// team toggle is off, and the `refused` result must come back through MCP.
// Skipped unless RUN_CODEX_TEAM_LIVE=1 (it spends Codex quota).
//
//	cd server && go build -o /tmp/pcd-live . && \
//	  RUN_CODEX_TEAM_LIVE=1 PCD_BIN=/tmp/pcd-live go test ./services/ -run TestCodexTeamLive -v
func TestCodexTeamLive(t *testing.T) {
	if os.Getenv("RUN_CODEX_TEAM_LIVE") != "1" {
		t.Skip("set RUN_CODEX_TEAM_LIVE=1 (spends Codex quota, needs a logged-in codex)")
	}
	if findAgentCommand("codex") == "" {
		t.Skip("codex not installed")
	}
	selfPath := os.Getenv("PCD_BIN")
	if selfPath == "" {
		t.Skip("set PCD_BIN to a built pcd binary (go build -o /tmp/pcd-live .)")
	}

	h := newTeamHarness(t)
	if err := h.agents.SetTeamMode(h.agent, false); err != nil {
		t.Fatal(err)
	}
	tokens := NewApproveTokenStore()
	tok, err := tokens.Issue(h.agent)
	if err != nil {
		t.Fatal(err)
	}
	hits := make(chan string, 8)
	// Stand in for handlers.NativeTeam (importing handlers here would be a cycle).
	deck := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			SessionID string          `json:"sessionId"`
			Tool      string          `json:"tool"`
			Arguments json.RawMessage `json:"arguments"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		if !tokens.Valid(req.SessionID, strings.TrimSpace(r.Header.Get("X-PCD-Approve-Token"))) {
			hits <- "forbidden"
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		hits <- req.Tool
		var a struct {
			Member string `json:"member"`
			Task   string `json:"task"`
		}
		_ = json.Unmarshal(req.Arguments, &a)
		_ = json.NewEncoder(w).Encode(h.svc.Ask(r.Context(), TeamCall{AgentID: req.SessionID, Member: a.Member, Task: a.Task}))
	}))
	defer deck.Close()

	broker := NewPermissionBroker()
	asked := make(chan PermissionRequest, 8)
	broker.SetAskHandler(func(req PermissionRequest) {
		asked <- req
		broker.Resolve(req.ID, PermissionDecision{Behavior: "deny", Message: "live team test is read-only"})
	})
	d := NewCodexDriver(CodexConfig{
		SessionID: h.agent, Cwd: h.cwd, Broker: broker,
		SelfPath: selfPath, TeamURL: deck.URL, TeamToken: tok, ToolTimeoutSec: 120,
	})
	if err := d.Start(); err != nil {
		t.Fatalf("start real Codex app-server: %v", err)
	}
	defer d.Stop()
	if err := d.Send(`Call the MCP tool ask_member on the pcd server exactly once with member "claude" and task "say hi". ` +
		`Do not run any other tool. Then reply with the "status" field of its result.`); err != nil {
		t.Fatalf("send: %v", err)
	}

	timer := time.NewTimer(3 * time.Minute)
	defer timer.Stop()
	refusedViaMCP := false
	for done := false; !done; {
		select {
		case ev, ok := <-d.Events():
			if !ok {
				t.Fatal("Codex app-server exited before the turn finished")
			}
			if ev.Message != nil {
				for _, b := range ev.Message.Content {
					if b.Type == "tool_result" && strings.Contains(string(b.Content), "refused") {
						refusedViaMCP = true
					}
				}
			}
			done = ev.Type == StreamTypeResult
		case <-timer.C:
			t.Fatal("timed out waiting for the Codex turn")
		}
	}
	select {
	case tool := <-hits:
		if tool != "ask_member" {
			t.Fatalf("team endpoint got %q, want ask_member", tool)
		}
	default:
		t.Fatal("the team endpoint was never called")
	}
	if !refusedViaMCP {
		t.Fatal("no MCP tool_result carrying the refused status")
	}
	select {
	case req := <-asked:
		t.Fatalf("broker was asked (team tool approval should be automatic): %+v", req)
	default:
	}
}
