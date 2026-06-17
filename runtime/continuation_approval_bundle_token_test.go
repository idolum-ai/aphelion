//go:build linux

package runtime

import (
	"strings"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/session"
)

func TestOperationPhaseBundleSealsPlanAndPhaseFingerprints(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 5, 31, 12, 0, 0, 0, time.UTC)
	opState := session.OperationState{
		ID: "compound-op",
		PhasePlan: session.OperationPhasePlan{
			ID:   "compound-plan",
			Goal: "ship compound approvals",
			Phases: []session.OperationPhase{
				{
					ID:               "phase-1",
					Summary:          "Inspect only",
					Status:           session.PlanStatusPending,
					AuthorityClass:   "read_only_review",
					BoundedEffect:    "Read repo files only.",
					AllowedActions:   []string{"inspect"},
					ForbiddenActions: []string{"edit_files"},
					ValidationPlan:   []string{"cite inspected files"},
				},
				{
					ID:               "phase-2",
					Summary:          "Write tests",
					Status:           session.PlanStatusPending,
					AuthorityClass:   "workspace_write",
					BoundedEffect:    "Add scoped tests only.",
					AllowedActions:   []string{"edit_tests", "run_tests"},
					ForbiddenActions: []string{"commit", "push_branch"},
					ValidationPlan:   []string{"targeted tests pass"},
				},
			},
		},
	}
	phases := opState.PhasePlan.Phases

	state := continuationStateFromOperationPhaseBundle(opState, phases, "", now)
	bundle := state.ApprovalBundle
	if bundle.OperationID != "compound-op" || bundle.PhasePlanID != "compound-plan" {
		t.Fatalf("bundle operation/plan identity = %q/%q, want compound-op/compound-plan", bundle.OperationID, bundle.PhasePlanID)
	}
	if !strings.HasPrefix(bundle.PlanFingerprint, "sha256:") {
		t.Fatalf("PlanFingerprint = %q, want sha256 fingerprint", bundle.PlanFingerprint)
	}
	if len(bundle.Phases) != 2 {
		t.Fatalf("len(bundle.Phases) = %d, want 2", len(bundle.Phases))
	}
	for i, phase := range bundle.Phases {
		if phase.PhaseFingerprint == "" || !strings.HasPrefix(phase.PhaseFingerprint, "sha256:") {
			t.Fatalf("phase %d fingerprint = %q, want sha256 fingerprint", i, phase.PhaseFingerprint)
		}
		if !continuationApprovalBundlePhaseMatchesOperation(opState, phase, phases[i], i) {
			t.Fatalf("phase %d token should match original operation phase", i)
		}
	}
	if bundle.Phases[0].PhaseFingerprint == bundle.Phases[1].PhaseFingerprint {
		t.Fatalf("phase fingerprints should be per-phase tokens, got duplicate %q", bundle.Phases[0].PhaseFingerprint)
	}

	changed := opState
	changed.PhasePlan.Phases[1].BoundedEffect = "Add scoped tests and runtime behavior."
	if continuationApprovalBundlePhaseMatchesOperation(changed, bundle.Phases[1], changed.PhasePlan.Phases[1], 1) {
		t.Fatalf("phase token matched after bounded effect changed; stale token should be rejected")
	}
	if got := operationPhasePlanFingerprint(changed, changed.PhasePlan.Phases); got == bundle.PlanFingerprint {
		t.Fatalf("plan fingerprint did not change after phase authority envelope changed: %q", got)
	}
}

