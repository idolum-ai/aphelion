//go:build linux

package core

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type DurableAgentRemoteEnrollment struct {
	AgentID          string
	ParentControlURL string
	ProtocolVersion  string
	Status           string
	LastSequence     int64
	EnrolledAt       time.Time
	LastSeenAt       time.Time
	RevokedAt        time.Time
	TailnetIdentity  TailnetPeerIdentity
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
	AgentID        string
	MessageID      string
	MessageKind    string
	Sequence       int64
	Signature      string
	ReceivedAt     time.Time
	ResponseStatus int
	ResponseJSON   string
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

type DurableAgentParentConversationPollRequest struct {
	Envelope DurableAgentControlEnvelope `json:"envelope"`
	Limit    int                         `json:"limit,omitempty"`
}

type DurableAgentParentConversationPollResponse struct {
	Messages []DurableAgentConversationMessage `json:"messages,omitempty"`
}

type DurableAgentParentConversationAcknowledgement struct {
	AgentID        string    `json:"agent_id,omitempty"`
	MessageIDs     []string  `json:"message_ids,omitempty"`
	AcknowledgedAt time.Time `json:"acknowledged_at,omitempty"`
}

type DurableAgentParentConversationAckRequest struct {
	Envelope DurableAgentControlEnvelope                   `json:"envelope"`
	Ack      DurableAgentParentConversationAcknowledgement `json:"ack"`
}

type DurableAgentParentConversationAckResponse struct {
	Accepted bool `json:"accepted"`
}

type DurableAgentRemoteBootstrap struct {
	ReviewTargetChatID int64
	AgentID            string
	ParentAgentID      string
	ChannelKind        string
	ParentControlURL   string
	EnrollmentToken    string
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

const DefaultDurableAgentControlProtocolVersion = "v1"

const (
	DurableAgentControlMessageEnrollment             = "enrollment"
	DurableAgentControlMessageReattestation          = "re_attestation"
	DurableAgentControlMessageReviewArtifactUpload   = "review_artifact_upload"
	DurableAgentControlMessageChildStateUpdate       = "child_state_update"
	DurableAgentControlMessagePolicyPoll             = "policy_poll"
	DurableAgentControlMessagePolicyUpdate           = "policy_update"
	DurableAgentControlMessagePolicyAck              = "policy_ack"
	DurableAgentControlMessageParentConversationPoll = "parent_conversation_poll"
	DurableAgentControlMessageParentConversationAck  = "parent_conversation_ack"
)

func NormalizeDurableAgentRemoteEnrollment(enrollment DurableAgentRemoteEnrollment) DurableAgentRemoteEnrollment {
	enrollment.AgentID = strings.TrimSpace(enrollment.AgentID)
	enrollment.ParentControlURL = strings.TrimSpace(enrollment.ParentControlURL)
	enrollment.ProtocolVersion = normalizeDurableAgentControlProtocolVersion(enrollment.ProtocolVersion)
	enrollment.Status = normalizeDurableAgentRemoteEnrollmentStatus(enrollment.Status)
	enrollment.TailnetIdentity = NormalizeTailnetPeerIdentity(enrollment.TailnetIdentity)
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

func NormalizeDurableAgentParentConversationAcknowledgement(ack DurableAgentParentConversationAcknowledgement) DurableAgentParentConversationAcknowledgement {
	ack.AgentID = strings.TrimSpace(ack.AgentID)
	ack.MessageIDs = normalizeDurableAgentStringSet(ack.MessageIDs)
	if ack.AcknowledgedAt.IsZero() {
		ack.AcknowledgedAt = time.Now().UTC()
	} else {
		ack.AcknowledgedAt = ack.AcknowledgedAt.UTC()
	}
	return ack
}

func NormalizeDurableAgentRemoteBootstrap(bootstrap DurableAgentRemoteBootstrap) DurableAgentRemoteBootstrap {
	bootstrap.AgentID = strings.TrimSpace(bootstrap.AgentID)
	bootstrap.ParentAgentID = strings.TrimSpace(bootstrap.ParentAgentID)
	bootstrap.ChannelKind = strings.TrimSpace(bootstrap.ChannelKind)
	bootstrap.ParentControlURL = strings.TrimSpace(bootstrap.ParentControlURL)
	bootstrap.EnrollmentToken = strings.TrimSpace(bootstrap.EnrollmentToken)
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
	if bootstrap.AgentID != "" {
		if err := ValidateDurableAgentID(bootstrap.AgentID); err != nil {
			return err
		}
	}
	switch {
	case bootstrap.AgentID == "":
		return fmt.Errorf("durable agent remote bootstrap agent_id is required")
	case bootstrap.ParentControlURL == "":
		return fmt.Errorf("durable agent remote bootstrap parent_control_url is required")
	case bootstrap.EnrollmentToken == "":
		return fmt.Errorf("durable agent remote bootstrap enrollment_token is required")
	default:
		if err := ValidateDurableAgentParentControlURL(bootstrap.ParentControlURL); err != nil {
			return err
		}
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
	default:
		if err := ValidateDurableAgentParentControlURL(payload.ParentControlURL); err != nil {
			return err
		}
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
		DurableAgentControlMessagePolicyAck,
		DurableAgentControlMessageParentConversationPoll,
		DurableAgentControlMessageParentConversationAck:
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
