//go:build linux

package core

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"time"
)

type DurableAgentLivePolicy struct {
	Charter                   string   `json:"charter,omitempty"`
	CapabilityEnvelope        []string `json:"capability_envelope,omitempty"`
	OutboundMode              string   `json:"outbound_mode,omitempty"`
	DriftPolicy               string   `json:"drift_policy,omitempty"`
	PublicSurfaceMode         string   `json:"public_surface_mode,omitempty"`
	SharedInferenceReuse      string   `json:"shared_inference_reuse,omitempty"`
	SharedInferenceReuseScope string   `json:"shared_inference_reuse_scope,omitempty"`
}

type DurableAgentBootstrapCeiling struct {
	CapabilityEnvelope           []string `json:"capability_envelope,omitempty"`
	AllowedOutboundModes         []string `json:"allowed_outbound_modes,omitempty"`
	AllowedPublicSurfaceModes    []string `json:"allowed_public_surface_modes,omitempty"`
	AllowedSharedInferenceReuse  []string `json:"allowed_shared_inference_reuse,omitempty"`
	AllowedSharedInferenceScopes []string `json:"allowed_shared_inference_scopes,omitempty"`
}

type DurableAgent struct {
	AgentID            string
	ParentAgentID      string
	ParentScopeKind    string
	ParentScopeID      string
	ReviewTargetChatID int64
	ChannelKind        string
	LivePolicy         DurableAgentLivePolicy
	BootstrapCeiling   DurableAgentBootstrapCeiling
	PolicyVersion      int64
	PolicyHash         string
	PolicyIssuedAt     time.Time
	LocalStorageRoots  []string
	NetworkPolicy      string
	WakeupMode         string
	SecretScopes       []string
	Status             string
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

type DurableAgentState struct {
	AgentID      string
	Cursor       string
	Status       string
	StateJSON    string
	LastWakeAt   time.Time
	LastReviewAt time.Time
	DormantAt    time.Time
	UpdatedAt    time.Time
}

type DurableReviewArtifact struct {
	AgentID       string
	Summary       string
	IntervalLabel string
	LocalActions  []string
	Questions     []string
	RiskFlags     []string
	ArtifactRefs  []string
	Metadata      map[string]string
}

func DefaultTelegramGroupLivePolicy(charter string) DurableAgentLivePolicy {
	return NormalizeDurableAgentLivePolicy(DurableAgentLivePolicy{
		Charter:                   strings.TrimSpace(charter),
		CapabilityEnvelope:        []string{"group_reply", "bounded_review_artifact"},
		OutboundMode:              "reply_with_policy_authorization",
		DriftPolicy:               "admin_review",
		PublicSurfaceMode:         "none",
		SharedInferenceReuse:      "disabled",
		SharedInferenceReuseScope: "public_prefix_only",
	})
}

func DefaultDurableAgentBootstrapCeiling(channelKind string, policy DurableAgentLivePolicy) DurableAgentBootstrapCeiling {
	policy = NormalizeDurableAgentLivePolicy(policy)
	switch strings.TrimSpace(channelKind) {
	case "telegram_group":
		capabilityEnvelope := append([]string(nil), policy.CapabilityEnvelope...)
		if len(capabilityEnvelope) == 0 {
			capabilityEnvelope = []string{"group_reply", "bounded_review_artifact"}
		}
		return NormalizeDurableAgentBootstrapCeiling(DurableAgentBootstrapCeiling{
			CapabilityEnvelope:           capabilityEnvelope,
			AllowedOutboundModes:         []string{"read_only", "draft_only", "reply_with_parent_review", "reply_with_policy_authorization"},
			AllowedPublicSurfaceModes:    []string{"none", "channel_transcript", "explicit_parent_relay_only"},
			AllowedSharedInferenceReuse:  []string{"disabled", "allowed"},
			AllowedSharedInferenceScopes: []string{"public_prefix_only"},
		})
	default:
		return NormalizeDurableAgentBootstrapCeiling(DurableAgentBootstrapCeiling{
			CapabilityEnvelope:           append([]string(nil), policy.CapabilityEnvelope...),
			AllowedOutboundModes:         []string{policy.OutboundMode},
			AllowedPublicSurfaceModes:    []string{policy.PublicSurfaceMode},
			AllowedSharedInferenceReuse:  []string{policy.SharedInferenceReuse},
			AllowedSharedInferenceScopes: []string{policy.SharedInferenceReuseScope},
		})
	}
}

func NormalizeDurableAgentLivePolicy(policy DurableAgentLivePolicy) DurableAgentLivePolicy {
	policy.Charter = strings.TrimSpace(policy.Charter)
	policy.OutboundMode = normalizeDurableAgentPolicyMode(policy.OutboundMode)
	policy.DriftPolicy = strings.TrimSpace(policy.DriftPolicy)
	if policy.DriftPolicy == "" {
		policy.DriftPolicy = "admin_review"
	}
	policy.PublicSurfaceMode = normalizeDurableAgentPublicSurfaceMode(policy.PublicSurfaceMode)
	policy.SharedInferenceReuse = normalizeDurableAgentSharedInferenceReuse(policy.SharedInferenceReuse)
	policy.SharedInferenceReuseScope = normalizeDurableAgentSharedInferenceReuseScope(policy.SharedInferenceReuseScope)
	policy.CapabilityEnvelope = normalizeDurableAgentStringSet(policy.CapabilityEnvelope)
	return policy
}

func NormalizeDurableAgentBootstrapCeiling(ceiling DurableAgentBootstrapCeiling) DurableAgentBootstrapCeiling {
	ceiling.CapabilityEnvelope = normalizeDurableAgentStringSet(ceiling.CapabilityEnvelope)
	ceiling.AllowedOutboundModes = normalizeDurableAgentPolicyModes(ceiling.AllowedOutboundModes)
	ceiling.AllowedPublicSurfaceModes = normalizeDurableAgentPublicSurfaceModes(ceiling.AllowedPublicSurfaceModes)
	ceiling.AllowedSharedInferenceReuse = normalizeDurableAgentSharedInferenceReuseValues(ceiling.AllowedSharedInferenceReuse)
	ceiling.AllowedSharedInferenceScopes = normalizeDurableAgentSharedInferenceReuseScopes(ceiling.AllowedSharedInferenceScopes)
	return ceiling
}

func (c DurableAgentBootstrapCeiling) IsZero() bool {
	return len(c.CapabilityEnvelope) == 0 &&
		len(c.AllowedOutboundModes) == 0 &&
		len(c.AllowedPublicSurfaceModes) == 0 &&
		len(c.AllowedSharedInferenceReuse) == 0 &&
		len(c.AllowedSharedInferenceScopes) == 0
}

func DurableAgentPolicyHash(policy DurableAgentLivePolicy) (string, error) {
	raw, err := json.Marshal(NormalizeDurableAgentLivePolicy(policy))
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

func normalizeDurableAgentPolicyMode(mode string) string {
	switch strings.TrimSpace(mode) {
	case "read_only", "draft_only", "reply_with_parent_review", "reply_with_policy_authorization":
		return strings.TrimSpace(mode)
	case "reply_within_charter":
		return "reply_with_policy_authorization"
	default:
		return "reply_with_policy_authorization"
	}
}

func normalizeDurableAgentPublicSurfaceMode(mode string) string {
	switch strings.TrimSpace(mode) {
	case "channel_transcript", "explicit_parent_relay_only":
		return strings.TrimSpace(mode)
	default:
		return "none"
	}
}

func normalizeDurableAgentSharedInferenceReuse(value string) string {
	switch strings.TrimSpace(value) {
	case "allowed":
		return "allowed"
	default:
		return "disabled"
	}
}

func normalizeDurableAgentSharedInferenceReuseScope(value string) string {
	switch strings.TrimSpace(value) {
	case "":
		return "public_prefix_only"
	case "public_prefix_only":
		return value
	default:
		return "public_prefix_only"
	}
}

func ValidateDurableAgentLivePolicyWithinCeiling(policy DurableAgentLivePolicy, ceiling DurableAgentBootstrapCeiling) error {
	policy = NormalizeDurableAgentLivePolicy(policy)
	ceiling = NormalizeDurableAgentBootstrapCeiling(ceiling)
	if ceiling.IsZero() {
		return nil
	}
	if len(ceiling.CapabilityEnvelope) > 0 {
		if disallowed := missingFromSet(policy.CapabilityEnvelope, ceiling.CapabilityEnvelope); len(disallowed) > 0 {
			return newCeilingViolation("capability_envelope", disallowed, ceiling.CapabilityEnvelope)
		}
	}
	if len(ceiling.AllowedOutboundModes) > 0 && !containsNormalized(ceiling.AllowedOutboundModes, policy.OutboundMode) {
		return newCeilingViolation("outbound_mode", []string{policy.OutboundMode}, ceiling.AllowedOutboundModes)
	}
	if len(ceiling.AllowedPublicSurfaceModes) > 0 && !containsNormalized(ceiling.AllowedPublicSurfaceModes, policy.PublicSurfaceMode) {
		return newCeilingViolation("public_surface_mode", []string{policy.PublicSurfaceMode}, ceiling.AllowedPublicSurfaceModes)
	}
	if len(ceiling.AllowedSharedInferenceReuse) > 0 && !containsNormalized(ceiling.AllowedSharedInferenceReuse, policy.SharedInferenceReuse) {
		return newCeilingViolation("shared_inference_reuse", []string{policy.SharedInferenceReuse}, ceiling.AllowedSharedInferenceReuse)
	}
	if policy.SharedInferenceReuse == "allowed" && len(ceiling.AllowedSharedInferenceScopes) > 0 && !containsNormalized(ceiling.AllowedSharedInferenceScopes, policy.SharedInferenceReuseScope) {
		return newCeilingViolation("shared_inference_reuse_scope", []string{policy.SharedInferenceReuseScope}, ceiling.AllowedSharedInferenceScopes)
	}
	return nil
}

type DurableAgentPolicyCeilingError struct {
	Field     string
	Requested []string
	Allowed   []string
}

func (e *DurableAgentPolicyCeilingError) Error() string {
	if e == nil {
		return "durable agent live policy exceeds bootstrap ceiling"
	}
	return "durable agent live policy exceeds bootstrap ceiling for " + strings.TrimSpace(e.Field)
}

func normalizeDurableAgentStringSet(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(values))
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

func normalizeDurableAgentPolicyModes(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		out = append(out, normalizeDurableAgentPolicyMode(value))
	}
	return normalizeDurableAgentStringSet(out)
}

func normalizeDurableAgentPublicSurfaceModes(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		out = append(out, normalizeDurableAgentPublicSurfaceMode(value))
	}
	return normalizeDurableAgentStringSet(out)
}

