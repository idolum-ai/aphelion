//go:build linux

package main

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/decision"
	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/telegram"
	toolpkg "github.com/idolum-ai/aphelion/tool"
)

func TestProposalApprovalSummaryIsOutcomeFirst(t *testing.T) {
	t.Parallel()

	details := strings.Join([]string{
		"Create a local git commit",
		"Kind: repo_history_mutation",
		"",
		"Why now:",
		"Saving this work as a commit gives us a clean review and rollback point before continuing.",
		"",
		"If approved:",
		"Create one local git commit. This approval will not push to any remote.",
		"",
		"Trigger:",
		"repository commit",
		"",
		"Command:",
		"git commit -m 'Document external channel runtime substrate'",
	}, "\n")

	text := approvedDecisionConfirmationText("Proposal", "3", decision.KindProposalApproval, details)
	for _, unwanted := range []string{"Approved content:", "Kind:", "Trigger:"} {
		if strings.Contains(text, unwanted) {
			t.Fatalf("approval text = %q, should not contain noisy metadata %q", text, unwanted)
		}
	}
	for _, wanted := range []string{"Approved — I’ll commit: `Document external channel runtime substrate`."} {
		if !strings.Contains(text, wanted) {
			t.Fatalf("approval text = %q, want %q", text, wanted)
		}
	}
	for _, hidden := range []string{"Decision:", "Why:", "Will do:", "Details hidden", "git" + " commit -m"} {
		if strings.Contains(text, hidden) {
			t.Fatalf("approval text = %q, should keep %q behind Expand details", text, hidden)
		}
	}
}

func TestTelegramExecApproverKeepsApprovalConfirmation(t *testing.T) {
	t.Parallel()

	sender := &decisionTestSender{}
	var broker *decision.Broker
	broker = decision.NewBroker(func(ctx context.Context, pending decision.PendingDecision) (decision.Delivery, error) {
		text := renderPendingDecisionSummary(pending)
		msgID, err := sender.SendInlineKeyboard(ctx, pending.ChatID, text, inlineButtonRows(pending), replyToMessageID(pending.MessageID))
		if err != nil {
			return decision.Delivery{}, err
		}
		go broker.Resolve(pending.ID, "approve")
		return decision.Delivery{MessageID: msgID}, nil
	})
	approver := newTelegramExecApprover(sender, broker)

	decisionResult, err := approver.ConfirmExec(context.Background(), toolpkg.ExecApprovalRequest{
		Principal:  principal.Principal{Role: principal.RoleAdmin},
		SessionKey: session.SessionKey{ChatID: 7},
		Command:    "rm -rf build",
		Reason:     "recursive delete",
		Proposal: session.OperationProposal{
			Kind:          "possible_delete_command",
			Summary:       "Approve command with possible delete pattern",
			WhyNow:        "This command text matched a pattern that may delete local state.",
			BoundedEffect: "Approving allows this command once. It does not approve unrelated edits, deploys, restarts, or account actions.",
			Status:        session.ProposalStatusPending,
		},
	})
	if err != nil {
		t.Fatalf("ConfirmExec() err = %v", err)
	}
	if !decisionResult.Approved {
		t.Fatal("Approved = false, want true")
	}
	if len(sender.deletes) != 0 {
		t.Fatalf("deletes = %#v, want no prompt delete on approval", sender.deletes)
	}
	if len(sender.edits) != 1 {
		t.Fatalf("edits = %#v, want durable approval confirmation", sender.edits)
	}
	if !strings.Contains(sender.edits[0].text, "Approved — Command needs confirmation:") || !strings.Contains(sender.edits[0].text, "possible delete pattern") || strings.Contains(sender.edits[0].text, "Decision:") {
		t.Fatalf("approval edit = %q, want compact proposal confirmation", sender.edits[0].text)
	}
	if !hasInlineButton(sender.edits[0].rows, "Expand details") {
		t.Fatalf("approval rows = %#v, want retained expand details button", sender.edits[0].rows)
	}
	if len(sender.inline) != 1 {
		t.Fatalf("inline = %#v, want one proposal prompt", sender.inline)
	}
	if !strings.Contains(sender.inline[0].text, "possible delete pattern") {
		t.Fatalf("inline text = %q, want proposal summary", sender.inline[0].text)
	}
	if len(sender.inline[0].rows) == 0 {
		t.Fatalf("rows = %#v, want button rows", sender.inline[0].rows)
	}
	choiceRow := sender.inline[0].rows[len(sender.inline[0].rows)-1]
	if len(choiceRow) != 2 {
		t.Fatalf("choice row = %#v, want exactly 2 buttons", choiceRow)
	}
	if choiceRow[0].Text != "Deny" || choiceRow[1].Text != "Approve" {
		t.Fatalf("choice order = %#v, want [Deny, Approve]", choiceRow)
	}
}

