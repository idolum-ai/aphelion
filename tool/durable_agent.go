//go:build linux

package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/session"
)

func (r *Registry) durableAgent(_ context.Context, input json.RawMessage, p principal.Principal) (string, error) {
	if r.store == nil {
		return "", fmt.Errorf("durable agent governance requires transcript store")
	}
	if err := requireAdminTool(p, "durable_agent"); err != nil {
		return "", err
	}

	var in durableAgentInput
	if err := json.Unmarshal(input, &in); err != nil {
		return "", fmt.Errorf("decode durable_agent input: %w", err)
	}

	switch strings.ToLower(strings.TrimSpace(in.Action)) {
	case "list":
		agents, err := r.store.ListDurableAgents()
		if err != nil {
			return "", err
		}
		return renderDurableAgentList(agents), nil
	case "policy_show":
		agentID := strings.TrimSpace(in.AgentID)
		if agentID == "" {
			return "", fmt.Errorf("durable_agent agent_id is required for policy_show")
		}
		agent, err := r.store.DurableAgent(agentID)
		if err != nil {
			return "", err
		}
		history := in.History
		if history <= 0 {
			history = 5
		}
		updates, err := r.store.DurableAgentPolicyUpdates(agentID, history)
		if err != nil {
			return "", err
		}
		return renderDurableAgentPolicy(*agent, updates), nil
	case "policy_apply":
		return r.applyDurableAgentPolicy(in)
	case "enrollment_show":
		agentID := strings.TrimSpace(in.AgentID)
		if agentID == "" {
			return "", fmt.Errorf("durable_agent agent_id is required for enrollment_show")
		}
		enrollment, err := r.store.DurableAgentRemoteEnrollment(agentID)
		if err != nil {
			return "", err
		}
		return renderDurableAgentEnrollment(*enrollment), nil
	case "enrollment_update":
		return r.updateDurableAgentEnrollment(in)
	default:
		return "", fmt.Errorf("durable_agent action must be one of list|policy_show|policy_apply|enrollment_show|enrollment_update")
	}
}

func (r *Registry) applyDurableAgentPolicy(in durableAgentInput) (string, error) {
	agentID := strings.TrimSpace(in.AgentID)
	if agentID == "" {
		return "", fmt.Errorf("durable_agent agent_id is required for policy_apply")
	}
	agent, err := r.store.DurableAgent(agentID)
	if err != nil {
		return "", err
	}
	if in.ReviewEventID > 0 {
		event, err := r.store.ReviewEventByID(in.ReviewEventID)
		if err != nil {
			return "", err
		}
		if event.SourceScope.Kind != session.ScopeKindDurableAgent || !durableAgentReviewTargetsAgent(agentID, event.SourceScope) {
			return "", fmt.Errorf("review event %d does not belong to durable agent %s", in.ReviewEventID, agentID)
		}
	}

	policy := agent.LivePolicy
	if strings.TrimSpace(in.Charter) != "" {
		policy.Charter = strings.TrimSpace(in.Charter)
	}
	if len(in.Capabilities) > 0 {
		policy.CapabilityEnvelope = append([]string(nil), in.Capabilities...)
	}
	if strings.TrimSpace(in.OutboundMode) != "" {
		policy.OutboundMode = strings.TrimSpace(in.OutboundMode)
	}
	if strings.TrimSpace(in.DriftPolicy) != "" {
		policy.DriftPolicy = strings.TrimSpace(in.DriftPolicy)
	}
	if strings.TrimSpace(in.PublicSurfaceMode) != "" {
		policy.PublicSurfaceMode = strings.TrimSpace(in.PublicSurfaceMode)
	}
	if strings.TrimSpace(in.SharedInferenceReuse) != "" {
		policy.SharedInferenceReuse = strings.TrimSpace(in.SharedInferenceReuse)
	}
	if strings.TrimSpace(in.SharedInferenceReuseScope) != "" {
		policy.SharedInferenceReuseScope = strings.TrimSpace(in.SharedInferenceReuseScope)
	}

	reason := strings.TrimSpace(in.Reason)
	if reason == "" && in.ReviewEventID > 0 {
		reason = fmt.Sprintf("ratified from review_event=%d", in.ReviewEventID)
	}
	updated, update, err := r.store.ApplyDurableAgentLivePolicy(agentID, policy, in.ReviewEventID, reason)
	if err != nil {
		return "", err
	}
	return renderDurableAgentPolicyApply(*updated, update), nil
}

