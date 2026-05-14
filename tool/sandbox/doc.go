//go:build linux

// Package sandbox owns process execution profiles.
//
// It resolves roots, profile constraints, Linux exec behavior, and the narrow
// sandbox-network helper protocol for tools. It should stay below tool policy
// and runtime orchestration.
package sandbox
