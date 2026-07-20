package handlers

import (
	"encoding/json"
	"net/http"

	"powercodedeck/services"
)

// PushVAPIDKey hands the client the application-server public key it needs to call
// pushManager.subscribe(), plus whether push is even available on this server.
func PushVAPIDKey(ps *services.PushService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(w, map[string]any{
			"enabled":   ps.Enabled(),
			"publicKey": ps.PublicKey(),
		})
	}
}

// PushSubscribe stores a browser's push subscription so the server can reach it.
func PushSubscribe(ps *services.PushService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var sub services.PushSubscription
		if err := json.NewDecoder(r.Body).Decode(&sub); err != nil {
			jsonError(w, "invalid subscription", http.StatusBadRequest)
			return
		}
		if !ps.Subscribe(sub) {
			jsonError(w, "incomplete subscription", http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// PushUnsubscribe forgets a subscription (user turned notifications off).
func PushUnsubscribe(ps *services.PushService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Endpoint string `json:"endpoint"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Endpoint == "" {
			jsonError(w, "endpoint required", http.StatusBadRequest)
			return
		}
		ps.Unsubscribe(body.Endpoint)
		w.WriteHeader(http.StatusNoContent)
	}
}
