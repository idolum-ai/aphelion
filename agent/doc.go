//go:build linux

// Package agent defines provider-facing turn primitives.
//
// It owns message, tool-call, streaming, and budget contracts shared by provider
// adapters and orchestration. It should stay transport-neutral and persistence
// neutral.
package agent
