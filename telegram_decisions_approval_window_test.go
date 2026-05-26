//go:build linux

package main

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/telegram"
	toolpkg "github.com/idolum-ai/aphelion/tool"
)

func TestTelegramExecApproverAddsApprovalWindowOfferToApprovedProposal(t *testing.T) {
	t.Parallel()

	store, err := session.NewSQLiteStore(filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	sender := &decisionTestSender{}
	broker := newTelegramDecisionBroker(sender)
	handler := newTelegramDecisionHandler(sender, &decisionTestRouter{}, broker, store)
	approver := newTelegramExecApprover(sender, broker, execApprovalWindowOfferer{store: store})
	approver.SetTimeout(time.Second)

	resultCh := make(chan toolpkg.ExecApprovalDecision, 1)
	errCh := make(chan error, 1)
	go func() {
		decisionResult, err := approver.ConfirmExec(context.Background(), toolpkg.ExecApprovalRequest{
			Principal:  principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 42},
			SessionKey: session.SessionKey{ChatID: 7, Scope: session.ScopeRef{Kind: session.ScopeKindTelegramDM, ID: "7"}},
			Command:    "pwd",
			Reason:     "outside workspace",
			Proposal: session.OperationProposal{
				Kind:          "possible_workspace_escape",
				Summary:       "Run command outside the configured workspace",
				WhyNow:        "Need to inspect live state.",
				BoundedEffect: "Run this command once.",
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
		ID:   "cb-approve-offer",
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
	if !hasInlineButton(approvalEdit.rows, "Approve 15m") || !hasInlineButton(approvalEdit.rows, "Close") {
		t.Fatalf("approval rows = %#v, want approval-window offer controls", approvalEdit.rows)
	}
	if !hasInlineButton(approvalEdit.rows, "Expand details") {
		t.Fatalf("approval rows = %#v, want existing expand details control preserved", approvalEdit.rows)
	}

	expandData := callbackDataForButton(t, approvalEdit.rows, "Expand details")
	if err := handler.HandleCallbackQuery(context.Background(), telegram.CallbackQuery{
		ID:   "cb-expand-approved-offer",
		Data: expandData,
		From: &telegram.User{ID: 42},
		Message: &telegram.Message{
			MessageID: approvalEdit.messageID,
			Chat:      &telegram.Chat{ID: approvalEdit.chatID},
		},
	}); err != nil {
		t.Fatalf("HandleCallbackQuery(expand) err = %v", err)
	}
	expanded := waitForDecisionEdit(t, sender, 2)
	if !hasInlineButton(expanded.rows, "Approve 15m") || !hasInlineButton(expanded.rows, "Close") || !hasInlineButton(expanded.rows, "Hide details") {
		t.Fatalf("expanded rows = %#v, want offer controls preserved with hide details", expanded.rows)
	}
}

func TestTelegramExecApproverSendsApprovalWindowFallbackWhenEditFails(t *testing.T) {
	t.Parallel()

	sender := &decisionTestSender{editInlineErr: errors.New("telegram edit failed")}
	broker := newTelegramDecisionBroker(sender)
	handler := newTelegramDecisionHandler(sender, &decisionTestRouter{}, broker, nil)
	approver := newTelegramExecApprover(sender, broker, execApprovalWindowOfferer{})
	approver.SetTimeout(time.Second)

	resultCh := make(chan toolpkg.ExecApprovalDecision, 1)
	errCh := make(chan error, 1)
	go func() {
		decisionResult, err := approver.ConfirmExec(context.Background(), toolpkg.ExecApprovalRequest{
			Principal:  principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 42},
			SessionKey: session.SessionKey{ChatID: 7, Scope: session.ScopeRef{Kind: session.ScopeKindTelegramDM, ID: "7"}},
			Command:    "pwd",
			Reason:     "outside workspace",
			Proposal: session.OperationProposal{
				Kind:          "possible_workspace_escape",
				Summary:       "Run command outside the configured workspace",
				WhyNow:        "Need to inspect live state.",
				BoundedEffect: "Run this command once.",
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
		ID:   "cb-approve-edit-fallback",
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

	if len(sender.inline) != 2 {
		t.Fatalf("inline = %#v, want prompt plus fallback control card", sender.inline)
	}
	fallback := sender.inline[1]
	if fallback.replyTo == nil || *fallback.replyTo != 1 {
		t.Fatalf("fallback replyTo = %#v, want reply to original approval card 1", fallback.replyTo)
	}
	if !hasInlineButton(fallback.rows, "Approve 15m") || !hasInlineButton(fallback.rows, "Close") {
		t.Fatalf("fallback rows = %#v, want approval-window offer controls", fallback.rows)
	}
}

func TestTelegramProposalPromptShowsApprovalWindowOfferAndThreadPrefix(t *testing.T) {
	t.Parallel()

	store, err := session.NewSQLiteStore(filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	thread, _, err := store.CreateTelegramThreadForUpdate(7, 42, 701, 901, "threaded approval work", time.Now().UTC())
	if err != nil {
		t.Fatalf("CreateTelegramThreadForUpdate() err = %v", err)
	}

	sender := &decisionTestSender{}
	offerer := execApprovalWindowOfferer{store: store}
	broker := newTelegramDecisionBrokerWithSummary(sender, nil, telegramDecisionBrokerUIOptions{
		ApprovalWindows: offerer,
		ThreadResolver:  store,
		ThreadRecorder:  store,
	})
	handler := newTelegramDecisionHandler(sender, &decisionTestRouter{}, broker, store)
	approver := newTelegramExecApprover(sender, broker, offerer)
	approver.SetPresentation(store)
	approver.SetTimeout(time.Second)

	resultCh := make(chan toolpkg.ExecApprovalDecision, 1)
	errCh := make(chan error, 1)
	go func() {
		decisionResult, err := approver.ConfirmExec(context.Background(), toolpkg.ExecApprovalRequest{
			Principal: principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 42},
			SessionKey: session.SessionKey{
				ChatID: 7,
				Scope:  session.TelegramThreadScopeRef(7, thread.ThreadID),
			},
			Command: "pwd",
			Reason:  "outside workspace",
			Proposal: session.OperationProposal{
				Kind:          "possible_workspace_escape",
				Summary:       "Run command outside the configured workspace",
				WhyNow:        "Need to inspect live state.",
				BoundedEffect: "Run this command once.",
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
	if !strings.HasPrefix(prompt.text, "(thread 1)\n\n") {
		t.Fatalf("prompt text = %q, want visible thread prefix", prompt.text)
	}
	if !hasInlineButton(prompt.rows, "Approve 15m") {
		t.Fatalf("prompt rows = %#v, want approval-window enable control on initial approval card", prompt.rows)
	}
	if hasInlineButton(prompt.rows, "Close") {
		t.Fatalf("prompt rows = %#v, want no close control that can strand the pending approval card", prompt.rows)
	}
	if got, ok, err := store.TelegramThreadIDForReplyMessage(7, 1); err != nil || !ok || got != thread.ThreadID {
		t.Fatalf("TelegramThreadIDForReplyMessage() = %d/%v/%v, want thread %d", got, ok, err, thread.ThreadID)
	}

	expandData := callbackDataForButton(t, prompt.rows, "Expand details")
	if err := handler.HandleCallbackQuery(context.Background(), telegram.CallbackQuery{
		ID:   "cb-expand-pending-thread-offer",
		Data: expandData,
		From: &telegram.User{ID: 42},
		Message: &telegram.Message{
			MessageID: 1,
			Chat:      &telegram.Chat{ID: 7},
		},
	}); err != nil {
		t.Fatalf("HandleCallbackQuery(expand) err = %v", err)
	}
	expanded := waitForDecisionEdit(t, sender, 1)
	if !strings.HasPrefix(expanded.text, "(thread 1)\n\n") {
		t.Fatalf("expanded text = %q, want visible thread prefix", expanded.text)
	}
	if !hasInlineButton(expanded.rows, "Approve 15m") {
		t.Fatalf("expanded rows = %#v, want approval-window enable control preserved", expanded.rows)
	}
	if hasInlineButton(expanded.rows, "Close") {
		t.Fatalf("expanded rows = %#v, want no close control that can strand the pending approval card", expanded.rows)
	}

	broker.Resolve("1", "deny")
	select {
	case err := <-errCh:
		t.Fatalf("ConfirmExec() err = %v", err)
	case decisionResult := <-resultCh:
		if decisionResult.Approved {
			t.Fatal("Approved = true, want denied cleanup")
		}
	case <-time.After(time.Second):
		t.Fatal("ConfirmExec() did not resolve after deny")
	}
}
