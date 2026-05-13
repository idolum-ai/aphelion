//go:build linux

// Package sandbox owns process execution profiles.
//
// It resolves roots, profile constraints, and Linux exec behavior for tools.
// It should stay below tool policy and runtime orchestration.
package sandbox
