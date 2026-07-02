//go:build linux

package tool

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/session"
)

func requestApprovalOperationStateForContinuation(current session.OperationState, in requestApprovalInput, requirement missingContinuationLeaseRequirement, state session.ContinuationState, whyNow string, boundedEffect string, now time.Time) session.OperationState {
	current = session.NormalizeOperationState(current)
	summary := requestApprovalContinuationLeaseSummary(requirement)
	if strings.TrimSpace(current.ID) == "" {
		current.ID = generatedOperationID("op")
	}
	current.Objective = firstNonEmptyTool(strings.TrimSpace(in.Objective), current.Objective, summary)
	proposalStatus := session.ProposalStatusPending
	switch state.ContinuationLease.Status {
	case session.ContinuationLeaseStatusActive:
		current.Status = session.OperationStatusActive
		current.Stage = "approval_active"
		current.Summary = "Continuation lease already approved and active: " + summary
		proposalStatus = session.ProposalStatusApproved
	case session.ContinuationLeaseStatusConsumed:
		current.Status = session.OperationStatusCompleted
		current.Stage = "approval_consumed"
		current.Summary = "Continuation lease already consumed: " + summary
		proposalStatus = session.ProposalStatusApproved
	case session.ContinuationLeaseStatusRevoked:
		current.Status = session.OperationStatusBlocked
		current.Stage = "approval_revoked"
		current.Summary = "Continuation lease was denied or revoked: " + summary
		proposalStatus = requestApprovalTerminalProposalStatus(state, session.ProposalStatusDenied)
	case session.ContinuationLeaseStatusExpired:
		current.Status = session.OperationStatusBlocked
		current.Stage = "approval_expired"
		current.Summary = "Continuation lease expired before use: " + summary
		proposalStatus = requestApprovalTerminalProposalStatus(state, session.ProposalStatusExpired)
	case session.ContinuationLeaseStatusDeferred:
		current.Status = session.OperationStatusBlocked
		current.Stage = "approval_deferred"
		current.Summary = "Continuation lease request remains deferred: " + summary
	default:
		current.Status = session.OperationStatusBlocked
		current.Stage = "approval_request"
		current.Summary = "Button-backed continuation lease requested: " + summary
	}
	current.Proposal = session.OperationProposal{
		ID:            state.DecisionID,
		Kind:          string(requirement.LeaseClass),
		Summary:       summary,
		WhyNow:        whyNow,
		BoundedEffect: boundedEffect,
		Status:        proposalStatus,
		UpdatedAt:     now,
	}
	current.UpdatedAt = now
	return session.NormalizeOperationState(current)
}

func requestApprovalTerminalProposalStatus(state session.ContinuationState, fallback session.ProposalStatus) session.ProposalStatus {
	state = session.NormalizeContinuationState(state)
	status := session.NormalizeProposalStatus(state.ActionProposal.Status)
	switch status {
	case session.ProposalStatusDenied, session.ProposalStatusExpired, session.ProposalStatusSuperseded:
		return status
	default:
		return fallback
	}
}

func requestApprovalContinuationLeaseRequirementFromContract(contract session.ContinuationRecoveryContract) (missingContinuationLeaseRequirement, error) {
	contract, err := session.CanonicalizeContinuationRecoveryContract(contract)
	if err != nil {
		return missingContinuationLeaseRequirement{}, err
	}
	requirement := normalizeMissingContinuationLeaseRequirement(missingContinuationLeaseRequirement{
		ContractID:          contract.ContractID,
		ContractHash:        contract.ContractHash,
		SubjectKind:         contract.SubjectKind,
		SubjectRef:          contract.SubjectRef,
		AgentID:             contract.AgentID,
		Resource:            contract.Resource,
		GrantID:             contract.GrantID,
		GrantTargetResource: contract.GrantTargetResource,
		RequestInstanceID:   contract.RequestInstanceID,
		Principal:           contract.Principal,
		LeaseClass:          contract.LeaseClass,
		AllowedActions:      append([]string(nil), contract.AllowedActions...),
		Constraints:         cloneStringMap(contract.Constraints),
		Tool:                contract.Tool,
		ToolAction:          contract.ToolAction,
		RetryOperation:      contract.RetryOperation,
	})
	if requirement.Principal == "" || requirement.RequestInstanceID == "" || requirement.LeaseClass == "" || len(requirement.AllowedActions) == 0 {
		return missingContinuationLeaseRequirement{}, fmt.Errorf("continuation recovery contract %s is incomplete", contract.ContractID)
	}
	if err := validateContinuationRetryOperationForRequirement(requirement); err != nil {
		return missingContinuationLeaseRequirement{}, err
	}
	return requirement, nil
}

