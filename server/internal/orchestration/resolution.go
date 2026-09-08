package orchestration

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

type ResolvedFile struct {
	Path    string `json:"path"`
	Content string `json:"content"`
	Delete  bool   `json:"delete"`
}
type ResolutionRequest struct {
	SourceAttempt string         `json:"sourceAttempt"`
	Fingerprint   string         `json:"fingerprint"`
	Files         []ResolvedFile `json:"files"`
}
type conflictResolution struct {
	Fingerprint    string         `json:"fingerprint"`
	IncomingCommit string         `json:"incomingCommit"`
	Files          []ResolvedFile `json:"files"`
}
type resolutionPlan struct {
	SourceAttempt string               `json:"sourceAttempt"`
	Resolutions   []conflictResolution `json:"resolutions"`
}

func validResolutionFiles(files []ResolvedFile) error {
	if len(files) == 0 || len(files) > 256 {
		return ErrInvalid
	}
	seen := map[string]bool{}
	total := 0
	for _, f := range files {
		if f.Path == "" || !utf8.ValidString(f.Path) || strings.ContainsRune(f.Path, '\x00') || strings.Contains(f.Path, "\\") || filepath.IsAbs(f.Path) || filepath.ToSlash(filepath.Clean(f.Path)) != f.Path {
			return fmt.Errorf("%w: invalid conflict path", ErrInvalid)
		}
		for _, part := range strings.Split(f.Path, "/") {
			if part == ".." || strings.EqualFold(part, ".git") {
				return ErrInvalid
			}
		}
		if seen[f.Path] || !utf8.ValidString(f.Content) || strings.ContainsRune(f.Content, '\x00') || len(f.Content) > 256*1024 || (f.Delete && f.Content != "") {
			return ErrInvalid
		}
		seen[f.Path] = true
		total += len(f.Content)
		if total > 4*1024*1024 {
			return ErrInvalid
		}
	}
	return nil
}

