//go:build linux

package tool

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/session"
)

func (r *Registry) durableAgent(ctx context.Context, input json.RawMessage, p principal.Principal, key session.SessionKey) (string, error) {
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
	case "create":
		return r.createDurableAgent(in, key)
	case "activate":
		return r.activateDurableAgent(in)
	case "connection_test":
		return r.testDurableAgentConnection(ctx, in)
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
		return "", fmt.Errorf("durable_agent action must be one of list|create|activate|connection_test|policy_show|policy_apply|enrollment_show|enrollment_update")
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

	patch := effectiveDurableAgentPolicyPatchFromInput(in)
	policy := agent.LivePolicy
	if err := applyDurableAgentPolicyPatch(&policy, patch); err != nil {
		return "", err
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

func (r *Registry) createDurableAgent(in durableAgentInput, key session.SessionKey) (string, error) {
	agentID := strings.TrimSpace(in.AgentID)
	if agentID == "" {
		return "", fmt.Errorf("durable_agent agent_id is required for create")
	}
	channelKind := strings.TrimSpace(in.ChannelKind)
	if channelKind == "" {
		return "", fmt.Errorf("durable_agent channel_kind is required for create")
	}

	existing, err := r.store.DurableAgent(agentID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}

	var agent core.DurableAgent
	if existing != nil {
		if strings.TrimSpace(existing.Status) != "" && strings.TrimSpace(existing.Status) != "draft" {
			return "", fmt.Errorf("durable agent %q already exists with status %q; use policy_apply or activate instead of create", existing.AgentID, existing.Status)
		}
		agent = *existing
	}
	agent.AgentID = agentID
	agent.ChannelKind = firstNonEmpty(channelKind, agent.ChannelKind)
	agent.ParentScopeKind = firstNonEmpty(agent.ParentScopeKind, string(key.Scope.Kind))
	agent.ParentScopeID = firstNonEmpty(agent.ParentScopeID, key.Scope.ID)
	if in.ReviewTargetChatID > 0 {
		agent.ReviewTargetChatID = in.ReviewTargetChatID
	} else if agent.ReviewTargetChatID == 0 && key.ChatID != 0 {
		agent.ReviewTargetChatID = key.ChatID
	}
	if strings.TrimSpace(in.WakeupMode) != "" {
		agent.WakeupMode = strings.TrimSpace(in.WakeupMode)
	} else if strings.TrimSpace(agent.WakeupMode) == "" && agent.ChannelKind == "email" {
		agent.WakeupMode = "poll"
	}
	if strings.TrimSpace(in.NetworkPolicy) != "" {
		agent.NetworkPolicy = strings.TrimSpace(in.NetworkPolicy)
	}
	if len(in.SecretScopes) > 0 {
		agent.SecretScopes = append([]string(nil), in.SecretScopes...)
	}
	patch := effectiveDurableAgentPolicyPatchFromInput(in)
	policy := agent.LivePolicy
	if strings.TrimSpace(policy.Charter) == "" &&
		len(policy.CapabilityEnvelope) == 0 &&
		strings.TrimSpace(policy.OutboundMode) == "" &&
		strings.TrimSpace(policy.DriftPolicy) == "" &&
		strings.TrimSpace(policy.PublicSurfaceMode) == "" &&
		strings.TrimSpace(policy.SharedInferenceReuse) == "" &&
		strings.TrimSpace(policy.SharedInferenceReuseScope) == "" {
		policy = defaultDurableAgentLivePolicy(agent.ChannelKind, patch.Charter)
	}
	if err := applyDurableAgentPolicyPatch(&policy, patch); err != nil {
		return "", err
	}
	agent.LivePolicy = core.NormalizeDurableAgentLivePolicy(policy)

	channelConfig, err := mergeDurableAgentChannelConfig(agent.ChannelConfig, in.ChannelConfig)
	if err != nil {
		return "", err
	}
	agent.ChannelConfig = channelConfig
	agent.Status = "draft"

	if err := r.store.UpsertDurableAgent(agent); err != nil {
		return "", err
	}
	updated, err := r.store.DurableAgent(agent.AgentID)
	if err != nil {
		return "", err
	}
	return renderDurableAgentLifecycle("create", *updated), nil
}

func (r *Registry) activateDurableAgent(in durableAgentInput) (string, error) {
	agentID := strings.TrimSpace(in.AgentID)
	if agentID == "" {
		return "", fmt.Errorf("durable_agent agent_id is required for activate")
	}
	agent, err := r.resolveDurableAgent(agentID)
	if err != nil {
		return "", err
	}
	if err := validateDurableAgentActivation(*agent); err != nil {
		return "", err
	}
	agent.Status = "active"
	if err := r.store.UpsertDurableAgent(*agent); err != nil {
		return "", err
	}
	return renderDurableAgentLifecycle("activate", *agent), nil
}

func (r *Registry) testDurableAgentConnection(ctx context.Context, in durableAgentInput) (string, error) {
	agentID := strings.TrimSpace(in.AgentID)
	if agentID == "" {
		return "", fmt.Errorf("durable_agent agent_id is required for connection_test")
	}
	agent, err := r.resolveDurableAgent(agentID)
	if err != nil {
		return "", err
	}
	switch strings.TrimSpace(agent.ChannelKind) {
	case "email":
		if agent.ChannelConfig.Email == nil {
			return "", fmt.Errorf("durable agent %q has no email channel_config", agent.AgentID)
		}
		args := []string{"gog"}
		if strings.TrimSpace(agent.ChannelConfig.Email.Account) != "" {
			args = append(args, "--account", strings.TrimSpace(agent.ChannelConfig.Email.Account))
		}
		args = append(args, "gmail", "search", firstNonEmpty(strings.TrimSpace(agent.ChannelConfig.Email.Query), "label:inbox"), "--json", "--results-only", "--max", "1", "--no-input")
		cmd := exec.CommandContext(ctx, args[0], args[1:]...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			return "", fmt.Errorf("durable agent connection_test failed: %w: %s", err, strings.TrimSpace(string(out)))
		}
		return fmt.Sprintf("action: durable-agent connection test\nagent_id: %s\nchannel_kind: %s\nstatus: ok\n", agent.AgentID, agent.ChannelKind), nil
	default:
		return "", fmt.Errorf("durable agent %q channel %q does not support connection_test yet", agent.AgentID, agent.ChannelKind)
	}
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
	fmt.Fprintf(&b, "status: %s\n", firstNonEmpty(strings.TrimSpace(agent.Status), "active"))
	fmt.Fprintf(&b, "wakeup_mode: %s\n", strings.TrimSpace(agent.WakeupMode))
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
	renderDurableAgentChannelConfig(&b, agent)
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

func renderDurableAgentLifecycle(action string, agent core.DurableAgent) string {
	var b strings.Builder
	fmt.Fprintf(&b, "action: durable-agent %s\n", strings.TrimSpace(action))
	fmt.Fprintf(&b, "agent_id: %s\n", strings.TrimSpace(agent.AgentID))
	fmt.Fprintf(&b, "channel_kind: %s\n", strings.TrimSpace(agent.ChannelKind))
	fmt.Fprintf(&b, "status: %s\n", firstNonEmpty(strings.TrimSpace(agent.Status), "active"))
	fmt.Fprintf(&b, "review_target_chat_id: %d\n", agent.ReviewTargetChatID)
	fmt.Fprintf(&b, "wakeup_mode: %s\n", strings.TrimSpace(agent.WakeupMode))
	fmt.Fprintf(&b, "outbound_mode: %s\n", strings.TrimSpace(agent.LivePolicy.OutboundMode))
	renderDurableAgentChannelConfig(&b, agent)
	return b.String()
}

func renderDurableAgentChannelConfig(b *strings.Builder, agent core.DurableAgent) {
	if b == nil || agent.ChannelConfig.Email == nil {
		return
	}
	email := agent.ChannelConfig.Email
	fmt.Fprintf(b, "email_address: %s\n", strings.TrimSpace(email.Address))
	fmt.Fprintf(b, "email_account: %s\n", strings.TrimSpace(email.Account))
	fmt.Fprintf(b, "email_adapter: %s\n", strings.TrimSpace(email.Adapter))
	fmt.Fprintf(b, "email_query: %s\n", strings.TrimSpace(email.Query))
	fmt.Fprintf(b, "email_poll_interval: %s\n", strings.TrimSpace(email.PollInterval))
	fmt.Fprintf(b, "email_summarize_pdfs: %t\n", email.SummarizePDFs)
	fmt.Fprintf(b, "email_synthesis_cadence: %s\n", strings.TrimSpace(email.SynthesisCadence))
	fmt.Fprintf(b, "email_surface_rules: %s\n", strings.Join(email.SurfaceRules, ","))
	fmt.Fprintf(b, "email_never_retain: %s\n", strings.Join(email.NeverRetain, ","))
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

type effectiveDurableAgentPolicyPatch struct {
	Charter                   string
	Autonomy                  string
	Visibility                string
	SharedContext             string
	Capabilities              []string
	CapabilitiesSet           bool
	DriftPolicy               string
	OutboundMode              string
	PublicSurfaceMode         string
	SharedInferenceReuse      string
	SharedInferenceReuseScope string
}

func effectiveDurableAgentPolicyPatchFromInput(in durableAgentInput) effectiveDurableAgentPolicyPatch {
	patch := effectiveDurableAgentPolicyPatch{}

	if in.PolicyPatch != nil {
		patch.Charter = strings.TrimSpace(in.PolicyPatch.Charter)
		patch.Autonomy = strings.TrimSpace(in.PolicyPatch.Autonomy)
		patch.Visibility = strings.TrimSpace(in.PolicyPatch.Visibility)
		patch.SharedContext = strings.TrimSpace(in.PolicyPatch.SharedContext)
		patch.DriftPolicy = strings.TrimSpace(in.PolicyPatch.DriftPolicy)
		if in.PolicyPatch.Capabilities != nil {
			patch.Capabilities = normalizePolicyCapabilities(in.PolicyPatch.Capabilities)
			patch.CapabilitiesSet = true
		}
	}
	if patch.Charter == "" {
		patch.Charter = strings.TrimSpace(in.Charter)
	}
	if patch.Autonomy == "" {
		patch.Autonomy = strings.TrimSpace(in.Autonomy)
	}
	if patch.Visibility == "" {
		patch.Visibility = strings.TrimSpace(in.Visibility)
	}
	if patch.SharedContext == "" {
		patch.SharedContext = strings.TrimSpace(in.SharedContext)
	}
	if !patch.CapabilitiesSet && in.Capabilities != nil {
		patch.Capabilities = normalizePolicyCapabilities(in.Capabilities)
		patch.CapabilitiesSet = true
	}
	if patch.DriftPolicy == "" {
		patch.DriftPolicy = strings.TrimSpace(in.DriftPolicy)
	}

	if in.PolicyOverrides != nil {
		patch.OutboundMode = strings.TrimSpace(in.PolicyOverrides.OutboundMode)
		patch.PublicSurfaceMode = strings.TrimSpace(in.PolicyOverrides.PublicSurfaceMode)
		patch.SharedInferenceReuse = strings.TrimSpace(in.PolicyOverrides.SharedInferenceReuse)
		patch.SharedInferenceReuseScope = strings.TrimSpace(in.PolicyOverrides.SharedInferenceReuseScope)
	}
	if patch.OutboundMode == "" {
		patch.OutboundMode = strings.TrimSpace(in.OutboundMode)
	}
	if patch.PublicSurfaceMode == "" {
		patch.PublicSurfaceMode = strings.TrimSpace(in.PublicSurfaceMode)
	}
	if patch.SharedInferenceReuse == "" {
		patch.SharedInferenceReuse = strings.TrimSpace(in.SharedInferenceReuse)
	}
	if patch.SharedInferenceReuseScope == "" {
		patch.SharedInferenceReuseScope = strings.TrimSpace(in.SharedInferenceReuseScope)
	}
	return patch
}

func applyDurableAgentPolicyPatch(policy *core.DurableAgentLivePolicy, patch effectiveDurableAgentPolicyPatch) error {
	if policy == nil {
		return nil
	}
	if patch.Charter != "" {
		policy.Charter = patch.Charter
	}
	if patch.Autonomy != "" {
		mode, err := durableAgentAutonomyToOutboundMode(patch.Autonomy)
		if err != nil {
			return err
		}
		policy.OutboundMode = mode
	}
	if patch.Visibility != "" {
		mode, err := durableAgentVisibilityToPublicSurfaceMode(patch.Visibility)
		if err != nil {
			return err
		}
		policy.PublicSurfaceMode = mode
	}
	if patch.SharedContext != "" {
		reuse, scope, err := durableAgentSharedContextToReuse(patch.SharedContext)
		if err != nil {
			return err
		}
		policy.SharedInferenceReuse = reuse
		policy.SharedInferenceReuseScope = scope
	}
	if patch.CapabilitiesSet {
		policy.CapabilityEnvelope = append([]string(nil), patch.Capabilities...)
	}
	if patch.DriftPolicy != "" {
		policy.DriftPolicy = patch.DriftPolicy
	}
	if patch.OutboundMode != "" {
		policy.OutboundMode = patch.OutboundMode
	}
	if patch.PublicSurfaceMode != "" {
		policy.PublicSurfaceMode = patch.PublicSurfaceMode
	}
	if patch.SharedInferenceReuse != "" {
		policy.SharedInferenceReuse = patch.SharedInferenceReuse
	}
	if patch.SharedInferenceReuseScope != "" {
		policy.SharedInferenceReuseScope = patch.SharedInferenceReuseScope
	}
	return nil
}

func normalizePolicyCapabilities(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		out = append(out, trimmed)
	}
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

func defaultDurableAgentLivePolicy(channelKind string, charter string) core.DurableAgentLivePolicy {
	switch strings.TrimSpace(channelKind) {
	case "email":
		return core.NormalizeDurableAgentLivePolicy(core.DurableAgentLivePolicy{
			Charter:                   strings.TrimSpace(charter),
			CapabilityEnvelope:        []string{"read_channel", "bounded_review_artifact"},
			OutboundMode:              "read_only",
			DriftPolicy:               "admin_review",
			PublicSurfaceMode:         "explicit_parent_relay_only",
			SharedInferenceReuse:      "disabled",
			SharedInferenceReuseScope: "public_prefix_only",
		})
	default:
		return core.DefaultTelegramGroupLivePolicy(charter)
	}
}

func mergeDurableAgentChannelConfig(existing core.DurableAgentChannelConfig, raw json.RawMessage) (core.DurableAgentChannelConfig, error) {
	existing = core.NormalizeDurableAgentChannelConfig(existing)
	if len(raw) == 0 || strings.TrimSpace(string(raw)) == "" {
		return existing, nil
	}
	var update core.DurableAgentChannelConfig
	if err := json.Unmarshal(raw, &update); err != nil {
		return core.DurableAgentChannelConfig{}, fmt.Errorf("decode durable_agent channel_config: %w", err)
	}
	update = core.NormalizeDurableAgentChannelConfig(update)
	if update.Email != nil {
		if existing.Email == nil {
			cfg := *update.Email
			existing.Email = &cfg
		} else {
			mergeDurableAgentEmailChannelConfig(existing.Email, *update.Email)
		}
	}
	return core.NormalizeDurableAgentChannelConfig(existing), nil
}

func mergeDurableAgentEmailChannelConfig(dst *core.DurableAgentEmailChannelConfig, src core.DurableAgentEmailChannelConfig) {
	if dst == nil {
		return
	}
	if strings.TrimSpace(src.Address) != "" {
		dst.Address = strings.TrimSpace(src.Address)
	}
	if strings.TrimSpace(src.Account) != "" {
		dst.Account = strings.TrimSpace(src.Account)
	}
	if strings.TrimSpace(src.Adapter) != "" {
		dst.Adapter = strings.TrimSpace(src.Adapter)
	}
	if strings.TrimSpace(src.Query) != "" {
		dst.Query = strings.TrimSpace(src.Query)
	}
	if strings.TrimSpace(src.PollInterval) != "" {
		dst.PollInterval = strings.TrimSpace(src.PollInterval)
	}
	if len(src.SurfaceRules) > 0 {
		dst.SurfaceRules = append([]string(nil), src.SurfaceRules...)
	}
	if src.SummarizePDFs {
		dst.SummarizePDFs = true
	}
	if strings.TrimSpace(src.SynthesisCadence) != "" {
		dst.SynthesisCadence = strings.TrimSpace(src.SynthesisCadence)
	}
	if len(src.NeverRetain) > 0 {
		dst.NeverRetain = append([]string(nil), src.NeverRetain...)
	}
}

func validateDurableAgentActivation(agent core.DurableAgent) error {
	switch strings.TrimSpace(agent.ChannelKind) {
	case "email":
		email := agent.ChannelConfig.Email
		if email == nil {
			return fmt.Errorf("durable agent %q cannot activate without email channel_config", agent.AgentID)
		}
		if strings.TrimSpace(email.Address) == "" {
			return fmt.Errorf("durable agent %q cannot activate without an email address", agent.AgentID)
		}
		if strings.TrimSpace(email.Adapter) == "" {
			return fmt.Errorf("durable agent %q cannot activate without an email adapter", agent.AgentID)
		}
		if strings.TrimSpace(agent.WakeupMode) == "" {
			return fmt.Errorf("durable agent %q cannot activate without a wakeup_mode", agent.AgentID)
		}
	}
	return nil
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
