package services

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestValidatePath(t *testing.T) {
	fs := NewFileService()

	base := t.TempDir()
	sub := filepath.Join(base, "sub")
	if err := os.MkdirAll(sub, 0755); err != nil {
		t.Fatal(err)
	}
	insideFile := filepath.Join(sub, "a.txt")
	if err := os.WriteFile(insideFile, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}

	// Sibling directory sharing the base's name prefix — the classic
	// HasPrefix bug: /base must NOT admit /base-evil.
	evil := base + "-evil"
	if err := os.MkdirAll(evil, 0755); err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(evil)
	evilFile := filepath.Join(evil, "secret.txt")
	if err := os.WriteFile(evilFile, []byte("s"), 0644); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name    string
		req     string
		wantErr bool
	}{
		{"file inside base", insideFile, false},
		{"base itself", base, false},
		{"dot-dot traversal", filepath.Join(base, "..", "etc", "passwd"), true},
		{"absolute path outside", "/etc/passwd", true},
		{"prefix sibling escape", evilFile, true},
		{"nonexistent write inside", filepath.Join(sub, "new.txt"), false},
		{"nonexistent nested write inside", filepath.Join(base, "x", "y", "z.txt"), false},
		{"empty path", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := fs.ValidatePath(base, tt.req)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ValidatePath(base, %q) err=%v, wantErr=%v", tt.req, err, tt.wantErr)
			}
		})
	}
}

func TestValidatePathSymlinkEscape(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks require privilege on Windows")
	}
	fs := NewFileService()

	base := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("s"), 0644); err != nil {
		t.Fatal(err)
	}

	link := filepath.Join(base, "link")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("cannot create symlink: %v", err)
	}

	// Existing target reached through a symlink that escapes the base.
	if _, err := fs.ValidatePath(base, filepath.Join(link, "secret.txt")); err == nil {
		t.Fatal("expected symlink escape (existing file) to be rejected")
	}
	// Non-existent write whose parent is a symlink escaping the base.
	if _, err := fs.ValidatePath(base, filepath.Join(link, "new.txt")); err == nil {
		t.Fatal("expected symlink escape (write path) to be rejected")
	}
}

func TestIsSensitivePath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // Windows home lookup

	fs := NewFileService()

	if err := os.MkdirAll(filepath.Join(home, ".ssh"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, "code", "proj"), 0755); err != nil {
		t.Fatal(err)
	}

	if !fs.IsSensitivePath(filepath.Join(home, ".ssh", "id_rsa")) {
		t.Fatal("~/.ssh/id_rsa should be sensitive")
	}
	if !fs.IsSensitivePath(filepath.Join(home, ".aws", "credentials")) {
		t.Fatal("~/.aws/credentials should be sensitive")
	}
	if fs.IsSensitivePath(filepath.Join(home, "code", "proj", "main.go")) {
		t.Fatal("a normal project file should NOT be sensitive")
	}
}
