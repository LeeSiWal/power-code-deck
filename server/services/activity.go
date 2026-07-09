package services

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// ActivityManager tails Claude Code session transcripts (~/.claude/projects/<enc>/*.jsonl)
// and turns real tool_use / tool_result / sub-agent events into structured activity
// snapshots. This replaces the old client-side regex-scraping of raw terminal bytes,
// which could not reliably see tool calls behind Claude Code's full-screen TUI.
//
// One watcher goroutine runs per (claude) agent session. Snapshots are pushed to the
// emitter (wired to the WS hub in main.go) as an "agent:activity" event.
type ActivityManager struct {
	mu       sync.Mutex
	watchers map[string]*transcriptWatcher
	emit     func(agentID string, snap AgentActivitySnapshot)
}

func NewActivityManager() *ActivityManager {
	return &ActivityManager{watchers: make(map[string]*transcriptWatcher)}
}

// SetEmitter wires how snapshots leave the manager. Called once from main.go after
// the hub exists.
func (m *ActivityManager) SetEmitter(fn func(agentID string, snap AgentActivitySnapshot)) {
	m.mu.Lock()
	m.emit = fn
	m.mu.Unlock()
}

// Start begins watching an agent's transcript. It is a no-op for non-Claude presets
// (only Claude Code writes the JSONL transcripts we parse). Safe to call repeatedly —
// an existing watcher for the same id is replaced.
func (m *ActivityManager) Start(agentID, preset, command, cwd string) {
	if !isClaudeAgent(preset, command) || cwd == "" {
		return
	}
	m.Stop(agentID)

	m.mu.Lock()
	emit := m.emit
	w := &transcriptWatcher{
		agentID: agentID,
		dir:     claudeProjectDir(cwd),
		stop:    make(chan struct{}),
		nodes:   make(map[string]*activityNode),
		tools:   make(map[string]*toolRun),
		subByID: make(map[string]string),
		emit: func(snap AgentActivitySnapshot) {
			if emit != nil {
				emit(agentID, snap)
			}
		},
	}
	m.watchers[agentID] = w
	m.mu.Unlock()

	go w.run()
}

// Stop tears down the watcher for an agent (on delete/restart).
func (m *ActivityManager) Stop(agentID string) {
	m.mu.Lock()
	w := m.watchers[agentID]
	delete(m.watchers, agentID)
	m.mu.Unlock()
	if w != nil {
		close(w.stop)
	}
}

// ---- snapshot wire types (JSON-serialized to the client) ----

type ActivityNode struct {
	ID             string `json:"id"`
	Kind           string `json:"kind"` // "main" | "subagent"
	Label          string `json:"label"`
	Status         string `json:"status"` // "working" | "thinking" | "idle" | "done"
	CurrentTool    string `json:"currentTool,omitempty"`
	CurrentTarget  string `json:"currentTarget,omitempty"`
	ToolCount      int    `json:"toolCount"`
	StartedAt      int64  `json:"startedAt"`
	LastActivityAt int64  `json:"lastActivityAt"`
	Parent         string `json:"parent,omitempty"`
}

type ActivityEvent struct {
	Node      string `json:"node"`
	Tool      string `json:"tool"`
	Target    string `json:"target,omitempty"`
	Sidechain bool   `json:"sidechain"`
	StartedAt int64  `json:"startedAt"`
	EndedAt   int64  `json:"endedAt,omitempty"`
}

type AgentActivitySnapshot struct {
	AgentID string          `json:"agentId"`
	Nodes   []ActivityNode  `json:"nodes"`
	Recent  []ActivityEvent `json:"recent"`
}

// ---- internal watcher ----

const (
	pollInterval    = 800 * time.Millisecond
	thinkingWindow  = 20 * time.Second // no in-flight tool but recently active
	nodeRetention   = 60 * time.Second // drop idle main / keep window for late subscribers
	doneRetention   = 45 * time.Second // keep a finished sub-agent visible this long
	recentRetention = 90 * time.Second
	maxRecent       = 24
	mainNodeID      = "main"
)

type activityNode struct {
	id             string
	kind           string
	label          string
	startedAt      int64
	lastActivityAt int64
	toolCount      int
	currentToolID  string
	done           bool
	doneAt         int64
	order          int
}

