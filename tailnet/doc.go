//go:build linux

// Package tailnet owns Tailscale status and control-plane adapters.
//
// It exposes typed tailnet observations and server wiring. Authority binding and
// operator repair decisions belong in higher packages.
package tailnet
