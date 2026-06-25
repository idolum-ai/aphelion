//go:build linux

package tool

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/idolum-ai/aphelion/commandeffect"
	"github.com/idolum-ai/aphelion/session"
)

const recoveryHandoffContractVersion = "aphelion.recovery_handoff.v1"

type recoveryHandoffOperation struct {
	Kind      string
	Tool      string
	InputJSON string
}

func compileRecoveryHandoffOperation(kind string, toolName string, payload map[string]any) (recoveryHandoffOperation, error) {
	kind = normalizeShellAlternativeToken(kind)
	toolName = strings.TrimSpace(toolName)
	if kind == "" || toolName == "" {
		return recoveryHandoffOperation{}, fmt.Errorf("recovery handoff requires operation kind and tool")
	}
	if payload == nil {
		payload = map[string]any{}
	}
	payload["recovery_contract"] = recoveryHandoffContractVersion
	payload["recovery_operation_kind"] = kind
	raw, err := json.Marshal(payload)
	if err != nil {
		return recoveryHandoffOperation{}, err
	}
	return recoveryHandoffOperation{Kind: kind, Tool: toolName, InputJSON: string(raw)}, nil
}

func compileContinuationLeaseRecoveryHandoff(requirement missingContinuationLeaseRequirement) (recoveryHandoffOperation, error) {
	requirement = normalizeMissingContinuationLeaseRequirement(requirement)
	if missingContinuationLeaseSubjectToken(requirement) == "" || requirement.Principal == "" || requirement.LeaseClass == "" {
		return recoveryHandoffOperation{}, fmt.Errorf("incomplete continuation lease recovery handoff")
	}
	payload := map[string]any{
		"action":                "request_continuation_lease",
		"lease_class":           string(requirement.LeaseClass),
		"principal":             requirement.Principal,
		"allowed_actions":       requirement.AllowedActions,
		"constraints":           requirement.Constraints,
		"tool":                  requirement.Tool,
		"tool_action":           requirement.ToolAction,
		"grant_id":              requirement.GrantID,
		"grant_target_resource": requirement.GrantTargetResource,
		"request_instance_id":   requirement.RequestInstanceID,
		"retry_after_lease":     true,
	}
	if requirement.AgentID != "" {
		payload["agent_id"] = requirement.AgentID
	}
	if requirement.Resource != "" {
		payload["resource"] = requirement.Resource
	}
	op, err := compileRecoveryHandoffOperation("continuation_lease_request", "request_approval", payload)
	if err != nil {
		return recoveryHandoffOperation{}, err
	}
	if err := validateRecoveryHandoffToolInput(session.NextActionBlockedNeedsAuthority, op.Tool, op.InputJSON); err != nil {
		return recoveryHandoffOperation{}, err
	}
	return op, nil
}

func compileCapabilityGrantRecoveryHandoff(request session.CapabilityRequest, requirement missingGrantRequirement) (recoveryHandoffOperation, error) {
	requirement = normalizeMissingGrantRequirement(requirement)
	payload := map[string]any{
		"action":            "grant_set",
		"request_id":        request.RequestID,
		"kind":              string(requirement.Kind),
		"target_resource":   requirement.TargetResource,
		"principal":         requirement.GrantedTo,
		"allowed_actions":   requirement.AllowedActions,
		"contract":          json.RawMessage(requirement.Contract),
		"constraints":       json.RawMessage(requirement.Constraints),
		"grant_status":      string(session.CapabilityGrantStatusActive),
		"retry_after_grant": true,
	}
	kind := firstNonEmpty(requirement.OperationKind, "capability_grant_review")
	toolName := firstNonEmpty(requirement.OperationTool, "capability_authority")
	op, err := compileRecoveryHandoffOperation(kind, toolName, payload)
	if err != nil {
		return recoveryHandoffOperation{}, err
	}
	if err := validateRecoveryHandoffToolInput(session.NextActionBlockedNeedsAuthority, op.Tool, op.InputJSON); err != nil {
		return recoveryHandoffOperation{}, err
	}
	return op, nil
}

