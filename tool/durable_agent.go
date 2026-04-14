//go:build linux

package tool

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
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
		if strings.TrimSpace(in.AgentID) == "" {
			return "", fmt.Errorf("durable_agent agent_id is required for policy_show")
		}
		agent, err := r.resolveDurableAgent(in.AgentID)
		if err != nil {
			return "", err
		}
		history := in.History
		if history <= 0 {
			history = 5
		}
		updates, err := r.store.DurableAgentPolicyUpdates(agent.AgentID, history)
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
		agent, err := r.resolveDurableAgent(agentID)
		if err != nil {
			return "", err
		}
		enrollment, err := r.store.DurableAgentRemoteEnrollment(agent.AgentID)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return "", fmt.Errorf("durable agent %q has no remote enrollment; use policy_apply for ordinary autonomy/privacy/shared-context changes", agent.AgentID)
			}
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
	agent, err := r.resolveDurableAgent(agentID)
	if err != nil {
		return "", err
	}
	if in.ReviewEventID > 0 {
		event, err := r.store.ReviewEventByID(in.ReviewEventID)
		if err != nil {
			return "", err
		}
		if event.SourceScope.Kind != session.ScopeKindDurableAgent || !durableAgentReviewTargetsAgent(agent.AgentID, event.SourceScope) {
			return "", fmt.Errorf("review event %d does not belong to durable agent %s", in.ReviewEventID, agent.AgentID)
		}
	}

	policy := agent.LivePolicy
	if strings.TrimSpace(in.Charter) != "" {
		policy.Charter = strings.TrimSpace(in.Charter)
	}
	if strings.TrimSpace(in.Autonomy) != "" {
		mode, err := durableAgentAutonomyToOutboundMode(in.Autonomy)
		if err != nil {
			return "", err
		}
		policy.OutboundMode = mode
	}
	if strings.TrimSpace(in.Visibility) != "" {
		mode, err := durableAgentVisibilityToPublicSurfaceMode(in.Visibility)
		if err != nil {
			return "", err
		}
		policy.PublicSurfaceMode = mode
	}
	if strings.TrimSpace(in.SharedContext) != "" {
		reuse, scope, err := durableAgentSharedContextToReuse(in.SharedContext)
		if err != nil {
			return "", err
		}
		policy.SharedInferenceReuse = reuse
		policy.SharedInferenceReuseScope = scope
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
	updated, update, err := r.store.ApplyDurableAgentLivePolicy(agent.AgentID, policy, in.ReviewEventID, reason)
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
	agent, err := r.resolveDurableAgent(agentID)
	if err != nil {
		return "", err
	}
	enrollment, err := r.store.DurableAgentRemoteEnrollment(agent.AgentID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", fmt.Errorf("durable agent %q has no remote enrollment; use policy_apply for ordinary autonomy/privacy/shared-context changes", agent.AgentID)
		}
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
	fmt.Fprintf(&b, "autonomy: %s\n", durableAgentAutonomyFromPolicy(agent.LivePolicy))
	fmt.Fprintf(&b, "visibility: %s\n", durableAgentVisibilityFromPolicy(agent.LivePolicy))
	fmt.Fprintf(&b, "shared_context: %s\n", durableAgentSharedContextFromPolicy(agent.LivePolicy))
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
	fmt.Fprintf(&b, "autonomy: %s\n", durableAgentAutonomyFromPolicy(agent.LivePolicy))
	fmt.Fprintf(&b, "visibility: %s\n", durableAgentVisibilityFromPolicy(agent.LivePolicy))
	fmt.Fprintf(&b, "shared_context: %s\n", durableAgentSharedContextFromPolicy(agent.LivePolicy))
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

func (r *Registry) resolveDurableAgent(raw string) (*core.DurableAgent, error) {
	agentID := strings.TrimSpace(raw)
	if agentID == "" {
		return nil, fmt.Errorf("durable_agent agent_id is required")
	}
	agent, err := r.store.DurableAgent(agentID)
	if err == nil {
		return agent, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}

	agents, listErr := r.store.ListDurableAgents()
	if listErr != nil {
		return nil, err
	}
	if matched := findDurableAgentCandidate(agents, agentID); matched != nil {
		return matched, nil
	}
	if len(agents) == 0 {
		return nil, fmt.Errorf("durable agent %q not found and no durable agents are registered", agentID)
	}
	return nil, fmt.Errorf("durable agent %q not found; available agent_ids: %s", agentID, strings.Join(durableAgentIDOptions(agents), ", "))
}

func findDurableAgentCandidate(agents []core.DurableAgent, raw string) *core.DurableAgent {
	normalized := normalizeDurableAgentReference(raw)
	if normalized == "" {
		return nil
	}
	var exact *core.DurableAgent
	exactCount := 0
	for i := range agents {
		if normalizeDurableAgentReference(agents[i].AgentID) == normalized {
			exact = &agents[i]
			exactCount++
		}
	}
	if exactCount == 1 {
		return exact
	}

	var fuzzy *core.DurableAgent
	fuzzyCount := 0
	for i := range agents {
		candidate := normalizeDurableAgentReference(agents[i].AgentID)
		if candidate == "" {
			continue
		}
		if strings.Contains(candidate, normalized) || strings.Contains(normalized, candidate) {
			fuzzy = &agents[i]
			fuzzyCount++
		}
	}
	if fuzzyCount == 1 {
		return fuzzy
	}
	return nil
}

func durableAgentIDOptions(agents []core.DurableAgent) []string {
	if len(agents) == 0 {
		return nil
	}
	out := make([]string, 0, len(agents))
	for _, agent := range agents {
		id := strings.TrimSpace(agent.AgentID)
		if id == "" {
			continue
		}
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

func normalizeDurableAgentReference(raw string) string {
	raw = strings.ToLower(strings.TrimSpace(raw))
	raw = strings.ReplaceAll(raw, "durable", " ")
	raw = strings.ReplaceAll(raw, "agent", " ")
	var b strings.Builder
	for _, r := range raw {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		}
	}
	return b.String()
}

func durableAgentAutonomyToOutboundMode(value string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "observe_only":
		return "read_only", nil
	case "local_drafts":
		return "draft_only", nil
	case "review_before_reply":
		return "reply_with_parent_review", nil
	case "reply_within_charter":
		return "reply_with_policy_authorization", nil
	default:
		return "", fmt.Errorf("durable_agent autonomy must be one of observe_only|local_drafts|review_before_reply|reply_within_charter")
	}
}

func durableAgentVisibilityToPublicSurfaceMode(value string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "private":
		return "none", nil
	case "parent_relay_only":
		return "explicit_parent_relay_only", nil
	case "public_channel":
		return "channel_transcript", nil
	default:
		return "", fmt.Errorf("durable_agent visibility must be one of private|parent_relay_only|public_channel")
	}
}

