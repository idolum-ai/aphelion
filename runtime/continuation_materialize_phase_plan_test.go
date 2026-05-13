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
					RequiresApproval: true,
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
	if cont.ActionProposal.RiskClass != "plan_lease" || !strings.Contains(cont.ActionProposal.Summary, "Approve plan budget") {
		t.Fatalf("action proposal = %#v, want next pending phase plan budget", cont.ActionProposal)
	}
	if len(cont.ApprovalBundle.Phases) != 1 || cont.ApprovalBundle.Phases[0].OperationPhaseID != "phase-2-implementation" {
		t.Fatalf("approval bundle = %#v, want next pending phase budget lane", cont.ApprovalBundle)
	}
	if cont.ContinuationLease.MaxTurns != 1 || cont.ContinuationLease.RemainingTurns != 1 {
		t.Fatalf("lease = %#v, want one-turn plan budget lease", cont.ContinuationLease)
	}

	opState, err := store.OperationState(key)
	if err != nil {
		t.Fatalf("OperationState() err = %v", err)
	}
	if opState.Proposal.Status != session.ProposalStatusPending || !strings.Contains(opState.Proposal.Summary, "Approve plan budget") {
		t.Fatalf("operation proposal = %#v, want synthetic pending plan budget proposal", opState.Proposal)
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
	if got, want := labels, []string{"Start", "Details", "Change", "Pause", "Stop"}; !equalStringSlices(got, want) {
		t.Fatalf("inline labels = %#v, want %#v", got, want)
	}
}

func TestMaterializePhasePlanIgnoresStaleInProgressWhenCurrentPhaseIsPending(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	key := session.SessionKey{ChatID: 9028, UserID: 0, Scope: telegramDMScopeRef(9028)}
	if err := store.UpdateOperationState(key, session.OperationState{
		ID:        "child-remainder-op",
		Objective: "Finish the repo-only child Telegram runner work.",
		Status:    session.OperationStatusBlocked,
		Stage:     "review_complete_plan_draft_ready_not_armed_due_autoapproval",
		Proposal: session.OperationProposal{
			ID:      "draft-child-remainder",
			Summary: "Draft repo-only child continuation",
			Status:  session.ProposalStatusSuperseded,
		},
		PhasePlan: session.OperationPhasePlan{
			ID:             "child-remainder-plan",
			Goal:           "Finish the repo-only custom child Telegram runner.",
			CurrentPhaseID: "phase-r1-repo-finish",
			Phases: []session.OperationPhase{
				{
					ID:             "phase-stale-live-route",
					Summary:        "Old live route config phase",
					Status:         session.PlanStatusInProgress,
					AuthorityClass: "config_change_restart",
					LeaseID:        "lease-old-live-route",
				},
				{
					ID:               "phase-r1-repo-finish",
					Summary:          "Commit current dirty safety/status slice and continue repo-only hardening",
					Status:           session.PlanStatusPending,
					AuthorityClass:   "workspace_write",
					BoundedEffect:    "Edit local repo files, run tests, and create local commits; stop before deploy.",
					AllowedActions:   []string{"edit_files", "run_tests", "git_commit"},
					ForbiddenActions: []string{"deploy", "restart_service", "read_token"},
				},
				{
					ID:             "phase-r2-status-polish",
					Summary:        "Polish doctor and status projections",
					Status:         session.PlanStatusPending,
					AuthorityClass: "workspace_write",
					BoundedEffect:  "Patch local status/doctor code and tests only.",
					AllowedActions: []string{"edit_files", "run_tests"},
				},
			},
		},
	}); err != nil {
		t.Fatalf("UpdateOperationState() err = %v", err)
	}

	materialized, err := rt.materializePendingOperationProposalApproval(context.Background(), key, core.InboundMessage{ChatID: 9028, SenderID: 1001, Text: "continue", MessageID: 1}, "continue", nil)
	if err != nil {
		t.Fatalf("materializePendingOperationProposalApproval() err = %v", err)
	}
	if !materialized {
		t.Fatal("materialized = false, want approval prompt despite stale non-current in-progress phase")
	}

	cont, err := store.ContinuationState(key)
	if err != nil {
		t.Fatalf("ContinuationState() err = %v", err)
	}
	if cont.Status != session.ContinuationStatusPending || cont.ActionProposal.RiskClass != "plan_lease" {
		t.Fatalf("continuation = %#v, want pending multi-step plan lease", cont)
	}
	if len(cont.ApprovalBundle.Phases) != 2 ||
		cont.ApprovalBundle.Phases[0].OperationPhaseID != "phase-r1-repo-finish" ||
		cont.ApprovalBundle.Phases[1].OperationPhaseID != "phase-r2-status-polish" {
		t.Fatalf("continuation = %#v, want current and next repo phases bundled with stale phase excluded", cont)
	}

	opState, err := store.OperationState(key)
	if err != nil {
		t.Fatalf("OperationState() err = %v", err)
	}
	if opState.PhasePlan.Phases[0].Status != session.PlanStatusPending || opState.PhasePlan.Phases[0].LeaseID != "" {
		t.Fatalf("stale phase = %#v, want cleared back to pending without old lease", opState.PhasePlan.Phases[0])
	}
	if opState.PhasePlan.CurrentPhaseID != "phase-r1-repo-finish" {
		t.Fatalf("CurrentPhaseID = %q, want phase-r1-repo-finish", opState.PhasePlan.CurrentPhaseID)
	}

	sender.mu.Lock()
	inlineText := ""
	if len(sender.inline) > 0 {
		inlineText = sender.inline[0].text
	}
	sender.mu.Unlock()
	if !strings.Contains(inlineText, "Commit current dirty safety/status slice") || strings.Contains(inlineText, "Old live route config phase") {
		t.Fatalf("inline text = %q, want current commit phase without stale phase", inlineText)
	}
}

