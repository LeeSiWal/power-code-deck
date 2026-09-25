package services

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// RoutingRunner lets routing discovery find CLIs exactly the way agent launch
// does (PATH plus the agent bin dirs, newest install wins) and run their
// documented status commands. It never installs or signs in.
type RoutingRunner struct{}

func (RoutingRunner) LookPath(name string) (string, error) {
	if p := findAgentCommand(name); p != "" {
		return p, nil
	}
	return "", exec.ErrNotFound
}

func (RoutingRunner) Run(ctx context.Context, bin string, args []string, stdin string) (string, error) {
	cmd := exec.CommandContext(ctx, bin, args...)
	if stdin != "" {
		// Keep stdin open until the command has answered; app-server exits on EOF.
		cmd.Stdin = strings.NewReader(stdin)
	}
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &limited{&out, 4 << 20}, &limited{&errOut, 64 << 10}
	err := cmd.Run()
	if ctx.Err() != nil && out.Len() > 0 {
		// Long-lived servers (app-server) are ended by the deadline after
		// answering; their output is still usable.
		return out.String(), nil
	}
	if out.Len() == 0 && errOut.Len() > 0 {
		// Several status commands (codex login status, Go flag usage for
		// agy --help) write their answer to stderr.
		if err != nil && ctx.Err() != nil {
			return "", errors.New(strings.TrimSpace(errOut.String()))
		}
		return errOut.String(), nil
	}
	return out.String(), err
}

type limited struct {
	b *bytes.Buffer
	n int
}

func (l *limited) Write(p []byte) (int, error) {
	if room := l.n - l.b.Len(); room < len(p) {
		if room > 0 {
			l.b.Write(p[:room])
		}
		return len(p), nil
	}
	return l.b.Write(p)
}

// RunUntil keeps stdin open (a JSON-RPC server exits on EOF) until done reports
// that the needed answer has arrived, the process exits, or ctx ends.
func (RoutingRunner) RunUntil(ctx context.Context, bin string, args []string, stdin string, done func(string) bool) (string, error) {
	cmd := exec.CommandContext(ctx, bin, args...)
	in, err := cmd.StdinPipe()
	if err != nil {
		return "", err
	}
	var mu sync.Mutex
	var out bytes.Buffer
	cmd.Stdout = writerFunc(func(p []byte) (int, error) {
		mu.Lock()
		defer mu.Unlock()
		if out.Len() < 4<<20 {
			out.Write(p)
		}
		return len(p), nil
	})
	if err := cmd.Start(); err != nil {
		return "", err
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	in.Write([]byte(stdin))
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	snapshot := func() string { mu.Lock(); defer mu.Unlock(); return out.String() }
	for {
		select {
		case err := <-exited:
			return snapshot(), err
		case <-ctx.Done():
			in.Close()
			return snapshot(), ctx.Err()
		case <-tick.C:
			if s := snapshot(); done(s) {
				in.Close()
				_ = cmd.Process.Kill()
				<-exited
				return s, nil
			}
		}
	}
}

type writerFunc func([]byte) (int, error)

func (f writerFunc) Write(p []byte) (int, error) { return f(p) }
