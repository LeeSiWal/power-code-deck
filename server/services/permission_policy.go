package services

import (
	"encoding/json"
	"path/filepath"
	"strings"
)

// Permission mode ids shared with the client. "auto" is OURS — the CLI has no such
// --permission-mode (the VS Code extension's "Auto (safety check)" is extension-only),
// so we implement it here: every gated tool routes through our approve bridge and this
// policy decides. bypassPermissions is the CLI's, but we ALSO enforce it here because
// registering an approve tool makes the CLI keep asking despite the flag.
const (
	AutoMode   = "auto"
	BypassMode = "bypassPermissions"
)

// autoDecide applies the session's permission mode as a server-side auto-approval
// policy — we own the approve bridge, so we can allow/gate before ever asking a human.
//
//   - bypassPermissions → allow everything ("전체 허용").
//   - auto → allow SAFE tool calls, surface RISKY ones for a human.
//   - anything else → not auto-decided (ask, as before).
//
// The second return reports whether we decided; false means "surface to the user".
func autoDecide(mode, tool string, input json.RawMessage, cwd string) (PermissionDecision, bool) {
	switch mode {
	case BypassMode:
		return PermissionDecision{Behavior: "allow"}, true
	case AutoMode:
		if isSafeToolCall(tool, input, cwd) {
			return PermissionDecision{Behavior: "allow"}, true
		}
	}
	return PermissionDecision{}, false
}

// isSafeToolCall classifies a call as safe-to-auto-approve for "auto" mode. Reads and
// searches are always safe; edits are safe only inside the project directory; shell
// commands are safe only when every chained segment leads with a known-harmless
// command and no dangerous token appears. Anything unrecognized is NOT safe (ask) —
// the policy fails toward asking, never toward running.
func isSafeToolCall(tool string, input json.RawMessage, cwd string) bool {
	switch tool {
	case "Read", "Grep", "Glob", "LS", "NotebookRead", "TodoWrite", "WebSearch", "WebFetch":
		return true
	case "Write", "Edit", "MultiEdit", "NotebookEdit":
		return editWithinCwd(input, cwd)
	case "Bash", "BashOutput":
		return isSafeBash(input)
	}
	return false
}

// editWithinCwd is true when the edit target resolves inside the project directory —
// so "auto" auto-approves edits to the code you're working on, but asks before writing
// anywhere else on the machine.
func editWithinCwd(input json.RawMessage, cwd string) bool {
	p := firstStringField(input, "file_path", "notebook_path", "path")
	if p == "" || cwd == "" {
		return false
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(cwd, p)
	}
	p = filepath.Clean(p)
	c := filepath.Clean(cwd)
	return p == c || strings.HasPrefix(p, c+string(filepath.Separator))
}

// dangerousCmds are command NAMES that force a prompt if they appear as a word
// anywhere in the line (not just at the start — e.g. `find . -exec rm {} \;`). Matched
// per-token, never as a substring, so "dd" no longer flags "git add".
var dangerousCmds = map[string]bool{
	"rm": true, "rmdir": true, "dd": true, "sudo": true, "chmod": true, "chown": true,
	"chgrp": true, "mkfs": true, "fdisk": true, "parted": true, "shutdown": true,
	"reboot": true, "kill": true, "pkill": true, "killall": true, "mount": true,
	"umount": true, "iptables": true, "passwd": true, "chpasswd": true, "usermod": true,
	"chsh": true, "visudo": true, "crontab": true, "systemctl": true, "service": true,
	"launchctl": true, "apt": true, "apt-get": true, "yum": true, "dnf": true,
	"brew": true, "curl": true, "wget": true, "ssh": true, "scp": true, "sftp": true,
	"nc": true, "ncat": true, "telnet": true, "eval": true, "exec": true, "truncate": true,
}

// dangerousPhrases are patterns matched as substrings — multi-word operations and
// redirects the per-command checks can't see (a safe leading command with a dangerous
// tail, a pipe into a shell, a write into a system path).
var dangerousPhrases = []string{
	"git push", "git reset --hard", "git clean", "git rebase", "git checkout --",
	"npm publish", "yarn publish", "pnpm publish", "npm install -g", "npm i -g",
	"pip install", "pip3 install",
	"| sh", "|sh", "| bash", "|bash", "| zsh", "|zsh",
	"> /", ">/", ">>/", "> ~", "> $", ":(){",
	"/etc/", "/dev/", "/usr/", "/bin/", "/sbin/", "~/.ssh",
}

