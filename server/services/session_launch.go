package services

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	ptyruntime "powercodedeck/internal/runtime/pty"
)

// prepareSessionLaunch preserves the legacy CLI discovery, installation, login,
// PATH, locale and Windows policies outside the reusable PTY runtime.
func prepareSessionLaunch(req CreateSessionRequest) (ptyruntime.LaunchSpec, error) {
	command, args := resolveLaunchCommand(req.Command, req.Args)
	locale := utf8Locale()
	return ptyruntime.LaunchSpec{
		Command: command,
		Args:    args,
		Env: withAgentPath(append(os.Environ(),
			"TERM=xterm-256color", "LANG="+locale, "LC_ALL="+locale)),
	}, nil
}

// npmCLIPackages maps a launcher command to the npm package that provides it, so
// a missing agent CLI can be auto-installed on first launch.
var npmCLIPackages = map[string]string{
	"claude": "@anthropic-ai/claude-code",
	"codex":  "@openai/codex",
}

// loginCommands maps an agent CLI to the shell snippet that ensures it's signed
// in, chained right after a fresh install so the user is taken straight through
// auth. An entry is only needed for CLIs that expose an explicit, separate login
// command: codex has `codex login` (and `codex login status` to skip it when
// already authenticated). Claude has none — its own first run takes you through
// sign-in — so simply exec-ing it (below) already flows into auth.
var loginCommands = map[string]string{
	"codex": "{ codex login status >/dev/null 2>&1 || codex login; }",
}

// npmGlobalBin resolves the directory npm installs global CLIs into (e.g.
// ~/.npm-global/bin), computed once via `npm prefix -g`. The server's own PATH
// frequently omits this dir because the user's `export PATH=...:$HOME/.npm-global/bin`
// lives in ~/.bashrc, which a non-interactive server (and `bash -l`, a login
// shell) never sources. Looking here explicitly lets us (a) detect an
// already-installed agent CLI instead of reinstalling it on every launch, and
// (b) run the binary we just installed.
var (
	npmGlobalBinOnce sync.Once
	npmGlobalBinDir  string
)

func npmGlobalBin() string {
	npmGlobalBinOnce.Do(func() {
		npm, err := exec.LookPath("npm")
		if err != nil {
			return
		}
		out, err := exec.Command(npm, "prefix", "-g").Output()
		if err != nil {
			return
		}
		prefix := strings.TrimSpace(string(out))
		if prefix == "" {
			return
		}
		if runtime.GOOS == "windows" {
			npmGlobalBinDir = prefix // .cmd shims live in the prefix root on Windows
		} else {
			npmGlobalBinDir = filepath.Join(prefix, "bin")
		}
	})
	return npmGlobalBinDir
}

// localeCandidates are the UTF-8 locales we'll hand a session, best first. A
// Korean locale is preferred (app messages in 한국어), but ANY UTF-8 locale beats
// a nonexistent one: glibc falls back to the C/POSIX locale when LC_ALL names a
// locale that isn't generated, and C/POSIX is ASCII (charmap ANSI_X3.4-1968) —
// so wcwidth-based apps (vim, less, htop) then measure 한글 as the wrong width,
// which is exactly the CJK breakage our renderer works so hard to avoid.
var localeCandidates = []string{"ko_KR.UTF-8", "ko_KR.utf8", "C.UTF-8", "C.utf8", "en_US.UTF-8", "en_US.utf8"}

var (
	utf8LocaleOnce sync.Once
	utf8LocaleName string
)

// utf8Locale returns a UTF-8 locale that actually EXISTS on this host, probed
// once via `locale -a`. We used to hard-code ko_KR.UTF-8, but a host without that
// locale generated (Ubuntu ships only C.utf8/en_US.utf8 by default) gave every
// session an ASCII charmap plus a `warning: setlocale` banner on each shell start.
// Windows has no `locale` binary and ignores these vars, so it keeps the old value.
func utf8Locale() string {
	utf8LocaleOnce.Do(func() {
		utf8LocaleName = localeCandidates[0]
		if runtime.GOOS == "windows" {
			return
		}
		out, err := exec.Command("locale", "-a").Output()
		if err != nil {
			return
		}
		have := make(map[string]struct{})
		for _, l := range strings.Split(string(out), "\n") {
			have[strings.ToLower(strings.TrimSpace(l))] = struct{}{}
		}
		for _, cand := range localeCandidates {
			if _, ok := have[strings.ToLower(cand)]; ok {
				utf8LocaleName = cand
				return
			}
		}
		// Nothing matched: C.UTF-8 is the most likely to exist on a modern glibc
		// and is at least honest about being UTF-8.
		utf8LocaleName = "C.UTF-8"
	})
	return utf8LocaleName
}

// localBinDir is $HOME/.local/bin — the conventional home for user-installed
// binaries. Like the npm global bin dir, it's usually only added to PATH from
// ~/.bashrc, which the server (and `bash -l`) never sources, so a CLI installed
// there would otherwise be invisible to a session.
func localBinDir() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".local", "bin")
}

// agentBinDirs are the directories that hold agent CLIs but are typically missing
// from the server's own PATH: the npm global bin dir and ~/.local/bin.
func agentBinDirs() []string {
	dirs := make([]string, 0, 2)
	if d := npmGlobalBin(); d != "" {
		dirs = append(dirs, d)
	}
	if d := localBinDir(); d != "" {
		dirs = append(dirs, d)
	}
	return dirs
}

