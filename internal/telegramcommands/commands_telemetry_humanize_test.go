//go:build linux

package telegramcommands

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
		"[PLAN_UPDATED]",
		"active: true",
		"[/PLAN_UPDATED]",
		"debug_chat:",
		"latest_turn id=744 status=completed kind=interactive last_activity=2026-04-19T22:01:16Z",
		"[DURABLE_AGENTS]",
		"count: 1",
		"[/DURABLE_AGENTS]",
		"pending_items:",
		"- none",
	}, "\n")

	got := humanizeTelegramTelemetryText(input)
	for _, needle := range []string{
		"Quick Read: Chat 7 is idle right now.",
		"Status Scope: chat Chat ID: 7 Generated At: 2026-04-19T22:02:14Z",
		"Summary: State: idle Active Turns: 0 Queue Depth: 0 Pending Items: 0",
		"Plan Updated:",
		"Active: true",
		"Trace Chat:",
		"Latest Turn: ID: 744 Status: completed Kind: interactive Last Activity: 2026-04-19T22:01:16Z",
		"Durable Agents:",
		"Count: 1",
		"Pending Items:",
		"- none",
	} {
		if !strings.Contains(got, needle) {
			t.Fatalf("humanizeTelegramTelemetryText() = %q, want substring %q", got, needle)
		}
	}
	for _, forbidden := range []string{"[PLAN_UPDATED]", "[/PLAN_UPDATED]", "[DURABLE_AGENTS]", "[/DURABLE_AGENTS]"} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("humanizeTelegramTelemetryText() = %q, should not contain %q", got, forbidden)
		}
	}
}