func TestRestartLoadedProposalApprovalCallbackIsStale(t *testing.T) {
	store, err := session.NewSQLiteStore(filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	choicesJSON, err := json.Marshal([]decision.Choice{{ID: "deny", Label: "Deny"}, {ID: "approve", Label: "Approve"}})
	if err != nil {
		t.Fatalf("Marshal() err = %v", err)
	}
	if err := store.UpsertPendingDecision(session.PendingDecisionRecord{
		ID:                "restart-approval",
		Sequence:          10,
		OwnerKey:          "session:telegram_dm:7:sender:42",
		SessionID:         "telegram_dm:7",
		ScopeKind:         string(session.ScopeKindTelegramDM),
		ScopeID:           "7",
		Kind:              string(decision.KindProposalApproval),
		ChatID:            7,
		SenderID:          42,
		MessageID:         99,
		Prompt:            "Approve this proposal?",
		Details:           "Command: git commit -m test",
		ChoicesJSON:       string(choicesJSON),
		DefaultChoice:     "deny",
		TimeoutNanos:      int64(time.Hour),
		DeliveryMessageID: 7003,
	}); err != nil {
		t.Fatalf("UpsertPendingDecision() err = %v", err)
	}

	sender := &decisionTestSender{}
	broker := newTelegramDecisionBroker(sender, decision.WithDurableStore(newTelegramDecisionDurableStore(store)))
	if err := broker.Load(context.Background()); err != nil {
		t.Fatalf("Load() err = %v", err)
	}
	handler := newTelegramDecisionHandler(sender, &decisionTestRouter{}, broker, store)

	if err := handler.HandleCallbackQuery(context.Background(), telegram.CallbackQuery{
		ID:   "cb-stale-approval",
		Data: decision.EncodeCallbackData("restart-approval", "approve"),
		From: &telegram.User{ID: 42},
		Message: &telegram.Message{
			MessageID: 7003,
			Chat:      &telegram.Chat{ID: 7, Type: "private"},
		},
	}); err != nil {
		t.Fatalf("HandleCallbackQuery() err = %v", err)
	}
	if len(sender.answers) != 1 || !strings.Contains(sender.answers[0].text, "no longer active") {
		t.Fatalf("answers = %#v, want stale approval acknowledgement", sender.answers)
	}
	if len(sender.edits) != 1 || !strings.Contains(sender.edits[0].text, "no longer active") {
		t.Fatalf("edits = %#v, want stale approval message edit", sender.edits)
	}
	if _, ok := broker.Peek("restart-approval"); ok {
		t.Fatal("Peek(restart-approval) = true after stale restart-loaded approval")
	}
	records, err := store.PendingDecisions()
	if err != nil {
		t.Fatalf("PendingDecisions() err = %v", err)
	}
	for _, record := range records {
		if record.ID == "restart-approval" {
			t.Fatal("restart-loaded non-resumable approval remained pending")
		}
	}
}

func TestRestartReconciliationDetachesNonResumableProposalApproval(t *testing.T) {
	store, err := session.NewSQLiteStore(filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	choicesJSON, err := json.Marshal([]decision.Choice{{ID: "deny", Label: "Deny"}, {ID: "approve", Label: "Approve"}})
	if err != nil {
		t.Fatalf("Marshal() err = %v", err)
	}
	if err := store.UpsertPendingDecision(session.PendingDecisionRecord{
		ID:                "restart-approval",
		Sequence:          10,
		OwnerKey:          "session:telegram_dm:7:sender:42",
		SessionID:         "telegram_dm:7",
		ScopeKind:         string(session.ScopeKindTelegramDM),
		ScopeID:           "7",
		Kind:              string(decision.KindProposalApproval),
		ChatID:            7,
		SenderID:          42,
		MessageID:         99,
		Prompt:            "Approve this proposal?",
		Details:           "Command: git commit -m test",
		ChoicesJSON:       string(choicesJSON),
		DefaultChoice:     "deny",
		TimeoutNanos:      int64(time.Hour),
		DeliveryMessageID: 7003,
	}); err != nil {
		t.Fatalf("UpsertPendingDecision() err = %v", err)
	}

	sender := &decisionTestSender{}
	broker := newTelegramDecisionBroker(sender, decision.WithDurableStore(newTelegramDecisionDurableStore(store)))
	if err := broker.Load(context.Background()); err != nil {
		t.Fatalf("Load() err = %v", err)
	}
	handler := newTelegramDecisionHandler(sender, &decisionTestRouter{}, broker, store)
	if err := handler.ReconcileRestartLoadedDecisions(context.Background()); err != nil {
		t.Fatalf("ReconcileRestartLoadedDecisions() err = %v", err)
	}
	if _, ok := broker.Peek("restart-approval"); ok {
		t.Fatal("non-resumable restart-loaded proposal approval remained active after reconciliation")
	}
	records, err := store.PendingDecisions()
	if err != nil {
		t.Fatalf("PendingDecisions() err = %v", err)
	}
	for _, record := range records {
		if record.ID == "restart-approval" {
			t.Fatal("non-resumable restart-loaded proposal approval remained durable after reconciliation")
		}
	}
}

func TestTelegramExecApprovalConfirmationExpandShowsCommandAfterApproval(t *testing.T) {
	t.Parallel()

	sender := &decisionTestSender{}
	broker := newTelegramDecisionBroker(sender)
	handler := newTelegramDecisionHandler(sender, &decisionTestRouter{}, broker, nil)
	approver := newTelegramExecApprover(sender, broker)
	approver.SetTimeout(time.Second)

	resultCh := make(chan toolpkg.ExecApprovalDecision, 1)
	errCh := make(chan error, 1)
	go func() {
		decisionResult, err := approver.ConfirmExec(context.Background(), toolpkg.ExecApprovalRequest{
			Principal:  principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 42},
			SessionKey: session.SessionKey{ChatID: 7},
			Command:    "rm -rf /tmp/aphelion-runtime-bin",
			Reason:     "recursive delete",
			Proposal: session.OperationProposal{
				Kind:          "possible_delete_command",
				Summary:       "Approve command with possible delete pattern",
				WhyNow:        "This command text matched a pattern that may delete local state.",
				BoundedEffect: "Approving allows this command once. It does not approve unrelated edits, deploys, restarts, or account actions.",
				Status:        session.ProposalStatusPending,
			},
		})
		if err != nil {
			errCh <- err
			return
		}
		resultCh <- decisionResult
	}()

	prompt := waitForDecisionInline(t, sender)
	approveData := callbackDataForButton(t, prompt.rows, "Approve")
	if err := handler.HandleCallbackQuery(context.Background(), telegram.CallbackQuery{
		ID:   "cb-approve",
		Data: approveData,
		From: &telegram.User{ID: 42},
		Message: &telegram.Message{
			MessageID: 1,
			Chat:      &telegram.Chat{ID: 7},
		},
	}); err != nil {
		t.Fatalf("HandleCallbackQuery(approve) err = %v", err)
	}

	select {
	case err := <-errCh:
		t.Fatalf("ConfirmExec() err = %v", err)
	case decisionResult := <-resultCh:
		if !decisionResult.Approved {
			t.Fatal("Approved = false, want true")
		}
	case <-time.After(time.Second):
		t.Fatal("ConfirmExec() did not resolve after approve callback")
	}

	approvalEdit := waitForDecisionEdit(t, sender, 1)
	expandData := callbackDataForButton(t, approvalEdit.rows, "Expand details")
	if expandData == "" {
		t.Fatalf("approval rows = %#v, want expand details callback", approvalEdit.rows)
	}
	if err := handler.HandleCallbackQuery(context.Background(), telegram.CallbackQuery{
		ID:   "cb-expand-approved",
		Data: expandData,
		From: &telegram.User{ID: 42},
		Message: &telegram.Message{
			MessageID: approvalEdit.messageID,
			Chat:      &telegram.Chat{ID: approvalEdit.chatID},
		},
	}); err != nil {
		t.Fatalf("HandleCallbackQuery(expand after approval) err = %v", err)
	}

	expanded := waitForDecisionEdit(t, sender, 2)
	if !strings.Contains(expanded.text, "Command:") || !strings.Contains(expanded.text, "rm -rf /tmp/aphelion-runtime-bin") {
		t.Fatalf("expanded text = %q, want full approved command", expanded.text)
	}
	if !hasInlineButton(expanded.rows, "Hide details") || hasInlineButton(expanded.rows, "Expand details") {
		t.Fatalf("expanded rows = %#v, want hide details button replacing expand", expanded.rows)
	}

	hideData := callbackDataForButton(t, expanded.rows, "Hide details")
	if err := handler.HandleCallbackQuery(context.Background(), telegram.CallbackQuery{
		ID:   "cb-hide-approved",
		Data: hideData,
		From: &telegram.User{ID: 42},
		Message: &telegram.Message{
			MessageID: expanded.messageID,
			Chat:      &telegram.Chat{ID: expanded.chatID},
		},
	}); err != nil {
		t.Fatalf("HandleCallbackQuery(hide after approval) err = %v", err)
	}

	collapsed := waitForDecisionEdit(t, sender, 3)
	if !strings.Contains(collapsed.text, "Approved — Command needs confirmation:") || strings.Contains(collapsed.text, "rm -rf /tmp/aphelion-runtime-bin") || strings.Contains(collapsed.text, "Decision:") {
		t.Fatalf("collapsed approved text = %q, want compact approval summary without raw command", collapsed.text)
	}
	if !hasInlineButton(collapsed.rows, "Expand details") || hasInlineButton(collapsed.rows, "Hide details") {
		t.Fatalf("collapsed rows = %#v, want expand details button restored", collapsed.rows)
	}
}

func TestTelegramExecApprovalExpandKeepsPendingDecisionButtons(t *testing.T) {
	t.Parallel()

	sender := &decisionTestSender{}
	broker := newTelegramDecisionBroker(sender)
	handler := newTelegramDecisionHandler(sender, &decisionTestRouter{}, broker, nil)
	approver := newTelegramExecApprover(sender, broker)
	approver.SetTimeout(time.Second)

	resultCh := make(chan toolpkg.ExecApprovalDecision, 1)
	errCh := make(chan error, 1)
	go func() {
		decisionResult, err := approver.ConfirmExec(context.Background(), toolpkg.ExecApprovalRequest{
			Principal:  principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 42},
			SessionKey: session.SessionKey{ChatID: 7},
			Command:    "rm -rf /tmp/aphelion-runtime-bin",
			Reason:     "recursive delete",
			Proposal: session.OperationProposal{
				Kind:          "possible_delete_command",
				Summary:       "Approve command with possible delete pattern",
				WhyNow:        "This command text matched a pattern that may delete local state.",
				BoundedEffect: "Approving allows this command once. It does not approve unrelated edits, deploys, restarts, or account actions.",
				Status:        session.ProposalStatusPending,
			},
		})
		if err != nil {
			errCh <- err
			return
		}
		resultCh <- decisionResult
	}()

	prompt := waitForDecisionInline(t, sender)
	expandData := callbackDataForButton(t, prompt.rows, "Expand details")
	if err := handler.HandleCallbackQuery(context.Background(), telegram.CallbackQuery{
		ID:   "cb-expand-pending",
		Data: expandData,
		From: &telegram.User{ID: 42},
		Message: &telegram.Message{
			MessageID: 1,
			Chat:      &telegram.Chat{ID: 7},
		},
	}); err != nil {
		t.Fatalf("HandleCallbackQuery(expand pending) err = %v", err)
	}

	expanded := waitForDecisionEdit(t, sender, 1)
	if !strings.Contains(expanded.text, "Command:") || !strings.Contains(expanded.text, "rm -rf /tmp/aphelion-runtime-bin") {
		t.Fatalf("expanded text = %q, want full pending command", expanded.text)
	}
	if !hasInlineButton(expanded.rows, "Deny") || !hasInlineButton(expanded.rows, "Approve") {
		t.Fatalf("expanded rows = %#v, want pending decision buttons", expanded.rows)
	}
	if !hasInlineButton(expanded.rows, "Hide details") || hasInlineButton(expanded.rows, "Expand details") {
		t.Fatalf("expanded rows = %#v, want hide details button replacing expand", expanded.rows)
	}

	hideData := callbackDataForButton(t, expanded.rows, "Hide details")
	if err := handler.HandleCallbackQuery(context.Background(), telegram.CallbackQuery{
		ID:   "cb-hide-pending",
		Data: hideData,
		From: &telegram.User{ID: 42},
		Message: &telegram.Message{
			MessageID: expanded.messageID,
			Chat:      &telegram.Chat{ID: expanded.chatID},
		},
	}); err != nil {
		t.Fatalf("HandleCallbackQuery(hide pending) err = %v", err)
	}

	collapsed := waitForDecisionEdit(t, sender, 2)
	if strings.Contains(collapsed.text, "Command:") || strings.Contains(collapsed.text, "rm -rf /tmp/aphelion-runtime-bin") {
		t.Fatalf("collapsed text = %q, want compact pending summary without raw command", collapsed.text)
	}
	if !hasInlineButton(collapsed.rows, "Deny") || !hasInlineButton(collapsed.rows, "Approve") {
		t.Fatalf("collapsed rows = %#v, want pending decision buttons", collapsed.rows)
	}
	if !hasInlineButton(collapsed.rows, "Expand details") || hasInlineButton(collapsed.rows, "Hide details") {
		t.Fatalf("collapsed rows = %#v, want expand details button restored", collapsed.rows)
	}

	approveData := callbackDataForButton(t, collapsed.rows, "Approve")
	if err := handler.HandleCallbackQuery(context.Background(), telegram.CallbackQuery{
		ID:   "cb-approve-expanded",
		Data: approveData,
		From: &telegram.User{ID: 42},
		Message: &telegram.Message{
			MessageID: collapsed.messageID,
			Chat:      &telegram.Chat{ID: collapsed.chatID},
		},
	}); err != nil {
		t.Fatalf("HandleCallbackQuery(approve after hide) err = %v", err)
	}

	select {
	case err := <-errCh:
		t.Fatalf("ConfirmExec() err = %v", err)
	case decisionResult := <-resultCh:
		if !decisionResult.Approved {
			t.Fatal("Approved = false, want true")
		}
	case <-time.After(time.Second):
		t.Fatal("ConfirmExec() did not resolve after expanded approve callback")
	}
}