func TestMaterializePhasePlanRecoversCurrentPhaseAfterRevokedLease(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	key := session.SessionKey{ChatID: 9032, UserID: 0, Scope: telegramDMScopeRef(9032)}
	leaseID := "lease-phase-recover-current"
	opState := session.OperationState{
		ID:        "recover-current-phase-op",
		Objective: "Reoffer the current phase after a bad lease revocation.",
		Status:    session.OperationStatusActive,
		Stage:     "phase_approval",
		PhasePlan: session.OperationPhasePlan{
			ID:             "recover-current-phase-plan",
			CurrentPhaseID: "phase-r1",
			Phases: []session.OperationPhase{
				{
					ID:             "phase-r1",
					Summary:        "Commit validated local repo slices",
					Status:         session.PlanStatusInProgress,
					AuthorityClass: "workspace_commit_then_repo_write_bounded",
					BoundedEffect:  "Run tests, commit coherent local slices, and report evidence.",
					AllowedActions: []string{"run_go_tests", "git_commit_validated_slices"},
					LeaseID:        leaseID,
				},
			},
		},
	}
	opState.Proposal = session.OperationProposal{
		ID:            operationPhaseProposalID(opState, opState.PhasePlan.Phases[0]),
		Kind:          "workspace_commit_then_repo_write_bounded",
		Summary:       "Commit validated local repo slices",
		BoundedEffect: "Run tests, commit coherent local slices, and report evidence.",
		Status:        session.ProposalStatusApproved,
	}
	if err := store.UpdateOperationState(key, opState); err != nil {
		t.Fatalf("UpdateOperationState() err = %v", err)
	}
	now := time.Now().UTC()
	if err := store.UpdateContinuationState(key, session.ContinuationState{
		Status:         session.ContinuationStatusRevoked,
		StageSummary:   "Commit validated local repo slices",
		RemainingTurns: 0,
		ActionProposal: session.ActionProposal{
			ID:          "aprop-" + opState.Proposal.ID,
			OperationID: opState.Proposal.ID,
			Summary:     "Commit validated local repo slices",
			RiskClass:   "workspace_commit_then_repo_write_bounded",
			Status:      session.ProposalStatusApproved,
			ExpiresAt:   now.Add(time.Hour),
		},
		ContinuationLease: session.ContinuationLease{
			ID:             leaseID,
			ProposalID:     "aprop-" + opState.Proposal.ID,
			Status:         session.ContinuationLeaseStatusRevoked,
			MaxTurns:       1,
			RemainingTurns: 0,
			RevokedAt:      now,
			ExpiresAt:      now.Add(time.Hour),
		},
	}); err != nil {
		t.Fatalf("UpdateContinuationState() err = %v", err)
	}

	materialized, err := rt.materializePendingOperationProposalApproval(context.Background(), key, core.InboundMessage{ChatID: 9032, SenderID: 1001, Text: "continue", MessageID: 1}, "continue", nil)
	if err != nil {
		t.Fatalf("materializePendingOperationProposalApproval() err = %v", err)
	}
	if !materialized {
		t.Fatal("materialized = false, want fresh prompt after revoked current-phase lease")
	}
	cont, err := store.ContinuationState(key)
	if err != nil {
		t.Fatalf("ContinuationState() err = %v", err)
	}
	if cont.Status != session.ContinuationStatusPending || cont.ContinuationLease.Status != session.ContinuationLeaseStatusPending {
		t.Fatalf("continuation = %#v, want fresh pending lease", cont)
	}
	got, err := store.OperationState(key)
	if err != nil {
		t.Fatalf("OperationState() err = %v", err)
	}
	if got.PhasePlan.Phases[0].Status != session.PlanStatusPending || got.PhasePlan.Phases[0].LeaseID == "" {
		t.Fatalf("phase = %#v, want re-materialized pending phase lease", got.PhasePlan.Phases[0])
	}
	if got.Proposal.Status != session.ProposalStatusPending {
		t.Fatalf("proposal status = %q, want fresh pending proposal", got.Proposal.Status)
	}
	sender.mu.Lock()
	inlineCount := len(sender.inline)
	sender.mu.Unlock()
	if inlineCount != 1 {
		t.Fatalf("inline count = %d, want fresh approval buttons", inlineCount)
	}
}

