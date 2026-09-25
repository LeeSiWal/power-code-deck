package ossbridge

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ModelPrefix marks a session model that runs through the bridge:
// "oss:<endpoint>:<model>". Encoding the endpoint in the model string lets it
// travel through every existing path (saved native config, model switches,
// restarts, tool switches) with no extra plumbing.
const ModelPrefix = "oss:"

// Model builds the session model string for a bridged local model.
func Model(endpoint, model string) string { return ModelPrefix + endpoint + ":" + model }

// ParseModel splits a bridged session model string.
func ParseModel(s string) (endpoint, model string, ok bool) {
	rest, found := strings.CutPrefix(s, ModelPrefix)
	if !found {
		return "", "", false
	}
	endpoint, model, ok = strings.Cut(rest, ":")
	return endpoint, model, ok && endpoint != "" && model != ""
}

// Port is the bridge's loopback port (PCD_OSS_BRIDGE_PORT, default 33090).
func Port() string {
	if p := os.Getenv("PCD_OSS_BRIDGE_PORT"); p != "" {
		return p
	}
	return "33090"
}

// instructions replace Codex's ~17k-character cloud prompt: a local model has
// a 64k context and should spend it on the work. The tool names are the ones
// the bridge shows the model (edit_file/create_file stand in for apply_patch).
const instructions = `You are a coding agent working in the user's project directory on their machine.
- Answer in the language the user writes in.
- Use exec_command to look around (ls, cat, grep, sed -n) and to run checks such as tests or builds. Keep commands short and non-interactive.
- When the user names a file loosely ("the test file", "설정 파일"), run ls (or ls -R for a small tree) first and use the existing file that matches. Create a new file only when the user asks for a new one.
- Before edit_file, read the file with cat in this turn and copy old_string from that output exactly.
- Change files only with edit_file (replace an exact block of lines you have just read) or create_file (new files). Do not edit files with shell commands such as sed -i or echo >.
- After changing something, check it (re-read the file or run the relevant command).
- Never say a change is done unless a tool result confirmed it. If something failed, say so plainly and what you tried.
- If a tool call failed, fix it and call the tool again right away; do not end your answer by saying you will try again.
- Keep the final answer short: what changed, and anything the user must know.`

// CodexArgs returns the codex -c overrides that point one Codex process at the
// bridge for this endpoint/model, writing the model's catalog entry first.
// Without the entry Codex treats the model as unknown ("fallback metadata")
// and does not offer apply_patch, so the model could not edit files.
func CodexArgs(endpoint, model string) ([]string, error) {
	path, err := writeCatalog(model)
	if err != nil {
		return nil, err
	}
	provider := "pcd_" + safeID(endpoint)
	base := "http://127.0.0.1:" + Port() + "/" + endpoint + "/v1"
	q := func(s string) string { b, _ := json.Marshal(s); return string(b) } // TOML basic string
	return []string{
		"-c", "model_providers." + provider + ".name=" + q("PowerCodeDeck local ("+endpoint+")"),
		"-c", "model_providers." + provider + ".base_url=" + q(base),
		"-c", "model_providers." + provider + ".wire_api=" + q("responses"),
		"-c", "model_provider=" + q(provider),
		"-c", "model_catalog_json=" + q(path),
	}, nil
}

// writeCatalog stores a one-model catalog (one file per model, so concurrent
// sessions on different models never race on it) and returns its path.
func writeCatalog(model string) (string, error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	dir = filepath.Join(dir, "powercodedeck", "codex-local-models")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(model))
	path := filepath.Join(dir, hex.EncodeToString(sum[:6])+".json")
	entry := map[string]any{
		"slug": model, "display_name": model, "description": "Local model via the PowerCodeDeck bridge",
		"default_reasoning_level": nil, "supported_reasoning_levels": []any{},
		"shell_type": "shell_command", "visibility": "list", "supported_in_api": true, "priority": 100,
		"base_instructions": instructions, "apply_patch_tool_type": "freeform",
		"truncation_policy":            map[string]any{"mode": "tokens", "limit": 10000},
		"supports_parallel_tool_calls": false, "context_window": 65536,
		"experimental_supported_tools": []any{}, "input_modalities": []string{"text"}, "support_verbosity": false,
	}
	b, err := json.Marshal(map[string]any{"models": []any{entry}})
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(path, b, 0600); err != nil {
		return "", fmt.Errorf("write codex catalog: %w", err)
	}
	return path, nil
}

func safeID(s string) string {
	return strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' {
			return r
		}
		return '_'
	}, s)
}
