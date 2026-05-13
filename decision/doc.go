//go:build linux

// Package decision owns bounded operator decision lifecycles.
//
// It tracks pending choices, durable reconciliation, timeout/default handling,
// and callback encoding. It should not know Telegram rendering details or grant
// authority by itself.
package decision
