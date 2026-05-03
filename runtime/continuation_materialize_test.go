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
	wantLabels := []string{"Approve & run", "Scope details", "Revise proposal", "Park"}
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

func TestMaterializeDurablePhasePlanUsesNextPendingPhase(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	key := session.SessionKey{ChatID: 9015, UserID: 0, Scope: telegramDMScopeRef(9015)}
	if err := store.UpdateOperationState(key, session.OperationState{
		ID:        "phase-plan-op",
		Objective: "Deliver Lighthouse inbox workflow.",
		Status:    session.OperationStatusBlocked,
		Stage:     "phase_plan",
		Proposal: session.OperationProposal{
			ID:      "stale-single-step",
			Summary: "Do the whole thing in one step",
			Status:  session.ProposalStatusPending,
		},
		PhasePlan: session.OperationPhasePlan{
			ID:   "phase-plan",
			Goal: "Deliver Lighthouse inbox workflow.",
			Phases: []session.OperationPhase{
				{
					ID:               "phase-1-contract",
					Summary:          "Write the read-only contract",
					Status:           session.PlanStatusCompleted,
					AuthorityClass:   "read_only_review",
					BoundedEffect:    "Inspect only and write the contract.",
					RequiresApproval: true,
				},
				{
					ID:               "phase-2-implementation",
					Summary:          "Implement the local inbox bridge",
					Status:           session.PlanStatusPending,
					AuthorityClass:   "workspace_write",
					WhyNow:           "The contract phase is complete.",
					BoundedEffect:    "Edit local files and run tests; stop before deploy.",
					AllowedActions:   []string{"edit_files", "run_tests"},
					ForbiddenActions: []string{"deploy", "restart_service"},
				},
			},
		},
	}); err != nil {
		t.Fatalf("UpdateOperationState() err = %v", err)
	}

	materialized, err := rt.materializePendingOperationProposalApproval(context.Background(), key, core.InboundMessage{ChatID: 9015, SenderID: 1001, Text: "continue", MessageID: 1}, "continue", nil)
	if err != nil {
		t.Fatalf("materializePendingOperationProposalApproval() err = %v", err)
	}
	if !materialized {
		t.Fatal("materialized = false, want phase-plan approval")
	}

	cont, err := store.ContinuationState(key)
	if err != nil {
		t.Fatalf("ContinuationState() err = %v", err)
	}
	if cont.Status != session.ContinuationStatusPending || cont.ContinuationLease.Status != session.ContinuationLeaseStatusPending {
		t.Fatalf("continuation = %#v, want pending lease", cont)
	}
	if cont.ActionProposal.Summary != "Implement the local inbox bridge" || cont.ActionProposal.RiskClass != "workspace_write" {
		t.Fatalf("action proposal = %#v, want next pending phase action", cont.ActionProposal)
	}
	if cont.ActionProposal.BoundedEffect != "Edit local files and run tests; stop before deploy." {
		t.Fatalf("bounded effect = %q", cont.ActionProposal.BoundedEffect)
	}
	if cont.ContinuationLease.MaxTurns != 1 || cont.ContinuationLease.RemainingTurns != 1 {
		t.Fatalf("lease = %#v, want one-turn phase lease", cont.ContinuationLease)
	}

	opState, err := store.OperationState(key)
	if err != nil {
		t.Fatalf("OperationState() err = %v", err)
	}
	if opState.Proposal.Status != session.ProposalStatusPending || opState.Proposal.Summary != "Implement the local inbox bridge" {
		t.Fatalf("operation proposal = %#v, want synthetic pending phase proposal", opState.Proposal)
	}
	if opState.PhasePlan.CurrentPhaseID != "phase-2-implementation" || opState.PhasePlan.Phases[1].LeaseID != cont.ContinuationLease.ID {
		t.Fatalf("phase plan = %#v, want current phase linked to lease", opState.PhasePlan)
	}

	sender.mu.Lock()
	inlineText := ""
	var labels []string
	if len(sender.inline) > 0 {
		inlineText = sender.inline[0].text
		labels = continuationButtonLabels(sender.inline[0].rows)
	}
	sender.mu.Unlock()
	if !strings.Contains(inlineText, "Implement the local inbox bridge") || strings.Contains(inlineText, "Do the whole thing in one step") {
		t.Fatalf("inline text = %q, want next phase without stale proposal", inlineText)
	}
	if got, want := labels, []string{"Approve Phase 2 implementation", "Scope details", "Revise Phase 2 implementation", "Park", "Stop"}; !equalStringSlices(got, want) {
		t.Fatalf("inline labels = %#v, want %#v", got, want)
	}
}

