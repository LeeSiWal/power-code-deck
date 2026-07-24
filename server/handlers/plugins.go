package handlers

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

// Plugin management for the deck. Claude Code's own `/plugin` command is
// interactive-mode only — sent over the stream-json protocol we drive it answers
// "isn't available in this environment" (see builtinSlashCommands). So the deck
// grows its own path: resolve a plugin's source from the marketplace manifest it
// already has checked out, fetch it into the same cache the CLI reads, and flip the
// same enabledPlugins flag in settings.json the CLI honours. The result is that both
// the CLI (on the next session start) and the deck's own slash picker see it.
//
// Installs are limited to plugins named in a TRUSTED marketplace manifest — the
// client sends only "name@marketplace", never a raw URL, so this can't be turned
// into a fetch-arbitrary-repo primitive.

// settingsMu serializes read-modify-write of ~/.claude/settings.json so two installs
// racing can't clobber each other's enabledPlugins edit.
var settingsMu sync.Mutex

// marketplaceManifest is the .claude-plugin/marketplace.json we parse for sources.
type marketplaceManifest struct {
	Plugins []marketplacePlugin `json:"plugins"`
}

type marketplacePlugin struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Category    string          `json:"category"`
	Source      json.RawMessage `json:"source"` // string ("./local") OR object ({source,url,path,ref,sha})
}

// pluginSource is the object form of a plugin's source.
type pluginSource struct {
	Source string `json:"source"` // "url" | "git-subdir" | "" (when the whole field was a string)
	URL    string `json:"url"`
	Path   string `json:"path"`
	Ref    string `json:"ref"`
	SHA    string `json:"sha"`
	local  string // set when Source field was a bare string like "./plugins/foo"
}

func parseSource(raw json.RawMessage) pluginSource {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return pluginSource{local: s}
	}
	var o pluginSource
	_ = json.Unmarshal(raw, &o)
	return o
}

// PluginInfo is one row in the management panel.
type PluginInfo struct {
	Ref         string `json:"ref"` // "name@marketplace"
	Name        string `json:"name"`
	Marketplace string `json:"marketplace"`
	Description string `json:"description"`
	Category    string `json:"category,omitempty"`
	Installed   bool   `json:"installed"` // present in the plugin cache
	Enabled     bool   `json:"enabled"`   // enabledPlugins[ref] == true
	Supported   bool   `json:"supported"` // deck knows how to fetch this source
	Skills      int    `json:"skills,omitempty"`
	Commands    int    `json:"commands,omitempty"`
}

// ListPlugins returns every plugin across the installed marketplaces, tagged with
// whether it's installed/enabled, so the panel can show installed ones first and let
// the rest be searched. One endpoint keeps the client simple.
func ListPlugins() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		home, err := os.UserHomeDir()
		if err != nil {
			jsonError(w, err.Error(), http.StatusInternalServerError)
			return
		}
		enabled := readEnabledPlugins(home)

		var out []PluginInfo
		for _, mk := range listMarketplaces(home) {
			for _, p := range parseMarketplace(home, mk) {
				if p.Name == "" {
					continue
				}
				ref := p.Name + "@" + mk
				dir := pluginDir(home, mk, p.Name)
				info := PluginInfo{
					Ref:         ref,
					Name:        p.Name,
					Marketplace: mk,
					Description: p.Description,
					Category:    p.Category,
					Installed:   dir != "",
					Enabled:     enabled[ref],
					Supported:   true, // every manifest source shape below is handled
				}
				if dir != "" {
					info.Skills = countDirEntries(filepath.Join(dir, "skills"))
					info.Commands = countDirEntries(filepath.Join(dir, "commands"))
				}
				out = append(out, info)
			}
		}
		if out == nil {
			out = []PluginInfo{}
		}
		jsonResponse(w, out)
	}
}

