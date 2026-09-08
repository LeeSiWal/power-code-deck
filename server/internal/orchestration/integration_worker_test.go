package orchestration

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"powercodedeck/internal/orchestration/taskgraph"
	"powercodedeck/internal/providers"
)

func integrationFixture(t *testing.T, check string, conflict bool) (*Store, Run, *Worker) {
	t.Helper()
	tasks := []taskgraph.Task{{ID: "a", Provider: "codex", Prompt: "a"}, {ID: "b", Provider: "codex", Prompt: "b"}}
	s, r, w := planWorkerFixture(t, check, tasks, func(cwd, prompt string) error {
		name := prompt + ".txt"
		if conflict {
			name = "tracked.txt"
		}
		return os.WriteFile(filepath.Join(cwd, name), []byte(prompt+"\n"), 0600)
	})
	if _, err := w.StartIntegration(r.ID); !errors.Is(err, ErrConflict) {
		t.Fatal("unfinished plan accepted", err)
	}
	if err := w.StartPlan(r.ID); err != nil {
		t.Fatal(err)
	}
	waitWorker(t, w)
	r, err := s.Get(r.ID)
	if err != nil || r.State != "awaiting_integration" {
		t.Fatal(r, err)
	}
	return s, r, w
}

func TestIntegrationVerifiesDisconnectedBranchesAndRetainsCombinedResult(t *testing.T) {
	s, r, w := integrationFixture(t, "pass", false)
	w.SetReviewer(func(id, cwd string) (providers.Execution, error) {
		for _, name := range []string{"a.txt", "b.txt"} {
			if _, err := os.Stat(filepath.Join(cwd, name)); err != nil {
				return nil, err
			}
		}
		return &fakeExecution{id: id, cwd: cwd, response: `{"verdict":"pass","summary":"combined branches reviewed"}`, stopped: make(chan struct{})}, nil
	})
	id, err := w.StartIntegration(r.ID)
	if err != nil {
		t.Fatal(err)
	}
	waitWorker(t, w)
	got, _ := s.Get(r.ID)
	if got.State != "succeeded" {
		p, _ := s.GetPlan(r.ID)
		t.Fatalf("%s %+v", got.State, p.Integrations)
	}
	p, _ := s.GetPlan(r.ID)
	if len(p.Integrations) != 1 || len(p.Integrations[0].Checks) != 3 {
		t.Fatal(p)
	}
	patch, err := w.ReadArtifact(r.ID, id, "changes.patch")
	if err != nil || !strings.Contains(string(patch), "a.txt") || !strings.Contains(string(patch), "b.txt") {
		t.Fatal(string(patch), err)
	}
	if _, err := w.ReadArtifact("other", id, "changes.patch"); err == nil {
		t.Fatal("foreign Run read integration evidence")
	}
	a, err := s.Artifact(r.ID, id, "result_commit")
	if err != nil {
		t.Fatal(err)
	}
	ref, err := git(context.Background(), r.Path, "rev-parse", "refs/powercodedeck/integrations/"+id)
	if err != nil || strings.TrimSpace(ref) != a.BaseCommit {
		t.Fatal(ref, err)
	}
	for _, name := range []string{"a.txt", "b.txt"} {
		content, err := git(context.Background(), r.Path, "show", a.BaseCommit+":"+name)
		if err != nil || content != strings.TrimSuffix(name, ".txt")+"\n" {
			t.Fatal(content, err)
		}
	}
	head, _ := git(context.Background(), r.Path, "rev-parse", "HEAD")
	status, _ := git(context.Background(), r.Path, "status", "--porcelain")
	if strings.TrimSpace(head) != r.BaseCommit || status != "" {
		t.Fatal("source moved or dirtied", head, status)
	}
	if _, err := w.StartIntegration(r.ID); !errors.Is(err, ErrConflict) {
		t.Fatal("completed result reran", err)
	}
}

func TestIntegrationRejectsConflictAndCombinedCheckFailure(t *testing.T) {
	for _, mode := range []string{"conflict", "reject-combined"} {
		t.Run(mode, func(t *testing.T) {
			check := "pass"
			if mode != "conflict" {
				check = mode
			}
			s, r, w := integrationFixture(t, check, mode == "conflict")
			id, err := w.StartIntegration(r.ID)
			if err != nil {
				t.Fatal(err)
			}
			waitWorker(t, w)
			p, _ := s.GetPlan(r.ID)
			got, _ := s.Get(r.ID)
			if got.State != "integration_failed" || p.Integrations[0].State != "failed" {
				t.Fatal(got, p)
			}
			if _, err := s.Artifact(r.ID, id, "result_commit"); err == nil {
				t.Fatal("failed integration published")
			}
			if mode == "conflict" {
				if _, err := w.ReadArtifact(r.ID, id, "integration_log"); err != nil {
					t.Fatal(err)
				}
			}
			for _, task := range p.Tasks {
				if task.State != taskgraph.Succeeded {
					t.Fatal("verified tasks lost", task)
				}
			}
			if err := s.CompleteIntegration(id); !errors.Is(err, ErrConflict) {
				t.Fatal("failure completed", err)
			}
			next, err := w.StartIntegration(r.ID)
			if err != nil {
				t.Fatal(err)
			}
			waitWorker(t, w)
			p, _ = s.GetPlan(r.ID)
			if next == id || len(p.Integrations) != 2 || p.Integrations[0].State != "failed" {
				t.Fatal("retry overwrote evidence", p)
			}
		})
	}
}

