package main

import (
	"log"
	"net"
	"net/http"
	"os"
	"time"

	"powercodedeck/internal/ossbridge"
	"powercodedeck/internal/routing"
)

// startOSSBridge serves the Responses→Chat bridge for local model servers
// (routing.json localEndpoints of kind "openai") on a loopback-only port, so
// Codex can use e.g. mlx_lm.server. It is never mounted on the main router:
// that one sits behind the public reverse proxy. PCD_OSS_BRIDGE_PORT (33090).
func startOSSBridge() {
	path := routingConfigPath()
	if path == "" {
		return
	}
	cfg, err := routing.LoadConfig(path)
	if err != nil {
		return // no or invalid routing.json: routing already reports it
	}
	eps := map[string]routing.LocalEndpoint{}
	for _, e := range cfg.LocalEndpoints {
		if e.Kind == "openai" {
			eps[e.ID] = e
		}
	}
	if len(eps) == 0 {
		return
	}
	port := os.Getenv("PCD_OSS_BRIDGE_PORT")
	if port == "" {
		port = "33090"
	}
	addr := net.JoinHostPort("127.0.0.1", port)
	br := &ossbridge.Bridge{Client: routing.NewLocalClient(), Endpoints: func() map[string]routing.LocalEndpoint { return eps }}
	srv := &http.Server{Addr: addr, Handler: br, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		ids := make([]string, 0, len(eps))
		for id := range eps {
			ids = append(ids, id)
		}
		log.Printf("OSS bridge (Responses→Chat) on http://%s for %v", addr, ids)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("OSS bridge stopped: %v", err)
		}
	}()
}
