package orchestration

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"powercodedeck/internal/providers"
)

// Launch selects the concrete profile for one attempt. The zero value is the
// legacy behavior: the Run's provider with CLI defaults.
type Launch struct {
	ProfileID  string `json:"profileId"`
	Provider   string `json:"provider"`
	Model      string `json:"model,omitempty"`
	Effort     string `json:"effort,omitempty"`
	DecisionID string `json:"decisionId,omitempty"`
	// InheritFrom is an earlier attempt of the same Run whose recorded
	// checkpoint is applied to the new worktree before the provider starts, so
	// switching models never discards finished edits.
	InheritFrom string `json:"inheritFrom,omitempty"`
	// Handoff replaces the plain prompt. It must contain the verbatim goal.
	Handoff string `json:"-"`
	// routed is set only by StartWith. Legacy starts record exactly the
	// artifacts they always did.
	routed bool
}

// LaunchFactory builds an execution for an explicit profile.
type LaunchFactory func(id, cwd string, l Launch) (providers.Execution, error)

// AttemptResult is reported after the worker slot is released. It carries
// process/verification facts only; routing classifies them.
type AttemptResult struct {
	RunID           string
	ExecutionID     string
	Launch          Launch
	Routed          bool
	Stage           string // prepare | provider | verify | done
	ProviderStatus  string // success | failed | interrupted | unknown | not_started
	Diagnostics     string // provider reason/stderr; not the model's answer on success
	Answer          string // bounded final model text (untrusted)
	Denials         int
	ObservedModel   string
	Usage           *providers.Usage
	CostUSD         *float64
	ExecMS          int64
	VerifyMS        int64
	RunState        string
	Checks          []Check
	Workspace       string
	BaseCommit      string
	ConversationID  string // provider-native session/thread ID of this attempt
	QuiesceVerified bool
	QuiesceDetail   string
}

func (w *Worker) SetLaunchFactory(f LaunchFactory) {
	w.mu.Lock()
	w.launch = f
	w.mu.Unlock()
}

// SetAttemptObserver receives every finished attempt after the slot is free,
// so the observer may start the next attempt itself.
func (w *Worker) SetAttemptObserver(f func(AttemptResult)) {
	w.mu.Lock()
	w.observer = f
	w.mu.Unlock()
}

// Interrupt stops the Run's current attempt without canceling the Run, so a
// switch can follow once the attempt has quiesced. It never starts anything.
func (w *Worker) Interrupt(id string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.run != id || w.cancel == nil {
		return ErrConflict
	}
	w.cancel()
	return nil
}

// Busy reports whether an attempt currently owns the worker slot.
func (w *Worker) Busy() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.cancel != nil
}

// StartWith starts an attempt with an explicit profile. InheritFrom must name
// an earlier attempt of this Run that recorded a checkpoint.
func (w *Worker) StartWith(id string, l Launch) (string, error) {
	if l.Provider == "" {
		return "", fmt.Errorf("%w: launch provider required", ErrInvalid)
	}
	if l.InheritFrom != "" {
		run, err := w.store.Get(id)
		if err != nil {
			return "", err
		}
		found := false
		for _, e := range run.Executions {
			if e.ID == l.InheritFrom {
				found = true
			}
		}
		if !found {
			return "", fmt.Errorf("%w: inherited attempt does not belong to this run", ErrInvalid)
		}
		if _, err := w.store.Artifact(id, l.InheritFrom, "checkpoint.patch"); err != nil {
			return "", fmt.Errorf("%w: inherited attempt has no checkpoint", ErrInvalid)
		}
	}
	l.routed = true
	return w.start(id, &l)
}

// gitEnv is git() with extra environment entries (e.g. a temporary index).
func gitEnv(ctx context.Context, dir string, env []string, stdin []byte, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "GIT_") {
			cmd.Env = append(cmd.Env, entry)
		}
	}
	cmd.Env = append(cmd.Env, "GIT_TERMINAL_PROMPT=0")
	cmd.Env = append(cmd.Env, env...)
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var output boundedOutput
	cmd.Stdout = &output
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git operation failed: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return output.String(), nil
}

// ManifestEntry mirrors routing.FileEntry without importing routing.
type ManifestEntry struct {
	Path      string `json:"path"`
	Status    string `json:"status"`
	SHA256    string `json:"sha256,omitempty"`
	Size      int64  `json:"size,omitempty"`
	Sensitive bool   `json:"sensitive,omitempty"`
}

type Checkpoint struct {
	BaseCommit string          `json:"baseCommit"`
	Files      []ManifestEntry `json:"files"`
	Excluded   []string        `json:"excluded,omitempty"`
}

// SensitivePath is injected by the application (routing.IsSensitivePath) so
// orchestration does not depend on routing policy.
var SensitivePath = func(string) bool { return false }