func requestApprovalContinuationLeaseSummary(requirement missingContinuationLeaseRequirement) string {
	if requirement.LeaseClass == session.ContinuationLeaseClassChildWake && requirement.AgentID != "" {
		return fmt.Sprintf("Invoke durable_agent wake_once for %s exactly once", requirement.AgentID)
	}
	if requirement.Tool != "" && requirement.ToolAction != "" {
		return fmt.Sprintf("Retry %s %s exactly once under the approved %s lease", requirement.Tool, requirement.ToolAction, requirement.LeaseClass)
	}
	return fmt.Sprintf("Use the approved %s continuation lease exactly once", requirement.LeaseClass)
}

func requestApprovalContinuationLeaseBoundedEffect(requirement missingContinuationLeaseRequirement) string {
	if requirement.LeaseClass == session.ContinuationLeaseClassChildWake && requirement.AgentID != "" {
		return fmt.Sprintf("Permit durable_agent wake_once to wake only %s once, consume the pending or contract-bound parent guidance batch, and stop after one child result or pre-child failure.", requirement.AgentID)
	}
	return fmt.Sprintf("Permit exactly one %s continuation turn for %s %s, then stop and report the result.", requirement.LeaseClass, requirement.Tool, requirement.ToolAction)
}

func requestApprovalContinuationLeaseForbiddenActions(requirement missingContinuationLeaseRequirement) []string {
	forbidden := []string{"expand_authority_without_new_approval", "ignore_stop_or_revocation", "unbounded_retry_loop"}
	if requirement.LeaseClass == session.ContinuationLeaseClassChildWake {
		forbidden = append(forbidden,
			"wake_unnamed_child",
			"change_child_policy_without_approval",
			"grant_child_capability_without_capability_authority",
			"credentials_or_tokens",
			"mailbox_or_external_account_probe",
		)
	}
	return forbidden
}

func requestApprovalContinuationLeaseValidationPlan(requirement missingContinuationLeaseRequirement) []string {
	if requirement.LeaseClass == session.ContinuationLeaseClassChildWake {
		return []string{
			"verify the lease is bound to the exact child agent_id",
			"record one wake result or typed pre-child blocker",
			"stop without retrying or broadening child authority",
		}
	}
	return []string{"record the typed result or blocker before any retry", "stop when the single approved turn is consumed"}
}

func requestApprovalContinuationLeaseConstraints(requirement missingContinuationLeaseRequirement) map[string]string {
	constraints := map[string]string{}
	for key, value := range requirement.Constraints {
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key != "" && value != "" {
			constraints[key] = value
		}
	}
	if requirement.Principal != "" {
		constraints["principal"] = requirement.Principal
	}
	if requirement.AgentID != "" {
		constraints["agent_id"] = requirement.AgentID
	}
	if requirement.Resource != "" {
		constraints["resource"] = requirement.Resource
	}
	if requirement.GrantID != "" {
		constraints["grant_id"] = requirement.GrantID
	}
	if requirement.GrantTargetResource != "" {
		constraints["grant_target_resource"] = requirement.GrantTargetResource
		constraints["target_resource"] = requirement.GrantTargetResource
	}
	if requirement.Tool != "" {
		constraints["tool"] = requirement.Tool
	}
	if requirement.ToolAction != "" {
		constraints["tool_action"] = requirement.ToolAction
	}
	return constraints
}

func requestApprovalContinuationLeaseStableIDs(requirement missingContinuationLeaseRequirement) (string, string, string) {
	token := strings.TrimPrefix(requestApprovalContinuationLeaseIdentityHash(requirement), "sha256:")
	if len(token) > 24 {
		token = token[:24]
	}
	decisionID := "lease-request-" + token
	return "aprop-" + token, decisionID, "lease-" + token
}

