//go:build linux

// Package turn owns one-turn orchestration in production.
//
// turn.Machine is the state-machine spine used across major turn species
// (interactive, durable child, heartbeat, cron, recovery), with species policy
// differences expressed explicitly through turn policy and ports.
//
// Responsibilities include:
//
//   - turn policy and stage order
//   - governor and face orchestration through explicit ports
//   - commit ordering contracts (persist/deliver boundaries)
//   - turn result materialization for runtime transport and session wiring
//
// This package does not own process-shell concerns such as channel polling,
// session lock lifecycles, or background scheduler loops.
package turn
