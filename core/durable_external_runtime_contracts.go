//go:build linux

package core

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

const (
	ExternalRuntimeModeOneshot         = "oneshot"
	ExternalRuntimeModeGatewayPresence = "gateway_presence"
	ExternalRuntimeModeRemoteService   = "remote_service"

	GatewayPresenceStatusActive  = "active"
	GatewayPresenceStatusRevoked = "revoked"

	GatewayDialogueModeDirectChildPersona       = "direct_child_persona"
	GatewaySameConversationReplyAllowAdmitted   = "allow_admitted_sender"
	GatewayEffectBoundaryAphelionBrokered       = "aphelion_brokered"
	ExternalRuntimeEffectSourceGatewayDialogue  = "gateway_dialogue"
	ExternalRuntimeContractKindExternalEffect   = "external_effect"
	ExternalRuntimeLeaseKindToolInvocation      = "tool_invocation"
	ExternalRuntimeLeaseKindRuntimeTask         = "runtime_task"
	ExternalRuntimeTaskPacketSchemaV1           = "aphelion.child_task_packet.v1"
	ExternalRuntimeTaskResultSchemaV1           = "aphelion.child_task_result.v1"
	ExternalRuntimeMemoryAdmissionStatusPending = "pending"

	ChildRuntimeAdapterOperationPreflight        = "preflight"
	ChildRuntimeAdapterOperationInstallStatus    = "install_status"
	ChildRuntimeAdapterOperationStart            = "start"
	ChildRuntimeAdapterOperationStop             = "stop"
	ChildRuntimeAdapterOperationStatus           = "status"
	ChildRuntimeAdapterOperationWake             = "wake"
	ChildRuntimeAdapterOperationCollectArtifacts = "collect_artifacts"
)

// DurableExternalRuntimeSpec describes a proposed child-local external runtime.
// It is configuration material, not authority to run the runtime by itself.
type DurableExternalRuntimeSpec struct {
	Kind           string            `json:"kind,omitempty"`
	Mode           string            `json:"mode,omitempty"`
	Source         RuntimeSourceRef  `json:"source,omitempty"`
	InstallRoot    string            `json:"install_root,omitempty"`
	StateRoot      string            `json:"state_root,omitempty"`
	WorkspaceRoot  string            `json:"workspace_root,omitempty"`
	Entrypoint     RuntimeEntrypoint `json:"entrypoint,omitempty"`
	Env            map[string]string `json:"env,omitempty"`
	NetworkClasses []string          `json:"network_classes,omitempty"`
}

type RuntimeSourceRef struct {
	Kind      string            `json:"kind,omitempty"`
	Repo      string            `json:"repo,omitempty"`
	Ref       string            `json:"ref,omitempty"`
	Digest    string            `json:"digest,omitempty"`
	Integrity map[string]string `json:"integrity,omitempty"`
}

type RuntimeEntrypoint struct {
	Kind    string   `json:"kind,omitempty"`
	Command []string `json:"command,omitempty"`
}

type StandingWorkOrder struct {
	ID                  string                 `json:"id,omitempty"`
	Version             int                    `json:"version,omitempty"`
	AgentID             string                 `json:"agent_id,omitempty"`
	Status              string                 `json:"status,omitempty"`
	Title               string                 `json:"title,omitempty"`
	RuntimeKind         string                 `json:"runtime_kind,omitempty"`
	PolicyCeilingRef    string                 `json:"policy_ceiling_ref,omitempty"`
	Principals          StandingWorkPrincipals `json:"principals,omitempty"`
	Schedule            ScheduleSpec           `json:"schedule,omitempty"`
	ReviewPolicy        ReviewPolicy           `json:"review_policy,omitempty"`
	ConditionalGrantIDs []string               `json:"conditional_grant_ids,omitempty"`
	Revocation          RevocationPolicy       `json:"revocation,omitempty"`
}

type StandingWorkPrincipals struct {
	AuthorityPrincipal      string   `json:"authority_principal,omitempty"`
	ReviewPrincipal         string   `json:"review_principal,omitempty"`
	ResourceOwnerPrincipals []string `json:"resource_owner_principals,omitempty"`
}

type ScheduleSpec struct {
	Kind       string `json:"kind,omitempty"`
	Expression string `json:"expression,omitempty"`
	Timezone   string `json:"timezone,omitempty"`
}

type ReviewPolicy struct {
	DefaultOutbound string `json:"default_outbound,omitempty"`
	SendRequires    string `json:"send_requires,omitempty"`
	TrialMode       string `json:"trial_mode,omitempty"`
}

type RevocationPolicy struct {
	StopFutureLeases   bool `json:"stop_future_leases,omitempty"`
	StopRunningGateway bool `json:"stop_running_gateway,omitempty"`
}

type ConditionalGrant struct {
	ID                       string                     `json:"id,omitempty"`
	StandingWorkOrderID      string                     `json:"standing_work_order_id,omitempty"`
	StandingWorkOrderVersion int                        `json:"standing_work_order_version,omitempty"`
	Capability               string                     `json:"capability,omitempty"`
	Tool                     string                     `json:"tool,omitempty"`
	Actions                  []string                   `json:"actions,omitempty"`
	CredentialScope          string                     `json:"credential_scope,omitempty"`
	Conditions               ConditionalGrantConditions `json:"conditions,omitempty"`
	Constraints              map[string]json.RawMessage `json:"constraints,omitempty"`
	Materializes             GrantMaterialization       `json:"materializes,omitempty"`
	Status                   string                     `json:"status,omitempty"`
}

type ConditionalGrantConditions struct {
	Triggers           []string     `json:"triggers,omitempty"`
	Schedule           ScheduleSpec `json:"schedule,omitempty"`
	MaxMessages        int          `json:"max_messages,omitempty"`
	MaxDurationSeconds int          `json:"max_duration_seconds,omitempty"`
}

type GrantMaterialization struct {
	LeaseKind   string `json:"lease_kind,omitempty"`
	TTLSeconds  int    `json:"ttl_seconds,omitempty"`
	ReviewRoute string `json:"review_route,omitempty"`
	SingleUse   bool   `json:"single_use,omitempty"`
}

type StandingWorkOrderAmendment struct {
	ID                     string                     `json:"id,omitempty"`
	StandingWorkOrderID    string                     `json:"standing_work_order_id,omitempty"`
	FromVersion            int                        `json:"from_version,omitempty"`
	ProposedVersion        int                        `json:"proposed_version,omitempty"`
	ProposedBy             string                     `json:"proposed_by,omitempty"`
	Status                 string                     `json:"status,omitempty"`
	Reason                 string                     `json:"reason,omitempty"`
	ChangeClass            []string                   `json:"change_class,omitempty"`
	Diff                   map[string]json.RawMessage `json:"diff,omitempty"`
	RiskDelta              map[string]json.RawMessage `json:"risk_delta,omitempty"`
	ActivationRequirements map[string]bool            `json:"activation_requirements,omitempty"`
}

type LeaseMaterialization struct {
	ID                       string              `json:"id,omitempty"`
	AgentID                  string              `json:"agent_id,omitempty"`
	StandingWorkOrderID      string              `json:"standing_work_order_id,omitempty"`
	StandingWorkOrderVersion int                 `json:"standing_work_order_version,omitempty"`
	MatchedConditions        MatchedConditions   `json:"matched_conditions,omitempty"`
	RuntimeSpecHash          string              `json:"runtime_spec_hash,omitempty"`
	IssuedLeases             []MaterializedLease `json:"issued_leases,omitempty"`
	CreatedAt                time.Time           `json:"created_at,omitempty"`
}

type MatchedConditions struct {
	Trigger      string    `json:"trigger,omitempty"`
	ScheduleTick time.Time `json:"schedule_tick,omitempty"`
}

type MaterializedLease struct {
	LeaseID                 string    `json:"lease_id,omitempty"`
	ConditionalGrantID      string    `json:"conditional_grant_id,omitempty"`
	ConditionalGrantVersion int       `json:"conditional_grant_version,omitempty"`
	Capability              string    `json:"capability,omitempty"`
	LeaseKind               string    `json:"lease_kind,omitempty"`
	ReviewRoute             string    `json:"review_route,omitempty"`
	SingleUse               bool      `json:"single_use,omitempty"`
	ExpiresAt               time.Time `json:"expires_at,omitempty"`
}

