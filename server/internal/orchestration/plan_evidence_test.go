package orchestration

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func commitAll(t *testing.T, repo, message string) {
	t.Helper()
	if _, err := git(context.Background(), repo, "add", "-A"); err != nil {
		t.Fatal(err)
	}
	if _, err := git(context.Background(), repo, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "-c", "commit.gpgsign=false", "commit", "-m", message); err != nil {
		t.Fatal(err)
	}
}

func planEvidenceOf(t *testing.T, repo string) planEvidence {
	t.Helper()
	ctx := context.Background()
	head, err := git(ctx, repo, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := collectPlanEvidence(ctx, repo, strings.TrimSpace(head))
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) > maxPlanInput {
		t.Fatalf("evidence is %d bytes, above the %d limit", len(raw), maxPlanInput)
	}
	var evidence planEvidence
	if err := json.Unmarshal([]byte(raw), &evidence); err != nil {
		t.Fatal(err)
	}
	return evidence
}

func TestPlanEvidenceCarriesListingAndContents(t *testing.T) {
	repo := testRepo(t)
	if err := os.MkdirAll(filepath.Join(repo, "src"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "src", "main.go"), []byte("package main\n"), 0600); err != nil {
		t.Fatal(err)
	}
	commitAll(t, repo, "add source")
	evidence := planEvidenceOf(t, repo)
	if evidence.Base == "" {
		t.Fatal("missing base commit")
	}
	want := []string{"src/main.go", "tracked.txt"}
	if strings.Join(evidence.Files, ",") != strings.Join(want, ",") {
		t.Fatalf("listing=%v want=%v", evidence.Files, want)
	}
	if evidence.Contents["tracked.txt"] != "original\n" || evidence.Contents["src/main.go"] != "package main\n" {
		t.Fatalf("contents=%v", evidence.Contents)
	}
	if len(evidence.Omitted) != 0 {
		t.Fatalf("omitted=%v", evidence.Omitted)
	}
}

func TestPlanEvidenceNamesEveryOmission(t *testing.T) {
	repo := testRepo(t)
	if err := os.WriteFile(filepath.Join(repo, "big.txt"), []byte(strings.Repeat("a", maxPlanFileSize+1)), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "binary.dat"), []byte{'a', 0, 'b'}, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/etc/passwd", filepath.Join(repo, "escape.link")); err != nil {
		t.Fatal(err)
	}
	commitAll(t, repo, "add awkward files")
	evidence := planEvidenceOf(t, repo)
	if len(evidence.Files) != 4 {
		t.Fatalf("listing must stay complete: %v", evidence.Files)
	}
	for _, name := range []string{"big.txt", "binary.dat", "escape.link"} {
		if evidence.Omitted[name] == "" {
			t.Fatalf("%s omitted without a reason: %v", name, evidence.Omitted)
		}
		if _, ok := evidence.Contents[name]; ok {
			t.Fatalf("%s content must not be included", name)
		}
	}
	if !strings.Contains(evidence.Omitted["escape.link"], "regular") {
		t.Fatalf("symlink reason=%q", evidence.Omitted["escape.link"])
	}
	if strings.Contains(strings.Join(evidence.Files, "\n"), "passwd") {
		t.Fatal("link target leaked into evidence")
	}
	for _, content := range evidence.Contents {
		if strings.Contains(content, "root:") {
			t.Fatal("link target contents leaked into evidence")
		}
	}
	if evidence.Contents["tracked.txt"] != "original\n" {
		t.Fatal("readable file dropped alongside omissions")
	}
}

// Over budget the listing must stay complete and every excluded file must be
// named, rather than the payload being silently shortened.
func TestPlanEvidenceStaysBoundedAndExplicitOverBudget(t *testing.T) {
	repo := testRepo(t)
	body := strings.Repeat("b", 1024)
	for i := 0; i < 200; i++ {
		if err := os.WriteFile(filepath.Join(repo, fmt.Sprintf("file%03d.txt", i)), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	commitAll(t, repo, "add many files")
	evidence := planEvidenceOf(t, repo)
	if len(evidence.Files) != 201 {
		t.Fatalf("listing must stay complete: %d files", len(evidence.Files))
	}
	if len(evidence.Contents) == 0 {
		t.Fatal("no contents fit in the budget")
	}
	if len(evidence.Contents) >= len(evidence.Files) {
		t.Fatal("every file fit; the test no longer exercises the budget")
	}
	for _, name := range evidence.Files {
		_, included := evidence.Contents[name]
		reason := evidence.Omitted[name]
		if included == (reason != "") {
			t.Fatalf("%s included=%v reason=%q", name, included, reason)
		}
	}
}

func TestPlanEvidenceRejectsUnusableRepositories(t *testing.T) {
	ctx := context.Background()
	empty := t.TempDir()
	if _, err := git(ctx, empty, "init"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(empty, "untracked.txt"), []byte("x\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := git(ctx, empty, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "-c", "commit.gpgsign=false", "commit", "--allow-empty", "-m", "empty"); err != nil {
		t.Fatal(err)
	}
	head, err := git(ctx, empty, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := collectPlanEvidence(ctx, empty, strings.TrimSpace(head)); err == nil {
		t.Fatal("a repository with no tracked files must be rejected")
	}
	repo := testRepo(t)
	if _, err := collectPlanEvidence(ctx, repo, "refs/heads/does-not-exist"); err == nil {
		t.Fatal("an unresolvable base must be rejected")
	}
}
