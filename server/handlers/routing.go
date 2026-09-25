package handlers

import (
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/gorilla/mux"
	"powercodedeck/internal/orchestration"
	"powercodedeck/internal/routing"
	"powercodedeck/internal/routing/runroute"
)

// RegisterRoutingRoutes adds the model-routing API to the authenticated router.
// Profiles and endpoints come only from the server's routing.json; nothing here
// accepts a URL, credential, or spend opt-in from the browser.
func RegisterRoutingRoutes(api *mux.Router, c *runroute.Coordinator) {
	fail := func(w http.ResponseWriter, err error) {
		switch {
		case errors.Is(err, runroute.ErrRoutingOff):
			jsonError(w, err.Error(), 409)
		case errors.Is(err, runroute.ErrNoProfile), errors.Is(err, routing.ErrContextOverflow):
			jsonError(w, err.Error(), 409)
		case errors.Is(err, orchestration.ErrInvalid):
			jsonError(w, err.Error(), 400)
		case errors.Is(err, orchestration.ErrConflict), errors.Is(err, routing.ErrStaleEpoch):
			jsonError(w, err.Error(), 409)
		case errors.As(err, new(routing.ErrBadTransition)):
			jsonError(w, err.Error(), 409)
		case errors.Is(err, sql.ErrNoRows):
			jsonError(w, "run not found", 404)
		default:
			jsonError(w, "routing unavailable: "+err.Error(), 500)
		}
	}
	decode := func(w http.ResponseWriter, r *http.Request, v any) bool {
		d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
		d.DisallowUnknownFields()
		if err := d.Decode(v); err != nil && err != io.EOF {
			jsonError(w, "invalid routing request", 400)
			return false
		}
		if d.Decode(new(any)) != io.EOF {
			jsonError(w, "one request object required", 400)
			return false
		}
		return true
	}
	api.HandleFunc("/v2/routing", func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(w, c.Snapshot(r.Context()))
	}).Methods("GET")
	api.HandleFunc("/v2/routing/refresh", func(w http.ResponseWriter, r *http.Request) {
		c.Refresh()
		jsonResponse(w, c.Snapshot(r.Context()))
	}).Methods("POST")
	api.HandleFunc("/v2/routing/adapters/{adapter}/validated", func(w http.ResponseWriter, r *http.Request) {
		if err := c.MarkValidated(mux.Vars(r)["adapter"]); err != nil {
			fail(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}).Methods("POST")
	api.HandleFunc("/v2/runs/{id}/routing", func(w http.ResponseWriter, r *http.Request) {
		v, err := c.View(mux.Vars(r)["id"])
		if err != nil {
			fail(w, err)
			return
		}
		jsonResponse(w, v)
	}).Methods("GET")
	api.HandleFunc("/v2/runs/{id}/routing", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Mode             routing.Mode      `json:"mode"`
			PinProfile       string            `json:"pinProfile"`
			PinAdapter       string            `json:"pinAdapter"`
			Strategy         *routing.Strategy `json:"strategy"`         // "" = config default
			CommercialShadow *bool             `json:"commercialShadow"` // approve paid decider calls in Shadow for this Run
		}
		if !decode(w, r, &body) {
			return
		}
		if !routing.ValidMode(body.Mode) {
			jsonError(w, "mode must be off, manual, shadow or auto", 400)
			return
		}
		id := mux.Vars(r)["id"]
		st, err := c.Settings(id, body.Mode, body.PinProfile, body.PinAdapter)
		if err != nil {
			fail(w, err)
			return
		}
		if body.Strategy != nil || body.CommercialShadow != nil {
			v, err := c.View(id)
			if err != nil {
				fail(w, err)
				return
			}
			o := v.Options
			if body.Strategy != nil {
				o.Strategy = *body.Strategy
			}
			if body.CommercialShadow != nil {
				o.CommercialShadow = *body.CommercialShadow
			}
			if _, err := c.SetOptions(id, o); err != nil {
				fail(w, err)
				return
			}
		}
		jsonResponse(w, st)
	}).Methods("PUT")
	api.HandleFunc("/v2/runs/{id}/routing", func(w http.ResponseWriter, r *http.Request) {
		if err := c.Forget(mux.Vars(r)["id"]); err != nil {
			fail(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}).Methods("DELETE")
	api.HandleFunc("/v2/runs/{id}/routing/preview", func(w http.ResponseWriter, r *http.Request) {
		d, err := c.Preview(r.Context(), mux.Vars(r)["id"], r.URL.Query().Get("profile"))
		if err != nil {
			fail(w, err)
			return
		}
		jsonResponse(w, d)
	}).Methods("GET")
	api.HandleFunc("/v2/runs/{id}/routing/start", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Profile    string `json:"profile"`
			Reevaluate bool   `json:"reevaluate"` // ask the decider even if it would be skipped
		}
		if !decode(w, r, &body) {
			return
		}
		var res runroute.StartResult
		var err error
		if body.Reevaluate && body.Profile == "" {
			res, err = c.Reevaluate(r.Context(), mux.Vars(r)["id"])
		} else {
			res, err = c.Start(r.Context(), mux.Vars(r)["id"], body.Profile)
		}
		if err != nil {
			// The decision is still useful to the user: why nothing started.
			if res.Decision.ID != "" {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusConflict)
				json.NewEncoder(w).Encode(map[string]any{"error": err.Error(), "decision": res.Decision, "state": res.State})
				return
			}
			fail(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		json.NewEncoder(w).Encode(res)
	}).Methods("POST")
	api.HandleFunc("/v2/runs/{id}/routing/switch", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Profile string `json:"profile"`
			When    string `json:"when"` // boundary | now
		}
		if !decode(w, r, &body) {
			return
		}
		id := mux.Vars(r)["id"]
		var st routing.RunState
		var err error
		switch body.When {
		case "now":
			st, err = c.SwitchNow(id, body.Profile)
		case "boundary", "":
			st, err = c.SwitchAtBoundary(id, body.Profile)
		default:
			jsonError(w, "when must be boundary or now", 400)
			return
		}
		if err != nil {
			fail(w, err)
			return
		}
		jsonResponse(w, st)
	}).Methods("POST")
}
