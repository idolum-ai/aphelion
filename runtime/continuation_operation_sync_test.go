//go:build linux

package runtime

import (
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/session"
)

func TestRevokeContinuationDoesNotRegressCompletedOperationPhase(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	now := time.Now().UTC()
	key := session.SessionKey{ChatID: 9061, UserID: 0, Scope: telegramDMScopeRef(9061)}
	phase := session.OperationPhase{
		ID:             "phase-completed-sync",
		Summary:        "Commit and push the completed branch",
		Status:         session.PlanStatusCompleted,
		AuthorityClass: "commit",
		LeaseID:        "lease-completed-sync",
		CompletedAt:    now.Add(-time.Minute),
	}
	opState := session.OperationState{
		ID:        "op-completed-sync",
		Objective: "Do not reopen completed phase work.",
		Status:    session.OperationStatusCompleted,
		Stage:     "completed",
		PhasePlan: session.OperationPhasePlan{
			ID:             "plan-completed-sync",
			CurrentPhaseID: phase.ID,
			Phases:         []session.OperationPhase{phase},
		},
	}
	proposalID := operationPhaseProposalID(opState, phase)
	opState.Proposal = session.OperationProposal{ID: proposalID, Status: session.ProposalStatusPending}
	if err := store.UpdateOperationState(key, opState); err != nil {
		t.Fatalf("UpdateOperationState() err = %v", err)
	}
	if err := store.UpdateContinuationState(key, session.ContinuationState{
		Kind:           session.TurnAuthorizationKindContinuation,
		Status:         session.ContinuationStatusApproved,
		DecisionID:     proposalID,
		Objective:      opState.Objective,
		StageSummary:   phase.Summary,
		RemainingTurns: 1,
		ApprovedBy:     1001,
		ActionProposal: session.ActionProposal{
			ID:          "aprop-" + proposalID,
			OperationID: proposalID,
			Summary:     phase.Summary,
			RiskClass:   "commit",
			Status:      session.ProposalStatusApproved,
			ExpiresAt:   now.Add(time.Hour),
		},
		ContinuationLease: session.ContinuationLease{
			ID:             phase.LeaseID,
			ProposalID:     "aprop-" + proposalID,
			Status:         session.ContinuationLeaseStatusActive,
			MaxTurns:       1,
			RemainingTurns: 1,
			ApprovedBy:     1001,
			ApprovedAt:     now,
			ExpiresAt:      now.Add(time.Hour),
		},
	}); err != nil {
		t.Fatalf("UpdateContinuationState() err = %v", err)
	}

	if _, err := rt.RevokeContinuationForKey(key); err != nil {
		t.Fatalf("RevokeContinuationForKey() err = %v", err)
	}
	got, err := store.OperationState(key)
	if err != nil {
		t.Fatalf("OperationState() err = %v", err)
	}
	if got.Status != session.OperationStatusCompleted || got.Stage != "completed" {
		t.Fatalf("operation status/stage = %q/%q, want completed/completed", got.Status, got.Stage)
	}
	if len(got.PhasePlan.Phases) != 1 {
		t.Fatalf("phases = %d, want 1", len(got.PhasePlan.Phases))
	}
	gotPhase := got.PhasePlan.Phases[0]
	if gotPhase.Status != session.PlanStatusCompleted || gotPhase.LeaseID != phase.LeaseID || gotPhase.CompletedAt.IsZero() {
		t.Fatalf("phase = %#v, want completed phase with retained lease evidence", gotPhase)
	}
	if got.PhasePlan.CurrentPhaseID != phase.ID {
		t.Fatalf("current phase = %q, want completed phase id retained", got.PhasePlan.CurrentPhaseID)
	}
}

