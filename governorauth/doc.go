//go:build linux

// Package governorauth owns authentication material for governor backends.
//
// It resolves, loads, validates, and saves local auth sources such as Aphelion
// or Codex CLI token files. It returns a typed auth bundle; it does not speak
// the backend protocol, choose model policy, run turns, execute tools, own
// session state, or render UI.
package governorauth
