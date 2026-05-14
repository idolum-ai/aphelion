//go:build linux

package core

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strings"
)

type DurableAgentLivePolicy struct {
	Mode                      string   `json:"mode,omitempty"`
	Charter                   string   `json:"charter,omitempty"`
	CapabilityEnvelope        []string `json:"capability_envelope,omitempty"`
	OutboundMode              string   `json:"outbound_mode,omitempty"`
	DriftPolicy               string   `json:"drift_policy,omitempty"`
	PublicSurfaceMode         string   `json:"public_surface_mode,omitempty"`
	SharedInferenceReuse      string   `json:"shared_inference_reuse,omitempty"`
	SharedInferenceReuseScope string   `json:"shared_inference_reuse_scope,omitempty"`
	TailnetMode               string   `json:"tailnet_mode,omitempty"`
	TailnetHostname           string   `json:"tailnet_hostname,omitempty"`
	TailnetTags               []string `json:"tailnet_tags,omitempty"`
	TailnetSurfacePolicy      string   `json:"tailnet_surface_policy,omitempty"`
}

type DurableAgentChannelConfig struct {
	External        *DurableAgentExternalChannelConfig        `json:"external,omitempty"`
	ScheduledReview *DurableAgentScheduledReviewChannelConfig `json:"scheduled_review,omitempty"`
}

type DurableAgentExternalChannelConfig struct {
	Address          string   `json:"address,omitempty"`
	Account          string   `json:"account,omitempty"`
	Adapter          string   `json:"adapter,omitempty"`
	Query            string   `json:"query,omitempty"`
	PollInterval     string   `json:"poll_interval,omitempty"`
	SurfaceRules     []string `json:"surface_rules,omitempty"`
	SummarizePDFs    bool     `json:"summarize_pdfs,omitempty"`
	SynthesisCadence string   `json:"synthesis_cadence,omitempty"`
	NeverRetain      []string `json:"never_retain,omitempty"`
}

type DurableAgentScheduledReviewChannelConfig struct {
	Title            string `json:"title,omitempty"`
	ScheduleKind     string `json:"schedule_kind,omitempty"`
	TimeUTC          string `json:"time_utc,omitempty"`
	Window           string `json:"window,omitempty"`
	MaxMessages      int    `json:"max_messages,omitempty"`
	ArtifactKind     string `json:"artifact_kind,omitempty"`
	TranscriptDir    string `json:"transcript_dir,omitempty"`
	PromptTemplate   string `json:"prompt_template,omitempty"`
	GuidanceQuestion string `json:"guidance_question,omitempty"`
	RecipeID         string `json:"recipe_id,omitempty"`
	RecipeVersion    string `json:"recipe_version,omitempty"`
	RecipeSource     string `json:"recipe_source,omitempty"`
}

type NodeLLMBootstrap struct {
	Backend         string `json:"backend,omitempty"`
	NativeProvider  string `json:"native_provider,omitempty"`
	APIKey          string `json:"api_key,omitempty"`
	BaseURL         string `json:"base_url,omitempty"`
	Model           string `json:"model,omitempty"`
	MaxTokens       int    `json:"max_tokens,omitempty"`
	CodexAuthSource string `json:"codex_auth_source,omitempty"`
	CodexHome       string `json:"codex_home,omitempty"`
	CodexBaseURL    string `json:"codex_base_url,omitempty"`
}

type DurableAgentBootstrapCeiling struct {
	CapabilityEnvelope           []string `json:"capability_envelope,omitempty"`
	AllowedOutboundModes         []string `json:"allowed_outbound_modes,omitempty"`
	AllowedPublicSurfaceModes    []string `json:"allowed_public_surface_modes,omitempty"`
	AllowedSharedInferenceReuse  []string `json:"allowed_shared_inference_reuse,omitempty"`
	AllowedSharedInferenceScopes []string `json:"allowed_shared_inference_scopes,omitempty"`
}