type toolRun struct {
	id        string
	node      string
	tool      string
	target    string
	sidechain bool
	startedAt int64
	endedAt   int64
}

type transcriptWatcher struct {
	agentID string
	dir     string
	stop    chan struct{}
	emit    func(AgentActivitySnapshot)

	// tail state
	curFile string
	offset  int64
	carry   string

	// activity model
	nodes    map[string]*activityNode
	tools    map[string]*toolRun
	subByID  map[string]string // Agent/Task tool_use id -> subagent node id
	openSubs []string          // still-running subagent node ids (attribution stack)
	recent   []*toolRun
	orderSeq int

	wasNonEmpty bool
}

func (w *transcriptWatcher) run() {
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-w.stop:
			return
		case <-ticker.C:
			w.poll()
			w.emitSnapshot()
		}
	}
}

// poll picks the newest transcript file for this project and consumes any bytes
// appended since the last read.
func (w *transcriptWatcher) poll() {
	path := w.newestTranscript()
	if path == "" {
		return
	}
	if path != w.curFile {
		// A new session file → start from a clean activity model so a fresh session
		// never inherits the previous one's nodes/tools.
		w.curFile = path
		w.offset = 0
		w.carry = ""
		w.nodes = make(map[string]*activityNode)
		w.tools = make(map[string]*toolRun)
		w.subByID = make(map[string]string)
		w.openSubs = nil
		w.recent = nil
	}

	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()

	st, err := f.Stat()
	if err != nil {
		return
	}
	size := st.Size()
	if size < w.offset {
		// truncated / rotated
		w.offset = 0
		w.carry = ""
	}
	if size == w.offset {
		return
	}

	if _, err := f.Seek(w.offset, io.SeekStart); err != nil {
		return
	}
	buf := make([]byte, size-w.offset)
	n, _ := io.ReadFull(f, buf)
	w.offset += int64(n)

	data := w.carry + string(buf[:n])
	lines := strings.Split(data, "\n")
	w.carry = lines[len(lines)-1]
	for _, line := range lines[:len(lines)-1] {
		line = strings.TrimSpace(line)
		if line != "" {
			w.processLine(line)
		}
	}
}

func (w *transcriptWatcher) newestTranscript() string {
	entries, err := os.ReadDir(w.dir)
	if err != nil {
		return ""
	}
	var newest string
	var newestMod time.Time
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if newest == "" || info.ModTime().After(newestMod) {
			newest = filepath.Join(w.dir, e.Name())
			newestMod = info.ModTime()
		}
	}
	return newest
}

type transcriptLine struct {
	Type        string         `json:"type"`
	IsSidechain bool           `json:"isSidechain"`
	Timestamp   string         `json:"timestamp"`
	Message     *transcriptMsg `json:"message"`
}

type transcriptMsg struct {
	Content json.RawMessage `json:"content"`
}

