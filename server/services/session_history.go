package services

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// SessionSummary is one past Claude Code session (a single transcript file) for a
// project working dir. Transcripts live at ~/.claude/projects/<enc>/*.jsonl and
// survive the session/process, so they can be browsed after a session has ended.
type SessionSummary struct {
	ID           string `json:"id"` // Claude session UUID = transcript filename (no .jsonl)
	StartedAt    string `json:"startedAt"`
	LastAt       string `json:"lastAt"`
	MessageCount int    `json:"messageCount"`
	Preview      string `json:"preview"` // first user message, trimmed
}

// SessionMessage is one rendered turn of a past session.
type SessionMessage struct {
	Role      string `json:"role"` // "user" | "assistant"
	Text      string `json:"text"`
	Timestamp string `json:"timestamp"`
}

// sessionIDRe guards against path traversal — a session id is a transcript
// filename stem (Claude uses UUIDs); only these characters are allowed.
var sessionIDRe = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// sessionTextBlock is a content block we care about when rendering a transcript.
type sessionTextBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
	Name string `json:"name"`
}

// extractSessionText pulls human-readable text out of a transcript line's content
// (a plain string, or an array of blocks). tool_use blocks are shown as a marker.
func extractSessionText(line transcriptLine) string {
	if line.Message == nil || len(line.Message.Content) == 0 {
		return ""
	}
	var str string
	if json.Unmarshal(line.Message.Content, &str) == nil {
		return str
	}
	var blocks []sessionTextBlock
	if json.Unmarshal(line.Message.Content, &blocks) != nil {
		return ""
	}
	var parts []string
	for _, b := range blocks {
		switch b.Type {
		case "text":
			if strings.TrimSpace(b.Text) != "" {
				parts = append(parts, b.Text)
			}
		case "tool_use":
			if b.Name != "" {
				parts = append(parts, "🔧 "+b.Name)
			}
		}
	}
	return strings.Join(parts, "\n")
}

func truncateRunes(s string, n int) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\n", " "))
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// ListSessions returns the past sessions for a project working dir, newest first.
func ListSessions(workingDir string) ([]SessionSummary, error) {
	dir := claudeProjectDir(workingDir)
	out := []SessionSummary{}
	if dir == "" {
		return out, nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return out, nil
		}
		return nil, err
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		id := strings.TrimSuffix(e.Name(), ".jsonl")
		out = append(out, summarizeSession(filepath.Join(dir, e.Name()), id))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].LastAt > out[j].LastAt })
	return out, nil
}

func summarizeSession(path, id string) SessionSummary {
	s := SessionSummary{ID: id}
	f, err := os.Open(path)
	if err != nil {
		return s
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1024*1024), 16*1024*1024)
	for sc.Scan() {
		var line transcriptLine
		if json.Unmarshal(sc.Bytes(), &line) != nil {
			continue
		}
		if (line.Type != "user" && line.Type != "assistant") || line.IsSidechain {
			continue
		}
		if line.Timestamp != "" {
			if s.StartedAt == "" {
				s.StartedAt = line.Timestamp
			}
			s.LastAt = line.Timestamp
		}
		text := extractSessionText(line)
		if strings.TrimSpace(text) == "" {
			continue
		}
		s.MessageCount++
		if s.Preview == "" && line.Type == "user" {
			s.Preview = truncateRunes(text, 100)
		}
	}
	return s
}

// ReadSession parses a session's transcript into rendered user/assistant turns.
func ReadSession(workingDir, sessionID string) ([]SessionMessage, error) {
	if !sessionIDRe.MatchString(sessionID) {
		return nil, os.ErrInvalid
	}
	dir := claudeProjectDir(workingDir)
	if dir == "" {
		return nil, os.ErrNotExist
	}
	f, err := os.Open(filepath.Join(dir, sessionID+".jsonl"))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	msgs := []SessionMessage{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1024*1024), 16*1024*1024)
	for sc.Scan() {
		var line transcriptLine
		if json.Unmarshal(sc.Bytes(), &line) != nil {
			continue
		}
		if (line.Type != "user" && line.Type != "assistant") || line.IsSidechain {
			continue
		}
		text := extractSessionText(line)
		if strings.TrimSpace(text) == "" {
			continue
		}
		msgs = append(msgs, SessionMessage{Role: line.Type, Text: text, Timestamp: line.Timestamp})
	}
	return msgs, nil
}

// DeleteSession removes a session's transcript file.
func DeleteSession(workingDir, sessionID string) error {
	if !sessionIDRe.MatchString(sessionID) {
		return os.ErrInvalid
	}
	dir := claudeProjectDir(workingDir)
	if dir == "" {
		return os.ErrNotExist
	}
	return os.Remove(filepath.Join(dir, sessionID+".jsonl"))
}
