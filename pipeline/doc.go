//go:build linux

// Package pipeline defines the intended boundary for Aphelion's conversational
// mechanics: the governor/face pipeline that a turn orchestrates but should not
// fully internalize.
//
// This package is a design-lab surface. It is not yet wired into the live
// runtime loop.
//
// In the intended architecture:
//
//   - runtime owns the long-lived process shell and channel wiring
//   - turn owns the state machine for one turn
//   - pipeline owns the governor/face mechanics that turn invokes
//
// The key architectural nouns here are:
//
//   - brokerage: bounded pre-turn negotiation over posture
//   - floor: governor-owned material truth/permission/refusal artifact
//   - scene: face-authored visible reply from that floor
//   - fallback: degraded delivery path when ordinary scene authorship is absent
//
// The purpose of this package is to make those mechanics explicit, testable,
// and eventually extractable from runtime without collapsing them back into a
// monolithic turn handler.
package pipeline
