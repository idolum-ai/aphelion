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
				"objective": {"type": "string", "description": "Optional operation objective or plan goal this approval serves"},
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
			"required": ["phase"]
		}`),
	}
}
