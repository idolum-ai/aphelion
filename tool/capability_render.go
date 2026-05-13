//go:build linux

package tool

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
)

func renderCapabilityChildRuntime(b *strings.Builder, contract string, constraints string) {
	material, ok, err := core.ExtractChildRuntimeContract(contract, constraints)
	if err != nil || !ok {
		return
	}
	b.WriteString("child_runtime: present\n")
	if material.Executable != "" {
		fmt.Fprintf(b, "child_runtime_executable: %s\n", material.Executable)
	}
	if len(material.ReadonlyPaths) > 0 {
		fmt.Fprintf(b, "child_runtime_readonly_paths: %d\n", len(material.ReadonlyPaths))
	}
	if len(material.ReadonlyBinds) > 0 {
		fmt.Fprintf(b, "child_runtime_readonly_binds: %d\n", len(material.ReadonlyBinds))
	}
	if len(material.SecretBinds) > 0 {
		fmt.Fprintf(b, "child_runtime_secret_binds: %d\n", len(material.SecretBinds))
	}
	if len(material.EnvFromParent) > 0 {
		fmt.Fprintf(b, "child_runtime_env_from_parent: %s\n", strings.Join(material.EnvFromParent, ","))
	}
}

func renderCapabilityRequestHelp() string {
	return strings.Join([]string{
		"[CAPABILITY_REQUEST]",
		"actions:",
		"- request_submit | request_show | request_list",
		"submits broad governed capability requests; parent/admin review and grants are handled through capability_authority",
	}, "\n")
}

func renderCapabilityAuthorityHelp() string {
	return strings.Join([]string{
		"[CAPABILITY_AUTHORITY]",
		"actions:",
		"- request_show | request_list | request_review",
		"- grant_set | grant_show | grant_list | grant_revoke",
		"- access_check",
	}, "\n")
}

func renderCapabilityRequest(header string, record session.CapabilityRequest) string {
	return renderCapabilityRequestWithReviewEvent(header, record, 0)
}

func renderCapabilityRequestWithReviewEvent(header string, record session.CapabilityRequest, reviewEventID int64) string {
	record = session.NormalizeCapabilityRequest(record)
	var b strings.Builder
	b.WriteString(strings.TrimSpace(header))
	b.WriteString("\n")
	fmt.Fprintf(&b, "request_id: %s\n", record.RequestID)
	fmt.Fprintf(&b, "kind: %s\n", record.Kind)
	fmt.Fprintf(&b, "target_resource: %s\n", record.TargetResource)
	fmt.Fprintf(&b, "review_status: %s\n", record.ReviewStatus)
	fmt.Fprintf(&b, "requested_by: %s\n", firstNonEmpty(record.RequestedBy, "-"))
	fmt.Fprintf(&b, "requested_for: %s\n", firstNonEmpty(record.RequestedFor, "-"))
	if record.ParentPrincipal != "" {
		fmt.Fprintf(&b, "parent_principal: %s\n", record.ParentPrincipal)
	}
	if record.AdminPrincipal != "" {
		fmt.Fprintf(&b, "admin_principal: %s\n", record.AdminPrincipal)
	}
	if record.RiskClass != "" {
		fmt.Fprintf(&b, "risk_class: %s\n", record.RiskClass)
	}
	if record.Purpose != "" {
		fmt.Fprintf(&b, "purpose: %s\n", record.Purpose)
	}
	if record.GrantID != "" {
		fmt.Fprintf(&b, "grant_id: %s\n", record.GrantID)
	}
	if record.Contract != "" {
		fmt.Fprintf(&b, "contract: %s\n", record.Contract)
	}
	if record.Constraints != "" {
		fmt.Fprintf(&b, "constraints: %s\n", record.Constraints)
	}
	if summary, ok := capabilityToolInvocationScopeSummary(session.CapabilityGrant{Contract: record.Contract, Constraints: record.Constraints, GrantID: record.RequestID}); ok {
		fmt.Fprintf(&b, "tool_invocation_scope: %s\n", summary)
	}
	if reviewEventID > 0 {
		fmt.Fprintf(&b, "review_event_id: %d\n", reviewEventID)
	}
	return b.String()
}

