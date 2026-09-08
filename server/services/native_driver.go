package services

import "powercodedeck/internal/providers/native"

// NativeDriver preserves the CLI wire contract during provider extraction.
// New execution consumers use internal/providers.Execution via native.Adapter.
type NativeDriver = native.Driver
