//go:build linux

// Package governorbackend contains provider adapters used as governor backends.
//
// Adapters in this package translate agent messages, tool definitions, stream
// events, and provider errors. They should not own runtime policy or prompt
// construction beyond provider-specific request shaping.
package governorbackend
