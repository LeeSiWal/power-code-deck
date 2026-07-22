package services

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"
)

type Agent struct {
	ID          string   `json:"id"`
	Preset      string   `json:"preset"`
	Name        string   `json:"name"`
	TmuxSession string   `json:"tmuxSession"`
	WorkingDir  string   `json:"workingDir"`
	Command     string   `json:"command"`
	Args        []string `json:"args"`
	Status      string   `json:"status"`
	ColorHue    int      `json:"colorHue"`
	ColorName   string   `json:"colorName"`
	CreatedAt   string   `json:"createdAt"`
	UpdatedAt   string   `json:"updatedAt"`
}

var colorPool = []struct {
	Name string
	Hue  int
}{
	{"blue", 220}, {"amber", 38}, {"emerald", 160}, {"violet", 270},
	{"pink", 330}, {"cyan", 190}, {"orange", 25}, {"teal", 170},
	{"red", 0}, {"lime", 80}, {"indigo", 245}, {"rose", 350},
}

type AgentService struct {
	db       *sql.DB
	engine   SessionEngine
	activity *ActivityManager
	// nativeAlive reports whether an agent has a live NATIVE session (Claude/Codex
	// driven as a structured stream, not a PTY). Injected because AgentService owns
	// rows, not the native process manager. Without it, a native-track agent that is
	// actively working reads as "stopped" — it has no live PTY, and the status check
	// only knew about the PTY engine.
	nativeAlive func(id string) bool
}

func NewAgentService(db *sql.DB, engine SessionEngine) *AgentService {
	return &AgentService{db: db, engine: engine}
}

// SetNativeLiveness wires the native-session liveness probe (nativeSvc.Running).
func (s *AgentService) SetNativeLiveness(fn func(id string) bool) { s.nativeAlive = fn }

// isAlive is the true "is this agent running" test: a live PTY session OR a live
// native session. Either means the agent is doing work.
func (s *AgentService) isAlive(id string) bool {
	if s.engine != nil && s.engine.HasSession(id) {
		return true
	}
	return s.nativeAlive != nil && s.nativeAlive(id)
}

// StartActivityForAll (re)starts transcript activity watchers for every existing
// agent. Watchers live only in memory, so a server restart loses them and the
// dashboard/control room show no "moving" signal until an agent is manually
// restarted. Called once at boot. Non-Claude presets are skipped inside Start.
func (s *AgentService) StartActivityForAll() {
	if s.activity == nil {
		return
	}
	agents, err := s.List()
	if err != nil {
		return
	}
	for i := range agents {
		s.startActivity(&agents[i])
	}
}

// SetActivityManager wires the transcript-based activity watcher so sessions start/stop
// watching in lockstep with their lifecycle. Optional — nil is safe.
func (s *AgentService) SetActivityManager(m *ActivityManager) {
	s.activity = m
}

func (s *AgentService) startActivity(a *Agent) {
	if s.activity != nil {
		s.activity.Start(a.ID, a.Preset, a.Command, a.WorkingDir)
	}
}

func (s *AgentService) stopActivity(id string) {
	if s.activity != nil {
		s.activity.Stop(id)
	}
}

