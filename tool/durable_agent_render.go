//go:build linux

package tool

import (
	"fmt"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
)

func renderDurableAgentList(agents []core.DurableAgent) string {
	var b strings.Builder
	b.WriteString("[DURABLE_AGENTS]\n")
	fmt.Fprintf(&b, "count: %d\n", len(agents))
	if len(agents) == 0 {
		b.WriteString("no_agents\n[/DURABLE_AGENTS]")
		return b.String()
	}
	for i, agent := range agents {
		fmt.Fprintf(&b, "%d. agent_id=%s channel=%s status=%s policy_version=%d outbound_mode=%s allowed_users=%s\n",
			i+1,
			strings.TrimSpace(agent.AgentID),
			durableAgentWizardDisplayChannelKind(strings.TrimSpace(agent.ChannelKind)),
			firstNonEmpty(strings.TrimSpace(agent.Status), "active"),
			agent.PolicyVersion,
			strings.TrimSpace(agent.LivePolicy.OutboundMode),
			formatDurableAgentTelegramUserIDs(agent.AllowedTelegramUserIDs),
		)
	}
	b.WriteString("[/DURABLE_AGENTS]")
	return b.String()
}

func renderDurableAgentBootstrapShow(agent core.DurableAgent, updates []session.DurableAgentBootstrapUpdate, inherited core.NodeLLMBootstrap) string {
	var b strings.Builder
	b.WriteString("action: durable-agent bootstrap show\n")
	fmt.Fprintf(&b, "agent_id: %s\n", agent.AgentID)
	fmt.Fprintf(&b, "bootstrap_source_hint: %s\n", durableAgentBootstrapSourceHint(agent.BootstrapLLM, inherited))
	fmt.Fprintf(&b, "bootstrap_llm_backend: %s\n", agent.BootstrapLLM.Backend)
	fmt.Fprintf(&b, "bootstrap_native_provider: %s\n", agent.BootstrapLLM.NativeProvider)
	fmt.Fprintf(&b, "bootstrap_model: %s\n", agent.BootstrapLLM.Model)
	if strings.TrimSpace(agent.BootstrapLLM.CodexHome) != "" {
		fmt.Fprintf(&b, "bootstrap_codex_home: %s\n", agent.BootstrapLLM.CodexHome)
	}
	fmt.Fprintf(&b, "parent_bootstrap_backend: %s\n", inherited.Backend)
	if inherited.Configured() && strings.TrimSpace(inherited.CodexHome) != "" {
		fmt.Fprintf(&b, "parent_bootstrap_codex_home: %s\n", inherited.CodexHome)
	}
	fmt.Fprintf(&b, "history_count: %d\n", len(updates))
	for _, update := range updates {
		fmt.Fprintf(&b, "- bootstrap_update id=%d kind=%s actor_role=%s", update.ID, strings.TrimSpace(update.UpdateKind), strings.TrimSpace(update.ActorRole))
		if update.ActorUserID > 0 {
			fmt.Fprintf(&b, " actor_user_id=%d", update.ActorUserID)
		}
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

func durableAgentBootstrapSourceHint(current core.NodeLLMBootstrap, inherited core.NodeLLMBootstrap) string {
	current = core.NormalizeNodeLLMBootstrap(current)
	inherited = core.NormalizeNodeLLMBootstrap(inherited)
	switch {
	case !current.Configured():
		return "unset"
	case inherited.Configured() && durableAgentNodeBootstrapEqual(current, inherited):
		return "matches_parent_copy"
	default:
		return "pinned_or_diverged"
	}
}

func renderDurableAgentPolicy(agent core.DurableAgent, updates []session.DurableAgentPolicyUpdate) string {
	var b strings.Builder
	channelKind := normalizeDurableAgentChannelKind(strings.TrimSpace(agent.ChannelKind))
	b.WriteString("action: durable-agent policy show\n")
	fmt.Fprintf(&b, "agent_id: %s\n", agent.AgentID)
	fmt.Fprintf(&b, "channel_kind: %s\n", channelKind)
	fmt.Fprintf(&b, "channel_profile: %s\n", durableAgentWizardDisplayChannelKind(channelKind))
	fmt.Fprintf(&b, "status: %s\n", firstNonEmpty(strings.TrimSpace(agent.Status), "active"))
	fmt.Fprintf(&b, "mode: %s\n", durableAgentModeFromPolicy(agent.LivePolicy))
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
	if strings.TrimSpace(agent.LivePolicy.TailnetMode) != "" {
		fmt.Fprintf(&b, "tailnet_mode: %s\n", strings.TrimSpace(agent.LivePolicy.TailnetMode))
		fmt.Fprintf(&b, "tailnet_hostname: %s\n", strings.TrimSpace(agent.LivePolicy.TailnetHostname))
		fmt.Fprintf(&b, "tailnet_tags: %s\n", strings.Join(agent.LivePolicy.TailnetTags, ","))
		fmt.Fprintf(&b, "tailnet_surface_policy: %s\n", strings.TrimSpace(agent.LivePolicy.TailnetSurfacePolicy))
	}
	fmt.Fprintf(&b, "allowed_telegram_user_ids: %s\n", formatDurableAgentTelegramUserIDs(agent.AllowedTelegramUserIDs))
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
	fmt.Fprintf(&b, "mode: %s\n", durableAgentModeFromPolicy(agent.LivePolicy))
	fmt.Fprintf(&b, "autonomy: %s\n", durableAgentAutonomyFromPolicy(agent.LivePolicy))
	fmt.Fprintf(&b, "visibility: %s\n", durableAgentVisibilityFromPolicy(agent.LivePolicy))
	fmt.Fprintf(&b, "shared_context: %s\n", durableAgentSharedContextFromPolicy(agent.LivePolicy))
	if strings.TrimSpace(agent.LivePolicy.TailnetMode) != "" {
		fmt.Fprintf(&b, "tailnet_mode: %s\n", strings.TrimSpace(agent.LivePolicy.TailnetMode))
		fmt.Fprintf(&b, "tailnet_hostname: %s\n", strings.TrimSpace(agent.LivePolicy.TailnetHostname))
		fmt.Fprintf(&b, "tailnet_surface_policy: %s\n", strings.TrimSpace(agent.LivePolicy.TailnetSurfacePolicy))
	}
	if update.SourceReviewEventID > 0 {
		fmt.Fprintf(&b, "source_review_event_id: %d\n", update.SourceReviewEventID)
	}
	if strings.TrimSpace(update.Reason) != "" {
		fmt.Fprintf(&b, "reason: %s\n", update.Reason)
	}
	return b.String()
}

func renderDurableAgentBootstrapApply(agent core.DurableAgent, update *session.DurableAgentBootstrapUpdate) string {
	var b strings.Builder
	b.WriteString("action: durable-agent bootstrap update\n")
	fmt.Fprintf(&b, "agent_id: %s\n", agent.AgentID)
	if update == nil {
		b.WriteString("changed: false\n")
		fmt.Fprintf(&b, "bootstrap_llm_backend: %s\n", agent.BootstrapLLM.Backend)
		fmt.Fprintf(&b, "bootstrap_native_provider: %s\n", agent.BootstrapLLM.NativeProvider)
		fmt.Fprintf(&b, "bootstrap_model: %s\n", agent.BootstrapLLM.Model)
		if strings.TrimSpace(agent.BootstrapLLM.CodexHome) != "" {
			fmt.Fprintf(&b, "bootstrap_codex_home: %s\n", agent.BootstrapLLM.CodexHome)
		}
		return b.String()
	}
	b.WriteString("changed: true\n")
	fmt.Fprintf(&b, "update_id: %d\n", update.ID)
	fmt.Fprintf(&b, "update_kind: %s\n", update.UpdateKind)
	fmt.Fprintf(&b, "previous_bootstrap_backend: %s\n", update.PreviousBootstrap.Backend)
	fmt.Fprintf(&b, "new_bootstrap_backend: %s\n", update.NewBootstrap.Backend)
	fmt.Fprintf(&b, "new_bootstrap_native_provider: %s\n", update.NewBootstrap.NativeProvider)
	fmt.Fprintf(&b, "new_bootstrap_model: %s\n", update.NewBootstrap.Model)
	if strings.TrimSpace(update.NewBootstrap.CodexHome) != "" {
		fmt.Fprintf(&b, "new_bootstrap_codex_home: %s\n", update.NewBootstrap.CodexHome)
	}
	if update.SourceReviewEventID > 0 {
		fmt.Fprintf(&b, "source_review_event_id: %d\n", update.SourceReviewEventID)
	}
	if update.ActorUserID > 0 {
		fmt.Fprintf(&b, "actor_user_id: %d\n", update.ActorUserID)
	}
	if strings.TrimSpace(update.ActorRole) != "" {
		fmt.Fprintf(&b, "actor_role: %s\n", update.ActorRole)
	}
	if strings.TrimSpace(update.Reason) != "" {
		fmt.Fprintf(&b, "reason: %s\n", update.Reason)
	}
	b.WriteString("note: next durable child wake uses the updated bootstrap\n")
	return b.String()
}

func renderDurableAgentEnrollment(enrollment core.DurableAgentRemoteEnrollment) string {
	var b strings.Builder
	b.WriteString("action: durable-agent enrollment\n")
	fmt.Fprintf(&b, "agent_id: %s\n", enrollment.AgentID)
	fmt.Fprintf(&b, "status: %s\n", enrollment.Status)
	fmt.Fprintf(&b, "parent_control_url: %s\n", enrollment.ParentControlURL)
	fmt.Fprintf(&b, "protocol_version: %s\n", enrollment.ProtocolVersion)
	fmt.Fprintf(&b, "last_sequence: %d\n", enrollment.LastSequence)
	if strings.TrimSpace(enrollment.TailnetIdentity.StableNodeID) != "" {
		fmt.Fprintf(&b, "tailnet_stable_node_id: %s\n", enrollment.TailnetIdentity.StableNodeID)
	}
	if strings.TrimSpace(enrollment.TailnetIdentity.NodeName) != "" {
		fmt.Fprintf(&b, "tailnet_node_name: %s\n", enrollment.TailnetIdentity.NodeName)
	}
	if strings.TrimSpace(enrollment.TailnetIdentity.ComputedName) != "" {
		fmt.Fprintf(&b, "tailnet_computed_name: %s\n", enrollment.TailnetIdentity.ComputedName)
	}
	if strings.TrimSpace(enrollment.TailnetIdentity.LoginName) != "" {
		fmt.Fprintf(&b, "tailnet_login_name: %s\n", enrollment.TailnetIdentity.LoginName)
	}
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
	channelKind := normalizeDurableAgentChannelKind(strings.TrimSpace(agent.ChannelKind))
	fmt.Fprintf(&b, "action: durable-agent %s\n", strings.TrimSpace(action))
	fmt.Fprintf(&b, "agent_id: %s\n", strings.TrimSpace(agent.AgentID))
	fmt.Fprintf(&b, "channel_kind: %s\n", channelKind)
	fmt.Fprintf(&b, "channel_profile: %s\n", durableAgentWizardDisplayChannelKind(channelKind))
	fmt.Fprintf(&b, "status: %s\n", firstNonEmpty(strings.TrimSpace(agent.Status), "active"))
	fmt.Fprintf(&b, "review_target_chat_id: %d\n", agent.ReviewTargetChatID)
	fmt.Fprintf(&b, "mode: %s\n", durableAgentModeFromPolicy(agent.LivePolicy))
	fmt.Fprintf(&b, "wakeup_mode: %s\n", strings.TrimSpace(agent.WakeupMode))
	fmt.Fprintf(&b, "outbound_mode: %s\n", strings.TrimSpace(agent.LivePolicy.OutboundMode))
	fmt.Fprintf(&b, "allowed_telegram_user_ids: %s\n", formatDurableAgentTelegramUserIDs(agent.AllowedTelegramUserIDs))
	renderDurableAgentChannelConfig(&b, agent)
	return b.String()
}

func renderDurableAgentAccess(action string, agent core.DurableAgent, requested []int64, changed bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "action: durable-agent access %s\n", strings.TrimSpace(action))
	fmt.Fprintf(&b, "agent_id: %s\n", strings.TrimSpace(agent.AgentID))
	if len(requested) > 0 {
		fmt.Fprintf(&b, "requested_user_ids: %s\n", formatDurableAgentTelegramUserIDs(requested))
	}
	fmt.Fprintf(&b, "changed: %t\n", changed)
	fmt.Fprintf(&b, "allowed_telegram_user_ids: %s\n", formatDurableAgentTelegramUserIDs(agent.AllowedTelegramUserIDs))
	return b.String()
}

func renderDurableAgentConversation(action string, agent core.DurableAgent, continuity core.DurableAgentContinuityState, history int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "action: durable-agent conversation %s\n", strings.TrimSpace(action))
	fmt.Fprintf(&b, "agent_id: %s\n", strings.TrimSpace(agent.AgentID))
	total := 0
	if continuity.Conversation != nil {
		total = len(continuity.Conversation.Messages)
	}
	threadState, lastParentAt, lastChildAt, lastParentAckAt, lastChildError := durableAgentConversationState(continuity)
	fmt.Fprintf(&b, "messages: %d\n", total)
	fmt.Fprintf(&b, "pending_parent_messages: %d\n", len(continuity.PendingParentConversationMessages(0)))
	fmt.Fprintf(&b, "thread_state: %s\n", threadState)
	if !lastParentAt.IsZero() {
		fmt.Fprintf(&b, "last_parent_message_at: %s\n", lastParentAt.UTC().Format(time.RFC3339))
	}
	if !lastChildAt.IsZero() {
		fmt.Fprintf(&b, "last_child_message_at: %s\n", lastChildAt.UTC().Format(time.RFC3339))
	}
	if !lastParentAckAt.IsZero() {
		fmt.Fprintf(&b, "last_parent_acknowledged_at: %s\n", lastParentAckAt.UTC().Format(time.RFC3339))
	}
	if lastChildError != "" {
		fmt.Fprintf(&b, "last_child_error: %s\n", truncateCompact(lastChildError, 220))
	}
	window := durableAgentConversationWindow(continuity, history)
	if len(window) == 0 {
		b.WriteString("conversation: -\n")
		b.WriteString("next: conversation_send\n")
		return b.String()
	}
	b.WriteString("conversation:\n")
	for _, message := range window {
		ts := "-"
		if !message.CreatedAt.IsZero() {
			ts = message.CreatedAt.UTC().Format(time.RFC3339)
		}
		line := fmt.Sprintf("- [%s] %s: %s", ts, message.Role, message.Text)
		if message.Role == "parent" && !message.AcknowledgedAt.IsZero() {
			line += " (acknowledged)"
		}
		b.WriteString(line)
		b.WriteString("\n")
	}
	b.WriteString("next: conversation_send\n")
	return b.String()
}

