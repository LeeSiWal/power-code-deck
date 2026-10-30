package config

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestRestrictEnvFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("mode bits don't apply")
	}
	p := filepath.Join(t.TempDir(), "deck.env")
	if err := os.WriteFile(p, []byte("JWT_SECRET=x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	restrictEnvFile(p)
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %o, want 600", fi.Mode().Perm())
	}
}
