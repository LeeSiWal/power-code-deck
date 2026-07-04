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

// Slash commands cache
var (
	slashCache     []SlashCommand
	slashCacheTime time.Time
	slashCacheMu   sync.Mutex
	slashCacheTTL  = 30 * time.Second
)

type SlashCommand struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	Description string `json:"description,omitempty"`
}

func SlashCommands() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		slashCacheMu.Lock()
		defer slashCacheMu.Unlock()

		if slashCache != nil && time.Since(slashCacheTime) < slashCacheTTL {
			jsonResponse(w, slashCache)
			return
		}

		home, err := os.UserHomeDir()
		if err != nil {
			jsonResponse(w, []SlashCommand{})
			return
		}

		var commands []SlashCommand

		// ~/.claude/commands/ — slash commands
		cmdDir := filepath.Join(home, ".claude", "commands")
		if entries, err := os.ReadDir(cmdDir); err == nil {
			for _, entry := range entries {
				if entry.IsDir() {
					continue
				}
				name := strings.TrimSuffix(entry.Name(), ".md")
				desc := readFirstLine(filepath.Join(cmdDir, entry.Name()))
				commands = append(commands, SlashCommand{
					Name:        "/" + name,
					Type:        "command",
					Description: desc,
				})
			}
		}

		// ~/.claude/agents/ — agent mentions
		agentDir := filepath.Join(home, ".claude", "agents")
		if entries, err := os.ReadDir(agentDir); err == nil {
			for _, entry := range entries {
				if entry.IsDir() {
					continue
				}
				name := strings.TrimSuffix(entry.Name(), ".md")
				commands = append(commands, SlashCommand{
					Name: "@" + name,
					Type: "agent",
				})
			}
		}

		// ~/.claude/skills/ — skills
		skillDir := filepath.Join(home, ".claude", "skills")
		if entries, err := os.ReadDir(skillDir); err == nil {
			for _, entry := range entries {
				name := entry.Name()
				if !entry.IsDir() {
					name = strings.TrimSuffix(name, ".md")
				}
				commands = append(commands, SlashCommand{
					Name: "/" + name,
					Type: "skill",
				})
			}
		}

		if commands == nil {
			commands = []SlashCommand{}
		}
		slashCache = commands
		slashCacheTime = time.Now()
		jsonResponse(w, commands)
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
		if len(line) > 80 {
			return line[:80]
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