type GatewayPresenceContract struct {
	ContractID                  string   `json:"contract_id,omitempty"`
	ContractVersion             string   `json:"contract_version,omitempty"`
	Status                      string   `json:"status,omitempty"`
	RuntimeKind                 string   `json:"runtime_kind,omitempty"`
	Channel                     string   `json:"channel,omitempty"`
	Account                     string   `json:"account,omitempty"`
	StandingWorkOrderID         string   `json:"standing_work_order_id,omitempty"`
	ConditionalGrantID          string   `json:"conditional_grant_id,omitempty"`
	InboundMode                 string   `json:"inbound_mode,omitempty"`
	DialogueMode                string   `json:"dialogue_mode,omitempty"`
	SameConversationReplyPolicy string   `json:"same_conversation_reply_policy,omitempty"`
	EffectBoundary              string   `json:"effect_boundary,omitempty"`
	OutboundMode                string   `json:"outbound_mode,omitempty"`
	AllowedSenderIDs            []string `json:"allowed_sender_ids,omitempty"`
	PairingPolicy               string   `json:"pairing_policy,omitempty"`
	UnknownSenderBehavior       string   `json:"unknown_sender_behavior,omitempty"`
	MemoryAdmission             string   `json:"memory_admission,omitempty"`
	OutboundDeliveryPolicy      string   `json:"outbound_delivery_policy,omitempty"`
	CredentialScope             string   `json:"credential_scope,omitempty"`
	StateRoot                   string   `json:"state_root,omitempty"`
	ReviewTargetPrincipal       string   `json:"review_target_principal,omitempty"`
	StopOnRevoke                bool     `json:"stop_on_revoke,omitempty"`
}

type GatewayEvent struct {
	EventID            string    `json:"event_id,omitempty"`
	RuntimeKind        string    `json:"runtime_kind,omitempty"`
	Channel            string    `json:"channel,omitempty"`
	Account            string    `json:"account,omitempty"`
	SenderID           string    `json:"sender_id,omitempty"`
	TransportMessageID string    `json:"transport_message_id,omitempty"`
	RawPayloadHash     string    `json:"raw_payload_hash,omitempty"`
	Text               string    `json:"text,omitempty"`
	AdapterTimestamp   time.Time `json:"adapter_timestamp,omitempty"`
}

type DialogueTurn struct {
	TurnID              string    `json:"turn_id,omitempty"`
	EventID             string    `json:"event_id,omitempty"`
	RuntimeKind         string    `json:"runtime_kind,omitempty"`
	Channel             string    `json:"channel,omitempty"`
	Account             string    `json:"account,omitempty"`
	SenderID            string    `json:"sender_id,omitempty"`
	TransportMessageID  string    `json:"transport_message_id,omitempty"`
	Text                string    `json:"text,omitempty"`
	Admitted            bool      `json:"admitted"`
	AdmissionDecision   string    `json:"admission_decision,omitempty"`
	GatewayContractHash string    `json:"gateway_contract_hash,omitempty"`
	MemoryAdmission     string    `json:"memory_admission,omitempty"`
	CreatedAt           time.Time `json:"created_at,omitempty"`
}

type SameConversationReply struct {
	ReplyID             string    `json:"reply_id,omitempty"`
	TurnID              string    `json:"turn_id,omitempty"`
	Channel             string    `json:"channel,omitempty"`
	Account             string    `json:"account,omitempty"`
	SenderID            string    `json:"sender_id,omitempty"`
	Text                string    `json:"text,omitempty"`
	GatewayContractHash string    `json:"gateway_contract_hash,omitempty"`
	CreatedAt           time.Time `json:"created_at,omitempty"`
}

type EffectRequest struct {
	ID             string                     `json:"id,omitempty"`
	AgentID        string                     `json:"agent_id,omitempty"`
	Source         string                     `json:"source,omitempty"`
	DialogueTurnID string                     `json:"dialogue_turn_id,omitempty"`
	RequestedBy    string                     `json:"requested_by,omitempty"`
	Action         string                     `json:"action,omitempty"`
	Provider       string                     `json:"provider,omitempty"`
	Purpose        string                     `json:"purpose,omitempty"`
	Constraints    map[string]json.RawMessage `json:"constraints,omitempty"`
	CreatedAt      time.Time                  `json:"created_at,omitempty"`
}

type EffectRequestInput struct {
	ID          string                     `json:"id,omitempty"`
	AgentID     string                     `json:"agent_id,omitempty"`
	Action      string                     `json:"action,omitempty"`
	Provider    string                     `json:"provider,omitempty"`
	Purpose     string                     `json:"purpose,omitempty"`
	Constraints map[string]json.RawMessage `json:"constraints,omitempty"`
	CreatedAt   time.Time                  `json:"created_at,omitempty"`
}

type DiscoveredEffectContract struct {
	ID                    string                     `json:"id,omitempty"`
	AgentID               string                     `json:"agent_id,omitempty"`
	SourceEffectRequestID string                     `json:"source_effect_request_id,omitempty"`
	ContractKind          string                     `json:"contract_kind,omitempty"`
	Provider              string                     `json:"provider,omitempty"`
	Action                string                     `json:"action,omitempty"`
	ReviewRoute           string                     `json:"review_route,omitempty"`
	Constraints           map[string]json.RawMessage `json:"constraints,omitempty"`
	Materializes          GrantMaterialization       `json:"materializes,omitempty"`
	ExpectedResult        ExpectedEffectResult       `json:"expected_result,omitempty"`
	CreatedAt             time.Time                  `json:"created_at,omitempty"`
}

type DiscoveredEffectContractOptions struct {
	ID             string               `json:"id,omitempty"`
	ReviewRoute    string               `json:"review_route,omitempty"`
	Materializes   GrantMaterialization `json:"materializes,omitempty"`
	ExpectedResult ExpectedEffectResult `json:"expected_result,omitempty"`
	CreatedAt      time.Time            `json:"created_at,omitempty"`
}

type ExpectedEffectResult struct {
	Kind           string `json:"kind,omitempty"`
	ArtifactPolicy string `json:"artifact_policy,omitempty"`
}

type EffectResult struct {
	ResultID         string    `json:"result_id,omitempty"`
	EffectRequestID  string    `json:"effect_request_id,omitempty"`
	EffectContractID string    `json:"effect_contract_id,omitempty"`
	Status           string    `json:"status,omitempty"`
	Summary          string    `json:"summary,omitempty"`
	ArtifactRefs     []string  `json:"artifact_refs,omitempty"`
	BlockerKind      string    `json:"blocker_kind,omitempty"`
	CreatedAt        time.Time `json:"created_at,omitempty"`
}

type ParentMemoryAdmission struct {
	AdmissionID  string    `json:"admission_id,omitempty"`
	AgentID      string    `json:"agent_id,omitempty"`
	SourceKind   string    `json:"source_kind,omitempty"`
	SourceID     string    `json:"source_id,omitempty"`
	Status       string    `json:"status,omitempty"`
	Summary      string    `json:"summary,omitempty"`
	ReviewRoute  string    `json:"review_route,omitempty"`
	ArtifactRefs []string  `json:"artifact_refs,omitempty"`
	RequestedAt  time.Time `json:"requested_at,omitempty"`
	AcceptedAt   time.Time `json:"accepted_at,omitempty"`
}

type ExternalRuntimeTaskPacketPayload struct {
	Schema               string                   `json:"schema,omitempty"`
	AgentID              string                   `json:"agent_id,omitempty"`
	EffectContractID     string                   `json:"effect_contract_id,omitempty"`
	LeaseMaterialization LeaseMaterialization     `json:"lease_materialization,omitempty"`
	Authority            []MaterializedLease      `json:"authority,omitempty"`
	Effect               DiscoveredEffectContract `json:"effect,omitempty"`
}

type ExternalRuntimeTaskResultPayload struct {
	Schema           string       `json:"schema,omitempty"`
	AgentID          string       `json:"agent_id,omitempty"`
	TaskPacketID     string       `json:"task_packet_id,omitempty"`
	EffectContractID string       `json:"effect_contract_id,omitempty"`
	Status           string       `json:"status,omitempty"`
	Summary          string       `json:"summary,omitempty"`
	EffectResult     EffectResult `json:"effect_result,omitempty"`
}