func TestContinuationOperationSyncPreservesTerminalOperationStatus(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	tests := []struct {
		name               string
		chatID             int64
		operationStatus    session.OperationStatus
		operationStage     string
		syncStatus         session.ProposalStatus
		wantProposalStatus session.ProposalStatus
	}{
		{
			name:               "completed expired",
			chatID:             9062,
			operationStatus:    session.OperationStatusCompleted,
			operationStage:     "completed",
			syncStatus:         session.ProposalStatusExpired,
			wantProposalStatus: session.ProposalStatusExpired,
		},
		{
			name:               "completed approved",
			chatID:             9063,
			operationStatus:    session.OperationStatusCompleted,
			operationStage:     "completed",
			syncStatus:         session.ProposalStatusApproved,
			wantProposalStatus: session.ProposalStatusPending,
		},
		{
			name:               "failed denied",
			chatID:             9064,
			operationStatus:    session.OperationStatusFailed,
			operationStage:     "failed",
			syncStatus:         session.ProposalStatusDenied,
			wantProposalStatus: session.ProposalStatusDenied,
		},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			key := session.SessionKey{ChatID: tc.chatID, UserID: 0, Scope: telegramDMScopeRef(tc.chatID)}
			opState := session.OperationState{
				ID:     "terminal-sync-op",
				Status: tc.operationStatus,
				Stage:  tc.operationStage,
				Proposal: session.OperationProposal{
					ID:     "terminal-sync-proposal",
					Status: session.ProposalStatusPending,
				},
			}
			if err := store.UpdateOperationState(key, opState); err != nil {
				t.Fatalf("UpdateOperationState() err = %v", err)
			}
			state := session.ContinuationState{
				ActionProposal:    session.ActionProposal{ID: "aprop-terminal-sync-proposal", OperationID: "terminal-sync-proposal"},
				ContinuationLease: session.ContinuationLease{ID: "lease-terminal-sync"},
			}
			if err := rt.syncOperationProposalStatusFromContinuation(key, state, tc.syncStatus); err != nil {
				t.Fatalf("syncOperationProposalStatusFromContinuation() err = %v", err)
			}
			got, err := store.OperationState(key)
			if err != nil {
				t.Fatalf("OperationState() err = %v", err)
			}
			if got.Status != tc.operationStatus || got.Stage != tc.operationStage {
				t.Fatalf("operation status/stage = %q/%q, want %q/%q", got.Status, got.Stage, tc.operationStatus, tc.operationStage)
			}
			if got.Proposal.Status != tc.wantProposalStatus {
				t.Fatalf("proposal status = %q, want %q", got.Proposal.Status, tc.wantProposalStatus)
			}
		})
	}
}

func TestContinuationOperationSyncPreservesCompletedPlanLease(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	tests := []struct {
		name       string
		chatID     int64
		syncStatus session.ProposalStatus
	}{
		{name: "denied", chatID: 9065, syncStatus: session.ProposalStatusDenied},
		{name: "expired", chatID: 9066, syncStatus: session.ProposalStatusExpired},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			key := session.SessionKey{ChatID: tc.chatID, UserID: 0, Scope: telegramDMScopeRef(tc.chatID)}
			if err := store.UpdateOperationState(key, session.OperationState{
				ID:     "completed-plan-lease-op",
				Status: session.OperationStatusBlocked,
				Stage:  "completed_plan_lease",
				Proposal: session.OperationProposal{
					ID:     "completed-plan-lease",
					Status: session.ProposalStatusPending,
				},
				PlanLease: session.OperationPlanLease{
					ID:     "completed-plan-lease",
					Status: session.PlanLeaseStatusCompleted,
				},
			}); err != nil {
				t.Fatalf("UpdateOperationState() err = %v", err)
			}
			state := session.ContinuationState{
				ActionProposal: session.ActionProposal{
					ID:          "aprop-plan-lease-completed-plan-lease",
					OperationID: "completed-plan-lease",
					RiskClass:   "plan_lease",
				},
				ContinuationLease: session.ContinuationLease{ID: "lease-completed-plan-lease"},
			}
			if err := rt.syncOperationProposalStatusFromContinuation(key, state, tc.syncStatus); err != nil {
				t.Fatalf("syncOperationProposalStatusFromContinuation() err = %v", err)
			}
			got, err := store.OperationState(key)
			if err != nil {
				t.Fatalf("OperationState() err = %v", err)
			}
			if got.PlanLease.Status != session.PlanLeaseStatusCompleted {
				t.Fatalf("plan lease status = %q, want completed", got.PlanLease.Status)
			}
		})
	}
}