func TestOperationPhaseBundleSubsetFingerprintsValidateAgainstOriginalPlan(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	now := time.Date(2026, 6, 16, 14, 40, 0, 0, time.UTC)
	key := session.SessionKey{ChatID: 9059, UserID: 0, Scope: telegramDMScopeRef(9059)}
	opState := session.OperationState{
		ID:        "subset-bundle-op",
		Objective: "Execute the useful middle of a longer phase plan.",
		Status:    session.OperationStatusActive,
		Stage:     "phase_plan",
		PhasePlan: session.OperationPhasePlan{
			ID:   "subset-bundle-plan",
			Goal: "Execute the useful middle of a longer phase plan.",
			Phases: []session.OperationPhase{
				{
					ID:             "phase-1-plan",
					Summary:        "Plan the work",
					Status:         session.PlanStatusCompleted,
					AuthorityClass: "strategy_plan",
				},
				{
					ID:               "phase-2-pick-workflows",
					Summary:          "Pick the critical workflows",
					Status:           session.PlanStatusPending,
					AuthorityClass:   "strategy_plan",
					BoundedEffect:    "Select the smallest workflow set from existing evidence.",
					AllowedActions:   []string{"inspect_existing_evidence", "update_operation_phase_plan"},
					ForbiddenActions: []string{"write_artifact", "external_contact"},
					ValidationPlan:   []string{"name selected workflows and confidence"},
				},
				{
					ID:               "phase-3-map-workflows",
					Summary:          "Map each selected workflow technically",
					Status:           session.PlanStatusPending,
					AuthorityClass:   "strategy_plan",
					BoundedEffect:    "Create technical notes from existing static map evidence.",
					AllowedActions:   []string{"inspect_existing_evidence", "update_operation_phase_plan"},
					ForbiddenActions: []string{"write_artifact", "external_contact"},
					ValidationPlan:   []string{"cite source evidence for every workflow"},
				},
				{
					ID:             "phase-4-write-dossier",
					Summary:        "Write the dossier artifact",
					Status:         session.PlanStatusPending,
					AuthorityClass: "artifact_generation",
				},
			},
		},
	}
	if err := store.UpdateOperationState(key, opState); err != nil {
		t.Fatalf("UpdateOperationState() err = %v", err)
	}

	offeredSubset := opState.PhasePlan.Phases[1:3]
	state := continuationStateFromOperationPhaseBundle(opState, offeredSubset, "", now)
	state.Status = session.ContinuationStatusApproved
	state.RemainingTurns = len(offeredSubset)
	state.ApprovedBy = 1001
	state.ContinuationLease.Status = session.ContinuationLeaseStatusActive
	state.ContinuationLease.RemainingTurns = len(offeredSubset)
	state.ContinuationLease.ApprovedBy = 1001
	state.ContinuationLease.ApprovedAt = now
	state.ApprovalBundle.Status = session.ContinuationLeaseStatusActive
	state.ApprovalBundle.ApprovedBy = 1001
	state.ApprovalBundle.ApprovedAt = now

	if err := rt.validateContinuationApprovalBundleFingerprints(key, state); err != nil {
		t.Fatalf("validateContinuationApprovalBundleFingerprints() err = %v, want subset bundle accepted against unchanged original plan", err)
	}

	unselectedChanged := opState
	unselectedChanged.PhasePlan.Phases = append([]session.OperationPhase(nil), opState.PhasePlan.Phases...)
	unselectedChanged.PhasePlan.Phases[3].AuthorityClass = "external_account_action"
	unselectedChanged.PhasePlan.Phases[3].BoundedEffect = "Write the dossier and publish it externally."
	if err := store.UpdateOperationState(key, unselectedChanged); err != nil {
		t.Fatalf("UpdateOperationState(unselected changed) err = %v", err)
	}
	if err := rt.validateContinuationApprovalBundleFingerprints(key, state); err != nil {
		t.Fatalf("validateContinuationApprovalBundleFingerprints() err = %v, want unselected phase drift ignored by selected-phase approval", err)
	}

	selectedChanged := unselectedChanged
	selectedChanged.PhasePlan.Phases = append([]session.OperationPhase(nil), unselectedChanged.PhasePlan.Phases...)
	selectedChanged.PhasePlan.Phases[1].BoundedEffect = "Select workflows and write a local artifact."
	if err := store.UpdateOperationState(key, selectedChanged); err != nil {
		t.Fatalf("UpdateOperationState(changed) err = %v", err)
	}
	if err := rt.validateContinuationApprovalBundleFingerprints(key, state); err == nil {
		t.Fatal("validateContinuationApprovalBundleFingerprints() err = nil, want selected phase authority change to stale the approval")
	}
}

