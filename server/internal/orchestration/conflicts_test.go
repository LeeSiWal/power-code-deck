package orchestration

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConflictStagesAreCapturedAndImmutable(t *testing.T) {
	s, r, w := integrationFixture(t, "pass", true)
	id, err := w.StartIntegration(r.ID)
	if err != nil {
		t.Fatal(err)
	}
	waitWorker(t, w)
	raw, err := w.ReadArtifact(r.ID, id, "conflicts")
	if err != nil {
		t.Fatal(err)
	}
	var report ConflictReport
	if err := json.Unmarshal(raw, &report); err != nil {
		t.Fatal(err)
	}
	if len(report.Files) != 1 || report.Files[0].Path != "tracked.txt" || report.Truncated {
		t.Fatal(report)
	}
	file := report.Files[0]
	for _, item := range []struct {
		v        ConflictVersion
		expected string
	}{{file.Base, "original\n"}, {file.Current, "a\n"}, {file.Incoming, "b\n"}} {
		data, err := w.ReadArtifact(r.ID, id, item.v.Artifact)
		if err != nil || string(data) != item.expected {
			t.Fatal(string(data), err, item)
		}
	}
	workspace, err := s.Artifact(r.ID, id, "workspace")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace.Path, "tracked.txt"), []byte("later manual edit\n"), 0600); err != nil {
		t.Fatal(err)
	}
	data, err := w.ReadArtifact(r.ID, id, file.Current.Artifact)
	if err != nil || string(data) != "a\n" {
		t.Fatal("stage changed with workspace", string(data), err)
	}
	if _, err := w.ReadArtifact("other-run", id, file.Current.Artifact); err == nil {
		t.Fatal("foreign stage exposed")
	}
	next, err := w.StartIntegration(r.ID)
	if err != nil {
		t.Fatal(err)
	}
	waitWorker(t, w)
	if next == id {
		t.Fatal("retry overwrote evidence")
	}
	data, err = w.ReadArtifact(r.ID, id, file.Current.Artifact)
	if err != nil || string(data) != "a\n" {
		t.Fatal("retry changed historical stage")
	}
}

func TestConflictCaptureHandlesBinarySizeAndUnusualPaths(t *testing.T) {
	for _, mode := range []string{"text", "binary", "large", "deleted"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			repo := testRepo(t)
			name := "file with\ttab\nand newline.txt"
			if err := os.WriteFile(filepath.Join(repo, name), []byte("base\n"), 0600); err != nil {
				t.Fatal(err)
			}
			base, err := git(ctx, repo, "rev-parse", "HEAD")
			if err != nil {
				t.Fatal(err)
			}
			base, err = snapshot(ctx, repo, strings.TrimSpace(base))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := git(ctx, repo, "reset", "--hard", base); err != nil {
				t.Fatal(err)
			}
			incoming := "incoming\n"
			current := "current\n"
			if mode == "binary" {
				incoming = "incoming\x00"
				current = "current\x00"
			}
			if mode == "large" {
				current = strings.Repeat("x", 256*1024+1)
			}
			if mode == "deleted" {
				err = os.Remove(filepath.Join(repo, name))
			} else {
				err = os.WriteFile(filepath.Join(repo, name), []byte(incoming), 0600)
			}
			if err != nil {
				t.Fatal(err)
			}
			right, err := snapshot(ctx, repo, base)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := git(ctx, repo, "reset", "--hard", base); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(repo, name), []byte(current), 0600); err != nil {
				t.Fatal(err)
			}
			left, err := snapshot(ctx, repo, base)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := git(ctx, repo, "reset", "--hard", left); err != nil {
				t.Fatal(err)
			}
			if _, err := git(ctx, repo, "cherry-pick", "--no-commit", right); err == nil {
				t.Fatal("expected real Git conflict")
			}
			artifacts := map[string]string{}
			if err := captureConflict(ctx, repo, t.TempDir(), "attempt", base, right, "conflict", func(_, kind, path, _ string) error { artifacts[kind] = path; return nil }); err != nil {
				t.Fatal(err)
			}
			raw, err := os.ReadFile(artifacts["conflicts"])
			if err != nil {
				t.Fatal(err)
			}
			var report ConflictReport
			if err := json.Unmarshal(raw, &report); err != nil {
				t.Fatal(err)
			}
			if len(report.Files) != 1 || report.Files[0].Path != name {
				t.Fatal(report)
			}
			file := report.Files[0]
			switch mode {
			case "binary":
				if file.Current.Unavailable != "binary" || file.Incoming.Unavailable != "binary" {
					t.Fatal(file)
				}
			case "large":
				if file.Current.Unavailable != "size_limit" {
					t.Fatal(file)
				}
			case "deleted":
				if file.Incoming.Unavailable != "deleted_or_absent" {
					t.Fatal(file)
				}
			case "text":
				data, err := os.ReadFile(artifacts[file.Current.Artifact])
				if err != nil || string(data) != current {
					t.Fatal(string(data), err)
				}
			}
		})
	}
}
