//go:build linux

// Package turn defines the intended boundary for turn orchestration.
//
// This package is a design-lab surface for the conversational state machine
// that currently lives mostly inside runtime.HandleInbound and related helpers.
// It is intentionally not wired into production yet.
//
// The architectural split we are testing is:
//
//   - runtime: process shell, channel integration, startup/recovery loops,
//     admission wiring, and long-lived house concerns
//   - turn: one turn's orchestration lifecycle
//   - pipeline: governor/face conversational mechanics such as brokerage,
//     floor extraction, scene authorship, and fallback rendering
//
// In that shape, turn owns the "how one turn proceeds" question:
//
//  1. accept an inbound event and scoped session snapshot
//  2. assemble the inputs needed for a single turn
//  3. invoke governor and face pipeline stages in the right order
//  4. produce a turn result, visible reply, and sidecar floor artifacts
//  5. hand persistence and outbound delivery to explicit ports
//
// This package should stay narrower than runtime and higher-level than the
// future pipeline package. It is the state machine spine, not the process shell
// and not the prompt/rendering mechanics themselves.
package turn