func TestMaterializeMetadataPreflightPhaseUsesReadOnlyContract(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	key := session.SessionKey{ChatID: 9033, UserID: 0, Scope: telegramDMScopeRef(9033)}
	if err := store.UpdateOperationState(key, session.OperationState{
		ID:        "metadata-preflight-op",
		Objective: "Run a metadata-only preflight.",
		Status:    session.OperationStatusBlocked,
		Stage:     "phase_approval",
		PhasePlan: session.OperationPhasePlan{
			ID:             "metadata-preflight-plan",
			CurrentPhaseID: "phase-metadata",
			Phases: []session.OperationPhase{{
				ID:             "phase-metadata",
				Summary:        "Live-adjacent metadata preflight. Prior diagnostic mentioned workspace_write mismatch.",
				Status:         session.PlanStatusPending,
				AuthorityClass: session.AuthorityClassLocalSecretMetadataReadLiveConfigRead,
				BoundedEffect:  "Inspect config route and token-file metadata only; no token contents and no Telegram network.",
				AllowedActions: []string{"report_button_diagnosis"},
			}},
		},
	}); err != nil {
		t.Fatalf("UpdateOperationState() err = %v", err)
	}

	materialized, err := rt.materializePendingOperationProposalApproval(context.Background(), key, core.InboundMessage{ChatID: 9033, SenderID: 1001, Text: "continue", MessageID: 1}, "continue", nil)
	if err != nil {
		t.Fatalf("materializePendingOperationProposalApproval() err = %v", err)
	}
	if !materialized {
		t.Fatal("materialized = false, want metadata phase prompt")
	}
	cont, err := store.ContinuationState(key)
	if err != nil {
		t.Fatalf("ContinuationState() err = %v", err)
	}
	if mode := continuationWorkMode(cont); mode != WorkModeReadOnly {
		t.Fatalf("continuationWorkMode() = %q, want read_only", mode)
	}
	if !actionListContains(cont.ContinuationLease.AllowedActions, session.AuthorityWorkActionReadOnly) {
		t.Fatalf("lease allowed actions = %#v, want read_only", cont.ContinuationLease.AllowedActions)
	}
	if actionListContains(cont.ContinuationLease.AllowedActions, string(WorkModeWorkspaceWrite)) {
		t.Fatalf("lease allowed actions = %#v, should not allow workspace_write", cont.ContinuationLease.AllowedActions)
	}
	sender.mu.Lock()
	inlineCount := len(sender.inline)
	sender.mu.Unlock()
	if inlineCount != 1 {
		t.Fatalf("inline count = %d, want real approval buttons", inlineCount)
	}
}

