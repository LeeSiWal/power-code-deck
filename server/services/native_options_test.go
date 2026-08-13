package services

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// Normalize is what stands between a typed-in value and a command line. A bad value
// must be DROPPED and reported — never passed through (the spawn would fail, taking
// the whole session with it) and never fatal (the session should still start without
// the setting the user fat-fingered).
func TestNormalizeDropsBadValuesWithoutFailing(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "a-file")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	opts := NativeOptions{
		AddDirs: []string{
			dir,                        // valid
			dir,                        // duplicate — collapses
			"relative/path",            // not absolute
			filepath.Join(dir, "nope"), // does not exist
			file,                       // exists but isn't a directory
			"  " + dir + "  ",          // whitespace, same dir → still a duplicate
		},
		MaxBudgetUSD:  25,
		Autocompact:   "auto",
		FallbackModel: "claude-sonnet-5, claude-opus-4-8[1m] ,,",
	}

	got, dropped := opts.Normalize()

	if len(got.AddDirs) != 1 || got.AddDirs[0] != dir {
		t.Errorf("AddDirs = %v, want exactly [%s]", got.AddDirs, dir)
	}
	if len(dropped) != 3 {
		t.Errorf("dropped %d reason(s) %v, want 3 (relative, missing, not-a-dir)", len(dropped), dropped)
	}
	if got.MaxBudgetUSD != 25 {
		t.Errorf("MaxBudgetUSD = %v, want 25", got.MaxBudgetUSD)
	}
	if got.Autocompact != "auto" {
		t.Errorf("Autocompact = %q, want auto", got.Autocompact)
	}
	// Empty entries are skipped, and the survivors keep their order.
	if got.FallbackModel != "claude-sonnet-5,claude-opus-4-8[1m]" {
		t.Errorf("FallbackModel = %q", got.FallbackModel)
	}
}

func TestNormalizeBudgetBounds(t *testing.T) {
	for _, tc := range []struct {
		given   float64
		want    float64
		dropped bool
	}{
		{given: 0, want: 0},                                      // unset
		{given: 12.5, want: 12.5},                                // ordinary
		{given: -1, want: 0, dropped: true},                      // negative
		{given: maxBudgetCeilingUSD + 1, want: 0, dropped: true}, // typo guard
	} {
		got, dropped := NativeOptions{MaxBudgetUSD: tc.given}.Normalize()
		if got.MaxBudgetUSD != tc.want {
			t.Errorf("budget %v → %v, want %v", tc.given, got.MaxBudgetUSD, tc.want)
		}
		if (len(dropped) > 0) != tc.dropped {
			t.Errorf("budget %v: dropped=%v, want %v", tc.given, dropped, tc.dropped)
		}
	}
}

func TestNormalizeAutocompactAcceptsOnlyDocumentedValues(t *testing.T) {
	for _, tc := range []struct{ given, want string }{
		{"", ""},         // unset
		{"auto", "auto"}, //
		{strconv.Itoa(autocompactMinTokens), strconv.Itoa(autocompactMinTokens)},
		{strconv.Itoa(autocompactMaxTokens), strconv.Itoa(autocompactMaxTokens)},
		{"50000", ""},            // below the documented floor
		{"2000000", ""},          // above the ceiling
		{"AUTO", ""},             // the CLI takes lowercase
		{"200000; rm -rf /", ""}, // not a number at all
	} {
		got, _ := NativeOptions{Autocompact: tc.given}.Normalize()
		if got.Autocompact != tc.want {
			t.Errorf("autocompact %q → %q, want %q", tc.given, got.Autocompact, tc.want)
		}
	}
}

// A fallback "model" that isn't a model name must not reach the command line.
func TestNormalizeRejectsNonModelFallbacks(t *testing.T) {
	got, dropped := NativeOptions{FallbackModel: "--dangerously-skip-permissions, ok-model"}.Normalize()
	if strings.Contains(got.FallbackModel, "--") {
		t.Fatalf("a flag-shaped value survived: %q", got.FallbackModel)
	}
	if got.FallbackModel != "ok-model" {
		t.Errorf("FallbackModel = %q, want ok-model", got.FallbackModel)
	}
	if len(dropped) != 1 {
		t.Errorf("dropped = %v, want one reason", dropped)
	}
}

