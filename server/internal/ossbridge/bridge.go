package ossbridge

import (
	"bytes"
	"encoding/json"
	"io"
	"log"
	"net"
	"net/http"
	"strings"

	"powercodedeck/internal/routing"
)

// Bridge serves /{endpoint}/v1/responses and /{endpoint}/v1/models for the
// local endpoints configured in routing.json. It must only be reachable from
// this machine: it runs on its own loopback listener (see main.go) and also
// refuses any non-loopback peer.
type Bridge struct {
	Client    *routing.LocalClient
	Endpoints func() map[string]routing.LocalEndpoint
}

func (b *Bridge) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err != nil || !net.ParseIP(host).IsLoopback() {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	parts := strings.SplitN(strings.Trim(r.URL.Path, "/"), "/", 2)
	if len(parts) != 2 {
		http.NotFound(w, r)
		return
	}
	ep, ok := b.Endpoints()[parts[0]]
	if !ok || ep.Kind != "openai" {
		http.Error(w, "unknown local endpoint (needs kind \"openai\")", http.StatusNotFound)
		return
	}
	switch {
	case parts[1] == "v1/models" && r.Method == http.MethodGet:
		b.models(w, r, ep)
	case parts[1] == "v1/responses" && r.Method == http.MethodPost:
		b.responses(w, r, ep)
	default:
		http.NotFound(w, r)
	}
}

func (b *Bridge) models(w http.ResponseWriter, r *http.Request, ep routing.LocalEndpoint) {
	resp, err := b.Client.Stream(r.Context(), ep, http.MethodGet, "/v1/models", nil)
	if err != nil {
		http.Error(w, "local model server unreachable: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, io.LimitReader(resp.Body, 4<<20))
}

func (b *Bridge) responses(w http.ResponseWriter, r *http.Request, ep routing.LocalEndpoint) {
	var in responsesRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 32<<20)).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid Responses request: "+err.Error())
		return
	}
	model := in.Model
	if ep.Model != "" {
		model = ep.Model // the endpoint's configured model wins over the client's alias
	}
	chat, custom := toChat(in, model)
	body, _ := json.Marshal(chat)
	resp, err := b.Client.Stream(r.Context(), ep, http.MethodPost, "/v1/chat/completions", body)
	if err != nil {
		writeError(w, http.StatusBadGateway, "local model server unreachable: "+err.Error())
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 2000))
		writeError(w, http.StatusBadGateway, "local model server returned "+resp.Status+": "+strings.TrimSpace(string(snippet)))
		return
	}
	if !in.Stream {
		var buf bytes.Buffer
		em := &emitter{w: &buf}
		if err := relay(resp.Body, em, model, custom); err != nil {
			writeError(w, http.StatusBadGateway, "local model stream broke: "+err.Error())
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(em.completed)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	flusher, _ := w.(http.Flusher)
	em := &emitter{w: w, flush: func() {
		if flusher != nil {
			flusher.Flush()
		}
	}}
	if err := relay(resp.Body, em, model, custom); err != nil {
		log.Printf("ossbridge %s: upstream stream broke: %v", ep.ID, err)
		em.event("response.failed", map[string]any{"response": map[string]any{"status": "failed",
			"error": map[string]any{"code": "server_error", "message": "local model stream broke: " + err.Error()}}})
	}
}

func writeError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"message": msg, "type": "server_error"}})
}