type ChildRuntimeAdapterOperation struct {
	Operation    string    `json:"operation,omitempty"`
	AgentID      string    `json:"agent_id,omitempty"`
	RuntimeKind  string    `json:"runtime_kind,omitempty"`
	RuntimeMode  string    `json:"runtime_mode,omitempty"`
	SpecHash     string    `json:"spec_hash,omitempty"`
	InputRef     string    `json:"input_ref,omitempty"`
	AuthorityRef string    `json:"authority_ref,omitempty"`
	Deadline     time.Time `json:"deadline,omitempty"`
}

func NormalizeDurableExternalRuntimeSpec(spec DurableExternalRuntimeSpec) DurableExternalRuntimeSpec {
	spec.Kind = normalizeExternalRuntimeToken(spec.Kind)
	spec.Mode = normalizeExternalRuntimeToken(spec.Mode)
	spec.Source = NormalizeRuntimeSourceRef(spec.Source)
	spec.InstallRoot = strings.TrimSpace(spec.InstallRoot)
	spec.StateRoot = strings.TrimSpace(spec.StateRoot)
	spec.WorkspaceRoot = strings.TrimSpace(spec.WorkspaceRoot)
	spec.Entrypoint.Kind = normalizeExternalRuntimeToken(spec.Entrypoint.Kind)
	spec.Entrypoint.Command = normalizeUniqueStrings(spec.Entrypoint.Command)
	spec.Env = normalizeExternalRuntimeStringMap(spec.Env)
	spec.NetworkClasses = normalizeExternalRuntimeTokens(spec.NetworkClasses)
	return spec
}

func NormalizeRuntimeSourceRef(ref RuntimeSourceRef) RuntimeSourceRef {
	ref.Kind = normalizeExternalRuntimeToken(ref.Kind)
	ref.Repo = strings.TrimSpace(ref.Repo)
	ref.Ref = strings.TrimSpace(ref.Ref)
	ref.Digest = strings.TrimSpace(ref.Digest)
	ref.Integrity = normalizeExternalRuntimeStringMap(ref.Integrity)
	return ref
}

func ValidateDurableExternalRuntimeSpec(spec DurableExternalRuntimeSpec) error {
	spec = NormalizeDurableExternalRuntimeSpec(spec)
	if spec.Kind == "" {
		return fmt.Errorf("external runtime spec requires kind")
	}
	switch spec.Mode {
	case ExternalRuntimeModeOneshot, ExternalRuntimeModeGatewayPresence, ExternalRuntimeModeRemoteService:
	default:
		return fmt.Errorf("external runtime spec has unsupported mode %q", spec.Mode)
	}
	if spec.Source.Kind == "" {
		return fmt.Errorf("external runtime spec requires source kind")
	}
	switch spec.Source.Kind {
	case "git":
		if spec.Source.Repo == "" || spec.Source.Ref == "" {
			return fmt.Errorf("git runtime source requires repo and ref")
		}
	case "binary", "container_image":
		if spec.Source.Ref == "" && spec.Source.Digest == "" {
			return fmt.Errorf("%s runtime source requires ref or digest", spec.Source.Kind)
		}
	default:
		return fmt.Errorf("external runtime spec has unsupported source kind %q", spec.Source.Kind)
	}
	if spec.StateRoot == "" || !filepath.IsAbs(spec.StateRoot) {
		return fmt.Errorf("external runtime spec requires absolute state_root")
	}
	if spec.InstallRoot != "" && !filepath.IsAbs(spec.InstallRoot) {
		return fmt.Errorf("external runtime install_root must be absolute")
	}
	if spec.WorkspaceRoot != "" && !filepath.IsAbs(spec.WorkspaceRoot) {
		return fmt.Errorf("external runtime workspace_root must be absolute")
	}
	return nil
}

func NormalizeStandingWorkOrder(order StandingWorkOrder) StandingWorkOrder {
	order.ID = strings.TrimSpace(order.ID)
	order.AgentID = strings.TrimSpace(order.AgentID)
	order.Status = normalizeExternalRuntimeToken(order.Status)
	order.Title = strings.TrimSpace(order.Title)
	order.RuntimeKind = normalizeExternalRuntimeToken(order.RuntimeKind)
	order.PolicyCeilingRef = strings.TrimSpace(order.PolicyCeilingRef)
	order.Principals.AuthorityPrincipal = strings.TrimSpace(order.Principals.AuthorityPrincipal)
	order.Principals.ReviewPrincipal = strings.TrimSpace(order.Principals.ReviewPrincipal)
	order.Principals.ResourceOwnerPrincipals = normalizeUniqueStrings(order.Principals.ResourceOwnerPrincipals)
	order.Schedule.Kind = normalizeExternalRuntimeToken(order.Schedule.Kind)
	order.Schedule.Expression = strings.TrimSpace(order.Schedule.Expression)
	order.Schedule.Timezone = strings.TrimSpace(order.Schedule.Timezone)
	order.ReviewPolicy.DefaultOutbound = normalizeExternalRuntimeToken(order.ReviewPolicy.DefaultOutbound)
	order.ReviewPolicy.SendRequires = normalizeExternalRuntimeToken(order.ReviewPolicy.SendRequires)
	order.ReviewPolicy.TrialMode = normalizeExternalRuntimeToken(order.ReviewPolicy.TrialMode)
	order.ConditionalGrantIDs = normalizeUniqueStrings(order.ConditionalGrantIDs)
	return order
}

func ValidateStandingWorkOrder(order StandingWorkOrder) error {
	order = NormalizeStandingWorkOrder(order)
	if order.ID == "" || order.AgentID == "" {
		return fmt.Errorf("standing work order requires id and agent_id")
	}
	if order.Version <= 0 {
		return fmt.Errorf("standing work order requires positive version")
	}
	if order.Status != "" && order.Status != "active" {
		return fmt.Errorf("standing work order status %q cannot materialize leases", order.Status)
	}
	if order.Principals.AuthorityPrincipal == "" {
		return fmt.Errorf("standing work order requires authority_principal")
	}
	return nil
}

func NormalizeConditionalGrant(grant ConditionalGrant) ConditionalGrant {
	grant.ID = strings.TrimSpace(grant.ID)
	grant.StandingWorkOrderID = strings.TrimSpace(grant.StandingWorkOrderID)
	grant.Capability = normalizeExternalRuntimeToken(grant.Capability)
	grant.Tool = strings.TrimSpace(grant.Tool)
	grant.Actions = normalizeExternalRuntimeTokens(grant.Actions)
	grant.CredentialScope = strings.TrimSpace(grant.CredentialScope)
	grant.Conditions.Triggers = normalizeUniqueStrings(grant.Conditions.Triggers)
	grant.Conditions.Schedule.Kind = normalizeExternalRuntimeToken(grant.Conditions.Schedule.Kind)
	grant.Conditions.Schedule.Expression = strings.TrimSpace(grant.Conditions.Schedule.Expression)
	grant.Conditions.Schedule.Timezone = strings.TrimSpace(grant.Conditions.Schedule.Timezone)
	grant.Constraints = normalizeExternalRuntimeRawMap(grant.Constraints)
	grant.Materializes.LeaseKind = normalizeExternalRuntimeToken(grant.Materializes.LeaseKind)
	grant.Materializes.ReviewRoute = normalizeExternalRuntimeToken(grant.Materializes.ReviewRoute)
	grant.Status = normalizeExternalRuntimeToken(grant.Status)
	return grant
}

func ValidateConditionalGrant(grant ConditionalGrant) error {
	grant = NormalizeConditionalGrant(grant)
	if grant.ID == "" || grant.StandingWorkOrderID == "" {
		return fmt.Errorf("conditional grant requires id and standing_work_order_id")
	}
	if grant.StandingWorkOrderVersion <= 0 {
		return fmt.Errorf("conditional grant requires positive standing_work_order_version")
	}
	if grant.Capability == "" {
		return fmt.Errorf("conditional grant requires capability")
	}
	if len(grant.Actions) == 0 {
		return fmt.Errorf("conditional grant requires at least one action")
	}
	if grant.Status != "" && grant.Status != "active" {
		return fmt.Errorf("conditional grant status %q cannot materialize leases", grant.Status)
	}
	if grant.Materializes.LeaseKind == "" {
		return fmt.Errorf("conditional grant requires materialized lease kind")
	}
	if grant.Materializes.TTLSeconds <= 0 {
		return fmt.Errorf("conditional grant requires positive ttl_seconds")
	}
	return validateExternalRuntimeRawMap("conditional grant constraints", grant.Constraints)
}

