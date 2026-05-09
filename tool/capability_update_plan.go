//go:build linux

package tool

import (
	"encoding/json"
	"fmt"
	"strings"
)

const capabilityUpdatePlanContractKey = "capability_update_plan"

type capabilityUpdatePlanInput struct {
	AgentID         string                            `json:"agent_id,omitempty"`
	PolicyPatch     *durableAgentPolicyPatchInput     `json:"policy_patch,omitempty"`
	PolicyOverrides *durableAgentPolicyOverridesInput `json:"policy_overrides,omitempty"`
	Provisioning    []string                          `json:"provisioning,omitempty"`
	Attestation     []string                          `json:"attestation,omitempty"`
	GrantActions    []string                          `json:"grant_actions,omitempty"`
	Reason          string                            `json:"reason,omitempty"`
	Notes           string                            `json:"notes,omitempty"`
}

func normalizeCapabilityUpdatePlan(plan capabilityUpdatePlanInput) capabilityUpdatePlanInput {
	plan.AgentID = strings.TrimSpace(plan.AgentID)
	plan.Provisioning = normalizeDurableAgentDelegationStrings(plan.Provisioning)
	plan.Attestation = normalizeDurableAgentDelegationStrings(plan.Attestation)
	plan.GrantActions = normalizeDurableAgentDelegationStrings(plan.GrantActions)
	plan.Reason = strings.TrimSpace(plan.Reason)
	plan.Notes = strings.TrimSpace(plan.Notes)
	if plan.PolicyPatch != nil {
		patch := *plan.PolicyPatch
		patch.Charter = strings.TrimSpace(patch.Charter)
		patch.Autonomy = strings.TrimSpace(patch.Autonomy)
		patch.Visibility = strings.TrimSpace(patch.Visibility)
		patch.SharedContext = strings.TrimSpace(patch.SharedContext)
		if patch.Capabilities != nil {
			patch.Capabilities = normalizePolicyCapabilities(patch.Capabilities)
			if patch.Capabilities == nil {
				patch.Capabilities = []string{}
			}
		}
		patch.DriftPolicy = strings.TrimSpace(patch.DriftPolicy)
		if durableAgentPolicyPatchInputIsZero(patch) {
			plan.PolicyPatch = nil
		} else {
			plan.PolicyPatch = &patch
		}
	}
	if plan.PolicyOverrides != nil {
		overrides := *plan.PolicyOverrides
		overrides.OutboundMode = strings.TrimSpace(overrides.OutboundMode)
		overrides.PublicSurfaceMode = strings.TrimSpace(overrides.PublicSurfaceMode)
		overrides.SharedInferenceReuse = strings.TrimSpace(overrides.SharedInferenceReuse)
		overrides.SharedInferenceReuseScope = strings.TrimSpace(overrides.SharedInferenceReuseScope)
		overrides.TailnetMode = strings.TrimSpace(overrides.TailnetMode)
		overrides.TailnetHostname = strings.TrimSpace(overrides.TailnetHostname)
		if overrides.TailnetTags != nil {
			overrides.TailnetTags = normalizePolicyCapabilities(overrides.TailnetTags)
			if overrides.TailnetTags == nil {
				overrides.TailnetTags = []string{}
			}
		}
		overrides.TailnetSurfacePolicy = strings.TrimSpace(overrides.TailnetSurfacePolicy)
		if durableAgentPolicyOverridesInputIsZero(overrides) {
			plan.PolicyOverrides = nil
		} else {
			plan.PolicyOverrides = &overrides
		}
	}
	return plan
}

func capabilityUpdatePlanIsZero(plan capabilityUpdatePlanInput) bool {
	plan = normalizeCapabilityUpdatePlan(plan)
	return plan.AgentID == "" &&
		plan.PolicyPatch == nil &&
		plan.PolicyOverrides == nil &&
		len(plan.Provisioning) == 0 &&
		len(plan.Attestation) == 0 &&
		len(plan.GrantActions) == 0 &&
		plan.Reason == "" &&
		plan.Notes == ""
}

func capabilityUpdatePlanHasDurablePolicyPatch(plan capabilityUpdatePlanInput) bool {
	plan = normalizeCapabilityUpdatePlan(plan)
	return plan.PolicyPatch != nil || plan.PolicyOverrides != nil
}

func capabilityUpdatePlanHasOperationalContent(plan capabilityUpdatePlanInput) bool {
	plan = normalizeCapabilityUpdatePlan(plan)
	return plan.PolicyPatch != nil ||
		plan.PolicyOverrides != nil ||
		len(plan.Provisioning) > 0 ||
		len(plan.Attestation) > 0 ||
		len(plan.GrantActions) > 0 ||
		plan.Reason != "" ||
		plan.Notes != ""
}