type contentBlock struct {
	Type      string          `json:"type"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
}

func (w *transcriptWatcher) processLine(raw string) {
	var line transcriptLine
	if err := json.Unmarshal([]byte(raw), &line); err != nil {
		return
	}
	if line.Message == nil || len(line.Message.Content) == 0 {
		return
	}
	// content may be a plain string (ordinary chat) or an array of blocks.
	var blocks []contentBlock
	if err := json.Unmarshal(line.Message.Content, &blocks); err != nil {
		return
	}
	ts := parseTimestamp(line.Timestamp)

	for _, b := range blocks {
		switch b.Type {
		case "tool_use":
			w.onToolUse(b, line.IsSidechain, ts)
		case "tool_result":
			w.onToolResult(b.ToolUseID, ts)
		}
	}
}

func (w *transcriptWatcher) onToolUse(b contentBlock, sidechain bool, ts int64) {
	if isSubagentTool(b.Name) {
		// A sub-agent spawn. Create a child node and mark the main agent busy
		// running the Agent/Task tool until its result returns.
		label := subagentLabel(b.Input)
		nodeID := "sub:" + b.ID
		w.orderSeq++
		w.nodes[nodeID] = &activityNode{
			id: nodeID, kind: "subagent", label: label,
			startedAt: ts, lastActivityAt: ts, order: w.orderSeq,
		}
		w.subByID[b.ID] = nodeID
		w.openSubs = append(w.openSubs, nodeID)

		w.touchMain(ts)
		main := w.nodes[mainNodeID]
		main.currentToolID = b.ID
		main.toolCount++
		w.addTool(&toolRun{id: b.ID, node: mainNodeID, tool: b.Name, target: label, startedAt: ts})
		return
	}

	// A regular tool. Sidechain tools belong to the running sub-agent; main-chain
	// tools belong to the main agent.
	nodeID := mainNodeID
	if sidechain {
		if s := w.currentOpenSub(); s != "" {
			nodeID = s
		}
	} else {
		w.touchMain(ts)
	}
	n := w.nodes[nodeID]
	if n == nil {
		return
	}
	n.lastActivityAt = ts
	n.toolCount++
	n.currentToolID = b.ID
	w.addTool(&toolRun{
		id: b.ID, node: nodeID, tool: b.Name,
		target: toolTarget(b.Name, b.Input), sidechain: sidechain, startedAt: ts,
	})
}

func (w *transcriptWatcher) onToolResult(toolUseID string, ts int64) {
	if toolUseID == "" {
		return
	}
	if tr, ok := w.tools[toolUseID]; ok {
		tr.endedAt = ts
		if n := w.nodes[tr.node]; n != nil {
			if n.currentToolID == toolUseID {
				n.currentToolID = ""
			}
			n.lastActivityAt = ts
		}
	}
	// If this was a sub-agent spawn, the sub-agent has finished.
	if subNode, ok := w.subByID[toolUseID]; ok {
		if n := w.nodes[subNode]; n != nil {
			n.done = true
			n.doneAt = ts
			n.currentToolID = ""
		}
		w.removeOpenSub(subNode)
		delete(w.subByID, toolUseID)
	}
}

func (w *transcriptWatcher) touchMain(ts int64) {
	n := w.nodes[mainNodeID]
	if n == nil {
		w.orderSeq++
		n = &activityNode{id: mainNodeID, kind: "main", label: "claude", startedAt: ts, order: w.orderSeq}
		w.nodes[mainNodeID] = n
	}
	if ts > n.lastActivityAt {
		n.lastActivityAt = ts
	}
}

func (w *transcriptWatcher) addTool(tr *toolRun) {
	w.tools[tr.id] = tr
	w.recent = append(w.recent, tr)
	if len(w.recent) > maxRecent*2 {
		w.recent = w.recent[len(w.recent)-maxRecent*2:]
	}
}

func (w *transcriptWatcher) currentOpenSub() string {
	for i := len(w.openSubs) - 1; i >= 0; i-- {
		if n := w.nodes[w.openSubs[i]]; n != nil && !n.done {
			return w.openSubs[i]
		}
	}
	return ""
}

func (w *transcriptWatcher) removeOpenSub(id string) {
	out := w.openSubs[:0]
	for _, s := range w.openSubs {
		if s != id {
			out = append(out, s)
		}
	}
	w.openSubs = out
}

// emitSnapshot recomputes status from wall-clock time, prunes stale nodes/events,
// and pushes to the emitter. It emits while there is anything to show (and once more
// to clear the client when activity fully drains).
func (w *transcriptWatcher) emitSnapshot() {
	now := time.Now().UnixMilli()
	w.prune(now)

	var nodes []*activityNode
	for _, n := range w.nodes {
		if !w.nodeVisible(n, now) {
			continue
		}
		nodes = append(nodes, n)
	}
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].order < nodes[j].order })

	snap := AgentActivitySnapshot{AgentID: w.agentID}
	for _, n := range nodes {
		an := ActivityNode{
			ID: n.id, Kind: n.kind, Label: n.label,
			Status: w.status(n, now), ToolCount: n.toolCount,
			StartedAt: n.startedAt, LastActivityAt: n.lastActivityAt,
		}
		if n.kind == "subagent" {
			an.Parent = mainNodeID
		}
		if n.currentToolID != "" {
			if tr := w.tools[n.currentToolID]; tr != nil {
				an.CurrentTool = tr.tool
				an.CurrentTarget = tr.target
			}
		}
		snap.Nodes = append(snap.Nodes, an)
	}

	recent := make([]*toolRun, 0, len(w.recent))
	for _, tr := range w.recent {
		if tr.endedAt != 0 && now-tr.endedAt > int64(recentRetention/time.Millisecond) {
			continue
		}
		recent = append(recent, tr)
	}
	if len(recent) > maxRecent {
		recent = recent[len(recent)-maxRecent:]
	}
	for i := len(recent) - 1; i >= 0; i-- { // newest first
		tr := recent[i]
		snap.Recent = append(snap.Recent, ActivityEvent{
			Node: tr.node, Tool: tr.tool, Target: tr.target,
			Sidechain: tr.sidechain, StartedAt: tr.startedAt, EndedAt: tr.endedAt,
		})
	}

	nonEmpty := len(snap.Nodes) > 0
	if !nonEmpty && !w.wasNonEmpty {
		return // nothing to show and nothing to clear
	}
	w.wasNonEmpty = nonEmpty
	w.emit(snap)
}

func (w *transcriptWatcher) nodeVisible(n *activityNode, now int64) bool {
	if n.done {
		return now-n.doneAt < int64(doneRetention/time.Millisecond)
	}
	if n.kind == "main" {
		return now-n.lastActivityAt < int64(nodeRetention/time.Millisecond)
	}
	return true
}

func (w *transcriptWatcher) status(n *activityNode, now int64) string {
	if n.done {
		return "done"
	}
	if n.currentToolID != "" {
		return "working"
	}
	if now-n.lastActivityAt < int64(thinkingWindow/time.Millisecond) {
		return "thinking"
	}
	return "idle"
}

func (w *transcriptWatcher) prune(now int64) {
	for id, n := range w.nodes {
		if n.done && now-n.doneAt > int64(doneRetention/time.Millisecond) {
			delete(w.nodes, id)
		}
	}
	for id, tr := range w.tools {
		if tr.endedAt != 0 && now-tr.endedAt > int64(recentRetention/time.Millisecond) {
			delete(w.tools, id)
		}
	}
}

// ---- helpers ----

func isClaudeAgent(preset, command string) bool {
	p := strings.ToLower(preset)
	c := strings.ToLower(command)
	return p == "claude" || strings.Contains(c, "claude")
}

func isSubagentTool(name string) bool {
	return name == "Task" || name == "Agent"
}

func claudeProjectDir(cwd string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".claude", "projects", encodeProjectPath(cwd))
}

// encodeProjectPath mirrors Claude Code's directory naming: every character that is
// not a letter or digit becomes '-'. e.g. /home/u/code/agentdeck-go -> -home-u-code-agentdeck-go
func encodeProjectPath(cwd string) string {
	var b strings.Builder
	for _, r := range cwd {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	return b.String()
}

func parseTimestamp(ts string) int64 {
	if ts != "" {
		if t, err := time.Parse(time.RFC3339, ts); err == nil {
			return t.UnixMilli()
		}
	}
	return time.Now().UnixMilli()
}

// subagentLabel pulls a human label for a spawned sub-agent from the Agent/Task input.
func subagentLabel(input json.RawMessage) string {
	var m map[string]interface{}
	if json.Unmarshal(input, &m) != nil {
		return "subagent"
	}
	for _, key := range []string{"subagent_type", "description"} {
		if v, ok := m[key].(string); ok && v != "" {
			return truncate(v, 40)
		}
	}
	return "subagent"
}

// toolTarget summarizes what a tool is acting on, for the activity strip.
func toolTarget(name string, input json.RawMessage) string {
	var m map[string]interface{}
	if json.Unmarshal(input, &m) != nil {
		return ""
	}
	str := func(keys ...string) string {
		for _, k := range keys {
			if v, ok := m[k].(string); ok && v != "" {
				return v
			}
		}
		return ""
	}
	var target string
	switch name {
	case "Read", "Write", "Edit", "MultiEdit", "NotebookEdit":
		target = filepath.Base(str("file_path", "notebook_path", "path"))
	case "Bash", "BashOutput":
		target = str("command", "description")
	case "Grep", "Glob":
		target = str("pattern", "query")
	case "WebFetch", "WebSearch":
		target = str("url", "query")
	case "Task", "Agent":
		target = str("subagent_type", "description")
	default:
		target = str("file_path", "path", "command", "pattern", "query", "url", "description")
	}
	return truncate(strings.TrimSpace(target), 48)
}

func truncate(s string, n int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}