// withAgentPath prepends the agent bin dirs to PATH in a copy of env, so the
// spawned CLI (and anything it launches) resolves installed agents even when the
// server was started without those dirs on PATH.
func withAgentPath(env []string) []string {
	dirs := agentBinDirs()
	if len(dirs) == 0 {
		return env
	}
	prefix := strings.Join(dirs, string(os.PathListSeparator))
	out := make([]string, 0, len(env)+1)
	replaced := false
	for _, kv := range env {
		if strings.HasPrefix(kv, "PATH=") && !replaced {
			out = append(out, "PATH="+prefix+string(os.PathListSeparator)+strings.TrimPrefix(kv, "PATH="))
			replaced = true
			continue
		}
		out = append(out, kv)
	}
	if !replaced {
		out = append(out, "PATH="+prefix)
	}
	return out
}

// findAgentCommand returns an absolute path to command if it's installed — either
// on PATH or in one of the agent bin dirs — or "" if it genuinely isn't.
//
// When several installs exist it picks the NEWEST, not the first on PATH. As a systemd
// service the deck has a bare PATH (/usr/bin and friends), so a root-owned system-wide
// install wins every time — even when the user has been updating their own copy under
// ~/.npm-global for months. pickNewest keeps the deck on whichever CLI the user
// actually maintains, and falls back to this function's original order whenever the
// versions can't be compared.
func findAgentCommand(command string) string {
	var candidates []string
	if p, err := exec.LookPath(command); err == nil {
		candidates = append(candidates, p)
	}
	for _, dir := range agentBinDirs() {
		cand := filepath.Join(dir, command)
		if runtime.GOOS == "windows" {
			cand += ".cmd"
		}
		if fi, err := os.Stat(cand); err == nil && !fi.IsDir() {
			candidates = append(candidates, cand)
		}
	}
	return pickNewest(candidates)
}

// resolveLaunchCommand decides what to actually spawn for a requested command:
//   - if it's already installed (on PATH or npm global bin), run it (with the
//     Windows .cmd shim + absolute path);
//   - if it's a known agent CLI that isn't installed, install it via npm, chain
//     its login flow, then exec it — all inside the session so the user sees the
//     progress and is carried straight through sign-in;
//   - otherwise run it as-is (it will fail with "not found", as before).
func resolveLaunchCommand(command string, args []string) (string, []string) {
	if resolved := findAgentCommand(command); resolved != "" {
		return normalizeCommand(resolved, args)
	}
	if pkg, ok := npmCLIPackages[command]; ok {
		return bootstrapInstallCommand(command, args, pkg)
	}
	return normalizeCommand(command, args)
}

// normalizeCommand applies the Windows .cmd shim and resolves the executable
// against PATH (go-pty looks bare names up relative to the working dir, not PATH).
func normalizeCommand(command string, args []string) (string, []string) {
	command, args = windowsShim(command, args)
	if resolved, err := exec.LookPath(command); err == nil {
		command = resolved
	}
	return command, args
}

// bootstrapInstallCommand builds a command that installs the CLI (npm -g), makes
// the freshly-installed binary reachable, chains its login flow, and then runs
// it — so the npm output streams into the terminal and the user is carried from
// install → sign-in → running CLI in one go. Requires Node/npm to be available.
//
// The PATH step is essential: `npm install -g` drops the binary into
// `$(npm prefix -g)/bin` (e.g. ~/.npm-global/bin), and that dir is usually only
// added to PATH from ~/.bashrc — which the `bash -l` login shell here does NOT
// source — so a bare `exec claude` right after install would fail "command not
// found". Prepending the global bin dir guarantees the just-installed CLI runs.
func bootstrapInstallCommand(command string, args []string, pkg string) (string, []string) {
	run := strings.TrimSpace(command + " " + strings.Join(args, " "))
	if runtime.GOOS == "windows" {
		login := ""
		if snip, ok := loginCommands[command]; ok {
			login = " && " + strings.NewReplacer(">/dev/null", ">nul", "{ ", "(", "; }", ")").Replace(snip)
		}
		line := "echo Installing " + command + " (first run)... && npm install -g " + pkg +
			" && for /f \"delims=\" %g in ('npm prefix -g') do set \"PATH=%g;%PATH%\"" +
			login + " && " + run
		shell := "cmd"
		if resolved, err := exec.LookPath("cmd"); err == nil {
			shell = resolved
		}
		return shell, []string{"/c", line}
	}
	login := ""
	if snip, ok := loginCommands[command]; ok {
		login = " && " + snip
	}
	line := "echo 'Installing " + command + " (first run)...'; npm install -g " + pkg +
		" && export PATH=\"$(npm prefix -g)/bin:$PATH\"" +
		login + " && exec " + run
	shell := "bash"
	if resolved, err := exec.LookPath("bash"); err == nil {
		shell = resolved
	}
	return shell, []string{"-lc", line}
}

// windowsShim adapts a command for Windows ConPTY. Windows CreateProcess (used
// by go-pty's ConPTY) cannot launch batch/script shims like `.cmd`/`.bat`/`.ps1`
// directly — and npm-installed CLIs (claude, codex) are `.cmd` shims. So
// on Windows, unless the command is already an `.exe`, run it through `cmd.exe /c`
// (which resolves the shim via PATHEXT). No-op on macOS/Linux.
func windowsShim(command string, args []string) (string, []string) {
	if runtime.GOOS != "windows" {
		return command, args
	}
	if strings.HasSuffix(strings.ToLower(command), ".exe") {
		return command, args
	}
	shimArgs := append([]string{"/c", command}, args...)
	return "cmd", shimArgs
}
