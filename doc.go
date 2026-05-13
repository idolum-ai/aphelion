//go:build linux

// Package main owns Aphelion's single-binary composition.
//
// The root package is intentionally the wiring layer for:
//
//   - CLI command dispatch and command output shaping
//   - install, deploy, quickstart, and maintenance entrypoints
//   - concrete runtime/provider/session/tool assembly
//   - Telegram operator UI glue and callback routing
//
// Durable behavior belongs in lower packages. Root should compose those
// packages, not become the owner of runtime loops, turn sequencing, persistence
// contracts, provider wire behavior, or tool implementation details.
package main
