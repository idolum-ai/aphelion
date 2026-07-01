//go:build linux

package tool

import (
	"fmt"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/session"
)

func (r *Registry) requestAuthorityBundleApproval(in requestApprovalInput, key session.SessionKey) (string, error) {
	bundleID := strings.TrimSpace(in.ContractID)
	if bundleID == "" {
		return "", fmt.Errorf("request_approval authority bundle requires authority bundle contract_id")
	}
	bundle, ok, err := r.store.AuthorityBundleContract(bundleID)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", fmt.Errorf("request_approval authority bundle contract %q not found", bundleID)
	}
	bundle = session.NormalizeAuthorityBundleContract(bundle)
	if bundle.SessionID != "" && bundle.SessionID != session.SessionIDForKey(key) {
		return "", fmt.Errorf("request_approval authority bundle session mismatch")
	}
	if !bundle.ExpiresAt.IsZero() && !time.Now().UTC().Before(bundle.ExpiresAt.UTC()) {
		return "", fmt.Errorf("request_approval authority bundle %q expired", bundleID)
	}
	now := time.Now().UTC()
	primary, err := r.authorityBundleApprovalCarrierContract(bundle, key, now)
	if err != nil {
		return "", err
	}
	requirement, err := requestApprovalContinuationLeaseRequirementFromContract(primary)
	if err != nil {
		return "", err
	}
	_, _, leaseID := requestApprovalContinuationLeaseStableIDs(requirement)
	out, err := r.requestContinuationLeaseApproval(requestApprovalInput{
		Action:     "request_continuation_lease",
		Objective:  firstNonEmptyTool(strings.TrimSpace(in.Objective), bundle.Objective),
		ContractID: primary.ContractID,
	}, key)
	if err != nil {
		return "", err
	}
	state, ok, err := r.store.ContinuationStateIfExists(key)
	if err != nil || !ok {
		return out, err
	}
	state = session.NormalizeContinuationState(state)
	if strings.TrimSpace(state.ContinuationLease.ID) != leaseID {
		return out, nil
	}
	state.ActionProposal.OperatorTitle = "Approve bounded authority bundle"
	state.ActionProposal.PlanTitle = "Approve bounded authority bundle"
	state.ActionProposal.Summary = bundle.Summary
	state.ActionProposal.WhyNow = "A typed recovery path requested one bounded authority bundle instead of repeated one-step approvals."
	state.ActionProposal.BoundedEffect = requestApprovalAuthorityBundleBoundedEffect(bundle)
	state.ActionProposal.RiskClass = "authority_bundle"
	state.ActionProposal.AllowedActions = mergeRequestApprovalStrings(state.ActionProposal.AllowedActions, bundle.AllowedActions)
	state.ActionProposal.ForbiddenActions = mergeRequestApprovalStrings(state.ActionProposal.ForbiddenActions, bundle.ForbiddenActions)
	state.ActionProposal.ValidationPlan = mergeRequestApprovalStrings(state.ActionProposal.ValidationPlan, bundle.StopConditions)
	state.ActionProposal.UpdatedAt = now
	state.ContinuationLease.RequiredCapabilityGrants = session.NormalizeCapabilityGrantSpecs(append(state.ContinuationLease.RequiredCapabilityGrants, bundle.RequiredCapabilityGrants...))
	state.ContinuationLease.ForbiddenActions = mergeRequestApprovalStrings(state.ContinuationLease.ForbiddenActions, bundle.ForbiddenActions)
	state.ContinuationLease.ValidationPlan = mergeRequestApprovalStrings(state.ContinuationLease.ValidationPlan, bundle.StopConditions)
	if state.ContinuationLease.Constraints == nil {
		state.ContinuationLease.Constraints = map[string]string{}
	}
	state.ContinuationLease.Constraints["authority_bundle_id"] = bundle.BundleID
	state.ContinuationLease.UpdatedAt = now
	state.StageSummary = bundle.Summary
	state.Objective = firstNonEmptyTool(bundle.Objective, state.Objective)
	state.UpdatedAt = now
	current, err := r.store.OperationState(key)
	if err != nil {
		return "", err
	}
	current = session.NormalizeOperationState(current)
	current = requestApprovalOperationStateForContinuation(current, in, requirement, state, state.ActionProposal.WhyNow, state.ActionProposal.BoundedEffect, now)
	current.Proposal.Summary = bundle.Summary
	current.Proposal.BoundedEffect = state.ActionProposal.BoundedEffect
	current.Proposal.Kind = "authority_bundle"
	current.Summary = "Button-backed authority bundle requested: " + bundle.Summary
	if err := r.store.UpdateOperationAndContinuationState(key, current, state); err != nil {
		return "", err
	}
	return renderOperationState("[APPROVAL_REQUESTED]", current), nil
}

