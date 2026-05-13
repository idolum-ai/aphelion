//go:build linux

// Package durableagent owns the child-agent substrate.
//
// It provides control-plane HTTP, remote loop, enrollment, signatures, and
// child runtime plumbing. Higher packages decide policy; this package moves
// authenticated child-agent data safely.
package durableagent
