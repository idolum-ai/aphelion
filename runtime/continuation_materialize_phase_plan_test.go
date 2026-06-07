//go:build linux

package runtime

import (
	"context"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
	"strings"
	"testing"
	"time"
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

func TestContinuationBoundaryDoesNotOfferStalePhaseAfterCompletedOperation(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	key := session.SessionKey{ChatID: 9049, UserID: 0, Scope: telegramDMScopeRef(9049)}
	now := time.Now().UTC()
	consumedLeaseID := "lease-phase-imexx-repo-push-route-repair"
	if err := store.UpdateOperationState(key, session.OperationState{
		ID:        "imexx-repo-effort-update",
		Objective: "Commit and push the Imexx repo documentation update.",
		Status:    session.OperationStatusCompleted,
		Stage:     "completed",
		Summary:   "Completed. Pushed existing commit and verified local HEAD matches origin/main.",
		Proposal: session.OperationProposal{
			ID:      "phase-imexx-repo-effort-update-phase-imexx-repo-push-route-repair",
			Summary: "Use approved GitHub route to push existing local commit.",
			Status:  session.ProposalStatusApproved,
		},
		PhasePlan: session.OperationPhasePlan{
			ID:             "imexx-repo-effort-update-plan",
			Goal:           "Commit and push the Imexx repo documentation update.",
			CurrentPhaseID: "phase-imexx-repo-push-route-repair",
			Phases: []session.OperationPhase{
				{
					ID:               "phase-imexx-repo-doc-update",
					Summary:          "Update local documentation/status artifacts.",
					Status:           session.PlanStatusCompleted,
					AuthorityClass:   "workspace_write",
					RequiresApproval: true,
				},
				{
					ID:               "phase-imexx-repo-commit-push",
					Summary:          "Commit and push the approved repo documentation update to idolum-ai.",
					Status:           session.PlanStatusPending,
					AuthorityClass:   "commit",
					BoundedEffect:    "Commit validated documentation-only changes and push to the intended repository.",
					AllowedActions:   []string{"commit", "push_remote"},
					RequiresApproval: true,
				},
				{
					ID:               "phase-imexx-repo-push-route-repair",
					Summary:          "Use approved GitHub route to push existing local commit.",
					Status:           session.PlanStatusInProgress,
					AuthorityClass:   "commit",
					BoundedEffect:    "Use approved GitHub credentials to push the existing commit and verify remote status.",
					AllowedActions:   []string{"commit", "push_remote"},
					RequiresApproval: true,
					LeaseID:          consumedLeaseID,
				},
			},
		},
	}); err != nil {
		t.Fatalf("UpdateOperationState() err = %v", err)
	}
	consumed := session.ContinuationState{
		Kind:           session.TurnAuthorizationKindContinuation,
		Status:         session.ContinuationStatusIdle,
		Objective:      "Commit and push the Imexx repo documentation update.",
		StageSummary:   "Use approved GitHub route to push existing local commit.",
		RemainingTurns: 0,
		ActionProposal: session.ActionProposal{
			ID:             "aprop-phase-imexx-repo-effort-update-phase-imexx-repo-push-route-repair",
			OperationID:    "phase-imexx-repo-effort-update-phase-imexx-repo-push-route-repair",
			Summary:        "Use approved GitHub route to push existing local commit.",
			RiskClass:      "commit",
			AllowedActions: []string{"commit", "push_remote"},
			Status:         session.ProposalStatusApproved,
		},
		ContinuationLease: session.ContinuationLease{
			ID:             consumedLeaseID,
			ProposalID:     "aprop-phase-imexx-repo-effort-update-phase-imexx-repo-push-route-repair",
			Status:         session.ContinuationLeaseStatusConsumed,
			MaxTurns:       1,
			RemainingTurns: 0,
			AllowedActions: []string{"commit", "push_remote"},
			ConsumedAt:     now,
			ExpiresAt:      now.Add(time.Minute),
		},
		UpdatedAt: now,
	}
	if err := store.UpdateContinuationState(key, consumed); err != nil {
		t.Fatalf("UpdateContinuationState() err = %v", err)
	}

	err = rt.maybeOfferNextOperationPhaseAfterContinuationBoundary(context.Background(), key, consumed, continuationLoopDecision{
		Continue: false,
		Reason:   "not_approved",
		Boundary: "no active approved continuation remains",
		Mission:  missionLoopAssessment{Status: "not_bound", Continue: true},
	})
	if err != nil {
		t.Fatalf("maybeOfferNextOperationPhaseAfterContinuationBoundary() err = %v", err)
	}

	sender.mu.Lock()
	inlineCount := len(sender.inline)
	sender.mu.Unlock()
	if inlineCount != 0 {
		t.Fatalf("inline count = %d, want no stale approval after completed operation", inlineCount)
	}
	reloaded, err := store.OperationState(key)
	if err != nil {
		t.Fatalf("OperationState() err = %v", err)
	}
	if reloaded.Status != session.OperationStatusCompleted {
		t.Fatalf("operation status = %q, want completed; operation=%#v", reloaded.Status, reloaded)
	}
	events, err := store.ExecutionEventsBySession(key, 0, 100)
	if err != nil {
		t.Fatalf("ExecutionEventsBySession() err = %v", err)
	}
	for _, event := range events {
		if strings.TrimSpace(event.EventType) == core.ExecutionEventContinuationOffered {
			t.Fatalf("events = %#v, want no continuation.offered after completed operation", events)
		}
	}
}

func TestMaterializeSinglePhasePlanRecordsNarrowBundleSignal(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	key := session.SessionKey{ChatID: 9044, UserID: 0, Scope: telegramDMScopeRef(9044)}
	now := time.Now().UTC()
	if _, err := store.AppendExecutionEvent(key, session.ExecutionEventInput{
		EventType: core.ExecutionEventContinuationBundleNarrowed,
		Stage:     "continuation",
		Status:    "observed",
		PayloadJSON: `{
			"operation_id":"narrow-op",
			"phase_plan_id":"narrow-plan",
			"phase_id":"phase-prior-commit",
			"phase_family":"local_workspace",
			"phase_category":"mechanical",
			"materialized_from":"operation_plan_lease",
			"bundle_width":1,
			"narrow_streak":1
		}`,
		CreatedAt: now.Add(-time.Minute),
	}); err != nil {
		t.Fatalf("AppendExecutionEvent(prior narrow) err = %v", err)
	}
	if err := store.UpdateOperationState(key, session.OperationState{
		ID:        "narrow-op",
		Objective: "Finish a repo-local implementation slice.",
		Status:    session.OperationStatusBlocked,
		Stage:     "phase_plan",
		PhasePlan: session.OperationPhasePlan{
			ID:   "narrow-plan",
			Goal: "Finish a repo-local implementation slice.",
			Phases: []session.OperationPhase{{
				ID:               "phase-implement-local",
				Summary:          "Implement the local repo patch",
				Status:           session.PlanStatusPending,
				AuthorityClass:   "workspace_write",
				BoundedEffect:    "Edit local files and run focused tests; stop before deploy or external effects.",
				AllowedActions:   []string{"edit_files", "run_tests"},
				ForbiddenActions: []string{"deploy", "restart_service", "push_remote"},
				RequiresApproval: true,
			}},
		},
	}); err != nil {
		t.Fatalf("UpdateOperationState() err = %v", err)
	}

	materialized, err := rt.materializePendingOperationProposalApproval(context.Background(), key, core.InboundMessage{ChatID: 9044, SenderID: 1001, Text: "continue", MessageID: 1}, "continue", nil)
	if err != nil {
		t.Fatalf("materializePendingOperationProposalApproval() err = %v", err)
	}
	if !materialized {
		t.Fatal("materialized = false, want single-phase approval")
	}

	events, err := store.ExecutionEventsBySession(key, 0, 200)
	if err != nil {
		t.Fatalf("ExecutionEventsBySession() err = %v", err)
	}
	var narrowed session.ExecutionEvent
	for _, event := range events {
		if strings.TrimSpace(event.EventType) == core.ExecutionEventContinuationBundleNarrowed {
			narrowed = event
		}
	}
	if narrowed.ID == 0 {
		t.Fatalf("events = %#v, want continuation.bundle.narrowed event", events)
	}
	payload := executionEventPayload(narrowed.PayloadJSON)
	for key, want := range map[string]string{
		"operation_id":       "narrow-op",
		"phase_plan_id":      "narrow-plan",
		"phase_id":           "phase-implement-local",
		"phase_family":       "local_workspace",
		"phase_category":     "mechanical",
		"materialized_from":  "operation_plan_lease",
		"prior_phase_id":     "phase-prior-commit",
		"prior_bundle_width": "1",
	} {
		if got := payloadString(payload, key); got != want {
			t.Fatalf("narrow payload %s = %q, want %q; payload=%s", key, got, want, narrowed.PayloadJSON)
		}
	}
	if streak, ok := payloadInt64(payload, "narrow_streak"); !ok || streak != 2 {
		t.Fatalf("narrow_streak = %d ok=%v, want 2; payload=%s", streak, ok, narrowed.PayloadJSON)
	}
	if count, ok := payloadInt64(payload, "consecutive_narrow_count"); !ok || count != 2 {
		t.Fatalf("consecutive_narrow_count = %d ok=%v, want 2; payload=%s", count, ok, narrowed.PayloadJSON)
	}

	lines, err := rt.StatusDiagnostics(9044)
	if err != nil {
		t.Fatalf("StatusDiagnostics() err = %v", err)
	}
	text := strings.Join(lines, "\n")
	for _, want := range []string{"Approval bundle width", "phase_id=phase-implement-local", "narrow_streak=2"} {
		if !strings.Contains(text, want) {
			t.Fatalf("StatusDiagnostics() = %q, want %q", text, want)
		}
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
					BoundedEffect:  "Patch local status/health code and tests only.",
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
	if !strings.Contains(inlineText, "Approve plan:\nTurn child diagnostic failures") || !strings.Contains(inlineText, "Covers:\n- Step 1: Turn child diagnostic failures") || strings.Contains(inlineText, "Allowed actions:") {
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
	if _, err := rt.ConfigureAutonomy(context.Background(), 9030, 1001, "leased 15m all"); err != nil {
		t.Fatalf("ConfigureAutonomy() err = %v", err)
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