func (r *Registry) authorityBundleApprovalCarrierContract(bundle session.AuthorityBundleContract, key session.SessionKey, now time.Time) (session.ContinuationRecoveryContract, error) {
	if r == nil || r.store == nil {
		return session.ContinuationRecoveryContract{}, fmt.Errorf("request_approval authority bundle store unavailable")
	}
	bundle = session.NormalizeAuthorityBundleContract(bundle)
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if primaryID := strings.TrimSpace(bundle.PrimaryContinuationContractID); primaryID != "" {
		primary, ok, err := r.store.ContinuationRecoveryContract(primaryID)
		if err != nil {
			return session.ContinuationRecoveryContract{}, err
		}
		if !ok {
			return session.ContinuationRecoveryContract{}, fmt.Errorf("request_approval authority bundle primary continuation contract %q not found", primaryID)
		}
		return primary, nil
	}
	if len(bundle.RequiredCapabilityGrants) == 0 {
		return session.ContinuationRecoveryContract{}, fmt.Errorf("request_approval authority bundle requires primary continuation contract or required capability grants")
	}
	sessionID := session.SessionIDForKey(key)
	if bundle.SessionID != "" && bundle.SessionID != sessionID {
		return session.ContinuationRecoveryContract{}, fmt.Errorf("request_approval authority bundle session mismatch")
	}
	// The carrier lease only authorizes presenting and approving the bundle.
	// The actual authority granted by the bundle remains in the bundle's typed
	// required grants and constraints, which are copied onto the approval state
	// after the carrier continuation is materialized.
	carrier, err := session.CompileContinuationRecoveryContract(session.ContinuationRecoveryContractInput{
		RequestInstanceID: strings.TrimSpace(bundle.RequestInstanceID) + ":approval-carrier",
		SessionID:         firstNonEmptyTool(strings.TrimSpace(bundle.SessionID), sessionID),
		SubjectKind:       "authority_bundle_request",
		SubjectRef:        bundle.BundleID,
		Principal:         bundle.Principal,
		LeaseClass:        session.ContinuationLeaseClassCapabilityGrant,
		AllowedActions:    []string{"approve_authority_bundle"},
		Constraints: map[string]string{
			"authority_bundle_id": bundle.BundleID,
		},
		Tool:       requestApprovalToolName,
		ToolAction: "request_authority_bundle",
		CreatedAt:  now,
	})
	if err != nil {
		return session.ContinuationRecoveryContract{}, err
	}
	return r.store.UpsertContinuationRecoveryContract(carrier)
}

func requestApprovalAuthorityBundleBoundedEffect(bundle session.AuthorityBundleContract) string {
	parts := []string{}
	if len(bundle.AllowedActions) > 0 {
		parts = append(parts, "Allowed: "+strings.Join(bundle.AllowedActions, ", "))
	}
	if len(bundle.ForbiddenActions) > 0 {
		parts = append(parts, "Forbidden: "+strings.Join(bundle.ForbiddenActions, ", "))
	}
	if len(bundle.StopConditions) > 0 {
		parts = append(parts, "Stop: "+strings.Join(bundle.StopConditions, "; "))
	}
	return strings.Join(parts, "\n")
}

func mergeRequestApprovalStrings(left []string, right []string) []string {
	out := append([]string(nil), left...)
	seen := map[string]struct{}{}
	for _, value := range out {
		token := requestApprovalActionToken(value)
		if token == "" {
			token = strings.TrimSpace(value)
		}
		if token != "" {
			seen[token] = struct{}{}
		}
	}
	for _, value := range right {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		token := requestApprovalActionToken(value)
		if token == "" {
			token = value
		}
		if _, ok := seen[token]; ok {
			continue
		}
		seen[token] = struct{}{}
		out = append(out, value)
	}
	return out
}
