//go:build linux

package runtime

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
)

func TestHandleInboundMaterializesPendingOperationProposalAsButtonBackedLease(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	provider.replyText = "I need approval before I cross this boundary."
	provider.faceReplyText = "Approve this lease with the buttons."
	provider.proposalReplyText = testPersonaContinuationProposal(session.ContinuationIntentDecisionHold, "")
	provider.planningReplyText = testGovernorContinuationRatification(session.ContinuationIntentDecisionHold, "Hold until explicit approval.", false)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	key := session.SessionKey{ChatID: 9011, UserID: 0, Scope: telegramDMScopeRef(9011)}
	if err := store.UpdateOperationState(key, session.OperationState{
		ID:        "button-backed-lease-test",
		Objective: "Implement a local button-backed approval path.",
		Status:    session.OperationStatusBlocked,
		Stage:     "lease_proposal",
		Proposal: session.OperationProposal{
			ID:            "button-backed-lease-local-v1",
			Kind:          "system_change",
			Summary:       "Materialize assistant-authored leases as buttons",
			WhyNow:        "Typed approvals are causing boop tax.",
			BoundedEffect: "Inspect and patch locally; stop before commit/deploy/restart.",
			Status:        session.ProposalStatusPending,
		},
	}); err != nil {
		t.Fatalf("UpdateOperationState() err = %v", err)
	}

	_, err = rt.HandleInbound(context.Background(), core.InboundMessage{ChatID: 9011, SenderID: 1001, SenderName: "admin", Text: "go get it", MessageID: 1})
	if err != nil {
		t.Fatalf("HandleInbound() err = %v", err)
	}

	sender.mu.Lock()
	defer sender.mu.Unlock()
	if len(sender.inline) != 1 {
		t.Fatalf("inline count = %d, want 1 button-backed lease prompt", len(sender.inline))
	}
	text := sender.inline[0].text
	if !strings.Contains(text, "Approval needed") || !strings.Contains(text, "Materialize assistant-authored leases as buttons") || !strings.Contains(text, "Inspect and patch locally") {
		t.Fatalf("inline text = %q, want materialized operation proposal details", text)
	}
	labels := []string{
		sender.inline[0].rows[0][0].Text, sender.inline[0].rows[0][1].Text,
		sender.inline[0].rows[1][0].Text, sender.inline[0].rows[1][1].Text,
	}
	wantLabels := []string{"Approve lease", "Continue once", "Ask edit", "Stop / park"}
	for i, want := range wantLabels {
		if labels[i] != want {
			t.Fatalf("labels = %#v, want prefix %#v", labels, wantLabels)
		}
	}
	if got := sender.inline[0].rows[0][0].CallbackData; !strings.Contains(got, "button-backed-lease-local-v1") {
		t.Fatalf("approve callback = %q, want proposal id", got)
	}

	state, err := store.ContinuationState(key)
	if err != nil {
		t.Fatalf("ContinuationState() err = %v", err)
	}
	if state.Status != session.ContinuationStatusPending || state.ActionProposal.OperationID != "button-backed-lease-local-v1" {
		t.Fatalf("state = %#v, want pending continuation tied to operation proposal", state)
	}
	if state.ActionProposal.BoundedEffect != "Inspect and patch locally; stop before commit/deploy/restart." {
		t.Fatalf("bounded effect = %q", state.ActionProposal.BoundedEffect)
	}
}

func TestApproveMaterializedOperationProposalUpdatesOperationProposalStatus(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	key := session.SessionKey{ChatID: 9012, UserID: 0, Scope: telegramDMScopeRef(9012)}
	opState := session.OperationState{Proposal: session.OperationProposal{ID: "lease-approve-sync", Summary: "Approve sync", Status: session.ProposalStatusPending}}
	if err := store.UpdateOperationState(key, opState); err != nil {
		t.Fatalf("UpdateOperationState() err = %v", err)
	}
	state := continuationStateFromOperationProposal(opState, "", time.Now().UTC())
	if err := store.UpdateContinuationState(key, state); err != nil {
		t.Fatalf("UpdateContinuationState() err = %v", err)
	}

	if _, err := rt.ApproveContinuation(9012, 1001); err != nil {
		t.Fatalf("ApproveContinuation() err = %v", err)
	}
	got, err := store.OperationState(key)
	if err != nil {
		t.Fatalf("OperationState() err = %v", err)
	}
	if got.Proposal.Status != session.ProposalStatusApproved || got.Status != session.OperationStatusActive {
		t.Fatalf("operation state = %#v, want approved/active", got)
	}
}

func TestRevokeMaterializedOperationProposalDeniesPendingOperationProposal(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	key := session.SessionKey{ChatID: 9013, UserID: 0, Scope: telegramDMScopeRef(9013)}
	opState := session.OperationState{Proposal: session.OperationProposal{ID: "lease-revoke-sync", Summary: "Revoke sync", Status: session.ProposalStatusPending}}
	if err := store.UpdateOperationState(key, opState); err != nil {
		t.Fatalf("UpdateOperationState() err = %v", err)
	}
	state := continuationStateFromOperationProposal(opState, "", time.Now().UTC())
	if err := store.UpdateContinuationState(key, state); err != nil {
		t.Fatalf("UpdateContinuationState() err = %v", err)
	}

	if _, err := rt.RevokeContinuation(9013); err != nil {
		t.Fatalf("RevokeContinuation() err = %v", err)
	}
	got, err := store.OperationState(key)
	if err != nil {
		t.Fatalf("OperationState() err = %v", err)
	}
	if got.Proposal.Status != session.ProposalStatusDenied || got.Status != session.OperationStatusBlocked {
		t.Fatalf("operation state = %#v, want denied/blocked", got)
	}
}
