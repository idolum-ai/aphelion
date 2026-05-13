//go:build linux

// Package session owns durable storage records and SQLite persistence.
//
// Records in this package are typed contracts for sessions, continuation,
// operations, capability authority, tool lifecycle, durable-agent state, mission
// ledger state, and execution evidence. Runtime may project these records for
// operators, but session owns their storage shape and normalization.
//
// This package should not import orchestration packages such as runtime, turn,
// or pipeline.
package session
