package orchestration

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"unicode/utf8"
)

// Keep the complete serialized input below common per-argument OS limits.
// Never silently truncate evidence to fit the model request.
const maxReviewInput = 64 * 1024

type reviewEvidence struct {
	Task      string            `json:"task"`
	Base      string            `json:"base_commit"`
	Patch     string            `json:"patch"`
	Untracked map[string]string `json:"untracked_files"`
}

func collectReviewEvidence(ctx context.Context, root string, run Run, base string) (string, error) {
	resolved, err := git(ctx, root, "rev-parse", "--verify", "--end-of-options", base+"^{commit}")
	if err != nil {
		return "", err
	}
	base = strings.TrimSpace(resolved)
	// Disable rename heuristics, external diff/textconv and submodule suppression.
	args := []string{"diff", "--no-ext-diff", "--no-textconv", "--no-renames", "--ignore-submodules=none", "--no-color"}
	stats, err := git(ctx, root, append(append([]string{}, args...), "--numstat", "-z", base, "--")...)
	if err != nil {
		return "", err
	}
	for _, entry := range strings.Split(stats, "\x00") {
		if strings.HasPrefix(entry, "-\t-\t") {
			return "", fmt.Errorf("review evidence contains binary changes")
		}
	}
	raw, err := git(ctx, root, append(append([]string{}, args...), "--raw", "-z", base, "--")...)
	if err != nil {
		return "", err
	}
	parts := strings.Split(raw, "\x00")
	for i := 0; i+1 < len(parts); i += 2 {
		fields := strings.Fields(parts[i])
		if len(fields) >= 2 && (fields[0] == ":160000" || fields[1] == "160000") {
			return "", fmt.Errorf("review evidence contains submodule changes")
		}
	}
	patch, err := git(ctx, root, append(append([]string{}, args...), "--patch", "--full-index", base, "--")...)
	if err != nil {
		return "", err
	}
	if len(patch) > maxReviewInput || !utf8.ValidString(patch) || strings.IndexByte(patch, 0) >= 0 {
		return "", fmt.Errorf("review patch exceeds limit or is not UTF-8")
	}
	evidence := reviewEvidence{Task: run.Prompt, Base: base, Patch: patch, Untracked: map[string]string{}}
	list, err := git(ctx, root, "ls-files", "--others", "--exclude-standard", "-z")
	if err != nil {
		return "", err
	}
	rootFS, err := os.OpenRoot(root)
	if err != nil {
		return "", err
	}
	defer rootFS.Close()
	total := len(patch) + len(run.Prompt)
	for _, name := range strings.Split(list, "\x00") {
		if name == "" {
			continue
		}
		if !utf8.ValidString(name) || len(evidence.Untracked) >= 256 {
			return "", fmt.Errorf("unsupported review file list")
		}
		info, err := rootFS.Lstat(name)
		if err != nil {
			return "", err
		}
		// Avoid following untracked links outside the execution workspace.
		if !info.Mode().IsRegular() {
			return "", fmt.Errorf("unsupported untracked review file %q", name)
		}
		if info.Size() > int64(maxReviewInput-total) {
			return "", fmt.Errorf("review evidence exceeds 64 KiB")
		}
		file, err := rootFS.Open(name)
		if err != nil {
			return "", err
		}
		content, readErr := io.ReadAll(io.LimitReader(file, int64(maxReviewInput-total)+1))
		closeErr := file.Close()
		if readErr != nil {
			return "", readErr
		}
		if closeErr != nil {
			return "", closeErr
		}
		total += len(content)
		if total > maxReviewInput || !utf8.Valid(content) || strings.IndexByte(string(content), 0) >= 0 {
			return "", fmt.Errorf("review evidence exceeds limit or contains binary data")
		}
		evidence.Untracked[name] = string(content)
	}
	data, err := json.Marshal(evidence)
	if err != nil {
		return "", err
	}
	if len(data) > maxReviewInput {
		return "", fmt.Errorf("serialized review evidence exceeds 64 KiB")
	}
	return string(data), nil
}
