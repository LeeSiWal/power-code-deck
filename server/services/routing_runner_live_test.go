package services

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"powercodedeck/internal/routing"
)

// Opt-in: PCD_ROUTING_PROBE_LIVE=1 runs the real CLIs' status/list commands
// (no inference, no paid calls) and prints the discovered matrix.
func TestRoutingProbeLive(t *testing.T) {
	if os.Getenv("PCD_ROUTING_PROBE_LIVE") != "1" {
		t.Skip("set PCD_ROUTING_PROBE_LIVE=1")
	}
	home, _ := os.UserHomeDir()
	p := &routing.Prober{Runner: RoutingRunner{}, HomeDir: home, Timeout: 20 * time.Second}
	start := time.Now()
	for _, s := range p.Probe(context.Background(), 0) {
		b, _ := json.Marshal(map[string]any{"adapter": s.AdapterID, "version": s.Version, "install": s.Installation.Value, "auth": s.Auth.Value, "authSource": s.Auth.Source,
			"authMethod": s.AuthMethod, "billing": s.Billing.Value, "technical": s.Technical.Value, "models": len(s.Models), "env": s.InheritedEnv})
		t.Log(string(b))
		if s.AdapterID == routing.AdapterCodex && s.Installation.Value == routing.Installed && len(s.Models) == 0 {
			t.Errorf("codex model/list returned nothing: %+v", s.Technical)
		}
	}
	t.Logf("probe took %s", time.Since(start))
}
