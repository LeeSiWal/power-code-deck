package services

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"powercodedeck/db"
	"powercodedeck/internal/history"
	"powercodedeck/internal/providers"
	"powercodedeck/internal/providers/antigravity"
)

func TestChatAGYHelper(t *testing.T) {
	if os.Getenv("PCD_CHAT_AGY_HELPER") != "1" {
		return
	}
	value := func(flag string) string {
		for i, arg := range os.Args {
			if arg == flag && i+1 < len(os.Args) {
				return os.Args[i+1]
			}
		}
		return ""
	}
	fmt.Println(`{"event":"init","conversation_id":"chat-owned","init":{"model":"fake"}}`)
	if value("--prompt") == "block" {
		for {
			time.Sleep(time.Second)
		}
	}
	text, _ := json.Marshal(map[string]any{"event": "step_update", "step_update": map[string]any{"conversation_id": "chat-owned", "step_type": "agent_response", "step_index": 1, "text_delta": "resume=" + value("--conversation")}})
	fmt.Println(string(text))
	fmt.Println(`{"event":"step_update","step_update":{"conversation_id":"chat-owned","step_type":"tool","step_index":2,"state":"DONE","tool_name":"read_file","tool_info":{"parameters":{"path":"test"},"output":{"text":"ok"}}}}`)
	fmt.Fprintln(os.Stderr, "approval required: shell command skipped")
	fmt.Println(`{"event":"result","result":{"conversation_id":"chat-owned","status":"SUCCESS","response":"done","usage":{"input_tokens":12}}}`)
	os.Exit(0)
}

func TestAntigravityNativeChatRoundTrip(t *testing.T) {
	bin, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	d := newAntigravityChat(antigravity.Config{Command: bin, PrefixArgs: []string{"-test.run=^TestChatAGYHelper$", "--"}, Cwd: t.TempDir(), Env: append(os.Environ(), "PCD_CHAT_AGY_HELPER=1")})
	if err := d.Start(); err != nil {
		t.Fatal(err)
	}
	defer d.Stop()
	s := NewNativeService("")
	sess := &nativeSession{id: "agy-agent", kind: "antigravity", driver: d}
	s.sessions[sess.id] = sess
	output := make(chan *StreamEvent, 100)
	canonical := make(chan providers.Event, 100)
	s.SetHandlers(func(_ string, event *StreamEvent) { output <- event }, nil)
	s.AddExecutionObserver(func(event providers.Event) { canonical <- event })
	pumpDone := make(chan struct{})
	go func() { s.pump(sess); close(pumpDone) }()
	defer func() {
		d.Stop()
		select {
		case <-pumpDone:
		case <-time.After(5 * time.Second):
			t.Error("pump leaked")
		}
	}()
	read := func() *StreamEvent {
		t.Helper()
		select {
		case ev := <-output:
			return ev
		case <-time.After(10 * time.Second):
			t.Fatal("no chat output")
			return nil
		}
	}
	var previous providers.Identity
	for _, prompt := range []string{"first", "follow-up", "block"} {
		if err := s.Send(sess.id, prompt); err != nil {
			t.Fatal(err)
		}
		if first := read(); first.Type != "user" || first.Message.Content[0].Text != prompt {
			t.Fatalf("reply overtook user: %+v", first)
		}
		id, ok := s.ExecutionIdentity(sess.id)
		if !ok || id == previous {
			t.Fatal("identity missing or reused")
		}
		previous = id
		if prompt == "block" {
			if ev := read(); ev.Type != "system" {
				t.Fatal(ev)
			}
			if err := s.Send(sess.id, "overlap"); err == nil {
				t.Fatal("overlapping prompt accepted")
			}
			if err := s.Interrupt(sess.id); err != nil {
				t.Fatal(err)
			}
		}
		var wires []*StreamEvent
		for {
			ev := read()
			wires = append(wires, ev)
			if ev.Type == "result" {
				break
			}
		}
		last := wires[len(wires)-1]
		if last.Usage != nil || strings.Contains(string(last.Raw), "total_cost_usd") {
			t.Fatal("invented per-turn usage/cost")
		}
		if prompt != "block" && !strings.Contains(string(last.Raw), "shell command skipped") {
			t.Fatal("diagnostic hidden")
		}
		if prompt == "follow-up" {
			found := false
			for _, ev := range wires {
				if strings.Contains(string(ev.Raw), "resume=chat-owned") {
					found = true
				}
			}
			if !found {
				t.Fatal("conversation was not resumed")
			}
		}
		if prompt == "block" && !last.IsError {
			t.Fatal("interruption not surfaced")
		}
	}
	if err := s.SetMode(sess.id, "bypassPermissions"); err == nil {
		t.Fatal("unsupported mode accepted")
	}
	if err := s.SetModel(sess.id, "claude-model"); err == nil {
		t.Fatal("unsupported UI model change accepted")
	}
	if err := s.SetEffort(sess.id, "max"); err == nil {
		t.Fatal("unsupported effort accepted")
	}
	if _, err := s.SetOptions(sess.id, NativeOptions{}); err == nil {
		t.Fatal("unsupported options accepted")
	}
	if d.ConversationID() != "chat-owned" {
		t.Fatal("conversation lost")
	}
	close(canonical) // all terminal wire callbacks have been received
	results := 0
	for ev := range canonical {
		if ev.Kind == providers.TurnFinished {
			results++
			if ev.Outcome.Status == providers.CompletionSuccess && ev.Outcome.Usage.Scope != providers.ConversationUsage {
				t.Fatal("canonical usage altered")
			}
		}
	}
	if results != 3 {
		t.Fatalf("canonical result duplicates/missing: %d", results)
	}
}