func (r *Registry) updateDurableAgentEnrollment(in durableAgentInput) (string, error) {
	agentID := strings.TrimSpace(in.AgentID)
	if agentID == "" {
		return "", fmt.Errorf("durable_agent agent_id is required for enrollment_update")
	}
	enrollment, err := r.store.DurableAgentRemoteEnrollment(agentID)
	if err != nil {
		return "", err
	}
	switch strings.ToLower(strings.TrimSpace(in.Operation)) {
	case "revoke":
		enrollment.Status = "revoked"
		enrollment.RevokedAt = time.Now().UTC()
	case "reactivate":
		if enrollment.Status == "decommissioned" {
			return "", fmt.Errorf("durable agent enrollment %s is decommissioned and cannot be reactivated", agentID)
		}
		enrollment.Status = "active"
		enrollment.RevokedAt = time.Time{}
	case "decommission":
		enrollment.Status = "decommissioned"
		enrollment.RevokedAt = time.Now().UTC()
	case "rotate_secret":
		secret := strings.TrimSpace(in.Secret)
		if secret == "" {
			return "", fmt.Errorf("durable_agent enrollment_update secret is required when operation=rotate_secret")
		}
		agent, err := r.store.DurableAgent(agentID)
		if err != nil {
			return "", err
		}
		agent.ControlPlaneSecret = secret
		if err := r.store.UpsertDurableAgent(*agent); err != nil {
			return "", err
		}
	default:
		return "", fmt.Errorf("durable_agent enrollment_update operation must be one of revoke|reactivate|decommission|rotate_secret")
	}
	if err := r.store.UpsertDurableAgentRemoteEnrollment(*enrollment); err != nil {
		return "", err
	}
	return renderDurableAgentEnrollment(*enrollment), nil
}

func renderDurableAgentList(agents []core.DurableAgent) string {
	var b strings.Builder
	b.WriteString("[DURABLE_AGENTS]\n")
	fmt.Fprintf(&b, "count: %d\n", len(agents))
	if len(agents) == 0 {
		b.WriteString("no_agents\n[/DURABLE_AGENTS]")
		return b.String()
	}
	for i, agent := range agents {
		fmt.Fprintf(&b, "%d. agent_id=%s channel=%s status=%s policy_version=%d outbound_mode=%s\n",
			i+1,
			strings.TrimSpace(agent.AgentID),
			strings.TrimSpace(agent.ChannelKind),
			firstNonEmpty(strings.TrimSpace(agent.Status), "active"),
			agent.PolicyVersion,
			strings.TrimSpace(agent.LivePolicy.OutboundMode),
		)
	}
	b.WriteString("[/DURABLE_AGENTS]")
	return b.String()
}

func renderDurableAgentPolicy(agent core.DurableAgent, updates []session.DurableAgentPolicyUpdate) string {
	var b strings.Builder
	b.WriteString("action: durable-agent policy show\n")
	fmt.Fprintf(&b, "agent_id: %s\n", agent.AgentID)
	fmt.Fprintf(&b, "channel_kind: %s\n", agent.ChannelKind)
	fmt.Fprintf(&b, "policy_version: %d\n", agent.PolicyVersion)
	fmt.Fprintf(&b, "policy_hash: %s\n", agent.PolicyHash)
	if !agent.PolicyIssuedAt.IsZero() {
		fmt.Fprintf(&b, "policy_issued_at: %s\n", agent.PolicyIssuedAt.UTC().Format(time.RFC3339Nano))
	}
	fmt.Fprintf(&b, "charter: %s\n", agent.LivePolicy.Charter)
	fmt.Fprintf(&b, "capabilities: %s\n", strings.Join(agent.LivePolicy.CapabilityEnvelope, ","))
	fmt.Fprintf(&b, "outbound_mode: %s\n", agent.LivePolicy.OutboundMode)
	fmt.Fprintf(&b, "drift_policy: %s\n", agent.LivePolicy.DriftPolicy)
	fmt.Fprintf(&b, "public_surface_mode: %s\n", agent.LivePolicy.PublicSurfaceMode)
	fmt.Fprintf(&b, "shared_inference_reuse: %s\n", agent.LivePolicy.SharedInferenceReuse)
	fmt.Fprintf(&b, "shared_inference_reuse_scope: %s\n", agent.LivePolicy.SharedInferenceReuseScope)
	fmt.Fprintf(&b, "bootstrap_capabilities: %s\n", strings.Join(agent.BootstrapCeiling.CapabilityEnvelope, ","))
	fmt.Fprintf(&b, "bootstrap_allowed_outbound_modes: %s\n", strings.Join(agent.BootstrapCeiling.AllowedOutboundModes, ","))
	fmt.Fprintf(&b, "bootstrap_allowed_public_surface_modes: %s\n", strings.Join(agent.BootstrapCeiling.AllowedPublicSurfaceModes, ","))
	fmt.Fprintf(&b, "bootstrap_allowed_shared_inference_reuse: %s\n", strings.Join(agent.BootstrapCeiling.AllowedSharedInferenceReuse, ","))
	fmt.Fprintf(&b, "bootstrap_allowed_shared_inference_scopes: %s\n", strings.Join(agent.BootstrapCeiling.AllowedSharedInferenceScopes, ","))
	fmt.Fprintf(&b, "bootstrap_llm_backend: %s\n", agent.BootstrapLLM.Backend)
	fmt.Fprintf(&b, "bootstrap_native_provider: %s\n", agent.BootstrapLLM.NativeProvider)
	fmt.Fprintf(&b, "bootstrap_model: %s\n", agent.BootstrapLLM.Model)
	if strings.TrimSpace(agent.BootstrapLLM.CodexHome) != "" {
		fmt.Fprintf(&b, "bootstrap_codex_home: %s\n", agent.BootstrapLLM.CodexHome)
	}
	fmt.Fprintf(&b, "policy_updates: %d\n", len(updates))
	for _, update := range updates {
		fmt.Fprintf(&b, "- id=%d previous=%d new=%d", update.ID, update.PreviousVersion, update.NewVersion)
		if update.SourceReviewEventID > 0 {
			fmt.Fprintf(&b, " review_event=%d", update.SourceReviewEventID)
		}
		if strings.TrimSpace(update.Reason) != "" {
			fmt.Fprintf(&b, " reason=%s", update.Reason)
		}
		fmt.Fprintf(&b, " applied_at=%s\n", update.AppliedAt.UTC().Format(time.RFC3339Nano))
	}
	return b.String()
}