// writeCheckpoint records every change relative to base — tracked edits and
// non-ignored untracked files — using a temporary index, so the worktree's own
// index and files are untouched. Sensitive untracked files are left out and
// listed instead.
func writeCheckpoint(ctx context.Context, worktree, dir, base string) (patchPath, manifestPath string, err error) {
	idx := filepath.Join(dir, "checkpoint.index")
	defer os.Remove(idx)
	env := []string{"GIT_INDEX_FILE=" + idx}
	if _, err = gitEnv(ctx, worktree, env, nil, "read-tree", base); err != nil {
		return
	}
	if _, err = gitEnv(ctx, worktree, env, nil, "add", "-u", "--", "."); err != nil {
		return
	}
	// Listed against the temporary (base) index so files the provider committed
	// after base also count as changes.
	others, err := gitEnv(ctx, worktree, env, nil, "ls-files", "--others", "--exclude-standard", "-z")
	if err != nil {
		return
	}
	var add []string
	cp := Checkpoint{BaseCommit: base}
	for _, p := range strings.Split(others, "\x00") {
		if p == "" {
			continue
		}
		if SensitivePath(p) {
			cp.Excluded = append(cp.Excluded, p)
			continue
		}
		add = append(add, p)
	}
	if len(add) > 0 {
		if _, err = gitEnv(ctx, worktree, env, []byte(strings.Join(add, "\x00")), "add", "--pathspec-from-file=-", "--pathspec-file-nul", "--"); err != nil {
			return
		}
	}
	patch, err := gitEnv(ctx, worktree, env, nil, "diff", "--cached", "--no-ext-diff", "--no-textconv", "--binary", base, "--")
	if err != nil {
		return
	}
	names, err := gitEnv(ctx, worktree, env, nil, "diff", "--cached", "--no-renames", "--name-status", "-z", base, "--")
	if err != nil {
		return
	}
	fields := strings.Split(names, "\x00")
	for i := 0; i+1 < len(fields); i += 2 {
		status, p := fields[i], fields[i+1]
		e := ManifestEntry{Path: p, Sensitive: SensitivePath(p)}
		switch status {
		case "A":
			e.Status = "added"
		case "D":
			e.Status = "deleted"
		default:
			e.Status = "modified"
		}
		if e.Status != "deleted" {
			if f, openErr := os.Open(filepath.Join(worktree, filepath.FromSlash(p))); openErr == nil {
				h := sha256.New()
				n, _ := io.Copy(h, io.LimitReader(f, 64<<20))
				f.Close()
				e.Size = n
				e.SHA256 = hex.EncodeToString(h.Sum(nil))
			}
		}
		cp.Files = append(cp.Files, e)
	}
	manifest, _ := json.MarshalIndent(cp, "", "  ")
	patchPath, manifestPath = filepath.Join(dir, "checkpoint.patch"), filepath.Join(dir, "checkpoint.json")
	if err = writeExclusive(patchPath, []byte(patch)); err != nil {
		return
	}
	err = writeExclusive(manifestPath, manifest)
	return
}

// saveCheckpoint snapshots the workspace only once nothing is still running
// in it; a snapshot taken while a tool writes would not be a checkpoint.
func (w *Worker) saveCheckpoint(ctx context.Context, id, worktree, dir, base string) error {
	wait := w.quiesceWait
	if wait <= 0 {
		wait = 10 * time.Second
	}
	// Where no process scan exists the snapshot is still taken; the attempt
	// then reports quiescence as unverified and routing requires a person to
	// reconcile before any automatic next writer.
	if ok, detail := quiesce(worktree, wait); !ok && runtime.GOOS == "linux" {
		return fmt.Errorf("workspace not quiescent: %s", detail)
	}
	patchPath, manifestPath, err := writeCheckpoint(ctx, worktree, dir, base)
	if err != nil {
		return err
	}
	for _, kind := range []string{"checkpoint.patch", "checkpoint.json"} {
		path := patchPath
		if kind == "checkpoint.json" {
			path = manifestPath
		}
		if err := w.store.AddArtifact(id, kind, path, base); err != nil {
			return err
		}
	}
	return nil
}

// applyCheckpoint applies an earlier attempt's checkpoint to a fresh worktree
// at the same base. A patch that does not apply cleanly fails the attempt
// rather than starting from a partial state.
func applyCheckpoint(ctx context.Context, worktree, patchPath string) error {
	info, err := os.Stat(patchPath)
	if err != nil {
		return err
	}
	if info.Size() == 0 {
		return nil
	}
	_, err = gitEnv(ctx, worktree, nil, nil, "apply", "--binary", "--whitespace=nowarn", patchPath)
	return err
}

// quiesce waits for every process whose working directory is inside the
// worktree to disappear. Only a positive observation counts: platforms without
// /proc report unverified, and the caller must not start a second writer
// automatically.
func quiesce(worktree string, wait time.Duration) (bool, string) {
	if runtime.GOOS != "linux" {
		return false, "process scan unsupported on " + runtime.GOOS
	}
	deadline := time.Now().Add(wait)
	for {
		left := processesIn(worktree)
		if len(left) == 0 {
			return true, "no process has its working directory in the attempt workspace"
		}
		if time.Now().After(deadline) {
			return false, fmt.Sprintf("%d process(es) still running in the workspace: pid %s", len(left), strings.Join(left, ","))
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func processesIn(root string) []string {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return []string{"?"}
	}
	self := strconv.Itoa(os.Getpid())
	var out []string
	for _, e := range entries {
		if _, err := strconv.Atoi(e.Name()); err != nil || e.Name() == self {
			continue
		}
		cwd, err := os.Readlink(filepath.Join("/proc", e.Name(), "cwd"))
		if err != nil {
			continue
		}
		if cwd == root || strings.HasPrefix(cwd, root+string(filepath.Separator)) {
			out = append(out, e.Name())
		}
	}
	return out
}
