//go:build linux

package telegramcommands

import (
	"context"
	"strings"
	"testing"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/face"
	"github.com/idolum-ai/aphelion/telegram"
)

func TestHandleTelegramCommandCallbackStatusFindChatShowsChatButtons(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := stubCommandRouter{
		canRestart: true,
		statusSystem: core.SystemStatusSnapshot{
			HotChats: []core.ChatStatusRollup{
				{ChatID: 9001, PendingCount: 3},
				{ChatID: 9002, PendingCount: 1},
			},
		},
	}
	handled, err := handleTelegramCommandCallback(context.Background(), sender, &router, telegram.CallbackQuery{
		ID:   "cb-status-find",
		From: &telegram.User{ID: 1001, Username: "admin"},
		Data: "status:find",
		Message: &telegram.Message{
			MessageID: 98,
			Chat:      &telegram.Chat{ID: 7, Type: "private"},
		},
	})
	if err != nil {
		t.Fatalf("handleTelegramCommandCallback() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if len(sender.editInline) != 1 {
		t.Fatalf("editInline count = %d, want 1", len(sender.editInline))
	}
	foundChatButton := false
	for _, row := range sender.editInline[0].rows {
		for _, button := range row {
			if strings.Contains(button.CallbackData, "status:chat:9001") {
				foundChatButton = true
			}
		}
	}
	if !foundChatButton {
		t.Fatalf("rows = %#v, want chat drill-down callback", sender.editInline[0].rows)
	}
}

func TestHandleTelegramCommandCallbackStatusBoundsOverflowWithoutFollowupChunks(t *testing.T) {
	t.Parallel()

	pending := make([]core.PendingItem, 0, 120)
	for i := 0; i < 120; i++ {
		pending = append(pending, core.PendingItem{
			Kind:    core.PendingItemKindDecision,
			ChatID:  int64(7000 + i%3),
			ID:      "decision-overflow-" + strings.Repeat("x", 20),
			Summary: strings.Repeat("very long pending summary ", 4),
		})
	}

	sender := &stubCommandSender{}
	router := stubCommandRouter{
		canRestart: true,
		statusReadableSummary: "System status overflow probe. " +
			strings.Repeat("This deliberately long quick read verifies deterministic Telegram chunking. ", 80),
		statusSystem: core.SystemStatusSnapshot{
			PendingItems: pending,
		},
	}
	handled, err := handleTelegramCommandCallback(context.Background(), sender, &router, telegram.CallbackQuery{
		ID:   "cb-status-overflow",
		From: &telegram.User{ID: 1001, Username: "admin"},
		Data: "status:system",
		Message: &telegram.Message{
			MessageID: 99,
			Chat:      &telegram.Chat{ID: 7, Type: "private"},
		},
	})
	if err != nil {
		t.Fatalf("handleTelegramCommandCallback() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if len(sender.editInline) != 1 {
		t.Fatalf("editInline count = %d, want 1", len(sender.editInline))
	}
	if len([]rune(sender.editInline[0].text)) > 3800 {
		t.Fatalf("edited text rune length = %d, want <= 3800", len([]rune(sender.editInline[0].text)))
	}
	if len(sender.msgs) != 0 {
		t.Fatalf("follow-up messages = %#v, want compact status without overflow chunks", sender.msgs)
	}
	if strings.Contains(sender.editInline[0].text, "status_scope=") || strings.Contains(sender.editInline[0].text, "summary ") {
		t.Fatalf("status text = %q, want operator panel instead of raw status telemetry", sender.editInline[0].text)
	}
}

func TestHandleTelegramCommandCallbackStatusUsesTypedFactsForQuickReadGrounding(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := stubCommandRouter{
		canRestart:            true,
		statusReadableSummary: "System is idle with no pending items.",
		statusSystem: core.SystemStatusSnapshot{
			PendingItems: []core.PendingItem{{
				Kind:    core.PendingItemKindDecision,
				ChatID:  7,
				ID:      "decision-system-status",
				Summary: "Needs review.",
			}},
		},
	}
	handled, err := handleTelegramCommandCallback(context.Background(), sender, &router, telegram.CallbackQuery{
		ID:   "cb-status-system-ground",
		From: &telegram.User{ID: 1001, Username: "admin"},
		Data: "status:system",
		Message: &telegram.Message{
			MessageID: 99,
			Chat:      &telegram.Chat{ID: 7, Type: "private"},
		},
	})
	if err != nil {
		t.Fatalf("handleTelegramCommandCallback() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if len(sender.editInline) != 1 {
		t.Fatalf("editInline count = %d, want 1", len(sender.editInline))
	}
	text := sender.editInline[0].text
	if strings.Contains(strings.ToLower(text), "system is idle with no pending") {
		t.Fatalf("status text = %q, should reject contradictory Quick Read", text)
	}
	if !strings.Contains(text, "Quick Read: System has 0 active turn(s), 0 queued chat(s), and 1 pending item(s).") {
		t.Fatalf("status text = %q, want deterministic typed-fact fallback", text)
	}
}

func TestOperatorPanelsAvoidRawTelemetryByDefault(t *testing.T) {
	t.Parallel()

	memoryText, _ := renderMemoryReviewPanel(memoryReviewSnapshot{
		Source: memoryReviewSourceShared,
		Query:  "release status",
		Items: []memoryReviewItem{{
			ID:      "mem-1",
			Label:   "Release note",
			Excerpt: "The latest release check passed.",
		}},
	})
	tailnetText, _ := renderTailnetCommand(core.TailnetStatusSnapshot{
		Enabled: true,
		Backend: "cli",
		Status:  "healthy",
		Parent:  &core.TailnetParentStatus{Enabled: true, Running: true},
	})
	agentsText, _ := renderDurableAgentsCommand([]core.DurableAgentStatusSnapshot{{
		AgentID:     "ops-child",
		ChannelKind: "telegram_dm",
		Status:      "active",
		Health:      "ok",
	}})
	systemText := face.RenderTelegramStatusSystemOperatorCard(core.SystemStatusSnapshot{
		PendingItems: []core.PendingItem{{Kind: core.PendingItemKindDecision, ChatID: 7}},
	}, "sonnet", "medium")
	durablesText := face.RenderTelegramStatusDurablesOperatorCard(core.DurableAgentsStatusSnapshot{
		TotalAgents:    1,
		DegradedAgents: 1,
		Agents: []core.DurableAgentStatusSnapshot{{
			AgentID:          "ops-child",
			Status:           "active",
			Health:           "degraded",
			EnrollmentStatus: "active",
		}},
	})
	for name, text := range map[string]string{"memory": memoryText, "tailnet": tailnetText, "agents": agentsText, "status-system": systemText, "status-durables": durablesText} {
		for _, forbidden := range []string{"source=", "enabled=true", "running=true", "kind=", "owner="} {
			if strings.Contains(text, forbidden) {
				t.Fatalf("%s panel = %q, should not contain raw telemetry %q", name, text, forbidden)
			}
		}
		for _, forbidden := range []string{"status_scope=", "summary "} {
			if strings.Contains(text, forbidden) {
				t.Fatalf("%s panel = %q, should not contain raw status telemetry %q", name, text, forbidden)
			}
		}
		for _, want := range []string{"Status:", "Why:", "Next:"} {
			if !strings.Contains(text, want) {
				t.Fatalf("%s panel = %q, want operator contract field %q", name, text, want)
			}
		}
	}
}
