package handlers

import (
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/gorilla/mux"
	"powercodedeck/internal/orchestration"
)

// RegisterRunRoutes receives the authenticated API router. Explicit start uses
// the worker; clients cannot assert that checks or executions succeeded.
func RegisterRunRoutes(api *mux.Router, store *orchestration.Store, workers ...*orchestration.Worker) {
	var worker *orchestration.Worker
	if len(workers) > 0 {
		worker = workers[0]
	}
	fail := func(w http.ResponseWriter, err error) {
		switch {
		case errors.Is(err, orchestration.ErrInvalid):
			jsonError(w, err.Error(), 400)
		case errors.Is(err, orchestration.ErrConflict):
			jsonError(w, err.Error(), 409)
		case errors.Is(err, sql.ErrNoRows):
			jsonError(w, "run not found", 404)
		default:
			jsonError(w, "work storage unavailable", 500)
		}
	}
	if worker != nil {
		api.HandleFunc("/v2/runs/{id}/plan/draft/generate", func(w http.ResponseWriter, r *http.Request) {
			id, err := worker.StartPlanning(mux.Vars(r)["id"])
			if err != nil {
				fail(w, err)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusAccepted)
			json.NewEncoder(w).Encode(map[string]string{"draftId": id})
		}).Methods("POST")
		api.HandleFunc("/v2/runs/{id}/plan/apply", func(w http.ResponseWriter, r *http.Request) {
			preview, err := worker.PreviewApplication(mux.Vars(r)["id"])
			if err != nil {
				fail(w, err)
				return
			}
			jsonResponse(w, preview)
		}).Methods("GET")
		api.HandleFunc("/v2/runs/{id}/plan/apply", func(w http.ResponseWriter, r *http.Request) {
			var target orchestration.ApplyTarget
			d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
			d.DisallowUnknownFields()
			if err := d.Decode(&target); err != nil {
				jsonError(w, "invalid application target", 400)
				return
			}
			if d.Decode(new(any)) != io.EOF || target.IntegrationID == "" || target.Branch == "" || target.BaseCommit == "" || target.ResultCommit == "" {
				jsonError(w, "complete reviewed target required", 400)
				return
			}
			record, err := worker.ApplyResult(mux.Vars(r)["id"], target)
			if err != nil {
				fail(w, err)
				return
			}
			jsonResponse(w, record)
		}).Methods("POST")
		api.HandleFunc("/v2/runs/{id}/plan/apply/reconcile", func(w http.ResponseWriter, r *http.Request) {
			record, err := worker.ReconcileApplication(mux.Vars(r)["id"])
			if err != nil {
				fail(w, err)
				return
			}
			jsonResponse(w, record)
		}).Methods("POST")
		api.HandleFunc("/v2/runs/{id}/plan/integrate", func(w http.ResponseWriter, r *http.Request) {
			attempt, err := worker.StartIntegration(mux.Vars(r)["id"])
			if err != nil {
				fail(w, err)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusAccepted)
			json.NewEncoder(w).Encode(map[string]string{"executionId": attempt})
		}).Methods("POST")
		api.HandleFunc("/v2/runs/{id}/plan/start", func(w http.ResponseWriter, r *http.Request) {
			if err := worker.StartPlan(mux.Vars(r)["id"]); err != nil {
				fail(w, err)
				return
			}
			w.WriteHeader(http.StatusAccepted)
		}).Methods("POST")
		api.HandleFunc("/v2/runs/{id}/plan/tasks/{task}/retry", func(w http.ResponseWriter, r *http.Request) {
			vars := mux.Vars(r)
			if err := store.RetryPlannedTask(vars["id"], vars["task"]); err != nil {
				fail(w, err)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		}).Methods("POST")
		api.HandleFunc("/v2/runs/{id}/start", func(w http.ResponseWriter, r *http.Request) {
			execution, err := worker.Start(mux.Vars(r)["id"])
			if err != nil {
				fail(w, err)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusAccepted)
			json.NewEncoder(w).Encode(map[string]string{"executionId": execution})
		}).Methods("POST")
		api.HandleFunc("/v2/runs/{id}/executions/{execution}/artifact", func(w http.ResponseWriter, r *http.Request) {
			vars := mux.Vars(r)
			content, err := worker.ReadArtifact(vars["id"], vars["execution"], r.URL.Query().Get("kind"))
			if err != nil {
				fail(w, err)
				return
			}
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.Header().Set("X-Content-Type-Options", "nosniff")
			w.Write(content)
		}).Methods("GET")
	}
	api.HandleFunc("/v2/runs/{id}/plan/draft", func(w http.ResponseWriter, r *http.Request) {
		draft, err := store.GetPlanDraft(mux.Vars(r)["id"])
		if err != nil {
			fail(w, err)
			return
		}
		jsonResponse(w, draft)
	}).Methods("GET")
	api.HandleFunc("/v2/runs/{id}/plan", func(w http.ResponseWriter, r *http.Request) {
		plan, err := store.GetPlan(mux.Vars(r)["id"])
		if err != nil {
			fail(w, err)
			return
		}
		jsonResponse(w, plan)
	}).Methods("GET")
	api.HandleFunc("/v2/runs/{id}/plan", func(w http.ResponseWriter, r *http.Request) {
		var plan orchestration.TaskPlan
		d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 512*1024))
		d.DisallowUnknownFields()
		if err := d.Decode(&plan); err != nil {
			jsonError(w, "invalid plan", 400)
			return
		}
		if d.Decode(new(any)) != io.EOF {
			jsonError(w, "one plan object required", 400)
			return
		}
		id := mux.Vars(r)["id"]
		if _, err := store.Get(id); err != nil {
			fail(w, err)
			return
		}
		if err := store.SavePlan(id, plan); err != nil {
			fail(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}).Methods("PUT")
	api.HandleFunc("/v2/runs", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Path     string `json:"path"`
			Prompt   string `json:"prompt"`
			Provider string `json:"provider"`
		}
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 128*1024))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&req); err != nil {
			jsonError(w, "invalid request", 400)
			return
		}
		if err := decoder.Decode(new(any)); err != io.EOF {
			jsonError(w, "one request object required", 400)
			return
		}
		run, err := store.Create(r.Header.Get("Idempotency-Key"), req.Path, req.Prompt, req.Provider)
		if err != nil {
			fail(w, err)
			return
		}
		jsonResponse(w, run)
	}).Methods("POST")
	api.HandleFunc("/v2/runs", func(w http.ResponseWriter, r *http.Request) {
		runs, err := store.List()
		if err != nil {
			fail(w, err)
			return
		}
		jsonResponse(w, map[string]any{"runs": runs})
	}).Methods("GET")
	api.HandleFunc("/v2/runs/{id}", func(w http.ResponseWriter, r *http.Request) {
		run, err := store.Get(mux.Vars(r)["id"])
		if err != nil {
			fail(w, err)
			return
		}
		jsonResponse(w, run)
	}).Methods("GET")
	api.HandleFunc("/v2/runs/{id}/cancel", func(w http.ResponseWriter, r *http.Request) {
		id := mux.Vars(r)["id"]
		if _, err := store.Get(id); err != nil {
			fail(w, err)
			return
		}
		cancel := store.Cancel
		if worker != nil {
			cancel = worker.Cancel
		}
		if err := cancel(id); err != nil {
			fail(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}).Methods("POST")
}
