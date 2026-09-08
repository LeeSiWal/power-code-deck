// Package pty owns in-process terminal sessions and their process lifecycle.
// It depends only on the session contract and go-pty, never on the legacy web
// application, agent database, control room or model CLI installation policy.
// Detaching a viewer never terminates a session; server exit still does.
package pty
