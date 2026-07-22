// Package version holds the single source of truth for product identity.
// User-facing name / version / tagline are referenced from the banner, CLI,
// and the health API so they never drift apart.
package version

import (
	_ "embed"
	"strings"
)

// versionFile is the ONE place the release number lives: the VERSION file in this
// package. The client build (client/vite.config.ts) reads the very same file, so the
// server banner/CLI/health API and the web UI can never disagree. To cut a release,
// edit that one line — nothing else.
//
// Release history:
//
//	v0.1.0 = original AgentDeck MVP
//	v0.2.0 = PowerCodeDeck renewal
//	v0.2.1 = Session Handoff
//	v0.2.2 = tmux-free internal session engine
//	v0.2.3 = cgo-free native builds (go-pty/ConPTY + modernc SQLite)
//	v0.2.4 = security hardening (WS origin/token, file path validation,
//	         host check, token type separation, graceful shutdown)
//	v0.2.5 = fix: Host guard honors CORS_ORIGINS (reverse-proxy access)
//	v0.2.6 = native Codex app-server chat driver
//	v0.3.0 = Control Room (multi-session overview)
//
//go:embed VERSION
var versionFile string

// Version is the current release, trimmed from the embedded VERSION file.
var Version = strings.TrimSpace(versionFile)

const (
	// AppName is the product name shown to users.
	AppName = "PowerCodeDeck"

	// Tagline is the one-line product description.
	Tagline = "Native web workspace for Claude Code and Codex"

	// Binary is the recommended executable / service name.
	Binary = "pcd"
)