func TestMaterializePendingOperationProposalWhenPhasePlanHasNoPendingPhase(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	key := session.SessionKey{ChatID: 9017, UserID: 0, Scope: telegramDMScopeRef(9017)}
	if err := store.UpdateOperationState(key, session.OperationState{
		ID:        "completed-phase-plan-op",
		Objective: "Ship the remaining operator cleanup.",
		Status:    session.OperationStatusBlocked,
		Proposal: session.OperationProposal{
			ID:            "ordinary-proposal-after-phases",
			Kind:          "read_only_review",
			Summary:       "Review the completed phase evidence and propose cleanup",
			WhyNow:        "The durable phases are complete, but the operator asked for one more ordinary proposal.",
			BoundedEffect: "Inspect only and report the next bounded proposal.",
			Status:        session.ProposalStatusPending,
		},
		PhasePlan: session.OperationPhasePlan{
			ID: "completed-phase-plan",
			Phases: []session.OperationPhase{
				{
					ID:          "phase-1",
					Summary:     "Write contract",
					Status:      session.PlanStatusCompleted,
					LeaseID:     "lease-phase-1",
					CompletedAt: time.Now().UTC(),
				},
				{
					ID:          "phase-2",
					Summary:     "Implement contract",
					Status:      session.PlanStatusCompleted,
					LeaseID:     "lease-phase-2",
					CompletedAt: time.Now().UTC(),
				},
			},
		},
	}); err != nil {
		t.Fatalf("UpdateOperationState() err = %v", err)
	}

	materialized, err := rt.materializePendingOperationProposalApproval(context.Background(), key, core.InboundMessage{ChatID: 9017, SenderID: 1001, Text: "continue", MessageID: 1}, "continue", nil)
	if err != nil {
		t.Fatalf("materializePendingOperationProposalApproval() err = %v", err)
	}
	if !materialized {
		t.Fatal("materialized = false, want ordinary proposal approval")
	}

	cont, err := store.ContinuationState(key)
	if err != nil {
		t.Fatalf("ContinuationState() err = %v", err)
	}
	if cont.Status != session.ContinuationStatusPending || cont.ActionProposal.OperationID != "ordinary-proposal-after-phases" {
		t.Fatalf("continuation = %#v, want pending ordinary proposal lease", cont)
	}
	sender.mu.Lock()
	inlineText := ""
	if len(sender.inline) > 0 {
		inlineText = sender.inline[0].text
	}
	sender.mu.Unlock()
	if !strings.Contains(inlineText, "Review the completed phase evidence and propose cleanup") {
		t.Fatalf("inline text = %q, want ordinary proposal prompt", inlineText)
	}
}

func TestMaterializePendingOperationProposalWhilePhasePlanIsInProgress(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	key := session.SessionKey{ChatID: 9018, UserID: 0, Scope: telegramDMScopeRef(9018)}
	if err := store.UpdateOperationState(key, session.OperationState{
		ID:        "in-progress-phase-plan-op",
		Objective: "Keep operator work moving without suppressing explicit proposals.",
		Status:    session.OperationStatusBlocked,
		Proposal: session.OperationProposal{
			ID:            "ordinary-proposal-during-phase",
			Kind:          "status_check",
			Summary:       "Report whether the active phase has enough evidence",
			WhyNow:        "The operator asked for a separate status proposal while a phase is marked in progress.",
			BoundedEffect: "Inspect state only and report status; do not advance the active phase.",
			Status:        session.ProposalStatusPending,
		},
		PhasePlan: session.OperationPhasePlan{
			ID:             "in-progress-phase-plan",
			CurrentPhaseID: "phase-1",
			Phases: []session.OperationPhase{
				{
					ID:             "phase-1",
					Summary:        "Patch the implementation",
					Status:         session.PlanStatusInProgress,
					AuthorityClass: "workspace_write",
					LeaseID:        "lease-phase-1",
				},
			},
		},
	}); err != nil {
		t.Fatalf("UpdateOperationState() err = %v", err)
	}

	materialized, err := rt.materializePendingOperationProposalApproval(context.Background(), key, core.InboundMessage{ChatID: 9018, SenderID: 1001, Text: "status", MessageID: 1}, "status", nil)
	if err != nil {
		t.Fatalf("materializePendingOperationProposalApproval() err = %v", err)
	}
	if !materialized {
		t.Fatal("materialized = false, want ordinary proposal approval")
	}

	cont, err := store.ContinuationState(key)
	if err != nil {
		t.Fatalf("ContinuationState() err = %v", err)
	}
	if cont.Status != session.ContinuationStatusPending || cont.ActionProposal.OperationID != "ordinary-proposal-during-phase" {
		t.Fatalf("continuation = %#v, want pending ordinary proposal lease", cont)
	}
	sender.mu.Lock()
	inlineText := ""
	if len(sender.inline) > 0 {
		inlineText = sender.inline[0].text
	}
	sender.mu.Unlock()
	if !strings.Contains(inlineText, "Report whether the active phase has enough evidence") {
		t.Fatalf("inline text = %q, want ordinary proposal prompt", inlineText)
	}
}