func durableAgentPolicyPatchInputIsZero(patch durableAgentPolicyPatchInput) bool {
	return strings.TrimSpace(patch.Mode) == "" &&
		strings.TrimSpace(patch.Charter) == "" &&
		strings.TrimSpace(patch.Autonomy) == "" &&
		strings.TrimSpace(patch.Visibility) == "" &&
		strings.TrimSpace(patch.SharedContext) == "" &&
		patch.Capabilities == nil &&
		strings.TrimSpace(patch.DriftPolicy) == ""
}

func durableAgentPolicyOverridesInputIsZero(overrides durableAgentPolicyOverridesInput) bool {
	return strings.TrimSpace(overrides.OutboundMode) == "" &&
		strings.TrimSpace(overrides.PublicSurfaceMode) == "" &&
		strings.TrimSpace(overrides.SharedInferenceReuse) == "" &&
		strings.TrimSpace(overrides.SharedInferenceReuseScope) == "" &&
		strings.TrimSpace(overrides.TailnetMode) == "" &&
		strings.TrimSpace(overrides.TailnetHostname) == "" &&
		overrides.TailnetTags == nil &&
		strings.TrimSpace(overrides.TailnetSurfacePolicy) == ""
}

func mergeCapabilityUpdatePlan(existing capabilityUpdatePlanInput, next capabilityUpdatePlanInput) capabilityUpdatePlanInput {
	existing = normalizeCapabilityUpdatePlan(existing)
	next = normalizeCapabilityUpdatePlan(next)
	if next.AgentID != "" {
		existing.AgentID = next.AgentID
	}
	if next.PolicyPatch != nil {
		patch := durableAgentPolicyPatchInput{}
		if existing.PolicyPatch != nil {
			patch = *existing.PolicyPatch
		}
		if next.PolicyPatch.Mode != "" {
			patch.Mode = next.PolicyPatch.Mode
		}
		if next.PolicyPatch.Charter != "" {
			patch.Charter = next.PolicyPatch.Charter
		}
		if next.PolicyPatch.Autonomy != "" {
			patch.Autonomy = next.PolicyPatch.Autonomy
		}
		if next.PolicyPatch.Visibility != "" {
			patch.Visibility = next.PolicyPatch.Visibility
		}
		if next.PolicyPatch.SharedContext != "" {
			patch.SharedContext = next.PolicyPatch.SharedContext
		}
		if next.PolicyPatch.Capabilities != nil {
			patch.Capabilities = append([]string(nil), next.PolicyPatch.Capabilities...)
		}
		if next.PolicyPatch.DriftPolicy != "" {
			patch.DriftPolicy = next.PolicyPatch.DriftPolicy
		}
		existing.PolicyPatch = &patch
	}
	if next.PolicyOverrides != nil {
		overrides := durableAgentPolicyOverridesInput{}
		if existing.PolicyOverrides != nil {
			overrides = *existing.PolicyOverrides
		}
		if next.PolicyOverrides.OutboundMode != "" {
			overrides.OutboundMode = next.PolicyOverrides.OutboundMode
		}
		if next.PolicyOverrides.PublicSurfaceMode != "" {
			overrides.PublicSurfaceMode = next.PolicyOverrides.PublicSurfaceMode
		}
		if next.PolicyOverrides.SharedInferenceReuse != "" {
			overrides.SharedInferenceReuse = next.PolicyOverrides.SharedInferenceReuse
		}
		if next.PolicyOverrides.SharedInferenceReuseScope != "" {
			overrides.SharedInferenceReuseScope = next.PolicyOverrides.SharedInferenceReuseScope
		}
		if next.PolicyOverrides.TailnetMode != "" {
			overrides.TailnetMode = next.PolicyOverrides.TailnetMode
		}
		if next.PolicyOverrides.TailnetHostname != "" {
			overrides.TailnetHostname = next.PolicyOverrides.TailnetHostname
		}
		if next.PolicyOverrides.TailnetTags != nil {
			overrides.TailnetTags = append([]string(nil), next.PolicyOverrides.TailnetTags...)
		}
		if next.PolicyOverrides.TailnetSurfacePolicy != "" {
			overrides.TailnetSurfacePolicy = next.PolicyOverrides.TailnetSurfacePolicy
		}
		existing.PolicyOverrides = &overrides
	}
	if len(next.Provisioning) > 0 {
		existing.Provisioning = next.Provisioning
	}
	if len(next.Attestation) > 0 {
		existing.Attestation = next.Attestation
	}
	if len(next.GrantActions) > 0 {
		existing.GrantActions = next.GrantActions
	}
	if next.Reason != "" {
		existing.Reason = next.Reason
	}
	if next.Notes != "" {
		existing.Notes = next.Notes
	}
	return normalizeCapabilityUpdatePlan(existing)
}

