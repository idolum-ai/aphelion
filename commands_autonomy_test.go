//go:build linux

package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/telegram"
)

func TestHandleTelegramCommandAutonomyAdmin(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := &stubCommandRouter{
		canRestart: true,
		autonomyStatus: core.AutonomyStatusSnapshot{
			DefaultMode:         "ask_first",
			Ceiling:             "leased",
			AllowLiveOverrides:  true,
			MaxOverrideDuration: 2 * time.Hour,
			Source:              "config",
			AuthorityBehavior:   "existing proposal and approval flows",
		},
	}
	handled, err := handleTelegramCommand(context.Background(), sender, router, core.InboundMessage{
		ChatID:    7,
		SenderID:  1001,
		MessageID: 14,
		Text:      "/auto policy",
	})
	if err != nil {
		t.Fatalf("handleTelegramCommand() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if len(sender.inline) != 1 {
		t.Fatalf("inline = %#v, want one auto policy panel", sender.inline)
	}
	if router.autonomyChatID != 7 || router.autonomySenderID != 1001 {
		t.Fatalf("autonomy status inputs = chat:%d sender:%d, want 7/1001", router.autonomyChatID, router.autonomySenderID)
	}
	for _, want := range []string{"Auto policy", "Default: Ask first", "Ceiling: Leased", "Live changes: enabled", "Authority behavior: existing proposal and approval flows."} {
		if !strings.Contains(sender.inline[0].text, want) {
			t.Fatalf("autonomy response = %q, want %q", sender.inline[0].text, want)
		}
	}
	if len(sender.inline[0].rows) == 0 {
		t.Fatalf("autonomy rows empty, want preset buttons")
	}
}

func TestHandleTelegramCommandAutonomyPresetCallbackAppliesLeasedOverride(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := &stubCommandRouter{canRestart: true, autonomyReturn: "Autonomy override enabled for this chat."}
	handled, err := handleTelegramCommandCallback(context.Background(), sender, router, telegram.CallbackQuery{
		ID:   "cb-autonomy-work",
		From: &telegram.User{ID: 1001},
		Data: encodeAutoCallbackData(autoSurfacePolicy, "work15"),
		Message: &telegram.Message{
			MessageID: 77,
			Chat:      &telegram.Chat{ID: 7, Type: "private"},
		},
	})
	if err != nil {
		t.Fatalf("handleTelegramCommandCallback() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if router.autonomyChatID != 7 || router.autonomySenderID != 1001 || router.autonomyArgs != "leased 15m workspace uses=2" {
		t.Fatalf("autonomy inputs chat=%d sender=%d args=%q, want workspace preset", router.autonomyChatID, router.autonomySenderID, router.autonomyArgs)
	}
	if len(sender.answers) != 1 {
		t.Fatalf("answers = %#v, want callback acknowledgement", sender.answers)
	}
	if len(sender.editInline) != 1 || !strings.Contains(sender.editInline[0].text, "enabled") || len(sender.editInline[0].rows) == 0 {
		t.Fatalf("editInline = %#v, want edited autonomy panel with buttons", sender.editInline)
	}
}

func TestHandleTelegramCommandAutonomyLeasedAdmin(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := &stubCommandRouter{canRestart: true, autonomyReturn: "Autonomy override enabled for this chat."}
	handled, err := handleTelegramCommand(context.Background(), sender, router, core.InboundMessage{
		ChatID:    7,
		SenderID:  1001,
		MessageID: 14,
		Text:      "/auto policy leased 15m workspace uses=2 focused plan",
	})
	if err != nil {
		t.Fatalf("handleTelegramCommand() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if router.autonomyChatID != 7 || router.autonomySenderID != 1001 || router.autonomyArgs != "leased 15m workspace uses=2 focused plan" {
		t.Fatalf("autonomy inputs = chat:%d sender:%d args:%q, want leased command", router.autonomyChatID, router.autonomySenderID, router.autonomyArgs)
	}
	if len(sender.msgs) != 1 || !strings.Contains(sender.msgs[0].Text, "enabled") {
		t.Fatalf("messages = %#v, want enabled response", sender.msgs)
	}
}

func TestHandleTelegramCommandAutonomyValidationErrorRepliesWithoutFatalError(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := &stubCommandRouter{canRestart: true, autonomyErr: errors.New("autonomy live override duration is capped at 4h0m0s")}
	handled, err := handleTelegramCommand(context.Background(), sender, router, core.InboundMessage{
		ChatID:    7,
		SenderID:  1001,
		MessageID: 14,
		Text:      "/auto policy leased 8h all",
	})
	if err != nil {
		t.Fatalf("handleTelegramCommand() err = %v, want nil so poller can advance the update offset", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if router.autonomyArgs != "leased 8h all" {
		t.Fatalf("autonomyArgs = %q, want command args recorded", router.autonomyArgs)
	}
	if len(sender.msgs) != 1 || !strings.Contains(sender.msgs[0].Text, "not applied") || !strings.Contains(sender.msgs[0].Text, "capped") {
		t.Fatalf("messages = %#v, want validation reply", sender.msgs)
	}
}

func TestHandleTelegramCommandAutonomyDeniedForNonAdmin(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := &stubCommandRouter{canRestart: false}
	handled, err := handleTelegramCommand(context.Background(), sender, router, core.InboundMessage{
		ChatID:   7,
		SenderID: 1002,
		Text:     "/auto",
	})
	if err != nil {
		t.Fatalf("handleTelegramCommand() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if len(sender.msgs) != 1 || !strings.Contains(strings.ToLower(sender.msgs[0].Text), "admin only") {
		t.Fatalf("messages = %#v, want admin-only response", sender.msgs)
	}
}

func TestHandleTelegramCommandAutoApproveNoArgsShowsPresetButtons(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := &stubCommandRouter{canRestart: true, autoApproveStatusReturn: "Auto approvals status for this chat."}
	handled, err := handleTelegramCommand(context.Background(), sender, router, core.InboundMessage{
		ChatID:    7,
		SenderID:  1001,
		MessageID: 14,
		Text:      "/auto approvals",
	})
	if err != nil {
		t.Fatalf("handleTelegramCommand() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if router.autoApproveStatusChatID != 7 || router.autoApproveStatusSenderID != 1001 || router.autoApproveArgs != "" {
		t.Fatalf("auto approvals status inputs = chat:%d sender:%d args:%q, want 7/1001/no configure", router.autoApproveStatusChatID, router.autoApproveStatusSenderID, router.autoApproveArgs)
	}
	if len(sender.inline) != 1 || !strings.Contains(sender.inline[0].text, "status") || len(sender.inline[0].rows) == 0 {
		t.Fatalf("inline = %#v, want auto-approval panel with preset buttons", sender.inline)
	}
}

func TestHandleTelegramCommandAutoApprovePresetCallbackAppliesLease(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := &stubCommandRouter{canRestart: true, autoApproveReturn: "Auto approvals enabled for this chat."}
	handled, err := handleTelegramCommandCallback(context.Background(), sender, router, telegram.CallbackQuery{
		ID:   "cb-autoapprove-deploy",
		From: &telegram.User{ID: 1001},
		Data: encodeAutoCallbackData(autoSurfaceApprovals, "deploy15"),
		Message: &telegram.Message{
			MessageID: 77,
			Chat:      &telegram.Chat{ID: 7, Type: "private"},
		},
	})
	if err != nil {
		t.Fatalf("handleTelegramCommandCallback() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if router.autoApproveChatID != 7 || router.autoApproveSenderID != 1001 || router.autoApproveArgs != "15m deploy uses=1" {
		t.Fatalf("autoapprove inputs chat=%d sender=%d args=%q, want deploy preset", router.autoApproveChatID, router.autoApproveSenderID, router.autoApproveArgs)
	}
	if len(sender.answers) != 1 {
		t.Fatalf("answers = %#v, want callback acknowledgement", sender.answers)
	}
	if len(sender.editInline) != 1 || !strings.Contains(sender.editInline[0].text, "enabled") || len(sender.editInline[0].rows) == 0 {
		t.Fatalf("editInline = %#v, want edited auto-approval panel with buttons", sender.editInline)
	}
}

func TestHandleTelegramCommandAutoApproveAdmin(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := &stubCommandRouter{canRestart: true, autoApproveReturn: "Auto approvals enabled for this chat."}
	handled, err := handleTelegramCommand(context.Background(), sender, router, core.InboundMessage{
		ChatID:    7,
		SenderID:  1001,
		MessageID: 14,
		Text:      "/auto approvals 15m all uses=2",
	})
	if err != nil {
		t.Fatalf("handleTelegramCommand() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if router.autoApproveChatID != 7 || router.autoApproveSenderID != 1001 || router.autoApproveArgs != "15m all uses=2" {
		t.Fatalf("autoapprove inputs = chat:%d sender:%d args:%q, want 7/1001/15m all uses=2", router.autoApproveChatID, router.autoApproveSenderID, router.autoApproveArgs)
	}
	if len(sender.msgs) != 1 || !strings.Contains(sender.msgs[0].Text, "enabled") {
		t.Fatalf("messages = %#v, want enabled response", sender.msgs)
	}
}

func TestHandleTelegramCommandAutoApproveValidationErrorRepliesWithoutFatalError(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := &stubCommandRouter{canRestart: true, autoApproveErr: errors.New("auto-approval duration is capped at 48h0m0s")}
	handled, err := handleTelegramCommand(context.Background(), sender, router, core.InboundMessage{
		ChatID:    7,
		SenderID:  1001,
		MessageID: 14,
		Text:      "/auto approvals 24h all",
	})
	if err != nil {
		t.Fatalf("handleTelegramCommand() err = %v, want nil so poller can advance the update offset", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if router.autoApproveArgs != "24h all" {
		t.Fatalf("autoApproveArgs = %q, want command args recorded", router.autoApproveArgs)
	}
	if len(sender.msgs) != 1 || !strings.Contains(sender.msgs[0].Text, "not applied") || !strings.Contains(sender.msgs[0].Text, "capped") {
		t.Fatalf("messages = %#v, want validation reply", sender.msgs)
	}
}

func TestHandleTelegramCommandAutoApproveDeniedForNonAdmin(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := &stubCommandRouter{canRestart: false}
	handled, err := handleTelegramCommand(context.Background(), sender, router, core.InboundMessage{
		ChatID:   7,
		SenderID: 1002,
		Text:     "/auto approvals 15m all",
	})
	if err != nil {
		t.Fatalf("handleTelegramCommand() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if router.autoApproveChatID != 0 {
		t.Fatalf("autoApproveChatID = %d, want not called", router.autoApproveChatID)
	}
	if len(sender.msgs) != 1 || !strings.Contains(strings.ToLower(sender.msgs[0].Text), "admin only") {
		t.Fatalf("messages = %#v, want admin-only denial", sender.msgs)
	}
}