func TestMaterializePlanningOnlyPhaseOffersPlanBudget(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	key := session.SessionKey{ChatID: 9031, UserID: 0, Scope: telegramDMScopeRef(9031)}
	if err := store.UpdateOperationState(key, session.OperationState{
		ID:        "children-diagnostic-20260504",
		Objective: "Repair child diagnostic failures.",
		Status:    session.OperationStatusBlocked,
		Stage:     "phase_approval",
		PhasePlan: session.OperationPhasePlan{
			ID:             "phase-children-diagnostic-20260504",
			CurrentPhaseID: "phase-2-repair-planning",
			Phases: []session.OperationPhase{
				{
					ID:               "phase-2-repair-planning",
					Summary:          "Turn child diagnostic failures into explicit repair phases.",
					Status:           session.PlanStatusPending,
					AuthorityClass:   "read_only_review",
					BoundedEffect:    "Draft repair phases only; do not execute repairs.",
					AllowedActions:   []string{"draft_repair_phases", "update_operation_phase_plan"},
					RequiresApproval: true,
				},
			},
		},
	}); err != nil {
		t.Fatalf("UpdateOperationState() err = %v", err)
	}

	materialized, err := rt.materializePendingOperationProposalApproval(context.Background(), key, core.InboundMessage{ChatID: 9031, SenderID: 1001, Text: "continue", MessageID: 1}, "continue", nil)
	if err != nil {
		t.Fatalf("materializePendingOperationProposalApproval() err = %v", err)
	}
	if !materialized {
		t.Fatal("materialized = false, want planning phase offered as a plan budget")
	}
	cont, err := store.ContinuationState(key)
	if err != nil {
		t.Fatalf("ContinuationState() err = %v", err)
	}
	if cont.ActionProposal.RiskClass != "plan_lease" || cont.RemainingTurns != 1 {
		t.Fatalf("continuation = %#v, want one-turn plan budget", cont)
	}
	if len(cont.ApprovalBundle.Phases) != 1 || cont.ApprovalBundle.Phases[0].OperationPhaseID != "phase-2-repair-planning" {
		t.Fatalf("approval bundle = %#v, want planning phase as budget lane", cont.ApprovalBundle)
	}
	sender.mu.Lock()
	inlineText := ""
	labels := []string(nil)
	if len(sender.inline) > 0 {
		inlineText = sender.inline[0].text
		labels = continuationButtonLabels(sender.inline[0].rows)
	}
	sender.mu.Unlock()
	if !strings.Contains(inlineText, "Plan:") || !strings.Contains(inlineText, "I'll do:") || strings.Contains(inlineText, "Allowed actions:") {
		t.Fatalf("inline text = %q, want compact plan budget prompt", inlineText)
	}
	if got, want := labels, []string{"Start", "Details", "Change", "Pause", "Stop"}; !equalStringSlices(got, want) {
		t.Fatalf("inline labels = %#v, want %#v", got, want)
	}
}