// Only the latest failed integration can seed repair. The immutable conflict
// fingerprint binds user input to the exact Git index stages they inspected.
func (w *Worker) loadResolution(run string, request ResolutionRequest) (*resolutionPlan, error) {
	if err := validResolutionFiles(request.Files); err != nil {
		return nil, err
	}
	var latest, state string
	if err := w.store.db.QueryRow(`SELECT id,state FROM v2_integrations WHERE run_id=? ORDER BY ordinal DESC LIMIT 1`, run).Scan(&latest, &state); err != nil {
		return nil, err
	}
	if latest != request.SourceAttempt || state != "failed" {
		return nil, ErrConflict
	}
	raw, err := w.ReadArtifact(run, latest, "conflicts")
	if err != nil {
		return nil, err
	}
	var report ConflictReport
	if err := json.Unmarshal(raw, &report); err != nil {
		return nil, err
	}
	if report.Fingerprint == "" || report.Fingerprint != request.Fingerprint || report.Truncated || len(report.Files) != len(request.Files) {
		return nil, ErrConflict
	}
	allowed := map[string]bool{}
	for _, f := range report.Files {
		for _, v := range []ConflictVersion{f.Base, f.Current, f.Incoming} {
			if v.Mode != "" && v.Mode != "100644" && v.Mode != "100755" {
				return nil, fmt.Errorf("%w: only regular files support direct repair", ErrInvalid)
			}
			if v.Unavailable != "" && v.Unavailable != "deleted_or_absent" {
				return nil, fmt.Errorf("%w: only complete text conflicts support direct repair", ErrInvalid)
			}
		}
		allowed[f.Path] = true
	}
	for _, f := range request.Files {
		if !allowed[f.Path] {
			return nil, ErrInvalid
		}
	}
	plan := &resolutionPlan{}
	previous, err := w.ReadArtifact(run, latest, "resolution_plan")
	if err == nil {
		if err = json.Unmarshal(previous, plan); err != nil {
			return nil, err
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	if len(plan.Resolutions) >= 64 {
		return nil, fmt.Errorf("%w: repair chain limit reached", ErrInvalid)
	}
	plan.SourceAttempt = latest
	plan.Resolutions = append(plan.Resolutions, conflictResolution{Fingerprint: request.Fingerprint, IncomingCommit: report.IncomingCommit, Files: request.Files})
	// Bound the accumulated recipe as well as each individual submission.
	encoded, err := json.Marshal(plan)
	if err != nil {
		return nil, err
	}
	if len(encoded) > 6*1024*1024 {
		return nil, fmt.Errorf("%w: resolution recipe too large", ErrInvalid)
	}
	return plan, nil
}

// applyConflictResolution touches only regular files in a newly allocated
// integration worktree. It never reads or edits the failed attempt's workspace.
func applyConflictResolution(ctx context.Context, cwd string, resolution conflictResolution) error {
	if err := validResolutionFiles(resolution.Files); err != nil {
		return err
	}
	list, err := git(ctx, cwd, "ls-files", "--unmerged", "-z")
	if err != nil {
		return err
	}
	if list == "" || fmt.Sprintf("%x", sha256.Sum256([]byte(list))) != resolution.Fingerprint {
		return fmt.Errorf("%w: conflict changed since inspection", ErrConflict)
	}
	paths := map[string]bool{}
	modes := map[string]string{}
	priority := map[string]int{}
	for _, entry := range strings.Split(list, "\x00") {
		if entry == "" {
			continue
		}
		header, path, ok := strings.Cut(entry, "\t")
		fields := strings.Fields(header)
		if !ok || len(fields) != 3 || (fields[0] != "100644" && fields[0] != "100755") {
			return fmt.Errorf("%w: only regular text files can be repaired", ErrInvalid)
		}
		paths[path] = true
		rank := 1
		if fields[2] == "3" {
			rank = 2
		}
		if fields[2] == "2" {
			rank = 3
		}
		if rank > priority[path] {
			modes[path] = fields[0]
			priority[path] = rank
		}
	}
	if len(paths) != len(resolution.Files) {
		return ErrConflict
	}
	for _, file := range resolution.Files {
		if !paths[file.Path] {
			return ErrInvalid
		}
	}
	for _, file := range resolution.Files {
		path := filepath.Join(cwd, filepath.FromSlash(file.Path))
		// Reject symlink ancestors, including ones created by a conflicting tree.
		for parent := filepath.Dir(path); parent != cwd; parent = filepath.Dir(parent) {
			info, err := os.Lstat(parent)
			if err != nil {
				return err
			}
			if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
				return ErrInvalid
			}
		}
		info, err := os.Lstat(path)
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		if err == nil && !info.Mode().IsRegular() {
			return ErrInvalid
		}
		if file.Delete {
			if err == nil {
				if err := os.Remove(path); err != nil {
					return err
				}
			}
			if _, err := git(ctx, cwd, "--literal-pathspecs", "rm", "--force", "--", file.Path); err != nil {
				return err
			}
		} else {
			if err := os.WriteFile(path, []byte(file.Content), 0600); err != nil {
				return err
			}
			mode := os.FileMode(0644)
			if modes[file.Path] == "100755" {
				mode = 0755
			}
			if err := os.Chmod(path, mode); err != nil {
				return err
			}
			if _, err := git(ctx, cwd, "--literal-pathspecs", "add", "--", file.Path); err != nil {
				return err
			}
			flag := "--chmod=-x"
			if modes[file.Path] == "100755" {
				flag = "--chmod=+x"
			}
			if _, err := git(ctx, cwd, "--literal-pathspecs", "update-index", flag, "--", file.Path); err != nil {
				return err
			}
		}
	}
	remaining, err := git(ctx, cwd, "ls-files", "--unmerged", "-z")
	if err != nil {
		return err
	}
	if remaining != "" {
		return fmt.Errorf("unresolved index entries remain")
	}
	_, err = git(ctx, cwd, "cherry-pick", "--quit")
	return err
}
