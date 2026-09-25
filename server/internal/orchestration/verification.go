package orchestration

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const checkManifest = ".powercodedeck/checks.json"

var checkName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$`)

type checkManifestFile struct {
	Version int         `json:"version"`
	Checks  []CheckSpec `json:"checks"`
}

type CheckSpec struct {
	Name           string   `json:"name"`
	Cwd            string   `json:"cwd,omitempty"`
	Argv           []string `json:"argv"`
	TimeoutSeconds int      `json:"timeoutSeconds,omitempty"`
}

func loadVerificationPlan(root string) ([]CheckSpec, error) {
	path := filepath.Join(root, checkManifest)
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read verification manifest: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() > 64*1024 {
		return nil, fmt.Errorf("%w: verification manifest must be a regular file up to 64 KiB", ErrInvalid)
	}
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, fmt.Errorf("read verification manifest: %w", err)
	}
	resolvedPath, err := filepath.EvalSymlinks(path)
	if err != nil {
		return nil, fmt.Errorf("read verification manifest: %w", err)
	}
	rel, err := filepath.Rel(resolvedRoot, resolvedPath)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return nil, fmt.Errorf("%w: verification manifest escapes the workspace", ErrInvalid)
	}
	f, err := os.Open(resolvedPath)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	decoder := json.NewDecoder(io.LimitReader(f, 64*1024+1))
	decoder.DisallowUnknownFields()
	var manifest checkManifestFile
	if err := decoder.Decode(&manifest); err != nil {
		return nil, fmt.Errorf("%w: invalid verification manifest: %v", ErrInvalid, err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return nil, fmt.Errorf("%w: verification manifest must contain one object", ErrInvalid)
	}
	if manifest.Version != 1 || len(manifest.Checks) > 16 {
		return nil, fmt.Errorf("%w: unsupported verification manifest", ErrInvalid)
	}
	seen := map[string]bool{"diff_check": true, "review": true}
	for i := range manifest.Checks {
		check := &manifest.Checks[i]
		if !checkName.MatchString(check.Name) || seen[check.Name] || len(check.Argv) == 0 || len(check.Argv) > 64 {
			return nil, fmt.Errorf("%w: invalid or duplicate check at index %d", ErrInvalid, i)
		}
		seen[check.Name] = true
		if check.Cwd == "" {
			check.Cwd = "."
		}
		clean := filepath.Clean(check.Cwd)
		if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
			return nil, fmt.Errorf("%w: check %q cwd escapes the workspace", ErrInvalid, check.Name)
		}
		check.Cwd = clean
		total := 0
		for _, arg := range check.Argv {
			total += len(arg)
			if strings.ContainsRune(arg, 0) {
				return nil, fmt.Errorf("%w: check %q contains a NUL argument", ErrInvalid, check.Name)
			}
		}
		if strings.TrimSpace(check.Argv[0]) == "" || total > 32*1024 || check.TimeoutSeconds < 0 || check.TimeoutSeconds > 1800 {
			return nil, fmt.Errorf("%w: check %q exceeds limits", ErrInvalid, check.Name)
		}
		if check.TimeoutSeconds == 0 {
			check.TimeoutSeconds = 600
		}
	}
	return manifest.Checks, nil
}

type cappedLog struct {
	file      *os.File
	written   int64
	truncated bool
}

func (w *cappedLog) Write(p []byte) (int, error) {
	const limit = int64(8 * 1024 * 1024)
	n := len(p)
	if w.written >= limit {
		w.truncated = true
		return n, nil
	}
	keep := p
	if int64(len(keep)) > limit-w.written {
		keep = keep[:limit-w.written]
		w.truncated = true
	}
	written, err := w.file.Write(keep)
	w.written += int64(written)
	if err != nil {
		return written, err
	}
	return n, nil
}

func scrubbedEnvironment(home string) ([]string, error) {
	if err := os.MkdirAll(home, 0700); err != nil {
		return nil, err
	}
	allowed := map[string]bool{
		"PATH": true, "TMPDIR": true, "TMP": true, "TEMP": true,
		"LANG": true, "SYSTEMROOT": true, "WINDIR": true,
		"COMSPEC": true, "PATHEXT": true,
	}
	env := make([]string, 0, 16)
	for _, entry := range os.Environ() {
		name := strings.ToUpper(strings.SplitN(entry, "=", 2)[0])
		if allowed[name] || strings.HasPrefix(name, "LC_") {
			env = append(env, entry)
		}
	}
	env = append(env, "HOME="+home, "USERPROFILE="+home, "XDG_CACHE_HOME="+filepath.Join(home, ".cache"))
	return append(env, "CI=true", "GIT_TERMINAL_PROMPT=0", "NO_COLOR=1"), nil
}

func runCheck(parent context.Context, root, artifactDir string, check CheckSpec) (bool, string, string, error) {
	cwd := filepath.Join(root, check.Cwd)
	resolved, err := filepath.EvalSymlinks(cwd)
	if err != nil {
		return false, "", "", fmt.Errorf("check %q cwd: %w", check.Name, err)
	}
	rel, err := filepath.Rel(root, resolved)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false, "", "", fmt.Errorf("%w: check %q cwd escapes the workspace", ErrInvalid, check.Name)
	}
	logPath := filepath.Join(artifactDir, "check-"+check.Name+".log")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return false, "", "", err
	}
	writer := &cappedLog{file: logFile}
	env, envErr := scrubbedEnvironment(filepath.Join(artifactDir, "check-home"))
	if envErr != nil {
		logFile.Close()
		return false, "", logPath, envErr
	}
	ctx, cancel := context.WithTimeout(parent, time.Duration(check.TimeoutSeconds)*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, check.Argv[0], check.Argv[1:]...)
	cmd.Dir, cmd.Env, cmd.Stdout, cmd.Stderr = resolved, env, writer, writer
	runErr := cmd.Run()
	closeErr := logFile.Close()
	if closeErr != nil && runErr == nil {
		runErr = closeErr
	}
	detail := strings.Join(check.Argv, " ")
	if ctx.Err() != nil {
		detail += ": " + ctx.Err().Error()
	} else if runErr != nil {
		detail += ": " + runErr.Error()
	} else {
		detail += ": passed"
	}
	if writer.truncated {
		detail += " (log truncated at 8 MiB)"
	}
	// Include a bounded final line to make the Run useful without opening artifacts.
	if f, err := os.Open(logPath); err == nil {
		scanner := bufio.NewScanner(f)
		var last string
		for scanner.Scan() {
			if text := strings.TrimSpace(scanner.Text()); text != "" {
				last = text
			}
		}
		f.Close()
		if len(last) > 500 {
			last = last[:500]
		}
		if last != "" {
			detail += "; last output: " + last
		}
	}
	return runErr == nil && ctx.Err() == nil, detail, logPath, nil
}
