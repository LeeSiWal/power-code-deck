package orchestration

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"powercodedeck/internal/orchestration/taskgraph"
	"powercodedeck/internal/providers"
)

func repairRequest(t *testing.T, w *Worker, run, attempt, content string) ResolutionRequest {
	t.Helper()
	raw, err := w.ReadArtifact(run, attempt, "conflicts")
	if err != nil {
		t.Fatal(err)
	}
	var report ConflictReport
	if err := json.Unmarshal(raw, &report); err != nil {
		t.Fatal(err)
	}
	if len(report.Files) != 1 {
		t.Fatal(report)
	}
	return ResolutionRequest{SourceAttempt: attempt, Fingerprint: report.Fingerprint, Files: []ResolvedFile{{Path: report.Files[0].Path, Content: content}}}
}

func failedIntegration(t *testing.T, w *Worker, run string) string {
	t.Helper()
	id, err := w.StartIntegration(run)
	if err != nil {
		t.Fatal(err)
	}
	waitWorker(t, w)
	return id
}

func TestRepairReplaysAllTasksAndPreservesFailedEvidence(t *testing.T) {
	tasks := []taskgraph.Task{{ID: "a", Prompt: "a", Provider: "codex"}, {ID: "b", Prompt: "b", Provider: "codex"}, {ID: "c", Prompt: "c", Provider: "codex"}, {ID: "d", Prompt: "d", Provider: "codex"}}
	s, r, w := planWorkerFixture(t, "pass", tasks, func(cwd, prompt string) error {
		name := "tracked.txt"
		if prompt == "d" {
			name = "later.txt"
		}
		return os.WriteFile(filepath.Join(cwd, name), []byte(prompt+"\n"), 0600)
	})
	if err := w.StartPlan(r.ID); err != nil {
		t.Fatal(err)
	}
	waitWorker(t, w)
	first := failedIntegration(t, w, r.ID)
	original, _ := w.ReadArtifact(r.ID, first, "conflicts")
	request := repairRequest(t, w, r.ID, first, "a+b\n")
	second, err := w.StartConflictResolution(r.ID, request)
	if err != nil {
		t.Fatal(err)
	}
	waitWorker(t, w)
	got, _ := s.Get(r.ID)
	if got.State != "integration_failed" {
		t.Fatal("later conflicting task was skipped", got)
	}
	next := repairRequest(t, w, r.ID, second, "a+b+c\n")
	third, err := w.StartConflictResolution(r.ID, next)
	if err != nil {
		t.Fatal(err)
	}
	waitWorker(t, w)
	got, _ = s.Get(r.ID)
	if got.State != "succeeded" {
		p, _ := s.GetPlan(r.ID)
		t.Fatal(got, p.Integrations)
	}
	result, err := s.Artifact(r.ID, third, "result_commit")
	if err != nil {
		t.Fatal(err)
	}
	for file, want := range map[string]string{"tracked.txt": "a+b+c\n", "later.txt": "d\n"} {
		text, err := git(context.Background(), r.Path, "show", result.BaseCommit+":"+file)
		if err != nil || text != want {
			t.Fatal(file, text, err)
		}
	}
	old, _ := w.ReadArtifact(r.ID, first, "conflicts")
	if string(old) != string(original) {
		t.Fatal("old conflict evidence changed")
	}
	p, _ := s.GetPlan(r.ID)
	if len(p.Integrations) != 3 || p.Integrations[0].State != "failed" || p.Integrations[1].State != "failed" || len(p.Integrations[2].Checks) != 3 {
		t.Fatal(p.Integrations)
	}
	head, _ := git(context.Background(), r.Path, "rev-parse", "HEAD")
	status, _ := git(context.Background(), r.Path, "status", "--porcelain")
	if strings.TrimSpace(head) != got.BaseCommit || status != "" {
		t.Fatal("source changed")
	}
}

func TestRepairRejectsStaleForeignAndInvalidInput(t *testing.T) {
	s, r, w := integrationFixture(t, "pass", true)
	first := failedIntegration(t, w, r.ID)
	request := repairRequest(t, w, r.ID, first, "resolved\n")
	invalid := []ResolutionRequest{
		{SourceAttempt: first, Fingerprint: "forged", Files: request.Files},
		{SourceAttempt: first, Fingerprint: request.Fingerprint, Files: []ResolvedFile{{Path: "../outside", Content: "edit"}}},
		{SourceAttempt: first, Fingerprint: request.Fingerprint, Files: []ResolvedFile{{Path: "other.txt", Content: "edit"}}},
		{SourceAttempt: first, Fingerprint: request.Fingerprint, Files: []ResolvedFile{{Path: "tracked.txt", Content: "x\x00"}}},
		{SourceAttempt: first, Fingerprint: request.Fingerprint, Files: []ResolvedFile{{Path: "tracked.txt", Content: "x", Delete: true}}},
		{SourceAttempt: first, Fingerprint: request.Fingerprint, Files: []ResolvedFile{{Path: "tracked.txt", Content: strings.Repeat("x", 256*1024+1)}}},
	}
	for _, bad := range invalid {
		if _, err := w.StartConflictResolution(r.ID, bad); err == nil {
			t.Fatal("invalid resolution accepted", bad.SourceAttempt)
		}
	}
	if _, err := w.StartConflictResolution("other-run", request); err == nil {
		t.Fatal("foreign run accepted")
	}
	p, _ := s.GetPlan(r.ID)
	if len(p.Integrations) != 1 {
		t.Fatal("invalid request allocated attempt")
	}
	failedIntegration(t, w, r.ID)
	if _, err := w.StartConflictResolution(r.ID, request); !errors.Is(err, ErrConflict) {
		t.Fatal("stale attempt accepted", err)
	}
}

