//go:build linux

package runtime

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
)

func externalToolInvocationReadinessFromSnapshots(toolName string, childPrincipal string, inputAction string, selectorName string, selectorValue string, tools []core.ToolLifecycleStatusSnapshot, grants []core.CapabilityGrantStatusSnapshot) core.ExternalToolInvocationReadinessSnapshot {
	toolName = strings.TrimSpace(toolName)
	childPrincipal = strings.TrimSpace(childPrincipal)
	inputAction = normalizeReadinessToken(inputAction)
	selectorName = strings.TrimSpace(selectorName)
	selectorValue = strings.TrimSpace(selectorValue)
	snapshot := core.ExternalToolInvocationReadinessSnapshot{
		GeneratedAt:      time.Now().UTC(),
		ToolName:         toolName,
		ChildPrincipal:   childPrincipal,
		Action:           inputAction,
		SelectorName:     selectorName,
		SelectorValue:    selectorValue,
		Ready:            false,
		Status:           "blocked",
		Why:              "tool is not visible in lifecycle status",
		NextRepairAction: "register, install, audit, and probe the external tool",
	}
	toolFound := false
	for _, tool := range tools {
		if !strings.EqualFold(strings.TrimSpace(tool.ToolName), toolName) {
			continue
		}
		toolFound = true
		if strings.TrimSpace(tool.InstallStatus) != string(session.ToolInstallStatusVerified) || strings.TrimSpace(tool.ProbeStatus) != string(session.ToolProbeStatusPassed) || strings.TrimSpace(tool.AuditStatus) != string(session.ToolAuditStatusPassed) {
			snapshot.Why = fmt.Sprintf("tool %s is not verified/probed/audited", toolName)
			snapshot.NextRepairAction = "run or repair tool install/audit/probe lifecycle"
			return snapshot
		}
		break
	}
	if !toolFound {
		return snapshot
	}

	grantFound := false
	for _, grant := range grants {
		if !strings.EqualFold(strings.TrimSpace(grant.TargetResource), toolName) || strings.TrimSpace(grant.GrantedTo) != childPrincipal {
			continue
		}
		grantFound = true
		if strings.TrimSpace(grant.Status) != string(session.CapabilityGrantStatusActive) {
			snapshot.Why = fmt.Sprintf("grant %s is %s", strings.TrimSpace(grant.GrantID), strings.TrimSpace(grant.Status))
			snapshot.NextRepairAction = "activate or replace the child tool grant"
			return snapshot
		}
		if !containsReadinessString(grant.AllowedActions, "invoke") {
			snapshot.Why = fmt.Sprintf("grant %s does not allow invoke", strings.TrimSpace(grant.GrantID))
			snapshot.NextRepairAction = "grant invoke for this external tool"
			return snapshot
		}
		if grant.ToolInvocationScope != "" && inputAction != "" && !toolInvocationScopeSummaryAllows(grant.ToolInvocationScope, inputAction, selectorName) {
			snapshot.Why = fmt.Sprintf("grant %s does not allow action/selector %s[%s]", strings.TrimSpace(grant.GrantID), inputAction, selectorName)
			snapshot.NextRepairAction = "issue a grant with the exact action/selector scope"
			return snapshot
		}
		if !grant.ChildRuntimePresent {
			snapshot.Why = fmt.Sprintf("grant %s has no child_runtime material", strings.TrimSpace(grant.GrantID))
			snapshot.NextRepairAction = "add child_runtime material to the active child tool grant"
			return snapshot
		}
		if strings.TrimSpace(grant.RuntimeMaterialMissing) != "" {
			snapshot.Why = fmt.Sprintf("runtime material missing: %s", strings.TrimSpace(grant.RuntimeMaterialMissing))
			snapshot.NextRepairAction = "provide or correct the named child_runtime material"
			return snapshot
		}
		if accountCheck, required := externalToolInvocationAccountGrantReadiness(childPrincipal, inputAction, selectorName, selectorValue, grants); required {
			if !accountCheck.Ready {
				snapshot.Why = accountCheck.Why
				snapshot.NextRepairAction = accountCheck.NextRepairAction
				return snapshot
			}
		}
		snapshot.Ready = true
		snapshot.Status = "ready"
		snapshot.Why = "tool exists, active child grant allows invoke and requested scope, runtime material is present"
		if externalToolInvocationUsesAccountSelector(selectorName, selectorValue) {
			snapshot.Why += ", and external account grant allows the requested account action"
		}
		snapshot.NextRepairAction = "none"
		return snapshot
	}
	if !grantFound {
		snapshot.Why = fmt.Sprintf("no active grant for %s to %s", childPrincipal, toolName)
		snapshot.NextRepairAction = "create an active child grant for this external tool"
	}
	return snapshot
}