func DefaultTelegramGroupLivePolicy(charter string) DurableAgentLivePolicy {
	return NormalizeDurableAgentLivePolicy(DurableAgentLivePolicy{
		Mode:                      "live",
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
	policy.Mode = NormalizeDurableAgentMode(policy.Mode)
	if policy.Mode == "" {
		policy.Mode = "live"
	}
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
	policy.TailnetMode = normalizeDurableAgentTailnetMode(policy.TailnetMode)
	policy.TailnetHostname = strings.ToLower(strings.TrimSpace(policy.TailnetHostname))
	policy.TailnetTags = normalizeDurableAgentStringSet(policy.TailnetTags)
	policy.TailnetSurfacePolicy = normalizeDurableAgentTailnetSurfacePolicy(policy.TailnetSurfacePolicy)
	if policy.TailnetMode == "" {
		policy.TailnetHostname = ""
		policy.TailnetTags = nil
		policy.TailnetSurfacePolicy = ""
	} else if policy.TailnetSurfacePolicy == "" {
		policy.TailnetSurfacePolicy = "private_status"
	}
	return policy
}

func NormalizeDurableAgentChannelConfig(cfg DurableAgentChannelConfig) DurableAgentChannelConfig {
	if cfg.External != nil {
		normalized := NormalizeDurableAgentExternalChannelConfig(*cfg.External)
		cfg.External = &normalized
	}
	if cfg.ScheduledReview != nil {
		normalized := NormalizeDurableAgentScheduledReviewChannelConfig(*cfg.ScheduledReview)
		cfg.ScheduledReview = &normalized
	}
	return cfg
}

func (cfg DurableAgentChannelConfig) MarshalJSON() ([]byte, error) {
	cfg = NormalizeDurableAgentChannelConfig(cfg)
	type channelConfigJSON struct {
		External        *DurableAgentExternalChannelConfig        `json:"external,omitempty"`
		ScheduledReview *DurableAgentScheduledReviewChannelConfig `json:"scheduled_review,omitempty"`
	}
	return json.Marshal(channelConfigJSON{External: cfg.External, ScheduledReview: cfg.ScheduledReview})
}

func (cfg DurableAgentChannelConfig) ExternalConfig() *DurableAgentExternalChannelConfig {
	cfg = NormalizeDurableAgentChannelConfig(cfg)
	return cfg.External
}

func (cfg DurableAgentChannelConfig) ScheduledReviewConfig() *DurableAgentScheduledReviewChannelConfig {
	cfg = NormalizeDurableAgentChannelConfig(cfg)
	return cfg.ScheduledReview
}

func NormalizeDurableAgentExternalChannelConfig(cfg DurableAgentExternalChannelConfig) DurableAgentExternalChannelConfig {
	cfg.Address = strings.TrimSpace(cfg.Address)
	cfg.Account = strings.TrimSpace(cfg.Account)
	cfg.Adapter = normalizeDurableAgentChannelAdapter(cfg.Adapter)
	cfg.Query = strings.TrimSpace(cfg.Query)
	cfg.PollInterval = strings.TrimSpace(cfg.PollInterval)
	cfg.SurfaceRules = normalizeDurableAgentStringSet(cfg.SurfaceRules)
	cfg.SynthesisCadence = strings.TrimSpace(cfg.SynthesisCadence)
	cfg.NeverRetain = normalizeDurableAgentStringSet(cfg.NeverRetain)
	return cfg
}

func NormalizeDurableAgentScheduledReviewChannelConfig(cfg DurableAgentScheduledReviewChannelConfig) DurableAgentScheduledReviewChannelConfig {
	cfg.Title = strings.TrimSpace(cfg.Title)
	cfg.ScheduleKind = strings.ToLower(strings.TrimSpace(cfg.ScheduleKind))
	cfg.TimeUTC = strings.TrimSpace(cfg.TimeUTC)
	cfg.Window = strings.ToLower(strings.TrimSpace(cfg.Window))
	cfg.ArtifactKind = strings.ToLower(strings.TrimSpace(cfg.ArtifactKind))
	cfg.TranscriptDir = strings.Trim(strings.TrimSpace(cfg.TranscriptDir), "/")
	cfg.PromptTemplate = strings.TrimSpace(cfg.PromptTemplate)
	cfg.GuidanceQuestion = strings.TrimSpace(cfg.GuidanceQuestion)
	cfg.RecipeID = strings.TrimSpace(cfg.RecipeID)
	cfg.RecipeVersion = strings.TrimSpace(cfg.RecipeVersion)
	cfg.RecipeSource = strings.TrimSpace(cfg.RecipeSource)
	if cfg.MaxMessages < 0 {
		cfg.MaxMessages = 0
	}
	return cfg
}

func NormalizeDurableAgentAllowedTelegramUserIDs(values []int64) []int64 {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[int64]struct{}, len(values))
	out := make([]int64, 0, len(values))
	for _, value := range values {
		if value <= 0 {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	if len(out) == 0 {
		return nil
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func NormalizeDurableAgentMode(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "":
		return ""
	case "live", "full", "autonomous":
		return "live"
	case "sketch", "idea", "proposal":
		return "sketch"
	case "local", "local_only", "local-only", "draft", "drafts":
		return "local"
	case "external", "external_channel", "external-channel", "observe", "observer":
		return "external"
	default:
		return "live"
	}
}

func (cfg DurableAgentChannelConfig) IsZero() bool {
	cfg = NormalizeDurableAgentChannelConfig(cfg)
	return cfg.External == nil && cfg.ScheduledReview == nil
}

func NormalizeDurableAgentBootstrapCeiling(ceiling DurableAgentBootstrapCeiling) DurableAgentBootstrapCeiling {
	ceiling.CapabilityEnvelope = normalizeDurableAgentStringSet(ceiling.CapabilityEnvelope)
	ceiling.AllowedOutboundModes = normalizeDurableAgentPolicyModes(ceiling.AllowedOutboundModes)
	ceiling.AllowedPublicSurfaceModes = normalizeDurableAgentPublicSurfaceModes(ceiling.AllowedPublicSurfaceModes)
	ceiling.AllowedSharedInferenceReuse = normalizeDurableAgentSharedInferenceReuseValues(ceiling.AllowedSharedInferenceReuse)
	ceiling.AllowedSharedInferenceScopes = normalizeDurableAgentSharedInferenceReuseScopes(ceiling.AllowedSharedInferenceScopes)
	return ceiling
}

func NormalizeNodeLLMBootstrap(bootstrap NodeLLMBootstrap) NodeLLMBootstrap {
	bootstrap.Backend = normalizeNodeLLMBackend(bootstrap.Backend)
	bootstrap.NativeProvider = normalizeNodeNativeProviderName(bootstrap.NativeProvider)
	bootstrap.APIKey = strings.TrimSpace(bootstrap.APIKey)
	bootstrap.BaseURL = strings.TrimSpace(bootstrap.BaseURL)
	bootstrap.Model = strings.TrimSpace(bootstrap.Model)
	bootstrap.CodexAuthSource = normalizeNodeCodexAuthSource(bootstrap.CodexAuthSource)
	bootstrap.CodexHome = strings.TrimSpace(bootstrap.CodexHome)
	bootstrap.CodexBaseURL = strings.TrimSpace(bootstrap.CodexBaseURL)
	if bootstrap.MaxTokens < 0 {
		bootstrap.MaxTokens = 0
	}
	if bootstrap.Backend == "" {
		hasNativeFields := bootstrap.NativeProvider != "" ||
			bootstrap.APIKey != "" ||
			bootstrap.BaseURL != "" ||
			bootstrap.Model != "" ||
			bootstrap.MaxTokens > 0
		hasCodexFields := bootstrap.CodexAuthSource != "" ||
			bootstrap.CodexHome != "" ||
			bootstrap.CodexBaseURL != ""
		switch {
		case hasCodexFields:
			bootstrap.Backend = "codex"
		case hasNativeFields:
			bootstrap.Backend = "native"
		}
	}
	switch bootstrap.Backend {
	case "":
		return NodeLLMBootstrap{}
	case "native":
		bootstrap.CodexAuthSource = ""
		bootstrap.CodexHome = ""
		bootstrap.CodexBaseURL = ""
		if bootstrap.NativeProvider == "" {
			return NodeLLMBootstrap{}
		}
	case "codex":
		bootstrap.NativeProvider = ""
		bootstrap.APIKey = ""
		bootstrap.BaseURL = ""
		bootstrap.Model = ""
		bootstrap.MaxTokens = 0
		if bootstrap.CodexAuthSource == "" {
			bootstrap.CodexAuthSource = "codex_cli"
		}
	}
	return bootstrap
}

func (b NodeLLMBootstrap) Configured() bool {
	b = NormalizeNodeLLMBootstrap(b)
	switch b.Backend {
	case "native":
		return b.NativeProvider != "" && b.APIKey != ""
	case "codex":
		return b.CodexHome != ""
	default:
		return false
	}
}

func ValidateNodeLLMBootstrap(bootstrap NodeLLMBootstrap) error {
	bootstrap = NormalizeNodeLLMBootstrap(bootstrap)
	switch bootstrap.Backend {
	case "":
		return &NodeLLMBootstrapError{Field: "backend", Message: "backend is required"}
	case "native":
		if bootstrap.NativeProvider == "" {
			return &NodeLLMBootstrapError{Field: "native_provider", Message: "native_provider is required for native backend"}
		}
		if strings.TrimSpace(bootstrap.APIKey) == "" {
			return &NodeLLMBootstrapError{Field: "api_key", Message: "api_key is required for native backend"}
		}
		if bootstrap.MaxTokens < 0 {
			return &NodeLLMBootstrapError{Field: "max_tokens", Message: "max_tokens must be >= 0"}
		}
		return nil
	case "codex":
		if bootstrap.CodexHome == "" {
			return &NodeLLMBootstrapError{Field: "codex_home", Message: "codex_home is required for codex backend"}
		}
		return nil
	default:
		return &NodeLLMBootstrapError{Field: "backend", Message: "backend must be one of native|codex"}
	}
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

func (s DurableAgentContinuityState) IsZero() bool {
	return len(s.RecentInteractions) == 0 &&
		len(s.PendingQuestions) == 0 &&
		len(s.ReviewRefs) == 0 &&
		len(s.RatifiedOutcomes) == 0 &&
		s.Conversation == nil &&
		s.SetupWizard == nil &&
		s.EmailPending == nil &&
		s.ExternalChannel == nil &&
		s.ScheduledReview == nil
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
	case "parent_relay_only":
		return "explicit_parent_relay_only"
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

func normalizeDurableAgentTailnetMode(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "none", "off", "disabled":
		return ""
	case "tsnet", "embedded_tsnet", "embedded-tsnet":
		return "tsnet"
	case "tagged_node", "tagged-node":
		return "tagged_node"
	default:
		return ""
	}
}

func normalizeDurableAgentTailnetSurfacePolicy(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "none", "off", "disabled":
		return ""
	case "status", "private_status", "private-status", "private_status_only":
		return "private_status"
	case "private_services", "private-services", "private":
		return "private_services"
	default:
		return ""
	}
}

func normalizeDurableAgentChannelAdapter(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func normalizeNodeNativeProviderName(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "anthropic", "openai", "openrouter":
		return strings.ToLower(strings.TrimSpace(value))
	default:
		return ""
	}
}

func normalizeNodeLLMBackend(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "native", "codex":
		return strings.ToLower(strings.TrimSpace(value))
	default:
		return ""
	}
}

func normalizeNodeCodexAuthSource(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "auto", "codex_cli":
		return strings.ToLower(strings.TrimSpace(value))
	default:
		return ""
	}
}

type NodeLLMBootstrapError struct {
	Field   string
	Message string
}

func (e *NodeLLMBootstrapError) Error() string {
	if e == nil {
		return "invalid node llm bootstrap"
	}
	field := strings.TrimSpace(e.Field)
	if field == "" {
		return "invalid node llm bootstrap"
	}
	msg := strings.TrimSpace(e.Message)
	if msg == "" {
		return "invalid node llm bootstrap for " + field
	}
	return "invalid node llm bootstrap for " + field + ": " + msg
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
