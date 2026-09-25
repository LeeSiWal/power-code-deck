package main

import (
	"database/sql"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"powercodedeck/internal/orchestration"
	"powercodedeck/internal/providers"
	"powercodedeck/internal/providers/antigravity"
	"powercodedeck/internal/routing"
	"powercodedeck/internal/routing/runroute"
	"powercodedeck/services"
)

// routingConfigPath is PCD_ROUTING_CONFIG, else <user config>/powercodedeck/routing.json.
// An absent file means routing mode "off": the Runs page behaves as before.
func routingConfigPath() string {
	if p := os.Getenv("PCD_ROUTING_CONFIG"); p != "" {
		return p
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "powercodedeck", "routing.json")
}

// setupRouting wires model routing onto the v2 worker. A broken routing.json
// keeps routing off (reported in the UI) instead of stopping the server.
func setupRouting(database *sql.DB, runs *orchestration.Store, worker *orchestration.Worker, rp *services.RunProviders) (*runroute.Coordinator, error) {
	store, err := routing.NewStore(database)
	if err != nil {
		return nil, err
	}
	if err := store.Recover(); err != nil {
		return nil, fmt.Errorf("recover routing state: %w", err)
	}
	policy, err := routing.LoadPolicyEvidence()
	if err != nil {
		return nil, err
	}
	path := routingConfigPath()
	cfg, cfgErr := routing.DefaultConfig(), error(nil)
	if path != "" {
		if cfg, cfgErr = routing.LoadConfig(path); cfgErr != nil {
			log.Printf("routing config %s invalid, routing stays off: %v", path, cfgErr)
			cfg = routing.DefaultConfig()
		}
	}
	home, _ := os.UserHomeDir()
	local := routing.NewLocalClient()
	prober := &routing.Prober{Runner: services.RoutingRunner{}, HomeDir: home, Endpoints: cfg.LocalEndpoints, HTTP: local}
	orchestration.SensitivePath = routing.IsSensitivePath
	worker.SetLaunchFactory(func(id, cwd string, l orchestration.Launch) (providers.Execution, error) {
		switch l.Provider {
		case routing.AdapterClaude:
			return rp.NewWith(providers.Claude, id, cwd, l.Model, l.Effort)
		case routing.AdapterCodex:
			return rp.NewWith(providers.Codex, id, cwd, l.Model, l.Effort)
		case routing.AdapterAntigravity:
			// Same mode as the existing implementation factory.
			return antigravity.New(id, antigravity.Config{Cwd: cwd, Mode: "accept-edits", Model: l.Model, Effort: l.Effort})
		}
		// Local text-only profiles cannot edit a worktree; Runs are code tasks.
		return nil, fmt.Errorf("no Run execution adapter for %q", l.Provider)
	})
	coord, err := runroute.New(runroute.Options{Config: cfg, ConfigErr: cfgErr, Store: store, Runs: runs, Worker: worker, Prober: prober, Policy: policy})
	if err != nil {
		return nil, err
	}
	worker.SetAttemptObserver(coord.OnAttempt)
	log.Printf("Model routing: mode=%s profiles=%d config=%s", cfg.Mode, len(cfg.Profiles), path)
	return coord, nil
}
