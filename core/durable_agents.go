//go:build linux

package core

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
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

type DurableAgentChannelConfig struct {
	External *DurableAgentExternalChannelConfig `json:"external,omitempty"`
	Email    *DurableAgentEmailChannelConfig    `json:"email,omitempty"`
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

// DurableAgentEmailChannelConfig is retained as a legacy JSON/type alias while
// Aphelion migrates parent-owned channel configuration to generic external
// channel records. New parent code should use DurableAgentExternalChannelConfig.
type DurableAgentEmailChannelConfig = DurableAgentExternalChannelConfig

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

type DurableAgent struct {
	AgentID                string
	ParentAgentID          string
	ParentScopeKind        string
	ParentScopeID          string
	ReviewTargetChatID     int64
	ChannelKind            string
	LivePolicy             DurableAgentLivePolicy
	ChannelConfig          DurableAgentChannelConfig
	BootstrapCeiling       DurableAgentBootstrapCeiling
	BootstrapLLM           NodeLLMBootstrap
	ControlPlaneSecret     string
	PolicyVersion          int64
	PolicyHash             string
	PolicyIssuedAt         time.Time
	LocalStorageRoots      []string
	NetworkPolicy          string
	WakeupMode             string
	SecretScopes           []string
	AllowedTelegramUserIDs []int64
	Status                 string
	CreatedAt              time.Time
	UpdatedAt              time.Time
}

type DurableAgentState struct {
	AgentID                       string
	Cursor                        string
	Status                        string
	StateJSON                     string
	LastOfferedPolicyVersion      int64
	LastOfferedPolicyHash         string
	LastOfferedPolicyAt           time.Time
	LastAcknowledgedPolicyVersion int64
	LastAcknowledgedPolicyHash    string
	LastAcknowledgedPolicyAt      time.Time
	LastAppliedPolicyVersion      int64
	LastAppliedPolicyHash         string
	LastAppliedPolicyAt           time.Time
	LastApplyStatus               string
	LastApplyError                string
	LastWakeAt                    time.Time
	LastReviewAt                  time.Time
	DormantAt                     time.Time
	UpdatedAt                     time.Time
}

// DurableAgentIdentityState stores durable policy-handshake identity that
// should remain canonical and independent from runtime wake posture.
type DurableAgentIdentityState struct {
	AgentID                       string
	LastOfferedPolicyVersion      int64
	LastOfferedPolicyHash         string
	LastOfferedPolicyAt           time.Time
	LastAcknowledgedPolicyVersion int64
	LastAcknowledgedPolicyHash    string
	LastAcknowledgedPolicyAt      time.Time
	LastAppliedPolicyVersion      int64
	LastAppliedPolicyHash         string
	LastAppliedPolicyAt           time.Time
	UpdatedAt                     time.Time
}

// DurableAgentRuntimeState stores runtime/apply posture and continuity data.
type DurableAgentRuntimeState struct {
	AgentID         string
	Cursor          string
	Status          string
	StateJSON       string
	LastApplyStatus string
	LastApplyError  string
	LastWakeAt      time.Time
	LastReviewAt    time.Time
	DormantAt       time.Time
	UpdatedAt       time.Time
}

func DurableAgentIdentityStateFrom(state DurableAgentState) DurableAgentIdentityState {
	return DurableAgentIdentityState{
		AgentID:                       state.AgentID,
		LastOfferedPolicyVersion:      state.LastOfferedPolicyVersion,
		LastOfferedPolicyHash:         state.LastOfferedPolicyHash,
		LastOfferedPolicyAt:           state.LastOfferedPolicyAt,
		LastAcknowledgedPolicyVersion: state.LastAcknowledgedPolicyVersion,
		LastAcknowledgedPolicyHash:    state.LastAcknowledgedPolicyHash,
		LastAcknowledgedPolicyAt:      state.LastAcknowledgedPolicyAt,
		LastAppliedPolicyVersion:      state.LastAppliedPolicyVersion,
		LastAppliedPolicyHash:         state.LastAppliedPolicyHash,
		LastAppliedPolicyAt:           state.LastAppliedPolicyAt,
		UpdatedAt:                     state.UpdatedAt,
	}
}

func DurableAgentRuntimeStateFrom(state DurableAgentState) DurableAgentRuntimeState {
	return DurableAgentRuntimeState{
		AgentID:         state.AgentID,
		Cursor:          state.Cursor,
		Status:          state.Status,
		StateJSON:       state.StateJSON,
		LastApplyStatus: state.LastApplyStatus,
		LastApplyError:  state.LastApplyError,
		LastWakeAt:      state.LastWakeAt,
		LastReviewAt:    state.LastReviewAt,
		DormantAt:       state.DormantAt,
		UpdatedAt:       state.UpdatedAt,
	}
}

type DurableAgentContinuityState struct {
	RecentInteractions []DurableAgentRecentInteraction `json:"recent_interactions,omitempty"`
	PendingQuestions   []DurableAgentPendingQuestion   `json:"pending_questions,omitempty"`
	ReviewRefs         []DurableAgentReviewReference   `json:"review_refs,omitempty"`
	RatifiedOutcomes   []DurableAgentRatifiedOutcome   `json:"ratified_outcomes,omitempty"`
	Conversation       *DurableAgentConversationState  `json:"conversation,omitempty"`
	SetupWizard        *DurableAgentSetupWizardState   `json:"setup_wizard,omitempty"`
	EmailPending       *DurableAgentEmailPendingState  `json:"email_pending,omitempty"`
}

type DurableAgentConversationState struct {
	Messages []DurableAgentConversationMessage `json:"messages,omitempty"`
}

type DurableAgentConversationMessage struct {
	Role           string    `json:"role,omitempty"`
	Text           string    `json:"text,omitempty"`
	CreatedAt      time.Time `json:"created_at,omitempty"`
	AcknowledgedAt time.Time `json:"acknowledged_at,omitempty"`
}

type DurableAgentSetupWizardState struct {
	SchemaVersion int                            `json:"schema_version,omitempty"`
	ChannelKind   string                         `json:"channel_kind,omitempty"`
	Status        string                         `json:"status,omitempty"`
	CurrentStep   string                         `json:"current_step,omitempty"`
	Answers       DurableAgentSetupWizardAnswers `json:"answers,omitempty"`
	Missing       []string                       `json:"missing,omitempty"`
	StartedAt     time.Time                      `json:"started_at,omitempty"`
	UpdatedAt     time.Time                      `json:"updated_at,omitempty"`
}

type DurableAgentSetupWizardAnswers struct {
	Address          string   `json:"address,omitempty"`
	Account          string   `json:"account,omitempty"`
	Adapter          string   `json:"adapter,omitempty"`
	Query            string   `json:"query,omitempty"`
	BootstrapProfile string   `json:"bootstrap_profile,omitempty"`
	BootstrapModel   string   `json:"bootstrap_model,omitempty"`
	Charter          string   `json:"charter,omitempty"`
	Autonomy         string   `json:"autonomy,omitempty"`
	WakeupMode       string   `json:"wakeup_mode,omitempty"`
	PollInterval     string   `json:"poll_interval,omitempty"`
	SurfaceRules     []string `json:"surface_rules,omitempty"`
	SummarizePDFs    *bool    `json:"summarize_pdfs,omitempty"`
	SynthesisCadence string   `json:"synthesis_cadence,omitempty"`
	Capabilities     []string `json:"capabilities,omitempty"`
	NeverRetain      []string `json:"never_retain,omitempty"`
	DriftPolicy      string   `json:"drift_policy,omitempty"`
}

type DurableAgentEmailPendingState struct {
	Threads []DurableAgentEmailPendingThread `json:"threads,omitempty"`
}

type DurableAgentEmailPendingThread struct {
	ThreadID        string    `json:"thread_id,omitempty"`
	MessageID       string    `json:"message_id,omitempty"`
	From            string    `json:"from,omitempty"`
	Subject         string    `json:"subject,omitempty"`
	Snippet         string    `json:"snippet,omitempty"`
	Body            string    `json:"body,omitempty"`
	ReceivedAt      time.Time `json:"received_at,omitempty"`
	Labels          []string  `json:"labels,omitempty"`
	AttachmentNames []string  `json:"attachment_names,omitempty"`
}

type DurableAgentRecentInteraction struct {
	Summary       string    `json:"summary,omitempty"`
	Source        string    `json:"source,omitempty"`
	TriggerKinds  []string  `json:"trigger_kinds,omitempty"`
	ReviewEventID int64     `json:"review_event_id,omitempty"`
	OccurredAt    time.Time `json:"occurred_at,omitempty"`
}

type DurableAgentPendingQuestion struct {
	Question      string    `json:"question,omitempty"`
	ReviewEventID int64     `json:"review_event_id,omitempty"`
	Status        string    `json:"status,omitempty"`
	CreatedAt     time.Time `json:"created_at,omitempty"`
	UpdatedAt     time.Time `json:"updated_at,omitempty"`
}

type DurableAgentReviewReference struct {
	ReviewEventID int64     `json:"review_event_id,omitempty"`
	Summary       string    `json:"summary,omitempty"`
	RiskFlags     []string  `json:"risk_flags,omitempty"`
	Status        string    `json:"status,omitempty"`
	CreatedAt     time.Time `json:"created_at,omitempty"`
}

type DurableAgentRatifiedOutcome struct {
	Summary             string    `json:"summary,omitempty"`
	PolicyVersion       int64     `json:"policy_version,omitempty"`
	PolicyHash          string    `json:"policy_hash,omitempty"`
	SourceReviewEventID int64     `json:"source_review_event_id,omitempty"`
	AppliedAt           time.Time `json:"applied_at,omitempty"`
}

type DurableAgentRemoteEnrollment struct {
	AgentID          string
	ParentControlURL string
	KeyFingerprint   string
	ProtocolVersion  string
	Status           string
	LastSequence     int64
	EnrolledAt       time.Time
	LastSeenAt       time.Time
	RevokedAt        time.Time
}

type DurableAgentControlEnvelope struct {
	ProtocolVersion string          `json:"protocol_version,omitempty"`
	AgentID         string          `json:"agent_id,omitempty"`
	ParentAgentID   string          `json:"parent_agent_id,omitempty"`
	MessageKind     string          `json:"message_kind,omitempty"`
	MessageID       string          `json:"message_id,omitempty"`
	Sequence        int64           `json:"sequence,omitempty"`
	Timestamp       time.Time       `json:"timestamp,omitempty"`
	Payload         json.RawMessage `json:"payload,omitempty"`
	Signature       string          `json:"signature,omitempty"`
}

type DurableAgentControlReceipt struct {
	AgentID     string
	MessageID   string
	MessageKind string
	Sequence    int64
	ReceivedAt  time.Time
}

type DurableAgentPolicySnapshot struct {
	AgentID       string
	PolicyVersion int64
	PolicyHash    string
	IssuedAt      time.Time
	LivePolicy    DurableAgentLivePolicy
}

type DurableAgentPolicyAcknowledgement struct {
	AgentID             string
	AcknowledgedVersion int64
	AcknowledgedHash    string
	AppliedVersion      int64
	AppliedHash         string
	Status              string
	Error               string
	AcknowledgedAt      time.Time
}

type DurableAgentRemoteBootstrap struct {
	ReviewTargetChatID int64
	AgentID            string
	ParentAgentID      string
	ChannelKind        string
	ParentControlURL   string
	EnrollmentToken    string
	KeyFingerprint     string
	ProtocolVersion    string
	BootstrapLLM       NodeLLMBootstrap
	BootstrapCeiling   DurableAgentBootstrapCeiling
	LocalStorageRoots  []string
	SecretScopes       []string
	NetworkPolicy      string
}

type DurableAgentEnrollmentPayload struct {
	ReviewTargetChatID int64                        `json:"review_target_chat_id,omitempty"`
	AgentID            string                       `json:"agent_id,omitempty"`
	ParentAgentID      string                       `json:"parent_agent_id,omitempty"`
	ChannelKind        string                       `json:"channel_kind,omitempty"`
	ParentControlURL   string                       `json:"parent_control_url,omitempty"`
	EnrollmentToken    string                       `json:"enrollment_token,omitempty"`
	KeyFingerprint     string                       `json:"key_fingerprint,omitempty"`
	ProtocolVersion    string                       `json:"protocol_version,omitempty"`
	BootstrapLLM       NodeLLMBootstrap             `json:"bootstrap_llm,omitempty"`
	BootstrapCeiling   DurableAgentBootstrapCeiling `json:"bootstrap_ceiling,omitempty"`
	LocalStorageRoots  []string                     `json:"local_storage_roots,omitempty"`
	SecretScopes       []string                     `json:"secret_scopes,omitempty"`
	NetworkPolicy      string                       `json:"network_policy,omitempty"`
}

type DurableAgentEnrollmentRequest struct {
	Envelope DurableAgentControlEnvelope   `json:"envelope"`
	Payload  DurableAgentEnrollmentPayload `json:"payload"`
}

type DurableAgentEnrollmentResponse struct {
	Enrollment DurableAgentRemoteEnrollment `json:"enrollment"`
	Policy     DurableAgentPolicySnapshot   `json:"policy"`
}

type DurableAgentPolicyPollRequest struct {
	Envelope     DurableAgentControlEnvelope `json:"envelope"`
	KnownVersion int64                       `json:"known_version,omitempty"`
	KnownHash    string                      `json:"known_hash,omitempty"`
}

type DurableAgentPolicyPollResponse struct {
	Snapshot DurableAgentPolicySnapshot `json:"snapshot"`
	Changed  bool                       `json:"changed"`
}

type DurableAgentReviewArtifactUploadRequest struct {
	Envelope DurableAgentControlEnvelope `json:"envelope"`
	Artifact DurableReviewArtifact       `json:"artifact"`
}

type DurableAgentReviewArtifactUploadResponse struct {
	Accepted      bool  `json:"accepted"`
	ReviewEventID int64 `json:"review_event_id,omitempty"`
}

type DurableAgentPolicyAcknowledgementRequest struct {
	Envelope DurableAgentControlEnvelope       `json:"envelope"`
	Ack      DurableAgentPolicyAcknowledgement `json:"ack"`
}

type DurableAgentPolicyAcknowledgementResponse struct {
	Accepted bool `json:"accepted"`
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

const durableAgentContinuityMaxItems = 12
const durableAgentConversationMaxItems = 48
const durableAgentEmailPendingMaxItems = 200

const DefaultDurableAgentControlProtocolVersion = "v1"

const (
	DurableAgentControlMessageEnrollment           = "enrollment"
	DurableAgentControlMessageReattestation        = "re_attestation"
	DurableAgentControlMessageReviewArtifactUpload = "review_artifact_upload"
	DurableAgentControlMessageChildStateUpdate     = "child_state_update"
	DurableAgentControlMessagePolicyPoll           = "policy_poll"
	DurableAgentControlMessagePolicyUpdate         = "policy_update"
	DurableAgentControlMessagePolicyAck            = "policy_ack"
)

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

func NormalizeDurableAgentChannelConfig(cfg DurableAgentChannelConfig) DurableAgentChannelConfig {
	if cfg.External != nil {
		normalized := NormalizeDurableAgentExternalChannelConfig(*cfg.External)
		cfg.External = &normalized
	}
	if cfg.Email != nil {
		normalized := NormalizeDurableAgentExternalChannelConfig(*cfg.Email)
		cfg.Email = &normalized
		if cfg.External == nil {
			legacy := normalized
			cfg.External = &legacy
		}
	}
	if cfg.External != nil && cfg.Email == nil {
		legacy := *cfg.External
		cfg.Email = &legacy
	}
	return cfg
}

func (cfg DurableAgentChannelConfig) MarshalJSON() ([]byte, error) {
	cfg = NormalizeDurableAgentChannelConfig(cfg)
	type channelConfigJSON struct {
		External *DurableAgentExternalChannelConfig `json:"external,omitempty"`
		Email    *DurableAgentEmailChannelConfig    `json:"email,omitempty"`
	}
	out := channelConfigJSON{External: cfg.External}
	if cfg.External == nil && cfg.Email != nil {
		out.Email = cfg.Email
	}
	return json.Marshal(out)
}

func (cfg DurableAgentChannelConfig) ExternalConfig() *DurableAgentExternalChannelConfig {
	cfg = NormalizeDurableAgentChannelConfig(cfg)
	return cfg.External
}

func NormalizeDurableAgentSetupWizardAnswers(answers DurableAgentSetupWizardAnswers) DurableAgentSetupWizardAnswers {
	return normalizeDurableAgentSetupWizardAnswers(answers)
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

func (cfg DurableAgentChannelConfig) IsZero() bool {
	cfg = NormalizeDurableAgentChannelConfig(cfg)
	return cfg.External == nil
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

func NormalizeDurableAgentRemoteEnrollment(enrollment DurableAgentRemoteEnrollment) DurableAgentRemoteEnrollment {
	enrollment.AgentID = strings.TrimSpace(enrollment.AgentID)
	enrollment.ParentControlURL = strings.TrimSpace(enrollment.ParentControlURL)
	enrollment.KeyFingerprint = strings.TrimSpace(enrollment.KeyFingerprint)
	enrollment.ProtocolVersion = normalizeDurableAgentControlProtocolVersion(enrollment.ProtocolVersion)
	enrollment.Status = normalizeDurableAgentRemoteEnrollmentStatus(enrollment.Status)
	if enrollment.LastSequence < 0 {
		enrollment.LastSequence = 0
	}
	return enrollment
}

func NormalizeDurableAgentControlEnvelope(envelope DurableAgentControlEnvelope) DurableAgentControlEnvelope {
	envelope.ProtocolVersion = normalizeDurableAgentControlProtocolVersion(envelope.ProtocolVersion)
	envelope.AgentID = strings.TrimSpace(envelope.AgentID)
	envelope.ParentAgentID = strings.TrimSpace(envelope.ParentAgentID)
	envelope.MessageKind = normalizeDurableAgentControlMessageKind(envelope.MessageKind)
	envelope.MessageID = strings.TrimSpace(envelope.MessageID)
	envelope.Signature = strings.TrimSpace(envelope.Signature)
	if envelope.Sequence < 0 {
		envelope.Sequence = 0
	}
	return envelope
}

func NormalizeDurableAgentPolicyAcknowledgement(ack DurableAgentPolicyAcknowledgement) DurableAgentPolicyAcknowledgement {
	ack.AgentID = strings.TrimSpace(ack.AgentID)
	ack.AcknowledgedHash = strings.TrimSpace(ack.AcknowledgedHash)
	ack.AppliedHash = strings.TrimSpace(ack.AppliedHash)
	ack.Status = normalizeDurableAgentPolicyApplyStatus(ack.Status)
	ack.Error = strings.TrimSpace(ack.Error)
	return ack
}

func NormalizeDurableAgentRemoteBootstrap(bootstrap DurableAgentRemoteBootstrap) DurableAgentRemoteBootstrap {
	bootstrap.AgentID = strings.TrimSpace(bootstrap.AgentID)
	bootstrap.ParentAgentID = strings.TrimSpace(bootstrap.ParentAgentID)
	bootstrap.ChannelKind = strings.TrimSpace(bootstrap.ChannelKind)
	bootstrap.ParentControlURL = strings.TrimSpace(bootstrap.ParentControlURL)
	bootstrap.EnrollmentToken = strings.TrimSpace(bootstrap.EnrollmentToken)
	bootstrap.KeyFingerprint = strings.TrimSpace(bootstrap.KeyFingerprint)
	bootstrap.ProtocolVersion = normalizeDurableAgentControlProtocolVersion(bootstrap.ProtocolVersion)
	bootstrap.BootstrapLLM = NormalizeNodeLLMBootstrap(bootstrap.BootstrapLLM)
	bootstrap.BootstrapCeiling = NormalizeDurableAgentBootstrapCeiling(bootstrap.BootstrapCeiling)
	bootstrap.LocalStorageRoots = normalizeDurableAgentStringSet(bootstrap.LocalStorageRoots)
	bootstrap.SecretScopes = normalizeDurableAgentStringSet(bootstrap.SecretScopes)
	bootstrap.NetworkPolicy = strings.TrimSpace(bootstrap.NetworkPolicy)
	return bootstrap
}

func ValidateDurableAgentRemoteBootstrap(bootstrap DurableAgentRemoteBootstrap) error {
	bootstrap = NormalizeDurableAgentRemoteBootstrap(bootstrap)
	switch {
	case bootstrap.AgentID == "":
		return fmt.Errorf("durable agent remote bootstrap agent_id is required")
	case bootstrap.ParentControlURL == "":
		return fmt.Errorf("durable agent remote bootstrap parent_control_url is required")
	case bootstrap.EnrollmentToken == "":
		return fmt.Errorf("durable agent remote bootstrap enrollment_token is required")
	case bootstrap.KeyFingerprint == "":
		return fmt.Errorf("durable agent remote bootstrap key_fingerprint is required")
	default:
		return ValidateNodeLLMBootstrap(bootstrap.BootstrapLLM)
	}
}

func (b DurableAgentRemoteBootstrap) EnrollmentPayload() DurableAgentEnrollmentPayload {
	b = NormalizeDurableAgentRemoteBootstrap(b)
	return DurableAgentEnrollmentPayload{
		ReviewTargetChatID: b.ReviewTargetChatID,
		AgentID:            b.AgentID,
		ParentAgentID:      b.ParentAgentID,
		ChannelKind:        b.ChannelKind,
		ParentControlURL:   b.ParentControlURL,
		EnrollmentToken:    b.EnrollmentToken,
		KeyFingerprint:     b.KeyFingerprint,
		ProtocolVersion:    b.ProtocolVersion,
		BootstrapLLM:       b.BootstrapLLM,
		BootstrapCeiling:   b.BootstrapCeiling,
		LocalStorageRoots:  append([]string(nil), b.LocalStorageRoots...),
		SecretScopes:       append([]string(nil), b.SecretScopes...),
		NetworkPolicy:      b.NetworkPolicy,
	}
}

func NormalizeDurableAgentEnrollmentPayload(payload DurableAgentEnrollmentPayload) DurableAgentEnrollmentPayload {
	payload.AgentID = strings.TrimSpace(payload.AgentID)
	payload.ParentAgentID = strings.TrimSpace(payload.ParentAgentID)
	payload.ChannelKind = strings.TrimSpace(payload.ChannelKind)
	payload.ParentControlURL = strings.TrimSpace(payload.ParentControlURL)
	payload.EnrollmentToken = strings.TrimSpace(payload.EnrollmentToken)
	payload.KeyFingerprint = strings.TrimSpace(payload.KeyFingerprint)
	payload.ProtocolVersion = normalizeDurableAgentControlProtocolVersion(payload.ProtocolVersion)
	payload.BootstrapLLM = NormalizeNodeLLMBootstrap(payload.BootstrapLLM)
	payload.BootstrapCeiling = NormalizeDurableAgentBootstrapCeiling(payload.BootstrapCeiling)
	payload.LocalStorageRoots = normalizeDurableAgentStringSet(payload.LocalStorageRoots)
	payload.SecretScopes = normalizeDurableAgentStringSet(payload.SecretScopes)
	payload.NetworkPolicy = strings.TrimSpace(payload.NetworkPolicy)
	return payload
}

func ValidateDurableAgentEnrollmentPayload(payload DurableAgentEnrollmentPayload) error {
	payload = NormalizeDurableAgentEnrollmentPayload(payload)
	switch {
	case payload.AgentID == "":
		return fmt.Errorf("durable agent enrollment payload agent_id is required")
	case payload.ParentControlURL == "":
		return fmt.Errorf("durable agent enrollment payload parent_control_url is required")
	case payload.EnrollmentToken == "":
		return fmt.Errorf("durable agent enrollment payload enrollment_token is required")
	case payload.KeyFingerprint == "":
		return fmt.Errorf("durable agent enrollment payload key_fingerprint is required")
	default:
		return ValidateNodeLLMBootstrap(payload.BootstrapLLM)
	}
}

func ValidateDurableAgentControlEnvelope(envelope DurableAgentControlEnvelope) error {
	envelope = NormalizeDurableAgentControlEnvelope(envelope)
	switch {
	case envelope.ProtocolVersion == "":
		return fmt.Errorf("durable agent control envelope protocol_version is required")
	case envelope.AgentID == "":
		return fmt.Errorf("durable agent control envelope agent_id is required")
	case envelope.MessageKind == "":
		return fmt.Errorf("durable agent control envelope message_kind is required")
	case envelope.MessageID == "":
		return fmt.Errorf("durable agent control envelope message_id is required")
	case envelope.Sequence <= 0:
		return fmt.Errorf("durable agent control envelope sequence must be > 0")
	case envelope.Timestamp.IsZero():
		return fmt.Errorf("durable agent control envelope timestamp is required")
	case envelope.Signature == "":
		return fmt.Errorf("durable agent control envelope signature is required")
	default:
		return nil
	}
}

func ParseDurableAgentContinuityState(raw string) (DurableAgentContinuityState, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return DurableAgentContinuityState{}, nil
	}
	var state DurableAgentContinuityState
	if err := json.Unmarshal([]byte(raw), &state); err != nil {
		return DurableAgentContinuityState{}, err
	}
	return NormalizeDurableAgentContinuityState(state), nil
}

func NormalizeDurableAgentContinuityState(state DurableAgentContinuityState) DurableAgentContinuityState {
	state.RecentInteractions = normalizeDurableAgentRecentInteractions(state.RecentInteractions)
	state.PendingQuestions = normalizeDurableAgentPendingQuestions(state.PendingQuestions)
	state.ReviewRefs = normalizeDurableAgentReviewReferences(state.ReviewRefs)
	state.RatifiedOutcomes = normalizeDurableAgentRatifiedOutcomes(state.RatifiedOutcomes)
	state.Conversation = normalizeDurableAgentConversationState(state.Conversation)
	state.SetupWizard = normalizeDurableAgentSetupWizardState(state.SetupWizard)
	state.EmailPending = normalizeDurableAgentEmailPendingState(state.EmailPending)
	return state
}

func (s DurableAgentContinuityState) Marshal() (string, error) {
	s = NormalizeDurableAgentContinuityState(s)
	if s.IsZero() {
		return "", nil
	}
	raw, err := json.Marshal(s)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func (s DurableAgentContinuityState) IsZero() bool {
	return len(s.RecentInteractions) == 0 &&
		len(s.PendingQuestions) == 0 &&
		len(s.ReviewRefs) == 0 &&
		len(s.RatifiedOutcomes) == 0 &&
		s.Conversation == nil &&
		s.SetupWizard == nil &&
		s.EmailPending == nil
}

func (s DurableAgentContinuityState) WithReviewArtifact(reviewEventID int64, artifact DurableReviewArtifact, at time.Time) DurableAgentContinuityState {
	s = NormalizeDurableAgentContinuityState(s)
	at = at.UTC()
	summary := normalizeDurableAgentText(artifact.Summary)
	source := normalizeDurableAgentText(artifact.Metadata["sender_name"])
	triggerKinds := normalizeDurableAgentCSV(artifact.Metadata["trigger_kinds"])
	if summary != "" {
		s.RecentInteractions = prependDurableAgentRecentInteraction(s.RecentInteractions, DurableAgentRecentInteraction{
			Summary:       summary,
			Source:        source,
			TriggerKinds:  triggerKinds,
			ReviewEventID: reviewEventID,
			OccurredAt:    at,
		})
	}
	for _, question := range artifact.Questions {
		question = normalizeDurableAgentText(question)
		if question == "" {
			continue
		}
		s.PendingQuestions = upsertDurableAgentPendingQuestion(s.PendingQuestions, DurableAgentPendingQuestion{
			Question:      question,
			ReviewEventID: reviewEventID,
			Status:        "pending_review",
			CreatedAt:     at,
			UpdatedAt:     at,
		})
	}
	if reviewEventID > 0 {
		s.ReviewRefs = prependDurableAgentReviewReference(s.ReviewRefs, DurableAgentReviewReference{
			ReviewEventID: reviewEventID,
			Summary:       summary,
			RiskFlags:     normalizeDurableAgentStringSet(artifact.RiskFlags),
			Status:        "pending",
			CreatedAt:     at,
		})
	}
	return NormalizeDurableAgentContinuityState(s)
}

func (s DurableAgentContinuityState) WithRatifiedOutcome(summary string, policyVersion int64, policyHash string, sourceReviewEventID int64, appliedAt time.Time) DurableAgentContinuityState {
	s = NormalizeDurableAgentContinuityState(s)
	if policyVersion <= 0 || strings.TrimSpace(policyHash) == "" {
		return s
	}
	s.RatifiedOutcomes = prependDurableAgentRatifiedOutcome(s.RatifiedOutcomes, DurableAgentRatifiedOutcome{
		Summary:             normalizeDurableAgentText(summary),
		PolicyVersion:       policyVersion,
		PolicyHash:          strings.TrimSpace(policyHash),
		SourceReviewEventID: sourceReviewEventID,
		AppliedAt:           appliedAt.UTC(),
	})
	return NormalizeDurableAgentContinuityState(s)
}

func (s DurableAgentContinuityState) WithConversationMessage(role string, text string, createdAt time.Time) DurableAgentContinuityState {
	s = NormalizeDurableAgentContinuityState(s)
	role = normalizeDurableAgentConversationRole(role)
	text = clampDurableAgentField(text, 1200)
	if role == "" || text == "" {
		return s
	}
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	next := DurableAgentConversationMessage{
		Role:      role,
		Text:      text,
		CreatedAt: createdAt.UTC(),
	}
	if s.Conversation == nil {
		s.Conversation = &DurableAgentConversationState{}
	}
	s.Conversation.Messages = append([]DurableAgentConversationMessage{next}, s.Conversation.Messages...)
	return NormalizeDurableAgentContinuityState(s)
}

func (s DurableAgentContinuityState) PendingParentConversationMessages(limit int) []DurableAgentConversationMessage {
	s = NormalizeDurableAgentContinuityState(s)
	if s.Conversation == nil || len(s.Conversation.Messages) == 0 {
		return nil
	}
	if limit <= 0 {
		limit = durableAgentConversationMaxItems
	}
	out := make([]DurableAgentConversationMessage, 0, limit)
	for _, message := range s.Conversation.Messages {
		if message.Role != "parent" {
			continue
		}
		if !message.AcknowledgedAt.IsZero() {
			continue
		}
		out = append(out, message)
		if len(out) == limit {
			break
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func (s DurableAgentContinuityState) AcknowledgeParentConversationMessages(at time.Time) DurableAgentContinuityState {
	s = NormalizeDurableAgentContinuityState(s)
	if s.Conversation == nil || len(s.Conversation.Messages) == 0 {
		return s
	}
	if at.IsZero() {
		at = time.Now().UTC()
	}
	updated := false
	for i := range s.Conversation.Messages {
		message := &s.Conversation.Messages[i]
		if message.Role != "parent" || !message.AcknowledgedAt.IsZero() {
			continue
		}
		message.AcknowledgedAt = at.UTC()
		updated = true
	}
	if !updated {
		return s
	}
	return NormalizeDurableAgentContinuityState(s)
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

func normalizeDurableAgentControlProtocolVersion(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", DefaultDurableAgentControlProtocolVersion:
		return DefaultDurableAgentControlProtocolVersion
	default:
		return ""
	}
}

func normalizeDurableAgentControlMessageKind(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case DurableAgentControlMessageEnrollment,
		DurableAgentControlMessageReattestation,
		DurableAgentControlMessageReviewArtifactUpload,
		DurableAgentControlMessageChildStateUpdate,
		DurableAgentControlMessagePolicyPoll,
		DurableAgentControlMessagePolicyUpdate,
		DurableAgentControlMessagePolicyAck:
		return strings.ToLower(strings.TrimSpace(value))
	default:
		return ""
	}
}

func normalizeDurableAgentRemoteEnrollmentStatus(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "revoked", "decommissioned":
		return strings.ToLower(strings.TrimSpace(value))
	default:
		return "active"
	}
}

func normalizeDurableAgentPolicyApplyStatus(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "pending", "applied", "failed":
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

func normalizeDurableAgentRecentInteractions(values []DurableAgentRecentInteraction) []DurableAgentRecentInteraction {
	if len(values) == 0 {
		return nil
	}
	out := make([]DurableAgentRecentInteraction, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value.Summary = normalizeDurableAgentText(value.Summary)
		value.Source = normalizeDurableAgentText(value.Source)
		value.TriggerKinds = normalizeDurableAgentStringSet(value.TriggerKinds)
		if value.Summary == "" {
			continue
		}
		key := durableAgentInteractionKey(value.ReviewEventID, value.Summary)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, value)
		if len(out) == durableAgentContinuityMaxItems {
			break
		}
	}
	return out
}

func normalizeDurableAgentPendingQuestions(values []DurableAgentPendingQuestion) []DurableAgentPendingQuestion {
	if len(values) == 0 {
		return nil
	}
	out := make([]DurableAgentPendingQuestion, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value.Question = normalizeDurableAgentText(value.Question)
		value.Status = normalizeDurableAgentPendingQuestionStatus(value.Status)
		if value.Question == "" {
			continue
		}
		key := durableAgentPendingQuestionKey(value.ReviewEventID, value.Question)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, value)
		if len(out) == durableAgentContinuityMaxItems {
			break
		}
	}
	return out
}

func normalizeDurableAgentReviewReferences(values []DurableAgentReviewReference) []DurableAgentReviewReference {
	if len(values) == 0 {
		return nil
	}
	out := make([]DurableAgentReviewReference, 0, len(values))
	seen := make(map[int64]struct{}, len(values))
	for _, value := range values {
		value.Summary = normalizeDurableAgentText(value.Summary)
		value.RiskFlags = normalizeDurableAgentStringSet(value.RiskFlags)
		value.Status = normalizeDurableAgentReviewStatus(value.Status)
		if value.ReviewEventID <= 0 {
			continue
		}
		if _, ok := seen[value.ReviewEventID]; ok {
			continue
		}
		seen[value.ReviewEventID] = struct{}{}
		out = append(out, value)
		if len(out) == durableAgentContinuityMaxItems {
			break
		}
	}
	return out
}

func normalizeDurableAgentRatifiedOutcomes(values []DurableAgentRatifiedOutcome) []DurableAgentRatifiedOutcome {
	if len(values) == 0 {
		return nil
	}
	out := make([]DurableAgentRatifiedOutcome, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value.Summary = normalizeDurableAgentText(value.Summary)
		value.PolicyHash = strings.TrimSpace(value.PolicyHash)
		if value.PolicyVersion <= 0 || value.PolicyHash == "" {
			continue
		}
		key := fmt.Sprintf("%d:%s", value.PolicyVersion, value.PolicyHash)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, value)
		if len(out) == durableAgentContinuityMaxItems {
			break
		}
	}
	return out
}

func normalizeDurableAgentConversationState(state *DurableAgentConversationState) *DurableAgentConversationState {
	if state == nil {
		return nil
	}
	normalized := DurableAgentConversationState{
		Messages: normalizeDurableAgentConversationMessages(state.Messages),
	}
	if len(normalized.Messages) == 0 {
		return nil
	}
	return &normalized
}

func normalizeDurableAgentConversationMessages(values []DurableAgentConversationMessage) []DurableAgentConversationMessage {
	if len(values) == 0 {
		return nil
	}
	out := make([]DurableAgentConversationMessage, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value.Role = normalizeDurableAgentConversationRole(value.Role)
		value.Text = clampDurableAgentField(value.Text, 1200)
		value.CreatedAt = value.CreatedAt.UTC()
		value.AcknowledgedAt = value.AcknowledgedAt.UTC()
		if value.Role == "" || value.Text == "" {
			continue
		}
		if value.Role != "parent" {
			value.AcknowledgedAt = time.Time{}
		}
		key := fmt.Sprintf("%s:%s:%s", value.Role, value.Text, value.CreatedAt.UTC().Format(time.RFC3339Nano))
		if value.CreatedAt.IsZero() {
			key = fmt.Sprintf("%s:%s", value.Role, value.Text)
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, value)
		if len(out) == durableAgentConversationMaxItems {
			break
		}
	}
	return out
}

func normalizeDurableAgentSetupWizardState(state *DurableAgentSetupWizardState) *DurableAgentSetupWizardState {
	if state == nil {
		return nil
	}
	normalized := *state
	if normalized.SchemaVersion <= 0 {
		normalized.SchemaVersion = 1
	}
	normalized.ChannelKind = strings.TrimSpace(normalized.ChannelKind)
	normalized.Status = normalizeDurableAgentSetupWizardStatus(normalized.Status)
	normalized.CurrentStep = strings.TrimSpace(normalized.CurrentStep)
	normalized.Answers = normalizeDurableAgentSetupWizardAnswers(normalized.Answers)
	normalized.Missing = normalizeDurableAgentStringSet(normalized.Missing)
	if normalized.Status == "" && normalized.ChannelKind == "" && normalized.CurrentStep == "" &&
		len(normalized.Missing) == 0 && durableAgentSetupWizardAnswersZero(normalized.Answers) &&
		normalized.StartedAt.IsZero() && normalized.UpdatedAt.IsZero() {
		return nil
	}
	return &normalized
}

func normalizeDurableAgentSetupWizardStatus(value string) string {
	switch strings.TrimSpace(value) {
	case "in_progress", "ready", "finalized", "cancelled":
		return strings.TrimSpace(value)
	default:
		return ""
	}
}

func normalizeDurableAgentSetupWizardAnswers(answers DurableAgentSetupWizardAnswers) DurableAgentSetupWizardAnswers {
	answers.Address = strings.TrimSpace(answers.Address)
	answers.Account = strings.TrimSpace(answers.Account)
	answers.Adapter = normalizeDurableAgentChannelAdapter(answers.Adapter)
	answers.Query = strings.TrimSpace(answers.Query)
	answers.BootstrapProfile = normalizeDurableAgentBootstrapProfile(answers.BootstrapProfile)
	answers.BootstrapModel = strings.TrimSpace(answers.BootstrapModel)
	answers.Charter = strings.TrimSpace(answers.Charter)
	answers.Autonomy = strings.TrimSpace(answers.Autonomy)
	answers.WakeupMode = strings.TrimSpace(answers.WakeupMode)
	answers.PollInterval = strings.TrimSpace(answers.PollInterval)
	answers.SurfaceRules = normalizeDurableAgentStringSet(answers.SurfaceRules)
	answers.SynthesisCadence = strings.TrimSpace(answers.SynthesisCadence)
	answers.Capabilities = normalizeDurableAgentStringSet(answers.Capabilities)
	answers.NeverRetain = normalizeDurableAgentStringSet(answers.NeverRetain)
	answers.DriftPolicy = strings.TrimSpace(answers.DriftPolicy)
	return answers
}

func normalizeDurableAgentConversationRole(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "parent":
		return "parent"
	case "child":
		return "child"
	case "system":
		return "system"
	default:
		return ""
	}
}

func durableAgentSetupWizardAnswersZero(answers DurableAgentSetupWizardAnswers) bool {
	return answers.Address == "" &&
		answers.Account == "" &&
		answers.Adapter == "" &&
		answers.Query == "" &&
		answers.BootstrapProfile == "" &&
		answers.BootstrapModel == "" &&
		answers.Charter == "" &&
		answers.Autonomy == "" &&
		answers.WakeupMode == "" &&
		answers.PollInterval == "" &&
		len(answers.SurfaceRules) == 0 &&
		answers.SummarizePDFs == nil &&
		answers.SynthesisCadence == "" &&
		len(answers.Capabilities) == 0 &&
		len(answers.NeverRetain) == 0 &&
		answers.DriftPolicy == ""
}

func normalizeDurableAgentBootstrapProfile(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "inherit_parent":
		return "inherit_parent"
	case "child_custom":
		return "child_custom"
	default:
		return ""
	}
}

func normalizeDurableAgentEmailPendingState(state *DurableAgentEmailPendingState) *DurableAgentEmailPendingState {
	if state == nil {
		return nil
	}
	normalized := DurableAgentEmailPendingState{
		Threads: normalizeDurableAgentEmailPendingThreads(state.Threads),
	}
	if len(normalized.Threads) == 0 {
		return nil
	}
	return &normalized
}

func normalizeDurableAgentEmailPendingThreads(values []DurableAgentEmailPendingThread) []DurableAgentEmailPendingThread {
	if len(values) == 0 {
		return nil
	}
	out := make([]DurableAgentEmailPendingThread, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value.ThreadID = strings.TrimSpace(value.ThreadID)
		value.MessageID = strings.TrimSpace(value.MessageID)
		value.From = clampDurableAgentField(value.From, 512)
		value.Subject = clampDurableAgentField(value.Subject, 512)
		value.Snippet = clampDurableAgentField(value.Snippet, 1200)
		value.Body = clampDurableAgentField(value.Body, 2400)
		value.Labels = normalizeDurableAgentStringSet(value.Labels)
		value.AttachmentNames = normalizeDurableAgentStringSet(value.AttachmentNames)
		if value.ThreadID == "" {
			continue
		}
		key := firstNonEmptyDurableAgent(value.ThreadID, value.MessageID)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, value)
		if len(out) == durableAgentEmailPendingMaxItems {
			break
		}
	}
	return out
}