func TestCompletedPhaseDuplicateReconcilerDoesNotCompleteNormalizerSuffixPhase(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	opState := session.NormalizeOperationState(session.OperationState{
		ID:     "op-duplicate-suffix",
		Status: session.OperationStatusActive,
		Stage:  "phase_plan",
		PhasePlan: session.OperationPhasePlan{
			ID:             "plan-duplicate-suffix",
			CurrentPhaseID: "fix-tests-2",
			Phases: []session.OperationPhase{
				{
					ID:             "fix-tests",
					Summary:        "Fix failing tests",
					Status:         session.PlanStatusCompleted,
					AuthorityClass: "commit",
					CompletedAt:    now.Add(-time.Minute),
				},
				{
					ID:             "fix-tests",
					Summary:        "Fix failing tests",
					Status:         session.PlanStatusPending,
					AuthorityClass: "commit",
				},
			},
		},
	})
	if len(opState.PhasePlan.Phases) != 2 {
		t.Fatalf("phase count = %d, want 2", len(opState.PhasePlan.Phases))
	}
	if got := opState.PhasePlan.Phases[1].ID; got != "fix-tests-2" {
		t.Fatalf("normalized duplicate phase ID = %q, want fix-tests-2", got)
	}

	got, reconciled := operationStateWithCompletedPhaseDuplicatesReconciled(opState, now)
	if reconciled {
		t.Fatalf("reconciled = true, want false for normalized sibling without explicit duplicate evidence")
	}
	if got.PhasePlan.Phases[1].Status != session.PlanStatusPending {
		t.Fatalf("sibling phase status = %q, want pending", got.PhasePlan.Phases[1].Status)
	}
	closed, completed := operationStateWithCompletedPhasePlanClosed(got, now)
	if completed || closed.Status == session.OperationStatusCompleted {
		t.Fatalf("closed/completed = %v/%q, want operation left open for unexecuted sibling phase", completed, closed.Status)
	}
}

func TestCompletedPhaseDuplicateReconcilerKeepsExplicitSupersessionEvidence(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	opState := session.OperationState{
		ID:     "op-explicit-supersession",
		Status: session.OperationStatusActive,
		Stage:  "phase_plan",
		PhasePlan: session.OperationPhasePlan{
			ID:             "plan-explicit-supersession",
			CurrentPhaseID: "old-phase",
			Phases: []session.OperationPhase{
				{
					ID:             "old-phase",
					Summary:        "Retry stale duplicate work",
					Status:         session.PlanStatusPending,
					AuthorityClass: "read_only",
				},
				{
					ID:                 "new-phase",
					Summary:            "Replace stale duplicate work",
					Status:             session.PlanStatusCompleted,
					AuthorityClass:     "read_only",
					CompletedAt:        now.Add(-time.Minute),
					SupersedesPhaseIDs: []string{"old-phase"},
					BlockedReasonCode:  "",
				},
			},
		},
	}

	got, reconciled := operationStateWithCompletedPhaseDuplicatesReconciled(opState, now)
	if !reconciled {
		t.Fatal("reconciled = false, want explicit supersession evidence to reconcile")
	}
	if got.PhasePlan.Phases[0].Status != session.PlanStatusCompleted {
		t.Fatalf("old phase status = %q, want completed", got.PhasePlan.Phases[0].Status)
	}
	if !got.PhasePlan.Phases[0].StaleAuthority || got.PhasePlan.Phases[0].BlockedReasonCode != "superseded_phase" {
		t.Fatalf("old phase = %#v, want stale superseded completion", got.PhasePlan.Phases[0])
	}
	if got.PhasePlan.Phases[0].LeaseID != "" || got.PhasePlan.Phases[0].CompletedAt.IsZero() {
		t.Fatalf("old phase = %#v, want no lease and completed timestamp", got.PhasePlan.Phases[0])
	}
}
