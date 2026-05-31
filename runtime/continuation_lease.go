//go:build linux

package runtime

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
)

func continuationExecutionPayload(state session.ContinuationState) map[string]any {
	state = session.NormalizeContinuationState(state)
	payload := map[string]any{
		"decision_id":     strings.TrimSpace(state.DecisionID),
		"objective":       strings.TrimSpace(state.Objective),
		"stage_summary":   strings.TrimSpace(state.StageSummary),
		"remaining_turns": state.RemainingTurns,
		"state_source":    "continuation_state",
		"debug_breadcrumb": core.ContinuationDebugBreadcrumb(
			0,
			state.DecisionID,
			"runtime.continuation",
			"runtime/continuation.go",
			"inspect /health trace for continuation state and TES events",
		),
	}
	proposal := session.NormalizeActionProposal(state.ActionProposal)
	if proposal.Active() {
		payload["proposal_id"] = strings.TrimSpace(proposal.ID)
		payload["proposal_status"] = strings.TrimSpace(string(proposal.Status))
		payload["risk_class"] = strings.TrimSpace(proposal.RiskClass)
		payload["plan_hash"] = strings.TrimSpace(proposal.PlanHash)
		if !proposal.ExpiresAt.IsZero() {
			payload["expires_at"] = proposal.ExpiresAt.UTC().Format(time.RFC3339Nano)
		}
	}
	lease := session.NormalizeContinuationLease(state.ContinuationLease)
	if strings.TrimSpace(lease.ID) != "" || strings.TrimSpace(lease.ProposalID) != "" {
		payload["lease_id"] = strings.TrimSpace(lease.ID)
		payload["lease_status"] = strings.TrimSpace(string(lease.Status))
		payload["lease_remaining_turns"] = lease.RemainingTurns
		payload["lease_max_turns"] = lease.MaxTurns
	}
	bundle := session.NormalizeContinuationApprovalBundle(state.ApprovalBundle)
	if bundle.Active() {
		payload["bundle_id"] = strings.TrimSpace(bundle.ID)
		payload["bundle_status"] = strings.TrimSpace(string(bundle.Status))
		payload["bundle_current_phase_id"] = strings.TrimSpace(bundle.CurrentPhaseID)
		payload["bundle_phase_count"] = len(bundle.Phases)
		if phase, ok := currentContinuationBundlePhase(bundle); ok {
			payload["bundle_phase_id"] = strings.TrimSpace(phase.ID)
			payload["bundle_operation_phase_id"] = strings.TrimSpace(phase.OperationPhaseID)
			payload["bundle_phase_index"] = phase.Index
			payload["bundle_phase_authority_class"] = strings.TrimSpace(phase.AuthorityClass)
		}
	}
	if !state.ParkedAt.IsZero() {
		payload["parked_at"] = state.ParkedAt.UTC().Format(time.RFC3339Nano)
	}
	if reason := strings.TrimSpace(state.ParkedReason); reason != "" {
		payload["parked_reason"] = reason
	}
	if source := strings.TrimSpace(state.ParkedSource); source != "" {
		payload["parked_source"] = source
	}
	return payload
}

