//go:build linux

package telegramcommands

import (
	"strings"
	"testing"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/telegram"
)

func TestTelegramVoiceContractForCommandSurfaces(t *testing.T) {
	t.Parallel()

	actionText := renderActionProposalPrompt(session.ActionProposal{
		ID:               "aprop-voice",
		MissionID:        "mission-voice",
		Summary:          "Clean up Telegram voice.",
		WhyNow:           "The command surface should speak in one voice.",
		BoundedEffect:    "Presentation-only cleanup.",
		RiskClass:        "ui",
		AllowedActions:   []string{"edit_copy"},
		ForbiddenActions: []string{"grant_authority"},
		ValidationPlan:   []string{"run tests"},
	})
	for _, notWant := range []string{"ActionProposal"} {
		if strings.Contains(actionText, notWant) {
			t.Fatalf("action proposal text = %q, contains stale voice %q", actionText, notWant)
		}
	}
	assertTelegramButtonLabels(t, "action proposal", actionProposalButtonRows("aprop-voice"))
	assertTelegramRowLabels(t, "action proposal", actionProposalButtonRows("aprop-voice")[0], []string{"Reject", "Change", "Approve"})

	agentsTop := durableAgentsBoardTopRow(telegramPageViewList, 1)
	assertTelegramRowLabels(t, "agents board", agentsTop, []string{"Refresh", "Analyze", "Show Retired"})
	assertTelegramButtonLabels(t, "agents board", [][]telegram.InlineButton{agentsTop})

	retireRows := durableAgentRetireConfirmRows("ops-child", telegramPageViewList, 1)
	assertTelegramButtonLabels(t, "agent retire", retireRows)
	assertTelegramRowLabels(t, "agent retire primary", retireRows[0], []string{"Cancel", "Retire"})
	assertTelegramRowLabels(t, "agent retire secondary", retireRows[1], []string{"Brief"})

	promotionRows := telegramThreadPromotionDraftRows("thread-promotion:1001:3:99")
	assertTelegramButtonLabels(t, "thread promotion", promotionRows)
	assertTelegramRowLabels(t, "thread promotion primary", promotionRows[0], []string{"Cancel", "Ready"})
	assertTelegramRowLabels(t, "thread promotion secondary", promotionRows[1], []string{"Refresh"})

	threadDetailRows := telegramThreadDetailRows(session.TelegramThread{ThreadID: 42, DisplaySlot: 1})
	assertTelegramRowLabels(t, "thread detail primary", threadDetailRows[0], []string{"Promote", "Absorb"})
	assertTelegramRowLabels(t, "thread detail secondary", threadDetailRows[1], []string{"Back"})

	threadText, threadRows := renderTelegramThreadsPanel([]session.TelegramThread{{
		ChatID:      7,
		ThreadID:    42,
		DisplaySlot: 1,
		Status:      session.TelegramThreadStatusOpen,
		CreatedText: "review the README",
	}}, telegramPageViewList, 1)
	for _, notWant := range []string{"**On Threads**", "**Open Threads**"} {
		if strings.Contains(threadText, notWant) {
			t.Fatalf("thread panel text = %q, contains stale heading %q", threadText, notWant)
		}
	}
	assertTelegramButtonLabels(t, "threads board", threadRows)

	pageRows := telegramPageNavigationRows(telegramPageInfo{Page: 1, PageSize: 5, Total: 10, PageCount: 2, Start: 0, End: 5}, telegramPageSurfaceThreads, telegramPageViewList)
	assertTelegramButtonLabels(t, "pagination", pageRows)
}

func assertTelegramButtonLabels(t *testing.T, surface string, rows [][]telegram.InlineButton) {
	t.Helper()
	for _, row := range rows {
		for _, button := range row {
			if strings.TrimSpace(button.Text) == "" {
				t.Fatalf("%s button has empty label: %#v", surface, button)
			}
			if words := strings.Fields(button.Text); len(words) > 2 {
				t.Fatalf("%s button label %q has %d words, want at most 2", surface, button.Text, len(words))
			}
			if len(button.CallbackData) > core.TelegramCallbackDataMaxBytes {
				t.Fatalf("%s button %q callback len=%d exceeds Telegram limit", surface, button.Text, len(button.CallbackData))
			}
		}
	}
}

func assertTelegramRowLabels(t *testing.T, surface string, row []telegram.InlineButton, want []string) {
	t.Helper()
	if len(row) != len(want) {
		t.Fatalf("%s row = %#v, want labels %v", surface, row, want)
	}
	for i, label := range want {
		if row[i].Text != label {
			t.Fatalf("%s row = %#v, want labels %v", surface, row, want)
		}
	}
}
