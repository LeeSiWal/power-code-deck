package handlers

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strconv"

	"powercodedeck/services"

	"github.com/gorilla/mux"
)

func ListLocalProviders(registry *services.ProviderRegistry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		providers, err := registry.List()
		if err != nil {
			jsonError(w, err.Error(), http.StatusInternalServerError)
			return
		}
		jsonResponse(w, providers)
	}
}

func PutLocalProvider(registry *services.ProviderRegistry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var p services.LocalProvider
		if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
			jsonError(w, "invalid request", http.StatusBadRequest)
			return
		}
		p.Name = mux.Vars(r)["name"]
		stored, err := registry.Upsert(p)
		if err != nil {
			jsonError(w, err.Error(), http.StatusBadRequest)
			return
		}
		jsonResponse(w, stored)
	}
}

func DeleteLocalProvider(registry *services.ProviderRegistry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := registry.Delete(mux.Vars(r)["name"]); err != nil {
			if err == sql.ErrNoRows {
				jsonError(w, "provider not found", http.StatusNotFound)
			} else {
				jsonError(w, err.Error(), http.StatusInternalServerError)
			}
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func LocalProviderHealth(registry *services.ProviderRegistry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(w, registry.Health(r.Context(), mux.Vars(r)["name"]))
	}
}

// RunIntelligence accepts a run and returns immediately. r.Context() is
// deliberately never handed to the service: local inference outlives the request
// that asked for it, and tying the two together is what killed five production
// runs at ~125 seconds when a browser or proxy hung up mid-inference.
//
// 202 means "accepted, watch intelligence:trace"; the body carries the RUNNING
// trace so the caller knows which id to watch. Only validation still answers
// inside the request, as 400.
func RunIntelligence(svc *services.IntelligenceService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req services.IntelligenceRunRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			jsonError(w, "invalid request", http.StatusBadRequest)
			return
		}
		trace, err := svc.Start(req)
		if err != nil {
			// The structured trace is more useful than flattening the refusal into an
			// HTTP error string — the client renders the reason from the trace events.
			w.WriteHeader(http.StatusBadRequest)
			jsonResponse(w, services.IntelligenceRunResult{Trace: trace})
			return
		}
		w.WriteHeader(http.StatusAccepted)
		jsonResponse(w, services.IntelligenceRunResult{Trace: trace})
	}
}

// CancelIntelligenceRun stops a run someone decided they no longer want. 404 means
// there is nothing running under that id — already finished, or never existed.
func CancelIntelligenceRun(svc *services.IntelligenceService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !svc.Cancel(mux.Vars(r)["id"]) {
			jsonError(w, "no running trace with that id", http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func ListIntelligenceTraces(svc *services.IntelligenceService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		traces, err := svc.Traces(limit)
		if err != nil {
			jsonError(w, err.Error(), http.StatusInternalServerError)
			return
		}
		jsonResponse(w, traces)
	}
}

func GetIntelligenceTrace(svc *services.IntelligenceService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		trace, err := svc.Trace(mux.Vars(r)["id"])
		if err != nil {
			if err == sql.ErrNoRows {
				jsonError(w, "trace not found", http.StatusNotFound)
			} else {
				jsonError(w, err.Error(), http.StatusInternalServerError)
			}
			return
		}
		jsonResponse(w, trace)
	}
}
