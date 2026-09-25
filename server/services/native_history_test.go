package services

import (
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"

	"powercodedeck/db"
	"powercodedeck/internal/history"
)

func openHistoryDB(t *testing.T, path string) *sql.DB {
	t.Helper()
	database, err := sql.Open("sqlite", path+"?_pragma=foreign_keys(ON)")
	if err != nil {
		t.Fatal(err)
	}
	database.SetMaxOpenConns(1)
	if err := db.Migrate(database); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	return database
}

func TestNativeHistorySurvivesReopenAndAgentDeletion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.db")
	database := openHistoryDB(t, path)
	_, err := database.Exec(`INSERT INTO agents(id,preset,name,tmux_session,working_dir,command) VALUES('a','antigravity','a','a','/test','agy'),('b','antigravity','b','b','/test','agy')`)
	if err != nil {
		t.Fatal(err)
	}
	s := NewNativeService("")
	s.SetHistoryStore(history.New(database))
	sess := &nativeSession{id: "a", kind: "antigravity"}
	s.emit(sess, nativeTextEvent("user", "keep my prompt"))
	init, _ := ParseStreamEvent([]byte(`{"type":"system","subtype":"init","session_id":"resume-owned"}`))
	s.emit(sess, init)
	s.emit(sess, nativeTextEvent("assistant", "partial answer"))
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	database = openHistoryDB(t, path)
	store := history.New(database)
	restarted := NewNativeService("")
	restarted.SetHistoryStore(store)
	restored := &nativeSession{id: "a", kind: "antigravity"}
	if err := restarted.restoreNativeHistory(restored); err != nil {
		t.Fatal(err)
	}
	if len(restored.history) != 4 || restored.history[0].Message.Content[0].Text != "keep my prompt" || restored.history[3].TerminalReason != "session_ended" {
		t.Fatalf("restored: %+v", restored.history)
	}
	var resume string
	if err := database.QueryRow(`SELECT claude_session_id FROM agents WHERE id='a'`).Scan(&resume); err != nil || resume != "resume-owned" {
		t.Fatalf("resume: %q %v", resume, err)
	}
	again := &nativeSession{id: "a", kind: "antigravity"}
	if err := restarted.restoreNativeHistory(again); err != nil || len(again.history) != 4 {
		t.Fatalf("repeated interruption marker: %v", err)
	}
	for _, key := range [][2]string{{"b", "antigravity"}, {"a", "claude"}} {
		events, err := store.Load(key[0], key[1])
		if err != nil || len(events) != 0 {
			t.Fatal("history crossed agent/provider boundary")
		}
	}
	if _, err := database.Exec(`DELETE FROM agents WHERE id='a'`); err != nil {
		t.Fatal(err)
	}
	if events, err := store.Load("a", "antigravity"); err != nil || len(events) != 0 {
		t.Fatal("deleted history retained")
	}
	if err := store.Append("a", "antigravity", json.RawMessage(`{"type":"result"}`), "late"); err == nil {
		t.Fatal("late process recreated deleted history")
	}
}

type failedHistory struct{}

func (failedHistory) Append(string, string, json.RawMessage, string) error {
	return errors.New("disk unavailable")
}
func (failedHistory) Load(string, string) ([]json.RawMessage, error) {
	return nil, errors.New("disk unavailable")
}

func TestNativeHistoryFailureIsVisibleAndDoesNotEndTurn(t *testing.T) {
	s := NewNativeService("")
	s.SetHistoryStore(failedHistory{})
	sess := &nativeSession{id: "a", kind: "antigravity"}
	var delivered []*StreamEvent
	s.SetHandlers(func(_ string, ev *StreamEvent) { delivered = append(delivered, ev) }, nil)
	s.emit(sess, nativeTextEvent("user", "hello"))
	s.emit(sess, nativeTextEvent("assistant", "answer"))
	if len(delivered) != 3 || delivered[1].Type != "storage_warning" || len(sess.history) != 3 {
		t.Fatal("storage failure was hidden or warning repeated")
	}
	if err := s.restoreNativeHistory(&nativeSession{id: "a", kind: "antigravity"}); err == nil {
		t.Fatal("unreadable history silently discarded")
	}
}

func TestNativeHistoryCompletedTurnAndLimit(t *testing.T) {
	database := openHistoryDB(t, filepath.Join(t.TempDir(), "limit.db"))
	if _, err := database.Exec(`INSERT INTO agents(id,preset,name,tmux_session,working_dir,command) VALUES('a','antigravity','a','a','/test','agy')`); err != nil {
		t.Fatal(err)
	}
	store := history.New(database)
	// Seed beyond retention in one transaction, then exercise transactional pruning.
	tx, err := database.Begin()
	if err != nil {
		t.Fatal(err)
	}
	for range history.Limit + 3 {
		if _, err := tx.Exec(`INSERT INTO native_history(agent_id,provider,event) VALUES('a','antigravity','{"type":"provider_event"}')`); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := store.Append("a", "antigravity", nativeResultEvent().Raw, ""); err != nil {
		t.Fatal(err)
	}
	s := NewNativeService("")
	s.SetHistoryStore(store)
	sess := &nativeSession{id: "a", kind: "antigravity"}
	if err := s.restoreNativeHistory(sess); err != nil {
		t.Fatal(err)
	}
	if len(sess.history) != history.Limit || sess.history[len(sess.history)-1].Type != "result" {
		t.Fatal("retention/order changed")
	}
	if _, err := database.Exec(`UPDATE native_history SET event='broken' WHERE id=(SELECT MAX(id) FROM native_history)`); err != nil {
		t.Fatal(err)
	}
	if err := s.restoreNativeHistory(&nativeSession{id: "a", kind: "antigravity"}); err == nil {
		t.Fatal("corrupt history ignored")
	}
}
