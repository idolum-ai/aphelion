//go:build linux

package telegramcommands

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/telegram"
)

func TestApprovalWindowEnableCallbackTargetsThreadScope(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := &stubCommandRouter{
		approvalWindowReturn: "Approval window active.",
		threadReplyOK:        true,
		threadReplyReturn: session.TelegramThread{
			ChatID:      7,
			ThreadID:    42,
			DisplaySlot: 5,
			Status:      session.TelegramThreadStatusOpen,
		},
	}
	handled, err := handleTelegramCommandCallback(context.Background(), sender, router, telegram.CallbackQuery{
		ID:      "cb-aw-enable",
		From:    &telegram.User{ID: 1001},
		Data:    encodeApprovalWindowCallbackData(approvalWindowActionEnable15),
		Message: &telegram.Message{MessageID: 77, Chat: &telegram.Chat{ID: 7}},
	})
	if err != nil {
		t.Fatalf("handleTelegramCommandCallback() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if router.approvalWindowAction != approvalWindowActionEnable15 || router.approvalWindowDuration != 15*time.Minute {
		t.Fatalf("approval action/duration = %q/%s, want enable15/15m", router.approvalWindowAction, router.approvalWindowDuration)
	}
	if router.approvalWindowMessage == nil || router.approvalWindowMessage.ChatID != 7 || router.approvalWindowMessage.SenderID != 1001 || router.approvalWindowMessage.TelegramThreadID != 42 {
		t.Fatalf("approval message = %#v, want scoped thread callback message", router.approvalWindowMessage)
	}
	if len(sender.editInline) != 1 {
		t.Fatalf("editInline = %#v, want one active approval-window edit", sender.editInline)
	}
	if !strings.HasPrefix(sender.editInline[0].text, "(thread 5)\n\n") {
		t.Fatalf("edit text = %q, want visible thread display prefix", sender.editInline[0].text)
	}
	if !commandRowsContain(sender.editInline[0].rows, "2x approval time", encodeApprovalWindowCallbackData(approvalWindowActionDouble)) ||
		!commandRowsContain(sender.editInline[0].rows, "Cancel approvals", encodeApprovalWindowCallbackData(approvalWindowActionCancel)) {
		t.Fatalf("edit rows = %#v, want active approval-window controls", sender.editInline[0].rows)
	}
}

func TestApprovalWindowDoubleCallbackKeepsActiveControls(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := &stubCommandRouter{approvalWindowReturn: "Approval window extended."}
	handled, err := handleTelegramCommandCallback(context.Background(), sender, router, telegram.CallbackQuery{
		ID:      "cb-aw-double",
		From:    &telegram.User{ID: 1001},
		Data:    encodeApprovalWindowCallbackData(approvalWindowActionDouble),
		Message: &telegram.Message{MessageID: 77, Chat: &telegram.Chat{ID: 7}},
	})
	if err != nil {
		t.Fatalf("handleTelegramCommandCallback() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if router.approvalWindowAction != approvalWindowActionDouble {
		t.Fatalf("approval action = %q, want double", router.approvalWindowAction)
	}
	if len(sender.editInline) != 1 || !commandRowsContain(sender.editInline[0].rows, "2x approval time", encodeApprovalWindowCallbackData(approvalWindowActionDouble)) {
		t.Fatalf("editInline = %#v, want active approval-window controls", sender.editInline)
	}
}

func TestApprovalWindowCancelCallbackClearsControls(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := &stubCommandRouter{approvalWindowReturn: "Approval window canceled."}
	handled, err := handleTelegramCommandCallback(context.Background(), sender, router, telegram.CallbackQuery{
		ID:      "cb-aw-cancel",
		From:    &telegram.User{ID: 1001},
		Data:    encodeApprovalWindowCallbackData(approvalWindowActionCancel),
		Message: &telegram.Message{MessageID: 77, Chat: &telegram.Chat{ID: 7}},
	})
	if err != nil {
		t.Fatalf("handleTelegramCommandCallback() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if router.approvalWindowAction != approvalWindowActionCancel {
		t.Fatalf("approval action = %q, want cancel", router.approvalWindowAction)
	}
	if len(sender.editClear) != 1 || !strings.Contains(sender.editClear[0].text, "canceled") {
		t.Fatalf("editClear = %#v, want canceled text without controls", sender.editClear)
	}
}

func TestApprovalWindowCloseCallbackOnlyClearsButtons(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := &stubCommandRouter{}
	handled, err := handleTelegramCommandCallback(context.Background(), sender, router, telegram.CallbackQuery{
		ID:      "cb-aw-close",
		From:    &telegram.User{ID: 1001},
		Data:    encodeApprovalWindowCallbackData(approvalWindowActionClose),
		Message: &telegram.Message{MessageID: 77, Text: "Approved.", Chat: &telegram.Chat{ID: 7}},
	})
	if err != nil {
		t.Fatalf("handleTelegramCommandCallback() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if router.approvalWindowAction != "" {
		t.Fatalf("approval action = %q, want no runtime call", router.approvalWindowAction)
	}
	if len(sender.editClear) != 1 || sender.editClear[0].text != "Approved." {
		t.Fatalf("editClear = %#v, want original text without controls", sender.editClear)
	}
}
