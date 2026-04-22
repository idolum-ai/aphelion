//go:build linux

package face

import (
	"strings"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/core"
)

func TestRenderTelegramDebugIncludesChatToolContext(t *testing.T) {
	t.Parallel()

	out := RenderTelegramDebug(core.ChatStatusSnapshot{
		ChatID: 7,
		LatestTurnRun: &core.TurnRunStatusSnapshot{
			ID:                    44,
			Status:                "running",
			Kind:                  "interactive",
			RequestText:           "Check whether a durable agent was created.",
			LastToolName:          "exec",
			LastToolPreview:       `{"command":"curl -fsS https://api.github.com/zen"}`,
			LastToolResultPreview: "stdout: Keep it logically awesome.",
			LastToolError:         "context canceled",
		},
	}, nil, nil, "sonnet", "medium")

	for _, needle := range []string{
		"status_scope=chat",
		"debug_chat:",
		"latest_turn id=44",
		"latest_request=\"Check whether a durable agent was created.\"",
		"last_tool_preview=\"{'command':'curl -fsS https://api.github.com/zen'}\"",
		"last_exec_command=\"curl -fsS https://api.github.com/zen\"",
		"last_tool_result=\"stdout: Keep it logically awesome.\"",
		"last_tool_error=\"context canceled\"",
		"source_attribution_chat:",
		"field=delivery class=projection",
	} {
		if !strings.Contains(out, needle) {
			t.Fatalf("RenderTelegramDebug() = %q, want substring %q", out, needle)
		}
	}
}

func TestRenderTelegramDebugIncludesAdminSystemAndDurablesSections(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	out := RenderTelegramDebug(
		core.ChatStatusSnapshot{ChatID: 7},
		&core.SystemStatusSnapshot{
			GeneratedAt: now,
			PendingItems: []core.PendingItem{
				{Kind: core.PendingItemKindDecision, ChatID: 7},
				{Kind: core.PendingItemKindContinuation, ChatID: 7},
			},
			LatestTurnRunsByChat: map[int64]core.TurnRunStatusSnapshot{
				7: {
					Status:       "running",
					Kind:         "interactive",
					RequestText:  "Run durable-agent wizard checks",
					LastToolName: "exec",
				},
			},
		},
		&core.DurableAgentsStatusSnapshot{
			TotalAgents: 1,
			Agents: []core.DurableAgentStatusSnapshot{
				{AgentID: "family-group", ChannelKind: "telegram_group", Status: "active", Health: "ok"},
			},
		},
		"opus",
		"high",
	)

	for _, needle := range []string{
		"status_scope=chat",
		"status_scope=system",
		"debug_system:",
			"pending_counts queue=0 decision=1 continuation=1 review=0 recovery=0 stale_turn=0",
		"latest_turns:",
		"chat_id=7 status=running kind=interactive",
		"status_scope=durables",
		"debug_durables:",
		"source_attribution_system:",
		"source_attribution_durables:",
		"- id=family-group channel=telegram_group status=active health=ok",
	} {
		if !strings.Contains(out, needle) {
			t.Fatalf("RenderTelegramDebug() = %q, want substring %q", out, needle)
		}
	}
}

func TestRenderTelegramDebugIncludesExecutionTimelineBlocks(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	out := RenderTelegramDebug(
		core.ChatStatusSnapshot{
			ChatID: 9,
			RecentExecution: []core.ExecutionEventSummary{
				{ChatID: 9, EventType: "turn.completed", Stage: "turn", Status: "completed", CreatedAt: now},
			},
		},
		&core.SystemStatusSnapshot{
			RecentExecution: []core.ExecutionEventSummary{
				{ChatID: 9, EventType: "decision.opened", Stage: "decision", Status: "pending", CreatedAt: now.Add(-time.Second)},
			},
		},
		nil,
		"opus",
		"high",
	)

	for _, needle := range []string{
		"execution_timeline:",
		"type=turn.completed",
		"type=decision.opened",
	} {
		if !strings.Contains(out, needle) {
			t.Fatalf("RenderTelegramDebug() = %q, want substring %q", out, needle)
		}
	}
}

func TestRenderTelegramDebugMarksDeliveryTimelineAsTransportLedger(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	out := RenderTelegramDebug(
		core.ChatStatusSnapshot{
			ChatID: 9,
			RecentExecution: []core.ExecutionEventSummary{
				{ChatID: 9, EventType: "delivery.final.sent", Stage: "delivery", Status: "sent", CreatedAt: now},
			},
		},
		nil,
		nil,
		"opus",
		"high",
	)

	for _, needle := range []string{
		"type=delivery.final.sent",
		"source_surface=outbound_transport_ledger",
		"visibility=human_render_unknown",
	} {
		if !strings.Contains(out, needle) {
			t.Fatalf("RenderTelegramDebug() = %q, want substring %q", out, needle)
		}
	}
}
