//go:build linux

// Package config owns Aphelion's operator configuration schema.
//
// It provides defaults, TOML loading, warning projection, normalization, and
// validation for the single-binary runtime. Config should describe live knobs
// only; speculative behavior belongs in requirements, not in ignored schema.
package config
