//go:build linux

package main

import (
	"strings"
	"testing"
)

func TestHumanizeTelegramTelemetryText(t *testing.T) {
	t.Parallel()

	input := strings.Join([]string{
		"quick_read Chat 7 is idle right now.",
		"status_scope=chat chat_id=7 generated_at=2026-04-19T22:02:14Z",
		"summary state=idle active_turns=0 queue_depth=0 pending_items=0",
		"debug_chat:",
		"latest_turn id=744 status=completed kind=interactive last_activity=2026-04-19T22:01:16Z",
		"pending_items:",
		"- none",
	}, "\n")

	got := humanizeTelegramTelemetryText(input)
	for _, needle := range []string{
		"Quick Read: Chat 7 is idle right now.",
		"Status Scope: chat Chat ID: 7 Generated At: 2026-04-19T22:02:14Z",
		"summary state=idle Active Turns: 0 Queue Depth: 0 Pending Items: 0",
		"Debug Chat:",
		"Latest Turn: id=744 status=completed kind=interactive Last Activity: 2026-04-19T22:01:16Z",
		"Pending Items:",
	} {
		if !strings.Contains(got, needle) {
			t.Fatalf("humanizeTelegramTelemetryText() = %q, want substring %q", got, needle)
		}
	}
}
