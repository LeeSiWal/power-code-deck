package handlers

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// fakeHome builds a $HOME with one marketplace holding a single local-source plugin
// (commands/ + skills/) so install can be exercised without touching the network.
func fakeHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	mkRoot := filepath.Join(home, ".claude", "plugins", "marketplaces", "test-mk")

	// The plugin's files, referenced by a "./plugins/demo" local source.
	demo := filepath.Join(mkRoot, "plugins", "demo")
	mustWrite(t, filepath.Join(demo, ".claude-plugin", "plugin.json"), `{"name":"demo","version":"1.2.3"}`)
	mustWrite(t, filepath.Join(demo, "skills", "alpha", "SKILL.md"), "---\nname: alpha\ndescription: a\n---\n")
	mustWrite(t, filepath.Join(demo, "commands", "run.md"), "do a thing\n")

	manifest := marketplaceManifest{Plugins: []marketplacePlugin{{
		Name:        "demo",
		Description: "a demo plugin",
		Source:      json.RawMessage(`"./plugins/demo"`),
	}}}
	data, _ := json.Marshal(manifest)
	mustWrite(t, filepath.Join(mkRoot, ".claude-plugin", "marketplace.json"), string(data))
	return home
}

func mustWrite(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestParseSource(t *testing.T) {
	if s := parseSource(json.RawMessage(`"./plugins/x"`)); s.local != "./plugins/x" {
		t.Fatalf("local source not parsed: %+v", s)
	}
	s := parseSource(json.RawMessage(`{"source":"git-subdir","url":"u","path":"p","sha":"abc"}`))
	if s.Source != "git-subdir" || s.URL != "u" || s.Path != "p" || s.SHA != "abc" {
		t.Fatalf("object source not parsed: %+v", s)
	}
}

func TestSplitRef(t *testing.T) {
	if _, _, ok := splitRef("bad"); ok {
		t.Fatal("expected failure without @")
	}
	if _, _, ok := splitRef("@mk"); ok {
		t.Fatal("expected failure with empty name")
	}
	name, mk, ok := splitRef("demo@test-mk")
	if !ok || name != "demo" || mk != "test-mk" {
		t.Fatalf("got %q %q %v", name, mk, ok)
	}
}

func TestFetchLocalSourceIntoCache(t *testing.T) {
	home := fakeHome(t)
	entry, found := findPlugin(home, "test-mk", "demo")
	if !found {
		t.Fatal("demo not found in fake marketplace")
	}
	if err := fetchIntoCache(home, "test-mk", "demo", parseSource(entry.Source)); err != nil {
		t.Fatal(err)
	}
	// Version came from plugin.json, matching the CLI's cache layout.
	got := pluginDir(home, "test-mk", "demo")
	want := filepath.Join(home, ".claude", "plugins", "cache", "test-mk", "demo", "1.2.3")
	if got != want {
		t.Fatalf("pluginDir = %q, want %q", got, want)
	}
	if countDirEntries(filepath.Join(got, "skills")) != 1 {
		t.Fatal("skill not copied")
	}
	if countDirEntries(filepath.Join(got, "commands")) != 1 {
		t.Fatal("command not copied")
	}
}

// setPluginEnabled must touch only enabledPlugins and leave every other setting alone.
func TestSetPluginEnabledPreservesOtherKeys(t *testing.T) {
	home := t.TempDir()
	settings := filepath.Join(home, ".claude", "settings.json")
	mustWrite(t, settings, `{"theme":"dark","enabledPlugins":{"keep@mk":true}}`)

	if err := setPluginEnabled(home, "demo@test-mk", true); err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	data, _ := os.ReadFile(settings)
	if err := json.Unmarshal(data, &root); err != nil {
		t.Fatal(err)
	}
	if root["theme"] != "dark" {
		t.Fatalf("unrelated key lost: %+v", root)
	}
	ep := root["enabledPlugins"].(map[string]any)
	if ep["keep@mk"] != true || ep["demo@test-mk"] != true {
		t.Fatalf("enabledPlugins wrong: %+v", ep)
	}
	// A backup of the prior file must exist for recovery.
	if _, err := os.Stat(settings + ".bak"); err != nil {
		t.Fatal("no backup written")
	}

	// Toggling off flips just that one key.
	if err := setPluginEnabled(home, "demo@test-mk", false); err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(settings)
	_ = json.Unmarshal(data, &root)
	ep = root["enabledPlugins"].(map[string]any)
	if ep["demo@test-mk"] != false {
		t.Fatalf("disable did not take: %+v", ep)
	}
}

func TestReadEnabledPlugins(t *testing.T) {
	home := t.TempDir()
	mustWrite(t, filepath.Join(home, ".claude", "settings.json"),
		`{"enabledPlugins":{"a@x":true,"b@y":false}}`)
	ep := readEnabledPlugins(home)
	if !ep["a@x"] || ep["b@y"] {
		t.Fatalf("enabled map wrong: %+v", ep)
	}
}