func TestOperationPhaseBundleSubsetPhaseFingerprintsUseOriginalPlanIndex(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 6, 16, 14, 41, 0, 0, time.UTC)
	opState := session.OperationState{
		ID: "subset-phase-index-op",
		PhasePlan: session.OperationPhasePlan{
			ID:   "subset-phase-index-plan",
			Goal: "Execute middle phases.",
			Phases: []session.OperationPhase{
				{ID: "phase-1", Summary: "Already done", Status: session.PlanStatusCompleted, AuthorityClass: "strategy_plan"},
				{
					ID:             "phase-2",
					Summary:        "First offered phase",
					Status:         session.PlanStatusPending,
					AuthorityClass: "workspace_write",
					BoundedEffect:  "Edit only the scoped local file.",
					AllowedActions: []string{"edit_files"},
				},
				{
					ID:             "phase-3",
					Summary:        "Second offered phase",
					Status:         session.PlanStatusPending,
					AuthorityClass: "workspace_write",
					BoundedEffect:  "Run focused local validation.",
					AllowedActions: []string{"run_tests"},
				},
			},
		},
	}

	state := continuationStateFromOperationPhaseBundle(opState, opState.PhasePlan.Phases[1:3], "", now)
	for _, token := range state.ApprovalBundle.Phases {
		phaseIndex := -1
		var phase session.OperationPhase
		for i, candidate := range opState.PhasePlan.Phases {
			if candidate.ID == token.OperationPhaseID {
				phaseIndex = i
				phase = candidate
				break
			}
		}
		if phaseIndex < 0 {
			t.Fatalf("token = %#v did not map to operation phase", token)
		}
		if !continuationApprovalBundlePhaseMatchesOperation(opState, token, phase, phaseIndex) {
			t.Fatalf("token for %q should match original full-plan index %d; token=%#v", token.OperationPhaseID, phaseIndex, token)
		}
	}
}

func TestContinuationApprovalBundleSubsetDefersUnselectedAndConsumesCurrentOnly(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 5, 31, 13, 0, 0, 0, time.UTC)
	bundle := session.ContinuationApprovalBundle{
		ID: "bundle-subset",
		Phases: []session.ContinuationApprovalBundlePhase{
			{ID: "token-1", OperationPhaseID: "phase-1", Status: session.ContinuationLeaseStatusPending},
			{ID: "token-2", OperationPhaseID: "phase-2", Status: session.ContinuationLeaseStatusPending},
			{ID: "token-3", OperationPhaseID: "phase-3", Status: session.ContinuationLeaseStatusPending},
		},
	}

	approved := continuationApprovalBundleWithPhaseSubsetApproved(bundle, []string{"token-1", "token-3"}, 42, now)
	if approved.Status != session.ContinuationLeaseStatusActive || approved.ApprovedBy != 42 || approved.ApprovedAt.IsZero() {
		t.Fatalf("approved bundle status/by/at = %q/%d/%v, want active/42/nonzero", approved.Status, approved.ApprovedBy, approved.ApprovedAt)
	}
	if approved.CurrentPhaseID != "token-1" {
		t.Fatalf("CurrentPhaseID = %q, want token-1", approved.CurrentPhaseID)
	}
	assertBundlePhaseStatus(t, approved, 0, session.ContinuationLeaseStatusActive)
	assertBundlePhaseStatus(t, approved, 1, session.ContinuationLeaseStatusDeferred)
	assertBundlePhaseStatus(t, approved, 2, session.ContinuationLeaseStatusPending)
	if approved.Phases[0].ApprovedAt.IsZero() || approved.Phases[0].ActivatedAt.IsZero() {
		t.Fatalf("approved current phase should have approved_at and activated_at")
	}
	if approved.Phases[1].DeferredAt.IsZero() {
		t.Fatalf("deferred phase should record deferred_at")
	}
	if approved.Phases[2].ApprovedAt.IsZero() || !approved.Phases[2].ActivatedAt.IsZero() {
		t.Fatalf("approved future phase should have approved_at and no activated_at")
	}

	consumed := continuationApprovalBundleAfterTurnConsumed(approved, now.Add(time.Minute))
	assertBundlePhaseStatus(t, consumed, 0, session.ContinuationLeaseStatusConsumed)
	assertBundlePhaseStatus(t, consumed, 1, session.ContinuationLeaseStatusDeferred)
	assertBundlePhaseStatus(t, consumed, 2, session.ContinuationLeaseStatusActive)
	if consumed.CurrentPhaseID != "token-3" {
		t.Fatalf("CurrentPhaseID after consuming current phase = %q, want token-3", consumed.CurrentPhaseID)
	}
	if consumed.Phases[0].ConsumedAt.IsZero() {
		t.Fatalf("consumed phase should record consumed_at")
	}
	if consumed.Phases[1].ConsumedAt.IsZero() == false {
		t.Fatalf("deferred phase should not be consumed")
	}
}