func buildContinuationActionProposal(decisionID string, consensus continuationConsensus, objective string, nextStep string, now time.Time) session.ActionProposal {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	op := session.NormalizeOperationState(consensus.OperationState)
	proposal := op.Proposal
	actionProposal := session.ActionProposal{
		ID:               "aprop-" + strings.TrimSpace(decisionID),
		OperationID:      strings.TrimSpace(op.ID),
		OperatorTitle:    firstNonEmptyContinuation(proposal.OperatorTitle, proposal.PlanTitle, continuationPlanTitleFromText(proposal.Summary), continuationPlanTitleFromText(nextStep), continuationPlanTitleFromText(objective)),
		PlanTitle:        firstNonEmptyContinuation(proposal.PlanTitle, proposal.OperatorTitle, continuationPlanTitleFromText(proposal.Summary), continuationPlanTitleFromText(nextStep), continuationPlanTitleFromText(objective)),
		Summary:          firstNonEmptyContinuation(proposal.Summary, nextStep, objective),
		WhyNow:           firstNonEmptyContinuation(proposal.WhyNow, consensus.GovernorIntent.Rationale, consensus.PersonaIntent.Rationale),
		BoundedEffect:    firstNonEmptyContinuation(proposal.BoundedEffect, consensus.GovernorIntent.Constraints, "Resume one bounded continuation turn and report the result."),
		RiskClass:        firstNonEmptyContinuation(proposal.Kind, "continuation"),
		AllowedActions:   []string{"continue_one_turn", "use_existing_authority_only", "report_evidence"},
		ForbiddenActions: []string{"expand_authority_without_new_approval", "external_effect_outside_bounded_effect", "ignore_stop_or_revocation"},
		ValidationPlan:   []string{"consume at most the approved continuation turn", "report what changed and what evidence supports it"},
		ExpiresAt:        now.Add(continuationLeaseDefaultTTL),
		Status:           session.ProposalStatusPending,
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	actionProposal = applyContinuationLeaseClassBoundaries(actionProposal)
	actionProposal.PlanHash = actionProposalHash(actionProposal)
	return session.NormalizeActionProposal(actionProposal)
}

func applyContinuationLeaseClassBoundaries(action session.ActionProposal) session.ActionProposal {
	action = session.NormalizeActionProposal(action)
	if actionListContains(action.AllowedActions, organicProposalSandboxAction) || continuationActionIsSystemChangeSandbox(action) {
		return session.NormalizeActionProposal(action)
	}
	action = session.ApplyAuthorityContractToActionProposal(action)
	class := session.InferContinuationLeaseClass(action.RiskClass, action.AllowedActions, action.BoundedEffect)
	switch class {
	case session.ContinuationLeaseClassDataAccess:
		action.AllowedActions = append(action.AllowedActions,
			"request_data_access",
			"read_approved_resource",
			"report_data_access_result",
		)
		action.ForbiddenActions = append(action.ForbiddenActions,
			"silent_data_ingestion",
			"read_unapproved_resource",
			"broad_filesystem_scan",
			"persist_data_without_approval",
			"external_account_access_without_grant",
		)
		action.ValidationPlan = append(action.ValidationPlan,
			"record resource descriptor, transform, retention, and access result",
			"verify no data was consumed before approval",
		)
	case session.ContinuationLeaseClassChildWake:
		action.AllowedActions = append(action.AllowedActions,
			"request_child_wake",
			"wake_named_child",
			"report_child_wake_result",
		)
		action.ForbiddenActions = append(action.ForbiddenActions,
			"wake_unnamed_child",
			"change_child_policy_without_approval",
			"grant_child_capability_without_capability_authority",
			"unbounded_child_wake_loop",
		)
		action.ValidationPlan = append(action.ValidationPlan,
			"record child agent id, wake count, parent message, and final child state",
		)
	case session.ContinuationLeaseClassCapabilityGrant:
		action.AllowedActions = append(action.AllowedActions,
			"prepare_capability_request",
			"review_capability_scope",
			"capability_access_check",
			"report_capability_decision",
		)
		action.ForbiddenActions = append(action.ForbiddenActions,
			"treat_lease_as_capability_grant",
			"grant_without_capability_authority",
			"invoke_without_active_capability_grant",
			"broaden_capability_target_silently",
		)
		action.ValidationPlan = append(action.ValidationPlan,
			"show request id, target resource, allowed actions, and active grant/access-check evidence before invocation",
		)
	case session.ContinuationLeaseClassDeployRestart:
		action.AllowedActions = append(action.AllowedActions,
			"git_status",
			"review_intended_diff",
			"git_commit_intended_changes",
			"make_build",
			"install_user_service",
			"restart_aphelion_service",
			"run_verify_deploy",
			"prepare_release_handoff",
			"run_explicit_release_step",
			"post_restart_verification",
			"report_release_result",
		)
		action.ForbiddenActions = append(action.ForbiddenActions,
			"deploy_without_handoff",
			"restart_without_recovery_artifact",
			"unbounded_restart_loop",
			"skip_post_deploy_verification",
			"push_or_commit_outside_release_lease",
			"commit_unrelated_changes",
			"skip_build_or_tests_before_restart",
		)
		action.ValidationPlan = append(action.ValidationPlan,
			"review git status and intended diff before staging",
			"commit only intended repo changes and record the commit hash",
			"build, install the user service, restart the user service, and run verify-deploy",
			"record pre-action git/service state, handoff, post-action status, journal/smoke evidence, and rollback/residual risk",
		)
	}
	return session.NormalizeActionProposal(action)
}

func continuationActionIsSystemChangeSandbox(action session.ActionProposal) bool {
	return strings.TrimSpace(action.RiskClass) == "system_change" &&
		actionListContains(action.ForbiddenActions, "deploy") &&
		(actionListContains(action.AllowedActions, "patch_code") ||
			actionListContains(action.AllowedActions, "run_tests") ||
			actionListContains(action.AllowedActions, "edit_files") ||
			actionListContains(action.AllowedActions, "write_user_workspace_memory_tmp"))
}

func buildContinuationLease(proposal session.ActionProposal, turns int, now time.Time) session.ContinuationLease {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	proposal = session.ApplyAuthorityContractToActionProposal(proposal)
	if turns <= 0 {
		turns = 1
	}
	leaseClass := session.InferContinuationLeaseClass(proposal.RiskClass, proposal.AllowedActions, proposal.BoundedEffect)
	lease := session.ContinuationLease{
		ID:               "lease-" + strings.TrimPrefix(strings.TrimSpace(proposal.ID), "aprop-"),
		ProposalID:       strings.TrimSpace(proposal.ID),
		MissionID:        strings.TrimSpace(proposal.MissionID),
		OperatorTitle:    firstNonEmptyContinuation(proposal.OperatorTitle, proposal.PlanTitle),
		PlanTitle:        firstNonEmptyContinuation(proposal.PlanTitle, proposal.OperatorTitle),
		Status:           session.ContinuationLeaseStatusPending,
		MaxTurns:         turns,
		RemainingTurns:   turns,
		LeaseClass:       leaseClass,
		Constraints:      session.DefaultContinuationLeaseConstraints(leaseClass),
		AllowedActions:   append([]string(nil), proposal.AllowedActions...),
		ForbiddenActions: append([]string(nil), proposal.ForbiddenActions...),
		ValidationPlan:   append([]string(nil), proposal.ValidationPlan...),
		ExpiresAt:        proposal.ExpiresAt,
		PlanHash:         proposal.PlanHash,
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	return session.NormalizeContinuationLease(lease)
}

func actionProposalHash(proposal session.ActionProposal) string {
	proposal.PlanHash = ""
	proposal.OperatorTitle = ""
	proposal.PlanTitle = ""
	proposal.CreatedAt = time.Time{}
	proposal.UpdatedAt = time.Time{}
	raw, err := json.Marshal(proposal)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func continuationStateWithLeaseApproved(state session.ContinuationState, approverID int64, now time.Time) (session.ContinuationState, error) {
	return continuationStateWithLeaseApprovedForBundlePhases(state, approverID, nil, now)
}

func continuationStateWithLeaseApprovedForBundlePhases(state session.ContinuationState, approverID int64, phaseIDs []string, now time.Time) (session.ContinuationState, error) {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	state = session.NormalizeContinuationState(state)
	if state.ActionProposal.Active() && !state.ActionProposal.ExpiresAt.IsZero() && !state.ActionProposal.ExpiresAt.After(now) {
		state.ActionProposal.Status = session.ProposalStatusExpired
		state.ContinuationLease.Status = session.ContinuationLeaseStatusExpired
		state.ContinuationLease.RemainingTurns = 0
		state.Status = session.ContinuationStatusIdle
		state.RemainingTurns = 0
		state.UpdatedAt = now
		return session.NormalizeContinuationState(state), fmt.Errorf("continuation proposal expired: %w", core.ErrContinuationExpired)
	}
	if strings.TrimSpace(state.ContinuationLease.ID) == "" {
		if !state.ActionProposal.Active() {
			state.ActionProposal = buildContinuationActionProposal(state.DecisionID, continuationConsensus{PersonaIntent: state.PersonaIntent, GovernorIntent: state.GovernorIntent}, state.Objective, state.StageSummary, now)
		}
		state.ContinuationLease = buildContinuationLease(state.ActionProposal, state.RemainingTurns, now)
	}
	if !state.ContinuationLease.ExpiresAt.IsZero() && !state.ContinuationLease.ExpiresAt.After(now) {
		state.ContinuationLease.Status = session.ContinuationLeaseStatusExpired
		state.ContinuationLease.RemainingTurns = 0
		state.ActionProposal.Status = session.ProposalStatusExpired
		state.Status = session.ContinuationStatusIdle
		state.RemainingTurns = 0
		state.UpdatedAt = now
		return session.NormalizeContinuationState(state), fmt.Errorf("continuation lease expired: %w", core.ErrContinuationExpired)
	}
	if state.RemainingTurns <= 0 {
		state.RemainingTurns = state.ContinuationLease.RemainingTurns
	}
	if state.RemainingTurns <= 0 {
		state.RemainingTurns = 1
	}
	state.Status = session.ContinuationStatusApproved
	state.ApprovedBy = approverID
	state.UpdatedAt = now
	state.ActionProposal.Status = session.ProposalStatusApproved
	state.ActionProposal.UpdatedAt = now
	state.ContinuationLease.Status = session.ContinuationLeaseStatusActive
	state.ContinuationLease.ApprovedBy = approverID
	state.ContinuationLease.ApprovedAt = now
	state.ContinuationLease.UpdatedAt = now
	if state.ApprovalBundle.Active() {
		state.ApprovalBundle = continuationApprovalBundleWithPhaseSubsetApproved(state.ApprovalBundle, phaseIDs, approverID, now)
	}
	if state.ContinuationLease.RemainingTurns <= 0 {
		state.ContinuationLease.RemainingTurns = state.RemainingTurns
	}
	if state.ContinuationLease.MaxTurns <= 0 {
		state.ContinuationLease.MaxTurns = state.ContinuationLease.RemainingTurns
	}
	return session.NormalizeContinuationState(state), nil
}

func continuationStateWithPlanLeaseApprovalConsumed(state session.ContinuationState, now time.Time) session.ContinuationState {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	state = session.NormalizeContinuationState(state)
	state.Status = session.ContinuationStatusIdle
	state.RemainingTurns = 0
	state.DecisionID = ""
	state.ActionProposal.Status = session.ProposalStatusApproved
	state.ActionProposal.UpdatedAt = now
	state.ContinuationLease.Status = session.ContinuationLeaseStatusConsumed
	state.ContinuationLease.RemainingTurns = 0
	state.ContinuationLease.ConsumedAt = now
	state.ContinuationLease.UpdatedAt = now
	state.UpdatedAt = now
	return session.NormalizeContinuationState(state)
}

func continuationStateWithLeaseRevoked(state session.ContinuationState, now time.Time) session.ContinuationState {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	state = session.NormalizeContinuationState(state)
	if state.ActionProposal.Active() && state.ActionProposal.Status != session.ProposalStatusApproved {
		state.ActionProposal.Status = session.ProposalStatusDenied
		state.ActionProposal.UpdatedAt = now
	}
	if strings.TrimSpace(state.ContinuationLease.ID) != "" || strings.TrimSpace(state.ContinuationLease.ProposalID) != "" {
		state.ContinuationLease.Status = session.ContinuationLeaseStatusRevoked
		state.ContinuationLease.RemainingTurns = 0
		state.ContinuationLease.RevokedAt = now
		state.ContinuationLease.UpdatedAt = now
	}
	if state.ApprovalBundle.Active() {
		state.ApprovalBundle.Status = session.ContinuationLeaseStatusRevoked
		state.ApprovalBundle.RevokedAt = now
		state.ApprovalBundle.UpdatedAt = now
	}
	state.Status = session.ContinuationStatusRevoked
	state.RemainingTurns = 0
	state.ApprovedBy = 0
	state.DecisionID = ""
	state.UpdatedAt = now
	return session.NormalizeContinuationState(state)
}

func continuationLeaseExpired(state session.ContinuationState, now time.Time) bool {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	state = session.NormalizeContinuationState(state)
	lease := state.ContinuationLease
	return strings.TrimSpace(lease.ID) != "" && !lease.ExpiresAt.IsZero() && !lease.ExpiresAt.After(now.UTC())
}

func continuationStateWithLeaseExpired(state session.ContinuationState, now time.Time) session.ContinuationState {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	state = session.NormalizeContinuationState(state)
	state.Status = session.ContinuationStatusIdle
	state.RemainingTurns = 0
	state.ApprovedBy = 0
	state.DecisionID = ""
	if state.ActionProposal.Active() {
		state.ActionProposal.Status = session.ProposalStatusExpired
		state.ActionProposal.UpdatedAt = now
	}
	if strings.TrimSpace(state.ContinuationLease.ID) != "" || strings.TrimSpace(state.ContinuationLease.ProposalID) != "" {
		state.ContinuationLease.Status = session.ContinuationLeaseStatusExpired
		state.ContinuationLease.RemainingTurns = 0
		state.ContinuationLease.UpdatedAt = now
	}
	if state.ApprovalBundle.Active() {
		state.ApprovalBundle.Status = session.ContinuationLeaseStatusExpired
		state.ApprovalBundle.UpdatedAt = now
	}
	state.UpdatedAt = now
	return session.NormalizeContinuationState(state)
}

func continuationStateAfterLeaseTurnConsumed(state session.ContinuationState, now time.Time) session.ContinuationState {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	state = session.NormalizeContinuationState(state)
	if state.RemainingTurns > 0 {
		state.RemainingTurns--
	}
	if strings.TrimSpace(state.ContinuationLease.ID) != "" || strings.TrimSpace(state.ContinuationLease.ProposalID) != "" {
		if state.ContinuationLease.RemainingTurns > 0 {
			state.ContinuationLease.RemainingTurns--
		}
		state.ContinuationLease.UpdatedAt = now
		if state.ContinuationLease.RemainingTurns <= 0 {
			state.ContinuationLease.Status = session.ContinuationLeaseStatusConsumed
			state.ContinuationLease.ConsumedAt = now
		}
	}
	state.ApprovalBundle = continuationApprovalBundleAfterTurnConsumed(state.ApprovalBundle, now)
	if state.RemainingTurns <= 0 {
		state.Status = session.ContinuationStatusIdle
		state.DecisionID = ""
		state.ApprovedBy = 0
	}
	state.UpdatedAt = now
	return session.NormalizeContinuationState(state)
}

func continuationApprovalBundleWithPhaseSubsetApproved(bundle session.ContinuationApprovalBundle, phaseIDs []string, approverID int64, now time.Time) session.ContinuationApprovalBundle {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	bundle = session.NormalizeContinuationApprovalBundle(bundle)
	if !bundle.Active() {
		return bundle
	}
	selected := make(map[string]struct{}, len(phaseIDs))
	for _, id := range phaseIDs {
		id = strings.TrimSpace(id)
		if id != "" {
			selected[id] = struct{}{}
		}
	}
	approveAll := len(selected) == 0
	bundle.Status = session.ContinuationLeaseStatusActive
	bundle.ApprovedBy = approverID
	bundle.ApprovedAt = now
	bundle.UpdatedAt = now
	bundle.CurrentPhaseID = ""
	for i := range bundle.Phases {
		phaseID := strings.TrimSpace(bundle.Phases[i].ID)
		_, approve := selected[phaseID]
		if approveAll || approve {
			bundle.Phases[i].Status = session.ContinuationLeaseStatusPending
			bundle.Phases[i].ApprovedAt = now
			if bundle.CurrentPhaseID == "" {
				bundle.Phases[i].Status = session.ContinuationLeaseStatusActive
				bundle.Phases[i].ActivatedAt = now
				bundle.CurrentPhaseID = phaseID
			}
		} else {
			bundle.Phases[i].Status = session.ContinuationLeaseStatusDeferred
			bundle.Phases[i].DeferredAt = now
		}
	}
	if bundle.CurrentPhaseID == "" {
		bundle.Status = session.ContinuationLeaseStatusDeferred
	}
	return session.NormalizeContinuationApprovalBundle(bundle)
}

func currentContinuationBundlePhase(bundle session.ContinuationApprovalBundle) (session.ContinuationApprovalBundlePhase, bool) {
	bundle = session.NormalizeContinuationApprovalBundle(bundle)
	if len(bundle.Phases) == 0 {
		return session.ContinuationApprovalBundlePhase{}, false
	}
	currentID := strings.TrimSpace(bundle.CurrentPhaseID)
	if currentID != "" {
		for _, phase := range bundle.Phases {
			if strings.TrimSpace(phase.ID) == currentID {
				return phase, true
			}
		}
	}
	for _, phase := range bundle.Phases {
		if phase.Status == session.ContinuationLeaseStatusActive || phase.Status == session.ContinuationLeaseStatusPending || phase.Status == "" {
			return phase, true
		}
	}
	return bundle.Phases[0], true
}

func continuationApprovalBundleAfterTurnConsumed(bundle session.ContinuationApprovalBundle, now time.Time) session.ContinuationApprovalBundle {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	bundle = session.NormalizeContinuationApprovalBundle(bundle)
	if !bundle.Active() || len(bundle.Phases) == 0 {
		return bundle
	}
	currentID := strings.TrimSpace(bundle.CurrentPhaseID)
	currentIndex := -1
	for i := range bundle.Phases {
		if strings.TrimSpace(bundle.Phases[i].ID) == currentID {
			currentIndex = i
			break
		}
	}
	if currentIndex < 0 {
		currentIndex = 0
	}
	bundle.Phases[currentIndex].Status = session.ContinuationLeaseStatusConsumed
	bundle.Phases[currentIndex].ConsumedAt = now
	nextIndex := -1
	for i := currentIndex + 1; i < len(bundle.Phases); i++ {
		if bundle.Phases[i].Status == session.ContinuationLeaseStatusPending || bundle.Phases[i].Status == "" {
			nextIndex = i
			break
		}
	}
	if nextIndex >= 0 {
		bundle.Phases[nextIndex].Status = session.ContinuationLeaseStatusActive
		bundle.Phases[nextIndex].ActivatedAt = now
		bundle.CurrentPhaseID = strings.TrimSpace(bundle.Phases[nextIndex].ID)
		if bundle.Status != session.ContinuationLeaseStatusRevoked && bundle.Status != session.ContinuationLeaseStatusExpired {
			bundle.Status = session.ContinuationLeaseStatusActive
		}
	} else if bundle.Status != session.ContinuationLeaseStatusRevoked && bundle.Status != session.ContinuationLeaseStatusExpired {
		bundle.Status = session.ContinuationLeaseStatusConsumed
		bundle.ConsumedAt = now
		bundle.CurrentPhaseID = ""
	}
	bundle.UpdatedAt = now
	return session.NormalizeContinuationApprovalBundle(bundle)
}

func newContinuationDecisionID() string {
	return fmt.Sprintf("%x", time.Now().UTC().UnixNano())
}