// safeBashCmds are the leading commands we let run unattended in "auto" mode. Tools
// with dangerous subcommands (git, npm) are here; those subcommands are caught by
// dangerousBash above.
var safeBashCmds = map[string]bool{
	"ls": true, "cat": true, "head": true, "tail": true, "pwd": true, "echo": true,
	"printf": true, "grep": true, "rg": true, "ag": true, "find": true, "fd": true,
	"wc": true, "which": true, "type": true, "env": true, "date": true, "whoami": true,
	"hostname": true, "uname": true, "tree": true, "stat": true, "file": true,
	"diff": true, "sort": true, "uniq": true, "cut": true, "tr": true, "jq": true,
	"yq": true, "true": true, "test": true, "basename": true, "dirname": true,
	"realpath": true, "readlink": true, "column": true,
	"git": true, "npm": true, "pnpm": true, "yarn": true, "npx": true, "bun": true,
	"node": true, "deno": true, "python": true, "python3": true,
	"tsc": true, "eslint": true, "prettier": true, "vitest": true, "jest": true,
	"go": true, "gofmt": true, "cargo": true, "rustc": true, "make": true,
}

// isSafeBash allows a shell command only when: no dangerous phrase appears, no
// dangerous command NAME appears as a word anywhere, AND every chained segment
// (split on && || | ; newline) leads with an allow-listed command.
func isSafeBash(input json.RawMessage) bool {
	cmd := strings.TrimSpace(firstStringField(input, "command"))
	if cmd == "" {
		return false
	}
	low := strings.ToLower(cmd)
	for _, p := range dangerousPhrases {
		if strings.Contains(low, p) {
			return false
		}
	}
	for _, w := range bashWords(low) {
		if dangerousCmds[w] {
			return false
		}
	}
	segs := splitBashSegments(cmd)
	if len(segs) == 0 {
		return false
	}
	for _, seg := range segs {
		first := firstCommandWord(seg)
		if first == "" {
			continue
		}
		if !safeBashCmds[first] {
			return false
		}
	}
	return true
}

// bashWords tokenizes a command on whitespace and shell operators, stripping any path
// prefix so "/usr/bin/rm" and "./rm" both surface as the word "rm" for the dangerous-
// command check.
func bashWords(cmd string) []string {
	fields := strings.FieldsFunc(cmd, func(r rune) bool {
		switch r {
		case ' ', '\t', '\n', '|', '&', ';', '(', ')', '<', '>', '`':
			return true
		}
		return false
	})
	out := make([]string, 0, len(fields))
	for _, w := range fields {
		if i := strings.LastIndexByte(w, '/'); i >= 0 {
			w = w[i+1:]
		}
		if w != "" {
			out = append(out, w)
		}
	}
	return out
}

// splitBashSegments breaks a command line on the operators that chain SEPARATE
// commands, so each piece's leading command can be vetted.
func splitBashSegments(cmd string) []string {
	repl := cmd
	for _, op := range []string{"&&", "||", "|", ";", "\n"} {
		repl = strings.ReplaceAll(repl, op, "\x00")
	}
	out := make([]string, 0)
	for _, p := range strings.Split(repl, "\x00") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// firstCommandWord returns the command name at the start of a segment, skipping any
// leading `VAR=value` environment assignments and stripping a path prefix
// (/usr/bin/ls or ./x → ls / x).
func firstCommandWord(seg string) string {
	for _, tok := range strings.Fields(seg) {
		if strings.Contains(tok, "=") && !strings.Contains(tok, "/") {
			continue // env assignment like FOO=bar — skip to the real command
		}
		if i := strings.LastIndexByte(tok, '/'); i >= 0 {
			tok = tok[i+1:]
		}
		return tok
	}
	return ""
}

func firstStringField(input json.RawMessage, keys ...string) string {
	var m map[string]any
	if json.Unmarshal(input, &m) != nil {
		return ""
	}
	for _, k := range keys {
		if v, ok := m[k].(string); ok && v != "" {
			return v
		}
	}
	return ""
}