func TestTelegramDecisionCallbackRequiresOriginalActorAndMessage(t *testing.T) {
	t.Parallel()

	sender := &decisionTestSender{}
	broker := newTelegramDecisionBroker(sender)
	handler := newTelegramDecisionHandler(sender, &decisionTestRouter{}, broker, nil)
	resolved := make(chan string, 1)
	errCh := make(chan error, 1)
	go func() {
		result, err := broker.Request(context.Background(), decision.Request{
			Kind:          decision.KindProposalApproval,
			ChatID:        7,
			SenderID:      42,
			Prompt:        "Approve this proposal?",
			Choices:       []decision.Choice{{ID: "deny", Label: "Deny"}, {ID: "approve", Label: "Approve"}},
			DefaultChoice: "deny",
			Timeout:       time.Second,
		})
		if err != nil {
			errCh <- err
			return
		}
		resolved <- result.Choice
	}()

	prompt := waitForDecisionInline(t, sender)
	approveData := callbackDataForButton(t, prompt.rows, "Approve")
	for _, cb := range []telegram.CallbackQuery{
		{
			ID:   "cb-wrong-user",
			Data: approveData,
			From: &telegram.User{ID: 99},
			Message: &telegram.Message{
				MessageID: 1,
				Chat:      &telegram.Chat{ID: 7},
			},
		},
		{
			ID:   "cb-wrong-message",
			Data: approveData,
			From: &telegram.User{ID: 42},
			Message: &telegram.Message{
				MessageID: 2,
				Chat:      &telegram.Chat{ID: 7},
			},
		},
	} {
		if err := handler.HandleCallbackQuery(context.Background(), cb); err != nil {
			t.Fatalf("HandleCallbackQuery(%s) err = %v", cb.ID, err)
		}
		select {
		case choice := <-resolved:
			t.Fatalf("unauthorized callback resolved choice %q", choice)
		case err := <-errCh:
			t.Fatalf("broker.Request() err = %v", err)
		default:
		}
	}

	if err := handler.HandleCallbackQuery(context.Background(), telegram.CallbackQuery{
		ID:   "cb-correct",
		Data: approveData,
		From: &telegram.User{ID: 42},
		Message: &telegram.Message{
			MessageID: 1,
			Chat:      &telegram.Chat{ID: 7},
		},
	}); err != nil {
		t.Fatalf("HandleCallbackQuery(correct) err = %v", err)
	}
	select {
	case choice := <-resolved:
		if choice != "approve" {
			t.Fatalf("choice = %q, want approve", choice)
		}
	case err := <-errCh:
		t.Fatalf("broker.Request() err = %v", err)
	case <-time.After(time.Second):
		t.Fatal("correct callback did not resolve decision")
	}
}