func renderCapabilityRequestList(records []session.CapabilityRequest) string {
	var b strings.Builder
	b.WriteString("[CAPABILITY_REQUESTS]\n")
	fmt.Fprintf(&b, "count: %d\n", len(records))
	if len(records) == 0 {
		b.WriteString("- (none)\n")
		return b.String()
	}
	for _, record := range records {
		record = session.NormalizeCapabilityRequest(record)
		fmt.Fprintf(&b, "- request_id=%s kind=%s target_resource=%s review_status=%s requested_for=%s parent_principal=%s\n", record.RequestID, record.Kind, firstNonEmpty(record.TargetResource, "-"), record.ReviewStatus, firstNonEmpty(record.RequestedFor, "-"), firstNonEmpty(record.ParentPrincipal, "-"))
	}
	return b.String()
}

func renderCapabilityBlocked(reason string, detail string, nextActions []string) string {
	var b strings.Builder
	b.WriteString("[CAPABILITY_BLOCKED]\n")
	fmt.Fprintf(&b, "status: blocked\n")
	if reason = strings.TrimSpace(reason); reason != "" {
		fmt.Fprintf(&b, "reason: %s\n", reason)
	}
	if detail = strings.TrimSpace(detail); detail != "" {
		fmt.Fprintf(&b, "detail: %s\n", detail)
	}
	if len(nextActions) > 0 {
		b.WriteString("next_action:\n")
		for _, action := range nextActions {
			action = strings.TrimSpace(action)
			if action == "" {
				continue
			}
			fmt.Fprintf(&b, "- %s\n", action)
		}
	}
	return b.String()
}

func renderCapabilityGrant(header string, grant session.CapabilityGrant) string {
	return renderCapabilityGrantWithUpdate(header, grant, nil)
}

func renderCapabilityGrantFailure(grant session.CapabilityGrant, cause error) string {
	base := strings.TrimSpace(renderCapabilityGrant("[CAPABILITY_GRANT_FAILED]", grant))
	next := capabilityGrantFailureNextActions(cause)
	if len(next) == 0 {
		return base + "\n"
	}
	var b strings.Builder
	b.WriteString(base)
	b.WriteString("\nnext_action:\n")
	for _, action := range next {
		fmt.Fprintf(&b, "- %s\n", action)
	}
	return b.String()
}

func capabilityGrantFailureNextActions(cause error) []string {
	var ceiling *core.DurableAgentPolicyCeilingError
	if errors.As(cause, &ceiling) && ceiling != nil {
		field := strings.TrimSpace(ceiling.Field)
		if field == "" {
			field = "live_policy"
		}
		return []string{
			fmt.Sprintf("The requested durable-agent policy exceeds the bootstrap ceiling for %s.", field),
			fmt.Sprintf("Requested: %s. Allowed: %s.", strings.Join(ceiling.Requested, ","), strings.Join(ceiling.Allowed, ",")),
			"Create an admin-reviewed request to widen the bootstrap ceiling, or retry grant_set with a policy inside the current ceiling.",
		}
	}
	if cause != nil && strings.TrimSpace(cause.Error()) != "" {
		return []string{"Inspect stale_reason, adjust the grant contract or durable policy patch, then retry grant_set."}
	}
	return nil
}

