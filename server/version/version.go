// Package version holds the single source of truth for product identity.
// User-facing name / version / tagline are referenced from the banner, CLI,
// and the health API so they never drift apart.
package version

const (
	// Version is the current release. v0.1.0 = original AgentDeck MVP,
	// v0.2.0 = PowerCodeDeck renewal, v0.2.1 = Session Handoff,
	// v0.2.2 = tmux-free internal session engine,
	// v0.2.3 = cgo-free native builds (go-pty/ConPTY + modernc SQLite),
	// v0.2.4 = security hardening (WS origin/token, file path validation,
	//          host check, token type separation, graceful shutdown),
	// v0.2.5 = fix: Host guard honors CORS_ORIGINS (reverse-proxy access),
	// v0.2.6 = native Codex app-server chat driver,
	// v0.3.0 = Control Room (planned).
	Version = "0.2.6"

	// AppName is the product name shown to users.
	AppName = "PowerCodeDeck"

	// Tagline is the one-line product description.
	Tagline = "Native web workspace for Claude Code and Codex"

	// Binary is the recommended executable / service name.
	Binary = "pcd"
)