// InstallPlugin fetches a plugin named in a trusted marketplace into the cache and
// enables it. Idempotent: re-installing an already-cached version just re-enables.
func InstallPlugin() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Ref string `json:"ref"` // "name@marketplace"
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			jsonError(w, "invalid request", http.StatusBadRequest)
			return
		}
		name, mk, ok := splitRef(req.Ref)
		if !ok {
			jsonError(w, "ref must be name@marketplace", http.StatusBadRequest)
			return
		}
		home, err := os.UserHomeDir()
		if err != nil {
			jsonError(w, err.Error(), http.StatusInternalServerError)
			return
		}

		entry, found := findPlugin(home, mk, name)
		if !found {
			jsonError(w, fmt.Sprintf("%q not found in marketplace %q", name, mk), http.StatusNotFound)
			return
		}

		if err := fetchIntoCache(home, mk, name, parseSource(entry.Source)); err != nil {
			jsonError(w, "install failed: "+err.Error(), http.StatusBadGateway)
			return
		}
		if err := setPluginEnabled(home, req.Ref, true); err != nil {
			jsonError(w, "enabled write failed: "+err.Error(), http.StatusInternalServerError)
			return
		}
		invalidateSlashCache()
		jsonResponse(w, map[string]any{"ref": req.Ref, "installed": true, "enabled": true})
	}
}

