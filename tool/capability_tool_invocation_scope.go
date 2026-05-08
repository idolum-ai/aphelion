//go:build linux

package tool

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/idolum-ai/aphelion/session"
)

// capabilityToolInvocationScope is the first-class grant/exposure constraint for
// authority-managed external tool invocations. It lives in a capability grant's
// constraints JSON under "tool_invocation" so the grant record can encode the
// input action and selector bounds instead of relying only on phase prose or tool
// implementation checks.
type capabilityToolInvocationScope struct {
	Actions               map[string]capabilityToolInvocationActionScope `json:"actions,omitempty"`
	AllowAdditionalFields bool                                           `json:"allow_additional_fields,omitempty"`
}

type capabilityToolInvocationActionScope struct {
	Selectors             map[string][]string `json:"selectors,omitempty"`
	RequiredSelectors     []string            `json:"required_selectors,omitempty"`
	AllowedFields         []string            `json:"allowed_fields,omitempty"`
	AllowAdditionalFields *bool               `json:"allow_additional_fields,omitempty"`
}

type capabilityToolInvocationScopeWrapper struct {
	ToolInvocation *capabilityToolInvocationScope `json:"tool_invocation,omitempty"`
}

func validateCapabilityToolInvocationScopeJSON(contractJSON string, constraintsJSON string) error {
	_, _, err := capabilityToolInvocationScopeFromJSON(contractJSON, constraintsJSON)
	return err
}

func capabilityGrantToolInvocationScope(grant session.CapabilityGrant) (capabilityToolInvocationScope, bool, error) {
	return capabilityToolInvocationScopeFromJSON(grant.Contract, grant.Constraints)
}

func capabilityToolInvocationScopeFromJSON(contractJSON string, constraintsJSON string) (capabilityToolInvocationScope, bool, error) {
	var merged capabilityToolInvocationScope
	found := false
	for _, source := range []struct {
		name string
		raw  string
	}{
		{name: "contract", raw: contractJSON},
		{name: "constraints", raw: constraintsJSON},
	} {
		raw := strings.TrimSpace(source.raw)
		if raw == "" || raw == "{}" {
			continue
		}
		var wrapper capabilityToolInvocationScopeWrapper
		if err := json.Unmarshal([]byte(raw), &wrapper); err != nil {
			return capabilityToolInvocationScope{}, false, fmt.Errorf("decode %s tool_invocation scope: %w", source.name, err)
		}
		if wrapper.ToolInvocation == nil {
			continue
		}
		next, err := normalizeCapabilityToolInvocationScope(*wrapper.ToolInvocation)
		if err != nil {
			return capabilityToolInvocationScope{}, false, fmt.Errorf("invalid %s tool_invocation scope: %w", source.name, err)
		}
		merged = mergeCapabilityToolInvocationScopes(merged, next)
		found = true
	}
	if !found {
		return capabilityToolInvocationScope{}, false, nil
	}
	return merged, true, nil
}

func normalizeCapabilityToolInvocationScope(scope capabilityToolInvocationScope) (capabilityToolInvocationScope, error) {
	normalized := capabilityToolInvocationScope{
		Actions:               make(map[string]capabilityToolInvocationActionScope, len(scope.Actions)),
		AllowAdditionalFields: scope.AllowAdditionalFields,
	}
	for action, actionScope := range scope.Actions {
		action = normalizeToolInvocationToken(action)
		if action == "" {
			return capabilityToolInvocationScope{}, fmt.Errorf("action names must be non-empty")
		}
		next := capabilityToolInvocationActionScope{
			Selectors:             make(map[string][]string, len(actionScope.Selectors)),
			RequiredSelectors:     normalizeUniqueStrings(actionScope.RequiredSelectors),
			AllowedFields:         normalizeUniqueStrings(actionScope.AllowedFields),
			AllowAdditionalFields: actionScope.AllowAdditionalFields,
		}
		for name, values := range actionScope.Selectors {
			name = strings.TrimSpace(name)
			if name == "" {
				return capabilityToolInvocationScope{}, fmt.Errorf("selector names for action %q must be non-empty", action)
			}
			cleanValues := make([]string, 0, len(values))
			seenValues := map[string]struct{}{}
			for _, value := range values {
				value = strings.TrimSpace(value)
				if value == "" {
					continue
				}
				if _, ok := seenValues[value]; ok {
					continue
				}
				seenValues[value] = struct{}{}
				cleanValues = append(cleanValues, value)
			}
			if len(cleanValues) == 0 {
				return capabilityToolInvocationScope{}, fmt.Errorf("selector %q for action %q must list at least one allowed value", name, action)
			}
			next.Selectors[name] = cleanValues
		}
		for _, required := range next.RequiredSelectors {
			if required == "" {
				continue
			}
			if _, ok := next.Selectors[required]; !ok {
				return capabilityToolInvocationScope{}, fmt.Errorf("required selector %q for action %q has no allowed selector values", required, action)
			}
		}
		normalized.Actions[action] = next
	}
	if len(normalized.Actions) == 0 {
		return capabilityToolInvocationScope{}, fmt.Errorf("tool_invocation scope requires at least one action")
	}
	return normalized, nil
}