func toolInvocationScopeSummaryAllows(summary string, action string, selectorName string) bool {
	summary = strings.TrimSpace(summary)
	action = normalizeReadinessToken(action)
	selectorName = strings.TrimSpace(selectorName)
	if summary == "" || action == "" {
		return true
	}
	for _, part := range strings.Split(summary, ";") {
		part = strings.TrimSpace(part)
		if part == action {
			return selectorName == ""
		}
		prefix := action + "["
		if strings.HasPrefix(part, prefix) && strings.HasSuffix(part, "]") {
			selectors := strings.Split(strings.TrimSuffix(strings.TrimPrefix(part, prefix), "]"), ",")
			return selectorName == "" || containsReadinessString(selectors, selectorName)
		}
	}
	return false
}

func firstMissingChildRuntimeMaterial(material core.ChildRuntimeContract) string {
	material = core.NormalizeChildRuntimeContract(material)
	if strings.TrimSpace(material.Executable) == "" && len(material.ReadonlyPaths) == 0 && len(material.ReadonlyBinds) == 0 && len(material.SecretBinds) == 0 && len(material.EnvFromParent) == 0 {
		return "child_runtime execution material not declared"
	}
	for _, path := range material.ReadonlyPaths {
		path = strings.TrimSpace(path)
		if path == "" {
			continue
		}
		if _, err := os.Stat(path); err != nil {
			return fmt.Sprintf("readonly_path %q", path)
		}
	}
	for _, bind := range material.ReadonlyBinds {
		if _, err := os.Stat(strings.TrimSpace(bind.Source)); err != nil {
			return fmt.Sprintf("readonly_bind source %q", strings.TrimSpace(bind.Source))
		}
	}
	for _, bind := range material.SecretBinds {
		if _, err := os.Stat(strings.TrimSpace(bind.Source)); err != nil {
			return fmt.Sprintf("secret_bind source %q", strings.TrimSpace(bind.Source))
		}
	}
	for _, name := range material.EnvFromParent {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if _, ok := os.LookupEnv(name); !ok {
			return fmt.Sprintf("env_from_parent %q", name)
		}
	}
	return ""
}

func containsReadinessString(values []string, want string) bool {
	want = strings.TrimSpace(want)
	for _, value := range values {
		if strings.TrimSpace(value) == want {
			return true
		}
	}
	return false
}

func normalizeReadinessToken(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, "-", "_")
	value = strings.ReplaceAll(value, " ", "_")
	return value
}

