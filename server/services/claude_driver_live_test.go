package services

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// Live end-to-end against the REAL claude CLI and the REAL pcd bridge binary.
// Skipped unless RUN_CLAUDE_LIVE=1 (it spends tokens and needs a signed-in CLI).
//
//	cd server && go build -o /tmp/pcd-live . && \
//	  RUN_CLAUDE_LIVE=1 PCD_BIN=/tmp/pcd-live go test ./services/ -run TestClaudeDriverLive -v
//
// This is the test that matters: it proves the whole native path — spawn, stream,
// user input, the MCP bridge, and a human decision actually stopping a tool.
func TestClaudeDriverLive(t *testing.T) {
	if os.Getenv("RUN_CLAUDE_LIVE") != "1" {
		t.Skip("set RUN_CLAUDE_LIVE=1 (spends tokens, needs a logged-in claude)")
	}
	if _, err := exec.LookPath("claude"); err != nil && findAgentCommand("claude") == "" {
		t.Skip("claude not installed")
	}
	selfPath := os.Getenv("PCD_BIN")
	if selfPath == "" {
		t.Skip("set PCD_BIN to a built pcd binary (go build -o /tmp/pcd-live .)")
	}

	broker := NewPermissionBroker()
	tokens := NewApproveTokenStore()
	tok, err := tokens.Issue("live-1")
	if err != nil {
		t.Fatal(err)
	}

	// Stand in for the deck's HTTP endpoint: park the ask on the broker and wait,
	// exactly like handlers.NativeApprove does.
	deck := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			SessionID string          `json:"sessionId"`
			ToolName  string          `json:"toolName"`
			Input     json.RawMessage `json:"input"`
			ToolUseID string          `json:"toolUseId"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		if !tokens.Valid(req.SessionID, r.Header.Get("X-PCD-Approve-Token")) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		d, err := broker.Ask(PermissionRequest{
			ID: req.ToolUseID, SessionID: req.SessionID, ToolName: req.ToolName, Input: req.Input,
		}, r.Context().Done())
		if err != nil {
			d = PermissionDecision{Behavior: "deny", Message: "cancelled"}
		}
		json.NewEncoder(w).Encode(d)
	}))
	defer deck.Close()

	// Answer every prompt with a deny carrying a distinctive message, so we can
	// prove OUR decision reached Claude rather than some default.
	const denyMsg = "PCD-LIVE-DENY: the deck user said no."
	asked := make(chan PermissionRequest, 4)
	broker.SetAskHandler(func(req PermissionRequest) {
		asked <- req
		go broker.Resolve(req.ID, PermissionDecision{Behavior: "deny", Message: denyMsg})
	})

	dir := t.TempDir()
	d := NewClaudeDriver(ClaudeConfig{
		SessionID: "live-1", Cwd: dir, SelfPath: selfPath,
		ApproveURL: deck.URL, ApproveToken: tok,
	})
	if err := d.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer d.Stop()

	if err := d.Send("Create a file named live.txt containing HI in the current directory using the Write tool."); err != nil {
		t.Fatalf("send: %v", err)
	}

	var sawInit, sawToolUse, sawResult bool
	var bridgeConnected bool
	var resultText string
	var denials []PermissionDenial
	var toolResults []string
	deadline := time.After(120 * time.Second)

loop:
	for {
		select {
		case ev, ok := <-d.Events():
			if !ok {
				break loop
			}
			switch {
			case ev.Type == StreamTypeSystem && ev.Subtype == "init":
				sawInit = true
				for _, m := range ev.MCPServers {
					if m.Name == "pcd" && m.Status == "connected" {
						bridgeConnected = true
					}
				}
			case ev.Type == StreamTypeAssistant && ev.Message != nil:
				for _, b := range ev.Message.Content {
					if b.Type == "tool_use" && b.Name == "Write" {
						sawToolUse = true
					}
				}
			case ev.Type == StreamTypeUser && ev.Message != nil:
				for _, b := range ev.Message.Content {
					if b.Type == "tool_result" {
						toolResults = append(toolResults, string(b.Content))
					}
				}
			case ev.Type == StreamTypeResult:
				sawResult = true
				resultText = ev.Result
				denials = ev.PermissionDenial
				break loop
			}
		case <-deadline:
			t.Fatal("timed out waiting for the turn to finish")
		}
	}

	if !sawInit {
		t.Error("never saw system/init")
	}
	if !bridgeConnected {
		t.Error("the pcd MCP bridge did not report connected — permissions would be silently denied")
	}
	if !sawToolUse {
		t.Error("Claude never tried the Write tool")
	}
	if !sawResult {
		t.Error("never saw a result event")
	}

	// The bridge must have asked us, with the real tool and our session.
	select {
	case req := <-asked:
		if req.ToolName != "Write" || req.SessionID != "live-1" {
			t.Errorf("asked about %+v", req)
		}
	default:
		t.Error("the bridge never asked for permission")
	}

	// Our deny must have taken effect. Assert on the PROTOCOL, not on Claude's
	// prose: it paraphrases ("The write was denied — I won't retry it"), so
	// grepping the result text for our wording is a flaky test of the model's
	// phrasing rather than of our permission gate.
	if _, err := os.Stat(dir + "/live.txt"); err == nil {
		t.Error("live.txt was created despite a deny — the permission gate did nothing")
	}
	// The refusal is recorded against the tool that was blocked.
	if len(denials) != 1 || denials[0].ToolName != "Write" {
		t.Errorf("permission_denials = %+v, want exactly the blocked Write", denials)
	}
	// And OUR message — not some default — is what came back as the tool's result.
	var sawOurReason bool
	for _, tr := range toolResults {
		if strings.Contains(tr, "PCD-LIVE-DENY") {
			sawOurReason = true
		}
	}
	if !sawOurReason {
		t.Errorf("our deny message never reached Claude as a tool_result; got %q", toolResults)
	}
	_ = resultText
	if d.ClaudeSessionID() == "" {
		t.Error("no claude session id learned from init — resume would be impossible")
	}
}