func TestAntigravityCreateDoesNotLaunchPTYOrInheritClaude(t *testing.T) {
	database, err := sql.Open("sqlite", t.TempDir()+"/agents.db")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err := db.Migrate(database); err != nil {
		t.Fatal(err)
	}
	s := &AgentService{db: database} // nil engine intentionally proves no PTY launch
	a, err := s.Create(CreateAgentRequest{Preset: "antigravity", Name: "AGY", Command: "agy", WorkingDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if a.Status != "stopped" {
		t.Fatal("unstarted agent reported running")
	}
	if m, mode, effort := s.NativeConfig(a.ID); m != "" || mode != "" || effort != "" {
		t.Fatal("foreign settings inherited")
	}
	if _, err := s.Create(CreateAgentRequest{Preset: "antigravity", Command: "claude"}); err == nil {
		t.Fatal("mismatched command accepted")
	}
	if _, err := s.Create(CreateAgentRequest{Preset: "antigravity", Command: "agy", Args: []string{"--dangerously-skip-permissions"}}); err == nil {
		t.Fatal("custom argv silently ignored")
	}
}

func TestAntigravityStoppedChatReader(t *testing.T) {
	d := newAntigravityChat(antigravity.Config{})
	d.Stop()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := d.NextEnvelope(ctx); err == nil || ctx.Err() != nil {
		t.Fatal("closed chat did not end immediately")
	}
}

func TestNativeStartSelectsAntigravity(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("PATH fixture is a POSIX executable wrapper")
	}
	bin, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "agy"), []byte("#!/bin/sh\nexec \"$PCD_CHAT_TEST_BIN\" -test.run=^TestChatAGYHelper$ -- \"$@\"\n"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("PCD_CHAT_TEST_BIN", bin)
	t.Setenv("PCD_CHAT_AGY_HELPER", "1")
	dbPath := filepath.Join(dir, "history.db")
	database := openHistoryDB(t, dbPath)
	if _, err := database.Exec(`INSERT INTO agents(id,preset,name,tmux_session,working_dir,command) VALUES('native-agy','antigravity','a','a',?,'agy')`, dir); err != nil {
		t.Fatal(err)
	}
	s := NewNativeService("")
	s.SetHistoryStore(history.New(database))
	done := make(chan *StreamEvent, 1)
	s.SetHandlers(func(_ string, ev *StreamEvent) {
		if ev.Type == "result" {
			done <- ev
		}
	}, nil)
	if err := s.Start("native-agy", "antigravity", dir, "", "", "bypassPermissions", "max"); err != nil {
		t.Fatal(err)
	}
	defer s.Stop("native-agy")
	if s.sessions["native-agy"].mode != "" || s.sessions["native-agy"].effort != "" {
		t.Fatal("Claude launch hints were used")
	}
	if err := s.Send("native-agy", "hello"); err != nil {
		t.Fatal(err)
	}
	select {
	case ev := <-done:
		if ev.IsError || ev.SessionID != "chat-owned" {
			t.Fatalf("result: %+v", ev)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("native launch did not complete")
	}
	s.Stop("native-agy")
	deadline := time.Now().Add(5 * time.Second)
	for s.Running("native-agy") && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if s.Running("native-agy") {
		t.Fatal("native stop did not finish")
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	database = openHistoryDB(t, dbPath)
	agents := &AgentService{db: database}
	reopened := NewNativeService("")
	reopened.SetPersistence(agents.SetClaudeSessionID, agents.ClaudeSessionID)
	reopened.SetHistoryStore(history.New(database))
	reopened.SetHandlers(func(_ string, ev *StreamEvent) {
		if ev.Type == "result" {
			done <- ev
		}
	}, nil)
	if err := reopened.Start("native-agy", "antigravity", dir, "", "", "", ""); err != nil {
		t.Fatal(err)
	}
	defer reopened.Stop("native-agy")
	previous := reopened.History("native-agy")
	if len(previous) < 3 || previous[0].Message.Content[0].Text != "hello" || previous[len(previous)-1].Type != "result" {
		t.Fatal("completed history not restored at native open")
	}
	if err := reopened.Send("native-agy", "follow-up"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("resumed request did not complete")
	}
	found := false
	for _, ev := range reopened.History("native-agy")[len(previous):] {
		if strings.Contains(string(ev.Raw), "resume=chat-owned") {
			found = true
		}
	}
	if !found {
		t.Fatal("cold reopen lost provider conversation ID")
	}
}
