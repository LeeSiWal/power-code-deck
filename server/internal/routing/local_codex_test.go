package routing

import "testing"

func localCodexCfg() Config {
	c := cfgWith(Profile{ID: "mac-qwen", Adapter: AdapterCodex, EndpointRef: "mac", Model: "qwen-mlx", Billing: BillingLocal, Tiers: []Tier{VeryEasy}, Allow: true})
	c.LocalEndpoints = append(c.LocalEndpoints, LocalEndpoint{ID: "mac", URL: "http://127.0.0.1:8080", Kind: "openai"})
	return c
}

// Codex on a local model needs the Codex CLI and a live endpoint listing the
// model — not a ChatGPT sign-in — and launches as "oss:<endpoint>:<model>".
func TestLocalCodexProfileGates(t *testing.T) {
	pol := mustPolicy(t)
	cfg := localCodexCfg()
	codexSignedOut := ready(AdapterCodex, BillingSubscription)
	codexSignedOut.Auth = Observation{Value: NotAuthenticated}
	mac := ready("local:mac", BillingLocal)
	mac.Models = []ModelInfo{{ID: "qwen-mlx"}}
	need := Need{Kind: KindCode, Automatic: true, MinTier: VeryEasy}

	cs := Filter(cfg, map[string]AdapterStatus{AdapterCodex: codexSignedOut, "local:mac": mac}, pol, NewHealth(), need)
	if r := reasons(cs, "mac-qwen"); len(r) != 0 {
		t.Fatalf("live endpoint, signed-out codex: excluded for %v", r)
	}
	cs = Filter(cfg, map[string]AdapterStatus{AdapterCodex: codexSignedOut}, pol, NewHealth(), need)
	if !hasReason(cs, "mac-qwen", REndpointUnavailable) {
		t.Fatalf("down endpoint not reported: %v", reasons(cs, "mac-qwen"))
	}
	mac.Models = []ModelInfo{{ID: "other"}}
	cs = Filter(cfg, map[string]AdapterStatus{AdapterCodex: codexSignedOut, "local:mac": mac}, pol, NewHealth(), need)
	if !hasReason(cs, "mac-qwen", RModelNotListed) {
		t.Fatalf("unlisted model not reported: %v", reasons(cs, "mac-qwen"))
	}
	if m := cfg.Profiles[len(cfg.Profiles)-1].LaunchModel(); m != "oss:mac:qwen-mlx" {
		t.Fatalf("launch model %q", m)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}
	bad := localCodexCfg()
	bad.Profiles[len(bad.Profiles)-1].EndpointRef = "lan" // an ollama endpoint: no chat bridge
	if err := bad.Validate(); err == nil {
		t.Fatal("codex profile on a non-openai endpoint accepted")
	}
}