func TestRepairStillRequiresReviewAndCanDelete(t *testing.T) {
	for _, mode := range []string{"reject", "delete"} {
		t.Run(mode, func(t *testing.T) {
			s, r, w := integrationFixture(t, "pass", true)
			first := failedIntegration(t, w, r.ID)
			request := repairRequest(t, w, r.ID, first, "resolved\n")
			if mode == "reject" {
				w.SetReviewer(func(id, cwd string) (providers.Execution, error) {
					return &fakeExecution{id: id, cwd: cwd, response: `{"verdict":"fail","summary":"resolution loses behavior"}`, stopped: make(chan struct{})}, nil
				})
			} else {
				request.Files[0].Delete = true
				request.Files[0].Content = ""
			}
			id, err := w.StartConflictResolution(r.ID, request)
			if err != nil {
				t.Fatal(err)
			}
			waitWorker(t, w)
			got, _ := s.Get(r.ID)
			if mode == "reject" {
				if got.State != "integration_failed" {
					t.Fatal(got)
				}
				if _, err := s.Artifact(r.ID, id, "result_commit"); err == nil {
					t.Fatal("unreviewed repair published")
				}
			} else {
				if got.State != "succeeded" {
					p, _ := s.GetPlan(r.ID)
					t.Fatal(got, p.Integrations)
				}
				a, _ := s.Artifact(r.ID, id, "result_commit")
				if _, err := git(context.Background(), r.Path, "cat-file", "-e", a.BaseCommit+":tracked.txt"); err == nil {
					t.Fatal("deletion ignored")
				}
			}
		})
	}
}

func TestRepairCancellationRejectsLateSuccess(t *testing.T) {
	s, r, w := integrationFixture(t, "pass", true)
	first := failedIntegration(t, w, r.ID)
	entered, stopped := make(chan struct{}), make(chan struct{})
	w.SetReviewer(func(id, cwd string) (providers.Execution, error) {
		return &fakeExecution{id: id, cwd: cwd, block: true, entered: entered, stopped: stopped}, nil
	})
	id, err := w.StartConflictResolution(r.ID, repairRequest(t, w, r.ID, first, "resolved\n"))
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("repair did not reach independent review")
	}
	if err := w.Cancel(r.ID); err != nil {
		t.Fatal(err)
	}
	waitWorker(t, w)
	select {
	case <-stopped:
	default:
		t.Fatal("reviewer still running")
	}
	if err := s.RecordIntegrationCheck(id, "review", true, "late"); !errors.Is(err, ErrConflict) {
		t.Fatal("late result accepted", err)
	}
	got, _ := s.Get(r.ID)
	if got.State != "canceled" {
		t.Fatal(got)
	}
}

func TestRepairRechecksCombinedProject(t *testing.T) {
	tasks := []taskgraph.Task{{ID: "a", Prompt: "a", Provider: "codex"}, {ID: "b", Prompt: "b", Provider: "codex"}}
	s, r, w := planWorkerFixture(t, "reject-combined", tasks, func(cwd, prompt string) error {
		if err := os.WriteFile(filepath.Join(cwd, prompt+".txt"), []byte(prompt), 0600); err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(cwd, "tracked.txt"), []byte(prompt+"\n"), 0600)
	})
	if err := w.StartPlan(r.ID); err != nil {
		t.Fatal(err)
	}
	waitWorker(t, w)
	first := failedIntegration(t, w, r.ID)
	id, err := w.StartConflictResolution(r.ID, repairRequest(t, w, r.ID, first, "resolved\n"))
	if err != nil {
		t.Fatal(err)
	}
	waitWorker(t, w)
	p, err := s.GetPlan(r.ID)
	if err != nil {
		t.Fatal(err)
	}
	last := p.Integrations[len(p.Integrations)-1]
	projectRejected := false
	for _, check := range last.Checks {
		if check.Name == "project_test" && !check.Passed && strings.Contains(check.Detail, "combined changes incompatible") {
			projectRejected = true
		}
	}
	if last.State != "failed" || !projectRejected {
		t.Fatal("repair bypassed combined project check", last)
	}
	if _, err := s.Artifact(r.ID, id, "result_commit"); err == nil {
		t.Fatal("failed project check published result")
	}
}

func TestRepairRejectsSymlinkTarget(t *testing.T) {
	s, r, w := integrationFixture(t, "pass", true)
	id := failedIntegration(t, w, r.ID)
	request := repairRequest(t, w, r.ID, id, "overwrite\n")
	workspace, err := s.Artifact(r.ID, id, "workspace")
	if err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outside, []byte("untouched\n"), 0600); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(workspace.Path, request.Files[0].Path)
	if err := os.Remove(target); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, target); err != nil {
		t.Fatal(err)
	}
	err = applyConflictResolution(context.Background(), workspace.Path, conflictResolution{Fingerprint: request.Fingerprint, Files: request.Files})
	if !errors.Is(err, ErrInvalid) {
		t.Fatal("symlink accepted", err)
	}
	content, err := os.ReadFile(outside)
	if err != nil || string(content) != "untouched\n" {
		t.Fatal("symlink destination changed", err)
	}
}