func NormalizeStandingWorkOrderAmendment(amendment StandingWorkOrderAmendment) StandingWorkOrderAmendment {
	amendment.ID = strings.TrimSpace(amendment.ID)
	amendment.StandingWorkOrderID = strings.TrimSpace(amendment.StandingWorkOrderID)
	amendment.ProposedBy = strings.TrimSpace(amendment.ProposedBy)
	amendment.Status = normalizeExternalRuntimeToken(amendment.Status)
	amendment.Reason = strings.TrimSpace(amendment.Reason)
	amendment.ChangeClass = normalizeExternalRuntimeTokens(amendment.ChangeClass)
	amendment.Diff = normalizeExternalRuntimeRawMap(amendment.Diff)
	amendment.RiskDelta = normalizeExternalRuntimeRawMap(amendment.RiskDelta)
	return amendment
}

func ValidateStandingWorkOrderAmendment(amendment StandingWorkOrderAmendment) error {
	amendment = NormalizeStandingWorkOrderAmendment(amendment)
	if amendment.ID == "" || amendment.StandingWorkOrderID == "" || amendment.ProposedBy == "" {
		return fmt.Errorf("standing work order amendment requires id, standing_work_order_id, and proposed_by")
	}
	if amendment.FromVersion <= 0 || amendment.ProposedVersion <= 0 || amendment.ProposedVersion == amendment.FromVersion {
		return fmt.Errorf("standing work order amendment requires distinct positive versions")
	}
	if len(amendment.ChangeClass) == 0 {
		return fmt.Errorf("standing work order amendment requires change_class")
	}
	if err := validateExternalRuntimeRawMap("standing work order amendment diff", amendment.Diff); err != nil {
		return err
	}
	return validateExternalRuntimeRawMap("standing work order amendment risk_delta", amendment.RiskDelta)
}

func NormalizeGatewayPresenceContract(contract GatewayPresenceContract) GatewayPresenceContract {
	contract.ContractID = strings.TrimSpace(contract.ContractID)
	contract.ContractVersion = strings.TrimSpace(contract.ContractVersion)
	contract.Status = normalizeExternalRuntimeToken(contract.Status)
	if contract.Status == "" {
		contract.Status = GatewayPresenceStatusActive
	}
	contract.RuntimeKind = normalizeExternalRuntimeToken(contract.RuntimeKind)
	contract.Channel = normalizeExternalRuntimeToken(contract.Channel)
	contract.Account = strings.TrimSpace(contract.Account)
	contract.StandingWorkOrderID = strings.TrimSpace(contract.StandingWorkOrderID)
	contract.ConditionalGrantID = strings.TrimSpace(contract.ConditionalGrantID)
	contract.InboundMode = normalizeExternalRuntimeToken(contract.InboundMode)
	contract.DialogueMode = normalizeExternalRuntimeToken(contract.DialogueMode)
	contract.SameConversationReplyPolicy = normalizeExternalRuntimeToken(contract.SameConversationReplyPolicy)
	contract.EffectBoundary = normalizeExternalRuntimeToken(contract.EffectBoundary)
	contract.OutboundMode = normalizeExternalRuntimeToken(contract.OutboundMode)
	contract.AllowedSenderIDs = normalizeUniqueStrings(contract.AllowedSenderIDs)
	contract.PairingPolicy = normalizeExternalRuntimeToken(contract.PairingPolicy)
	contract.UnknownSenderBehavior = normalizeExternalRuntimeToken(contract.UnknownSenderBehavior)
	contract.MemoryAdmission = normalizeExternalRuntimeToken(contract.MemoryAdmission)
	contract.OutboundDeliveryPolicy = normalizeExternalRuntimeToken(contract.OutboundDeliveryPolicy)
	contract.CredentialScope = strings.TrimSpace(contract.CredentialScope)
	contract.StateRoot = strings.TrimSpace(contract.StateRoot)
	contract.ReviewTargetPrincipal = strings.TrimSpace(contract.ReviewTargetPrincipal)
	return contract
}

func ValidateGatewayPresenceContract(contract GatewayPresenceContract) error {
	contract = NormalizeGatewayPresenceContract(contract)
	if contract.Status != GatewayPresenceStatusActive {
		return fmt.Errorf("gateway presence contract is not active")
	}
	if contract.RuntimeKind == "" || contract.Channel == "" || contract.Account == "" {
		return fmt.Errorf("gateway presence contract requires runtime_kind, channel, and account")
	}
	if contract.DialogueMode != "" && contract.DialogueMode != GatewayDialogueModeDirectChildPersona {
		return fmt.Errorf("gateway presence contract has unsupported dialogue_mode %q", contract.DialogueMode)
	}
	if contract.SameConversationReplyPolicy != "" && contract.SameConversationReplyPolicy != GatewaySameConversationReplyAllowAdmitted {
		return fmt.Errorf("gateway presence contract has unsupported same_conversation_reply_policy %q", contract.SameConversationReplyPolicy)
	}
	if contract.EffectBoundary != "" && contract.EffectBoundary != GatewayEffectBoundaryAphelionBrokered {
		return fmt.Errorf("gateway presence contract has unsupported effect_boundary %q", contract.EffectBoundary)
	}
	if len(contract.AllowedSenderIDs) == 0 {
		return fmt.Errorf("gateway presence contract requires allowed_sender_ids")
	}
	if contract.StateRoot != "" && !filepath.IsAbs(contract.StateRoot) {
		return fmt.Errorf("gateway presence state_root must be absolute")
	}
	return nil
}

func NormalizeLeaseMaterialization(materialization LeaseMaterialization) LeaseMaterialization {
	materialization.ID = strings.TrimSpace(materialization.ID)
	materialization.AgentID = strings.TrimSpace(materialization.AgentID)
	materialization.StandingWorkOrderID = strings.TrimSpace(materialization.StandingWorkOrderID)
	materialization.MatchedConditions.Trigger = strings.TrimSpace(materialization.MatchedConditions.Trigger)
	materialization.RuntimeSpecHash = strings.TrimSpace(materialization.RuntimeSpecHash)
	for i := range materialization.IssuedLeases {
		materialization.IssuedLeases[i] = NormalizeMaterializedLease(materialization.IssuedLeases[i])
	}
	if !materialization.MatchedConditions.ScheduleTick.IsZero() {
		materialization.MatchedConditions.ScheduleTick = materialization.MatchedConditions.ScheduleTick.UTC()
	}
	if !materialization.CreatedAt.IsZero() {
		materialization.CreatedAt = materialization.CreatedAt.UTC()
	}
	return materialization
}

func NormalizeMaterializedLease(lease MaterializedLease) MaterializedLease {
	lease.LeaseID = strings.TrimSpace(lease.LeaseID)
	lease.ConditionalGrantID = strings.TrimSpace(lease.ConditionalGrantID)
	lease.Capability = normalizeExternalRuntimeToken(lease.Capability)
	lease.LeaseKind = normalizeExternalRuntimeToken(lease.LeaseKind)
	lease.ReviewRoute = normalizeExternalRuntimeToken(lease.ReviewRoute)
	if !lease.ExpiresAt.IsZero() {
		lease.ExpiresAt = lease.ExpiresAt.UTC()
	}
	return lease
}

func ValidateLeaseMaterialization(materialization LeaseMaterialization) error {
	materialization = NormalizeLeaseMaterialization(materialization)
	if materialization.ID == "" || materialization.AgentID == "" || materialization.StandingWorkOrderID == "" {
		return fmt.Errorf("lease materialization requires id, agent_id, and standing_work_order_id")
	}
	if materialization.StandingWorkOrderVersion <= 0 {
		return fmt.Errorf("lease materialization requires positive standing_work_order_version")
	}
	if materialization.RuntimeSpecHash == "" {
		return fmt.Errorf("lease materialization requires runtime_spec_hash")
	}
	if len(materialization.IssuedLeases) == 0 {
		return fmt.Errorf("lease materialization requires issued leases")
	}
	for _, lease := range materialization.IssuedLeases {
		if lease.LeaseID == "" || lease.ConditionalGrantID == "" || lease.Capability == "" || lease.LeaseKind == "" {
			return fmt.Errorf("lease materialization contains incomplete lease")
		}
		if lease.ConditionalGrantVersion <= 0 {
			return fmt.Errorf("lease materialization lease requires conditional grant version")
		}
	}
	return nil
}

