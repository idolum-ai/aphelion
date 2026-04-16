//go:build linux

// Package pipeline owns governor/face conversational mechanics used by turn
// orchestration.
//
// The package provides production contracts and helpers for:
//
//   - brokerage parsing and ratification shaping
//   - floor material extraction and formatting
//   - render/fallback contracts and render-decision policy
//   - execution and awareness contracts shared with turn/runtime boundaries
//
// In the live architecture:
//
//   - runtime owns process shell + transport wiring
//   - turn owns one-turn stage ordering
//   - pipeline owns conversational transformations across governor/face
package pipeline
