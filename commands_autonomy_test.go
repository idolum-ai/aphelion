//go:build linux

package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/core"
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
		Text:      "/autonomy",
	})
	if err != nil {
		t.Fatalf("handleTelegramCommand() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if len(sender.msgs) != 1 {
		t.Fatalf("messages = %#v, want one autonomy response", sender.msgs)
	}
	if router.autonomyChatID != 7 || router.autonomySenderID != 1001 {
		t.Fatalf("autonomy status inputs = chat:%d sender:%d, want 7/1001", router.autonomyChatID, router.autonomySenderID)
	}
	for _, want := range []string{"Autonomy policy", "Default: Ask first", "Ceiling: Leased", "Live changes: enabled", "Authority behavior: existing proposal and approval flows."} {
		if !strings.Contains(sender.msgs[0].Text, want) {
			t.Fatalf("autonomy response = %q, want %q", sender.msgs[0].Text, want)
		}
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
		Text:      "/autonomy leased 15m workspace uses=2 focused plan",
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
		Text:      "/autonomy leased 8h all",
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
		Text:     "/autonomy",
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

func TestHandleTelegramCommandAutoApproveAdmin(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := &stubCommandRouter{canRestart: true, autoApproveReturn: "Auto-approval enabled for this chat."}
	handled, err := handleTelegramCommand(context.Background(), sender, router, core.InboundMessage{
		ChatID:    7,
		SenderID:  1001,
		MessageID: 14,
		Text:      "/autoapprove 15m all uses=2",
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
		Text:      "/autoapprove 24h all",
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
		Text:     "/autoapprove 15m all",
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
