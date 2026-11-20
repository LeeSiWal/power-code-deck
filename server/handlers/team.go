package handlers

import (
	"database/sql"
	"encoding/json"
	"net/http"

	"github.com/gorilla/mux"
	"powercodedeck/services"
)

// TeamModeGet reports whether dream team mode is on for a session.
func TeamModeGet(agents *services.AgentService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := mux.Vars(r)["id"]
		if _, err := agents.Get(id); err != nil {
			jsonError(w, "agent not found", http.StatusNotFound)
			return
		}
		jsonResponse(w, map[string]bool{"on": agents.TeamMode(id)})
	}
}

// TeamModeSet turns dream team mode on or off for a session.
func TeamModeSet(agents *services.AgentService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := mux.Vars(r)["id"]
		var body struct {
			On bool `json:"on"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			jsonError(w, "invalid body", http.StatusBadRequest)
			return
		}
		if _, err := agents.Get(id); err != nil {
			jsonError(w, "agent not found", http.StatusNotFound)
			return
		}
		if err := agents.SetTeamMode(id, body.On); err != nil {
			jsonError(w, err.Error(), http.StatusInternalServerError)
			return
		}
		jsonResponse(w, map[string]bool{"on": body.On})
	}
}

// TeamSettingsGet returns the global team settings.
func TeamSettingsGet(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(w, services.LoadTeamSettings(db))
	}
}

// TeamSettingsPut stores the global team settings and returns the normalized result.
func TeamSettingsPut(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var s services.TeamSettings
		if err := json.NewDecoder(r.Body).Decode(&s); err != nil {
			jsonError(w, "invalid body", http.StatusBadRequest)
			return
		}
		if err := services.SaveTeamSettings(db, s); err != nil {
			jsonError(w, err.Error(), http.StatusInternalServerError)
			return
		}
		jsonResponse(w, services.LoadTeamSettings(db))
	}
}