func NormalizeGatewayEvent(event GatewayEvent) GatewayEvent {
	event.EventID = strings.TrimSpace(event.EventID)
	event.RuntimeKind = normalizeExternalRuntimeToken(event.RuntimeKind)
	event.Channel = normalizeExternalRuntimeToken(event.Channel)
	event.Account = strings.TrimSpace(event.Account)
	event.SenderID = strings.TrimSpace(event.SenderID)
	event.TransportMessageID = strings.TrimSpace(event.TransportMessageID)
	event.RawPayloadHash = strings.TrimSpace(event.RawPayloadHash)
	event.Text = strings.TrimSpace(event.Text)
	if !event.AdapterTimestamp.IsZero() {
		event.AdapterTimestamp = event.AdapterTimestamp.UTC()
	}
	return event
}

func NormalizeDialogueTurn(turn DialogueTurn) DialogueTurn {
	turn.TurnID = strings.TrimSpace(turn.TurnID)
	turn.EventID = strings.TrimSpace(turn.EventID)
	turn.RuntimeKind = normalizeExternalRuntimeToken(turn.RuntimeKind)
	turn.Channel = normalizeExternalRuntimeToken(turn.Channel)
	turn.Account = strings.TrimSpace(turn.Account)
	turn.SenderID = strings.TrimSpace(turn.SenderID)
	turn.TransportMessageID = strings.TrimSpace(turn.TransportMessageID)
	turn.Text = strings.TrimSpace(turn.Text)
	turn.AdmissionDecision = normalizeExternalRuntimeToken(turn.AdmissionDecision)
	turn.GatewayContractHash = strings.TrimSpace(turn.GatewayContractHash)
	turn.MemoryAdmission = normalizeExternalRuntimeToken(turn.MemoryAdmission)
	if !turn.CreatedAt.IsZero() {
		turn.CreatedAt = turn.CreatedAt.UTC()
	}
	return turn
}

func ValidateDialogueTurn(turn DialogueTurn) error {
	turn = NormalizeDialogueTurn(turn)
	if turn.TurnID == "" || turn.EventID == "" || turn.SenderID == "" || turn.TransportMessageID == "" {
		return fmt.Errorf("dialogue turn requires turn_id, event_id, sender_id, and transport_message_id")
	}
	if turn.Channel == "" || turn.Account == "" {
		return fmt.Errorf("dialogue turn requires channel and account")
	}
	if turn.AdmissionDecision == "" {
		return fmt.Errorf("dialogue turn requires admission_decision")
	}
	return nil
}

func NormalizeSameConversationReply(reply SameConversationReply) SameConversationReply {
	reply.ReplyID = strings.TrimSpace(reply.ReplyID)
	reply.TurnID = strings.TrimSpace(reply.TurnID)
	reply.Channel = normalizeExternalRuntimeToken(reply.Channel)
	reply.Account = strings.TrimSpace(reply.Account)
	reply.SenderID = strings.TrimSpace(reply.SenderID)
	reply.Text = strings.TrimSpace(reply.Text)
	reply.GatewayContractHash = strings.TrimSpace(reply.GatewayContractHash)
	if !reply.CreatedAt.IsZero() {
		reply.CreatedAt = reply.CreatedAt.UTC()
	}
	return reply
}

func ValidateSameConversationReply(reply SameConversationReply) error {
	reply = NormalizeSameConversationReply(reply)
	if reply.ReplyID == "" || reply.TurnID == "" || reply.SenderID == "" {
		return fmt.Errorf("same-conversation reply requires reply_id, turn_id, and sender_id")
	}
	if reply.Channel == "" || reply.Account == "" || reply.Text == "" {
		return fmt.Errorf("same-conversation reply requires channel, account, and text")
	}
	return nil
}

