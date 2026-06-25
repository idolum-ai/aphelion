//go:build linux

package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/agent"
	"github.com/idolum-ai/aphelion/session"
)

const requestApprovalToolName = "request_approval"

func (r *Registry) requestApproval(_ context.Context, input json.RawMessage, key session.SessionKey) (string, error) {
	if r.store == nil {
		return "", fmt.Errorf("request_approval requires transcript store")
	}
	if key.ChatID == 0 && key.UserID == 0 && key.Scope.IsZero() {
		return "", fmt.Errorf("request_approval requires session context")
	}
	if len(input) == 0 {
		return "", fmt.Errorf("request_approval input is required")
	}

	var in requestApprovalInput
	if err := decodeToolObjectInput(input, &in, "request_approval"); err != nil {
		return "", err
	}
	if requestApprovalActionToken(in.Action) == "request_continuation_lease" {
		return r.requestContinuationLeaseApproval(in, key)
	}
	rawAllowedActions := append([]string(nil), in.Phase.AllowedActions...)
	rawForbiddenActions := append([]string(nil), in.Phase.ForbiddenActions...)
	phase, err := parseOperationPhaseInput(in.Phase)
	if err != nil {
		return "", fmt.Errorf("%s", strings.Replace(err.Error(), "update_operation phase", "request_approval phase", 1))
	}
	if strings.TrimSpace(phase.Summary) == "" {
		return "", fmt.Errorf("request_approval phase summary is required")
	}
	if strings.TrimSpace(phase.ID) == "" {
		phase.ID = generatedOperationID("approval-phase")
	}
	if phase.Status == "" {
		phase.Status = session.PlanStatusPending
	}
	if phase.Status != session.PlanStatusPending {
		return "", fmt.Errorf("request_approval phase status must be pending")
	}
	phase.RequiresApproval = true
	manualOnly := false
	phase.AutoApproveEligible = &manualOnly

	proposal := session.ActionProposal{
		Summary:          phase.Summary,
		WhyNow:           phase.WhyNow,
		BoundedEffect:    phase.BoundedEffect,
		RiskClass:        firstNonEmptyTool(phase.GateReasonCode, phase.AuthorityClass, "continuation"),
		AllowedActions:   rawAllowedActions,
		ForbiddenActions: rawForbiddenActions,
		ValidationPlan:   append([]string(nil), phase.ValidationPlan...),
		Status:           session.ProposalStatusPending,
	}
	proposal = session.ReconcileActionProposalAuthority(proposal)
	phase.AllowedActions = append([]string(nil), proposal.AllowedActions...)
	phase.ForbiddenActions = append([]string(nil), proposal.ForbiddenActions...)
	compilation := session.CompileActionProposalAuthorityContract(proposal)
	if compilation.Invalid() {
		return "", fmt.Errorf("request_approval authority contract invalid: %s", session.AuthorityContractCompilationSummary(compilation))
	}

	current, err := r.store.OperationState(key)
	if err != nil {
		return "", err
	}
	current = session.NormalizeOperationState(current)
	now := time.Now().UTC()
	state := current
	state.Status = session.OperationStatusBlocked
	state.Stage = "approval_request"
	if objective := strings.TrimSpace(in.Objective); objective != "" {
		state.Objective = objective
	} else if strings.TrimSpace(state.Objective) == "" {
		state.Objective = strings.TrimSpace(phase.Summary)
	}
	state.Summary = "Button-backed approval requested: " + strings.TrimSpace(phase.Summary)
	if strings.TrimSpace(state.ID) == "" {
		state.ID = generatedOperationID("op")
	}
	state.Proposal = session.OperationProposal{
		ID:            generatedOperationID("proposal"),
		Kind:          firstNonEmptyTool(phase.AuthorityClass, phase.GateReasonCode, "continuation"),
		Summary:       phase.Summary,
		WhyNow:        phase.WhyNow,
		BoundedEffect: phase.BoundedEffect,
		Status:        session.ProposalStatusPending,
		UpdatedAt:     now,
	}
	plan := current.PhasePlan
	plan.ID = firstNonEmptyTool(plan.ID, generatedOperationID("approval-plan"))
	plan.Goal = firstNonEmptyTool(strings.TrimSpace(in.Objective), plan.Goal, current.Objective, phase.Summary)
	plan.CurrentPhaseID = phase.ID
	replaced := false
	for i := range plan.Phases {
		if strings.TrimSpace(plan.Phases[i].ID) == strings.TrimSpace(phase.ID) {
			plan.Phases[i] = phase
			replaced = true
			break
		}
	}
	if !replaced {
		plan.Phases = append(plan.Phases, phase)
	}
	plan.UpdatedAt = now
	state.PhasePlan = plan
	state.UpdatedAt = now
	state = session.NormalizeOperationState(state)

	if err := r.store.UpdateOperationState(key, state); err != nil {
		return "", err
	}
	return renderOperationState("[APPROVAL_REQUESTED]", state), nil
}