func renderCapabilityGrantWithUpdate(header string, grant session.CapabilityGrant, update *capabilityUpdatePlanApplyResult) string {
	grant = session.NormalizeCapabilityGrant(grant)
	var b strings.Builder
	b.WriteString(strings.TrimSpace(header))
	b.WriteString("\n")
	fmt.Fprintf(&b, "grant_id: %s\n", grant.GrantID)
	if grant.RequestID != "" {
		fmt.Fprintf(&b, "request_id: %s\n", grant.RequestID)
	}
	fmt.Fprintf(&b, "kind: %s\n", grant.Kind)
	fmt.Fprintf(&b, "target_resource: %s\n", grant.TargetResource)
	fmt.Fprintf(&b, "status: %s\n", grant.Status)
	fmt.Fprintf(&b, "granted_to: %s\n", grant.GrantedTo)
	fmt.Fprintf(&b, "granted_by: %s\n", firstNonEmpty(grant.GrantedBy, "-"))
	fmt.Fprintf(&b, "allowed_actions: %s\n", strings.Join(grant.AllowedActions, ","))
	if grant.AnchorFingerprint != "" {
		fmt.Fprintf(&b, "anchor_fingerprint: %s\n", grant.AnchorFingerprint)
	}
	if grant.StaleReason != "" {
		fmt.Fprintf(&b, "stale_reason: %s\n", grant.StaleReason)
	}
	if !grant.ExpiresAt.IsZero() {
		fmt.Fprintf(&b, "expires_at: %s\n", grant.ExpiresAt.Format(time.RFC3339Nano))
	}
	if grant.InvocationCount > 0 || grant.FailureCount > 0 {
		fmt.Fprintf(&b, "counters: invocations=%d failures=%d\n", grant.InvocationCount, grant.FailureCount)
	}
	renderCapabilityChildRuntime(&b, grant.Contract, grant.Constraints)
	if summary, ok := capabilityToolInvocationScopeSummary(grant); ok {
		fmt.Fprintf(&b, "tool_invocation_scope: %s\n", summary)
	}
	if update != nil {
		b.WriteString("capability_update_plan: present\n")
		fmt.Fprintf(&b, "policy_update_applied: %t\n", update.PolicyUpdateApplied)
		fmt.Fprintf(&b, "policy_changed: %t\n", update.PolicyChanged)
		fmt.Fprintf(&b, "policy_agent_id: %s\n", update.AgentID)
		fmt.Fprintf(&b, "policy_version: %d\n", update.PolicyVersion)
		fmt.Fprintf(&b, "policy_hash: %s\n", update.PolicyHash)
		if update.PolicyUpdateID > 0 {
			fmt.Fprintf(&b, "policy_update_id: %d\n", update.PolicyUpdateID)
		}
	}
	return b.String()
}

func renderCapabilityGrantList(records []session.CapabilityGrant) string {
	var b strings.Builder
	b.WriteString("[CAPABILITY_GRANTS]\n")
	fmt.Fprintf(&b, "count: %d\n", len(records))
	if len(records) == 0 {
		b.WriteString("- (none)\n")
		return b.String()
	}
	for _, record := range records {
		record = session.NormalizeCapabilityGrant(record)
		fmt.Fprintf(&b, "- grant_id=%s kind=%s target_resource=%s status=%s granted_to=%s actions=%s\n", record.GrantID, record.Kind, firstNonEmpty(record.TargetResource, "-"), record.Status, firstNonEmpty(record.GrantedTo, "-"), strings.Join(record.AllowedActions, ","))
	}
	return b.String()
}

func renderCapabilityAccess(kind session.CapabilityKind, target string, principalID string, action string, allowed bool, grant session.CapabilityGrant) string {
	var b strings.Builder
	b.WriteString("[CAPABILITY_ACCESS]\n")
	fmt.Fprintf(&b, "kind: %s\n", kind)
	fmt.Fprintf(&b, "target_resource: %s\n", target)
	fmt.Fprintf(&b, "principal: %s\n", principalID)
	fmt.Fprintf(&b, "action: %s\n", action)
	fmt.Fprintf(&b, "allowed: %t\n", allowed)
	if allowed {
		fmt.Fprintf(&b, "grant_id: %s\n", grant.GrantID)
		fmt.Fprintf(&b, "grant_status: %s\n", grant.Status)
	}
	return b.String()
}
