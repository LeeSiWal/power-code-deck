package handlers

import (
	"bufio"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"powercodedeck/services"
	"powercodedeck/ws"

	"github.com/gorilla/mux"
)

func ListAgents(agentSvc *services.AgentService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		agents, err := agentSvc.List()
		if err != nil {
			jsonError(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if agents == nil {
			agents = []services.Agent{}
		}
		jsonResponse(w, agents)
	}
}

func CreateAgent(agentSvc *services.AgentService, hub *ws.Hub) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req services.CreateAgentRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			jsonError(w, "invalid request", http.StatusBadRequest)
			return
		}

		if req.Preset == "" || req.Name == "" || req.WorkingDir == "" || req.Command == "" {
			jsonError(w, "preset, name, workingDir, and command are required", http.StatusBadRequest)
			return
		}

		agent, err := agentSvc.Create(req)
		if err != nil {
			jsonError(w, err.Error(), http.StatusInternalServerError)
			return
		}

		// Broadcast agent created
		hub.BroadcastAll(ws.EventAgentCreated, agent)

		w.WriteHeader(http.StatusCreated)
		jsonResponse(w, agent)
	}
}

func GetAgent(agentSvc *services.AgentService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := mux.Vars(r)["id"]
		agent, err := agentSvc.Get(id)
		if err != nil {
			jsonError(w, "agent not found", http.StatusNotFound)
			return
		}
		jsonResponse(w, agent)
	}
}

func DeleteAgent(agentSvc *services.AgentService, hub *ws.Hub) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := mux.Vars(r)["id"]
		if err := agentSvc.Delete(id); err != nil {
			jsonError(w, err.Error(), http.StatusInternalServerError)
			return
		}

		hub.BroadcastAll(ws.EventAgentDestroyed, map[string]string{"agentId": id})

		w.WriteHeader(http.StatusNoContent)
	}
}

// Slash commands cache, keyed by project dir ("" = home only).
var (
	slashCache    = map[string]slashCacheEntry{}
	slashCacheMu  sync.Mutex
	slashCacheTTL = 30 * time.Second
)

type slashCacheEntry struct {
	cmds []SlashCommand
	at   time.Time
}

type SlashCommand struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	Description string `json:"description,omitempty"`
	/** Scope tells the UI where a command came from — a project's own commands are
	  the ones you'd expect to see while working in it. */
	Scope string `json:"scope"` // "project" | "user"
}

// SlashCommands lists the commands / agent mentions / skills a native chat can
// actually invoke. Only user-defined ones: verified against the real CLI that a
// custom command sent over stream-json expands normally, while a BUILT-IN like
// /help answers "isn't available in this environment" — so listing built-ins would
// hand the user buttons that do nothing.
//
// Takes ?agentId= to include that agent's project-level .claude/, which Claude Code
// expands just like the ones in $HOME. Without it, per-project commands — the most
// useful kind — would never appear.
func SlashCommands(agentSvc *services.AgentService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		projectDir := ""
		if id := r.URL.Query().Get("agentId"); id != "" && agentSvc != nil {
			if a, err := agentSvc.Get(id); err == nil && a != nil {
				projectDir = a.WorkingDir
			}
		}

		slashCacheMu.Lock()
		defer slashCacheMu.Unlock()
		if e, ok := slashCache[projectDir]; ok && time.Since(e.at) < slashCacheTTL {
			jsonResponse(w, e.cmds)
			return
		}

		var commands []SlashCommand
		seen := map[string]int{}
		add := func(c SlashCommand) {
			if i, ok := seen[c.Name]; ok {
				commands[i] = c // later root wins — project overrides a same-named user one
				return
			}
			seen[c.Name] = len(commands)
			commands = append(commands, c)
		}

		// Built-ins first so a user- or project-defined command of the same name
		// overrides them, matching how the CLI resolves it.
		for _, c := range builtinSlashCommands {
			add(c)
		}
		// Home first, then project, so the project's version of a name wins.
		if home, err := os.UserHomeDir(); err == nil {
			scanSlashRoot(filepath.Join(home, ".claude"), "user", add)
			scanPlugins(home, add) // namespaced, so it can't collide with the above
		}
		if projectDir != "" {
			scanSlashRoot(filepath.Join(projectDir, ".claude"), "project", add)
		}

		if commands == nil {
			commands = []SlashCommand{}
		}
		slashCache[projectDir] = slashCacheEntry{cmds: commands, at: time.Now()}
		jsonResponse(w, commands)
	}
}

