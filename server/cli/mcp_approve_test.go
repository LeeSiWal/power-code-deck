package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

// askServer must DENY when it cannot reach the deck. Allowing on error would run
// an unapproved tool because a pipe broke — the failure mode that turns a
// permission system into decoration.
func TestAskServerDeniesWhenUnreachable(t *testing.T) {
	t.Setenv("PCD_APPROVE_URL", "http://127.0.0.1:1/nope") // nothing listens on port 1
	got := askServer(approveRequest{ToolName: "Write"})
	if got.Behavior != "deny" {
		t.Fatalf("behavior = %q, want deny when the deck is unreachable", got.Behavior)
	}
	if got.Message == "" {
		t.Fatal("deny must carry a message — Claude reads it and adapts")
	}
}

func TestAskServerDeniesWithoutConfig(t *testing.T) {
	os.Unsetenv("PCD_APPROVE_URL")
	if got := askServer(approveRequest{ToolName: "Bash"}); got.Behavior != "deny" {
		t.Fatalf("behavior = %q, want deny with no bridge configured", got.Behavior)
	}
}

// A server answer that isn't allow/deny must not be trusted through.
func TestAskServerRejectsUnknownBehavior(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"behavior":"maybe"}`))
	}))
	defer srv.Close()
	t.Setenv("PCD_APPROVE_URL", srv.URL)
	if got := askServer(approveRequest{ToolName: "Bash"}); got.Behavior != "deny" {
		t.Fatalf("behavior = %q, want deny for an unknown decision", got.Behavior)
	}
}

// The happy path: the deck's allow (with edited input) is passed through intact.
func TestAskServerPassesAllowThrough(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-PCD-Approve-Token") != "secret-1" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		var req approveRequest
		json.NewDecoder(r.Body).Decode(&req)
		if req.ToolName != "Write" || req.SessionID != "s1" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Write([]byte(`{"behavior":"allow","updatedInput":{"file_path":"/tmp/edited"}}`))
	}))
	defer srv.Close()
	t.Setenv("PCD_APPROVE_URL", srv.URL)
	t.Setenv("PCD_APPROVE_TOKEN", "secret-1")

	got := askServer(approveRequest{SessionID: "s1", ToolName: "Write", Input: json.RawMessage(`{"file_path":"/tmp/x"}`)})
	if got.Behavior != "allow" {
		t.Fatalf("behavior = %q", got.Behavior)
	}
	// approve-with-changes: the user's edit must survive to the CLI.
	if string(got.UpdatedInput) != `{"file_path":"/tmp/edited"}` {
		t.Fatalf("updatedInput = %s", got.UpdatedInput)
	}
}

// The deck refusing the ask (bad token) must deny, not allow.
func TestAskServerDeniesOnHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "bad token", http.StatusForbidden)
	}))
	defer srv.Close()
	t.Setenv("PCD_APPROVE_URL", srv.URL)
	if got := askServer(approveRequest{ToolName: "Bash"}); got.Behavior != "deny" {
		t.Fatalf("behavior = %q, want deny on HTTP error", got.Behavior)
	}
}
