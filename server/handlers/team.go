package handlers

import (
	"database/sql"
	"encoding/json"
	"net/http"

	"github.com/gorilla/mux"
	"powercodedeck/services"
)

// TeamNative is the part of NativeService the toggle needs.
type TeamNative interface {
	TeamReady(id string) bool
	TeamConductor(id string) (kind, model, cwd string, ok bool)
	SetTeamBrief(id, text string)
}

// TeamModeGet reports whether dream team mode is on for a session, and whether
// its running process has the team tools (ready; false until the next restart
// for sessions started before them).
func TeamModeGet(agents *services.AgentService, native TeamNative) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := mux.Vars(r)["id"]
		if _, err := agents.Get(id); err != nil {
			jsonError(w, "agent not found", http.StatusNotFound)
			return
		}
		jsonResponse(w, map[string]bool{"on": agents.TeamMode(id), "ready": native.TeamReady(id)})
	}
}

// TeamModeSet turns dream team mode on or off for a session. Turning it on
// queues the team brief for the session's next message (only when its process
// has the team tools; a new conductor conversation gets it at start); turning
// it off drops a brief that has not been sent yet.
func TeamModeSet(agents *services.AgentService, db *sql.DB, native TeamNative) http.HandlerFunc {
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
		was := agents.TeamMode(id)
		if err := agents.SetTeamMode(id, body.On); err != nil {
			jsonError(w, err.Error(), http.StatusInternalServerError)
			return
		}
		switch {
		case body.On && !was && native.TeamReady(id):
			kind, model, _, _ := native.TeamConductor(id)
			native.SetTeamBrief(id, services.TeamBrief(services.LoadTeamSettings(db), kind, model))
		case !body.On:
			native.SetTeamBrief(id, "")
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
