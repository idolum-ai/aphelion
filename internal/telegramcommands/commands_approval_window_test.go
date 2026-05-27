//go:build linux

package telegramcommands

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/core"
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

func callbackDataForCommandButton(t *testing.T, rows [][]telegram.InlineButton, label string) string {
	t.Helper()
	for _, row := range rows {
		for _, button := range row {
			if button.Text == label {
				return button.CallbackData
			}
		}
	}
	t.Fatalf("button %q not found in rows %#v", label, rows)
	return ""
}

func TestApprovalWindowRowsExposeOnlyReachableCompoundCallbacks(t *testing.T) {
	t.Parallel()

	offer := session.ApprovalWindowOffer{ID: "offer-test"}
	standaloneData := callbackDataForCommandButton(t, ApprovalWindowOfferRows("offer-test"), "Approve 15m")
	_, standaloneAction, ok := decodeApprovalWindowCallbackData(standaloneData)
	if !ok || standaloneAction != approvalWindowActionEnable15 {
		t.Fatalf("standalone callback action = %q ok=%v, want plain enable15", standaloneAction, ok)
	}
	embeddedData := callbackDataForCommandButton(t, ApprovalWindowEmbeddedOfferRows(offer), "Approve 15m")
	_, embeddedAction, ok := decodeApprovalWindowCallbackData(embeddedData)
	if !ok || embeddedAction != approvalWindowActionEnable15Compound {
		t.Fatalf("embedded callback action = %q ok=%v, want compound enable15", embeddedAction, ok)
	}
	continuationRows, err := approvalWindowOfferRowsForSource(context.Background(), &stubCommandRouter{}, core.InboundMessage{ChatID: 7, SenderID: 1001}, session.ApprovalWindowOfferSourceContinuation, "decision-continuation", "continuation")
	if err != nil {
		t.Fatalf("approvalWindowOfferRowsForSource() err = %v", err)
	}
	continuationData := callbackDataForCommandButton(t, continuationRows, "Approve 15m")
	_, continuationAction, ok := decodeApprovalWindowCallbackData(continuationData)
	if !ok || continuationAction != approvalWindowActionEnable15 {
		t.Fatalf("continuation callback action = %q ok=%v, want plain enable15", continuationAction, ok)
	}
}

