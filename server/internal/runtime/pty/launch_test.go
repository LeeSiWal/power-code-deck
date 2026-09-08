package pty_test

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
	"time"

	ptyruntime "powercodedeck/internal/runtime/pty"
	"powercodedeck/internal/runtime/session"
)

// Exercise the extracted engine without importing any legacy application code.
// The logical command is deliberately not an executable on the host.
func TestPreparedLaunchAndRestart(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell integration; cross-compiled for Windows")
	}
	shell, err := exec.LookPath("sh")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	dir, err = filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	req := session.CreateSessionRequest{
		ID: "execution-42", Type: "custom", Command: "logical-provider",
		Args: []string{"argument with spaces"}, Cwd: dir,
	}
	launches := 0
	engine := ptyruntime.NewEngine(4096, func(got session.CreateSessionRequest) (ptyruntime.LaunchSpec, error) {
		if !reflect.DeepEqual(got, req) {
			t.Fatalf("preparer request = %+v, want %+v", got, req)
		}
		launches++
		return ptyruntime.LaunchSpec{
			Command: shell,
			Args:    []string{"-c", `printf 'launch=%s,arg=%s,cwd=%s\n' "$PCD_TEST_LAUNCH" "$1" "$PWD"; while IFS= read -r line; do printf 'reply=%s\n' "$line"; done`, "pcd-test", got.Args[0]},
			Env:     append(os.Environ(), fmt.Sprintf("PCD_TEST_LAUNCH=%d", launches)),
		}, nil
	})
	info, err := engine.Create(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = engine.Kill(req.ID) })
	if info.Command != req.Command || !reflect.DeepEqual(info.Args, req.Args) || info.Cwd != req.Cwd {
		t.Fatalf("metadata must retain logical request: %+v", info)
	}
	awaitReplay := func(want string) {
		t.Helper()
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			result, err := engine.Attach(req.ID, "viewer")
			if err != nil {
				t.Fatal(err)
			}
			if bytes.Contains(result.Replay, []byte(want)) {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatalf("replay never contained %q", want)
	}
	awaitReplay("launch=1,arg=argument with spaces,cwd=" + dir)
	if err := engine.Detach(req.ID, "viewer"); err != nil {
		t.Fatal(err)
	}
	if engine.HasViewer(req.ID, "viewer") || !engine.HasSession(req.ID) {
		t.Fatal("detach must remove viewer but keep process")
	}
	if err := engine.Write(req.ID, []byte("still-running\n")); err != nil {
		t.Fatal(err)
	}
	awaitReplay("reply=still-running")
	if err := engine.Restart(req.ID); err != nil {
		t.Fatal(err)
	}
	if launches != 2 {
		t.Fatalf("restart must prepare another launch, got %d", launches)
	}
	awaitReplay("launch=2,arg=argument with spaces,cwd=" + dir)
}

func TestLaunchFailureDoesNotRegisterSession(t *testing.T) {
	denied := errors.New("launch denied")
	for _, tc := range []struct {
		name    string
		prepare ptyruntime.PrepareLaunch
		cause   error
	}{
		{"missing preparer", nil, nil},
		{"preparation failure", func(session.CreateSessionRequest) (ptyruntime.LaunchSpec, error) {
			return ptyruntime.LaunchSpec{}, denied
		}, denied},
		{"missing executable", func(session.CreateSessionRequest) (ptyruntime.LaunchSpec, error) {
			return ptyruntime.LaunchSpec{Command: filepath.Join(t.TempDir(), "missing-executable")}, nil
		}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			engine := ptyruntime.NewEngine(0, tc.prepare)
			if _, err := engine.Create(session.CreateSessionRequest{ID: "failed"}); err == nil {
				t.Fatal("expected launch error")
			} else if tc.cause != nil && !errors.Is(err, tc.cause) {
				t.Fatalf("lost error cause: %v", err)
			}
			list, err := engine.List()
			if err != nil || len(list) != 0 || engine.HasSession("failed") {
				t.Fatalf("failed launch registered a session: %+v, %v", list, err)
			}
		})
	}
}
