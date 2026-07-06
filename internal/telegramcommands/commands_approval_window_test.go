//go:build linux

package telegramcommands

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/decision"
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

func TestApprovalWindowRowsForOfferDoesNotRenderUsedOfferAsActive(t *testing.T) {
	t.Parallel()

	offer := session.ApprovalWindowOffer{ID: "offer-used", UsedAt: time.Now().UTC()}
	if rows := ApprovalWindowRowsForOffer(offer); rows != nil {
		t.Fatalf("ApprovalWindowRowsForOffer(used) = %#v, want nil stale controls", rows)
	}
}

func TestApprovalWindowRowsForOfferRendersOpenedUsedOfferAsActive(t *testing.T) {
	t.Parallel()

	offer := session.ApprovalWindowOffer{ID: "offer-opened", UsedAt: time.Now().UTC(), OpenedLeaseID: "lease-opened", OpenedOverrideID: "override-opened"}
	rows := ApprovalWindowRowsForLiveOffer(offer)
	if len(rows) != 1 || len(rows[0]) != 2 {
		t.Fatalf("ApprovalWindowRowsForOffer(opened) = %#v, want active controls", rows)
	}
	if rows[0][0].Text != "Double time" || rows[0][1].Text != "Cancel approvals" {
		t.Fatalf("rows = %#v, want active controls", rows)
	}
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

func TestApprovalWindowRowsUseConfiguredDefaultDuration(t *testing.T) {
	t.Parallel()

	offer := session.ApprovalWindowOffer{ID: "offer-configured"}
	standaloneData := callbackDataForCommandButton(t, ApprovalWindowOfferRowsForDuration("offer-configured", 30*time.Minute), "Approve 30m")
	_, standaloneAction, ok := decodeApprovalWindowCallbackData(standaloneData)
	if !ok || standaloneAction != approvalWindowActionEnable15 {
		t.Fatalf("standalone callback action = %q ok=%v, want plain enable15", standaloneAction, ok)
	}
	embeddedData := callbackDataForCommandButton(t, ApprovalWindowEmbeddedOfferRowsForDuration(offer, 30*time.Minute), "Approve 30m")
	_, embeddedAction, ok := decodeApprovalWindowCallbackData(embeddedData)
	if !ok || embeddedAction != approvalWindowActionEnable15Compound {
		t.Fatalf("embedded callback action = %q ok=%v, want compound enable15", embeddedAction, ok)
	}
	router := &stubCommandRouter{defaultApprovalWindowDuration: 30 * time.Minute}
	continuationRows, err := approvalWindowOfferRowsForSource(context.Background(), router, core.InboundMessage{ChatID: 7, SenderID: 1001}, session.ApprovalWindowOfferSourceContinuation, "decision-continuation-30m", "continuation")
	if err != nil {
		t.Fatalf("approvalWindowOfferRowsForSource() err = %v", err)
	}
	continuationData := callbackDataForCommandButton(t, continuationRows, "Approve 30m")
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
			ID:                 "offer-decision-denied",
			ChatID:             7,
			ScopeKind:          string(session.ScopeKindTelegramDM),
			ScopeID:            "7",
			SourceKind:         session.ApprovalWindowOfferSourceDecision,
			SourceDecisionKind: string(decision.KindProposalApproval),
			SourceID:           "decision-embedded",
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
	if len(sender.editInline) != 0 {
		t.Fatalf("editInline = %#v, want no active controls for non-active response", sender.editInline)
	}
	if len(sender.inline) != 1 || !strings.Contains(sender.inline[0].text, "Approval windows are admin only.") {
		t.Fatalf("inline = %#v, want original approval-window failure text", sender.inline)
	}
	if len(sender.inline[0].rows) != 0 {
		t.Fatalf("inline rows = %#v, want no active controls for non-active response", sender.inline[0].rows)
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
		approvalWindowActive:              true,
		approvalWindowReturnBeforeResolve: true,
		approvalWindowLookupOK:            true,
		approvalWindowLookupOffer: session.ApprovalWindowOffer{
			ID:                 "offer-stale-decision",
			ChatID:             7,
			ScopeKind:          string(session.ScopeKindTelegramDM),
			ScopeID:            "7",
			SourceKind:         session.ApprovalWindowOfferSourceDecision,
			SourceDecisionKind: string(decision.KindProposalApproval),
			SourceID:           "decision-stale",
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

func TestApprovalWindowEmbeddedDecisionCompoundRejectsNonProposalDecisionKind(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := &stubCommandRouter{
		approvalWindowReturn:              "Approval window active.",
		approvalWindowActive:              true,
		approvalWindowReturnBeforeResolve: true,
		approvalWindowLookupOK:            true,
		approvalWindowLookupOffer: session.ApprovalWindowOffer{
			ID:                 "offer-non-proposal",
			ChatID:             7,
			ScopeKind:          string(session.ScopeKindTelegramDM),
			ScopeID:            "7",
			SourceKind:         session.ApprovalWindowOfferSourceDecision,
			SourceDecisionKind: string(decision.KindArtifactRetention),
			SourceID:           "decision-continuation",
		},
		resolvedDecisionOK: true,
	}

	handled, err := handleTelegramCommandCallback(context.Background(), sender, router, telegram.CallbackQuery{
		ID:      "cb-aw-enable-non-proposal-compound",
		From:    &telegram.User{ID: 1001},
		Data:    encodeApprovalWindowCallbackData("offer-non-proposal", approvalWindowActionEnable15Compound),
		Message: &telegram.Message{MessageID: 77, Chat: &telegram.Chat{ID: 7}},
	})
	if err != nil {
		t.Fatalf("handleTelegramCommandCallback() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if router.approvalWindowAction != "" {
		t.Fatalf("approvalWindowAction = %q, want no approval window enable", router.approvalWindowAction)
	}
	if router.resolvedDecisionChoice != "" {
		t.Fatalf("resolvedDecisionChoice = %q, want no resolve", router.resolvedDecisionChoice)
	}
	if len(sender.editClear) != 1 || !strings.Contains(sender.editClear[0].text, "not a proposal approval") {
		t.Fatalf("editClear = %#v, want proposal-kind rejection", sender.editClear)
	}
}

func TestApprovalWindowEmbeddedDecisionCompoundRollsBackWhenResolveFailsAfterOpen(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := &stubCommandRouter{
		approvalWindowReturn:              "Approval window active.",
		approvalWindowActive:              true,
		approvalWindowReturnBeforeResolve: true,
		approvalWindowLookupOK:            true,
		approvalWindowLookupOffer: session.ApprovalWindowOffer{
			ID:                 "offer-resolve-race",
			ChatID:             7,
			ScopeKind:          string(session.ScopeKindTelegramDM),
			ScopeID:            "7",
			SourceKind:         session.ApprovalWindowOfferSourceDecision,
			SourceDecisionKind: string(decision.KindProposalApproval),
			SourceID:           "decision-race",
		},
		resolvedDecisionPeekOK: true,
		resolvedDecisionOK:     false,
	}

	handled, err := handleTelegramCommandCallback(context.Background(), sender, router, telegram.CallbackQuery{
		ID:      "cb-aw-enable-decision-compound-race",
		From:    &telegram.User{ID: 1001},
		Data:    encodeApprovalWindowCallbackData("offer-resolve-race", approvalWindowActionEnable15Compound),
		Message: &telegram.Message{MessageID: 77, Chat: &telegram.Chat{ID: 7}},
	})
	if err != nil {
		t.Fatalf("handleTelegramCommandCallback() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if router.approvalWindowCancelCalls != 1 || router.approvalWindowAction != approvalWindowActionCancel {
		t.Fatalf("approval rollback action/calls = %q/%d, want one cancel", router.approvalWindowAction, router.approvalWindowCancelCalls)
	}
	if router.resolvedDecisionID != "decision-race" || router.resolvedDecisionChoice != "approve" {
		t.Fatalf("resolve attempt = %q/%q, want final resolve attempted after open", router.resolvedDecisionID, router.resolvedDecisionChoice)
	}
	if len(sender.editClear) != 1 || !strings.Contains(sender.editClear[0].text, "Approval window was not opened.") {
		t.Fatalf("editClear = %#v, want fail-closed approval-window edit", sender.editClear)
	}
	if len(sender.inline) != 0 || len(sender.editInline) != 0 {
		t.Fatalf("inline=%#v editInline=%#v, want no active controls after rollback", sender.inline, sender.editInline)
	}
}

func TestApprovalWindowEmbeddedDecisionCompoundOpensBeforeResolveWithoutDuplicateCard(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := &stubCommandRouter{
		approvalWindowReturn:              "Approval window active.",
		approvalWindowActive:              true,
		approvalWindowReturnBeforeResolve: true,
		approvalWindowLookupOK:            true,
		approvalWindowLookupOffer: session.ApprovalWindowOffer{
			ID:                 "offer-decision",
			ChatID:             7,
			ScopeKind:          string(session.ScopeKindTelegramDM),
			ScopeID:            "7",
			SourceKind:         session.ApprovalWindowOfferSourceDecision,
			SourceDecisionKind: string(decision.KindProposalApproval),
			SourceID:           "decision-embedded",
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
	if router.approvalWindowAction != approvalWindowActionEnable15 || router.approvalWindowOfferID != "offer-decision" {
		t.Fatalf("approval window action = %q offer=%q, want enable15 offer-decision", router.approvalWindowAction, router.approvalWindowOfferID)
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
		approvalWindowActive: true,
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

func TestApprovalWindowEnableCallbackUsesConfiguredDefaultDuration(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := &stubCommandRouter{
		defaultApprovalWindowDuration: 30 * time.Minute,
		approvalWindowReturn:          "Approval window active.",
		approvalWindowActive:          true,
	}
	handled, err := handleTelegramCommandCallback(context.Background(), sender, router, telegram.CallbackQuery{
		ID:      "cb-aw-enable-30m",
		From:    &telegram.User{ID: 1001},
		Data:    encodeApprovalWindowCallbackData("offer-test-30m", approvalWindowActionEnable15),
		Message: &telegram.Message{MessageID: 77, Chat: &telegram.Chat{ID: 7}},
	})
	if err != nil {
		t.Fatalf("handleTelegramCommandCallback() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if router.approvalWindowAction != approvalWindowActionEnable15 || router.approvalWindowDuration != 30*time.Minute {
		t.Fatalf("approval action/duration = %q/%s, want enable15/30m", router.approvalWindowAction, router.approvalWindowDuration)
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
	router := &stubCommandRouter{approvalWindowReturn: "Approval window canceled.", approvalWindowCanceled: true}
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

func TestApprovalWindowCloseCallbackErrorAnswersWithoutClearingControls(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := &stubCommandRouter{approvalWindowErr: errors.New("approval windows are admin only")}
	handled, err := handleTelegramCommandCallback(context.Background(), sender, router, telegram.CallbackQuery{
		ID:      "cb-aw-close-non-admin",
		From:    &telegram.User{ID: 2002},
		Data:    encodeApprovalWindowCallbackData("offer-live", approvalWindowActionClose),
		Message: &telegram.Message{MessageID: 77, Text: "Approved.", Chat: &telegram.Chat{ID: 7}},
	})
	if err != nil {
		t.Fatalf("handleTelegramCommandCallback() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if len(sender.answers) != 1 || !strings.Contains(sender.answers[0].text, "admin only") {
		t.Fatalf("answers = %#v, want admin-only callback answer", sender.answers)
	}
	if len(sender.editClear) != 0 || len(sender.editInline) != 0 {
		t.Fatalf("editClear=%#v editInline=%#v, want controls left intact on Close error", sender.editClear, sender.editInline)
	}
	if router.approvalWindowAction != approvalWindowActionClose || router.approvalWindowSenderID != 2002 {
		t.Fatalf("approval close action=%q sender=%d, want close sender 2002", router.approvalWindowAction, router.approvalWindowSenderID)
	}
}

func TestApprovalWindowEmptyOfferCloseCallbackIsStaleAndKeepsControls(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := &stubCommandRouter{}
	for name, data := range map[string]string{
		"empty_token":       encodeApprovalWindowCallbackData("", approvalWindowActionClose),
		"missing_separator": approvalWindowCallbackPrefix + approvalWindowActionClose,
	} {
		t.Run(name, func(t *testing.T) {
			sender.answers = nil
			sender.editClear = nil
			router.approvalWindowAction = ""
			router.approvalWindowOfferID = ""
			handled, err := handleTelegramCommandCallback(context.Background(), sender, router, telegram.CallbackQuery{
				ID:      "cb-aw-close-empty-" + name,
				From:    &telegram.User{ID: 1001},
				Data:    data,
				Message: &telegram.Message{MessageID: 77, Text: "Approved.", Chat: &telegram.Chat{ID: 7}},
			})
			if err != nil {
				t.Fatalf("handleTelegramCommandCallback() err = %v", err)
			}
			if !handled {
				t.Fatal("handled = false, want true")
			}
			if len(sender.answers) != 1 || sender.answers[0].text != approvalWindowCallbackStale {
				t.Fatalf("answers = %#v, want stale answer", sender.answers)
			}
			if len(sender.editClear) != 0 {
				t.Fatalf("editClear = %#v, want controls left intact", sender.editClear)
			}
			if router.approvalWindowAction != "" || router.approvalWindowOfferID != "" {
				t.Fatalf("router close action=%q offer=%q, want no authoritative close", router.approvalWindowAction, router.approvalWindowOfferID)
			}
		})
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
	if router.approvalWindowAction != approvalWindowActionClose || router.approvalWindowOfferID != "offer-test" || router.approvalWindowSenderID != 1001 {
		t.Fatalf("approval close = action:%q offer:%q sender:%d, want close/offer-test/1001", router.approvalWindowAction, router.approvalWindowOfferID, router.approvalWindowSenderID)
	}
	if len(sender.editClear) != 1 || sender.editClear[0].text != "Approved." {
		t.Fatalf("editClear = %#v, want original text without controls", sender.editClear)
	}
}
