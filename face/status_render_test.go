//go:build linux

package face

import (
	"strings"
	"testing"

	"github.com/idolum-ai/aphelion/core"
)

func TestRenderTelegramStatusChatSummaryStateQueued(t *testing.T) {
	t.Parallel()

	out := RenderTelegramStatusChat(core.ChatStatusSnapshot{
		ChatID:     7,
		QueueDepth: 3,
		PendingItems: []core.PendingItem{
			{Kind: core.PendingItemKindQueue, ChatID: 7, Summary: "queue_depth=3"},
		},
	}, "medium", "high", false)

	if !strings.Contains(out, "summary state=queued") {
		t.Fatalf("RenderTelegramStatusChat() = %q, want queued summary state", out)
	}
	if !strings.Contains(out, "current_signal=queue:3") {
		t.Fatalf("RenderTelegramStatusChat() = %q, want queue current signal", out)
	}
}

func TestRenderTelegramStatusChatSummaryStateInterrupted(t *testing.T) {
	t.Parallel()

	out := RenderTelegramStatusChat(core.ChatStatusSnapshot{
		ChatID: 7,
		LatestTurnRun: &core.TurnRunStatusSnapshot{
			Status: "interrupted",
			Kind:   "interactive",
		},
	}, "medium", "high", false)

	if !strings.Contains(out, "summary state=interrupted") {
		t.Fatalf("RenderTelegramStatusChat() = %q, want interrupted summary state", out)
	}
	if !strings.Contains(out, "current_signal=turn:interactive:interrupted") {
		t.Fatalf("RenderTelegramStatusChat() = %q, want interrupted turn signal", out)
	}
}

func TestRenderTelegramStatusChatSummaryStateBlockedIncludesOperationAndPlan(t *testing.T) {
	t.Parallel()

	out := RenderTelegramStatusChat(core.ChatStatusSnapshot{
		ChatID:           7,
		OperationStatus:  "blocked",
		OperationStage:   "approval_wait",
		OperationSummary: "Waiting for admin review",
		PlanStepStatus:   "in_progress",
		PlanStep:         "Await admin approval",
		LatestTurnRun: &core.TurnRunStatusSnapshot{
			Status: "interrupted",
			Kind:   "interactive",
		},
	}, "medium", "high", false)

	if !strings.Contains(out, "summary state=blocked") {
		t.Fatalf("RenderTelegramStatusChat() = %q, want blocked summary state", out)
	}
	if !strings.Contains(out, "operation status=blocked stage=approval_wait") {
		t.Fatalf("RenderTelegramStatusChat() = %q, want operation status line", out)
	}
	if !strings.Contains(out, "summary=\"Waiting for admin review\"") {
		t.Fatalf("RenderTelegramStatusChat() = %q, want operation summary", out)
	}
	if !strings.Contains(out, "plan_step status=in_progress step=\"Await admin approval\"") {
		t.Fatalf("RenderTelegramStatusChat() = %q, want plan step line", out)
	}
	if !strings.Contains(out, "current_signal=operation:blocked:approval_wait") {
		t.Fatalf("RenderTelegramStatusChat() = %q, want blocked operation current signal", out)
	}
}

func TestRenderTelegramStatusChatIncludesTurnPhaseHiddenInputsDeliveryAndDetachedWork(t *testing.T) {
	t.Parallel()

	out := RenderTelegramStatusChat(core.ChatStatusSnapshot{
		ChatID:             88,
		TurnPhase:          "deliver",
		TurnPhaseSummary:   "sending telegram reply",
		HiddenInputSummary: "pending review events keep converging around approvals",
		HiddenInputCategories: []string{
			"semantic_recurrence",
			"unresolved_memory_state",
		},
		DeliveryStatus:     "delivery_failed",
		DeliverySummary:    "persisted turn failed during delivery; no retry queue is active",
		PlanCompletedSteps: 2,
		PlanTotalSteps:     2,
		PlanFullyExecuted:  true,
		PendingItems: []core.PendingItem{
			{Kind: core.PendingItemKindDecision, ChatID: 88, ID: "decision-1"},
			{Kind: core.PendingItemKindRecovery, ChatID: 88, ID: "recovery-1"},
		},
		StaleRunningTurns: []core.TurnRunStatusSnapshot{
			{ID: 41, ChatID: 88, Status: "running"},
		},
	}, "medium", "high", false)

	for _, needle := range []string{
		"turn_phase phase=deliver",
		"hidden_inputs categories=semantic_recurrence,unresolved_memory_state",
		"delivery status=delivery_failed",
		"plan_progress completed=2 total=2 fully_executed=true",
		"detached_work decisions=1 continuations=0 recoveries=1 stale_turns=1",
		"current_signal=phase:deliver",
	} {
		if !strings.Contains(out, needle) {
			t.Fatalf("RenderTelegramStatusChat() = %q, want substring %q", out, needle)
		}
	}
}

func TestRenderTelegramStatusDurablesIncludesHealthCards(t *testing.T) {
	t.Parallel()

	out := RenderTelegramStatusDurables(core.DurableAgentsStatusSnapshot{
		TotalAgents:    1,
		ActiveAgents:   1,
		DegradedAgents: 1,
		Agents: []core.DurableAgentStatusSnapshot{
			{
				AgentID:                  "family-group",
				ChannelKind:              "telegram_group",
				Status:                   "active",
				Health:                   "degraded",
				ReviewTargetChatID:       1001,
				PolicyVersion:            4,
				PolicyHash:               "8f829f8793fcb1234567890",
				PolicyOutboundMode:       "reply_with_parent_review",
				PolicyDrift:              "admin_review",
				CapabilityEnvelope:       []string{"group_reply", "bounded_review_artifact"},
				LastApplyStatus:          "failed",
				LastApplyError:           "policy apply timed out while child was offline",
				LastAppliedPolicyVersion: 3,
			},
		},
	})

	for _, needle := range []string{
		"status_scope=durables",
		"summary total=1 active=1 dormant=0 degraded=1 inactive=0",
		"- id=family-group channel=telegram_group status=active health=degraded review_chat=1001",
		"policy version=4 hash=8f829f8793fc outbound=reply_with_parent_review",
		"runtime apply_error=\"policy apply timed out while child was offline\"",
		"enrollment status=none",
	} {
		if !strings.Contains(out, needle) {
			t.Fatalf("RenderTelegramStatusDurables() = %q, want substring %q", out, needle)
		}
	}
}

func TestRenderTelegramStatusDurablesShowsEmptyState(t *testing.T) {
	t.Parallel()

	out := RenderTelegramStatusDurables(core.DurableAgentsStatusSnapshot{})
	if !strings.Contains(out, "status_scope=durables") {
		t.Fatalf("RenderTelegramStatusDurables() = %q, want durables scope", out)
	}
	if !strings.Contains(out, "agents:\n- none") {
		t.Fatalf("RenderTelegramStatusDurables() = %q, want empty durable list marker", out)
	}
}