func mergeCapabilityToolInvocationScopes(existing capabilityToolInvocationScope, next capabilityToolInvocationScope) capabilityToolInvocationScope {
	if existing.Actions == nil {
		existing.Actions = map[string]capabilityToolInvocationActionScope{}
	}
	if next.AllowAdditionalFields {
		existing.AllowAdditionalFields = true
	}
	for action, actionScope := range next.Actions {
		existing.Actions[action] = actionScope
	}
	return existing
}

func validateCapabilityToolInvocationInput(grant session.CapabilityGrant, input json.RawMessage) error {
	scope, ok, err := capabilityGrantToolInvocationScope(grant)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	if len(input) == 0 {
		return fmt.Errorf("tool invocation blocked by grant %s: input payload is required by tool_invocation scope", grant.GrantID)
	}
	var payload map[string]json.RawMessage
	decoder := json.NewDecoder(bytes.NewReader(input))
	decoder.UseNumber()
	if err := decoder.Decode(&payload); err != nil {
		return fmt.Errorf("tool invocation blocked by grant %s: input must be a JSON object: %w", grant.GrantID, err)
	}
	if payload == nil {
		return fmt.Errorf("tool invocation blocked by grant %s: input must be a JSON object", grant.GrantID)
	}
	actionRaw, ok := payload["action"]
	if !ok {
		return fmt.Errorf("tool invocation blocked by grant %s: input action is required by tool_invocation scope", grant.GrantID)
	}
	actionValue, ok := scalarJSONString(actionRaw)
	if !ok || strings.TrimSpace(actionValue) == "" {
		return fmt.Errorf("tool invocation blocked by grant %s: input action must be a string", grant.GrantID)
	}
	action := normalizeToolInvocationToken(actionValue)
	actionScope, ok := scope.Actions[action]
	if !ok {
		return fmt.Errorf("tool invocation blocked by grant %s: action %q is not allowed by tool_invocation scope", grant.GrantID, actionValue)
	}
	if err := validateCapabilityToolInvocationSelectors(grant, payload, scope, actionScope, actionValue); err != nil {
		return err
	}
	return nil
}

func validateCapabilityToolInvocationSelectors(grant session.CapabilityGrant, payload map[string]json.RawMessage, scope capabilityToolInvocationScope, actionScope capabilityToolInvocationActionScope, actionValue string) error {
	allowedFields := map[string]struct{}{"action": {}}
	for name := range actionScope.Selectors {
		allowedFields[name] = struct{}{}
	}
	for _, name := range actionScope.AllowedFields {
		allowedFields[name] = struct{}{}
	}
	required := map[string]struct{}{}
	for name := range actionScope.Selectors {
		required[name] = struct{}{}
	}
	for _, name := range actionScope.RequiredSelectors {
		required[name] = struct{}{}
	}
	for name := range required {
		if _, ok := payload[name]; !ok {
			return fmt.Errorf("tool invocation blocked by grant %s: selector %q is required for action %q", grant.GrantID, name, actionValue)
		}
	}
	for name, allowedValues := range actionScope.Selectors {
		raw, ok := payload[name]
		if !ok {
			continue
		}
		value, ok := scalarJSONString(raw)
		if !ok {
			return fmt.Errorf("tool invocation blocked by grant %s: selector %q must be a scalar value", grant.GrantID, name)
		}
		if !stringAllowed(value, allowedValues) {
			return fmt.Errorf("tool invocation blocked by grant %s: selector %q value %q is not allowed", grant.GrantID, name, value)
		}
	}
	allowAdditional := scope.AllowAdditionalFields
	if actionScope.AllowAdditionalFields != nil {
		allowAdditional = *actionScope.AllowAdditionalFields
	}
	if !allowAdditional {
		for name := range payload {
			if _, ok := allowedFields[name]; !ok {
				return fmt.Errorf("tool invocation blocked by grant %s: input field %q is not allowed by tool_invocation scope", grant.GrantID, name)
			}
		}
	}
	return nil
}

func capabilityToolInvocationScopeSummary(grant session.CapabilityGrant) (string, bool) {
	scope, ok, err := capabilityGrantToolInvocationScope(grant)
	if err != nil || !ok {
		return "", false
	}
	actions := make([]string, 0, len(scope.Actions))
	for action, actionScope := range scope.Actions {
		selectorNames := make([]string, 0, len(actionScope.Selectors))
		for name := range actionScope.Selectors {
			selectorNames = append(selectorNames, name)
		}
		sort.Strings(selectorNames)
		if len(selectorNames) > 0 {
			actions = append(actions, fmt.Sprintf("%s[%s]", action, strings.Join(selectorNames, ",")))
		} else {
			actions = append(actions, action)
		}
	}
	sort.Strings(actions)
	return strings.Join(actions, ";"), true
}

func normalizeToolInvocationToken(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, "-", "_")
	value = strings.ReplaceAll(value, " ", "_")
	return value
}

func normalizeUniqueStrings(values []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func scalarJSONString(raw json.RawMessage) (string, bool) {
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s, true
	}
	var n json.Number
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&n); err == nil {
		return n.String(), true
	}
	var b bool
	if err := json.Unmarshal(raw, &b); err == nil {
		if b {
			return "true", true
		}
		return "false", true
	}
	return "", false
}

func stringAllowed(value string, allowed []string) bool {
	for _, candidate := range allowed {
		if candidate == "*" || value == candidate {
			return true
		}
	}
	return false
}