func TestTelegramExecApproverTimesOutToDeny(t *testing.T) {
	t.Parallel()

	sender := &decisionTestSender{}
	broker := newTelegramDecisionBroker(sender)
	approver := newTelegramExecApprover(sender, broker)
	approver.SetTimeout(10 * time.Millisecond)

	decisionResult, err := approver.ConfirmExec(context.Background(), toolpkg.ExecApprovalRequest{
		Principal:  principal.Principal{Role: principal.RoleAdmin},
		SessionKey: session.SessionKey{ChatID: 7},
		Command:    "pip install playwright",
		Reason:     "dependency installation",
		Proposal: session.OperationProposal{
			Kind:          "capability_acquisition",
			Summary:       "Acquire browser automation",
			WhyNow:        "A screenshot requires browser automation in this operation.",
			BoundedEffect: "Install Playwright locally and capture one screenshot.",
			Status:        session.ProposalStatusPending,
		},
	})
	if err != nil {
		t.Fatalf("ConfirmExec() err = %v", err)
	}
	if decisionResult.Approved {
		t.Fatal("Approved = true, want false")
	}
	if len(sender.edits) != 1 {
		t.Fatalf("edits = %#v, want one blocked edit", sender.edits)
	}
	if len(sender.inline) != 1 {
		t.Fatalf("inline = %#v, want one proposal prompt", sender.inline)
	}
	if !strings.Contains(sender.inline[0].text, "I’d like to acquire browser automation.") {
		t.Fatalf("inline text = %q, want intent-first capability proposal summary", sender.inline[0].text)
	}
}
