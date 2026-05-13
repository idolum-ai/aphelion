//go:build linux

package runtime

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
)

func authorityProposalOpen(proposal session.ActionProposal, now time.Time) bool {
	proposal = session.NormalizeActionProposal(proposal)
	if !proposal.Active() {
		return false
	}
	if proposal.Status != "" && proposal.Status != session.ProposalStatusPending && proposal.Status != session.ProposalStatusApproved {
		return false
	}
	return proposal.ExpiresAt.IsZero() || proposal.ExpiresAt.After(now.UTC())
}

func authorityContinuationLeaseOpen(lease session.ContinuationLease, now time.Time) bool {
	lease = session.NormalizeContinuationLease(lease)
	switch lease.Status {
	case session.ContinuationLeaseStatusPending, session.ContinuationLeaseStatusActive:
	default:
		return false
	}
	if strings.TrimSpace(lease.ID) == "" && strings.TrimSpace(lease.ProposalID) == "" {
		return false
	}
	if lease.Status == session.ContinuationLeaseStatusActive && lease.RemainingTurns <= 0 {
		return false
	}
	return lease.ExpiresAt.IsZero() || lease.ExpiresAt.After(now.UTC())
}

func authorityPlanLeaseOpen(lease session.OperationPlanLease, now time.Time) bool {
	lease = session.NormalizeOperationPlanLease(lease)
	if !lease.Active() {
		return false
	}
	switch lease.Status {
	case session.PlanLeaseStatusProposed, session.PlanLeaseStatusApproved, session.PlanLeaseStatusActive, session.PlanLeaseStatusPaused:
	default:
		return false
	}
	if lease.Status == session.PlanLeaseStatusActive && lease.RemainingTurns <= 0 {
		return false
	}
	return lease.ExpiresAt.IsZero() || lease.ExpiresAt.After(now.UTC())
}

func authorityPendingContinuationMissingDecision(state session.ContinuationState, decisions map[string]session.PendingDecisionRecord) bool {
	state = session.NormalizeContinuationState(state)
	if state.Status != session.ContinuationStatusPending {
		return false
	}
	if state.ActionProposal.Status != "" && state.ActionProposal.Status != session.ProposalStatusPending {
		return false
	}
	decisionID := strings.TrimSpace(state.DecisionID)
	if decisionID == "" {
		return false
	}
	_, ok := decisions[decisionID]
	return !ok
}

func authorityContinuationLeaseExpiredButConsumable(lease session.ContinuationLease, now time.Time) bool {
	lease = session.NormalizeContinuationLease(lease)
	if lease.ExpiresAt.IsZero() || lease.ExpiresAt.After(now.UTC()) {
		return false
	}
	switch lease.Status {
	case session.ContinuationLeaseStatusPending, session.ContinuationLeaseStatusActive:
	default:
		return false
	}
	return lease.RemainingTurns > 0
}

func authorityContinuationLeaseProposalMismatch(state session.ContinuationState) bool {
	state = session.NormalizeContinuationState(state)
	proposalID := strings.TrimSpace(state.ActionProposal.ID)
	leaseProposalID := strings.TrimSpace(state.ContinuationLease.ProposalID)
	if proposalID == "" || leaseProposalID == "" {
		return false
	}
	return proposalID != leaseProposalID
}

func authorityParkedContinuationNeedsRecoveryReview(state session.ContinuationState) bool {
	state = session.NormalizeContinuationState(state)
	if state.ParkedAt.IsZero() {
		return false
	}
	if state.Status == session.ContinuationStatusRevoked || state.ContinuationLease.Status == session.ContinuationLeaseStatusRevoked {
		return false
	}
	return state.RemainingTurns > 0 || state.ContinuationLease.RemainingTurns > 0
}

func authorityPlanLeaseExpiredButConsumable(lease session.OperationPlanLease, now time.Time) bool {
	lease = session.NormalizeOperationPlanLease(lease)
	if lease.ExpiresAt.IsZero() || lease.ExpiresAt.After(now.UTC()) {
		return false
	}
	switch lease.Status {
	case session.PlanLeaseStatusProposed, session.PlanLeaseStatusApproved, session.PlanLeaseStatusActive, session.PlanLeaseStatusPaused:
	default:
		return false
	}
	return lease.RemainingTurns > 0
}

func authorityOperationBlockedWithoutEscalation(state session.OperationState, lease session.OperationPlanLease, now time.Time) bool {
	state = session.NormalizeOperationState(state)
	if state.Status != session.OperationStatusBlocked && !authorityPhasePlanHasBlockedPhase(state.PhasePlan) {
		return false
	}
	if state.Proposal.Active() && (state.Proposal.Status == "" || state.Proposal.Status == session.ProposalStatusPending) {
		return false
	}
	return !authorityPlanLeaseOpen(lease, now)
}