func formatDurableAgentTelegramUserIDs(values []int64) string {
	values = core.NormalizeDurableAgentAllowedTelegramUserIDs(values)
	if len(values) == 0 {
		return "-"
	}
	parts := make([]string, 0, len(values))
	for _, value := range values {
		parts = append(parts, fmt.Sprintf("%d", value))
	}
	return strings.Join(parts, ",")
}

func renderDurableAgentChannelConfig(b *strings.Builder, agent core.DurableAgent) {
	if b == nil {
		return
	}
	external := agent.ChannelConfig.ExternalConfig()
	if external == nil {
		return
	}
	fmt.Fprintf(b, "channel_address: %s\n", strings.TrimSpace(external.Address))
	fmt.Fprintf(b, "channel_account: %s\n", strings.TrimSpace(external.Account))
	fmt.Fprintf(b, "channel_adapter: %s\n", strings.TrimSpace(external.Adapter))
	fmt.Fprintf(b, "channel_query: %s\n", strings.TrimSpace(external.Query))
	fmt.Fprintf(b, "channel_poll_interval: %s\n", strings.TrimSpace(external.PollInterval))
	fmt.Fprintf(b, "channel_summarize_pdfs: %t\n", external.SummarizePDFs)
	fmt.Fprintf(b, "channel_synthesis_cadence: %s\n", strings.TrimSpace(external.SynthesisCadence))
	fmt.Fprintf(b, "channel_surface_rules: %s\n", strings.Join(external.SurfaceRules, ","))
	fmt.Fprintf(b, "channel_never_retain: %s\n", strings.Join(external.NeverRetain, ","))
}
