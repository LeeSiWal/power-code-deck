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

// startOSSBridge serves the Responses→Chat bridge for local model servers of
// kind "openai" on a loopback-only port, so Codex can use e.g. mlx_lm.server.
// It is never mounted on the main router: that one sits behind the public
// reverse proxy. It always runs, reading the live endpoint list, so a server
// added in Settings works without a restart. PCD_OSS_BRIDGE_PORT (33090).
func startOSSBridge(eps *routing.EndpointSet) {
	port := os.Getenv("PCD_OSS_BRIDGE_PORT")
	if port == "" {
		port = "33090"
	}
	addr := net.JoinHostPort("127.0.0.1", port)
	br := &ossbridge.Bridge{Client: routing.NewLocalClient(), Endpoints: func() map[string]routing.LocalEndpoint { return eps.Get("openai") }}
	srv := &http.Server{Addr: addr, Handler: br, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		log.Printf("OSS bridge (Responses→Chat) on http://%s (%d local endpoints)", addr, len(eps.Get("openai")))
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("OSS bridge stopped: %v", err)
		}
	}()
}

// loadLocalEndpoints seeds the live endpoint list from routing.json.
func loadLocalEndpoints() *routing.EndpointSet {
	eps := &routing.EndpointSet{}
	if path := routingConfigPath(); path != "" {
		if cfg, err := routing.LoadConfig(path); err == nil {
			eps.Set(cfg.LocalEndpoints)
		}
	}
	return eps
}
