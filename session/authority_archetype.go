//go:build linux

package session

import (
	"encoding/json"
	"sort"
	"strings"
)

type RedactedAuthorityArchetype struct {
	Kind                         string   `json:"kind"`
	LeaseClass                   string   `json:"lease_class,omitempty"`
	SubjectKind                  string   `json:"subject_kind,omitempty"`
	Tool                         string   `json:"tool,omitempty"`
	ToolAction                   string   `json:"tool_action,omitempty"`
	AllowedActions               []string `json:"allowed_actions,omitempty"`
	ConstraintKeys               []string `json:"constraint_keys,omitempty"`
	ResourceClass                string   `json:"resource_class,omitempty"`
	RetryOperationKind           string   `json:"retry_operation_kind,omitempty"`
	RetryTool                    string   `json:"retry_tool,omitempty"`
	RetryAction                  string   `json:"retry_action,omitempty"`
	RequiredGrantKinds           []string `json:"required_grant_kinds,omitempty"`
	RequiredGrantResourceClasses []string `json:"required_grant_resource_classes,omitempty"`
	RequiredGrantActions         []string `json:"required_grant_actions,omitempty"`
	ComponentKinds               []string `json:"component_kinds,omitempty"`
	StopConditionCount           int      `json:"stop_condition_count,omitempty"`
}

func RedactedContinuationRecoveryArchetype(contract ContinuationRecoveryContract) RedactedAuthorityArchetype {
	contract = NormalizeContinuationRecoveryContract(contract)
	return RedactedAuthorityArchetype{
		Kind:               "continuation_recovery_contract",
		LeaseClass:         string(contract.LeaseClass),
		SubjectKind:        normalizeEnumValue(contract.SubjectKind),
		Tool:               normalizeEnumValue(contract.Tool),
		ToolAction:         normalizeEnumValue(contract.ToolAction),
		AllowedActions:     normalizeActionStringSlice(contract.AllowedActions),
		ConstraintKeys:     sortedMapKeys(contract.Constraints),
		ResourceClass:      continuationRecoveryResourceClass(contract),
		RetryOperationKind: normalizeEnumValue(contract.RetryOperation.OperationKind),
		RetryTool:          normalizeEnumValue(contract.RetryOperation.Tool),
		RetryAction:        retryOperationActionClass(contract.RetryOperation.InputJSON),
	}
}

func RedactedAuthorityBundleArchetype(bundle AuthorityBundleContract) RedactedAuthorityArchetype {
	bundle = NormalizeAuthorityBundleContract(bundle)
	out := RedactedAuthorityArchetype{
		Kind:               "authority_bundle",
		AllowedActions:     normalizeActionStringSlice(bundle.AllowedActions),
		ComponentKinds:     authorityBundleComponentKinds(bundle.Components),
		StopConditionCount: len(bundle.StopConditions),
	}
	kindSet := map[string]struct{}{}
	resourceSet := map[string]struct{}{}
	actionSet := map[string]struct{}{}
	for _, spec := range bundle.RequiredCapabilityGrants {
		spec = NormalizeCapabilityGrantSpec(spec)
		if spec.Kind != "" {
			kindSet[string(spec.Kind)] = struct{}{}
		}
		if resource := AuthorityResourceClass(spec.TargetResource); resource != "" {
			resourceSet[resource] = struct{}{}
		}
		for _, action := range spec.AllowedActions {
			if action != "" {
				actionSet[action] = struct{}{}
			}
		}
	}
	out.RequiredGrantKinds = sortedSetValues(kindSet)
	out.RequiredGrantResourceClasses = sortedSetValues(resourceSet)
	out.RequiredGrantActions = sortedSetValues(actionSet)
	return out
}

func continuationRecoveryResourceClass(contract ContinuationRecoveryContract) string {
	switch {
	case strings.TrimSpace(contract.GrantTargetResource) != "":
		return AuthorityResourceClass(contract.GrantTargetResource)
	case strings.TrimSpace(contract.Resource) != "":
		return AuthorityResourceClass(contract.Resource)
	case strings.TrimSpace(contract.AgentID) != "":
		return "durable_agent"
	default:
		return ""
	}
}

func retryOperationActionClass(inputJSON string) string {
	var payload map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(inputJSON)), &payload); err != nil {
		return ""
	}
	action, _ := payload["action"].(string)
	return normalizeEnumValue(action)
}

func authorityBundleComponentKinds(components []AuthorityBundleComponent) []string {
	seen := map[string]struct{}{}
	for _, component := range normalizeAuthorityBundleComponents(components) {
		if component.Kind != "" {
			seen[component.Kind] = struct{}{}
		}
	}
	return sortedSetValues(seen)
}

func sortedMapKeys(values map[string]string) []string {
	out := make([]string, 0, len(values))
	for key := range values {
		key = normalizeEnumValue(key)
		if key != "" {
			out = append(out, key)
		}
	}
	sort.Strings(out)
	return out
}

func sortedSetValues(values map[string]struct{}) []string {
	out := make([]string, 0, len(values))
	for value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			out = append(out, value)
		}
	}
	sort.Strings(out)
	return out
}
