package orchestration

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"unicode/utf8"
)

// Planning evidence is bounded like review evidence, but a plan must be possible
// for a repository whose contents exceed the budget. So the tracked listing is
// always complete and every file whose content is left out is named with a
// reason. Nothing is dropped silently.
const (
	maxPlanInput    = 64 * 1024
	maxPlanFiles    = 2000
	maxPlanFileSize = 8 * 1024
)

// Every reason a file's content can be left out. longestPlanReason bounds the
// budget reservation, so it must stay the longest of them.
var (
	reasonBudget      = "plan evidence budget exhausted"
	reasonUnavailable = "unavailable in the planning workspace"
	reasonUnreadable  = "unreadable in the planning workspace"
	reasonIrregular   = "not a regular file"
	reasonBinary      = "binary or not UTF-8"
	reasonTooLarge    = fmt.Sprintf("larger than %d bytes", maxPlanFileSize)
	longestPlanReason = longest(reasonBudget, reasonUnavailable, reasonUnreadable, reasonIrregular, reasonBinary, reasonTooLarge)
)

func longest(values ...string) string {
	out := ""
	for _, value := range values {
		if len(value) > len(out) {
			out = value
		}
	}
	return out
}

type planEvidence struct {
	Base     string            `json:"base_commit"`
	Files    []string          `json:"files"`
	Contents map[string]string `json:"contents"`
	Omitted  map[string]string `json:"omitted_contents"`
}

// collectPlanEvidence reads the planning worktree on the host so the planner
// needs no tool permissions. Contents are chosen smallest first, which is
// deterministic and covers the most files within the budget.
func collectPlanEvidence(ctx context.Context, root, base string) (string, error) {
	resolved, err := git(ctx, root, "rev-parse", "--verify", "--end-of-options", base+"^{commit}")
	if err != nil {
		return "", err
	}
	list, err := git(ctx, root, "ls-files", "-z")
	if err != nil {
		return "", err
	}
	evidence := planEvidence{
		Base:     strings.TrimSpace(resolved),
		Files:    []string{},
		Contents: map[string]string{},
		Omitted:  map[string]string{},
	}
	for _, name := range strings.Split(list, "\x00") {
		if name == "" {
			continue
		}
		if !utf8.ValidString(name) {
			return "", fmt.Errorf("unsupported plan file list")
		}
		if len(evidence.Files) >= maxPlanFiles {
			return "", fmt.Errorf("repository has more than %d tracked files", maxPlanFiles)
		}
		evidence.Files = append(evidence.Files, name)
	}
	if len(evidence.Files) == 0 {
		return "", fmt.Errorf("plan evidence has no tracked files")
	}
	sort.Strings(evidence.Files)
	skeleton, err := json.Marshal(evidence)
	if err != nil {
		return "", err
	}
	used := len(skeleton)
	if used > maxPlanInput {
		return "", fmt.Errorf("plan file listing alone exceeds %d bytes", maxPlanInput)
	}
	rootFS, err := os.OpenRoot(root)
	if err != nil {
		return "", err
	}
	defer rootFS.Close()
	type candidate struct {
		name string
		size int64
	}
	candidates := []candidate{}
	for _, name := range evidence.Files {
		info, err := rootFS.Lstat(name)
		if err != nil {
			evidence.Omitted[name] = reasonUnavailable
			continue
		}
		// Never follow a link out of the planning workspace.
		if !info.Mode().IsRegular() {
			evidence.Omitted[name] = reasonIrregular
			continue
		}
		if info.Size() > maxPlanFileSize {
			evidence.Omitted[name] = reasonTooLarge
			continue
		}
		candidates = append(candidates, candidate{name, info.Size()})
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].size != candidates[j].size {
			return candidates[i].size < candidates[j].size
		}
		return candidates[i].name < candidates[j].name
	})
	// Omission reasons are themselves part of the payload. Reserve the worst-case
	// reason for every candidate up front, so a file can always be reported as
	// omitted; including its content then releases that reservation.
	cost := func(key, value string) int {
		k, _ := json.Marshal(key)
		v, _ := json.Marshal(value)
		return len(k) + len(v) + 2
	}
	for name, reason := range evidence.Omitted {
		used += cost(name, reason)
	}
	for _, item := range candidates {
		used += cost(item.name, longestPlanReason)
	}
	if used > maxPlanInput {
		return "", fmt.Errorf("plan file listing alone exceeds %d bytes", maxPlanInput)
	}
	exhausted := false
	for _, item := range candidates {
		if exhausted {
			evidence.Omitted[item.name] = reasonBudget
			continue
		}
		used -= cost(item.name, longestPlanReason)
		omit := func(reason string) {
			evidence.Omitted[item.name] = reason
			used += cost(item.name, reason)
		}
		file, err := rootFS.Open(item.name)
		if err != nil {
			omit(reasonUnreadable)
			continue
		}
		content, readErr := io.ReadAll(io.LimitReader(file, maxPlanFileSize+1))
		closeErr := file.Close()
		if readErr != nil {
			return "", readErr
		}
		if closeErr != nil {
			return "", closeErr
		}
		if len(content) > maxPlanFileSize {
			omit(reasonTooLarge)
			continue
		}
		if !utf8.Valid(content) || strings.IndexByte(string(content), 0) >= 0 {
			omit(reasonBinary)
			continue
		}
		if used+cost(item.name, string(content)) > maxPlanInput {
			exhausted = true
			omit(reasonBudget)
			continue
		}
		used += cost(item.name, string(content))
		evidence.Contents[item.name] = string(content)
	}
	data, err := json.Marshal(evidence)
	if err != nil {
		return "", err
	}
	if len(data) > maxPlanInput {
		return "", fmt.Errorf("serialized plan evidence exceeds %d bytes", maxPlanInput)
	}
	return string(data), nil
}
