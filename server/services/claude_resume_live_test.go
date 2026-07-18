package services

import (
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"
)

// Live: does --resume actually carry the conversation across processes?
//
// Two sessions: the first is told a secret word, then dies. The second starts with
// --resume on the first's session id and is asked to recall it. If resume works,
// the word comes back; if it doesn't, we'd be lying to the user about continuity.
//
//	cd server && go build -o /tmp/pcd-live . && \
//	  RUN_CLAUDE_LIVE=1 PCD_BIN=/tmp/pcd-live go test ./services/ -run TestClaudeResumeLive -v
func TestClaudeResumeLive(t *testing.T) {
	if os.Getenv("RUN_CLAUDE_LIVE") != "1" {
		t.Skip("set RUN_CLAUDE_LIVE=1 (spends tokens, needs a logged-in claude)")
	}
	if _, err := exec.LookPath("claude"); err != nil && findAgentCommand("claude") == "" {
		t.Skip("claude not installed")
	}
	selfPath := os.Getenv("PCD_BIN")
	if selfPath == "" {
		t.Skip("set PCD_BIN to a built pcd binary")
	}

	deck, tok := liveApprovalDeck(t, "resume-1")
	dir := t.TempDir()

	newDriver := func(resume string) *ClaudeDriver {
		return NewClaudeDriver(ClaudeConfig{
			SessionID: "resume-1", Cwd: dir, SelfPath: selfPath,
			ApproveURL: deck, ApproveToken: tok, ResumeID: resume,
		})
	}

	// --- session 1: plant a word, remember the conversation id ---
	d1 := newDriver("")
	if err := d1.Start(); err != nil {
		t.Fatalf("start 1: %v", err)
	}
	if err := d1.Send("Remember this word for later: PINEAPPLE-42. Reply with just: OK"); err != nil {
		t.Fatalf("send 1: %v", err)
	}
	waitForResult(t, d1, 90*time.Second)
	claudeSession := d1.ClaudeSessionID()
	d1.Stop()
	if claudeSession == "" {
		t.Fatal("no claude session id learned — nothing to resume")
	}
	t.Logf("session 1 conversation id: %s", claudeSession)

	// --- session 2: a NEW process, resuming that conversation ---
	d2 := newDriver(claudeSession)
	if err := d2.Start(); err != nil {
		t.Fatalf("start 2 (resume): %v", err)
	}
	defer d2.Stop()
	if err := d2.Send("What word did I ask you to remember? Reply with just the word."); err != nil {
		t.Fatalf("send 2: %v", err)
	}
	answer := waitForResult(t, d2, 90*time.Second)

	if !strings.Contains(strings.ToUpper(answer), "PINEAPPLE-42") {
		t.Fatalf("resumed session did not recall the word; answer was: %.200s", answer)
	}
}

// waitForResult drains events until the turn ends, returning the result text.
func waitForResult(t *testing.T, d *ClaudeDriver, timeout time.Duration) string {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case ev, ok := <-d.Events():
			if !ok {
				return ""
			}
			if ev.Type == StreamTypeResult {
				return ev.Result
			}
		case <-deadline:
			t.Fatal("timed out waiting for the turn to finish")
			return ""
		}
	}
}

// liveApprovalDeck stands up the deck side of the permission bridge, auto-allowing
// everything (these tests are about resume, not approvals).
func liveApprovalDeck(t *testing.T, sessionID string) (url, token string) {
	t.Helper()
	broker := NewPermissionBroker()
	tokens := NewApproveTokenStore()
	tok, err := tokens.Issue(sessionID)
	if err != nil {
		t.Fatal(err)
	}
	var once sync.Once
	_ = once
	broker.SetAskHandler(func(req PermissionRequest) {
		go broker.Resolve(req.ID, PermissionDecision{Behavior: "allow"})
	})
	srv := newApproveServer(broker, tokens)
	t.Cleanup(srv.Close)
	return srv.URL, tok
}