func TestMaterializeDoesNotReofferSyntheticPhaseProposalAsOrdinaryProposal(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	key := session.SessionKey{ChatID: 9019, UserID: 0, Scope: telegramDMScopeRef(9019)}
	opState := session.OperationState{
		ID:        "synthetic-phase-proposal-op",
		Objective: "Avoid duplicate phase approvals.",
		Status:    session.OperationStatusBlocked,
		PhasePlan: session.OperationPhasePlan{
			ID:             "synthetic-phase-plan",
			CurrentPhaseID: "phase-1",
			Phases: []session.OperationPhase{
				{
					ID:             "phase-1",
					Summary:        "Patch the implementation",
					Status:         session.PlanStatusInProgress,
					AuthorityClass: "workspace_write",
					LeaseID:        "lease-phase-1",
				},
			},
		},
	}
	opState.Proposal = session.OperationProposal{
		ID:            operationPhaseProposalID(opState, opState.PhasePlan.Phases[0]),
		Kind:          "workspace_write",
		Summary:       "Patch the implementation",
		BoundedEffect: "Edit files and run tests.",
		Status:        session.ProposalStatusPending,
	}
	if err := store.UpdateOperationState(key, opState); err != nil {
		t.Fatalf("UpdateOperationState() err = %v", err)
	}

	materialized, err := rt.materializePendingOperationProposalApproval(context.Background(), key, core.InboundMessage{ChatID: 9019, SenderID: 1001, Text: "continue", MessageID: 1}, "continue", nil)
	if err != nil {
		t.Fatalf("materializePendingOperationProposalApproval() err = %v", err)
	}
	if !materialized {
		t.Fatal("materialized = false, want phase-plan ownership to suppress generic continuation")
	}

	sender.mu.Lock()
	inlineCount := len(sender.inline)
	sender.mu.Unlock()
	if inlineCount != 0 {
		t.Fatalf("inline count = %d, want no duplicate ordinary proposal prompt", inlineCount)
	}
}

func TestApproveDurablePhasePlanLeaseMarksPhaseInProgress(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	key := session.SessionKey{ChatID: 9016, UserID: 0, Scope: telegramDMScopeRef(9016)}
	if err := store.UpdateOperationState(key, session.OperationState{
		ID:        "phase-plan-approve-op",
		Objective: "Deliver durable phase plan.",
		Status:    session.OperationStatusBlocked,
		PhasePlan: session.OperationPhasePlan{
			ID: "phase-plan-approve",
			Phases: []session.OperationPhase{{
				ID:             "phase-1",
				Summary:        "Patch the operation planner",
				Status:         session.PlanStatusPending,
				AuthorityClass: "workspace_write",
				BoundedEffect:  "Edit files and run tests.",
			}},
		},
	}); err != nil {
		t.Fatalf("UpdateOperationState() err = %v", err)
	}
	materialized, err := rt.materializePendingOperationProposalApproval(context.Background(), key, core.InboundMessage{ChatID: 9016, SenderID: 1001, Text: "go", MessageID: 1}, "go", nil)
	if err != nil {
		t.Fatalf("materializePendingOperationProposalApproval() err = %v", err)
	}
	if !materialized {
		t.Fatal("materialized = false, want phase lease")
	}

	if _, err := rt.ApproveContinuation(9016, 1001); err != nil {
		t.Fatalf("ApproveContinuation() err = %v", err)
	}
	got, err := store.OperationState(key)
	if err != nil {
		t.Fatalf("OperationState() err = %v", err)
	}
	if got.Proposal.Status != session.ProposalStatusApproved || got.Status != session.OperationStatusActive {
		t.Fatalf("operation = %#v, want approved active synthetic proposal", got)
	}
	if len(got.PhasePlan.Phases) != 1 || got.PhasePlan.Phases[0].Status != session.PlanStatusInProgress {
		t.Fatalf("phase plan = %#v, want approved phase in_progress", got.PhasePlan)
	}
	if got.PhasePlan.CurrentPhaseID != "phase-1" {
		t.Fatalf("CurrentPhaseID = %q, want phase-1", got.PhasePlan.CurrentPhaseID)
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
