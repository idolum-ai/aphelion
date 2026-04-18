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
