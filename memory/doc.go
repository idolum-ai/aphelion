//go:build linux

// Package memory owns local semantic and curated memory services.
//
// It handles indexing, promotion, context selection, imported corpus data, and
// memory instrumentation. It should not decide transport behavior or operator
// authority.
package memory
