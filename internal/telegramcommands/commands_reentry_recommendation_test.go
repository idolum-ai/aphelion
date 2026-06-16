//go:build linux

package telegramcommands

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/telegram"
)

func TestHandleTelegramCommandCallbackReentryRecommendationSelectQueuesScopedWork(t *testing.T) {
	t.Parallel()

	record := testReentryRecommendationRecord()
	candidate := record.Candidates[0]
	router := &stubCommandRouter{
		canRestart:                  true,
		reentryRecommendation:       record,
		reentryRecommendationOK:     true,
		queueReentrySelected:        true,
		queueReentryCandidateReturn: candidate,
	}
	sender := &stubCommandSender{}
	handled, err := handleTelegramCommandCallback(context.Background(), sender, router, telegram.CallbackQuery{
		ID:       "cb-reentry",
		UpdateID: 8081,
		From:     &telegram.User{ID: 1001, Username: "admin"},
		Data:     core.EncodeReentryRecommendationCallbackData(record.ID, candidate.ID, core.ReentryRecommendationCallbackSelect),
		Message:  &telegram.Message{MessageID: 93, Chat: &telegram.Chat{ID: 7, Type: "private"}},
	})
	if err != nil {
		t.Fatalf("handleTelegramCommandCallback() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if router.queueReentryRecommendationID != record.ID || router.queueReentryCandidateID != candidate.ID {
		t.Fatalf("queued reentry = %q/%q, want %q/%q", router.queueReentryRecommendationID, router.queueReentryCandidateID, record.ID, candidate.ID)
	}
	if router.queueReentryRecommendationMsg == nil {
		t.Fatal("queueReentryRecommendationMsg = nil, want queued synthetic turn")
	}
	if got := router.queueReentryRecommendationMsg.IngressSurface; got != telegramReentryRecommendationIngressSurface {
		t.Fatalf("IngressSurface = %q, want %q", got, telegramReentryRecommendationIngressSurface)
	}
	if router.queueReentryRecommendationMsg.IngressUpdateID != 8081 {
		t.Fatalf("IngressUpdateID = %d, want callback update", router.queueReentryRecommendationMsg.IngressUpdateID)
	}
	if !strings.Contains(router.queueReentryRecommendationMsg.Text, "If action is needed, ask before doing it.") {
		t.Fatalf("queued text = %q, want ask-before-action warning", router.queueReentryRecommendationMsg.Text)
	}
	for _, forbidden := range []string{"grant authority", "consumed lease", "fresh bounded approval"} {
		if strings.Contains(router.queueReentryRecommendationMsg.Text, forbidden) {
			t.Fatalf("queued text leaked internal copy %q: %q", forbidden, router.queueReentryRecommendationMsg.Text)
		}
	}
	if len(sender.answers) != 1 || sender.answers[0].text != "Queued." {
		t.Fatalf("answers = %#v, want queued acknowledgement", sender.answers)
	}
	if len(sender.editClear) != 1 || !strings.Contains(sender.editClear[0].text, "Queued re-entry path") {
		t.Fatalf("editClear = %#v, want queued keyboard-clearing edit", sender.editClear)
	}
}

func TestHandleTelegramCommandCallbackReentryRecommendationIgnoreDoesNotQueueWork(t *testing.T) {
	t.Parallel()

	record := testReentryRecommendationRecord()
	router := &stubCommandRouter{
		canRestart:              true,
		reentryRecommendation:   record,
		reentryRecommendationOK: true,
	}
	sender := &stubCommandSender{}
	handled, err := handleTelegramCommandCallback(context.Background(), sender, router, telegram.CallbackQuery{
		ID:      "cb-reentry-ignore",
		From:    &telegram.User{ID: 1001, Username: "admin"},
		Data:    core.EncodeReentryRecommendationCallbackData(record.ID, "", core.ReentryRecommendationCallbackIgnore),
		Message: &telegram.Message{MessageID: 93, Chat: &telegram.Chat{ID: 7, Type: "private"}},
	})
	if err != nil {
		t.Fatalf("handleTelegramCommandCallback() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if router.ignoreReentryRecommendationID != record.ID {
		t.Fatalf("ignoreReentryRecommendationID = %q, want %q", router.ignoreReentryRecommendationID, record.ID)
	}
	if router.queueReentryRecommendationMsg != nil {
		t.Fatalf("queueReentryRecommendationMsg = %#v, want no queued work", router.queueReentryRecommendationMsg)
	}
	if len(sender.answers) != 1 || sender.answers[0].text != "Ignored." {
		t.Fatalf("answers = %#v, want ignored acknowledgement", sender.answers)
	}
	if len(sender.editClear) != 1 || sender.editClear[0].text != "Ignored recommendation." {
		t.Fatalf("editClear = %#v, want ignored edit", sender.editClear)
	}
}

func TestHandleTelegramCommandCallbackReentryRecommendationSelectRecordsEditFailureAfterQueue(t *testing.T) {
	t.Parallel()

	record := testReentryRecommendationRecord()
	candidate := record.Candidates[0]
	router := &stubCommandRouter{
		canRestart:                  true,
		reentryRecommendation:       record,
		reentryRecommendationOK:     true,
		queueReentrySelected:        true,
		queueReentryCandidateReturn: candidate,
	}
	editErr := errors.New("telegram edit failed")
	sender := &stubCommandSender{editErr: editErr}
	handled, err := handleTelegramCommandCallback(context.Background(), sender, router, telegram.CallbackQuery{
		ID:       "cb-reentry-edit-fail",
		UpdateID: 8082,
		From:     &telegram.User{ID: 1001, Username: "admin"},
		Data:     core.EncodeReentryRecommendationCallbackData(record.ID, candidate.ID, core.ReentryRecommendationCallbackSelect),
		Message:  &telegram.Message{MessageID: 93, Chat: &telegram.Chat{ID: 7, Type: "private"}},
	})
	if err != nil {
		t.Fatalf("handleTelegramCommandCallback() err = %v, want edit failure recorded only", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if router.queueReentryRecommendationMsg == nil {
		t.Fatal("queueReentryRecommendationMsg = nil, want queued work despite edit failure")
	}
	if len(router.callbackErrorRecords) != 1 {
		t.Fatalf("callbackErrorRecords = %#v, want one edit diagnostic", router.callbackErrorRecords)
	}
	recorded := router.callbackErrorRecords[0]
	if recorded.callbackKind != "reentry_recommendation.select.edit" || recorded.err != editErr {
		t.Fatalf("callbackErrorRecords[0] = %#v, want select edit error", recorded)
	}
}

func TestReentryRecommendationSelectionPromptIncludesTypedProvenance(t *testing.T) {
	t.Parallel()

	candidate := session.ReentryRecommendationCandidate{
		Label:            "Review current operation",
		PromptText:       "The operator selected this suggested path. This suggestion only chose a path. If action is needed, ask before doing it.",
		SourceKind:       "operation_state",
		SourceRef:        "op-release",
		EvidenceRefs:     []string{"ev-turn", "ev-op"},
		JudgmentReason:   "Current operation has the strongest durable evidence.",
		RequiresApproval: true,
	}

	prompt := reentryRecommendationSelectionPrompt(session.ReentryRecommendation{ID: "reentry-test"}, candidate)
	for _, want := range []string{
		"Candidate source: operation_state op-release",
		"Evidence refs: ev-turn, ev-op",
		"Judgment reason: Current operation has the strongest durable evidence.",
		"This suggestion only chose a path",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt = %q, want %q", prompt, want)
		}
	}
}

func testReentryRecommendationRecord() session.ReentryRecommendation {
	return session.ReentryRecommendation{
		ID:        "reentry-test",
		Owner:     "telegram:7",
		ChatID:    7,
		SenderID:  1001,
		SessionID: "telegram:7:0",
		Scope:     session.ScopeRef{Kind: session.ScopeKindTelegramDM, ID: "7"},
		Status:    session.ReentryRecommendationStatusShown,
		Candidates: []session.ReentryRecommendationCandidate{
			{
				ID:               "c1",
				Kind:             session.ReentryCandidateRequestNextLease,
				Label:            "Next step",
				Summary:          "Open a bounded follow-up.",
				PromptText:       "The operator selected this suggested path. This suggestion only chose a path. If action is needed, ask before doing it.",
				AuthorityClass:   "commit",
				RequiresApproval: true,
			},
		},
	}
}
