//go:build linux

// Package principal owns actor identity and role resolution.
//
// It maps inbound facts into typed principals and keeps role vocabulary separate
// from transport, storage, and capability enforcement.
package principal