func requestApprovalContinuationLeaseIdentityHash(requirement missingContinuationLeaseRequirement) string {
	requirement = normalizeMissingContinuationLeaseRequirement(requirement)
	payload := map[string]any{
		"request_instance_id": requirement.RequestInstanceID,
		"contract_hash":       requestApprovalContinuationLeaseContractHash(requirement),
	}
	raw, _ := json.Marshal(payload)
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func requestApprovalContinuationLeaseContractHash(requirement missingContinuationLeaseRequirement) string {
	requirement = normalizeMissingContinuationLeaseRequirement(requirement)
	if requirement.ContractHash != "" {
		return requirement.ContractHash
	}
	payload := map[string]any{
		"agent_id":              requirement.AgentID,
		"resource":              requirement.Resource,
		"grant_id":              requirement.GrantID,
		"grant_target_resource": requirement.GrantTargetResource,
		"principal":             requirement.Principal,
		"lease_class":           string(requirement.LeaseClass),
		"allowed_actions":       normalizeActionStringsForHash(requirement.AllowedActions),
		"constraints":           normalizeStringMapForHash(requirement.Constraints),
		"tool":                  requirement.Tool,
		"tool_action":           requirement.ToolAction,
	}
	if requirement.RetryOperation.Active() {
		retry := session.NormalizeContinuationRetryOperation(requirement.RetryOperation)
		payload["retry_operation"] = map[string]any{
			"contract":       retry.Contract,
			"operation_kind": retry.OperationKind,
			"tool":           retry.Tool,
			"input_json":     retry.InputJSON,
			"subject_kind":   retry.SubjectKind,
			"subject_ref":    retry.SubjectRef,
		}
	}
	raw, _ := json.Marshal(payload)
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func requestApprovalContinuationLeaseGrantSpecs(requirement missingContinuationLeaseRequirement) []session.CapabilityGrantSpec {
	grantID := strings.TrimSpace(requirement.GrantID)
	target := strings.TrimSpace(requirement.GrantTargetResource)
	if grantID == "" && target == "" {
		return nil
	}
	kind := session.CapabilityKindTool
	allowed := []string{"invoke"}
	contract := ""
	constraints := ""
	if requirement.LeaseClass == session.ContinuationLeaseClassChildWake {
		kind = session.CapabilityKindGenericDelegation
		if target == "" && requirement.AgentID != "" {
			target = durableAgentWakeOnceCapabilityTarget(requirement.AgentID)
		}
		contract = compactJSON(map[string]any{
			"bounded_effect": "Allow invoking durable_agent wake_once for the named child only. The continuation child_wake lease still bounds each wake attempt and supplies the one-turn execution authority.",
			"tool_name":      "durable_agent",
			"tool_action":    "wake_once",
			"agent_id":       requirement.AgentID,
		})
		constraints = compactJSON(map[string]any{"agent_id": requirement.AgentID})
	} else if requirement.LeaseClass == session.ContinuationLeaseClassDataAccess {
		kind = session.CapabilityKindFileAccess
		allowed = []string{"read"}
	} else if requirement.LeaseClass == session.ContinuationLeaseClassLocalWorkspace {
		kind = session.CapabilityKindFileAccess
		allowed = []string{"write"}
	}
	if target == "" {
		target = firstNonEmptyTool(requirement.Resource, requirement.Tool, requirement.AgentID)
	}
	return []session.CapabilityGrantSpec{{
		GrantID:        grantID,
		Kind:           kind,
		TargetResource: target,
		GrantedTo:      requirement.Principal,
		AllowedActions: allowed,
		Contract:       contract,
		Constraints:    constraints,
	}}
}

func requestApprovalContinuationLeaseGrantIDs(requirement missingContinuationLeaseRequirement) []string {
	if grantID := strings.TrimSpace(requirement.GrantID); grantID != "" {
		return []string{grantID}
	}
	return nil
}

func normalizeActionStringsForHash(values []string) []string {
	out := make([]string, 0, len(values))
	seen := map[string]struct{}{}
	for _, value := range values {
		token := requestApprovalActionToken(value)
		if token == "" {
			continue
		}
		if _, ok := seen[token]; ok {
			continue
		}
		seen[token] = struct{}{}
		out = append(out, token)
	}
	sort.Strings(out)
	return out
}

func normalizeStringMapForHash(values map[string]string) map[string]string {
	out := map[string]string{}
	for key, value := range values {
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key != "" && value != "" {
			out[key] = value
		}
	}
	return out
}

func requestApprovalActionToken(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, "-", "_")
	value = strings.ReplaceAll(value, " ", "_")
	return value
}

func firstNonEmptyTool(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func cloneStringMap(values map[string]string) map[string]string {
	out := make(map[string]string, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}