func TestEncodeDecodeRoundTrip(t *testing.T) {
	// Nothing set stores as "" so an unconfigured row stays visibly empty.
	if s := EncodeNativeOptions(NativeOptions{}); s != "" {
		t.Errorf("empty options encoded to %q, want \"\"", s)
	}
	in := NativeOptions{AddDirs: []string{"/a", "/b"}, MaxBudgetUSD: 3.5, Autocompact: "auto", FallbackModel: "m"}
	out := DecodeNativeOptions(EncodeNativeOptions(in))
	if len(out.AddDirs) != 2 || out.AddDirs[1] != "/b" || out.MaxBudgetUSD != 3.5 ||
		out.Autocompact != "auto" || out.FallbackModel != "m" {
		t.Errorf("round trip lost data: %+v", out)
	}
	// A corrupted row costs the settings, never the session.
	if got := DecodeNativeOptions("{not json"); !got.IsZero() {
		t.Errorf("corrupt JSON decoded to %+v, want zero value", got)
	}
}

// The flags must appear only when configured — an empty option has to leave the CLI's
// own default alone rather than passing an empty value.
func TestBuildArgsIncludesOptionsOnlyWhenSet(t *testing.T) {
	bare := strings.Join(NewClaudeDriver(ClaudeConfig{SessionID: "a1", SelfPath: "/opt/pcd"}).buildArgs(), " ")
	// These three are genuinely opt-in: with nothing configured they must not appear,
	// or the deck would be inventing extra directories, a spend cap, and a fallback
	// model nobody asked for.
	for _, flag := range []string{"--add-dir", "--max-budget-usd", "--fallback-model"} {
		if strings.Contains(bare, flag) {
			t.Errorf("unset options still passed %s: %s", flag, bare)
		}
	}
	// Auto-compaction is the exception, for the same reason effort is: leaving it off
	// means the CLI's own policy (grow to the full 1M window), which is the single
	// largest cost driver in a long session. Unset must mean the deck's default.
	if !strings.Contains(bare, "--autocompact "+DefaultAutocompact) {
		t.Errorf("--autocompact not pinned to the deck default: %s", bare)
	}
	// Sub-agent forwarding is not a setting — it is always on, or the deck can never
	// show what a sub-agent actually did.
	if !strings.Contains(bare, "--forward-subagent-text") {
		t.Errorf("--forward-subagent-text missing: %s", bare)
	}

	d := NewClaudeDriver(ClaudeConfig{
		SessionID: "a1", SelfPath: "/opt/pcd",
		Options: NativeOptions{
			AddDirs:       []string{"/one", "/two"},
			MaxBudgetUSD:  12.5,
			Autocompact:   "auto",
			FallbackModel: "claude-sonnet-5",
		},
	})
	args := d.buildArgs()
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "--add-dir /one /two") {
		t.Errorf("add-dir not passed as one variadic flag: %s", joined)
	}
	if !strings.Contains(joined, "--max-budget-usd 12.5") {
		t.Errorf("budget missing or misformatted: %s", joined)
	}
	if !strings.Contains(joined, "--autocompact auto") || !strings.Contains(joined, "--fallback-model claude-sonnet-5") {
		t.Errorf("autocompact/fallback missing: %s", joined)
	}
	// --add-dir's paths must be separate argv entries, not one glued string: the CLI
	// splits on argv, so "dir1 dir2" in a single entry would be read as one path.
	at := -1
	for i, a := range args {
		if a == "--add-dir" {
			at = i
			break
		}
	}
	if at < 0 || at+2 >= len(args) || args[at+1] != "/one" || args[at+2] != "/two" {
		t.Errorf("--add-dir values are not separate argv entries: %#v", args)
	}
}
