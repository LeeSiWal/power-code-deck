package handlers

import (
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/gorilla/mux"
	"powercodedeck/internal/orchestration"
	"powercodedeck/services"
)

// RegisterRunApprovalRoutes must receive the authenticated API router.
func RegisterRunApprovalRoutes(api *mux.Router, store *orchestration.Store, broker *services.PermissionBroker) {
	active := func(w http.ResponseWriter, r *http.Request) []string {
		run, err := store.Get(mux.Vars(r)["id"])
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				jsonError(w, "run not found", 404)
			} else {
				jsonError(w, "work storage unavailable", 500)
			}
			return nil
		}
		if run.State == "plan_running" {
			plan, err := store.GetPlan(run.ID)
			if err != nil {
				jsonError(w, "work storage unavailable", 500)
				return nil
			}
			ids := []string{}
			for _, task := range plan.Tasks {
				if task.State == "running" {
					ids = append(ids, task.AttemptID)
				}
			}
			return ids
		}
		if run.State != "running" || len(run.Executions) == 0 {
			jsonError(w, "run has no active execution", 409)
			return nil
		}
		return []string{run.Executions[len(run.Executions)-1].ID}
	}
	api.HandleFunc("/v2/runs/{id}/approvals", func(w http.ResponseWriter, r *http.Request) {
		id := active(w, r)
		if id == nil {
			return
		}
		pending := []services.PermissionRequest{}
		for _, session := range id {
			pending = append(pending, broker.Pending(session)...)
		}
		jsonResponse(w, pending)
	}).Methods("GET")
	api.HandleFunc("/v2/runs/{id}/approvals", func(w http.ResponseWriter, r *http.Request) {
		id := active(w, r)
		if id == nil {
			return
		}
		var req struct {
			ID       string `json:"id"`
			Behavior string `json:"behavior"`
		}
		d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
		d.DisallowUnknownFields()
		if err := d.Decode(&req); err != nil {
			jsonError(w, "invalid decision", 400)
			return
		}
		if d.Decode(new(any)) != io.EOF || req.ID == "" || (req.Behavior != "allow" && req.Behavior != "deny") {
			jsonError(w, "invalid decision", 400)
			return
		}
		resolved := false
		for _, session := range id {
			if broker.ResolveSession(session, req.ID, services.PermissionDecision{Behavior: req.Behavior}) {
				resolved = true
				break
			}
		}
		if !resolved {
			jsonError(w, "approval is no longer pending", 409)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}).Methods("POST")
}
