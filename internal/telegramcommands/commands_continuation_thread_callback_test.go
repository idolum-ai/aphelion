//go:build linux

package telegramcommands

import (
	"context"
	"strings"
	"testing"

	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/telegram"
)

func TestHandleTelegramCommandCallbackContinuationApproveTargetsThreadPrompt(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	triggerStarted := make(chan struct{})
	router := stubCommandRouter{continuationState: session.ContinuationState{
		Status:         session.ContinuationStatusPending,
		DecisionID:     "decision-thread",
		RemainingTurns: 1,
		StageSummary:   "Resume the thread step.",
	}, canRestart: true, triggerContinuationStarted: triggerStarted, threadReplyOK: true, threadReplyReturn: session.TelegramThread{ChatID: 7, ThreadID: 3, Status: session.TelegramThreadStatusOpen}}
	handled, err := handleTelegramCommandCallback(context.Background(), sender, &router, telegram.CallbackQuery{
		ID:      "cb-thread-continue",
		From:    &telegram.User{ID: 1002, Username: "approved"},
		Data:    encodeContinuationCallbackData("decision-thread", continuationActionApproveLease),
		Message: &telegram.Message{MessageID: 93, Text: "Continue this bounded thread?", Chat: &telegram.Chat{ID: 7, Type: "private"}},
	})
	if err != nil {
		t.Fatalf("handleTelegramCommandCallback() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if router.threadReplyChatID != 7 || router.threadReplyMessageID != 93 {
		t.Fatalf("thread reply lookup = chat:%d message:%d, want 7/93", router.threadReplyChatID, router.threadReplyMessageID)
	}
	if router.continuationStateMessage.TelegramThreadID != 3 {
		t.Fatalf("continuationStateMessage.TelegramThreadID = %d, want 3", router.continuationStateMessage.TelegramThreadID)
	}
	if router.approveContinuationMessage.TelegramThreadID != 3 {
		t.Fatalf("approveContinuationMessage.TelegramThreadID = %d, want 3", router.approveContinuationMessage.TelegramThreadID)
	}
	if router.approveContinuationInput != 0 {
		t.Fatalf("approveContinuationInput = %d, want unscoped path unused", router.approveContinuationInput)
	}
	waitForStubContinuationTrigger(t, triggerStarted)
	if router.triggerContinuationMessage.TelegramThreadID != 3 {
		t.Fatalf("triggerContinuationMessage.TelegramThreadID = %d, want 3", router.triggerContinuationMessage.TelegramThreadID)
	}
	if router.triggerContinuationInput != 0 {
		t.Fatalf("triggerContinuationInput = %d, want unscoped path unused", router.triggerContinuationInput)
	}
	if len(sender.editClear) != 1 {
		t.Fatalf("editClear count = %d, want 1", len(sender.editClear))
	}
	if !strings.HasPrefix(sender.editClear[0].text, "(thread 3)\n\n") {
		t.Fatalf("edit text = %q, want thread prefix preserved", sender.editClear[0].text)
	}
}

func TestHandleTelegramCommandCallbackContinuationDoesNotTrustThreadPrefixWithoutLedger(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	triggerStarted := make(chan struct{})
	router := stubCommandRouter{continuationState: session.ContinuationState{
		Status:         session.ContinuationStatusPending,
		DecisionID:     "decision-main",
		RemainingTurns: 1,
		StageSummary:   "Resume the main step.",
	}, canRestart: true, triggerContinuationStarted: triggerStarted}
	handled, err := handleTelegramCommandCallback(context.Background(), sender, &router, telegram.CallbackQuery{
		ID:      "cb-prefix-only",
		From:    &telegram.User{ID: 1002, Username: "approved"},
		Data:    encodeContinuationCallbackData("decision-main", continuationActionApproveLease),
		Message: &telegram.Message{MessageID: 94, Text: "(thread 3)\n\nContinue this bounded thread?", Chat: &telegram.Chat{ID: 7, Type: "private"}},
	})
	if err != nil {
		t.Fatalf("handleTelegramCommandCallback() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if router.threadReplyChatID != 7 || router.threadReplyMessageID != 94 {
		t.Fatalf("thread reply lookup = chat:%d message:%d, want 7/94", router.threadReplyChatID, router.threadReplyMessageID)
	}
	if router.continuationStateMessage.TelegramThreadID != 0 || router.approveContinuationMessage.TelegramThreadID != 0 {
		t.Fatalf("scoped continuation messages = state:%#v approve:%#v, want no thread from presentation text", router.continuationStateMessage, router.approveContinuationMessage)
	}
	if router.approveContinuationInput != 7 {
		t.Fatalf("approveContinuationInput = %d, want main chat path", router.approveContinuationInput)
	}
	waitForStubContinuationTrigger(t, triggerStarted)
	if router.triggerContinuationInput != 7 || router.triggerContinuationMessage.TelegramThreadID != 0 {
		t.Fatalf("trigger = chat:%d msg:%#v, want main chat path", router.triggerContinuationInput, router.triggerContinuationMessage)
	}
}
