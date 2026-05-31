//go:build linux

// Package governorbackend contains provider-shaped adapters for governor-only
// backends.
//
// It speaks backend protocols such as Codex/ChatGPT-style streaming,
// continuation, auth refresh transport, and error normalization so runtime can
// wire the governor through a normal agent.Provider shape. It does not discover
// auth sources, decide policy, execute tools, own sessions, or render Telegram
// UI.
package governorbackend
