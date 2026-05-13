//go:build linux

package tool

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/idolum-ai/aphelion/core"
)

type effectiveDurableAgentPolicyPatch struct {
	Mode                      string
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
	TailnetMode               string
	TailnetHostname           string
	TailnetTags               []string
	TailnetTagsSet            bool
	TailnetSurfacePolicy      string
}

func effectiveDurableAgentPolicyPatchFromInput(in durableAgentInput) effectiveDurableAgentPolicyPatch {
	patch := effectiveDurableAgentPolicyPatch{}

	if in.PolicyPatch != nil {
		patch.Mode = core.NormalizeDurableAgentMode(in.PolicyPatch.Mode)
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
	if in.PolicyOverrides != nil {
		patch.OutboundMode = strings.TrimSpace(in.PolicyOverrides.OutboundMode)
		patch.PublicSurfaceMode = strings.TrimSpace(in.PolicyOverrides.PublicSurfaceMode)
		patch.SharedInferenceReuse = strings.TrimSpace(in.PolicyOverrides.SharedInferenceReuse)
		patch.SharedInferenceReuseScope = strings.TrimSpace(in.PolicyOverrides.SharedInferenceReuseScope)
		patch.TailnetMode = strings.TrimSpace(in.PolicyOverrides.TailnetMode)
		patch.TailnetHostname = strings.TrimSpace(in.PolicyOverrides.TailnetHostname)
		if in.PolicyOverrides.TailnetTags != nil {
			patch.TailnetTags = normalizePolicyCapabilities(in.PolicyOverrides.TailnetTags)
			patch.TailnetTagsSet = true
		}
		patch.TailnetSurfacePolicy = strings.TrimSpace(in.PolicyOverrides.TailnetSurfacePolicy)
	}
	return patch
}

func applyDurableAgentPolicyPatch(policy *core.DurableAgentLivePolicy, patch effectiveDurableAgentPolicyPatch) error {
	if policy == nil {
		return nil
	}
	if patch.Mode != "" {
		policy.Mode = patch.Mode
		applyDurableAgentModePolicyDefaults(policy)
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
	if patch.TailnetMode != "" {
		policy.TailnetMode = patch.TailnetMode
	}
	if patch.TailnetHostname != "" {
		policy.TailnetHostname = patch.TailnetHostname
	}
	if patch.TailnetTagsSet {
		policy.TailnetTags = append([]string(nil), patch.TailnetTags...)
	}
	if patch.TailnetSurfacePolicy != "" {
		policy.TailnetSurfacePolicy = patch.TailnetSurfacePolicy
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
	switch normalizeDurableAgentChannelKind(channelKind) {
	case "external_channel":
		return core.NormalizeDurableAgentLivePolicy(core.DurableAgentLivePolicy{
			Mode:                      "external",
			Charter:                   strings.TrimSpace(charter),
			CapabilityEnvelope:        []string{"read_channel", "bounded_review_artifact", "summarize_pdf"},
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

func durableAgentModeFromPolicy(policy core.DurableAgentLivePolicy) string {
	mode := core.NormalizeDurableAgentMode(policy.Mode)
	if mode == "" {
		return "live"
	}
	return mode
}

func applyDurableAgentModePolicyDefaults(policy *core.DurableAgentLivePolicy) {
	if policy == nil {
		return
	}
	switch core.NormalizeDurableAgentMode(policy.Mode) {
	case "sketch":
		policy.OutboundMode = "draft_only"
		policy.PublicSurfaceMode = "none"
		policy.SharedInferenceReuse = "disabled"
		policy.SharedInferenceReuseScope = "public_prefix_only"
		policy.CapabilityEnvelope = []string{"bounded_review_artifact"}
	case "local":
		policy.OutboundMode = "draft_only"
		policy.PublicSurfaceMode = "none"
		policy.SharedInferenceReuse = "disabled"
		policy.SharedInferenceReuseScope = "public_prefix_only"
		policy.CapabilityEnvelope = []string{"local_workspace", "bounded_review_artifact"}
	case "external":
		if strings.TrimSpace(policy.OutboundMode) == "" {
			policy.OutboundMode = "read_only"
		}
		if strings.TrimSpace(policy.PublicSurfaceMode) == "" {
			policy.PublicSurfaceMode = "explicit_parent_relay_only"
		}
		if strings.TrimSpace(policy.SharedInferenceReuse) == "" {
			policy.SharedInferenceReuse = "disabled"
		}
		if strings.TrimSpace(policy.SharedInferenceReuseScope) == "" {
			policy.SharedInferenceReuseScope = "public_prefix_only"
		}
		if len(policy.CapabilityEnvelope) == 0 {
			policy.CapabilityEnvelope = []string{"read_channel", "bounded_review_artifact"}
		}
	}
}

func applyDurableAgentModeRuntimeDefaults(agent *core.DurableAgent) {
	if agent == nil {
		return
	}
	switch durableAgentModeFromPolicy(agent.LivePolicy) {
	case "sketch", "local":
		if strings.TrimSpace(agent.WakeupMode) == "" || normalizeDurableChannelWakeupMode(agent.WakeupMode) == "poll" {
			agent.WakeupMode = "manual"
		}
	}
}

func mergeDurableAgentChannelConfig(existing core.DurableAgentChannelConfig, raw json.RawMessage) (core.DurableAgentChannelConfig, error) {
	existing = core.NormalizeDurableAgentChannelConfig(existing)
	if len(raw) == 0 || strings.TrimSpace(string(raw)) == "" {
		return existing, nil
	}
	type channelConfigInput struct {
		External *core.DurableAgentExternalChannelConfig `json:"external,omitempty"`
		Channel  *core.DurableAgentExternalChannelConfig `json:"channel,omitempty"`
	}
	var updateRaw channelConfigInput
	if err := json.Unmarshal(raw, &updateRaw); err != nil {
		return core.DurableAgentChannelConfig{}, fmt.Errorf("decode durable_agent channel_config: %w", err)
	}
	update := core.DurableAgentChannelConfig{}
	switch {
	case updateRaw.External != nil:
		cfg := *updateRaw.External
		update.External = &cfg
	case updateRaw.Channel != nil:
		cfg := *updateRaw.Channel
		update.External = &cfg
	}
	update = core.NormalizeDurableAgentChannelConfig(update)
	if external := update.ExternalConfig(); external != nil {
		if existingExternal := existing.ExternalConfig(); existingExternal == nil {
			cfg := *external
			existing.External = &cfg
		} else {
			cfg := *existingExternal
			mergeDurableAgentExternalChannelConfig(&cfg, *external)
			existing.External = &cfg
		}
	}
	return core.NormalizeDurableAgentChannelConfig(existing), nil
}

func mergeDurableAgentExternalChannelConfig(dst *core.DurableAgentExternalChannelConfig, src core.DurableAgentExternalChannelConfig) {
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
	switch durableAgentModeFromPolicy(agent.LivePolicy) {
	case "sketch", "local":
		return nil
	}
	switch normalizeDurableAgentChannelKind(agent.ChannelKind) {
	case "external_channel":
		external := agent.ChannelConfig.ExternalConfig()
		if external == nil {
			return fmt.Errorf("durable agent %q cannot activate without external channel_config", agent.AgentID)
		}
		if strings.TrimSpace(external.Address) == "" {
			return fmt.Errorf("durable agent %q cannot activate without a channel address", agent.AgentID)
		}
		if strings.TrimSpace(external.Adapter) == "" {
			return fmt.Errorf("durable agent %q cannot activate without a channel adapter", agent.AgentID)
		}
		if strings.TrimSpace(agent.WakeupMode) == "" {
			return fmt.Errorf("durable agent %q cannot activate without a wakeup_mode", agent.AgentID)
		}
	}
	return nil
}

func (r *Registry) inheritDurableAgentBootstrapIfMissing(agent *core.DurableAgent) {
	if r == nil || agent == nil {
		return
	}
	if core.NormalizeNodeLLMBootstrap(agent.BootstrapLLM).Configured() {
		return
	}
	inherited := core.NormalizeNodeLLMBootstrap(r.durableAgentBootstrapLLM)
	if !inherited.Configured() {
		return
	}
	agent.BootstrapLLM = inherited
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