func TestMaterializeCompletedPhasePlanWithoutProposalAllowsContinuationFallback(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	key := session.SessionKey{ChatID: 9029, UserID: 0, Scope: telegramDMScopeRef(9029)}
	if err := store.UpdateOperationState(key, session.OperationState{
		ID:        "completed-plan-no-proposal",
		Objective: "Allow organic continuation when the phase plan has no actionable approval.",
		Status:    session.OperationStatusBlocked,
		PhasePlan: session.OperationPhasePlan{
			ID: "completed-plan",
			Phases: []session.OperationPhase{
				{ID: "phase-1", Summary: "Review", Status: session.PlanStatusCompleted, CompletedAt: time.Now().UTC()},
			},
		},
	}); err != nil {
		t.Fatalf("UpdateOperationState() err = %v", err)
	}

	materialized, err := rt.materializePendingOperationProposalApproval(context.Background(), key, core.InboundMessage{ChatID: 9029, SenderID: 1001, Text: "continue", MessageID: 1}, "continue", nil)
	if err != nil {
		t.Fatalf("materializePendingOperationProposalApproval() err = %v", err)
	}
	if materialized {
		t.Fatal("materialized = true, want false so organic continuation fallback can run")
	}
	sender.mu.Lock()
	inlineCount := len(sender.inline)
	sender.mu.Unlock()
	if inlineCount != 0 {
		t.Fatalf("inline count = %d, want no materialized prompt", inlineCount)
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

func TestMaterializePlanLeaseUsesAutoApprovalInsteadOfSuppressingPrompt(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	if _, err := rt.ConfigureAutoApproval(context.Background(), 9030, 1001, "15m all"); err != nil {
		t.Fatalf("ConfigureAutoApproval() err = %v", err)
	}
	key := session.SessionKey{ChatID: 9030, UserID: 0, Scope: telegramDMScopeRef(9030)}
	if err := store.UpdateOperationState(key, session.OperationState{
		ID:        "autoapprove-plan-lease-op",
		Objective: "Approve a bounded plan envelope without manual buttons.",
		Status:    session.OperationStatusBlocked,
		Stage:     "plan_lease_proposal",
		PlanLease: session.OperationPlanLease{
			ID:         "autoapprove-plan-lease",
			Summary:    "Approve bounded local review budget",
			Status:     session.PlanLeaseStatusProposed,
			TurnBudget: 2,
			Lanes: []session.OperationPlanLeaseLane{
				{ID: "review", Summary: "Review state", AuthorityClass: "read_only_review", ExpectedTurns: 2},
			},
		},
	}); err != nil {
		t.Fatalf("UpdateOperationState() err = %v", err)
	}

	materialized, err := rt.materializePendingOperationProposalApproval(context.Background(), key, core.InboundMessage{ChatID: 9030, SenderID: 1001, Text: "continue", MessageID: 1}, "continue", nil)
	if err != nil {
		t.Fatalf("materializePendingOperationProposalApproval() err = %v", err)
	}
	if !materialized {
		t.Fatal("materialized = false, want auto-approved plan lease")
	}
	sender.mu.Lock()
	inlineCount := len(sender.inline)
	sender.mu.Unlock()
	if inlineCount != 0 {
		t.Fatalf("inline count = %d, want autoapproval to consume without manual buttons", inlineCount)
	}
	cont, err := store.ContinuationState(key)
	if err != nil {
		t.Fatalf("ContinuationState() err = %v", err)
	}
	if cont.ActionProposal.Status != session.ProposalStatusApproved || cont.ContinuationLease.Status != session.ContinuationLeaseStatusConsumed {
		t.Fatalf("continuation = %#v, want auto-approved consumed plan lease", cont)
	}
	leases, err := store.ActiveOperatorAutoApprovalLeases(9030, time.Now().UTC())
	if err != nil {
		t.Fatalf("ActiveOperatorAutoApprovalLeases() err = %v", err)
	}
	if len(leases) != 1 || leases[0].UsedCount != 1 {
		t.Fatalf("autoapproval leases = %#v, want one consumed use", leases)
	}
}

func TestMaterializeVisibleButtonRequestBypassesAutoApproval(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	if _, err := rt.ConfigureAutoApproval(context.Background(), 9034, 1001, "15m all"); err != nil {
		t.Fatalf("ConfigureAutoApproval() err = %v", err)
	}
	key := session.SessionKey{ChatID: 9034, UserID: 0, Scope: telegramDMScopeRef(9034)}
	if err := store.UpdateOperationState(key, session.OperationState{
		ID:        "visible-buttons-op",
		Objective: "Ask for real visible approval buttons.",
		Status:    session.OperationStatusBlocked,
		Stage:     "phase_approval",
		PhasePlan: session.OperationPhasePlan{
			ID:             "visible-buttons-plan",
			CurrentPhaseID: "phase-visible",
			Phases: []session.OperationPhase{{
				ID:               "phase-visible",
				Summary:          "Read status only",
				Status:           session.PlanStatusPending,
				AuthorityClass:   "read_only_review",
				RequiresApproval: true,
			}},
		},
	}); err != nil {
		t.Fatalf("UpdateOperationState() err = %v", err)
	}

	materialized, err := rt.materializePendingOperationProposalApproval(context.Background(), key, core.InboundMessage{ChatID: 9034, SenderID: 1001, Text: "send me request for approval with buttons", MessageID: 1}, "send me request for approval with buttons", nil)
	if err != nil {
		t.Fatalf("materializePendingOperationProposalApproval() err = %v", err)
	}
	if !materialized {
		t.Fatal("materialized = false, want visible approval prompt")
	}
	sender.mu.Lock()
	inlineCount := len(sender.inline)
	sender.mu.Unlock()
	if inlineCount != 1 {
		t.Fatalf("inline count = %d, want real buttons despite active autoapproval", inlineCount)
	}
	cont, err := store.ContinuationState(key)
	if err != nil {
		t.Fatalf("ContinuationState() err = %v", err)
	}
	if cont.Status != session.ContinuationStatusPending {
		t.Fatalf("continuation status = %q, want pending visible button prompt", cont.Status)
	}
	leases, err := store.ActiveOperatorAutoApprovalLeases(9034, time.Now().UTC())
	if err != nil {
		t.Fatalf("ActiveOperatorAutoApprovalLeases() err = %v", err)
	}
	if len(leases) != 1 || leases[0].UsedCount != 0 {
		t.Fatalf("autoapproval leases = %#v, want no consumed use", leases)
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
				ID:               "phase-1",
				Summary:          "Patch the operation planner",
				Status:           session.PlanStatusPending,
				AuthorityClass:   "workspace_write",
				BoundedEffect:    "Edit files and run tests.",
				RequiresApproval: true,
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

func TestMaterializeSingleLocalDesignPhaseDoesNotRaiseApproval(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	key := session.SessionKey{ChatID: 9035, UserID: 0, Scope: telegramDMScopeRef(9035)}
	if err := store.UpdateOperationState(key, session.OperationState{
		ID:        "single-local-design-op",
		Objective: "Draft one local design note without external effects.",
		Status:    session.OperationStatusBlocked,
		PhasePlan: session.OperationPhasePlan{
			ID: "single-local-design-plan",
			Phases: []session.OperationPhase{{
				ID:             "phase-design",
				Summary:        "Draft local child-agent design artifact",
				Status:         session.PlanStatusPending,
				AuthorityClass: "read_only_review",
				BoundedEffect:  "Inspect local notes and write a local design artifact only.",
				AllowedActions: []string{"inspect_local_notes", "draft_contract"},
			}},
		},
	}); err != nil {
		t.Fatalf("UpdateOperationState() err = %v", err)
	}

	materialized, err := rt.materializePendingOperationProposalApproval(context.Background(), key, core.InboundMessage{ChatID: 9035, SenderID: 1001, Text: "continue", MessageID: 1}, "continue", nil)
	if err != nil {
		t.Fatalf("materializePendingOperationProposalApproval() err = %v", err)
	}
	if materialized {
		t.Fatal("materialized = true, want no approval prompt for one local design lane")
	}
	sender.mu.Lock()
	inlineCount := len(sender.inline)
	sentCount := len(sender.sent)
	sender.mu.Unlock()
	if inlineCount != 0 || sentCount != 0 {
		t.Fatalf("inline=%d sent=%d, want no approval/status ritual", inlineCount, sentCount)
	}
}

func TestMaterializeSingleLocalReportPhaseDoesNotRaiseApproval(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	key := session.SessionKey{ChatID: 9036, UserID: 0, Scope: telegramDMScopeRef(9036)}
	if err := store.UpdateOperationState(key, session.OperationState{
		ID:        "single-local-report-op",
		Objective: "Write a local lifecycle report.",
		Status:    session.OperationStatusBlocked,
		PhasePlan: session.OperationPhasePlan{
			ID: "single-local-report-plan",
			Phases: []session.OperationPhase{{
				ID:             "phase-report",
				Summary:        "Map local lifecycle evidence and write report",
				Status:         session.PlanStatusPending,
				AuthorityClass: "workspace_write",
				BoundedEffect:  "Read local state and write a local report artifact; no external account or restart.",
				AllowedActions: []string{"inspect_local_state", "write_report_artifact"},
			}},
		},
	}); err != nil {
		t.Fatalf("UpdateOperationState() err = %v", err)
	}

	materialized, err := rt.materializePendingOperationProposalApproval(context.Background(), key, core.InboundMessage{ChatID: 9036, SenderID: 1001, Text: "continue", MessageID: 1}, "continue", nil)
	if err != nil {
		t.Fatalf("materializePendingOperationProposalApproval() err = %v", err)
	}
	if materialized {
		t.Fatal("materialized = true, want no approval prompt for one local report lane")
	}
	sender.mu.Lock()
	inlineCount := len(sender.inline)
	sentCount := len(sender.sent)
	sender.mu.Unlock()
	if inlineCount != 0 || sentCount != 0 {
		t.Fatalf("inline=%d sent=%d, want no approval/status ritual", inlineCount, sentCount)
	}
}

func TestMaterializePublicReadPhaseStillRaisesFreshApproval(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	key := session.SessionKey{ChatID: 9037, UserID: 0, Scope: telegramDMScopeRef(9037)}
	if err := store.UpdateOperationState(key, session.OperationState{
		ID:        "public-read-op",
		Objective: "Run one public account metadata read.",
		Status:    session.OperationStatusBlocked,
		PhasePlan: session.OperationPhasePlan{
			ID: "public-read-plan",
			Phases: []session.OperationPhase{{
				ID:             "phase-public-read",
				Summary:        "Read public profile metadata once",
				Status:         session.PlanStatusPending,
				AuthorityClass: "public_account_content_read",
				BoundedEffect:  "Invoke exactly one public profile metadata read for example_handle.",
				AllowedActions: []string{"public_profile_metadata_read"},
			}},
		},
	}); err != nil {
		t.Fatalf("UpdateOperationState() err = %v", err)
	}

	materialized, err := rt.materializePendingOperationProposalApproval(context.Background(), key, core.InboundMessage{ChatID: 9037, SenderID: 1001, Text: "continue", MessageID: 1}, "continue", nil)
	if err != nil {
		t.Fatalf("materializePendingOperationProposalApproval() err = %v", err)
	}
	if !materialized {
		t.Fatal("materialized = false, want public read approval prompt")
	}
	cont, err := store.ContinuationState(key)
	if err != nil {
		t.Fatalf("ContinuationState() err = %v", err)
	}
	if cont.Status != session.ContinuationStatusPending || cont.ActionProposal.RiskClass != "public_account_content_read" {
		t.Fatalf("continuation = %#v, want pending public read approval", cont)
	}
	sender.mu.Lock()
	inlineCount := len(sender.inline)
	sender.mu.Unlock()
	if inlineCount != 1 {
		t.Fatalf("inline count = %d, want approval buttons", inlineCount)
	}
}

func TestMaterializeSupersededPhaseIsStructurallySuppressed(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	key := session.SessionKey{ChatID: 9038, UserID: 0, Scope: telegramDMScopeRef(9038)}
	if err := store.UpdateOperationState(key, session.OperationState{
		ID:        "superseded-phase-op",
		Objective: "Avoid stale duplicate approvals.",
		Status:    session.OperationStatusBlocked,
		PhasePlan: session.OperationPhasePlan{
			ID:             "superseded-phase-plan",
			CurrentPhaseID: "phase-old",
			Phases: []session.OperationPhase{
				{
					ID:             "phase-old",
					Summary:        "Verify old app-only bearer readiness",
					Status:         session.PlanStatusPending,
					AuthorityClass: "external_account_auth_status",
					BoundedEffect:  "Old readiness check that should no longer be used.",
				},
				{
					ID:                 "phase-new",
					Summary:            "Use newer completed bearer readiness evidence",
					Status:             session.PlanStatusCompleted,
					AuthorityClass:     "external_account_auth_status",
					SupersedesPhaseIDs: []string{"phase-old"},
				},
			},
		},
	}); err != nil {
		t.Fatalf("UpdateOperationState() err = %v", err)
	}

	materialized, err := rt.materializePendingOperationProposalApproval(context.Background(), key, core.InboundMessage{ChatID: 9038, SenderID: 1001, Text: "continue", MessageID: 1}, "continue", nil)
	if err != nil {
		t.Fatalf("materializePendingOperationProposalApproval() err = %v", err)
	}
	if materialized {
		t.Fatal("materialized = true, want superseded phase suppressed")
	}
	sender.mu.Lock()
	inlineCount := len(sender.inline)
	sentCount := len(sender.sent)
	sender.mu.Unlock()
	if inlineCount != 0 || sentCount != 0 {
		t.Fatalf("inline=%d sent=%d, want no stale duplicate prompt", inlineCount, sentCount)
	}
}