func (s *AgentService) List() ([]Agent, error) {
	rows, err := s.db.Query("SELECT id, preset, name, tmux_session, working_dir, command, args, status, COALESCE(color_hue, 220), COALESCE(color_name, 'blue'), created_at, updated_at FROM agents ORDER BY created_at DESC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	// First: read all rows (no DB writes while rows are open — avoids SQLite deadlock)
	var agents []Agent
	for rows.Next() {
		var a Agent
		var argsJSON string
		if err := rows.Scan(&a.ID, &a.Preset, &a.Name, &a.TmuxSession, &a.WorkingDir, &a.Command, &argsJSON, &a.Status, &a.ColorHue, &a.ColorName, &a.CreatedAt, &a.UpdatedAt); err != nil {
			continue
		}
		json.Unmarshal([]byte(argsJSON), &a.Args)
		agents = append(agents, a)
	}
	rows.Close()

	// Second: check session status and update DB after rows are closed
	for i := range agents {
		a := &agents[i]
		if s.isAlive(a.ID) {
			if a.Status != "running" {
				a.Status = "running"
				s.db.Exec("UPDATE agents SET status = 'running', updated_at = datetime('now') WHERE id = ?", a.ID)
				insertAgentLog(s.db, a.ID, "실행 시작됨")
			}
		} else {
			if a.Status == "running" {
				a.Status = "stopped"
				s.db.Exec("UPDATE agents SET status = 'stopped', updated_at = datetime('now') WHERE id = ?", a.ID)
				insertAgentLog(s.db, a.ID, "세션 종료됨")
			}
		}
	}
	return agents, nil
}

func (s *AgentService) Get(id string) (*Agent, error) {
	var a Agent
	var argsJSON string
	err := s.db.QueryRow(
		"SELECT id, preset, name, tmux_session, working_dir, command, args, status, COALESCE(color_hue, 220), COALESCE(color_name, 'blue'), created_at, updated_at FROM agents WHERE id = ?",
		id,
	).Scan(&a.ID, &a.Preset, &a.Name, &a.TmuxSession, &a.WorkingDir, &a.Command, &argsJSON, &a.Status, &a.ColorHue, &a.ColorName, &a.CreatedAt, &a.UpdatedAt)
	if err != nil {
		return nil, err
	}
	json.Unmarshal([]byte(argsJSON), &a.Args)
	// Reconcile the stored status against real liveness (PTY engine OR native
	// session) so a native-track agent that's actively working isn't reported
	// "stopped" just for lacking a live PTY. Read-only — no DB write on a Get.
	if s.isAlive(a.ID) {
		a.Status = "running"
	} else {
		a.Status = "stopped"
	}
	return &a, nil
}

type CreateAgentRequest struct {
	Preset     string   `json:"preset"`
	Name       string   `json:"name"`
	WorkingDir string   `json:"workingDir"`
	Command    string   `json:"command"`
	Args       []string `json:"args"`
}

func (s *AgentService) assignColor() (int, string) {
	// Get existing hues
	rows, err := s.db.Query("SELECT COALESCE(color_hue, 220) FROM agents")
	if err != nil {
		return colorPool[0].Hue, colorPool[0].Name
	}
	defer rows.Close()

	var existingHues []int
	for rows.Next() {
		var h int
		rows.Scan(&h)
		existingHues = append(existingHues, h)
	}

	bestHue := colorPool[0].Hue
	bestName := colorPool[0].Name
	maxDist := -1

	for _, c := range colorPool {
		if len(existingHues) == 0 {
			return c.Hue, c.Name
		}
		minDist := 360
		for _, h := range existingHues {
			diff := c.Hue - h
			if diff < 0 {
				diff = -diff
			}
			if 360-diff < diff {
				diff = 360 - diff
			}
			if diff < minDist {
				minDist = diff
			}
		}
		if minDist > maxDist {
			maxDist = minDist
			bestHue = c.Hue
			bestName = c.Name
		}
	}
	return bestHue, bestName
}

func (s *AgentService) Create(req CreateAgentRequest) (*Agent, error) {
	b := make([]byte, 4)
	rand.Read(b)
	id := hex.EncodeToString(b)
	// Legacy DB column value; the session id used everywhere is the agent id.
	tmuxSession := "pcd-" + id

	argsJSON, _ := json.Marshal(req.Args)

	// Assign color
	colorHue, colorName := s.assignColor()

	// Expand a leading ~ so a directly-typed "~/code/foo" resolves to the home
	// directory instead of a literal "~" path the shell can't cd into.
	workingDir := expandHome(req.WorkingDir)

	// Start the session's process via the engine (tmux/PTY details are hidden).
	if _, err := s.engine.Create(CreateSessionRequest{
		ID:      id,
		Type:    req.Preset,
		Command: req.Command,
		Args:    req.Args,
		Cwd:     workingDir,
		Cols:    80,
		Rows:    24,
	}); err != nil {
		return nil, fmt.Errorf("failed to start session: %w", err)
	}

	now := time.Now().Format("2006-01-02T15:04:05Z")
	agent := &Agent{
		ID:          id,
		Preset:      req.Preset,
		Name:        req.Name,
		TmuxSession: tmuxSession,
		WorkingDir:  workingDir,
		Command:     req.Command,
		Args:        req.Args,
		Status:      "running",
		ColorHue:    colorHue,
		ColorName:   colorName,
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	_, err := s.db.Exec(
		"INSERT INTO agents (id, preset, name, tmux_session, working_dir, command, args, status, color_hue, color_name) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
		agent.ID, agent.Preset, agent.Name, agent.TmuxSession, agent.WorkingDir, agent.Command, string(argsJSON), agent.Status, agent.ColorHue, agent.ColorName,
	)
	if err != nil {
		s.engine.Kill(id)
		return nil, err
	}

	// A new session inherits the model + permission mode from your last session, so
	// continuing work doesn't reset those choices back to Auto/수동 every time. (The
	// /clear and 이어하기 paths copy from their exact source agent afterwards; this is
	// the general case — a fresh agent from the dashboard or a new project.)
	driver := "claude"
	if req.Preset == "codex-cli" || req.Command == "codex" {
		driver = "codex"
	}
	if model, mode := s.inheritedNativeConfig(workingDir, driver); model != "" || mode != "" {
		s.SetNativeConfig(agent.ID, model, mode)
	}

	insertAgentLog(s.db, agent.ID, "세션 생성됨 · "+agent.Name+" ("+agent.Command+")")
	s.startActivity(agent)
	return agent, nil
}

// inheritedNativeConfig picks the model + permission mode a new session should start
// with: the most recent prior session in the SAME project, or — if this project has
// none yet — the last choice made anywhere. Empty strings mean no prior choice
// (Claude's defaults). The just-created row can't match itself: it's still empty, so
// the "!= ''" filter excludes it.
func (s *AgentService) inheritedNativeConfig(workingDir, driver string) (model, mode string) {
	match := `( (? = 'codex' AND (preset = 'codex-cli' OR command = 'codex'))
	            OR (? = 'claude' AND (preset IN ('claude', 'claude-code') OR command = 'claude')) )`
	err := s.db.QueryRow(
		`SELECT COALESCE(native_model, ''), COALESCE(native_mode, '')
		   FROM agents
		  WHERE working_dir = ? AND (native_model != '' OR native_mode != '') AND `+match+`
		  ORDER BY created_at DESC LIMIT 1`, workingDir, driver, driver,
	).Scan(&model, &mode)
	if err == nil && (model != "" || mode != "") {
		return model, mode
	}
	_ = s.db.QueryRow(
		`SELECT COALESCE(native_model, ''), COALESCE(native_mode, '')
		   FROM agents
		  WHERE (native_model != '' OR native_mode != '') AND `+match+`
		  ORDER BY created_at DESC LIMIT 1`,
		driver, driver,
	).Scan(&model, &mode)
	return model, mode
}

func (s *AgentService) Delete(id string) error {
	if _, err := s.Get(id); err != nil {
		return err
	}

	// Explicit user delete → Kill the underlying process.
	s.engine.Kill(id)
	s.stopActivity(id)

	_, err := s.db.Exec("DELETE FROM agents WHERE id = ?", id)
	return err
}

// ClaudeSessionID returns the Claude conversation id we last saw for this agent,
// or "" if it never ran natively. This is what --resume takes.
func (s *AgentService) ClaudeSessionID(id string) string {
	var sid string
	_ = s.db.QueryRow("SELECT COALESCE(claude_session_id, '') FROM agents WHERE id = ?", id).Scan(&sid)
	return sid
}

// SetClaudeSessionID remembers the conversation so a later open can resume it.
// Best-effort: failing to record it costs continuity, never correctness.
func (s *AgentService) SetClaudeSessionID(id, sessionID string) {
	if id == "" || sessionID == "" {
		return
	}
	_, _ = s.db.Exec("UPDATE agents SET claude_session_id = ? WHERE id = ?", sessionID, id)
}

// NativeConfig returns the remembered native-chat model + permission mode for an
// agent (both "" if never set — meaning Claude's defaults).
func (s *AgentService) NativeConfig(id string) (model, mode string) {
	_ = s.db.QueryRow(
		"SELECT COALESCE(native_model, ''), COALESCE(native_mode, '') FROM agents WHERE id = ?", id,
	).Scan(&model, &mode)
	return model, mode
}

// SetNativeConfig persists the native-chat model + permission mode so a restart or
// another device resumes with the same choices. Best-effort, like the resume id.
func (s *AgentService) SetNativeConfig(id, model, mode string) {
	if id == "" {
		return
	}
	_, _ = s.db.Exec("UPDATE agents SET native_model = ?, native_mode = ? WHERE id = ?", model, mode, id)
}

func (s *AgentService) Restart(id string) (*Agent, error) {
	agent, err := s.Get(id)
	if err != nil {
		return nil, err
	}

	// Restart = Kill the current process, then start a fresh one. Rebuilt from
	// the DB record so it works even after a server restart (engine memory lost).
	s.engine.Kill(id)
	if _, err := s.engine.Create(CreateSessionRequest{
		ID:      id,
		Type:    agent.Preset,
		Command: agent.Command,
		Args:    agent.Args,
		Cwd:     agent.WorkingDir,
		Cols:    80,
		Rows:    24,
	}); err != nil {
		return nil, fmt.Errorf("failed to restart session: %w", err)
	}

	s.db.Exec("UPDATE agents SET status = 'running', updated_at = datetime('now') WHERE id = ?", id)
	agent.Status = "running"
	insertAgentLog(s.db, id, "재시작됨")
	s.startActivity(agent)
	return agent, nil
}

func (s *AgentService) GetWorkingDir(id string) (string, error) {
	agent, err := s.Get(id)
	if err != nil {
		return "", err
	}
	return agent.WorkingDir, nil
}

func (s *AgentService) UpdateStatus(id, status string) error {
	_, err := s.db.Exec("UPDATE agents SET status = ?, updated_at = datetime('now') WHERE id = ?", status, id)
	return err
}

// SendKeys delivers a line of input to a session (used by the REST /send API).
// It addresses the session by id — callers never handle tmux names. A CR is
// appended so the line is submitted.
func (s *AgentService) SendKeys(sessionID, data string) error {
	return s.engine.Write(sessionID, []byte(data+"\r"))
}