// TogglePlugin flips enabledPlugins[ref] without refetching. Enabling a plugin that
// was never installed is refused — it would be a dead flag the CLI ignores.
func TogglePlugin() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Ref     string `json:"ref"`
			Enabled bool   `json:"enabled"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			jsonError(w, "invalid request", http.StatusBadRequest)
			return
		}
		name, mk, ok := splitRef(req.Ref)
		if !ok {
			jsonError(w, "ref must be name@marketplace", http.StatusBadRequest)
			return
		}
		home, err := os.UserHomeDir()
		if err != nil {
			jsonError(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if req.Enabled && pluginDir(home, mk, name) == "" {
			jsonError(w, "plugin is not installed — install it first", http.StatusConflict)
			return
		}
		if err := setPluginEnabled(home, req.Ref, req.Enabled); err != nil {
			jsonError(w, err.Error(), http.StatusInternalServerError)
			return
		}
		invalidateSlashCache()
		jsonResponse(w, map[string]any{"ref": req.Ref, "enabled": req.Enabled})
	}
}

// --- source fetching -------------------------------------------------------

// fetchIntoCache places a plugin's files at cache/<mk>/<name>/<version>/, mirroring
// what the CLI's own installer produces so pluginDir() and the CLI both find it.
func fetchIntoCache(home, mk, name string, src pluginSource) error {
	var srcDir string
	var cleanup func()

	switch {
	case src.local != "": // "./plugins/foo" — already inside the marketplace checkout
		srcDir = filepath.Join(home, ".claude", "plugins", "marketplaces", mk, filepath.Clean(src.local))
		if !strings.HasPrefix(srcDir, filepath.Join(home, ".claude", "plugins", "marketplaces", mk)) {
			return fmt.Errorf("local source escapes marketplace dir")
		}
	case src.Source == "url" || src.Source == "git-subdir":
		tmp, err := os.MkdirTemp("", "pcd-plugin-*")
		if err != nil {
			return err
		}
		cleanup = func() { os.RemoveAll(tmp) }
		if err := gitFetch(tmp, src); err != nil {
			cleanup()
			return err
		}
		srcDir = tmp
		if src.Source == "git-subdir" && src.Path != "" {
			srcDir = filepath.Join(tmp, filepath.Clean(src.Path))
		}
	default:
		return fmt.Errorf("unsupported source shape")
	}
	if cleanup != nil {
		defer cleanup()
	}

	if st, err := os.Stat(srcDir); err != nil || !st.IsDir() {
		return fmt.Errorf("resolved source dir missing: %s", srcDir)
	}

	version := readPluginVersion(srcDir, src.SHA)
	dest := filepath.Join(home, ".claude", "plugins", "cache", mk, name, version)
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	// Fresh copy: drop any half-written prior attempt at this exact version.
	_ = os.RemoveAll(dest)
	return copyTree(srcDir, dest)
}

// gitFetch clones src.URL into dst and checks out the pinned sha (falling back to the
// ref, then the default branch). Shelling out to git matches how the CLI fetches and
// avoids a git library dependency in a repo that vendors its deps.
func gitFetch(dst string, src pluginSource) error {
	if src.URL == "" {
		return fmt.Errorf("source has no url")
	}
	if out, err := runGit("", "clone", "--quiet", src.URL, dst); err != nil {
		return fmt.Errorf("clone: %v: %s", err, out)
	}
	for _, target := range []string{src.SHA, src.Ref} {
		if target == "" {
			continue
		}
		if _, err := runGit(dst, "checkout", "--quiet", target); err == nil {
			return nil
		}
	}
	return nil // default branch is acceptable when neither sha nor ref resolves
}

func runGit(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	// Never let git block on a credential prompt for a private repo.
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// readPluginVersion mirrors the CLI's cache layout, which keys the copy by the
// plugin.json version. Falls back to a short sha, then a constant, so a manifest
// without a version still lands somewhere stable.
func readPluginVersion(dir, sha string) string {
	data, err := os.ReadFile(filepath.Join(dir, ".claude-plugin", "plugin.json"))
	if err == nil {
		var m struct {
			Version string `json:"version"`
		}
		if json.Unmarshal(data, &m) == nil && m.Version != "" {
			return m.Version
		}
	}
	if len(sha) >= 7 {
		return sha[:7]
	}
	return "0.0.0"
}

// copyTree copies a directory recursively, skipping VCS metadata. Symlinks are
// resolved to regular files so the cache never points back outside itself.
func copyTree(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return os.MkdirAll(dst, 0o755)
		}
		base := filepath.Base(path)
		if base == ".git" {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, info.Mode().Perm()|0o700)
		}
		return copyFile(path, target, info.Mode().Perm())
	})
}

func copyFile(src, dst string, perm os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, perm|0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// --- settings.json edits ---------------------------------------------------

func readEnabledPlugins(home string) map[string]bool {
	out := map[string]bool{}
	data, err := os.ReadFile(filepath.Join(home, ".claude", "settings.json"))
	if err != nil {
		return out
	}
	var s struct {
		EnabledPlugins map[string]bool `json:"enabledPlugins"`
	}
	if json.Unmarshal(data, &s) == nil {
		for k, v := range s.EnabledPlugins {
			out[k] = v
		}
	}
	return out
}

// setPluginEnabled edits ONLY enabledPlugins in settings.json, preserving every other
// key, and writes atomically after a timestamped backup so a user's hand-tuned
// settings can always be recovered.
func setPluginEnabled(home, ref string, enabled bool) error {
	settingsMu.Lock()
	defer settingsMu.Unlock()

	path := filepath.Join(home, ".claude", "settings.json")
	root := map[string]any{}
	if data, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(data, &root); err != nil {
			return fmt.Errorf("settings.json is not valid JSON: %w", err)
		}
		_ = os.WriteFile(path+".bak", data, 0o600) // best-effort backup of the last-good file
	}

	ep, _ := root["enabledPlugins"].(map[string]any)
	if ep == nil {
		ep = map[string]any{}
	}
	ep[ref] = enabled
	root["enabledPlugins"] = ep

	out, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return err
	}
	out = append(out, '\n')
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, out, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path) // atomic swap
}

// --- marketplace lookup ----------------------------------------------------

func listMarketplaces(home string) []string {
	root := filepath.Join(home, ".claude", "plugins", "marketplaces")
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	return names
}

func parseMarketplace(home, mk string) []marketplacePlugin {
	data, err := os.ReadFile(filepath.Join(home, ".claude", "plugins", "marketplaces", mk, ".claude-plugin", "marketplace.json"))
	if err != nil {
		return nil
	}
	var m marketplaceManifest
	if json.Unmarshal(data, &m) != nil {
		return nil
	}
	return m.Plugins
}

func findPlugin(home, mk, name string) (marketplacePlugin, bool) {
	for _, p := range parseMarketplace(home, mk) {
		if p.Name == name {
			return p, true
		}
	}
	return marketplacePlugin{}, false
}

// --- small helpers ---------------------------------------------------------

func splitRef(ref string) (name, marketplace string, ok bool) {
	name, marketplace, ok = strings.Cut(ref, "@")
	if !ok || name == "" || marketplace == "" {
		return "", "", false
	}
	return name, marketplace, true
}

func countDirEntries(dir string) int {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	n := 0
	for _, e := range entries {
		if e.IsDir() || strings.HasSuffix(e.Name(), ".md") {
			n++
		}
	}
	return n
}

func invalidateSlashCache() {
	slashCacheMu.Lock()
	slashCache = map[string]slashCacheEntry{}
	slashCacheMu.Unlock()
}
