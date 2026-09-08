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
