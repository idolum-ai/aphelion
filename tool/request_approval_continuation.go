//go:build linux

package tool

import (
	"fmt"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/session"
)

const requestApprovalContinuationLeaseTTL = 30 * time.Minute

func (r *Registry) requestContinuationLeaseApproval(in requestApprovalInput, key session.SessionKey) (string, error) {
	contractID := strings.TrimSpace(in.ContractID)
	if contractID == "" {
		return "", fmt.Errorf("request_approval continuation lease requires continuation recovery contract_id")
	}
	contract, ok, err := r.store.ContinuationRecoveryContract(contractID)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", fmt.Errorf("request_approval continuation recovery contract %q not found", contractID)
	}
	if contract.SessionID != "" && contract.SessionID != session.SessionIDForKey(key) {
		return "", fmt.Errorf("request_approval continuation recovery contract session mismatch")
	}
	requirement, err := requestApprovalContinuationLeaseRequirementFromContract(contract)
	if err != nil {
		return "", err
	}
	now := time.Now().UTC()
	expiresAt := now.Add(requestApprovalContinuationLeaseTTL)
	proposalID, decisionID, leaseID := requestApprovalContinuationLeaseStableIDs(requirement)
	if prior, ok, err := r.store.ContinuationStateIfExists(key); err != nil {
		return "", err
	} else if ok {
		prior = session.NormalizeContinuationState(prior)
		if requestApprovalContinuationStateMatchesRequestIdentity(prior, requirement, leaseID) && requestApprovalContinuationStateRefreshable(prior) {
			requirement.RequestInstanceID = requestApprovalContinuationRefreshedRequestInstanceID(requirement.RequestInstanceID, prior)
			proposalID, decisionID, leaseID = requestApprovalContinuationLeaseStableIDs(requirement)
		}
	}
	summary := requestApprovalContinuationLeaseSummary(requirement)
	boundedEffect := requestApprovalContinuationLeaseBoundedEffect(requirement)
	proposal := session.ActionProposal{
		ID:               proposalID,
		OperatorTitle:    "Approve bounded " + string(requirement.LeaseClass) + " lease",
		PlanTitle:        "Approve bounded " + string(requirement.LeaseClass) + " lease",
		Summary:          summary,
		WhyNow:           "A governed tool has an active capability grant but needs a matching continuation lease before it may retry.",
		BoundedEffect:    boundedEffect,
		RiskClass:        string(requirement.LeaseClass),
		AllowedActions:   append([]string(nil), requirement.AllowedActions...),
		ForbiddenActions: requestApprovalContinuationLeaseForbiddenActions(requirement),
		ValidationPlan:   requestApprovalContinuationLeaseValidationPlan(requirement),
		ExpiresAt:        expiresAt,
		Status:           session.ProposalStatusPending,
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	proposal = session.ReconcileActionProposalAuthority(proposal)
	if compilation := requestApprovalContinuationLeaseAuthorityCompilation(proposal, requirement, expiresAt); compilation.Invalid() {
		return "", fmt.Errorf("request_approval continuation lease authority contract invalid: %s", session.AuthorityContractCompilationSummary(compilation))
	}
	lease := session.ContinuationLease{
		ID:                       leaseID,
		ProposalID:               proposalID,
		Status:                   session.ContinuationLeaseStatusPending,
		MaxTurns:                 1,
		RemainingTurns:           1,
		LeaseClass:               requirement.LeaseClass,
		Constraints:              requestApprovalContinuationLeaseConstraints(requirement),
		AllowedActions:           append([]string(nil), requirement.AllowedActions...),
		ForbiddenActions:         append([]string(nil), proposal.ForbiddenActions...),
		ValidationPlan:           append([]string(nil), proposal.ValidationPlan...),
		RequiredCapabilityGrants: requestApprovalContinuationLeaseGrantSpecs(requirement),
		CapabilityGrantIDs:       requestApprovalContinuationLeaseGrantIDs(requirement),
		RecoveryContractID:       contract.ContractID,
		RetryOperation:           requirement.RetryOperation,
		PlanHash:                 contract.ContractHash,
		ExpiresAt:                expiresAt,
		CreatedAt:                now,
		UpdatedAt:                now,
	}
	state := session.ContinuationState{
		Kind:           session.TurnAuthorizationKindContinuation,
		Status:         session.ContinuationStatusPending,
		DecisionID:     decisionID,
		Objective:      firstNonEmptyTool(strings.TrimSpace(in.Objective), summary),
		StageSummary:   summary,
		RemainingTurns: 1,
		PersonaIntent: session.ContinuationIntent{
			Decision:   session.ContinuationIntentDecisionContinue,
			Rationale:  "A typed missing-lease blocker requested an explicit continuation lease.",
			NextStep:   summary,
			Confidence: "high",
			UpdatedAt:  now,
		},
		GovernorIntent: session.ContinuationIntent{
			Decision:    session.ContinuationIntentDecisionContinue,
			Rationale:   "The lease is bounded to one exact tool action and target constraint.",
			NextStep:    summary,
			Constraints: boundedEffect,
			Confidence:  "high",
			Ratified:    true,
			UpdatedAt:   now,
		},
		ActionProposal:    proposal,
		ContinuationLease: lease,
		UpdatedAt:         now,
	}
	state = session.NormalizeContinuationState(state)

	current, err := r.store.OperationState(key)
	if err != nil {
		return "", err
	}
	current = session.NormalizeOperationState(current)
	if prior, ok, err := r.store.ContinuationStateIfExists(key); err != nil {
		return "", err
	} else if ok {
		prior = session.NormalizeContinuationState(prior)
		if requestApprovalContinuationStateMatchesRequestIdentity(prior, requirement, leaseID) {
			current = requestApprovalOperationStateForContinuation(current, in, requirement, prior, proposal.WhyNow, boundedEffect, now)
			if err := r.store.UpdateOperationState(key, session.NormalizeOperationState(current)); err != nil {
				return "", err
			}
			return renderOperationState("[APPROVAL_REQUESTED]", current), nil
		}
		if requestApprovalContinuationStateIsLive(prior) {
			return "", RequestApprovalContinuationConflictError{
				ExistingLeaseID:       strings.TrimSpace(prior.ContinuationLease.ID),
				ExistingLeaseClass:    prior.ContinuationLease.LeaseClass,
				ExistingStatus:        prior.Status,
				ExistingLeaseStatus:   prior.ContinuationLease.Status,
				RequestedLeaseID:      leaseID,
				RequestedLeaseClass:   requirement.LeaseClass,
				RequestInstanceID:     requirement.RequestInstanceID,
				RequestedContractHash: contract.ContractHash,
			}
		}
	}
	current = requestApprovalOperationStateForContinuation(current, in, requirement, state, proposal.WhyNow, boundedEffect, now)
	if err := r.store.UpdateOperationAndContinuationState(key, current, state); err != nil {
		return "", err
	}
	return renderOperationState("[APPROVAL_REQUESTED]", current), nil
}

