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

		// Home first, then project, so the project's version of a name wins.
		if home, err := os.UserHomeDir(); err == nil {
			scanSlashRoot(filepath.Join(home, ".claude"), "user", add)
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
