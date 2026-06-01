//go:build linux

// Package session owns Aphelion's durable record/store shell.
//
// Records in this package are typed contracts for sessions, continuation,
// operations, capability authority, tool lifecycle, durable-agent state, mission
// ledger state, Telegram-derived persistence records, tailnet records, and
// execution evidence. Runtime may project or act on these records, but session
// owns their storage shape, normalization, codecs, schema, migrations, indexes,
// and atomic persistence transitions.
//
// Session owns durable facts, not live decisions. It should not own runtime
// orchestration, turn sequencing, user-visible rendering, external tool/account
// behavior, transport command semantics, policy judgment, or child wake behavior.
//
// This package should not import orchestration or transport packages such as
// runtime, turn, pipeline, or telegram. See README.md for the full boundary
// contract.
package session