func durableAgentSharedContextToReuse(value string) (string, string, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "isolated":
		return "disabled", "public_prefix_only", nil
	case "public_only":
		return "allowed", "public_prefix_only", nil
	default:
		return "", "", fmt.Errorf("durable_agent shared_context must be one of isolated|public_only")
	}
}

func durableAgentAutonomyFromPolicy(policy core.DurableAgentLivePolicy) string {
	switch strings.TrimSpace(policy.OutboundMode) {
	case "read_only":
		return "observe_only"
	case "draft_only":
		return "local_drafts"
	case "reply_with_parent_review":
		return "review_before_reply"
	case "reply_with_policy_authorization":
		return "reply_within_charter"
	default:
		return ""
	}
}

func durableAgentVisibilityFromPolicy(policy core.DurableAgentLivePolicy) string {
	switch strings.TrimSpace(policy.PublicSurfaceMode) {
	case "none":
		return "private"
	case "explicit_parent_relay_only":
		return "parent_relay_only"
	case "channel_transcript":
		return "public_channel"
	default:
		return ""
	}
}

func durableAgentSharedContextFromPolicy(policy core.DurableAgentLivePolicy) string {
	if strings.TrimSpace(policy.SharedInferenceReuse) == "allowed" {
		return "public_only"
	}
	return "isolated"
}
