//go:build linux

// Package prompt owns compiled prompt contracts.
//
// It renders stable system, cache, awareness, and tool-discipline instructions
// from typed inputs. Runtime may choose inputs, but prompt should not inspect
// stores or transports directly.
package prompt