func (r *Registry) requestContinuationLeaseApproval(in requestApprovalInput, key session.SessionKey) (string, error) {
	requirement, err := requestApprovalContinuationLeaseRequirement(in)
	if err != nil {
		return "", err
	}
	now := time.Now().UTC()
	expiresAt := now.Add(30 * time.Minute)
	proposalID := generatedOperationID("aprop-lease-request")
	decisionID := strings.TrimPrefix(proposalID, "aprop-")
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
	if compilation := session.CompileActionProposalAuthorityContract(proposal); compilation.Invalid() {
		return "", fmt.Errorf("request_approval continuation lease authority contract invalid: %s", session.AuthorityContractCompilationSummary(compilation))
	}
	lease := session.ContinuationLease{
		ID:               "lease-" + decisionID,
		ProposalID:       proposalID,
		Status:           session.ContinuationLeaseStatusPending,
		MaxTurns:         1,
		RemainingTurns:   1,
		LeaseClass:       requirement.LeaseClass,
		Constraints:      requestApprovalContinuationLeaseConstraints(requirement),
		AllowedActions:   append([]string(nil), requirement.AllowedActions...),
		ForbiddenActions: append([]string(nil), proposal.ForbiddenActions...),
		ValidationPlan:   append([]string(nil), proposal.ValidationPlan...),
		ExpiresAt:        expiresAt,
		CreatedAt:        now,
		UpdatedAt:        now,
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
	if err := r.store.UpdateContinuationState(key, session.NormalizeContinuationState(state)); err != nil {
		return "", err
	}

	current, err := r.store.OperationState(key)
	if err != nil {
		return "", err
	}
	current = session.NormalizeOperationState(current)
	current.Status = session.OperationStatusBlocked
	current.Stage = "approval_request"
	if strings.TrimSpace(current.ID) == "" {
		current.ID = generatedOperationID("op")
	}
	current.Objective = firstNonEmptyTool(strings.TrimSpace(in.Objective), current.Objective, summary)
	current.Summary = "Button-backed continuation lease requested: " + summary
	current.Proposal = session.OperationProposal{
		ID:            decisionID,
		Kind:          string(requirement.LeaseClass),
		Summary:       summary,
		WhyNow:        proposal.WhyNow,
		BoundedEffect: boundedEffect,
		Status:        session.ProposalStatusPending,
		UpdatedAt:     now,
	}
	current.UpdatedAt = now
	if err := r.store.UpdateOperationState(key, session.NormalizeOperationState(current)); err != nil {
		return "", err
	}
	return renderOperationState("[APPROVAL_REQUESTED]", current), nil
}

func requestApprovalContinuationLeaseRequirement(in requestApprovalInput) (missingContinuationLeaseRequirement, error) {
	leaseClass := session.NormalizeContinuationLeaseClass(session.ContinuationLeaseClass(in.LeaseClass))
	if leaseClass == "" {
		return missingContinuationLeaseRequirement{}, fmt.Errorf("request_approval continuation lease_class is required")
	}
	toolName := strings.TrimSpace(in.Tool)
	toolAction := requestApprovalActionToken(in.ToolAction)
	constraints := map[string]string{}
	for key, value := range in.Constraints {
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key != "" && value != "" {
			constraints[key] = value
		}
	}
	requirement := normalizeMissingContinuationLeaseRequirement(missingContinuationLeaseRequirement{
		AgentID:             strings.TrimSpace(in.AgentID),
		Resource:            strings.TrimSpace(in.Resource),
		GrantID:             strings.TrimSpace(in.GrantID),
		GrantTargetResource: strings.TrimSpace(in.GrantTargetResource),
		Principal:           strings.TrimSpace(in.Principal),
		LeaseClass:          leaseClass,
		AllowedActions:      append([]string(nil), in.AllowedActions...),
		Constraints:         constraints,
		Tool:                toolName,
		ToolAction:          toolAction,
	})
	if requirement.Principal == "" {
		return missingContinuationLeaseRequirement{}, fmt.Errorf("request_approval continuation principal is required")
	}
	if len(requirement.AllowedActions) == 0 {
		return missingContinuationLeaseRequirement{}, fmt.Errorf("request_approval continuation allowed_actions is required")
	}
	if requirement.LeaseClass == session.ContinuationLeaseClassChildWake {
		if requirement.Tool != "durable_agent" || requirement.ToolAction != "wake_once" {
			return missingContinuationLeaseRequirement{}, fmt.Errorf("child_wake lease requests must target durable_agent wake_once")
		}
		if requirement.AgentID == "" {
			return missingContinuationLeaseRequirement{}, fmt.Errorf("child_wake lease requests require agent_id")
		}
		if !operationStringSliceContains(requirement.AllowedActions, durableAgentWakeOnceAction) {
			return missingContinuationLeaseRequirement{}, fmt.Errorf("child_wake lease requests require %s action", durableAgentWakeOnceAction)
		}
		if got := strings.TrimSpace(requirement.Constraints["agent_id"]); got != "" && got != requirement.AgentID {
			return missingContinuationLeaseRequirement{}, fmt.Errorf("child_wake lease request agent_id constraint mismatch")
		}
		requirement.Constraints["agent_id"] = requirement.AgentID
	}
	return requirement, nil
}

func requestApprovalContinuationLeaseSummary(requirement missingContinuationLeaseRequirement) string {
	if requirement.LeaseClass == session.ContinuationLeaseClassChildWake && requirement.AgentID != "" {
		return fmt.Sprintf("Approve one no-content wake/readiness turn for %s", requirement.AgentID)
	}
	if requirement.Tool != "" && requirement.ToolAction != "" {
		return fmt.Sprintf("Approve one %s lease for %s %s", requirement.LeaseClass, requirement.Tool, requirement.ToolAction)
	}
	return fmt.Sprintf("Approve one %s continuation lease", requirement.LeaseClass)
}

func requestApprovalContinuationLeaseBoundedEffect(requirement missingContinuationLeaseRequirement) string {
	if requirement.LeaseClass == session.ContinuationLeaseClassChildWake && requirement.AgentID != "" {
		return fmt.Sprintf("Permit durable_agent wake_once to wake only %s once, consume the pending parent guidance batch, and stop after one child result or pre-child failure.", requirement.AgentID)
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
	return constraints
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

func requestApprovalToolDefinition() agent.ToolDef {
	return agent.ToolDef{
		Name:        requestApprovalToolName,
		Description: "Request a button-backed continuation approval card from a bounded phase contract. This only offers approval; it does not grant authority or execute the work.",
		Parameters: json.RawMessage(`{
			"type": "object",
			"properties": {
				"action": {"type": "string", "enum": ["request_continuation_lease"], "description": "Use request_continuation_lease to materialize a continuation lease requested by a typed blocker instead of a phase approval"},
				"objective": {"type": "string", "description": "Optional operation objective or plan goal this approval serves"},
				"lease_class": {"type": "string", "description": "Continuation lease class for request_continuation_lease, such as child_wake or data_access"},
				"principal": {"type": "string", "description": "Canonical principal that needs the continuation lease"},
				"allowed_actions": {"type": "array", "items": {"type": "string"}, "description": "Exact lease actions to authorize for request_continuation_lease"},
				"constraints": {"type": "object", "description": "Exact lease constraints, such as agent_id for child_wake"},
				"tool": {"type": "string", "description": "Tool that will consume the requested lease"},
				"tool_action": {"type": "string", "description": "Tool action that will consume the requested lease"},
				"grant_id": {"type": "string", "description": "Capability grant that already authorizes the tool/resource but still needs a continuation lease"},
				"grant_target_resource": {"type": "string", "description": "Capability grant target resource"},
				"agent_id": {"type": "string", "description": "Named durable child for child_wake lease requests"},
				"resource": {"type": "string", "description": "Named resource for data/resource lease requests"},
				"retry_after_lease": {"type": "boolean", "description": "True when the blocked invocation should be retried only after the lease is approved"},
				"phase": {
					"type": "object",
					"description": "Bounded approval phase to materialize into a continuation approval card",
					"properties": {
						"id": {"type": "string", "description": "Optional stable phase id; generated when omitted"},
						"summary": {"type": "string", "description": "Approval-card summary / next step"},
						"status": {"type": "string", "enum": ["pending"], "description": "Must be pending when supplied"},
						"authority_class": {"type": "string", "description": "Authority/risk class such as read_only_review, workspace_write, commit, deploy, or system_change"},
						"why_now": {"type": "string", "description": "Why this approval should be offered now"},
						"bounded_effect": {"type": "string", "description": "What approval permits, including stop conditions"},
						"allowed_actions": {"type": "array", "items": {"type": "string"}, "description": "Allowed action labels for this approval"},
						"forbidden_actions": {"type": "array", "items": {"type": "string"}, "description": "Forbidden action labels / stop boundaries"},
						"validation_plan": {"type": "array", "items": {"type": "string"}, "description": "Evidence expected after approved work"},
						"required_capability_grants": {
							"type": "array",
							"description": "Capability grants that are approved together with this phase when the operator approves the continuation. Use only for capability requests that already exist and are visibly required by the bounded phase.",
							"items": {
								"type": "object",
								"properties": {
									"request_id": {"type": "string", "description": "Existing capability_request id to approve with this phase"},
									"grant_id": {"type": "string", "description": "Optional explicit grant id to create"},
									"kind": {"type": "string", "description": "Capability kind, e.g. external_account, tool, file_access, network_access, or generic_delegation"},
									"target_resource": {"type": "string", "description": "Capability target such as github, a repo path, or a tool name"},
									"granted_to": {"type": "string", "description": "Principal receiving the grant"},
									"allowed_actions": {"type": "array", "items": {"type": "string"}, "description": "Allowed grant actions such as read, write, or invoke"},
									"contract": {"description": "Optional grant contract JSON copied into capability grant state"},
									"constraints": {"description": "Optional grant constraints JSON copied into capability grant state"},
									"expires_at": {"type": "string", "description": "Optional absolute expiration timestamp"}
								},
								"required": ["request_id"]
							}
						},
						"gate_level": {"type": "string", "enum": ["normal_approval", "escalated_operator_approval", "hard_consent_block"], "description": "Typed approval gate"},
						"gate_reason_code": {"type": "string", "description": "Typed gate reason such as external_account_auth_status, credential_metadata_check, capability_grant, deploy, or workspace_write"},
						"approval_subject": {"type": "string", "description": "Who can satisfy this gate: operator, third_party, or resource_owner"},
						"autoapprove_eligible": {"type": "boolean", "description": "Ignored by this tool; request_approval forces manual buttons"},
						"blocked_reason_code": {"type": "string", "description": "Typed blocker code if approval must be blocked instead of offered"},
						"requires_consent": {"type": "boolean", "description": "True when explicit consent is required"},
						"requires_opt_in": {"type": "boolean", "description": "True when explicit opt-in is required"},
						"supersedes_phase_ids": {"type": "array", "items": {"type": "string"}, "description": "Phase ids superseded by this approval"},
						"stale_authority": {"type": "boolean", "description": "True when this request is stale and must not be offered"},
						"requires_approval": {"type": "boolean", "description": "Ignored by this tool; request_approval always requires approval"}
					},
					"required": ["summary"]
				}
			},
			"anyOf": [
				{"required": ["phase"]},
				{"required": ["action", "lease_class", "principal", "allowed_actions"]}
			]
		}`),
	}
}
