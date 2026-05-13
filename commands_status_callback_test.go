//go:build linux

package main

import (
	"context"
	"strings"
	"testing"

	"github.com/idolum-ai/aphelion/core"
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

func TestHandleTelegramCommandCallbackStatusChunksOverflowDeterministically(t *testing.T) {
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
	if len(sender.msgs) == 0 {
		t.Fatalf("follow-up messages = %#v, want overflow chunks", sender.msgs)
	}
}

func TestRenderStatusSourceAttributionLifecycleFieldScope(t *testing.T) {
	t.Parallel()

	system := renderStatusSourceAttribution(statusViewSystem)
	if !strings.Contains(system, "field=tool_authority_lifecycle") {
		t.Fatalf("system source attribution = %q, want tool_authority_lifecycle field", system)
	}

	hot := renderStatusSourceAttribution(statusViewHotChats)
	if strings.Contains(hot, "field=tool_authority_lifecycle") {
		t.Fatalf("hot source attribution = %q, do not want tool_authority_lifecycle field", hot)
	}

	find := renderStatusSourceAttribution(statusViewFindChat)
	if strings.Contains(find, "field=tool_authority_lifecycle") {
		t.Fatalf("find source attribution = %q, do not want tool_authority_lifecycle field", find)
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
	}, core.MemoryFocus{})
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
	for name, text := range map[string]string{"memory": memoryText, "tailnet": tailnetText, "agents": agentsText} {
		for _, forbidden := range []string{"source=", "enabled=true", "running=true", "kind=", "owner="} {
			if strings.Contains(text, forbidden) {
				t.Fatalf("%s panel = %q, should not contain raw telemetry %q", name, text, forbidden)
			}
		}
		for _, want := range []string{"Status:", "Why:", "Next:"} {
			if !strings.Contains(text, want) {
				t.Fatalf("%s panel = %q, want operator contract field %q", name, text, want)
			}
		}
	}
}
