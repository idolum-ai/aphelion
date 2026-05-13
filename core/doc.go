//go:build linux

// Package core contains shared contracts that cross package boundaries.
//
// Core records are transport-neutral and persistence-neutral: inbound/outbound
// messages, status snapshots, durable-agent contracts, artifacts, stream
// controls, and review callbacks. Keep business behavior in the owning package;
// core should stay focused on stable data shapes and small normalization rules.
package core
