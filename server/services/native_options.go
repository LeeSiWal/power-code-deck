package services

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// NativeOptions are the per-agent session settings that aren't toolbar-frequency.
//
// Model, permission mode, and effort get their own columns and their own pills because
// they're switched constantly. These four are set once for an agent and then left
// alone, so they live together as one JSON blob: adding a fifth knob later costs a
// struct field instead of a migration, a signature change, and a new WS event.
//
// Every field is optional — a zero value means "don't pass the flag", which restores
// the CLI's own default.
type NativeOptions struct {
	// AddDirs are extra directories the agent may read and write, beyond its cwd.
	// The usual reason is a monorepo sibling or a shared library checked out elsewhere.
	AddDirs []string `json:"addDirs,omitempty"`

	// MaxBudgetUSD caps what one session may spend on API calls. 0 = no cap.
	MaxBudgetUSD float64 `json:"maxBudgetUsd,omitempty"`

	// Autocompact is the auto-compaction window: "auto", or a token count as a string.
	Autocompact string `json:"autocompact,omitempty"`

	// FallbackModel is tried when the primary is overloaded or unavailable. The CLI
	// accepts a comma-separated list and retries the primary each user turn.
	FallbackModel string `json:"fallbackModel,omitempty"`
}

// Budget bounds. The ceiling isn't a policy about what a session is worth — it's a
// typo guard: 50000 entered where 50 was meant should not be silently accepted.
const maxBudgetCeilingUSD = 1000

// The CLI documents the auto-compact window as 100k–1M tokens.
const (
	autocompactMinTokens = 100_000
	autocompactMaxTokens = 1_000_000
)

// DefaultAutocompact is the window a session uses until someone chooses otherwise.
//
// Left unset, the CLI lets context run to the full 1M window before compacting, and
// every request in between re-reads the whole conversation. Measured on a real deck
// session: context reached 999,545 tokens and the session read 866M input tokens in
// total, ~74% of its cost. Capping at 200K would have cut that roughly in half.
//
// 200K is a starting point, not a law — it is expressible in both directions from the
// settings sheet, and a session that needs the old behaviour can ask for 1000000.
const DefaultAutocompact = "200000"

// maxAddDirs bounds how much extra filesystem one session can be handed. Well past
// any real monorepo, low enough that a runaway list can't build an unreadable command.
const maxAddDirs = 16

// modelSlug matches the model ids the CLI takes — aliases ("opus") and full names
// ("claude-sonnet-5"), including the bracketed context variants ("claude-opus-4-8[1m]").
//
// The first character MUST be alphanumeric. Model names are full of hyphens, so a
// pattern that merely allows them also accepts "--dangerously-skip-permissions" as a
// perfectly good "model name" — which would then be handed to the CLI as the value of
// --fallback-model. Anchoring the first character is what keeps a flag-shaped string
// out of argv.
var modelSlug = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._\[\]-]*$`)

// Normalize validates and cleans the options, returning the usable set plus a message
// for anything dropped.
//
// It never returns an error for a bad field. These values reach a command line, and a
// session that refuses to start is a worse outcome than one that starts without the
// setting the user fat-fingered — so bad input is dropped and reported, not fatal.
// The caller surfaces the message; the session runs either way.
func (o NativeOptions) Normalize() (NativeOptions, []string) {
	var out NativeOptions
	var dropped []string

	seen := make(map[string]bool, len(o.AddDirs))
	for _, dir := range o.AddDirs {
		dir = strings.TrimSpace(dir)
		if dir == "" {
			continue
		}
		if !filepath.IsAbs(dir) {
			dropped = append(dropped, fmt.Sprintf("추가 경로 %q: 절대 경로가 아닙니다", dir))
			continue
		}
		dir = filepath.Clean(dir)
		// An unreadable or missing path is worth reporting rather than passing on: the
		// CLI would either fail the spawn or silently grant nothing, and both look like
		// "the deck ignored my setting".
		info, err := os.Stat(dir)
		if err != nil {
			dropped = append(dropped, fmt.Sprintf("추가 경로 %q: 찾을 수 없습니다", dir))
			continue
		}
		if !info.IsDir() {
			dropped = append(dropped, fmt.Sprintf("추가 경로 %q: 디렉터리가 아닙니다", dir))
			continue
		}
		if seen[dir] {
			continue
		}
		if len(out.AddDirs) >= maxAddDirs {
			dropped = append(dropped, fmt.Sprintf("추가 경로는 최대 %d개입니다 — 나머지는 무시했습니다", maxAddDirs))
			break
		}
		seen[dir] = true
		out.AddDirs = append(out.AddDirs, dir)
	}

	switch {
	case o.MaxBudgetUSD < 0:
		dropped = append(dropped, "예산 상한이 음수입니다 — 무시했습니다")
	case o.MaxBudgetUSD > maxBudgetCeilingUSD:
		dropped = append(dropped, fmt.Sprintf("예산 상한이 $%d를 넘습니다 — 무시했습니다", maxBudgetCeilingUSD))
	default:
		out.MaxBudgetUSD = o.MaxBudgetUSD
	}

	if ac := strings.TrimSpace(o.Autocompact); ac != "" {
		if ac == "auto" {
			out.Autocompact = ac
		} else if n, err := strconv.Atoi(ac); err == nil && n >= autocompactMinTokens && n <= autocompactMaxTokens {
			out.Autocompact = strconv.Itoa(n)
		} else {
			dropped = append(dropped, fmt.Sprintf(
				"자동 압축 값 %q: 'auto' 또는 %d~%d 토큰만 가능합니다", ac, autocompactMinTokens, autocompactMaxTokens))
		}
	}

	if fm := strings.TrimSpace(o.FallbackModel); fm != "" {
		var models []string
		for _, m := range strings.Split(fm, ",") {
			m = strings.TrimSpace(m)
			if m == "" {
				continue
			}
			if !modelSlug.MatchString(m) {
				dropped = append(dropped, fmt.Sprintf("폴백 모델 %q: 모델 이름 형식이 아닙니다", m))
				continue
			}
			models = append(models, m)
		}
		out.FallbackModel = strings.Join(models, ",")
	}

	return out, dropped
}

// IsZero reports whether nothing is set, so callers can skip storing an empty blob.
func (o NativeOptions) IsZero() bool {
	return len(o.AddDirs) == 0 && o.MaxBudgetUSD == 0 && o.Autocompact == "" && o.FallbackModel == ""
}

// EncodeNativeOptions serializes options for the DB. An empty set stores as "" so a
// row that was never configured stays visibly empty rather than holding "{}".
func EncodeNativeOptions(o NativeOptions) string {
	if o.IsZero() {
		return ""
	}
	b, err := json.Marshal(o)
	if err != nil {
		return ""
	}
	return string(b)
}

// DecodeNativeOptions parses what EncodeNativeOptions wrote. Unparseable JSON yields
// the zero value: a corrupted row costs the settings, never the session.
func DecodeNativeOptions(raw string) NativeOptions {
	var o NativeOptions
	if strings.TrimSpace(raw) == "" {
		return o
	}
	if err := json.Unmarshal([]byte(raw), &o); err != nil {
		return NativeOptions{}
	}
	return o
}
