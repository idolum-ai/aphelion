//go:build linux

package telegramcommands

import (
	"context"
	"strings"
	"testing"

	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/telegram"
)

func TestCommandMenuCallbackRoutesNoArgMissionCommand(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := &stubCommandRouter{
		missionHomeMissions: []session.MissionState{{
			ID:        "mission-menu",
			Title:     "Menu mission",
			Objective: "Verify command menu routing.",
			Status:    session.MissionStatusCandidate,
			Authority: session.DefaultMissionAuthority(),
		}},
	}
	handled, err := handleTelegramCommandCallback(context.Background(), sender, router, telegram.CallbackQuery{
		ID:   "cb-menu-mission",
		From: &telegram.User{ID: 1001},
		Data: encodeCommandMenuCallbackData("mission"),
		Message: &telegram.Message{
			MessageID: 55,
			Chat:      &telegram.Chat{ID: 7, Type: "private"},
		},
	})
	if err != nil {
		t.Fatalf("handleTelegramCommandCallback() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if len(sender.answers) != 1 {
		t.Fatalf("answers = %#v, want menu callback acknowledgement", sender.answers)
	}
	if router.missionHomeChatID != 7 || router.missionHomeSenderID != 1001 {
		t.Fatalf("mission home inputs chat=%d sender=%d, want routed callback", router.missionHomeChatID, router.missionHomeSenderID)
	}
	if len(sender.inline) != 1 || !strings.Contains(sender.inline[0].text, "Mission Ledger") || len(sender.inline[0].rows) == 0 {
		t.Fatalf("inline = %#v, want routed mission button panel", sender.inline)
	}
}

func TestCommandMenuCallbackDeniesAdminCommandForNonAdmin(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := &stubCommandRouter{canRestart: false}
	handled, err := handleTelegramCommandCallback(context.Background(), sender, router, telegram.CallbackQuery{
		ID:   "cb-menu-restart-denied",
		From: &telegram.User{ID: 1002},
		Data: encodeCommandMenuCallbackData("restart"),
		Message: &telegram.Message{
			MessageID: 55,
			Chat:      &telegram.Chat{ID: 7, Type: "private"},
		},
	})
	if err != nil {
		t.Fatalf("handleTelegramCommandCallback() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if len(sender.answers) != 1 || !strings.Contains(strings.ToLower(sender.answers[0].text), "admin") {
		t.Fatalf("answers = %#v, want admin-only denial", sender.answers)
	}
	if router.restartCalls != 0 {
		t.Fatalf("restartCalls = %d, want denied command not routed", router.restartCalls)
	}
	if len(sender.inline) != 0 || len(sender.msgs) != 0 {
		t.Fatalf("sender inline=%#v msgs=%#v, want acknowledgement only", sender.inline, sender.msgs)
	}
}

func TestCommandMenuRowsExposeRoleScopedButtons(t *testing.T) {
	t.Parallel()

	publicRows := commandMenuRows(false)
	adminRows := commandMenuRows(true)
	if !commandRowsContain(publicRows, "Mission", "menu:mission") || !commandRowsContain(publicRows, "Status", "menu:status") {
		t.Fatalf("public rows = %#v, want no-arg public commands", publicRows)
	}
	if commandRowsContain(publicRows, "Restart", "menu:restart") {
		t.Fatalf("public rows = %#v, want restart hidden", publicRows)
	}
	if commandRowsContain(adminRows, "Auto", "menu:auto") {
		t.Fatalf("admin rows = %#v, want no /auto command", adminRows)
	}
	if !commandRowsContain(adminRows, "Restart", "menu:restart") {
		t.Fatalf("admin rows = %#v, want admin restart command", adminRows)
	}
}

func commandRowsContain(rows [][]telegram.InlineButton, label string, callback string) bool {
	for _, row := range rows {
		for _, button := range row {
			if button.Text == label && button.CallbackData == callback {
				return true
			}
		}
	}
	return false
}
