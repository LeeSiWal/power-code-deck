package services

import (
	"os"
	"path/filepath"
	"testing"
)

// resolvePath must expand a leading ~ and make relative paths absolute so
// project operations never depend on the process's working directory.
func TestResolvePath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home) // os.UserHomeDir() reads $HOME on Linux

	s := &ProjectService{workspaceRoot: filepath.Join(home, "ws")}

	cases := map[string]string{
		"~":          home,
		"~/code":     filepath.Join(home, "code"),
		"~/a/b":      filepath.Join(home, "a", "b"),
		"/abs/path":  "/abs/path",
		"rel/dir":    filepath.Join(s.DefaultRoot(), "rel", "dir"),
		"":           s.DefaultRoot(),
		"  ~/code  ": filepath.Join(home, "code"), // trimmed
	}
	for in, want := range cases {
		if got := s.resolvePath(in); got != want {
			t.Errorf("resolvePath(%q) = %q, want %q", in, got, want)
		}
	}
}

// CreateProject with the client default "~/code" must make a real dir under the
// home directory — not a literal "~" folder under the cwd.
func TestCreateProjectExpandsTilde(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	s := &ProjectService{}

	got, err := s.CreateProject("~/code", "my-project")
	if err != nil {
		t.Fatalf("CreateProject error: %v", err)
	}
	want := filepath.Join(home, "code", "my-project")
	if got != want {
		t.Fatalf("CreateProject path = %q, want %q", got, want)
	}
	if fi, err := os.Stat(want); err != nil || !fi.IsDir() {
		t.Fatalf("expected directory at %q (err=%v)", want, err)
	}
	// A literal "~" directory must NOT have been created under the cwd.
	if _, err := os.Stat("~"); err == nil {
		os.RemoveAll("~")
		t.Fatalf("a literal ~ directory was created under cwd")
	}
}
