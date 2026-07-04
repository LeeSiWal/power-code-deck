package handlers

import (
	"encoding/json"
	"net/http"
	"time"

	"powercodedeck/services"
	"powercodedeck/ws"

	"github.com/gorilla/mux"
)

func GetAgentMeta(gitSvc *services.GitService, portScanner *services.PortScanner, notifSvc *services.NotificationService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := mux.Vars(r)["id"]
		git := gitSvc.GetInfo(id)
		ports := portScanner.GetPorts(id)
		notifs, _ := notifSvc.ListUnread(id)

		result := map[string]interface{}{
			"agentId":        id,
			"gitBranch":      "",
			"gitDirty":       false,
			"gitAhead":       0,
			"listeningPorts": ports,
			"notifications":  notifs,
		}
		if git != nil {
			result["gitBranch"] = git.Branch
			result["gitDirty"] = git.Dirty
			result["gitAhead"] = git.Ahead
		}
		if result["listeningPorts"] == nil {
			result["listeningPorts"] = []int{}
		}
		if result["notifications"] == nil {
			result["notifications"] = []services.Notification{}
		}
		jsonResponse(w, result)
	}
}

func SendToAgent(agentSvc *services.AgentService, hub *ws.Hub) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := mux.Vars(r)["id"]
		var body struct {
			Data string `json:"data"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			jsonError(w, "invalid body", http.StatusBadRequest)
			return
		}
		agent, err := agentSvc.Get(id)
		if err != nil {
			jsonError(w, "agent not found", http.StatusNotFound)
			return
		}
		if err := agentSvc.SendKeys(agent.ID, body.Data); err != nil {
			jsonError(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func SetAgentMetaStatus(hub *ws.Hub) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := mux.Vars(r)["id"]
		var body struct {
			Key   string `json:"key"`
			Text  string `json:"text"`
			Color string `json:"color,omitempty"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			jsonError(w, "invalid body", http.StatusBadRequest)
			return
		}
		hub.BroadcastAll(ws.EventAgentMetaStatus, ws.AgentMetaStatusPayload{
			AgentID: id, Key: body.Key, Text: body.Text, Color: body.Color,
		})
		w.WriteHeader(http.StatusNoContent)
	}
}

func SetAgentMetaProgress(hub *ws.Hub) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := mux.Vars(r)["id"]
		var body struct {
			Value float64 `json:"value"`
			Label string  `json:"label,omitempty"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			jsonError(w, "invalid body", http.StatusBadRequest)
			return
		}
		hub.BroadcastAll(ws.EventAgentMetaProgress, ws.AgentMetaProgressPayload{
			AgentID: id, Value: body.Value, Label: body.Label,
		})
		w.WriteHeader(http.StatusNoContent)
	}
}

func AddAgentMetaLog(hub *ws.Hub) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := mux.Vars(r)["id"]
		var body struct {
			Level   string `json:"level"`
			Message string `json:"message"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			jsonError(w, "invalid body", http.StatusBadRequest)
			return
		}
		hub.BroadcastAll(ws.EventAgentMetaLog, ws.AgentMetaLogPayload{
			AgentID: id, Level: body.Level, Message: body.Message,
			Timestamp: time.Now().Format("2006-01-02T15:04:05Z"),
		})
		w.WriteHeader(http.StatusNoContent)
	}
}
