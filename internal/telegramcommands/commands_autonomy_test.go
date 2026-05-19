//go:build linux

package telegramcommands

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
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
			AuthorityBehavior:   "approval grants require an open auto mode gate",
		},
	}
	handled, err := handleTelegramCommand(context.Background(), sender, router, core.InboundMessage{
		ChatID:    7,
		SenderID:  1001,
		MessageID: 14,
		Text:      "/auto mode",
	})
	if err != nil {
		t.Fatalf("handleTelegramCommand() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if len(sender.inline) != 1 {
		t.Fatalf("inline = %#v, want one auto mode panel", sender.inline)
	}
	if router.autonomyChatID != 7 || router.autonomySenderID != 1001 {
		t.Fatalf("autonomy status inputs = chat:%d sender:%d, want 7/1001", router.autonomyChatID, router.autonomySenderID)
	}
	for _, want := range []string{"Auto mode", "Default: Ask first", "Ceiling: Leased", "Live changes: enabled", "Authority behavior: approval grants require an open auto mode gate."} {
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
		Data: encodeAutoCallbackData(autoSurfaceMode, "work15"),
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
	if router.autonomyChatID != 7 || router.autonomySenderID != 1001 || router.autonomyArgs != "leased 15m workspace" {
		t.Fatalf("autonomy inputs chat=%d sender=%d args=%q, want workspace preset", router.autonomyChatID, router.autonomySenderID, router.autonomyArgs)
	}
	if len(sender.answers) != 1 {
		t.Fatalf("answers = %#v, want callback acknowledgement", sender.answers)
	}
	if len(sender.editInline) != 1 || !strings.Contains(sender.editInline[0].text, "enabled") || len(sender.editInline[0].rows) == 0 {
		t.Fatalf("editInline = %#v, want edited autonomy panel with buttons", sender.editInline)
	}
}

func TestHandleTelegramCommandAutonomyDoubleCallbackRoutesToRuntime(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := &stubCommandRouter{canRestart: true, autonomyReturn: "Autonomy override doubled for this chat."}
	handled, err := handleTelegramCommandCallback(context.Background(), sender, router, telegram.CallbackQuery{
		ID:   "cb-autonomy-double",
		From: &telegram.User{ID: 1001},
		Data: encodeAutoCallbackData(autoSurfaceMode, autoActionDouble),
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
	if router.autonomyChatID != 7 || router.autonomySenderID != 1001 || router.autonomyArgs != "double" {
		t.Fatalf("autonomy inputs chat=%d sender=%d args=%q, want double", router.autonomyChatID, router.autonomySenderID, router.autonomyArgs)
	}
	if len(sender.editInline) != 1 || !strings.Contains(sender.editInline[0].text, "doubled") || !autoRowsContainCallback(sender.editInline[0].rows, encodeAutoCallbackData(autoSurfaceMode, autoActionDouble)) {
		t.Fatalf("editInline = %#v, want doubled panel with 2x button", sender.editInline)
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
		Text:      "/auto mode leased 15m workspace focused plan",
	})
	if err != nil {
		t.Fatalf("handleTelegramCommand() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if router.autonomyChatID != 7 || router.autonomySenderID != 1001 || router.autonomyArgs != "leased 15m workspace focused plan" {
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
		Text:      "/auto mode leased 8h all",
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

func TestHandleTelegramCommandAutoLimitsShowsReadOnlyPanel(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := &stubCommandRouter{
		canRestart: true,
		autonomyStatus: core.AutonomyStatusSnapshot{
			DefaultMode:         "ask_first",
			Ceiling:             "leased",
			AllowLiveOverrides:  true,
			MaxOverrideDuration: time.Hour,
			AuthorityBehavior:   "approval grants require an open auto mode gate",
		},
	}
	handled, err := handleTelegramCommand(context.Background(), sender, router, core.InboundMessage{
		ChatID:    7,
		SenderID:  1001,
		MessageID: 14,
		Text:      "/auto limits",
	})
	if err != nil {
		t.Fatalf("handleTelegramCommand() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if len(sender.inline) != 1 || !strings.Contains(sender.inline[0].text, "Auto limits") {
		t.Fatalf("inline = %#v, want limits panel", sender.inline)
	}
	if len(sender.inline[0].rows) != 1 || len(sender.inline[0].rows[0]) != 2 {
		t.Fatalf("limits rows = %#v, want back and refresh only", sender.inline[0].rows)
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

func TestHandleTelegramCommandAutoApproveDoubleCallbackRoutesToRuntime(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := &stubCommandRouter{canRestart: true, autoApproveReturn: "Auto approvals doubled for this chat."}
	handled, err := handleTelegramCommandCallback(context.Background(), sender, router, telegram.CallbackQuery{
		ID:   "cb-autoapprove-double",
		From: &telegram.User{ID: 1001},
		Data: encodeAutoCallbackData(autoSurfaceApprovals, autoActionDouble),
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
	if router.autoApproveChatID != 7 || router.autoApproveSenderID != 1001 || router.autoApproveArgs != "double" {
		t.Fatalf("autoapprove inputs chat=%d sender=%d args=%q, want double", router.autoApproveChatID, router.autoApproveSenderID, router.autoApproveArgs)
	}
	if len(sender.editInline) != 1 || !strings.Contains(sender.editInline[0].text, "doubled") || !autoRowsContainCallback(sender.editInline[0].rows, encodeAutoCallbackData(autoSurfaceApprovals, autoActionDouble)) {
		t.Fatalf("editInline = %#v, want doubled panel with 2x button", sender.editInline)
	}
}

func autoRowsContainCallback(rows [][]telegram.InlineButton, callback string) bool {
	for _, row := range rows {
		for _, button := range row {
			if button.CallbackData == callback {
				return true
			}
		}
	}
	return false
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

func TestHandleTelegramCommandAutoThreadRoutesScopedApprovals(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := &stubCommandRouter{
		canRestart:        true,
		autoApproveReturn: "Auto approvals enabled for thread 4.",
		threadsReturn: []session.TelegramThread{
			{ChatID: 7, ThreadID: 4, DisplaySlot: 4, Status: session.TelegramThreadStatusOpen},
		},
		threadReplyOK:     true,
		threadReplyReturn: session.TelegramThread{ChatID: 7, ThreadID: 4, DisplaySlot: 4, Status: session.TelegramThreadStatusOpen},
	}
	handled, err := handleTelegramCommand(context.Background(), sender, router, core.InboundMessage{
		ChatID:    7,
		SenderID:  1001,
		MessageID: 14,
		Text:      "/auto thread 4 approvals 15m workspace uses=1 focused thread work",
	})
	if err != nil {
		t.Fatalf("handleTelegramCommand() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if router.threadReplyChatID != 7 || router.threadReplyMessageID != 4 {
		t.Fatalf("thread lookup = chat:%d thread:%d, want 7/4", router.threadReplyChatID, router.threadReplyMessageID)
	}
	if router.autoApproveMessage == nil || router.autoApproveMessage.TelegramThreadID != 4 || router.autoApproveArgs != "15m workspace uses=1 focused thread work" {
		t.Fatalf("auto approve message=%#v args=%q, want scoped thread 4 command", router.autoApproveMessage, router.autoApproveArgs)
	}
	if router.autoApproveChatID != 7 || router.autoApproveSenderID != 1001 {
		t.Fatalf("auto approve inputs chat=%d sender=%d, want 7/1001", router.autoApproveChatID, router.autoApproveSenderID)
	}
	if len(sender.msgs) != 1 || !strings.Contains(sender.msgs[0].Text, "thread 4") {
		t.Fatalf("messages = %#v, want thread response", sender.msgs)
	}
}

func TestHandleTelegramCommandAutoThreadResolvesDisplaySlotToCanonicalThreadID(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := &stubCommandRouter{
		canRestart:        true,
		autoApproveReturn: "Auto approvals enabled for thread 42.",
		threadReplyOK:     true,
		threadReplyReturn: session.TelegramThread{ChatID: 7, ThreadID: 42, DisplaySlot: 1, Status: session.TelegramThreadStatusOpen},
		threadsReturn: []session.TelegramThread{
			{ChatID: 7, ThreadID: 42, DisplaySlot: 1, Status: session.TelegramThreadStatusOpen},
		},
	}
	handled, err := handleTelegramCommand(context.Background(), sender, router, core.InboundMessage{
		ChatID:    7,
		SenderID:  1001,
		MessageID: 14,
		Text:      "/auto thread 1 approvals 15m workspace uses=1",
	})
	if err != nil {
		t.Fatalf("handleTelegramCommand() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if router.threadsChatID != 7 {
		t.Fatalf("threadsChatID = %d, want display-slot lookup in chat 7", router.threadsChatID)
	}
	if router.threadReplyMessageID != 42 {
		t.Fatalf("thread lookup id = %d, want canonical thread 42", router.threadReplyMessageID)
	}
	if router.autoApproveMessage == nil || router.autoApproveMessage.TelegramThreadID != 42 || router.autoApproveArgs != "15m workspace uses=1" {
		t.Fatalf("auto approve message=%#v args=%q, want canonical thread 42 command", router.autoApproveMessage, router.autoApproveArgs)
	}
}

func TestHandleTelegramCommandAutoThreadRejectsClosedThread(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := &stubCommandRouter{
		canRestart: true,
		threadsReturn: []session.TelegramThread{
			{ChatID: 7, ThreadID: 4, DisplaySlot: 4, Status: session.TelegramThreadStatusClosed},
		},
	}
	handled, err := handleTelegramCommand(context.Background(), sender, router, core.InboundMessage{
		ChatID:    7,
		SenderID:  1001,
		MessageID: 14,
		Text:      "/auto thread 4 approvals 15m workspace",
	})
	if err != nil {
		t.Fatalf("handleTelegramCommand() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if router.autoApproveMessage != nil || router.autoApproveArgs != "" {
		t.Fatalf("auto approve routed unexpectedly message=%#v args=%q", router.autoApproveMessage, router.autoApproveArgs)
	}
	if len(sender.msgs) != 1 || !strings.Contains(sender.msgs[0].Text, "No open thread 4") {
		t.Fatalf("messages = %#v, want closed-thread error", sender.msgs)
	}
}

func TestAutoThreadPanelRecordsCallbackThreadMapping(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := &stubCommandRouter{
		canRestart: true,
		threadsReturn: []session.TelegramThread{
			{ChatID: 7, ThreadID: 42, DisplaySlot: 1, Status: session.TelegramThreadStatusOpen},
		},
		threadReplyOK:     true,
		threadReplyReturn: session.TelegramThread{ChatID: 7, ThreadID: 42, DisplaySlot: 1, Status: session.TelegramThreadStatusOpen},
	}

	handled, err := handleTelegramCommand(context.Background(), sender, router, core.InboundMessage{
		ChatID:    7,
		SenderID:  1001,
		MessageID: 14,
		Text:      "/auto thread 1",
	})
	if err != nil {
		t.Fatalf("handleTelegramCommand() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if len(sender.inline) != 1 || !strings.HasPrefix(sender.inline[0].text, "(thread 1)\n\n") {
		t.Fatalf("inline = %#v, want thread-prefixed auto panel", sender.inline)
	}
	if router.threadCallbackChatID != 7 || router.threadCallbackID != 42 || router.threadCallbackMessageID != 1 || router.threadCallbackSurface != "auto" {
		t.Fatalf("callback mapping chat=%d thread=%d message=%d surface=%q, want 7/42/1/auto", router.threadCallbackChatID, router.threadCallbackID, router.threadCallbackMessageID, router.threadCallbackSurface)
	}
}

func TestAutoCallbackUsesThreadScopeFromCallbackMessage(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := &stubCommandRouter{
		canRestart:        true,
		autoApproveReturn: "Auto approvals enabled for thread 42.",
		threadReplyOK:     true,
		threadReplyReturn: session.TelegramThread{ChatID: 7, ThreadID: 42, DisplaySlot: 1, Status: session.TelegramThreadStatusOpen},
	}

	handled, err := handleTelegramCommandCallback(context.Background(), sender, router, telegram.CallbackQuery{
		ID:   "cb-auto-thread",
		From: &telegram.User{ID: 1001},
		Message: &telegram.Message{
			MessageID: 900,
			Chat:      &telegram.Chat{ID: 7},
		},
		Data: encodeAutoCallbackData(autoSurfaceApprovals, "work15"),
	})
	if err != nil {
		t.Fatalf("handleTelegramCommandCallback() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if router.autoApproveMessage == nil || router.autoApproveMessage.TelegramThreadID != 42 || router.autoApproveMessage.OriginDetail != "thread_display:1" {
		t.Fatalf("auto approve message = %#v, want scoped thread 42 with visible thread 1", router.autoApproveMessage)
	}
	if router.autoApproveArgs != "15m workspace uses=2" {
		t.Fatalf("autoApproveArgs = %q, want work preset", router.autoApproveArgs)
	}
	if len(sender.editInline) != 1 || !strings.HasPrefix(sender.editInline[0].text, "(thread 1)\n\n") || !strings.Contains(sender.editInline[0].text, "thread 42") {
		t.Fatalf("editInline = %#v, want thread-prefixed scoped callback result", sender.editInline)
	}
}

func TestAutoCallbackWithoutThreadMappingUsesDefaultScope(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := &stubCommandRouter{
		canRestart:        true,
		autoApproveReturn: "Auto approvals enabled for default chat.",
	}

	handled, err := handleTelegramCommandCallback(context.Background(), sender, router, telegram.CallbackQuery{
		ID:   "cb-auto-default",
		From: &telegram.User{ID: 1001},
		Message: &telegram.Message{
			MessageID: 900,
			Chat:      &telegram.Chat{ID: 7},
		},
		Data: encodeAutoCallbackData(autoSurfaceApprovals, "work15"),
	})
	if err != nil {
		t.Fatalf("handleTelegramCommandCallback() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if router.autoApproveMessage != nil {
		t.Fatalf("autoApproveMessage = %#v, want default chat method", router.autoApproveMessage)
	}
	if router.autoApproveChatID != 7 || router.autoApproveSenderID != 1001 {
		t.Fatalf("auto approve default inputs chat=%d sender=%d, want 7/1001", router.autoApproveChatID, router.autoApproveSenderID)
	}
	if len(sender.editInline) != 1 || strings.HasPrefix(sender.editInline[0].text, "(thread ") {
		t.Fatalf("editInline = %#v, want unprefixed default callback result", sender.editInline)
	}
}
