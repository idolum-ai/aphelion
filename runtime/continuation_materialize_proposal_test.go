//go:build linux

package runtime

import (
	"context"
	"database/sql"
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
	if !strings.Contains(text, "Approve:\nMaterialize assistant-authored leases as buttons") || !strings.Contains(text, "Inspect and patch locally") {
		t.Fatalf("inline text = %q, want materialized operation proposal details", text)
	}
	labels := []string{
		sender.inline[0].rows[0][0].Text, sender.inline[0].rows[0][1].Text,
		sender.inline[0].rows[1][0].Text, sender.inline[0].rows[1][1].Text,
	}
	wantLabels := []string{"Start", "Details", "Change", "Pause"}
	for i, want := range wantLabels {
		if labels[i] != want {
			t.Fatalf("labels = %#v, want prefix %#v", labels, wantLabels)
		}
	}
	if got := sender.inline[0].rows[0][0].CallbackData; got == "" || len(got) > core.TelegramCallbackDataMaxBytes {
		t.Fatalf("approve callback = %q len=%d, want non-empty <= %d", got, len(got), core.TelegramCallbackDataMaxBytes)
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

func TestMaterializePendingOperationProposalFailsClosedOnUnreadableContinuationState(t *testing.T) {
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

	key := session.SessionKey{ChatID: 9012, UserID: 0, Scope: telegramDMScopeRef(9012)}
	if err := store.UpdateOperationState(key, session.OperationState{
		ID:        "fail-closed-continuation-read",
		Objective: "Do not materialize approval prompts when current approval state is unreadable.",
		Status:    session.OperationStatusBlocked,
		Proposal: session.OperationProposal{
			ID:            "proposal-fail-closed",
			Kind:          "system_change",
			Summary:       "Fail closed on unreadable approval state",
			WhyNow:        "Duplicate approval prompts are unsafe when the existing state cannot be read.",
			BoundedEffect: "Inspect local state and stop before commit/deploy/restart.",
			Status:        session.ProposalStatusPending,
		},
	}); err != nil {
		t.Fatalf("UpdateOperationState() err = %v", err)
	}
	if err := store.UpdateContinuationState(key, session.ContinuationState{Status: session.ContinuationStatusPending, DecisionID: "broken-current-state", RemainingTurns: 1}); err != nil {
		t.Fatalf("UpdateContinuationState() err = %v", err)
	}
	db, err := sql.Open("sqlite3", store.DBPath())
	if err != nil {
		t.Fatalf("sql.Open() err = %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(`UPDATE sessions SET continuation_state_json = ? WHERE session_id = ?`, "{", session.SessionIDForKey(key)); err != nil {
		t.Fatalf("corrupt continuation state: %v", err)
	}

	if _, exists, err := store.ContinuationStateIfExists(key); err == nil || !exists || !strings.Contains(err.Error(), "decode continuation state") {
		t.Fatalf("ContinuationStateIfExists() exists=%v err=%v, want decode error before materialization", exists, err)
	}
	materialized, err := rt.materializePendingOperationProposalApproval(context.Background(), key, core.InboundMessage{ChatID: 9012, SenderID: 1001, SenderName: "admin", Text: "go get it", MessageID: 1}, "go get it", nil)
	if err == nil || !strings.Contains(err.Error(), "read prior continuation state") {
		t.Fatalf("materializePendingOperationProposalApproval() materialized=%v err=%v, want fail-closed continuation read error", materialized, err)
	}

	sender.mu.Lock()
	defer sender.mu.Unlock()
	if len(sender.inline) != 0 {
		t.Fatalf("inline count = %d, want no approval prompt when continuation state is unreadable", len(sender.inline))
	}
}

func TestMaterializeOperationProposalShowsDataAccessLeaseClassCard(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	key := session.SessionKey{ChatID: 9031, UserID: 0, Scope: telegramDMScopeRef(9031)}
	if err := store.UpdateOperationState(key, session.OperationState{
		ID:        "data-access-card",
		Objective: "Inspect generated image artifact through governed data access.",
		Status:    session.OperationStatusBlocked,
		Proposal: session.OperationProposal{
			ID:            "data-access-image-read",
			Kind:          "data_access",
			Summary:       "Read one generated image artifact",
			WhyNow:        "The model can analyze the image only if the artifact is routed as data.",
			BoundedEffect: "Read artifact://image2/field-of-attention.png once; no retention or broad filesystem scan.",
			Status:        session.ProposalStatusPending,
		},
	}); err != nil {
		t.Fatalf("UpdateOperationState() err = %v", err)
	}

	materialized, err := rt.materializePendingOperationProposalApproval(context.Background(), key, core.InboundMessage{ChatID: 9031, SenderID: 1001, Text: "continue", MessageID: 1}, "continue", nil)
	if err != nil {
		t.Fatalf("materializePendingOperationProposalApproval() err = %v", err)
	}
	if !materialized {
		t.Fatal("materialized = false, want data-access lease card")
	}
	cont, err := store.ContinuationState(key)
	if err != nil {
		t.Fatalf("ContinuationState() err = %v", err)
	}
	if cont.ContinuationLease.LeaseClass != session.ContinuationLeaseClassDataAccess {
		t.Fatalf("lease class = %q, want data_access", cont.ContinuationLease.LeaseClass)
	}
	if !actionListContains(cont.ActionProposal.AllowedActions, "read_approved_resource") || !actionListContains(cont.ActionProposal.ForbiddenActions, "silent_data_ingestion") {
		t.Fatalf("proposal actions allowed=%#v forbidden=%#v, want data-access boundaries", cont.ActionProposal.AllowedActions, cont.ActionProposal.ForbiddenActions)
	}
	if cont.ContinuationLease.Constraints["resource"] == "" || cont.ContinuationLease.Constraints["retention"] == "" {
		t.Fatalf("lease constraints = %#v, want data-access constraints", cont.ContinuationLease.Constraints)
	}

	sender.mu.Lock()
	inlineText := ""
	if len(sender.inline) > 0 {
		inlineText = sender.inline[0].text
	}
	sender.mu.Unlock()
	for _, want := range []string{"Approve:\nRead one generated image artifact", "Read artifact://image2/field-of-attention.png once"} {
		if !strings.Contains(inlineText, want) {
			t.Fatalf("inline text = %q, want %q", inlineText, want)
		}
	}
	for _, notWant := range []string{"Operator card:", "Constraint: resource=", "Use the buttons"} {
		if strings.Contains(inlineText, notWant) {
			t.Fatalf("inline text = %q, did not want verbose contract fragment %q", inlineText, notWant)
		}
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

func TestMaterializePendingOperationProposalAfterTurnAuthorization(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	key := session.SessionKey{ChatID: 9014, UserID: 0, Scope: telegramDMScopeRef(9014)}
	if err := store.UpdateOperationState(key, session.OperationState{
		ID:        "post-continuation-next-lease",
		Objective: "Continue a broader goal after one approved turn.",
		Status:    session.OperationStatusBlocked,
		Proposal: session.OperationProposal{
			ID:            "post-continuation-next-lease-v1",
			Kind:          "read_only_review",
			Summary:       "Plan the next safe phase",
			WhyNow:        "The approved turn completed only phase one.",
			BoundedEffect: "Review only and report one next proposal.",
			Status:        session.ProposalStatusPending,
		},
	}); err != nil {
		t.Fatalf("UpdateOperationState() err = %v", err)
	}

	materialized, err := rt.materializePendingOperationProposalApproval(context.Background(), key, core.InboundMessage{
		ChatID:       9014,
		SenderID:     1001,
		Text:         approvedContinuationEventText,
		Origin:       core.InboundOriginTurnAuthorization,
		OriginDetail: string(session.TurnAuthorizationKindContinuation),
	}, approvedContinuationEventText, nil)
	if err != nil {
		t.Fatalf("materializePendingOperationProposalApproval() err = %v", err)
	}
	if !materialized {
		t.Fatal("materialized = false, want post-authorization proposal buttons")
	}
	sender.mu.Lock()
	inlineCount := len(sender.inline)
	sender.mu.Unlock()
	if inlineCount != 1 {
		t.Fatalf("inline count = %d, want 1", inlineCount)
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