// builtinSlashCommands are the CLI's OWN commands, each one verified to work over
// the stream protocol we drive. The set had to be probed rather than assumed: it is
// undocumented and not uniform. /help, /status, /memory, /permissions and /rewind
// all answer "isn't available in this environment", and /todos is an unknown
// command — listing any of those would put a dead entry in the picker.
//
// /model is deliberately absent although it works: switching the model that way
// leaves the deck's own model state (the pill, and what a restart resumes with)
// pointing at the old one. The pill is the honest path.
//
// /clear is listed but the CLIENT handles it — forwarding would drop the CLI's
// context while the transcript stayed on screen, so the chat would look intact
// while Claude had forgotten all of it.
var builtinSlashCommands = []SlashCommand{
	{Name: "/context", Type: "builtin", Scope: "builtin", Description: "컨텍스트 사용량 — 무엇이 얼마나 차지하는지"},
	{Name: "/compact", Type: "builtin", Scope: "builtin", Description: "대화를 요약해 컨텍스트를 줄임"},
	{Name: "/clear", Type: "builtin", Scope: "builtin", Description: "새 세션으로 시작 — 대화와 컨텍스트를 함께 비움"},
	{Name: "/cost", Type: "builtin", Scope: "builtin", Description: "이번 세션의 사용량과 비용"},
	{Name: "/usage", Type: "builtin", Scope: "builtin", Description: "구독 사용량 한도"},
	{Name: "/init", Type: "builtin", Scope: "builtin", Description: "저장소를 분석해 CLAUDE.md 생성"},
	{Name: "/mcp", Type: "builtin", Scope: "builtin", Description: "MCP 서버 상태"},
	{Name: "/doctor", Type: "builtin", Scope: "builtin", Description: "Claude Code 설치 상태 점검"},
}

type claudeSettings struct {
	EnabledPlugins map[string]bool `json:"enabledPlugins"`
}

// scanPlugins adds the commands and skills of every ENABLED plugin, which are a
// large part of what a user can actually invoke and were missing entirely.
//
// They are only reachable under their plugin namespace — verified against the real
// CLI: "/newton:mission" expands, while "/mission" answers "Unknown command". So
// the name we offer must carry the prefix or the entry would be a dead one.
func scanPlugins(home string, add func(SlashCommand)) {
	data, err := os.ReadFile(filepath.Join(home, ".claude", "settings.json"))
	if err != nil {
		return
	}
	var s claudeSettings
	if json.Unmarshal(data, &s) != nil {
		return
	}
	for key, enabled := range s.EnabledPlugins {
		if !enabled {
			continue
		}
		// "newton@newton-marketplace"
		name, marketplace, ok := strings.Cut(key, "@")
		if !ok || name == "" {
			continue
		}
		dir := pluginDir(home, marketplace, name)
		if dir == "" {
			continue
		}

		cmdDir := filepath.Join(dir, "commands")
		if entries, err := os.ReadDir(cmdDir); err == nil {
			for _, e := range entries {
				if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
					continue
				}
				add(SlashCommand{
					Name:        "/" + name + ":" + strings.TrimSuffix(e.Name(), ".md"),
					Type:        "command",
					Description: readFirstLine(filepath.Join(cmdDir, e.Name())),
					Scope:       "plugin",
				})
			}
		}

		skillDir := filepath.Join(dir, "skills")
		if entries, err := os.ReadDir(skillDir); err == nil {
			for _, e := range entries {
				skill, desc := e.Name(), ""
				if e.IsDir() {
					desc = skillDescription(filepath.Join(skillDir, skill, "SKILL.md"))
				} else {
					if !strings.HasSuffix(skill, ".md") {
						continue
					}
					skill = strings.TrimSuffix(skill, ".md")
				}
				add(SlashCommand{
					Name:        "/" + name + ":" + skill,
					Type:        "skill",
					Description: desc,
					Scope:       "plugin",
				})
			}
		}
	}
}

