package orchestration

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

type ConflictVersion struct {
	Mode        string `json:"mode,omitempty"`
	Artifact    string `json:"artifact,omitempty"`
	Unavailable string `json:"unavailable,omitempty"`
}
type ConflictFile struct {
	Path     string          `json:"path"`
	Base     ConflictVersion `json:"base"`
	Current  ConflictVersion `json:"current"`
	Incoming ConflictVersion `json:"incoming"`
}
type ConflictReport struct {
	Fingerprint    string         `json:"fingerprint"`
	BaseCommit     string         `json:"baseCommit"`
	IncomingCommit string         `json:"incomingCommit"`
	Files          []ConflictFile `json:"files"`
	Truncated      bool           `json:"truncated"`
}

var objectID = regexp.MustCompile(`^(?:[0-9a-f]{40}|[0-9a-f]{64})$`)

// captureConflict copies index stages, not mutable workspace paths. Symlink
// stages are blob text, never followed; gitlinks/binary/large data are metadata.
func captureConflict(ctx context.Context, cwd, dir, id, base, incoming, detail string, save func(string, string, string, string) error) error {
	logPath := filepath.Join(dir, "integration.log")
	if len(detail) > 8*1024*1024 {
		detail = detail[:8*1024*1024]
	}
	if err := writeExclusive(logPath, []byte(detail)); err != nil {
		return err
	}
	if err := save(id, "integration_log", logPath, base); err != nil {
		return err
	}
	list, err := git(ctx, cwd, "ls-files", "--unmerged", "-z")
	if err != nil {
		return err
	}
	report := ConflictReport{Fingerprint: fmt.Sprintf("%x", sha256.Sum256([]byte(list))), BaseCommit: base, IncomingCommit: incoming, Files: []ConflictFile{}}
	indexes := map[string]int{}
	total := 0
	for _, entry := range strings.Split(list, "\x00") {
		if entry == "" {
			continue
		}
		header, path, ok := strings.Cut(entry, "\t")
		if !ok {
			return fmt.Errorf("invalid conflict index entry")
		}
		fields := strings.Fields(header)
		if len(fields) != 3 || !objectID.MatchString(fields[1]) {
			return fmt.Errorf("invalid conflict stage")
		}
		i, exists := indexes[path]
		if !exists {
			if len(report.Files) >= 256 {
				report.Truncated = true
				continue
			}
			i = len(report.Files)
			indexes[path] = i
			absent := ConflictVersion{Unavailable: "deleted_or_absent"}
			report.Files = append(report.Files, ConflictFile{Path: path, Base: absent, Current: absent, Incoming: absent})
		}
		var version *ConflictVersion
		switch fields[2] {
		case "1":
			version = &report.Files[i].Base
		case "2":
			version = &report.Files[i].Current
		case "3":
			version = &report.Files[i].Incoming
		default:
			return fmt.Errorf("unknown conflict stage")
		}
		version.Mode = fields[0]
		if fields[0] == "160000" {
			version.Unavailable = "submodule"
			continue
		}
		sizeText, err := git(ctx, cwd, "cat-file", "-s", fields[1])
		if err != nil {
			return err
		}
		size, err := strconv.Atoi(strings.TrimSpace(sizeText))
		if err != nil || size < 0 {
			return fmt.Errorf("invalid conflict blob size")
		}
		if size > 256*1024 || total+size > 4*1024*1024 {
			version.Unavailable = "size_limit"
			continue
		}
		total += size
		text, err := git(ctx, cwd, "cat-file", "blob", fields[1])
		if err != nil {
			return err
		}
		if strings.ContainsRune(text, '\x00') || !utf8.ValidString(text) {
			version.Unavailable = "binary"
			continue
		}
		kind := fmt.Sprintf("conflict:%d:%s", i, fields[2])
		output := filepath.Join(dir, fmt.Sprintf("conflict-%d-%s.txt", i, fields[2]))
		if err := writeExclusive(output, []byte(text)); err != nil {
			return err
		}
		if err := save(id, kind, output, base); err != nil {
			return err
		}
		*version = ConflictVersion{Artifact: kind, Mode: fields[0]}
	}
	if len(report.Files) == 0 {
		return nil
	}
	raw, err := json.Marshal(report)
	if err != nil {
		return err
	}
	output := filepath.Join(dir, "conflicts.json")
	if err := writeExclusive(output, raw); err != nil {
		return err
	}
	return save(id, "conflicts", output, base)
}
