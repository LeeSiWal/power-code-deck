package orchestration

import (
	"database/sql"
	"errors"
	"path/filepath"
	"sync"
	"testing"

	_ "modernc.org/sqlite"
)

func openTest(t *testing.T, path string) *Store {
	t.Helper()
	database, err := sql.Open("sqlite", path+"?_pragma=foreign_keys(ON)&_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	database.SetMaxOpenConns(1)
	t.Cleanup(func() { database.Close() })
	s, err := New(database)
	if err != nil {
		t.Fatal(err)
	}
	return s
}
func createTest(t *testing.T, s *Store, key string) Run {
	t.Helper()
	r, err := s.Create(key, t.TempDir(), "implement task", "antigravity")
	if err != nil {
		t.Fatal(err)
	}
	return r
}
func TestIdempotencyAndStateGates(t *testing.T) {
	s := openTest(t, filepath.Join(t.TempDir(), "work.db"))
	r := createTest(t, s, "request")
	again, err := s.Create("request", r.Path, r.Prompt, r.Provider)
	if err != nil || again.ID != r.ID {
		t.Fatal("request duplicated", err)
	}
	if _, err := s.Create("request", r.Path, "different", r.Provider); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	if err := s.Complete(r.ID); !errors.Is(err, ErrConflict) {
		t.Fatal("queued task completed", err)
	}
	e, err := s.StartAttempt(r.ID)
	if err != nil {
		t.Fatal(err)
	}
	if e == r.ID || e == r.TaskID {
		t.Fatal("identity reused")
	}
	if _, err := s.StartAttempt(r.ID); !errors.Is(err, ErrConflict) {
		t.Fatal("double start", err)
	}
	if err := s.RecordCheck(e, "tests", true, "premature"); !errors.Is(err, ErrConflict) {
		t.Fatal("premature evidence accepted", err)
	}
	if err := s.FinishAttempt(e, true, "provider success"); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Get(r.ID)
	if got.State != "awaiting_checks" {
		t.Fatal("CLI success became task success")
	}
	if err := s.Complete(r.ID); !errors.Is(err, ErrConflict) {
		t.Fatal("unverified completion", err)
	}
	if err := s.RecordCheck(e, "tests", false, "assertion failed"); err != nil {
		t.Fatal(err)
	}
	got, _ = s.Get(r.ID)
	if got.State != "failed" || len(got.Executions[0].Checks) != 1 {
		t.Fatal("failed evidence lost")
	}
	retry, err := s.StartAttempt(r.ID)
	if err != nil || retry == e {
		t.Fatal("retry identity", err)
	}
	if err := s.FinishAttempt(e, true, "late"); !errors.Is(err, ErrConflict) {
		t.Fatal("stale execution accepted", err)
	}
	if err := s.FinishAttempt(retry, true, ""); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordCheck(retry, "tests", true, "passed"); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordCheck(retry, "tests", false, "overwrite"); !errors.Is(err, ErrConflict) {
		t.Fatal("evidence overwritten", err)
	}
	if err := s.Complete(r.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.Cancel(r.ID); !errors.Is(err, ErrConflict) {
		t.Fatal("completed run canceled", err)
	}
}
func TestRecoveryAndCancellation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "work.db")
	s := openTest(t, path)
	r := createTest(t, s, "recover")
	e, err := s.StartAttempt(r.ID)
	if err != nil {
		t.Fatal(err)
	}
	s.db.Close()
	s = openTest(t, path)
	if err := s.Recover(); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(r.ID)
	if err != nil || got.State != "interrupted" || got.Executions[0].State != "interrupted" {
		t.Fatal(got, err)
	}
	if err := s.FinishAttempt(e, true, "late success"); !errors.Is(err, ErrConflict) {
		t.Fatal("recovered process changed result", err)
	}
	retry, err := s.StartAttempt(r.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Cancel(r.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.FinishAttempt(retry, true, "late success"); !errors.Is(err, ErrConflict) {
		t.Fatal("cancellation undone", err)
	}
	if _, err := s.StartAttempt(r.ID); !errors.Is(err, ErrConflict) {
		t.Fatal("canceled run restarted", err)
	}
}
func TestOnlyOneConcurrentClaim(t *testing.T) {
	s := openTest(t, filepath.Join(t.TempDir(), "work.db"))
	r := createTest(t, s, "claim")
	var wg sync.WaitGroup
	results := make(chan error, 8)
	for range 8 {
		wg.Add(1)
		go func() { defer wg.Done(); _, err := s.StartAttempt(r.ID); results <- err }()
	}
	wg.Wait()
	close(results)
	success := 0
	for err := range results {
		if err == nil {
			success++
		} else if !errors.Is(err, ErrConflict) {
			t.Fatal(err)
		}
	}
	if success != 1 {
		t.Fatalf("claims=%d", success)
	}
}

func TestRetryCannotMovePinnedBaseCommit(t *testing.T) {
	s := openTest(t, filepath.Join(t.TempDir(), "work.db"))
	run := createTest(t, s, "pinned-base")
	first, err := s.StartAttempt(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.BindBase(run.ID, "commit-one"); err != nil {
		t.Fatal(err)
	}
	if err := s.FinishAttempt(first, false, "retry"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.StartAttempt(run.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.BindBase(run.ID, "commit-two"); !errors.Is(err, ErrConflict) {
		t.Fatal("retry moved the pinned source revision", err)
	}
	got, err := s.Get(run.ID)
	if err != nil || got.BaseCommit != "commit-one" {
		t.Fatal("pinned source revision was lost", got.BaseCommit, err)
	}
}

func TestRecoveryDuringVerificationPreservesEvidenceAndAllowsRetry(t *testing.T) {
	path := filepath.Join(t.TempDir(), "work.db")
	s := openTest(t, path)
	r := createTest(t, s, "verification-recovery")
	if err := s.RequireChecks(r.ID, "tests", "review"); err != nil {
		t.Fatal(err)
	}
	e, err := s.StartAttempt(r.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.FinishAttempt(e, true, "implementation done"); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordCheck(e, "tests", true, "passed"); err != nil {
		t.Fatal(err)
	}
	s.db.Close()
	s = openTest(t, path)
	if err := s.Recover(); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(r.ID)
	if err != nil || got.State != "interrupted" || got.Executions[0].State != "succeeded" || len(got.Executions[0].Checks) != 1 {
		t.Fatal(got, err)
	}
	if err := s.Complete(r.ID); !errors.Is(err, ErrConflict) {
		t.Fatal("recovered run completed without review", err)
	}
	if err := s.RecordCheck(e, "review", true, "late"); !errors.Is(err, ErrConflict) {
		t.Fatal("stale review accepted", err)
	}
	retry, err := s.StartAttempt(r.ID)
	if err != nil || retry == e {
		t.Fatal("retry unavailable", err)
	}
}