func TestIntegrationRequiresFreshIndependentReview(t *testing.T) {
	for _, mode := range []string{"reject", "mutate"} {
		t.Run(mode, func(t *testing.T) {
			s, r, w := integrationFixture(t, "pass", false)
			w.SetReviewer(func(id, cwd string) (providers.Execution, error) {
				e := &fakeExecution{id: id, cwd: cwd, response: `{"verdict":"fail","summary":"combined behavior incorrect"}`, stopped: make(chan struct{})}
				if mode == "mutate" {
					e.response = `{"verdict":"pass","summary":"looks good"}`
					e.reviewChange = "unreviewed\n"
				}
				return e, nil
			})
			id, err := w.StartIntegration(r.ID)
			if err != nil {
				t.Fatal(err)
			}
			waitWorker(t, w)
			got, _ := s.Get(r.ID)
			if got.State != "integration_failed" {
				t.Fatal(got)
			}
			if _, err := s.Artifact(r.ID, id, "result_commit"); err == nil {
				t.Fatal("unreviewed result published")
			}
		})
	}
}

func TestIntegrationCancelStopsReviewerAndRejectsLateEvidence(t *testing.T) {
	s, r, w := integrationFixture(t, "pass", false)
	entered, stopped := make(chan struct{}), make(chan struct{})
	w.SetReviewer(func(id, cwd string) (providers.Execution, error) {
		return &fakeExecution{id: id, cwd: cwd, block: true, entered: entered, stopped: stopped}, nil
	})
	id, err := w.StartIntegration(r.ID)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("review not started")
	}
	if _, err := w.Start(r.ID); !errors.Is(err, ErrConflict) {
		t.Fatal("single worker shared occupied slot", err)
	}
	if _, err := w.StartIntegration(r.ID); !errors.Is(err, ErrConflict) {
		t.Fatal("duplicate integration", err)
	}
	if err := w.Cancel(r.ID); err != nil {
		t.Fatal(err)
	}
	waitWorker(t, w)
	select {
	case <-stopped:
	default:
		t.Fatal("reviewer not stopped")
	}
	if err := s.RecordIntegrationCheck(id, "review", true, "late"); !errors.Is(err, ErrConflict) {
		t.Fatal("late check accepted", err)
	}
	if err := s.AddIntegrationArtifact(id, "result_commit", "", "late"); !errors.Is(err, ErrConflict) {
		t.Fatal("late artifact accepted", err)
	}
	got, _ := s.Get(r.ID)
	if got.State != "canceled" {
		t.Fatal(got)
	}
}

func TestIntegrationRecoveryDoesNotReuseChecks(t *testing.T) {
	s, r, w := integrationFixture(t, "pass", false)
	checks, err := loadVerificationPlan(r.Path)
	if err != nil {
		t.Fatal(err)
	}
	first, err := s.BeginIntegration(r.ID, checks)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.RecordIntegrationCheck(first, "diff_check", true, "passed"); err != nil {
		t.Fatal(err)
	}
	if err := s.Recover(); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordIntegrationCheck(first, "review", true, "late"); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	p, _ := s.GetPlan(r.ID)
	if p.Integrations[0].State != "interrupted" {
		t.Fatal(p)
	}
	next, err := w.StartIntegration(r.ID)
	if err != nil {
		t.Fatal(err)
	}
	waitWorker(t, w)
	p, _ = s.GetPlan(r.ID)
	if first == next || len(p.Integrations) != 2 || len(p.Integrations[0].Checks) != 1 || len(p.Integrations[1].Checks) != 3 {
		t.Fatal(p)
	}
}

func TestIntegrationRejectsChangedSource(t *testing.T) {
	s, r, w := integrationFixture(t, "pass", false)
	if _, err := git(context.Background(), r.Path, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "-c", "commit.gpgsign=false", "commit", "--allow-empty", "-m", "advance source"); err != nil {
		t.Fatal(err)
	}
	if _, err := w.StartIntegration(r.ID); !errors.Is(err, ErrConflict) {
		t.Fatal("different source accepted", err)
	}
	p, _ := s.GetPlan(r.ID)
	if len(p.Integrations) != 0 {
		t.Fatal("failed preflight reserved work")
	}
}