// pluginDir resolves an enabled plugin's files: the newest non-orphaned version in
// the marketplace cache, else the marketplace checkout itself (a single-plugin repo
// keeps commands/ and skills/ at its root). Orphaned versions are ones a newer
// install superseded — listing them would offer commands that no longer exist.
func pluginDir(home, marketplace, name string) string {
	cache := filepath.Join(home, ".claude", "plugins", "cache", marketplace, name)
	if entries, err := os.ReadDir(cache); err == nil {
		best, bestMod := "", time.Time{}
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			p := filepath.Join(cache, e.Name())
			if _, err := os.Stat(filepath.Join(p, ".orphaned_at")); err == nil {
				continue
			}
			if info, err := e.Info(); err == nil && info.ModTime().After(bestMod) {
				best, bestMod = p, info.ModTime()
			}
		}
		if best != "" {
			return best
		}
	}
	mk := filepath.Join(home, ".claude", "plugins", "marketplaces", marketplace)
	if st, err := os.Stat(mk); err == nil && st.IsDir() {
		return mk
	}
	return ""
}

// skillDescription pulls `description:` out of a SKILL.md front-matter block.
// readFirstLine can't be reused: it skips the "---" fence and would return the
// `name:` line instead.
func skillDescription(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for i := 0; scanner.Scan() && i < 30; i++ {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "description:") {
			continue
		}
		d := strings.Trim(strings.TrimSpace(strings.TrimPrefix(line, "description:")), `"'`)
		if r := []rune(d); len(r) > 80 {
			return string(r[:80]) + "…"
		}
		return d
	}
	return ""
}

// scanSlashRoot collects one .claude directory's commands, agent mentions and skills.
func scanSlashRoot(root, scope string, add func(SlashCommand)) {
	cmdDir := filepath.Join(root, "commands")
	if entries, err := os.ReadDir(cmdDir); err == nil {
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			name := strings.TrimSuffix(entry.Name(), ".md")
			add(SlashCommand{
				Name:        "/" + name,
				Type:        "command",
				Description: readFirstLine(filepath.Join(cmdDir, entry.Name())),
				Scope:       scope,
			})
		}
	}

	if entries, err := os.ReadDir(filepath.Join(root, "agents")); err == nil {
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			add(SlashCommand{
				Name:  "@" + strings.TrimSuffix(entry.Name(), ".md"),
				Type:  "agent",
				Scope: scope,
			})
		}
	}

	if entries, err := os.ReadDir(filepath.Join(root, "skills")); err == nil {
		for _, entry := range entries {
			name := entry.Name()
			if !entry.IsDir() {
				name = strings.TrimSuffix(name, ".md")
			}
			add(SlashCommand{Name: "/" + name, Type: "skill", Scope: scope})
		}
	}
}

func readFirstLine(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "---") {
			continue
		}
		// Truncate by runes, not bytes: a Korean character is 3 bytes in UTF-8, so
		// line[:80] would slice one in half and the description reached the UI as
		// replacement characters (…놓친 ��).
		if r := []rune(line); len(r) > 80 {
			return string(r[:80]) + "…"
		}
		return line
	}
	return ""
}

func RestartAgent(agentSvc *services.AgentService, hub *ws.Hub) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := mux.Vars(r)["id"]
		agent, err := agentSvc.Restart(id)
		if err != nil {
			jsonError(w, err.Error(), http.StatusInternalServerError)
			return
		}

		hub.BroadcastAll(ws.EventAgentStatus, ws.AgentStatusPayload{
			AgentID: id,
			Status:  "running",
		})

		jsonResponse(w, agent)
	}
}