func TestApprovalWindowStandaloneEnableCallbackDoesNotApplyCompoundAction(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := &stubCommandRouter{
		approvalWindowReturn:   "Approval windows are admin only.",
		approvalWindowLookupOK: true,
		approvalWindowLookupOffer: session.ApprovalWindowOffer{
			ID:         "offer-decision-denied",
			ChatID:     7,
			ScopeKind:  string(session.ScopeKindTelegramDM),
			ScopeID:    "7",
			SourceKind: session.ApprovalWindowOfferSourceDecision,
			SourceID:   "decision-embedded",
		},
		resolvedDecisionOK: true,
	}
	triggerStarted := make(chan struct{})
	router.triggerContinuationStarted = triggerStarted

	handled, err := handleTelegramCommandCallback(context.Background(), sender, router, telegram.CallbackQuery{
		ID:      "cb-aw-enable-standalone-no-compound",
		From:    &telegram.User{ID: 1002},
		Data:    encodeApprovalWindowCallbackData("offer-decision-denied", approvalWindowActionEnable15),
		Message: &telegram.Message{MessageID: 77, Chat: &telegram.Chat{ID: 7}},
	})
	if err != nil {
		t.Fatalf("handleTelegramCommandCallback() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if router.resolvedDecisionID != "" || router.resolvedDecisionChoice != "" || router.resolvedDecisionActor != 0 {
		t.Fatalf("resolved decision = %q/%q/%d, want no compound decision resolution", router.resolvedDecisionID, router.resolvedDecisionChoice, router.resolvedDecisionActor)
	}
	if router.approveContinuationInput != 0 || router.triggerContinuationInput != 0 {
		t.Fatalf("continuation approve/trigger = %d/%d, want no compound continuation mutation", router.approveContinuationInput, router.triggerContinuationInput)
	}
	if len(sender.inline) != 1 || !strings.Contains(sender.inline[0].text, "Approval windows are admin only.") {
		t.Fatalf("inline = %#v, want original approval-window failure text", sender.inline)
	}
	if strings.Contains(sender.inline[0].text, "Current approval:") || strings.Contains(sender.inline[0].text, "Current continuation:") {
		t.Fatalf("inline text = %q, should not include compound success/failure note", sender.inline[0].text)
	}
}

func TestApprovalWindowEmbeddedCompoundStaleDecisionFailsBeforeOpeningWindow(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := &stubCommandRouter{
		approvalWindowReturn:              "Approval window active.",
		approvalWindowReturnBeforeResolve: true,
		approvalWindowLookupOK:            true,
		approvalWindowLookupOffer: session.ApprovalWindowOffer{
			ID:         "offer-stale-decision",
			ChatID:     7,
			ScopeKind:  string(session.ScopeKindTelegramDM),
			ScopeID:    "7",
			SourceKind: session.ApprovalWindowOfferSourceDecision,
			SourceID:   "decision-stale",
		},
		resolvedDecisionOK: false,
	}

	handled, err := handleTelegramCommandCallback(context.Background(), sender, router, telegram.CallbackQuery{
		ID:      "cb-aw-enable-stale-decision-compound",
		From:    &telegram.User{ID: 1001},
		Data:    encodeApprovalWindowCallbackData("offer-stale-decision", approvalWindowActionEnable15Compound),
		Message: &telegram.Message{MessageID: 77, Chat: &telegram.Chat{ID: 7}},
	})
	if err != nil {
		t.Fatalf("handleTelegramCommandCallback() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if router.approvalWindowAction != "" {
		t.Fatalf("approvalWindowAction = %q, want no approval window enable before valid source", router.approvalWindowAction)
	}
	if router.resolvedDecisionID != "decision-stale" || router.resolvedDecisionChoice != "" {
		t.Fatalf("decision preflight/resolve = %q/%q, want stale source preflight without resolve", router.resolvedDecisionID, router.resolvedDecisionChoice)
	}
	if len(sender.editClear) != 1 || !strings.Contains(sender.editClear[0].text, "Approval window was not opened.") {
		t.Fatalf("editClear = %#v, want fail-closed approval-window edit", sender.editClear)
	}
	if strings.Contains(sender.editClear[0].text, "Approval window active") || strings.Contains(sender.editClear[0].text, "Current approval: approved") {
		t.Fatalf("edit text = %q, should not show active window or compound success", sender.editClear[0].text)
	}
}

func TestApprovalWindowEmbeddedDecisionCompoundDoesNotSendDuplicateActiveCard(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := &stubCommandRouter{
		approvalWindowReturn:   "Approval window active.",
		approvalWindowLookupOK: true,
		approvalWindowLookupOffer: session.ApprovalWindowOffer{
			ID:         "offer-decision",
			ChatID:     7,
			ScopeKind:  string(session.ScopeKindTelegramDM),
			ScopeID:    "7",
			SourceKind: session.ApprovalWindowOfferSourceDecision,
			SourceID:   "decision-embedded",
		},
		resolvedDecisionOK: true,
	}
	triggerStarted := make(chan struct{})
	router.triggerContinuationStarted = triggerStarted
	handled, err := handleTelegramCommandCallback(context.Background(), sender, router, telegram.CallbackQuery{
		ID:      "cb-aw-enable-decision-compound",
		From:    &telegram.User{ID: 1001},
		Data:    encodeApprovalWindowCallbackData("offer-decision", approvalWindowActionEnable15Compound),
		Message: &telegram.Message{MessageID: 77, Chat: &telegram.Chat{ID: 7}},
	})
	if err != nil {
		t.Fatalf("handleTelegramCommandCallback() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if router.resolvedDecisionID != "decision-embedded" || router.resolvedDecisionChoice != "approve" || router.resolvedDecisionActor != 1001 {
		t.Fatalf("resolved decision = %q/%q/%d, want embedded approve by actor", router.resolvedDecisionID, router.resolvedDecisionChoice, router.resolvedDecisionActor)
	}
	if router.approveContinuationInput != 0 || router.triggerContinuationInput != 0 {
		t.Fatalf("continuation approve/trigger = %d/%d, want no continuation mutation for decision offer", router.approveContinuationInput, router.triggerContinuationInput)
	}
	if len(sender.inline) != 0 {
		t.Fatalf("inline = %#v, want no duplicate active approval-window card from compound callback", sender.inline)
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