func (r *Runtime) externalToolInvocationReadinessStatusSnapshot(tools []core.ToolLifecycleStatusSnapshot, grants []core.CapabilityGrantStatusSnapshot) []core.ExternalToolInvocationReadinessSnapshot {
	if len(grants) == 0 {
		return nil
	}
	out := make([]core.ExternalToolInvocationReadinessSnapshot, 0, len(grants))
	seen := map[string]struct{}{}
	for _, grant := range grants {
		if strings.TrimSpace(grant.Kind) != string(session.CapabilityKindTool) || !strings.HasPrefix(strings.TrimSpace(grant.GrantedTo), "durable_agent:") {
			continue
		}
		if strings.TrimSpace(grant.ToolInvocationScope) == "" && !grant.ChildRuntimePresent {
			continue
		}
		action, selectorName := firstActionSelectorFromToolInvocationScopeSummary(grant.ToolInvocationScope)
		key := strings.TrimSpace(grant.TargetResource) + "\x00" + strings.TrimSpace(grant.GrantedTo) + "\x00" + action + "\x00" + selectorName
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		accountValues := []string{""}
		if externalToolInvocationUsesAccountSelector(selectorName, "") {
			accountValues = externalToolInvocationAccountValues(strings.TrimSpace(grant.GrantedTo), grants)
			if len(accountValues) == 0 {
				accountValues = []string{""}
			}
		}
		for _, selectorValue := range accountValues {
			rowKey := key + "\x00" + selectorValue
			if _, ok := seen[rowKey]; ok {
				continue
			}
			seen[rowKey] = struct{}{}
			out = append(out, externalToolInvocationReadinessFromSnapshots(strings.TrimSpace(grant.TargetResource), strings.TrimSpace(grant.GrantedTo), action, selectorName, selectorValue, tools, grants))
			if len(out) >= 5 {
				break
			}
		}
		if len(out) >= 5 {
			break
		}
	}
	return out
}

func firstActionSelectorFromToolInvocationScopeSummary(summary string) (string, string) {
	for _, part := range strings.Split(strings.TrimSpace(summary), ";") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if before, after, ok := strings.Cut(part, "["); ok {
			selector := strings.TrimSuffix(after, "]")
			if first, _, ok := strings.Cut(selector, ","); ok {
				selector = first
			}
			return normalizeReadinessToken(before), strings.TrimSpace(selector)
		}
		return normalizeReadinessToken(part), ""
	}
	return "", ""
}

func capabilityGrantToolInvocationScopeSummaryForStatus(grant session.CapabilityGrant) string {
	grant = session.NormalizeCapabilityGrant(grant)
	for _, raw := range []string{grant.Contract, grant.Constraints} {
		raw = strings.TrimSpace(raw)
		if raw == "" || raw == "{}" {
			continue
		}
		var wrapper struct {
			ToolInvocation *struct {
				Actions map[string]struct {
					Selectors map[string][]string `json:"selectors,omitempty"`
				} `json:"actions,omitempty"`
			} `json:"tool_invocation,omitempty"`
		}
		if err := json.Unmarshal([]byte(raw), &wrapper); err != nil || wrapper.ToolInvocation == nil {
			continue
		}
		parts := make([]string, 0, len(wrapper.ToolInvocation.Actions))
		for action, actionScope := range wrapper.ToolInvocation.Actions {
			action = normalizeReadinessToken(action)
			selectors := make([]string, 0, len(actionScope.Selectors))
			for name := range actionScope.Selectors {
				if strings.TrimSpace(name) != "" {
					selectors = append(selectors, strings.TrimSpace(name))
				}
			}
			sort.Strings(selectors)
			if len(selectors) > 0 {
				parts = append(parts, action+"["+strings.Join(selectors, ",")+"]")
			} else if action != "" {
				parts = append(parts, action)
			}
		}
		sort.Strings(parts)
		return strings.Join(parts, ";")
	}
	return ""
}