func TestContinuationStateWithLeaseApprovedApprovesAllBundlePhasesAsTokens(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 5, 31, 14, 0, 0, 0, time.UTC)
	state := session.ContinuationState{
		Status:         session.ContinuationStatusPending,
		RemainingTurns: 2,
		ActionProposal: session.ActionProposal{ID: "proposal", Status: session.ProposalStatusPending},
		ContinuationLease: session.ContinuationLease{
			ID:             "lease",
			Status:         session.ContinuationLeaseStatusPending,
			RemainingTurns: 2,
		},
		ApprovalBundle: session.ContinuationApprovalBundle{
			ID: "bundle-all",
			Phases: []session.ContinuationApprovalBundlePhase{
				{ID: "token-1", OperationPhaseID: "phase-1", Status: session.ContinuationLeaseStatusPending},
				{ID: "token-2", OperationPhaseID: "phase-2", Status: session.ContinuationLeaseStatusPending},
			},
		},
	}

	approved, err := continuationStateWithLeaseApproved(state, 77, now)
	if err != nil {
		t.Fatalf("continuationStateWithLeaseApproved() err = %v", err)
	}
	if approved.ApprovalBundle.CurrentPhaseID != "token-1" {
		t.Fatalf("CurrentPhaseID = %q, want token-1", approved.ApprovalBundle.CurrentPhaseID)
	}
	assertBundlePhaseStatus(t, approved.ApprovalBundle, 0, session.ContinuationLeaseStatusActive)
	assertBundlePhaseStatus(t, approved.ApprovalBundle, 1, session.ContinuationLeaseStatusPending)
	for i, phase := range approved.ApprovalBundle.Phases {
		if phase.ApprovedAt.IsZero() {
			t.Fatalf("phase %d ApprovedAt is zero; approve-all should stamp each phase token", i)
		}
	}
	if approved.ApprovalBundle.Phases[0].ActivatedAt.IsZero() || !approved.ApprovalBundle.Phases[1].ActivatedAt.IsZero() {
		t.Fatalf("only the first approved phase should be activated initially")
	}
}

func assertBundlePhaseStatus(t *testing.T, bundle session.ContinuationApprovalBundle, index int, want session.ContinuationLeaseStatus) {
	t.Helper()
	if len(bundle.Phases) <= index {
		t.Fatalf("bundle has %d phases, missing index %d", len(bundle.Phases), index)
	}
	if got := bundle.Phases[index].Status; got != want {
		t.Fatalf("bundle phase %d status = %q, want %q", index, got, want)
	}
}
