//go:build linux

// Package tool owns Aphelion's bounded tool implementations.
//
// The package translates typed tool requests into constrained local behavior:
// exec, file access, memory, capability authority, external tool lifecycle, and
// durable-agent operator tools. Runtime assembles the registry, but tool owns
// validation, sandbox-aware execution, and tool-facing render output.
//
// This package should not import runtime, turn, or pipeline orchestration.
package tool