func externalToolInvocationAccountGrantReadiness(childPrincipal string, inputAction string, selectorName string, selectorValue string, grants []core.CapabilityGrantStatusSnapshot) (core.ExternalToolInvocationReadinessSnapshot, bool) {
	if !externalToolInvocationUsesAccountSelector(selectorName, selectorValue) {
		return core.ExternalToolInvocationReadinessSnapshot{}, false
	}
	selectorValue = strings.TrimSpace(selectorValue)
	snapshot := core.ExternalToolInvocationReadinessSnapshot{
		Ready:            false,
		Status:           "blocked",
		Why:              "account-scoped tool invocation requires an external_account grant",
		NextRepairAction: "create an active external_account grant for the requested account",
	}
	if selectorValue == "" {
		snapshot.Why = "account-scoped tool invocation is missing the requested account value"
		return snapshot, true
	}
	requiredActions := externalToolInvocationRequiredAccountActions(inputAction)
	var found bool
	for _, grant := range grants {
		if strings.TrimSpace(grant.Kind) != string(session.CapabilityKindExternalAccount) || strings.TrimSpace(grant.GrantedTo) != childPrincipal {
			continue
		}
		if !externalToolInvocationAccountTargetMatches(grant.TargetResource, selectorValue) {
			continue
		}
		found = true
		if strings.TrimSpace(grant.Status) != string(session.CapabilityGrantStatusActive) {
			snapshot.Why = fmt.Sprintf("external_account grant %s for %s is %s", strings.TrimSpace(grant.GrantID), selectorValue, strings.TrimSpace(grant.Status))
			snapshot.NextRepairAction = "activate or replace the requested account grant"
			return snapshot, true
		}
		if len(requiredActions) > 0 && !externalToolInvocationAccountActionsAllow(grant.AllowedActions, requiredActions) {
			snapshot.Why = fmt.Sprintf("external_account grant %s for %s does not allow %s", strings.TrimSpace(grant.GrantID), selectorValue, strings.Join(requiredActions, " or "))
			snapshot.NextRepairAction = "issue an external_account grant with the exact account action scope"
			return snapshot, true
		}
		snapshot.Ready = true
		snapshot.Status = "ready"
		snapshot.Why = "external account grant allows the requested account action"
		snapshot.NextRepairAction = "none"
		return snapshot, true
	}
	if !found {
		snapshot.Why = fmt.Sprintf("missing external_account grant for %s to %s", childPrincipal, selectorValue)
	}
	return snapshot, true
}

func externalToolInvocationUsesAccountSelector(selectorName string, selectorValue string) bool {
	selectorName = normalizeReadinessToken(selectorName)
	switch selectorName {
	case "account", "mailbox", "email_account", "external_account":
		return true
	default:
		return selectorName == "" && strings.Contains(strings.TrimSpace(selectorValue), "@")
	}
}

func externalToolInvocationRequiredAccountActions(inputAction string) []string {
	action := normalizeReadinessToken(inputAction)
	switch {
	case strings.Contains(action, "archive"):
		return []string{"archive", "mark_processed"}
	case strings.Contains(action, "mark"):
		return []string{"mark_processed", "archive"}
	case strings.Contains(action, "search"):
		return []string{"search", "read"}
	case strings.Contains(action, "read"), strings.Contains(action, "get"), strings.Contains(action, "list"), strings.Contains(action, "metadata"):
		return []string{"read", "metadata"}
	case strings.Contains(action, "connection"), strings.Contains(action, "status"), strings.Contains(action, "probe"):
		return []string{"connection_test", "read"}
	default:
		return []string{"read"}
	}
}

func externalToolInvocationAccountActionsAllow(allowed []string, required []string) bool {
	for _, want := range required {
		for _, value := range allowed {
			if normalizeReadinessToken(value) == normalizeReadinessToken(want) {
				return true
			}
		}
	}
	return false
}

func externalToolInvocationAccountTargetMatches(target string, account string) bool {
	target = strings.TrimSpace(target)
	account = strings.TrimSpace(account)
	if target == "" || account == "" {
		return false
	}
	return strings.EqualFold(target, account) || strings.HasSuffix(strings.ToLower(target), ":"+strings.ToLower(account))
}

func externalToolInvocationAccountValues(childPrincipal string, grants []core.CapabilityGrantStatusSnapshot) []string {
	var out []string
	seen := map[string]struct{}{}
	for _, grant := range grants {
		if strings.TrimSpace(grant.Kind) != string(session.CapabilityKindExternalAccount) || strings.TrimSpace(grant.GrantedTo) != childPrincipal {
			continue
		}
		value := externalToolInvocationAccountValueFromTarget(grant.TargetResource)
		if value == "" {
			continue
		}
		key := strings.ToLower(value)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func externalToolInvocationAccountValueFromTarget(target string) string {
	target = strings.TrimSpace(target)
	if target == "" {
		return ""
	}
	if strings.Contains(target, "@") {
		if idx := strings.LastIndex(target, ":"); idx >= 0 && idx+1 < len(target) {
			return strings.TrimSpace(target[idx+1:])
		}
		return target
	}
	return ""
}