func DialogueTurnFromGatewayEvent(contract GatewayPresenceContract, event GatewayEvent, now time.Time) (DialogueTurn, error) {
	contract = NormalizeGatewayPresenceContract(contract)
	if err := ValidateGatewayPresenceContract(contract); err != nil {
		return DialogueTurn{}, err
	}
	event = NormalizeGatewayEvent(event)
	if event.RuntimeKind != "" && event.RuntimeKind != contract.RuntimeKind {
		return DialogueTurn{}, fmt.Errorf("gateway event runtime_kind does not match contract")
	}
	if event.Channel != "" && event.Channel != contract.Channel {
		return DialogueTurn{}, fmt.Errorf("gateway event channel does not match contract")
	}
	if event.Account != "" && event.Account != contract.Account {
		return DialogueTurn{}, fmt.Errorf("gateway event account does not match contract")
	}
	if event.SenderID == "" || event.TransportMessageID == "" {
		return DialogueTurn{}, fmt.Errorf("gateway event requires sender_id and transport_message_id")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	hash, err := StableExternalRuntimeContractHash(contract)
	if err != nil {
		return DialogueTurn{}, err
	}
	admitted := stringSliceContains(contract.AllowedSenderIDs, event.SenderID)
	decision := "admitted"
	if !admitted {
		decision = firstNonEmptyExternal(contract.UnknownSenderBehavior, "rejected_unknown_sender")
	}
	eventID := event.EventID
	if eventID == "" {
		eventID = externalRuntimeID("gateway_event", contract.Channel, contract.Account, event.TransportMessageID, event.SenderID)
	}
	return DialogueTurn{
		TurnID:              externalRuntimeID("dialogue_turn", hash, eventID, event.TransportMessageID),
		EventID:             eventID,
		RuntimeKind:         firstNonEmptyExternal(event.RuntimeKind, contract.RuntimeKind),
		Channel:             firstNonEmptyExternal(event.Channel, contract.Channel),
		Account:             firstNonEmptyExternal(event.Account, contract.Account),
		SenderID:            event.SenderID,
		TransportMessageID:  event.TransportMessageID,
		Text:                event.Text,
		Admitted:            admitted,
		AdmissionDecision:   decision,
		GatewayContractHash: hash,
		MemoryAdmission:     contract.MemoryAdmission,
		CreatedAt:           now.UTC(),
	}, nil
}

func SameConversationReplyFromDialogue(contract GatewayPresenceContract, turn DialogueTurn, text string, now time.Time) (SameConversationReply, error) {
	contract = NormalizeGatewayPresenceContract(contract)
	if err := ValidateGatewayPresenceContract(contract); err != nil {
		return SameConversationReply{}, err
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return SameConversationReply{}, fmt.Errorf("same-conversation reply requires text")
	}
	if !turn.Admitted {
		return SameConversationReply{}, fmt.Errorf("same-conversation reply requires admitted dialogue turn")
	}
	if normalizeExternalRuntimeToken(contract.SameConversationReplyPolicy) != GatewaySameConversationReplyAllowAdmitted {
		return SameConversationReply{}, fmt.Errorf("same-conversation reply is not allowed by gateway contract")
	}
	if turn.Channel != contract.Channel || turn.Account != contract.Account || !stringSliceContains(contract.AllowedSenderIDs, turn.SenderID) {
		return SameConversationReply{}, fmt.Errorf("same-conversation reply turn does not match gateway contract")
	}
	hash, err := StableExternalRuntimeContractHash(contract)
	if err != nil {
		return SameConversationReply{}, err
	}
	if turn.GatewayContractHash != "" && turn.GatewayContractHash != hash {
		return SameConversationReply{}, fmt.Errorf("same-conversation reply gateway contract hash mismatch")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	return SameConversationReply{
		ReplyID:             externalRuntimeID("same_reply", hash, turn.TurnID, text),
		TurnID:              strings.TrimSpace(turn.TurnID),
		Channel:             turn.Channel,
		Account:             turn.Account,
		SenderID:            turn.SenderID,
		Text:                text,
		GatewayContractHash: hash,
		CreatedAt:           now.UTC(),
	}, nil
}

func EffectRequestFromDialogue(turn DialogueTurn, input EffectRequestInput) (EffectRequest, error) {
	input.Action = normalizeExternalRuntimeToken(input.Action)
	input.Provider = strings.TrimSpace(input.Provider)
	input.AgentID = strings.TrimSpace(input.AgentID)
	input.Purpose = strings.TrimSpace(input.Purpose)
	input.Constraints = normalizeExternalRuntimeRawMap(input.Constraints)
	if !turn.Admitted {
		return EffectRequest{}, fmt.Errorf("effect request requires admitted dialogue turn")
	}
	if input.AgentID == "" || input.Action == "" || input.Provider == "" {
		return EffectRequest{}, fmt.Errorf("effect request requires agent_id, action, and provider")
	}
	if err := validateExternalRuntimeRawMap("effect request constraints", input.Constraints); err != nil {
		return EffectRequest{}, err
	}
	now := input.CreatedAt
	if now.IsZero() {
		now = time.Now().UTC()
	}
	id := strings.TrimSpace(input.ID)
	if id == "" {
		id = externalRuntimeID("effect_req", input.AgentID, turn.TurnID, input.Provider, input.Action, string(mustExternalRuntimeJSON(input.Constraints)))
	}
	return EffectRequest{
		ID:             id,
		AgentID:        input.AgentID,
		Source:         ExternalRuntimeEffectSourceGatewayDialogue,
		DialogueTurnID: strings.TrimSpace(turn.TurnID),
		RequestedBy:    "sender:" + strings.TrimSpace(turn.SenderID),
		Action:         input.Action,
		Provider:       input.Provider,
		Purpose:        input.Purpose,
		Constraints:    input.Constraints,
		CreatedAt:      now.UTC(),
	}, nil
}

func MaterializeStandingWorkOrderLeases(order StandingWorkOrder, grants []ConditionalGrant, trigger string, runtimeSpecHash string, now time.Time) (LeaseMaterialization, error) {
	order = NormalizeStandingWorkOrder(order)
	if err := ValidateStandingWorkOrder(order); err != nil {
		return LeaseMaterialization{}, err
	}
	trigger = strings.TrimSpace(trigger)
	if trigger == "" {
		return LeaseMaterialization{}, fmt.Errorf("lease materialization requires trigger")
	}
	runtimeSpecHash = strings.TrimSpace(runtimeSpecHash)
	if runtimeSpecHash == "" {
		return LeaseMaterialization{}, fmt.Errorf("lease materialization requires runtime_spec_hash")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	allowedGrantIDs := make(map[string]struct{}, len(order.ConditionalGrantIDs))
	for _, id := range order.ConditionalGrantIDs {
		allowedGrantIDs[id] = struct{}{}
	}
	var issued []MaterializedLease
	for _, grant := range grants {
		grant = NormalizeConditionalGrant(grant)
		if _, ok := allowedGrantIDs[grant.ID]; !ok {
			continue
		}
		if grant.StandingWorkOrderID != order.ID || grant.StandingWorkOrderVersion != order.Version {
			continue
		}
		if !conditionalGrantMatchesTrigger(grant, trigger) {
			continue
		}
		if err := ValidateConditionalGrant(grant); err != nil {
			return LeaseMaterialization{}, err
		}
		leaseSeed := externalRuntimeID("lease_seed", order.ID, fmt.Sprint(order.Version), grant.ID, trigger, now.UTC().Format(time.RFC3339Nano))
		issued = append(issued, MaterializedLease{
			LeaseID:                 "lease_" + strings.TrimPrefix(leaseSeed, "lease_seed-"),
			ConditionalGrantID:      grant.ID,
			ConditionalGrantVersion: grant.StandingWorkOrderVersion,
			Capability:              grant.Capability,
			LeaseKind:               grant.Materializes.LeaseKind,
			ReviewRoute:             grant.Materializes.ReviewRoute,
			SingleUse:               grant.Materializes.SingleUse,
			ExpiresAt:               now.UTC().Add(time.Duration(grant.Materializes.TTLSeconds) * time.Second),
		})
	}
	if len(issued) == 0 {
		return LeaseMaterialization{}, fmt.Errorf("standing work order has no matching conditional grants for trigger %q", trigger)
	}
	return LeaseMaterialization{
		ID:                       externalRuntimeID("lm", order.ID, fmt.Sprint(order.Version), trigger, now.UTC().Format(time.RFC3339Nano)),
		AgentID:                  order.AgentID,
		StandingWorkOrderID:      order.ID,
		StandingWorkOrderVersion: order.Version,
		MatchedConditions:        MatchedConditions{Trigger: trigger, ScheduleTick: now.UTC()},
		RuntimeSpecHash:          runtimeSpecHash,
		IssuedLeases:             issued,
		CreatedAt:                now.UTC(),
	}, nil
}

func DiscoveredEffectContractFromRequest(req EffectRequest, opts DiscoveredEffectContractOptions) (DiscoveredEffectContract, error) {
	req = NormalizeEffectRequest(req)
	if err := ValidateEffectRequest(req); err != nil {
		return DiscoveredEffectContract{}, err
	}
	opts.ReviewRoute = normalizeExternalRuntimeToken(opts.ReviewRoute)
	opts.Materializes.LeaseKind = normalizeExternalRuntimeToken(opts.Materializes.LeaseKind)
	opts.Materializes.ReviewRoute = normalizeExternalRuntimeToken(opts.Materializes.ReviewRoute)
	if opts.Materializes.LeaseKind == "" {
		opts.Materializes.LeaseKind = ExternalRuntimeLeaseKindToolInvocation
	}
	if opts.Materializes.TTLSeconds <= 0 {
		opts.Materializes.TTLSeconds = 900
	}
	if opts.ReviewRoute == "" {
		opts.ReviewRoute = firstNonEmptyExternal(opts.Materializes.ReviewRoute, "review_principal")
	}
	if opts.Materializes.ReviewRoute == "" {
		opts.Materializes.ReviewRoute = opts.ReviewRoute
	}
	if opts.ExpectedResult.Kind == "" {
		opts.ExpectedResult.Kind = "effect_result"
	}
	if opts.ExpectedResult.ArtifactPolicy == "" {
		opts.ExpectedResult.ArtifactPolicy = "bounded_redacted_summary"
	}
	now := opts.CreatedAt
	if now.IsZero() {
		now = time.Now().UTC()
	}
	id := strings.TrimSpace(opts.ID)
	if id == "" {
		id = externalRuntimeID("effect_contract", req.ID, req.Provider, req.Action, string(mustExternalRuntimeJSON(req.Constraints)))
	}
	contract := DiscoveredEffectContract{
		ID:                    id,
		AgentID:               req.AgentID,
		SourceEffectRequestID: req.ID,
		ContractKind:          ExternalRuntimeContractKindExternalEffect,
		Provider:              req.Provider,
		Action:                req.Action,
		ReviewRoute:           opts.ReviewRoute,
		Constraints:           req.Constraints,
		Materializes:          opts.Materializes,
		ExpectedResult:        opts.ExpectedResult,
		CreatedAt:             now.UTC(),
	}
	return NormalizeDiscoveredEffectContract(contract), ValidateDiscoveredEffectContract(contract)
}

func NormalizeEffectRequest(req EffectRequest) EffectRequest {
	req.ID = strings.TrimSpace(req.ID)
	req.AgentID = strings.TrimSpace(req.AgentID)
	req.Source = normalizeExternalRuntimeToken(req.Source)
	req.DialogueTurnID = strings.TrimSpace(req.DialogueTurnID)
	req.RequestedBy = strings.TrimSpace(req.RequestedBy)
	req.Action = normalizeExternalRuntimeToken(req.Action)
	req.Provider = strings.TrimSpace(req.Provider)
	req.Purpose = strings.TrimSpace(req.Purpose)
	req.Constraints = normalizeExternalRuntimeRawMap(req.Constraints)
	if !req.CreatedAt.IsZero() {
		req.CreatedAt = req.CreatedAt.UTC()
	}
	return req
}

func ValidateEffectRequest(req EffectRequest) error {
	req = NormalizeEffectRequest(req)
	if req.ID == "" || req.AgentID == "" {
		return fmt.Errorf("effect request requires id and agent_id")
	}
	if req.Source == "" || req.DialogueTurnID == "" || req.RequestedBy == "" {
		return fmt.Errorf("effect request requires source, dialogue_turn_id, and requested_by")
	}
	if req.Action == "" || req.Provider == "" {
		return fmt.Errorf("effect request requires action and provider")
	}
	return validateExternalRuntimeRawMap("effect request constraints", req.Constraints)
}

func NormalizeDiscoveredEffectContract(contract DiscoveredEffectContract) DiscoveredEffectContract {
	contract.ID = strings.TrimSpace(contract.ID)
	contract.AgentID = strings.TrimSpace(contract.AgentID)
	contract.SourceEffectRequestID = strings.TrimSpace(contract.SourceEffectRequestID)
	contract.ContractKind = normalizeExternalRuntimeToken(contract.ContractKind)
	if contract.ContractKind == "" {
		contract.ContractKind = ExternalRuntimeContractKindExternalEffect
	}
	contract.Provider = strings.TrimSpace(contract.Provider)
	contract.Action = normalizeExternalRuntimeToken(contract.Action)
	contract.ReviewRoute = normalizeExternalRuntimeToken(contract.ReviewRoute)
	contract.Constraints = normalizeExternalRuntimeRawMap(contract.Constraints)
	contract.Materializes.LeaseKind = normalizeExternalRuntimeToken(contract.Materializes.LeaseKind)
	contract.Materializes.ReviewRoute = normalizeExternalRuntimeToken(contract.Materializes.ReviewRoute)
	if !contract.CreatedAt.IsZero() {
		contract.CreatedAt = contract.CreatedAt.UTC()
	}
	return contract
}

func ValidateDiscoveredEffectContract(contract DiscoveredEffectContract) error {
	contract = NormalizeDiscoveredEffectContract(contract)
	if contract.ID == "" || contract.AgentID == "" || contract.SourceEffectRequestID == "" {
		return fmt.Errorf("discovered effect contract requires id, agent_id, and source_effect_request_id")
	}
	if contract.ContractKind != ExternalRuntimeContractKindExternalEffect {
		return fmt.Errorf("discovered effect contract has unsupported contract_kind %q", contract.ContractKind)
	}
	if contract.Provider == "" || contract.Action == "" {
		return fmt.Errorf("discovered effect contract requires provider and action")
	}
	if contract.ReviewRoute == "" {
		return fmt.Errorf("discovered effect contract requires review_route")
	}
	if contract.Materializes.LeaseKind == "" || contract.Materializes.TTLSeconds <= 0 {
		return fmt.Errorf("discovered effect contract requires materialization lease kind and ttl")
	}
	return validateExternalRuntimeRawMap("discovered effect constraints", contract.Constraints)
}

func ExternalRuntimeTaskPacketPayloadFromDiscoveredEffect(contract DiscoveredEffectContract, materialization LeaseMaterialization) (ExternalRuntimeTaskPacketPayload, error) {
	contract = NormalizeDiscoveredEffectContract(contract)
	if err := ValidateDiscoveredEffectContract(contract); err != nil {
		return ExternalRuntimeTaskPacketPayload{}, err
	}
	materialization = NormalizeLeaseMaterialization(materialization)
	if err := ValidateLeaseMaterialization(materialization); err != nil {
		return ExternalRuntimeTaskPacketPayload{}, err
	}
	if materialization.AgentID != "" && materialization.AgentID != contract.AgentID {
		return ExternalRuntimeTaskPacketPayload{}, fmt.Errorf("lease materialization agent_id mismatch")
	}
	return ExternalRuntimeTaskPacketPayload{
		Schema:               ExternalRuntimeTaskPacketSchemaV1,
		AgentID:              contract.AgentID,
		EffectContractID:     contract.ID,
		LeaseMaterialization: materialization,
		Authority:            append([]MaterializedLease(nil), materialization.IssuedLeases...),
		Effect:               contract,
	}, nil
}

func NormalizeEffectResult(result EffectResult) EffectResult {
	result.ResultID = strings.TrimSpace(result.ResultID)
	result.EffectRequestID = strings.TrimSpace(result.EffectRequestID)
	result.EffectContractID = strings.TrimSpace(result.EffectContractID)
	result.Status = normalizeExternalRuntimeToken(result.Status)
	result.Summary = strings.TrimSpace(result.Summary)
	result.ArtifactRefs = normalizeUniqueStrings(result.ArtifactRefs)
	result.BlockerKind = normalizeExternalRuntimeToken(result.BlockerKind)
	if !result.CreatedAt.IsZero() {
		result.CreatedAt = result.CreatedAt.UTC()
	}
	return result
}

func ValidateEffectResult(result EffectResult) error {
	result = NormalizeEffectResult(result)
	if result.ResultID == "" || result.EffectContractID == "" || result.Status == "" {
		return fmt.Errorf("effect result requires result_id, effect_contract_id, and status")
	}
	if result.Status == "blocked" && result.BlockerKind == "" {
		return fmt.Errorf("blocked effect result requires blocker_kind")
	}
	return nil
}

func NormalizeParentMemoryAdmission(admission ParentMemoryAdmission) ParentMemoryAdmission {
	admission.AdmissionID = strings.TrimSpace(admission.AdmissionID)
	admission.AgentID = strings.TrimSpace(admission.AgentID)
	admission.SourceKind = normalizeExternalRuntimeToken(admission.SourceKind)
	admission.SourceID = strings.TrimSpace(admission.SourceID)
	admission.Status = normalizeExternalRuntimeToken(admission.Status)
	if admission.Status == "" {
		admission.Status = ExternalRuntimeMemoryAdmissionStatusPending
	}
	admission.Summary = strings.TrimSpace(admission.Summary)
	admission.ReviewRoute = normalizeExternalRuntimeToken(admission.ReviewRoute)
	admission.ArtifactRefs = normalizeUniqueStrings(admission.ArtifactRefs)
	if !admission.RequestedAt.IsZero() {
		admission.RequestedAt = admission.RequestedAt.UTC()
	}
	if !admission.AcceptedAt.IsZero() {
		admission.AcceptedAt = admission.AcceptedAt.UTC()
	}
	return admission
}

func ValidateParentMemoryAdmission(admission ParentMemoryAdmission) error {
	admission = NormalizeParentMemoryAdmission(admission)
	if admission.AdmissionID == "" || admission.AgentID == "" || admission.SourceKind == "" || admission.SourceID == "" {
		return fmt.Errorf("parent memory admission requires admission_id, agent_id, source_kind, and source_id")
	}
	if admission.Status == "" {
		return fmt.Errorf("parent memory admission requires status")
	}
	return nil
}

func NormalizeExternalRuntimeTaskPacketPayload(payload ExternalRuntimeTaskPacketPayload) ExternalRuntimeTaskPacketPayload {
	payload.Schema = strings.TrimSpace(payload.Schema)
	if payload.Schema == "" {
		payload.Schema = ExternalRuntimeTaskPacketSchemaV1
	}
	payload.AgentID = strings.TrimSpace(payload.AgentID)
	payload.EffectContractID = strings.TrimSpace(payload.EffectContractID)
	payload.LeaseMaterialization = NormalizeLeaseMaterialization(payload.LeaseMaterialization)
	for i := range payload.Authority {
		payload.Authority[i] = NormalizeMaterializedLease(payload.Authority[i])
	}
	payload.Effect = NormalizeDiscoveredEffectContract(payload.Effect)
	return payload
}

func ValidateExternalRuntimeTaskPacketPayload(payload ExternalRuntimeTaskPacketPayload) error {
	payload = NormalizeExternalRuntimeTaskPacketPayload(payload)
	if payload.Schema != ExternalRuntimeTaskPacketSchemaV1 {
		return fmt.Errorf("external runtime task packet has unsupported schema %q", payload.Schema)
	}
	if payload.AgentID == "" || payload.EffectContractID == "" {
		return fmt.Errorf("external runtime task packet requires agent_id and effect_contract_id")
	}
	if err := ValidateLeaseMaterialization(payload.LeaseMaterialization); err != nil {
		return err
	}
	if err := ValidateDiscoveredEffectContract(payload.Effect); err != nil {
		return err
	}
	if payload.Effect.AgentID != payload.AgentID || payload.Effect.ID != payload.EffectContractID {
		return fmt.Errorf("external runtime task packet effect does not match packet envelope")
	}
	if payload.LeaseMaterialization.AgentID != payload.AgentID {
		return fmt.Errorf("external runtime task packet lease materialization agent_id mismatch")
	}
	if len(payload.Authority) != len(payload.LeaseMaterialization.IssuedLeases) {
		return fmt.Errorf("external runtime task packet authority must mirror lease materialization")
	}
	for i, lease := range payload.Authority {
		if lease != payload.LeaseMaterialization.IssuedLeases[i] {
			return fmt.Errorf("external runtime task packet authority lease %d mismatch", i)
		}
	}
	return nil
}

func NormalizeExternalRuntimeTaskResultPayload(payload ExternalRuntimeTaskResultPayload) ExternalRuntimeTaskResultPayload {
	payload.Schema = strings.TrimSpace(payload.Schema)
	if payload.Schema == "" {
		payload.Schema = ExternalRuntimeTaskResultSchemaV1
	}
	payload.AgentID = strings.TrimSpace(payload.AgentID)
	payload.TaskPacketID = strings.TrimSpace(payload.TaskPacketID)
	payload.EffectContractID = strings.TrimSpace(payload.EffectContractID)
	payload.Status = normalizeExternalRuntimeToken(payload.Status)
	payload.Summary = strings.TrimSpace(payload.Summary)
	payload.EffectResult = NormalizeEffectResult(payload.EffectResult)
	return payload
}

func ValidateExternalRuntimeTaskResultPayload(payload ExternalRuntimeTaskResultPayload) error {
	payload = NormalizeExternalRuntimeTaskResultPayload(payload)
	if payload.Schema != ExternalRuntimeTaskResultSchemaV1 {
		return fmt.Errorf("external runtime task result has unsupported schema %q", payload.Schema)
	}
	if payload.AgentID == "" || payload.TaskPacketID == "" || payload.EffectContractID == "" || payload.Status == "" {
		return fmt.Errorf("external runtime task result requires agent_id, task_packet_id, effect_contract_id, and status")
	}
	if err := ValidateEffectResult(payload.EffectResult); err != nil {
		return err
	}
	if payload.EffectResult.EffectContractID != payload.EffectContractID {
		return fmt.Errorf("external runtime task result effect contract mismatch")
	}
	return nil
}

func NormalizeChildRuntimeAdapterOperation(op ChildRuntimeAdapterOperation) ChildRuntimeAdapterOperation {
	op.Operation = normalizeExternalRuntimeToken(op.Operation)
	op.AgentID = strings.TrimSpace(op.AgentID)
	op.RuntimeKind = normalizeExternalRuntimeToken(op.RuntimeKind)
	op.RuntimeMode = normalizeExternalRuntimeToken(op.RuntimeMode)
	op.SpecHash = strings.TrimSpace(op.SpecHash)
	op.InputRef = strings.TrimSpace(op.InputRef)
	op.AuthorityRef = strings.TrimSpace(op.AuthorityRef)
	if !op.Deadline.IsZero() {
		op.Deadline = op.Deadline.UTC()
	}
	return op
}

func ValidateChildRuntimeAdapterOperation(op ChildRuntimeAdapterOperation) error {
	op = NormalizeChildRuntimeAdapterOperation(op)
	switch op.Operation {
	case ChildRuntimeAdapterOperationPreflight,
		ChildRuntimeAdapterOperationInstallStatus,
		ChildRuntimeAdapterOperationStart,
		ChildRuntimeAdapterOperationStop,
		ChildRuntimeAdapterOperationStatus,
		ChildRuntimeAdapterOperationWake,
		ChildRuntimeAdapterOperationCollectArtifacts:
	default:
		return fmt.Errorf("child runtime adapter operation has unsupported operation %q", op.Operation)
	}
	if op.AgentID == "" || op.RuntimeKind == "" || op.RuntimeMode == "" || op.SpecHash == "" {
		return fmt.Errorf("child runtime adapter operation requires agent_id, runtime_kind, runtime_mode, and spec_hash")
	}
	switch op.RuntimeMode {
	case ExternalRuntimeModeOneshot, ExternalRuntimeModeGatewayPresence, ExternalRuntimeModeRemoteService:
	default:
		return fmt.Errorf("child runtime adapter operation has unsupported runtime_mode %q", op.RuntimeMode)
	}
	if op.Operation == ChildRuntimeAdapterOperationWake {
		if op.InputRef == "" || op.AuthorityRef == "" {
			return fmt.Errorf("child runtime adapter wake requires input_ref and authority_ref")
		}
		if op.Deadline.IsZero() {
			return fmt.Errorf("child runtime adapter wake requires deadline")
		}
	}
	return nil
}

func ChildRuntimeAdapterWakeOperationFromTaskPacket(spec DurableExternalRuntimeSpec, packet ExternalRuntimeTaskPacketPayload, packetRef string, authorityRef string, deadline time.Time) (ChildRuntimeAdapterOperation, error) {
	spec = NormalizeDurableExternalRuntimeSpec(spec)
	if err := ValidateDurableExternalRuntimeSpec(spec); err != nil {
		return ChildRuntimeAdapterOperation{}, err
	}
	packet = NormalizeExternalRuntimeTaskPacketPayload(packet)
	if err := ValidateExternalRuntimeTaskPacketPayload(packet); err != nil {
		return ChildRuntimeAdapterOperation{}, err
	}
	packetRef = strings.TrimSpace(packetRef)
	authorityRef = strings.TrimSpace(authorityRef)
	if packetRef == "" || authorityRef == "" {
		return ChildRuntimeAdapterOperation{}, fmt.Errorf("adapter wake operation requires packet_ref and authority_ref")
	}
	if deadline.IsZero() {
		return ChildRuntimeAdapterOperation{}, fmt.Errorf("adapter wake operation requires deadline")
	}
	specHash, err := StableExternalRuntimeContractHash(spec)
	if err != nil {
		return ChildRuntimeAdapterOperation{}, err
	}
	if packet.LeaseMaterialization.RuntimeSpecHash != "" && packet.LeaseMaterialization.RuntimeSpecHash != specHash {
		return ChildRuntimeAdapterOperation{}, fmt.Errorf("adapter wake operation runtime spec hash mismatch")
	}
	op := ChildRuntimeAdapterOperation{
		Operation:    ChildRuntimeAdapterOperationWake,
		AgentID:      packet.AgentID,
		RuntimeKind:  spec.Kind,
		RuntimeMode:  spec.Mode,
		SpecHash:     specHash,
		InputRef:     packetRef,
		AuthorityRef: authorityRef,
		Deadline:     deadline.UTC(),
	}
	return NormalizeChildRuntimeAdapterOperation(op), ValidateChildRuntimeAdapterOperation(op)
}

func StableExternalRuntimeContractHash(value any) (string, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func conditionalGrantMatchesTrigger(grant ConditionalGrant, trigger string) bool {
	trigger = strings.TrimSpace(trigger)
	for _, candidate := range grant.Conditions.Triggers {
		if strings.TrimSpace(candidate) == trigger {
			return true
		}
	}
	if grant.Conditions.Schedule.Kind != "" && trigger == "schedule:"+grant.StandingWorkOrderID {
		return true
	}
	return false
}

func normalizeExternalRuntimeToken(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, "-", "_")
	value = strings.ReplaceAll(value, " ", "_")
	for strings.Contains(value, "__") {
		value = strings.ReplaceAll(value, "__", "_")
	}
	return strings.Trim(value, "_")
}

func normalizeExternalRuntimeTokens(values []string) []string {
	out := make([]string, 0, len(values))
	seen := map[string]struct{}{}
	for _, value := range values {
		value = normalizeExternalRuntimeToken(value)
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

func normalizeExternalRuntimeStringMap(values map[string]string) map[string]string {
	out := map[string]string{}
	for key, value := range values {
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key != "" && value != "" {
			out[key] = value
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func normalizeExternalRuntimeRawMap(values map[string]json.RawMessage) map[string]json.RawMessage {
	out := map[string]json.RawMessage{}
	for key, value := range values {
		key = strings.TrimSpace(key)
		value = bytes.TrimSpace(value)
		if key == "" || len(value) == 0 {
			continue
		}
		var compact bytes.Buffer
		if json.Compact(&compact, value) == nil {
			out[key] = append(json.RawMessage(nil), compact.Bytes()...)
		} else {
			out[key] = append(json.RawMessage(nil), value...)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func validateExternalRuntimeRawMap(label string, values map[string]json.RawMessage) error {
	for key, value := range values {
		if !json.Valid(value) {
			return fmt.Errorf("%s %q is not valid JSON", label, key)
		}
	}
	return nil
}

func stringSliceContains(values []string, want string) bool {
	want = strings.TrimSpace(want)
	for _, value := range values {
		if strings.TrimSpace(value) == want {
			return true
		}
	}
	return false
}

func firstNonEmptyExternal(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

func externalRuntimeID(prefix string, parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	token := hex.EncodeToString(sum[:])
	if len(token) > 24 {
		token = token[:24]
	}
	return strings.TrimSpace(prefix) + "-" + token
}

func mustExternalRuntimeJSON(value any) []byte {
	raw, err := json.Marshal(value)
	if err != nil {
		panic(fmt.Sprintf("marshal external runtime contract JSON: %v", err))
	}
	return raw
}
