//go:build linux

package telegramcommands

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/telegram"
)

func TestHandleTelegramCommandCallbackContinuationApprove(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	triggerStarted := make(chan struct{})
	router := stubCommandRouter{continuationState: session.ContinuationState{
		Status:         session.ContinuationStatusPending,
		DecisionID:     "decision-1",
		RemainingTurns: 1,
		StageSummary:   "Resume the next bounded step.",
	}, canRestart: true, triggerContinuationStarted: triggerStarted}
	handled, err := handleTelegramCommandCallback(context.Background(), sender, &router, telegram.CallbackQuery{
		ID:      "cb-continue",
		From:    &telegram.User{ID: 1002, Username: "approved"},
		Data:    encodeContinuationCallbackData("decision-1", continuationActionApproveLease),
		Message: &telegram.Message{MessageID: 93, Chat: &telegram.Chat{ID: 7, Type: "private"}},
	})
	if err != nil {
		t.Fatalf("handleTelegramCommandCallback() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if router.approveContinuationInput != 7 {
		t.Fatalf("approveContinuationInput = %d, want 7", router.approveContinuationInput)
	}
	if router.approveContinuationApprover != 1002 {
		t.Fatalf("approveContinuationApprover = %d, want 1002", router.approveContinuationApprover)
	}
	waitForStubContinuationTrigger(t, triggerStarted)
	if router.triggerContinuationInput != 7 {
		t.Fatalf("triggerContinuationInput = %d, want 7", router.triggerContinuationInput)
	}
	if router.continuationStateInput != 7 {
		t.Fatalf("continuationStateInput = %d, want 7", router.continuationStateInput)
	}
	if len(sender.edits) != 0 {
		t.Fatalf("edits count = %d, want 0 plain edits", len(sender.edits))
	}
	if len(sender.editClear) != 0 {
		t.Fatalf("editClear count = %d, want no keyboard-clearing edit", len(sender.editClear))
	}
	if len(sender.editInline) != 1 {
		t.Fatalf("editInline count = %d, want 1 approval-window offer edit", len(sender.editInline))
	}
	if !commandRowsContain(sender.editInline[0].rows, "Approve 15m", encodeApprovalWindowCallbackData("offer-test", approvalWindowActionEnable15)) {
		t.Fatalf("editInline rows = %#v, want approval-window offer", sender.editInline[0].rows)
	}
	if router.callbackRetireChatID != 7 || router.callbackRetireMessageID != 93 || router.callbackRetireSurface != continuationCallbackRetiredSurface {
		t.Fatalf("retired callback projection = chat:%d msg:%d surface:%q, want 7/93/%s", router.callbackRetireChatID, router.callbackRetireMessageID, router.callbackRetireSurface, continuationCallbackRetiredSurface)
	}
}

func TestHandleTelegramCommandCallbackContinueOnceFailsClosed(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := stubCommandRouter{continuationState: session.ContinuationState{
		Status:         session.ContinuationStatusPending,
		DecisionID:     "decision-continue-once",
		RemainingTurns: 3,
		StageSummary:   "Run a bounded multi-step lease.",
	}, canRestart: true}
	handled, err := handleTelegramCommandCallback(context.Background(), sender, &router, telegram.CallbackQuery{
		ID:      "cb-continue-once",
		From:    &telegram.User{ID: 1002, Username: "approved"},
		Data:    encodeContinuationCallbackData("decision-continue-once", continuationActionContinueOnce),
		Message: &telegram.Message{MessageID: 194, Chat: &telegram.Chat{ID: 7, Type: "private"}},
	})
	if err != nil {
		t.Fatalf("handleTelegramCommandCallback() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if router.approveContinuationInput != 0 || router.triggerContinuationInput != 0 {
		t.Fatalf("approve/trigger = %d/%d, want 0/0 for legacy continue_once", router.approveContinuationInput, router.triggerContinuationInput)
	}
	if len(sender.answers) != 1 || sender.answers[0].text != legacyContinueOnceCallbackText {
		t.Fatalf("answers = %#v, want legacy continue_once stale answer", sender.answers)
	}
	if router.callbackRetireChatID != 7 || router.callbackRetireMessageID != 194 || router.callbackRetireSurface != continuationCallbackRetiredSurface {
		t.Fatalf("retired callback projection = chat:%d msg:%d surface:%q, want 7/194/%s", router.callbackRetireChatID, router.callbackRetireMessageID, router.callbackRetireSurface, continuationCallbackRetiredSurface)
	}
}

func TestHandleTelegramCommandCallbackContinuationApproveContinuesWhenEditFails(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{editErr: errors.New("telegram editMessageText failed: message is not modified")}
	triggerStarted := make(chan struct{})
	router := stubCommandRouter{continuationState: session.ContinuationState{
		Status:         session.ContinuationStatusPending,
		DecisionID:     "decision-2",
		RemainingTurns: 1,
		StageSummary:   "Resume the next bounded step.",
	}, canRestart: true, triggerContinuationStarted: triggerStarted}
	handled, err := handleTelegramCommandCallback(context.Background(), sender, &router, telegram.CallbackQuery{
		ID:      "cb-continue-edit-fail",
		From:    &telegram.User{ID: 1002, Username: "approved"},
		Data:    encodeContinuationCallbackData("decision-2", continuationActionApproveLease),
		Message: &telegram.Message{MessageID: 193, Chat: &telegram.Chat{ID: 7, Type: "private"}},
	})
	if err != nil {
		t.Fatalf("handleTelegramCommandCallback() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	waitForStubContinuationTrigger(t, triggerStarted)
	if router.triggerContinuationInput != 7 {
		t.Fatalf("triggerContinuationInput = %d, want 7", router.triggerContinuationInput)
	}
	if len(sender.edits) != 0 {
		t.Fatalf("edits count = %d, want 0 plain edits", len(sender.edits))
	}
	if len(sender.editClear) != 0 {
		t.Fatalf("editClear count = %d, want no keyboard-clearing edit", len(sender.editClear))
	}
	if router.callbackRetireChatID != 7 || router.callbackRetireMessageID != 193 || router.callbackRetireSurface != continuationCallbackRetiredSurface {
		t.Fatalf("retired callback projection = chat:%d msg:%d surface:%q, want 7/193/%s", router.callbackRetireChatID, router.callbackRetireMessageID, router.callbackRetireSurface, continuationCallbackRetiredSurface)
	}
	if len(sender.editInline) != 1 {
		t.Fatalf("editInline count = %d, want 1 approval-window offer edit", len(sender.editInline))
	}
}

func TestHandleTelegramCommandCallbackContinuationApproveContainsExpiredLease(t *testing.T) {
	t.Parallel()

	pending := session.ContinuationState{
		Status:         session.ContinuationStatusPending,
		DecisionID:     "decision-expired",
		RemainingTurns: 1,
		StageSummary:   "Resume the expired bounded step.",
	}
	expired := pending
	expired.Status = session.ContinuationStatusIdle
	expired.RemainingTurns = 0
	expired.ActionProposal = session.ActionProposal{ID: "aprop-expired", Status: session.ProposalStatusExpired}
	expired.ContinuationLease = session.ContinuationLease{ID: "lease-expired", ProposalID: "aprop-expired", Status: session.ContinuationLeaseStatusExpired}

	sender := &stubCommandSender{}
	router := stubCommandRouter{
		canRestart:                true,
		continuationState:         pending,
		approveContinuationReturn: expired,
		approveContinuationErr:    fmt.Errorf("approve continuation: %w", core.ErrContinuationExpired),
		refreshContinuationReturn: session.ContinuationState{
			Status:         session.ContinuationStatusPending,
			DecisionID:     "decision-refreshed",
			RemainingTurns: 1,
			StageSummary:   "Resume the expired bounded step.",
		},
		refreshContinuationSent: true,
	}
	handled, err := handleTelegramCommandCallback(context.Background(), sender, &router, telegram.CallbackQuery{
		ID:      "cb-expired",
		From:    &telegram.User{ID: 1002, Username: "approved"},
		Data:    encodeContinuationCallbackData("decision-expired", continuationActionApproveLease),
		Message: &telegram.Message{MessageID: 194, Chat: &telegram.Chat{ID: 7, Type: "private"}},
	})
	if err != nil {
		t.Fatalf("handleTelegramCommandCallback() err = %v, want nil for expired continuation", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if router.approveContinuationInput != 7 || router.approveContinuationApprover != 1002 {
		t.Fatalf("approve input/approver = %d/%d, want 7/1002", router.approveContinuationInput, router.approveContinuationApprover)
	}
	if router.triggerContinuationInput != 0 {
		t.Fatalf("triggerContinuationInput = %d, want 0 after expired approval", router.triggerContinuationInput)
	}
	if router.refreshContinuationInput != 7 || !strings.Contains(router.refreshContinuationReason, "expired") {
		t.Fatalf("refresh input/reason = %d/%q, want 7 expired reason", router.refreshContinuationInput, router.refreshContinuationReason)
	}
	if len(sender.answers) != 1 || !strings.Contains(strings.ToLower(sender.answers[0].text), "fresh approval prompt") {
		t.Fatalf("answers = %#v, want fresh approval prompt callback answer", sender.answers)
	}
	if len(sender.editClear) != 1 {
		t.Fatalf("editClear = %#v, want one expired refresh message update", sender.editClear)
	}
	editText := strings.ToLower(sender.editClear[0].text)
	if !strings.Contains(editText, "expired") || !strings.Contains(editText, "fresh approval prompt") {
		t.Fatalf("editClear = %#v, want expired refresh message update", sender.editClear)
	}
	if len(router.callbackErrorRecords) != 0 {
		t.Fatalf("callbackErrorRecords = %#v, want no callback failure when expired approval refreshes successfully", router.callbackErrorRecords)
	}
}

func TestHandleTelegramCommandCallbackContinuationApproveRecordsAckErrorWithoutFailing(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{answerErr: errors.New("telegram answerCallbackQuery failed: Bad Request: chat not found")}
	triggerStarted := make(chan struct{})
	router := stubCommandRouter{continuationState: session.ContinuationState{
		Status:         session.ContinuationStatusPending,
		DecisionID:     "decision-ack-error",
		RemainingTurns: 1,
		StageSummary:   "Resume despite callback ack failure.",
	}, canRestart: true, triggerContinuationStarted: triggerStarted}
	handled, err := handleTelegramCommandCallback(context.Background(), sender, &router, telegram.CallbackQuery{
		ID:      "cb-ack-error",
		From:    &telegram.User{ID: 1002, Username: "approved"},
		Data:    encodeContinuationCallbackData("decision-ack-error", continuationActionApproveLease),
		Message: &telegram.Message{MessageID: 195, Chat: &telegram.Chat{ID: 7, Type: "private"}},
	})
	if err != nil {
		t.Fatalf("handleTelegramCommandCallback() err = %v, want nil for callback ack failure", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	waitForStubContinuationTrigger(t, triggerStarted)
	if router.triggerContinuationInput != 7 {
		t.Fatalf("triggerContinuationInput = %d, want 7", router.triggerContinuationInput)
	}
	if len(sender.answers) != 1 {
		t.Fatalf("answers count = %d, want 1", len(sender.answers))
	}
	if len(router.callbackErrorRecords) != 1 {
		t.Fatalf("callbackErrorRecords = %#v, want one ack record", router.callbackErrorRecords)
	}
	if router.callbackErrorRecords[0].chatID != 7 || router.callbackErrorRecords[0].callbackKind != "continuation.approve.answer" {
		t.Fatalf("callback error record = %#v, want continuation.approve.answer", router.callbackErrorRecords[0])
	}
}

func TestHandleTelegramCommandCallbackContinuationStopRendersCombinedStopResult(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := stubCommandRouter{
		canRestart:             true,
		continuationState:      session.ContinuationState{Status: session.ContinuationStatusPending, DecisionID: "decision-3", RemainingTurns: 1},
		stopContinuationResult: core.StopResult{ContinuationRevoked: true, ContinuationLabel: "Plan: Resource-Owner Assistant (Phase J1)"},
	}
	handled, err := handleTelegramCommandCallback(context.Background(), sender, &router, telegram.CallbackQuery{
		ID:      "cb-stop",
		From:    &telegram.User{ID: 1002, Username: "approved"},
		Data:    encodeContinuationCallbackData("decision-3", "stop"),
		Message: &telegram.Message{MessageID: 94, Chat: &telegram.Chat{ID: 7, Type: "private"}},
	})
	if err != nil {
		t.Fatalf("handleTelegramCommandCallback() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if router.stopContinuationInput != 7 {
		t.Fatalf("stopContinuationInput = %d, want 7", router.stopContinuationInput)
	}
	if len(sender.edits) != 0 {
		t.Fatalf("edits count = %d, want 0 plain edits", len(sender.edits))
	}
	if len(sender.editClear) != 1 {
		t.Fatalf("editClear count = %d, want 1", len(sender.editClear))
	}
	if sender.editClear[0].text != "Stopped Plan: Resource-Owner Assistant (Phase J1)." {
		t.Fatalf("edit text = %q, want continuation revoke text", sender.editClear[0].text)
	}
}

func TestHandleTelegramCommandCallbackContinuationStopRendersNoOpStopResult(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := stubCommandRouter{
		canRestart:             true,
		continuationState:      session.ContinuationState{Status: session.ContinuationStatusPending, DecisionID: "decision-4", RemainingTurns: 1},
		stopContinuationResult: core.StopResult{},
	}
	handled, err := handleTelegramCommandCallback(context.Background(), sender, &router, telegram.CallbackQuery{
		ID:      "cb-stop-none",
		From:    &telegram.User{ID: 1002, Username: "approved"},
		Data:    encodeContinuationCallbackData("decision-4", "stop"),
		Message: &telegram.Message{MessageID: 95, Chat: &telegram.Chat{ID: 7, Type: "private"}},
	})
	if err != nil {
		t.Fatalf("handleTelegramCommandCallback() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if len(sender.edits) != 0 {
		t.Fatalf("edits count = %d, want 0 plain edits", len(sender.edits))
	}
	if len(sender.editClear) != 1 {
		t.Fatalf("editClear count = %d, want 1", len(sender.editClear))
	}
	if sender.editClear[0].text != "Continuation approval was already inactive for this chat." {
		t.Fatalf("edit text = %q, want inactive continuation text", sender.editClear[0].text)
	}
}

func TestHandleTelegramCommandCallbackContinuationRejectsStaleDecisionID(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := stubCommandRouter{
		continuationState: session.ContinuationState{
			Status:         session.ContinuationStatusPending,
			DecisionID:     "decision-current",
			RemainingTurns: 1,
		},
	}
	handled, err := handleTelegramCommandCallback(context.Background(), sender, &router, telegram.CallbackQuery{
		ID:      "cb-stale",
		From:    &telegram.User{ID: 1002, Username: "approved"},
		Data:    encodeContinuationCallbackData("decision-old", continuationActionApproveLease),
		Message: &telegram.Message{MessageID: 196, Chat: &telegram.Chat{ID: 7, Type: "private"}},
	})
	if err != nil {
		t.Fatalf("handleTelegramCommandCallback() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if router.approveContinuationInput != 0 {
		t.Fatalf("approveContinuationInput = %d, want 0 for stale callback", router.approveContinuationInput)
	}
	if router.triggerContinuationInput != 0 {
		t.Fatalf("triggerContinuationInput = %d, want 0 for stale callback", router.triggerContinuationInput)
	}
	if len(sender.answers) != 1 {
		t.Fatalf("answers count = %d, want 1", len(sender.answers))
	}
	if sender.answers[0].text != staleContinuationCallbackText {
		t.Fatalf("answer text = %q, want stale callback warning", sender.answers[0].text)
	}
	if len(sender.edits) != 0 {
		t.Fatalf("edits count = %d, want 0 for stale callback", len(sender.edits))
	}
	if len(sender.editClear) != 1 || sender.editClear[0].messageID != 196 || sender.editClear[0].text != staleContinuationCallbackText {
		t.Fatalf("editClear = %#v, want stale continuation card retired", sender.editClear)
	}
	if router.callbackRetireChatID != 7 || router.callbackRetireMessageID != 196 || router.callbackRetireSurface != continuationCallbackRetiredSurface {
		t.Fatalf("retired callback projection = chat:%d msg:%d surface:%q, want 7/196/%s", router.callbackRetireChatID, router.callbackRetireMessageID, router.callbackRetireSurface, continuationCallbackRetiredSurface)
	}
}

func TestHandleTelegramCommandCallbackContinuationRejectsUnauthorizedActor(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := stubCommandRouter{
		continuationState: session.ContinuationState{
			Status:         session.ContinuationStatusPending,
			DecisionID:     "decision-auth",
			RemainingTurns: 1,
		},
	}
	handled, err := handleTelegramCommandCallback(context.Background(), sender, &router, telegram.CallbackQuery{
		ID:      "cb-auth",
		From:    &telegram.User{ID: 2002, Username: "member"},
		Data:    encodeContinuationCallbackData("decision-auth", continuationActionApproveLease),
		Message: &telegram.Message{MessageID: 197, Chat: &telegram.Chat{ID: 7, Type: "group"}},
	})
	if err != nil {
		t.Fatalf("handleTelegramCommandCallback() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if router.approveContinuationInput != 0 || router.triggerContinuationInput != 0 {
		t.Fatalf("approve/trigger = %d/%d, want 0/0", router.approveContinuationInput, router.triggerContinuationInput)
	}
	if len(sender.answers) != 1 || !strings.Contains(sender.answers[0].text, "admins only") {
		t.Fatalf("answers = %#v, want admin-only callback answer", sender.answers)
	}
}

func TestHandleTelegramCommandCallbackContinuationRejectsWrongPromptMessage(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := stubCommandRouter{
		canRestart: true,
		continuationState: session.ContinuationState{
			Status:            session.ContinuationStatusPending,
			DecisionID:        "decision-message-bound",
			DecisionMessageID: 198,
			RemainingTurns:    1,
		},
	}
	handled, err := handleTelegramCommandCallback(context.Background(), sender, &router, telegram.CallbackQuery{
		ID:      "cb-wrong-message",
		From:    &telegram.User{ID: 1002, Username: "admin"},
		Data:    encodeContinuationCallbackData("decision-message-bound", continuationActionApproveLease),
		Message: &telegram.Message{MessageID: 199, Chat: &telegram.Chat{ID: 7, Type: "private"}},
	})
	if err != nil {
		t.Fatalf("handleTelegramCommandCallback() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if router.approveContinuationInput != 0 || router.triggerContinuationInput != 0 {
		t.Fatalf("approve/trigger = %d/%d, want 0/0", router.approveContinuationInput, router.triggerContinuationInput)
	}
	if len(sender.answers) != 1 || sender.answers[0].text != staleContinuationCallbackText {
		t.Fatalf("answers = %#v, want stale callback answer", sender.answers)
	}
	if len(sender.editClear) != 1 || sender.editClear[0].messageID != 199 || sender.editClear[0].text != staleContinuationCallbackText {
		t.Fatalf("editClear = %#v, want wrong prompt card retired", sender.editClear)
	}
	if router.callbackRetireChatID != 7 || router.callbackRetireMessageID != 199 || router.callbackRetireSurface != continuationCallbackRetiredSurface {
		t.Fatalf("retired callback projection = chat:%d msg:%d surface:%q, want 7/199/%s", router.callbackRetireChatID, router.callbackRetireMessageID, router.callbackRetireSurface, continuationCallbackRetiredSurface)
	}
}

func TestContinuationControlsV2DecodeCurrentActions(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		raw  string
		want string
	}{
		{"approve_lease", continuationActionApproveLease},
		{"continue_once", continuationActionContinueOnce},
		{"ask-edit", continuationActionAskEdit},
		{"park", continuationActionStopPark},
		{"resume", continuationActionResumeEdge},
		{"next-lease", continuationActionAskNextLease},
		{"status-only", continuationActionStatusOnly},
	} {
		id, action, ok := decodeContinuationCallbackData(encodeContinuationCallbackData("decision-v2", tc.raw))
		if !ok || id != "decision-v2" || action != tc.want {
			t.Fatalf("decode %q = id=%q action=%q ok=%t, want decision-v2/%q/true", tc.raw, id, action, ok, tc.want)
		}
	}
}

func TestContinuationControlsRejectRemovedApprovalAliases(t *testing.T) {
	t.Parallel()

	for _, raw := range []string{"approve", "continue"} {
		data := core.EncodeContinuationCallbackData("decision-v2", raw)
		if _, _, ok := decodeContinuationCallbackData(data); ok {
			t.Fatalf("decode %q ok=true, want removed approval alias rejected", raw)
		}
	}
}

func TestContinuationCallbackCompactsLongIDsAndMatchesState(t *testing.T) {
	t.Parallel()

	longID := "button-backed-materialization-live-test-v1"
	data := encodeContinuationCallbackData(longID, continuationActionAskNextLease)
	if data == "" || len(data) > core.TelegramCallbackDataMaxBytes {
		t.Fatalf("callback data = %q len=%d, want non-empty <= %d", data, len(data), core.TelegramCallbackDataMaxBytes)
	}
	decodedID, action, ok := decodeContinuationCallbackData(data)
	if !ok || action != continuationActionAskNextLease || decodedID == longID {
		t.Fatalf("decode = id=%q action=%q ok=%t, want compact id/%q/true", decodedID, action, ok, continuationActionAskNextLease)
	}
	state := session.ContinuationState{
		Status:         session.ContinuationStatusPending,
		DecisionID:     longID,
		RemainingTurns: 1,
		ActionProposal: session.ActionProposal{ID: "aprop-" + longID},
		ContinuationLease: session.ContinuationLease{
			ID:         "lease-" + longID,
			ProposalID: "aprop-" + longID,
		},
	}
	if !continuationCallbackMatchesState(state, decodedID, action) {
		t.Fatalf("continuationCallbackMatchesState() = false for compact id %q", decodedID)
	}
}

func TestHandleTelegramCommandCallbackContinuationApproveLease(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	triggerStarted := make(chan struct{})
	router := stubCommandRouter{continuationState: session.ContinuationState{
		Status:         session.ContinuationStatusPending,
		DecisionID:     "decision-approve-lease",
		RemainingTurns: 1,
		StageSummary:   "Resume the next bounded step.",
	}, canRestart: true, triggerContinuationStarted: triggerStarted}
	handled, err := handleTelegramCommandCallback(context.Background(), sender, &router, telegram.CallbackQuery{
		ID:      "cb-approve-lease",
		From:    &telegram.User{ID: 1002, Username: "approved"},
		Data:    encodeContinuationCallbackData("decision-approve-lease", continuationActionApproveLease),
		Message: &telegram.Message{MessageID: 293, Chat: &telegram.Chat{ID: 7, Type: "private"}},
	})
	if err != nil {
		t.Fatalf("handleTelegramCommandCallback() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if router.approveContinuationInput != 7 || router.approveContinuationApprover != 1002 {
		t.Fatalf("approve input/approver = %d/%d, want 7/1002", router.approveContinuationInput, router.approveContinuationApprover)
	}
	waitForStubContinuationTrigger(t, triggerStarted)
	if router.triggerContinuationInput != 7 {
		t.Fatalf("triggerContinuationInput = %d, want 7", router.triggerContinuationInput)
	}
	if len(sender.editInline) != 1 || !strings.Contains(sender.editInline[0].text, "Approved.") {
		t.Fatalf("editInline = %#v, want approval confirmation with approval-window offer", sender.editInline)
	}
	if strings.Contains(sender.editInline[0].text, "Continuation approved") || strings.Contains(sender.editInline[0].text, "Remaining turns") {
		t.Fatalf("editInline = %#v, want humanized approval copy", sender.editInline)
	}
}

func TestHandleTelegramCommandCallbackContinuationDetailsKeepsPendingPlanButtons(t *testing.T) {
	t.Parallel()

	state := session.ContinuationState{
		Status:         session.ContinuationStatusPending,
		DecisionID:     "decision-plan-details",
		Objective:      "Finish the governed onboarding plan.",
		StageSummary:   "Approve the bounded plan budget.",
		RemainingTurns: 3,
		ActionProposal: session.ActionProposal{
			ID:             "aprop-plan-details",
			RiskClass:      "plan_lease",
			Summary:        "Approve three bounded setup steps.",
			AllowedActions: []string{"approve_operation_plan_lease"},
		},
		ContinuationLease: session.ContinuationLease{
			ID:             "lease-plan-details",
			ProposalID:     "aprop-plan-details",
			Status:         session.ContinuationLeaseStatusPending,
			AllowedActions: []string{"approve_operation_plan_lease"},
		},
	}
	sender := &stubCommandSender{}
	router := stubCommandRouter{canRestart: true, continuationState: state}
	handled, err := handleTelegramCommandCallback(context.Background(), sender, &router, telegram.CallbackQuery{
		ID:      "cb-plan-details",
		From:    &telegram.User{ID: 1002, Username: "approved"},
		Data:    encodeContinuationCallbackData("aprop-plan-details", continuationActionStatusOnly),
		Message: &telegram.Message{MessageID: 393, Chat: &telegram.Chat{ID: 7, Type: "private"}},
	})
	if err != nil {
		t.Fatalf("handleTelegramCommandCallback() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if len(sender.editClear) != 0 {
		t.Fatalf("editClear = %#v, want no keyboard-clearing edit for details", sender.editClear)
	}
	if len(sender.editInline) != 1 {
		t.Fatalf("editInline = %#v, want details edit with buttons retained", sender.editInline)
	}
	if !strings.Contains(sender.editInline[0].text, "Approved steps remaining: 3") {
		t.Fatalf("details text = %q, want expanded plan details", sender.editInline[0].text)
	}
	var labels []string
	for _, row := range sender.editInline[0].rows {
		for _, button := range row {
			labels = append(labels, button.Text)
		}
	}
	for _, want := range []string{"Start", "Details", "Change", "Pause", "Stop"} {
		if !slices.Contains(labels, want) {
			t.Fatalf("retained labels = %#v, missing %q", labels, want)
		}
	}
}

func TestHandleTelegramCommandCallbackContinuationApproveDoesNotWaitForTrigger(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	triggerStarted := make(chan struct{})
	triggerRelease := make(chan struct{})
	defer close(triggerRelease)
	router := stubCommandRouter{continuationState: session.ContinuationState{
		Status:         session.ContinuationStatusPending,
		DecisionID:     "decision-blocked-trigger",
		RemainingTurns: 1,
		StageSummary:   "Run a bounded continuation.",
	}, canRestart: true, triggerContinuationStarted: triggerStarted, triggerContinuationRelease: triggerRelease}
	handled, err := handleTelegramCommandCallback(context.Background(), sender, &router, telegram.CallbackQuery{
		ID:      "cb-blocked-trigger",
		From:    &telegram.User{ID: 1002, Username: "approved"},
		Data:    encodeContinuationCallbackData("decision-blocked-trigger", continuationActionApproveLease),
		Message: &telegram.Message{MessageID: 294, Chat: &telegram.Chat{ID: 7, Type: "private"}},
	})
	if err != nil {
		t.Fatalf("handleTelegramCommandCallback() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if len(sender.editInline) != 1 || !strings.Contains(sender.editInline[0].text, "Approved.") {
		t.Fatalf("editInline = %#v, want immediate approval confirmation with approval-window offer", sender.editInline)
	}
	if strings.Contains(sender.editInline[0].text, "Continuation approved") || strings.Contains(sender.editInline[0].text, "Remaining turns") {
		t.Fatalf("editInline = %#v, want humanized approval copy", sender.editInline)
	}
	waitForStubContinuationTrigger(t, triggerStarted)
	if router.triggerContinuationInput != 7 {
		t.Fatalf("triggerContinuationInput = %d, want 7", router.triggerContinuationInput)
	}
}

func TestHandleTelegramCommandCallbackContinuationStatusOnlyDoesNotMutateOrTrigger(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := stubCommandRouter{canRestart: true, continuationState: session.ContinuationState{
		Status:         session.ContinuationStatusPending,
		DecisionID:     "decision-status",
		RemainingTurns: 1,
		Objective:      "Keep the edge visible.",
		StageSummary:   "Report status only.",
		ActionProposal: session.ActionProposal{
			Summary:          "Inspect the proposed scope",
			BoundedEffect:    "Inspect local state and report only.",
			AllowedActions:   []string{"inspect_readonly_state"},
			ForbiddenActions: []string{"edit_files", "deploy"},
			ValidationPlan:   []string{"report evidence"},
		},
	}}
	handled, err := handleTelegramCommandCallback(context.Background(), sender, &router, telegram.CallbackQuery{
		ID:      "cb-status-only",
		From:    &telegram.User{ID: 1002, Username: "approved"},
		Data:    encodeContinuationCallbackData("decision-status", continuationActionStatusOnly),
		Message: &telegram.Message{MessageID: 294, Chat: &telegram.Chat{ID: 7, Type: "private"}},
	})
	if err != nil {
		t.Fatalf("handleTelegramCommandCallback() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if router.approveContinuationInput != 0 || router.triggerContinuationInput != 0 || router.stopContinuationInput != 0 {
		t.Fatalf("router mutated approve/trigger/stop = %d/%d/%d, want 0/0/0", router.approveContinuationInput, router.triggerContinuationInput, router.stopContinuationInput)
	}
	if len(sender.editClear) != 0 {
		t.Fatalf("editClear = %#v, want status-only details to retain buttons", sender.editClear)
	}
	if len(sender.editInline) != 1 {
		t.Fatalf("editInline = %#v, want status-only no-authority text with buttons", sender.editInline)
	}
	if !strings.Contains(sender.editInline[0].text, "Scope details") ||
		!strings.Contains(sender.editInline[0].text, "Scope: Inspect local state and report only.") ||
		!strings.Contains(sender.editInline[0].text, "Stops before: edit_files, deploy") {
		t.Fatalf("editInline = %#v, want detailed continuation scope text", sender.editInline)
	}
	var labels []string
	for _, row := range sender.editInline[0].rows {
		for _, button := range row {
			labels = append(labels, button.Text)
		}
	}
	for _, want := range []string{"Start", "Details", "Change", "Pause", "Stop"} {
		if !slices.Contains(labels, want) {
			t.Fatalf("retained labels = %#v, missing %q", labels, want)
		}
	}
}

func TestHandleTelegramCommandCallbackContinuationAskNextLeaseRefreshesProposal(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := stubCommandRouter{
		canRestart: true,
		continuationState: session.ContinuationState{
			Status:         session.ContinuationStatusIdle,
			DecisionID:     "decision-expired-refresh",
			RemainingTurns: 0,
			ActionProposal: session.ActionProposal{ID: "aprop-expired-refresh", Status: session.ProposalStatusExpired},
			ContinuationLease: session.ContinuationLease{
				ID:         "lease-expired-refresh",
				ProposalID: "aprop-expired-refresh",
				Status:     session.ContinuationLeaseStatusExpired,
			},
		},
		refreshContinuationReturn: session.ContinuationState{
			Status:         session.ContinuationStatusPending,
			DecisionID:     "decision-refreshed",
			RemainingTurns: 1,
			StageSummary:   "Use the fresh approval prompt.",
		},
		refreshContinuationSent: true,
	}
	handled, err := handleTelegramCommandCallback(context.Background(), sender, &router, telegram.CallbackQuery{
		ID:      "cb-next-lease",
		From:    &telegram.User{ID: 1002, Username: "approved"},
		Data:    encodeContinuationCallbackData("aprop-expired-refresh", continuationActionAskNextLease),
		Message: &telegram.Message{MessageID: 296, Chat: &telegram.Chat{ID: 7, Type: "private"}},
	})
	if err != nil {
		t.Fatalf("handleTelegramCommandCallback() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if router.refreshContinuationInput != 7 || !strings.Contains(router.refreshContinuationReason, "requested") {
		t.Fatalf("refresh input/reason = %d/%q, want requested refresh", router.refreshContinuationInput, router.refreshContinuationReason)
	}
	if router.approveContinuationInput != 0 || router.triggerContinuationInput != 0 || router.stopContinuationInput != 0 {
		t.Fatalf("router approve/trigger/stop = %d/%d/%d, want 0/0/0", router.approveContinuationInput, router.triggerContinuationInput, router.stopContinuationInput)
	}
	if len(sender.editClear) != 1 || !strings.Contains(sender.editClear[0].text, "fresh approval prompt") {
		t.Fatalf("editClear = %#v, want refreshed prompt status", sender.editClear)
	}
}

func TestHandleTelegramCommandCallbackContinuationAskEditParksWithoutTrigger(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := stubCommandRouter{
		canRestart:             true,
		continuationState:      session.ContinuationState{Status: session.ContinuationStatusPending, DecisionID: "decision-edit", RemainingTurns: 1},
		stopContinuationResult: core.StopResult{ContinuationRevoked: true},
	}
	handled, err := handleTelegramCommandCallback(context.Background(), sender, &router, telegram.CallbackQuery{
		ID:      "cb-ask-edit",
		From:    &telegram.User{ID: 1002, Username: "approved"},
		Data:    encodeContinuationCallbackData("decision-edit", continuationActionAskEdit),
		Message: &telegram.Message{MessageID: 295, Chat: &telegram.Chat{ID: 7, Type: "private"}},
	})
	if err != nil {
		t.Fatalf("handleTelegramCommandCallback() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if router.stopContinuationInput != 7 {
		t.Fatalf("stopContinuationInput = %d, want 7", router.stopContinuationInput)
	}
	if router.triggerContinuationInput != 0 || router.approveContinuationInput != 0 {
		t.Fatalf("trigger/approve = %d/%d, want 0/0", router.triggerContinuationInput, router.approveContinuationInput)
	}
	if len(sender.editClear) != 1 || !strings.Contains(sender.editClear[0].text, "parked this request") {
		t.Fatalf("editClear = %#v, want ask-edit confirmation", sender.editClear)
	}
}

func TestCompactContinuationStopsForDeployLeaseDoNotSelfForbidDeployRestart(t *testing.T) {
	t.Parallel()

	state := session.ContinuationState{
		ActionProposal: session.ActionProposal{
			RiskClass: "deploy",
			AllowedActions: []string{
				"make_build",
				"install_user_service",
				"restart_aphelion_service",
				"run_verify_deploy",
			},
			ForbiddenActions: []string{
				"deploy_without_handoff",
				"restart_without_recovery_artifact",
				"skip_post_deploy_verification",
				"credentials_or_tokens",
			},
		},
		ContinuationLease: session.ContinuationLease{LeaseClass: session.ContinuationLeaseClassDeployRestart},
	}
	stops := compactContinuationStops(state)
	joined := strings.Join(stops, ", ")
	if strings.Contains(joined, "deploy/restart") {
		t.Fatalf("stops = %#v, did not want broad deploy/restart stop for deploy lease", stops)
	}
	for _, want := range []string{"release without handoff", "restart without recovery artifact", "credentials/tokens"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("stops = %#v, want %q", stops, want)
		}
	}
}

func TestCompactContinuationStopsForReadOnlyLeaseStillStopBeforeDeployRestart(t *testing.T) {
	t.Parallel()

	state := session.ContinuationState{
		ActionProposal: session.ActionProposal{
			RiskClass:        "read_only_review",
			AllowedActions:   []string{"read_only"},
			ForbiddenActions: []string{"deploy_restart_without_explicit_approval", "credentials_or_tokens"},
		},
	}
	stops := compactContinuationStops(state)
	joined := strings.Join(stops, ", ")
	if !strings.Contains(joined, "deploy/restart") {
		t.Fatalf("stops = %#v, want broad deploy/restart stop for non-deploy lease", stops)
	}
}

func TestHandleTelegramCommandCallbackApprovalBundleCurrentCopyAndSelection(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	triggerStarted := make(chan struct{})
	state := session.ContinuationState{
		Status:         session.ContinuationStatusPending,
		DecisionID:     "bundle-decision-current",
		RemainingTurns: 1,
		StageSummary:   "Run the current sealed phase.",
		ActionProposal: session.ActionProposal{RiskClass: "approval_bundle"},
		ApprovalBundle: session.ContinuationApprovalBundle{
			ID:             "bundle-current",
			Status:         session.ContinuationLeaseStatusPending,
			CurrentPhaseID: "token-current",
			Phases: []session.ContinuationApprovalBundlePhase{
				{ID: "token-current", OperationPhaseID: "phase-1", Status: session.ContinuationLeaseStatusPending},
				{ID: "token-later", OperationPhaseID: "phase-2", Status: session.ContinuationLeaseStatusPending},
			},
		},
	}
	router := stubCommandRouter{continuationState: state, approveContinuationReturn: state, canRestart: true, triggerContinuationStarted: triggerStarted}
	handled, err := handleTelegramCommandCallback(context.Background(), sender, &router, telegram.CallbackQuery{
		ID:      "cb-bundle-current",
		From:    &telegram.User{ID: 1002, Username: "approved"},
		Data:    encodeContinuationCallbackData("bundle-decision-current", continuationActionApproveBundleCurrent),
		Message: &telegram.Message{MessageID: 302, Chat: &telegram.Chat{ID: 7, Type: "private"}},
	})
	if err != nil {
		t.Fatalf("handleTelegramCommandCallback() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if got, want := router.approveContinuationPhaseIDs, []string{"token-current"}; !slices.Equal(got, want) {
		t.Fatalf("approveContinuationPhaseIDs = %#v, want %#v", got, want)
	}
	waitForStubContinuationTrigger(t, triggerStarted)
	if len(sender.editInline) != 1 || !strings.Contains(sender.editInline[0].text, "Later steps will ask again") {
		t.Fatalf("editInline = %#v, want current-only deferred copy", sender.editInline)
	}
}

func TestHandleTelegramCommandCallbackApprovalBundleStatusDetailsExplainTokens(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	state := session.ContinuationState{
		Status:         session.ContinuationStatusPending,
		DecisionID:     "bundle-decision-details",
		RemainingTurns: 1,
		ActionProposal: session.ActionProposal{
			RiskClass:        "approval_bundle",
			BoundedEffect:    "Run bounded phase work only.",
			AllowedActions:   []string{"edit_docs"},
			ForbiddenActions: []string{"deploy"},
		},
		ApprovalBundle: session.ContinuationApprovalBundle{
			ID:             "bundle-details",
			Status:         session.ContinuationLeaseStatusPending,
			CurrentPhaseID: "token-current",
			Phases:         []session.ContinuationApprovalBundlePhase{{ID: "token-current", OperationPhaseID: "phase-1", Status: session.ContinuationLeaseStatusPending}},
		},
	}
	router := stubCommandRouter{continuationState: state, canRestart: true}
	handled, err := handleTelegramCommandCallback(context.Background(), sender, &router, telegram.CallbackQuery{
		ID:      "cb-bundle-details",
		From:    &telegram.User{ID: 1002, Username: "approved"},
		Data:    encodeContinuationCallbackData("bundle-decision-details", continuationActionStatusOnly),
		Message: &telegram.Message{MessageID: 303, Chat: &telegram.Chat{ID: 7, Type: "private"}},
	})
	if err != nil {
		t.Fatalf("handleTelegramCommandCallback() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if len(sender.editInline) != 1 {
		t.Fatalf("editInline = %#v, want details with buttons", sender.editInline)
	}
	text := sender.editInline[0].text
	for _, want := range []string{"Grouped approval", "Approve all", "Approve current", "old buttons cannot approve"} {
		if !strings.Contains(text, want) {
			t.Fatalf("details text = %q, missing %q", text, want)
		}
	}
	for _, want := range []string{"Approve all", "Approve current", "Details", "Change", "Pause", "Stop"} {
		if !buttonRowsContainLabel(sender.editInline[0].rows, want) {
			t.Fatalf("rows = %#v, missing %q", sender.editInline[0].rows, want)
		}
	}
}

func buttonRowsContainLabel(rows [][]telegram.InlineButton, label string) bool {
	for _, row := range rows {
		for _, button := range row {
			if button.Text == label {
				return true
			}
		}
	}
	return false
}