func requestApprovalContinuationLeaseAuthorityCompilation(proposal session.ActionProposal, requirement missingContinuationLeaseRequirement, expiresAt time.Time) session.AuthorityContractCompilation {
	if !session.ContinuationConstraintsAreDiscoveredEffect(requirement.Constraints) {
		return session.CompileActionProposalAuthorityContract(proposal)
	}
	return session.CompileContinuationAuthorityContract(session.ContinuationState{
		Status:         session.ContinuationStatusApproved,
		ActionProposal: proposal,
		ContinuationLease: session.ContinuationLease{
			Status:         session.ContinuationLeaseStatusActive,
			RemainingTurns: 1,
			LeaseClass:     requirement.LeaseClass,
			Constraints:    requestApprovalContinuationLeaseConstraints(requirement),
			AllowedActions: append([]string(nil), requirement.AllowedActions...),
			ExpiresAt:      expiresAt,
		},
	})
}

func requestApprovalContinuationStateMatchesRequestIdentity(state session.ContinuationState, requirement missingContinuationLeaseRequirement, leaseID string) bool {
	state = session.NormalizeContinuationState(state)
	lease := state.ContinuationLease
	if strings.TrimSpace(lease.ID) != leaseID {
		return false
	}
	if lease.LeaseClass != requirement.LeaseClass {
		return false
	}
	if lease.PlanHash != requestApprovalContinuationLeaseContractHash(requirement) {
		return false
	}
	if requirement.ContractID != "" && strings.TrimSpace(lease.RecoveryContractID) != requirement.ContractID {
		return false
	}
	return true
}

func requestApprovalContinuationStateIsLive(state session.ContinuationState) bool {
	state = session.NormalizeContinuationState(state)
	if state.Status != session.ContinuationStatusPending && state.Status != session.ContinuationStatusApproved {
		return false
	}
	switch state.ContinuationLease.Status {
	case session.ContinuationLeaseStatusPending, session.ContinuationLeaseStatusActive:
		return true
	default:
		return false
	}
}

func requestApprovalContinuationStateRefreshable(state session.ContinuationState) bool {
	state = session.NormalizeContinuationState(state)
	if state.Status != session.ContinuationStatusRevoked || state.ContinuationLease.Status != session.ContinuationLeaseStatusRevoked {
		return false
	}
	if state.ActionProposal.Status != session.ProposalStatusSuperseded {
		return false
	}
	reason := normalizeRequestApprovalRefreshReason(state.HandshakeBlockedReason)
	return strings.Contains(reason, "invalid_authority_contract")
}

func requestApprovalContinuationRefreshedRequestInstanceID(base string, state session.ContinuationState) string {
	base = strings.TrimSpace(base)
	if base == "" {
		base = "request"
	}
	state = session.NormalizeContinuationState(state)
	parts := []string{
		base,
		strings.TrimSpace(state.ContinuationLease.ID),
		strings.TrimSpace(state.HandshakeBlockedReason),
		state.ContinuationLease.RevokedAt.UTC().Format(time.RFC3339Nano),
		state.UpdatedAt.UTC().Format(time.RFC3339Nano),
	}
	digest := session.EffectAttemptCommandHash(strings.Join(parts, "|"))
	if len(digest) > 23 {
		digest = digest[7:23]
	}
	return base + ":refresh:" + digest
}

func normalizeRequestApprovalRefreshReason(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var b strings.Builder
	lastUnderscore := false
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			lastUnderscore = false
			continue
		}
		if !lastUnderscore {
			b.WriteByte('_')
			lastUnderscore = true
		}
	}
	return strings.Trim(b.String(), "_")
}

func continuationLeaseStillLiveForRequestApproval(status session.ContinuationLeaseStatus) bool {
	switch status {
	case session.ContinuationLeaseStatusPending, session.ContinuationLeaseStatusActive, session.ContinuationLeaseStatusConsumed:
		return true
	default:
		return false
	}
}