func renderDurableAgentPolicyApply(agent core.DurableAgent, update *session.DurableAgentPolicyUpdate) string {
	var b strings.Builder
	b.WriteString("action: durable-agent policy apply\n")
	fmt.Fprintf(&b, "agent_id: %s\n", agent.AgentID)
	if update == nil {
		b.WriteString("changed: false\n")
		fmt.Fprintf(&b, "policy_version: %d\n", agent.PolicyVersion)
		fmt.Fprintf(&b, "policy_hash: %s\n", agent.PolicyHash)
		return b.String()
	}
	b.WriteString("changed: true\n")
	fmt.Fprintf(&b, "policy_version: %d\n", agent.PolicyVersion)
	fmt.Fprintf(&b, "policy_hash: %s\n", agent.PolicyHash)
	if update.SourceReviewEventID > 0 {
		fmt.Fprintf(&b, "source_review_event_id: %d\n", update.SourceReviewEventID)
	}
	if strings.TrimSpace(update.Reason) != "" {
		fmt.Fprintf(&b, "reason: %s\n", update.Reason)
	}
	return b.String()
}

func renderDurableAgentEnrollment(enrollment core.DurableAgentRemoteEnrollment) string {
	var b strings.Builder
	b.WriteString("action: durable-agent enrollment\n")
	fmt.Fprintf(&b, "agent_id: %s\n", enrollment.AgentID)
	fmt.Fprintf(&b, "status: %s\n", enrollment.Status)
	fmt.Fprintf(&b, "parent_control_url: %s\n", enrollment.ParentControlURL)
	fmt.Fprintf(&b, "key_fingerprint: %s\n", enrollment.KeyFingerprint)
	fmt.Fprintf(&b, "protocol_version: %s\n", enrollment.ProtocolVersion)
	fmt.Fprintf(&b, "last_sequence: %d\n", enrollment.LastSequence)
	if !enrollment.EnrolledAt.IsZero() {
		fmt.Fprintf(&b, "enrolled_at: %s\n", enrollment.EnrolledAt.UTC().Format(time.RFC3339))
	}
	if !enrollment.LastSeenAt.IsZero() {
		fmt.Fprintf(&b, "last_seen_at: %s\n", enrollment.LastSeenAt.UTC().Format(time.RFC3339))
	}
	if !enrollment.RevokedAt.IsZero() {
		fmt.Fprintf(&b, "revoked_at: %s\n", enrollment.RevokedAt.UTC().Format(time.RFC3339))
	}
	return b.String()
}

func durableAgentReviewTargetsAgent(agentID string, scope session.ScopeRef) bool {
	agentID = strings.TrimSpace(agentID)
	return strings.TrimSpace(scope.DurableAgentID) == agentID || strings.TrimSpace(scope.ID) == agentID
}
