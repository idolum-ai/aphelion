//go:build linux

// Package tool owns Aphelion's governed tool runtime.
//
// The package translates typed tool requests into constrained local behavior:
// native tool implementations, capability authority, external tool manifest
// schema, install/audit/probe lifecycle, drift checks, grant-gated invocation,
// sandbox-aware execution, and tool-facing render output.
//
// It owns the machinery for manifest-backed external tools, but not every
// external tool body. A bundled manifest or script is not installed, verified,
// registered, granted, callable, or safe merely because it exists in the repo.
//
// This package should not import runtime, turn, or pipeline orchestration.
package tool