func capabilityUpdatePlanFromDurableDelegation(agentID string, input durableAgentDelegationRequestInput) capabilityUpdatePlanInput {
	plan := capabilityUpdatePlanInput{}
	if input.CapabilityUpdatePlan != nil {
		plan = *input.CapabilityUpdatePlan
	}
	next := capabilityUpdatePlanInput{
		AgentID:         strings.TrimSpace(agentID),
		PolicyPatch:     input.PolicyPatch,
		PolicyOverrides: input.PolicyOverrides,
		Provisioning:    input.Provisioning,
		Attestation:     input.Attestation,
		GrantActions:    input.GrantActions,
		Reason:          input.UpdateReason,
	}
	merged := mergeCapabilityUpdatePlan(plan, next)
	if !capabilityUpdatePlanHasOperationalContent(merged) {
		return capabilityUpdatePlanInput{}
	}
	return merged
}

func capabilityUpdatePlanFromCapabilityInput(input capabilityInput) capabilityUpdatePlanInput {
	if input.CapabilityUpdatePlan == nil {
		return capabilityUpdatePlanInput{}
	}
	return normalizeCapabilityUpdatePlan(*input.CapabilityUpdatePlan)
}

func mergeCapabilityUpdatePlanIntoContract(contract string, plan capabilityUpdatePlanInput) (string, error) {
	plan = normalizeCapabilityUpdatePlan(plan)
	if capabilityUpdatePlanIsZero(plan) {
		return strings.TrimSpace(contract), nil
	}
	obj := make(map[string]any)
	trimmed := strings.TrimSpace(contract)
	if trimmed == "" {
		trimmed = "{}"
	}
	if err := json.Unmarshal([]byte(trimmed), &obj); err != nil {
		return "", fmt.Errorf("capability contract must be a json object when capability_update_plan is provided: %w", err)
	}
	if obj == nil {
		obj = make(map[string]any)
	}
	if rawExisting, ok := obj[capabilityUpdatePlanContractKey]; ok && rawExisting != nil {
		existingRaw, err := json.Marshal(rawExisting)
		if err != nil {
			return "", fmt.Errorf("marshal existing capability_update_plan: %w", err)
		}
		var existing capabilityUpdatePlanInput
		if err := json.Unmarshal(existingRaw, &existing); err != nil {
			return "", fmt.Errorf("decode existing capability_update_plan: %w", err)
		}
		plan = mergeCapabilityUpdatePlan(existing, plan)
	}
	obj[capabilityUpdatePlanContractKey] = plan
	updated, err := json.Marshal(obj)
	if err != nil {
		return "", fmt.Errorf("marshal capability contract with update plan: %w", err)
	}
	return string(updated), nil
}

func capabilityUpdatePlanFromContract(contract string) (capabilityUpdatePlanInput, bool, error) {
	trimmed := strings.TrimSpace(contract)
	if trimmed == "" {
		return capabilityUpdatePlanInput{}, false, nil
	}
	var decoded any
	if err := json.Unmarshal([]byte(trimmed), &decoded); err != nil {
		return capabilityUpdatePlanInput{}, false, fmt.Errorf("decode capability contract: %w", err)
	}
	obj, ok := decoded.(map[string]any)
	if !ok {
		return capabilityUpdatePlanInput{}, false, nil
	}
	rawValue, ok := obj[capabilityUpdatePlanContractKey]
	if !ok || rawValue == nil {
		return capabilityUpdatePlanInput{}, false, nil
	}
	raw, err := json.Marshal(rawValue)
	if err != nil {
		return capabilityUpdatePlanInput{}, false, fmt.Errorf("marshal capability_update_plan: %w", err)
	}
	var plan capabilityUpdatePlanInput
	if err := json.Unmarshal(raw, &plan); err != nil {
		return capabilityUpdatePlanInput{}, false, fmt.Errorf("decode capability_update_plan: %w", err)
	}
	plan = normalizeCapabilityUpdatePlan(plan)
	return plan, !capabilityUpdatePlanIsZero(plan), nil
}
