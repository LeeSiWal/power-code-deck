package handlers

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"powercodedeck/internal/routing"
)

func TestLocalModelConfigEdits(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "routing.json")
	os.WriteFile(path, []byte(`{"version":1,"mode":"auto","profiles":[{"id":"claude-sonnet5-low","adapter":"claude","model":"claude-sonnet-5","effort":"low","billing":"subscription","tiers":["EASY","MEDIUM"],"allow":true}]}`), 0600)
	e, err := endpointFor("studio", localModelInput{URL: "http://192.168.1.22:8080/", Kind: "openai", Model: "qwen"})
	if err != nil || !e.LocalNetOnly || e.URL != "http://192.168.1.22:8080" {
		t.Fatalf("endpoint %+v %v", e, err)
	}
	cfg, err := routing.UpdateConfigFile(path, func(raw map[string]any) error {
		setEndpoint(raw, e)
		setLocalProfile(raw, e, true)
		setJudge(raw, "studio", true)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.LocalEndpoints) != 1 || cfg.TierJudge == nil || cfg.Profiles[0].ID != "local-studio" || !cfg.Profiles[0].Allow || cfg.Profiles[0].LaunchModel() != "oss:studio:qwen" {
		t.Fatalf("after add: %+v", cfg)
	}
	var raw map[string]any
	b, _ := os.ReadFile(path)
	json.Unmarshal(b, &raw)
	if ps := asList(raw["profiles"]); len(ps) != 2 || ps[1].(map[string]any)["effort"] != "low" {
		t.Fatalf("existing profile changed: %v", ps)
	}
	if baks, _ := filepath.Glob(path + ".bak-*"); len(baks) != 1 {
		t.Fatalf("backups %v", baks)
	}
	// Turning easy work off keeps the profile (sessions may reference it).
	cfg, _ = routing.UpdateConfigFile(path, func(raw map[string]any) error { setLocalProfile(raw, e, false); return nil })
	if cfg.Profiles[0].ID != "local-studio" || cfg.Profiles[0].Allow {
		t.Fatalf("after off: %+v", cfg.Profiles[0])
	}
	// Removing the endpoint also removes its profile and the judge.
	cfg, err = routing.UpdateConfigFile(path, func(raw map[string]any) error {
		raw["localEndpoints"] = filterList(raw["localEndpoints"], func(m map[string]any) bool { return m["id"] != "studio" })
		raw["profiles"] = filterList(raw["profiles"], func(m map[string]any) bool { return m["endpointRef"] != "studio" })
		setJudge(raw, "studio", false)
		return nil
	})
	if err != nil || len(cfg.LocalEndpoints) != 0 || cfg.TierJudge != nil || len(cfg.Profiles) != 1 {
		t.Fatalf("after remove: %+v %v", cfg, err)
	}
	// An invalid result is refused and the file is left as it was.
	before, _ := os.ReadFile(path)
	if _, err := routing.UpdateConfigFile(path, func(raw map[string]any) error { setJudge(raw, "ghost", true); return nil }); err == nil {
		t.Fatal("judge on an unknown endpoint accepted")
	}
	if after, _ := os.ReadFile(path); string(after) != string(before) {
		t.Fatal("refused edit changed the file")
	}
	if _, err := endpointFor("x", localModelInput{URL: "http://8.8.8.8:8080"}); err == nil {
		t.Fatal("public address accepted")
	}
}
