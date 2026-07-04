package services

import (
	"os/exec"
	"testing"
	"time"
)

// requireTmux skips a test when tmux is unavailable (e.g. CI without tmux).
func requireTmux(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed; skipping session engine integration test")
	}
}

func newTestSession(t *testing.T, e *TmuxSessionEngine, id string) {
	t.Helper()
	e.Kill(id) // clear any stale session from a previous run
	if _, err := e.Create(CreateSessionRequest{
		ID:      id,
		Type:    "shell",
		Command: "sleep",
		Args:    []string{"300"},
		Cwd:     "/tmp",
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if !e.HasSession(id) {
		t.Fatal("session should be running immediately after Create")
	}
}

// The core guarantee: a viewer detaching must NOT terminate the process.
func TestDetachDoesNotKill(t *testing.T) {
	requireTmux(t)
	e := NewTmuxSessionEngine()
	id := "test-detach-notkill"
	newTestSession(t, e, id)
	defer e.Kill(id)

	if _, err := e.Attach(id, "viewer-1"); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	time.Sleep(150 * time.Millisecond)

	if err := e.Detach(id, "viewer-1"); err != nil {
		t.Fatalf("Detach: %v", err)
	}
	time.Sleep(150 * time.Millisecond)

	if !e.HasSession(id) {
		t.Fatal("Detach must NOT kill the session — the process should still be running")
	}

	if err := e.Kill(id); err != nil {
		t.Fatalf("Kill: %v", err)
	}
	time.Sleep(150 * time.Millisecond)
	if e.HasSession(id) {
		t.Fatal("Kill must terminate the session")
	}
}

// Multiple viewers: the session survives until the last one detaches, and even
// then only the streaming PTY is torn down — the process keeps running.
func TestMultiViewerDetach(t *testing.T) {
	requireTmux(t)
	e := NewTmuxSessionEngine()
	id := "test-multiviewer"
	newTestSession(t, e, id)
	defer e.Kill(id)

	if _, err := e.Attach(id, "viewer-A"); err != nil {
		t.Fatalf("Attach A: %v", err)
	}
	if _, err := e.Attach(id, "viewer-B"); err != nil {
		t.Fatalf("Attach B: %v", err)
	}

	e.Detach(id, "viewer-A")
	time.Sleep(100 * time.Millisecond)
	if !e.HasSession(id) {
		t.Fatal("session must survive while viewer-B is still attached")
	}

	e.Detach(id, "viewer-B")
	time.Sleep(100 * time.Millisecond)
	if !e.HasSession(id) {
		t.Fatal("session must survive even after the last viewer detaches")
	}
}

// Restart ends the old process and starts a fresh one; the session stays alive.
func TestRestartKeepsSessionAlive(t *testing.T) {
	requireTmux(t)
	e := NewTmuxSessionEngine()
	id := "test-restart"
	newTestSession(t, e, id)
	defer e.Kill(id)

	if err := e.Restart(id); err != nil {
		t.Fatalf("Restart: %v", err)
	}
	time.Sleep(150 * time.Millisecond)
	if !e.HasSession(id) {
		t.Fatal("session should be running after Restart")
	}
}
