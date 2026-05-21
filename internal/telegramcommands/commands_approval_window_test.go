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

func TestApprovalWindowRowsRespectTelegramLabelContract(t *testing.T) {
	t.Parallel()

	offer := session.ApprovalWindowOffer{ID: "offer-test"}
	for name, rows := range map[string][][]telegram.InlineButton{
		"offer":    ApprovalWindowOfferRows("offer-test"),
		"embedded": ApprovalWindowEmbeddedOfferRows(offer),
		"active":   ApprovalWindowActiveRows("offer-test"),
	} {
		for rowIndex, row := range rows {
			for buttonIndex, button := range row {
				if words := strings.Fields(button.Text); len(words) > 2 {
					t.Fatalf("%s row %d button %d label %q has %d words, want <= 2", name, rowIndex, buttonIndex, button.Text, len(words))
				}
			}
		}
	}
}

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
		Data:    encodeApprovalWindowCallbackData("offer-test", approvalWindowActionEnable15),
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
	if router.approvalWindowOfferID != "offer-test" {
		t.Fatalf("approval offer id = %q, want offer-test", router.approvalWindowOfferID)
	}
	if len(sender.editInline) != 0 {
		t.Fatalf("editInline = %#v, want no edit of the pending approval card", sender.editInline)
	}
	if len(sender.inline) != 1 {
		t.Fatalf("inline = %#v, want one active approval-window control card", sender.inline)
	}
	if sender.inline[0].replyTo == nil || *sender.inline[0].replyTo != 77 {
		t.Fatalf("inline replyTo = %#v, want reply to original approval card 77", sender.inline[0].replyTo)
	}
	if !strings.HasPrefix(sender.inline[0].text, "(thread 5)\n\n") {
		t.Fatalf("inline text = %q, want visible thread display prefix", sender.inline[0].text)
	}
	if !commandRowsContain(sender.inline[0].rows, "Double time", encodeApprovalWindowCallbackData("offer-test", approvalWindowActionDouble)) ||
		!commandRowsContain(sender.inline[0].rows, "Cancel approvals", encodeApprovalWindowCallbackData("offer-test", approvalWindowActionCancel)) {
		t.Fatalf("inline rows = %#v, want active approval-window controls", sender.inline[0].rows)
	}
}

func TestApprovalWindowDoubleCallbackKeepsActiveControls(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := &stubCommandRouter{approvalWindowReturn: "Approval window extended."}
	handled, err := handleTelegramCommandCallback(context.Background(), sender, router, telegram.CallbackQuery{
		ID:      "cb-aw-double",
		From:    &telegram.User{ID: 1001},
		Data:    encodeApprovalWindowCallbackData("offer-test", approvalWindowActionDouble),
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
	if len(sender.editInline) != 1 || !commandRowsContain(sender.editInline[0].rows, "Double time", encodeApprovalWindowCallbackData("offer-test", approvalWindowActionDouble)) {
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
		Data:    encodeApprovalWindowCallbackData("offer-test", approvalWindowActionCancel),
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
		Data:    encodeApprovalWindowCallbackData("offer-test", approvalWindowActionClose),
		Message: &telegram.Message{MessageID: 77, Text: "Approved.", Chat: &telegram.Chat{ID: 7}},
	})
	if err != nil {
		t.Fatalf("handleTelegramCommandCallback() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if router.approvalWindowAction != approvalWindowActionClose || router.approvalWindowOfferID != "offer-test" {
		t.Fatalf("approval close = action:%q offer:%q, want close/offer-test", router.approvalWindowAction, router.approvalWindowOfferID)
	}
	if len(sender.editClear) != 1 || sender.editClear[0].text != "Approved." {
		t.Fatalf("editClear = %#v, want original text without controls", sender.editClear)
	}
}
