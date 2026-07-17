package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"powercodedeck/services"
)

func post(t *testing.T, h http.HandlerFunc, token, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, "/internal/native/approve", bytes.NewBufferString(body))
	r.Header.Set("X-PCD-Approve-Token", token)
	w := httptest.NewRecorder()
	h(w, r)
	return w
}

// The endpoint must hold the response open until a human answers — that block is
// what pauses Claude. If it returned early with a default, we'd be inventing an
// answer the user never gave.
func TestApproveBlocksUntilAnswered(t *testing.T) {
	broker := services.NewPermissionBroker()
	tokens := services.NewApproveTokenStore()
	tok, _ := tokens.Issue("s1")
	h := NativeApprove(broker, tokens)

	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		done <- post(t, h, tok, `{"sessionId":"s1","toolName":"Write","toolUseId":"toolu_1","input":{"file_path":"/tmp/x"}}`)
	}()

	// Wait for it to register, then confirm it is still waiting.
	deadline := time.Now().Add(time.Second)
	for len(broker.Pending("s1")) == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	select {
	case <-done:
		t.Fatal("handler answered before the user did")
	case <-time.After(50 * time.Millisecond):
	}

	broker.Resolve("toolu_1", services.PermissionDecision{Behavior: "allow"})

	select {
	case w := <-done:
		var got services.PermissionDecision
		json.Unmarshal(w.Body.Bytes(), &got)
		if got.Behavior != "allow" {
			t.Fatalf("decision = %+v", got)
		}
		// allow must carry updatedInput; the handler echoes the original when the
		// user didn't edit it, so the CLI gets one consistent shape.
		if string(got.UpdatedInput) != `{"file_path":"/tmp/x"}` {
			t.Fatalf("updatedInput = %s, want the original input echoed back", got.UpdatedInput)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("handler did not return after the user answered")
	}
}

// A bridge must not be able to raise prompts on a session it wasn't spawned for.
func TestApproveRejectsWrongSessionToken(t *testing.T) {
	broker := services.NewPermissionBroker()
	tokens := services.NewApproveTokenStore()
	tokens.Issue("s1")
	other, _ := tokens.Issue("s2")
	h := NativeApprove(broker, tokens)

	w := post(t, h, other, `{"sessionId":"s1","toolName":"Bash","toolUseId":"t1","input":{}}`)
	if w.Code != http.StatusForbidden {
		t.Fatalf("code = %d, want 403 for s2's token on s1", w.Code)
	}
	if len(broker.Pending("s1")) != 0 {
		t.Fatal("a rejected ask must not park a prompt on the session")
	}
}

func TestApproveRejectsMissingToken(t *testing.T) {
	broker := services.NewPermissionBroker()
	tokens := services.NewApproveTokenStore()
	tokens.Issue("s1")
	h := NativeApprove(broker, tokens)

	if w := post(t, h, "", `{"sessionId":"s1","toolName":"Bash","toolUseId":"t1","input":{}}`); w.Code != http.StatusForbidden {
		t.Fatalf("code = %d, want 403 with no token", w.Code)
	}
}

// A revoked session (its process died) must not be askable.
func TestApproveRejectsRevokedToken(t *testing.T) {
	broker := services.NewPermissionBroker()
	tokens := services.NewApproveTokenStore()
	tok, _ := tokens.Issue("s1")
	tokens.Revoke("s1")
	h := NativeApprove(broker, tokens)

	if w := post(t, h, tok, `{"sessionId":"s1","toolName":"Bash","toolUseId":"t1","input":{}}`); w.Code != http.StatusForbidden {
		t.Fatalf("code = %d, want 403 after revoke", w.Code)
	}
}

// When the caller hangs up (Claude died mid-wait), answer deny rather than leaking
// the goroutine or claiming an allow nobody gave.
func TestApproveDeniesWhenCancelled(t *testing.T) {
	broker := services.NewPermissionBroker()
	tokens := services.NewApproveTokenStore()
	tok, _ := tokens.Issue("s1")
	h := NativeApprove(broker, tokens)

	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		done <- post(t, h, tok, `{"sessionId":"s1","toolName":"Write","toolUseId":"toolu_2","input":{}}`)
	}()
	deadline := time.Now().Add(time.Second)
	for len(broker.Pending("s1")) == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}

	broker.CancelSession("s1") // the session's process went away

	select {
	case w := <-done:
		var got services.PermissionDecision
		json.Unmarshal(w.Body.Bytes(), &got)
		if got.Behavior != "deny" || got.Message == "" {
			t.Fatalf("decision = %+v, want a deny with an explanation", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("handler did not return after the session was cancelled")
	}
}
