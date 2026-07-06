package services

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The watch must be recursive: a file created in a subdirectory — and in a
// directory created after the watch starts — must be reported.
func TestWatcherRecursiveAndNewDirs(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}

	s := NewWatcherService()
	events := make(chan FileChange, 32)
	s.SetOnChange(func(_ string, c FileChange) { events <- c })
	if err := s.Watch("a1", root); err != nil {
		t.Fatalf("Watch: %v", err)
	}
	defer s.Unwatch("a1")
	time.Sleep(150 * time.Millisecond) // let inotify arm

	waitFor := func(name string) {
		t.Helper()
		deadline := time.After(3 * time.Second)
		for {
			select {
			case c := <-events:
				if strings.Contains(c.Path, name) {
					return
				}
			case <-deadline:
				t.Fatalf("no event mentioning %q within timeout", name)
			}
		}
	}

	// 1. File created in an existing subdirectory.
	if err := os.WriteFile(filepath.Join(sub, "existing.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	waitFor("existing.txt")

	// 2. A brand-new directory, then a file inside it — the watcher must have
	//    auto-added the new dir.
	newDir := filepath.Join(root, "created")
	if err := os.Mkdir(newDir, 0o755); err != nil {
		t.Fatal(err)
	}
	waitFor("created")
	time.Sleep(150 * time.Millisecond) // let the new dir get added
	if err := os.WriteFile(filepath.Join(newDir, "inside.txt"), []byte("y"), 0o644); err != nil {
		t.Fatal(err)
	}
	waitFor("inside.txt")
}
