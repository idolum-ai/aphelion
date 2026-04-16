//go:build linux

// Package runtime owns Aphelion's long-lived process shell.
//
// Runtime responsibilities are intentionally shell-level:
//
//   - transport ingress and outbound adapters
//   - principal/scope resolution and session locking
//   - background loops (heartbeat, cron, startup recovery, idle expiry)
//   - durable-agent lifecycle wiring
//   - construction of concrete ports passed into turn orchestration
//
// Runtime should not be the primary owner of one-turn stage order.
// Turn sequencing is delegated to turn.Machine and pipeline contracts.
package runtime
