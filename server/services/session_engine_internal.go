package services

import ptyruntime "powercodedeck/internal/runtime/pty"

// InternalPtySessionEngine is the legacy name for the shared runtime engine.
type InternalPtySessionEngine = ptyruntime.Engine

// NewInternalPtySessionEngine retains all existing launch and terminal defaults.
// New applications can construct ptyruntime.Engine with their own launch policy.
func NewInternalPtySessionEngine(scrollbackBytes int) *InternalPtySessionEngine {
	return ptyruntime.NewEngine(scrollbackBytes, prepareSessionLaunch)
}

var _ SessionEngine = (*InternalPtySessionEngine)(nil)
