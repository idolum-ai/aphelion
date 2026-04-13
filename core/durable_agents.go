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

type DurableAgent struct {
	AgentID            string
	ParentAgentID      string
	ParentScopeKind    string
	ParentScopeID      string
	ReviewTargetChatID int64
	ChannelKind        string
	LivePolicy         DurableAgentLivePolicy
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