func prependDurableAgentRecentInteraction(values []DurableAgentRecentInteraction, next DurableAgentRecentInteraction) []DurableAgentRecentInteraction {
	return append([]DurableAgentRecentInteraction{next}, values...)
}

func prependDurableAgentReviewReference(values []DurableAgentReviewReference, next DurableAgentReviewReference) []DurableAgentReviewReference {
	return append([]DurableAgentReviewReference{next}, values...)
}

func prependDurableAgentRatifiedOutcome(values []DurableAgentRatifiedOutcome, next DurableAgentRatifiedOutcome) []DurableAgentRatifiedOutcome {
	return append([]DurableAgentRatifiedOutcome{next}, values...)
}

func upsertDurableAgentPendingQuestion(values []DurableAgentPendingQuestion, next DurableAgentPendingQuestion) []DurableAgentPendingQuestion {
	key := durableAgentPendingQuestionKey(next.ReviewEventID, next.Question)
	out := make([]DurableAgentPendingQuestion, 0, len(values)+1)
	out = append(out, next)
	for _, existing := range values {
		if durableAgentPendingQuestionKey(existing.ReviewEventID, existing.Question) == key {
			continue
		}
		out = append(out, existing)
	}
	return out
}

func normalizeDurableAgentText(value string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
}

func clampDurableAgentField(value string, max int) string {
	normalized := normalizeDurableAgentText(value)
	if max <= 0 {
		return ""
	}
	runes := []rune(normalized)
	if len(runes) <= max {
		return normalized
	}
	return string(runes[:max])
}

func firstNonEmptyDurableAgent(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func normalizeDurableAgentCSV(value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return normalizeDurableAgentStringSet(strings.Split(value, ","))
}

func normalizeDurableAgentPendingQuestionStatus(value string) string {
	switch strings.TrimSpace(value) {
	case "answered", "dismissed", "ratified":
		return strings.TrimSpace(value)
	default:
		return "pending_review"
	}
}

func normalizeDurableAgentReviewStatus(value string) string {
	switch strings.TrimSpace(value) {
	case "delivered", "dismissed":
		return strings.TrimSpace(value)
	default:
		return "pending"
	}
}

func durableAgentInteractionKey(reviewEventID int64, summary string) string {
	if reviewEventID > 0 {
		return fmt.Sprintf("review:%d", reviewEventID)
	}
	return normalizeDurableAgentText(summary)
}

func durableAgentPendingQuestionKey(reviewEventID int64, question string) string {
	if reviewEventID > 0 {
		return fmt.Sprintf("%d:%s", reviewEventID, normalizeDurableAgentText(question))
	}
	return normalizeDurableAgentText(question)
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