func shellAlternativePayload(input map[string]any) map[string]any {
	out := make(map[string]any, len(input)+4)
	for key, value := range input {
		out[key] = value
	}
	out["recommendation_only"] = true
	out["authority_model"] = "existing_tool_or_lease_membrane_required"
	out["recovery_contract"] = recoveryHandoffContractVersion
	return out
}

func validateRecoveryHandoffToolInput(state session.NextActionState, toolName string, raw string) error {
	toolName = strings.TrimSpace(toolName)
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return fmt.Errorf("recovery handoff %s requires operation input", toolName)
	}
	switch toolName {
	case "request_approval":
		var in requestApprovalInput
		if err := decodeToolObjectInput(json.RawMessage(raw), &in, "request_approval"); err != nil {
			return err
		}
		if requestApprovalActionToken(in.Action) != "request_continuation_lease" {
			return fmt.Errorf("request_approval recovery handoff must request a continuation lease")
		}
		_, err := requestApprovalContinuationLeaseRequirement(in)
		return err
	case "capability_authority":
		var in capabilityInput
		if err := decodeToolObjectInput(json.RawMessage(raw), &in, "capability_authority"); err != nil {
			return err
		}
		if strings.TrimSpace(in.Action) != "grant_set" {
			return fmt.Errorf("capability_authority recovery handoff must grant_set")
		}
		if strings.TrimSpace(in.RequestID) == "" || strings.TrimSpace(in.TargetResource) == "" || strings.TrimSpace(in.Principal) == "" {
			return fmt.Errorf("capability_authority recovery handoff missing request, target, or principal")
		}
		return nil
	case "read_file":
		var in readFileInput
		if err := decodeToolObjectInput(json.RawMessage(raw), &in, "read_file"); err != nil {
			return err
		}
		if strings.TrimSpace(in.Path) == "" {
			return fmt.Errorf("read_file recovery handoff requires path")
		}
		return nil
	case "list_dir":
		var in listDirInput
		if err := decodeToolObjectInput(json.RawMessage(raw), &in, "list_dir"); err != nil {
			return err
		}
		if strings.TrimSpace(in.Path) == "" {
			return fmt.Errorf("list_dir recovery handoff requires path")
		}
		return nil
	case "search":
		var in searchFilesInput
		if err := decodeToolObjectInput(json.RawMessage(raw), &in, "search"); err != nil {
			return err
		}
		if strings.TrimSpace(in.Query) == "" {
			return fmt.Errorf("search recovery handoff requires query")
		}
		return nil
	case "exec":
		var in execInput
		if err := decodeToolObjectInput(json.RawMessage(raw), &in, "exec"); err != nil {
			return err
		}
		if strings.TrimSpace(in.Command) == "" {
			return fmt.Errorf("exec recovery handoff requires command")
		}
		if err := validateExecEffectPlanDispatchable(commandeffect.PlanCommand(in.Command)); err != nil {
			return err
		}
		return nil
	case "system_log_read":
		var decoded map[string]any
		if err := decodeToolObjectInput(json.RawMessage(raw), &decoded, "system_log_read"); err != nil {
			return err
		}
		unit, ok := decoded["unit"].(string)
		if !ok || strings.TrimSpace(unit) == "" {
			return fmt.Errorf("system_log_read recovery handoff requires unit")
		}
		return nil
	case "update_operation":
		var in updateOperationInput
		if err := decodeToolObjectInput(json.RawMessage(raw), &in, "update_operation"); err != nil {
			return err
		}
		if state == session.NextActionReadyToExecute {
			return fmt.Errorf("update_operation recovery handoff is advisory and must not be ready_to_execute")
		}
		return nil
	default:
		if state == session.NextActionReadyToExecute {
			return fmt.Errorf("ready recovery handoff names unknown executable tool %q", toolName)
		}
		return nil
	}
}