func normalizeDurableAgentSharedInferenceReuseValues(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		out = append(out, normalizeDurableAgentSharedInferenceReuse(value))
	}
	return normalizeDurableAgentStringSet(out)
}

func normalizeDurableAgentSharedInferenceReuseScopes(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		out = append(out, normalizeDurableAgentSharedInferenceReuseScope(value))
	}
	return normalizeDurableAgentStringSet(out)
}

func newCeilingViolation(field string, requested []string, allowed []string) error {
	return &DurableAgentPolicyCeilingError{
		Field:     strings.TrimSpace(field),
		Requested: normalizeDurableAgentStringSet(requested),
		Allowed:   normalizeDurableAgentStringSet(allowed),
	}
}

func missingFromSet(requested []string, allowed []string) []string {
	if len(requested) == 0 {
		return nil
	}
	allowedSet := make(map[string]struct{}, len(allowed))
	for _, value := range allowed {
		allowedSet[strings.TrimSpace(value)] = struct{}{}
	}
	missing := make([]string, 0)
	for _, value := range requested {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := allowedSet[value]; ok {
			continue
		}
		missing = append(missing, value)
	}
	return normalizeDurableAgentStringSet(missing)
}

func containsNormalized(values []string, needle string) bool {
	needle = strings.TrimSpace(needle)
	if needle == "" {
		return false
	}
	for _, value := range values {
		if strings.TrimSpace(value) == needle {
			return true
		}
	}
	return false
}