func authorityPhasePlanHasBlockedPhase(plan session.OperationPhasePlan) bool {
	for _, phase := range plan.Phases {
		if strings.TrimSpace(phase.BlockedReasonCode) != "" || phase.StaleAuthority {
			return true
		}
	}
	return false
}

func authorityCapabilityGrantExpired(grant session.CapabilityGrant, now time.Time) bool {
	grant = session.NormalizeCapabilityGrant(grant)
	return grant.Status == session.CapabilityGrantStatusActive && !grant.ExpiresAt.IsZero() && !grant.ExpiresAt.After(now.UTC())
}

func authorityCapabilityGrantUsedWithoutTurnLeaseEvidence(grant session.CapabilityGrant, invocations []session.CapabilityInvocation) bool {
	grant = session.NormalizeCapabilityGrant(grant)
	if grant.InvocationCount <= 0 {
		return false
	}
	if !authorityGrantRequiresChildRuntime(grant) {
		return false
	}
	if len(invocations) == 0 {
		return true
	}
	_, _, ok := authorityMissingTurnLeaseEvidenceMax(invocations)
	return ok
}

func authorityMissingTurnLeaseEvidenceMax(invocations []session.CapabilityInvocation) (int64, time.Time, bool) {
	var maxID int64
	var maxCreatedAt time.Time
	found := false
	for _, invocation := range invocations {
		invocation = session.NormalizeCapabilityInvocation(invocation)
		if strings.TrimSpace(invocation.ContinuationLeaseID) != "" || strings.TrimSpace(invocation.OperationPlanLeaseID) != "" {
			continue
		}
		found = true
		if invocation.InvocationID > maxID {
			maxID = invocation.InvocationID
		}
		if !invocation.CreatedAt.IsZero() && (maxCreatedAt.IsZero() || invocation.CreatedAt.After(maxCreatedAt)) {
			maxCreatedAt = invocation.CreatedAt.UTC()
		}
	}
	return maxID, maxCreatedAt, found
}

func authorityGrantRequiresChildRuntime(grant session.CapabilityGrant) bool {
	grant = session.NormalizeCapabilityGrant(grant)
	if grant.Status != session.CapabilityGrantStatusActive {
		return false
	}
	if _, ok := core.DurableAgentIDFromPrincipal(grant.GrantedTo); !ok {
		return false
	}
	switch grant.Kind {
	case session.CapabilityKindTool,
		session.CapabilityKindExternalAccount,
		session.CapabilityKindLocalDevice,
		session.CapabilityKindFileAccess,
		session.CapabilityKindNetworkAccess:
		return true
	default:
		return false
	}
}

func authorityAutoApprovalUsedOutsideScopeFinding(event session.ExecutionEvent) (authorityProjectionFinding, bool) {
	if event.EventType != core.ExecutionEventAutoApprovalUsed {
		return authorityProjectionFinding{}, false
	}
	var payload struct {
		LeaseID     string `json:"lease_id"`
		Scope       string `json:"scope"`
		RequestKind string `json:"request_kind"`
		DecisionID  string `json:"decision_id"`
		ProposalID  string `json:"proposal_id"`
		WorkMode    string `json:"work_mode"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(event.PayloadJSON)), &payload); err != nil {
		return authorityProjectionFinding{}, false
	}
	if !authorityAutoApprovalScopeAllowsWorkMode(payload.Scope, payload.WorkMode) {
		return authorityProjectionFinding{
			Code:            "auto_approval_used_outside_scope",
			Severity:        "error",
			SourceKind:      "auto_approval_lease",
			SourceID:        strings.TrimSpace(payload.LeaseID),
			SessionID:       strings.TrimSpace(event.SessionID),
			ChatID:          event.ChatID,
			Detail:          "auto-approval use event records work_mode outside the lease scope",
			SuggestedRepair: "revoke the lease and inspect the linked decision or proposal before continuing",
		}, true
	}
	return authorityProjectionFinding{}, false
}

func authorityTailnetBindingInactive(binding session.TailnetGrantBinding) bool {
	switch strings.TrimSpace(binding.Status) {
	case "", session.TailnetGrantBindingStatusRevoked:
		return true
	default:
		return false
	}
}

func authorityAutoApprovalScopeAllowsWorkMode(scope string, workMode string) bool {
	scope = session.NormalizeOperatorAutoApprovalScope(scope)
	workMode = strings.TrimSpace(workMode)
	if workMode == "" {
		return true
	}
	switch scope {
	case session.OperatorAutoApprovalScopeAll:
		return true
	case session.OperatorAutoApprovalScopeWorkspace:
		return workMode == string(WorkModeReadOnly) || workMode == string(WorkModeWorkspaceWrite)
	case session.OperatorAutoApprovalScopeDeploy:
		return workMode == string(WorkModeDeploy) || workMode == string(WorkModeCommit)
	default:
		return false
	}
}
