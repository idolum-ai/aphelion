//go:build linux

package session

import (
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
)

type TurnRunKind string

const (
	TurnRunKindInteractive TurnRunKind = "interactive"
	TurnRunKindHeartbeat   TurnRunKind = "heartbeat"
	TurnRunKindCron        TurnRunKind = "cron"
	TurnRunKindRecovery    TurnRunKind = "recovery"
)

type TurnRunStatus string

const (
	TurnRunStatusRunning     TurnRunStatus = "running"
	TurnRunStatusCompleted   TurnRunStatus = "completed"
	TurnRunStatusFailed      TurnRunStatus = "failed"
	TurnRunStatusInterrupted TurnRunStatus = "interrupted"
)

type ScopeKind string

const (
	ScopeKindTelegramDM    ScopeKind = "telegram_dm"
	ScopeKindTelegramGroup ScopeKind = "telegram_group"
	ScopeKindHeartbeat     ScopeKind = "heartbeat"
	ScopeKindCron          ScopeKind = "cron"
	ScopeKindRecovery      ScopeKind = "recovery"
	ScopeKindDurableAgent  ScopeKind = "durable_agent"
)

type ScopeRef struct {
	Kind            ScopeKind
	ID              string
	DurableAgentID  string
	ParentScopeKind ScopeKind
	ParentScopeID   string
}

type PlanStatus string

const (
	PlanStatusPending    PlanStatus = "pending"
	PlanStatusInProgress PlanStatus = "in_progress"
	PlanStatusCompleted  PlanStatus = "completed"
)

type PlanStep struct {
	Step   string     `json:"step"`
	Status PlanStatus `json:"status"`
}

type PlanState struct {
	Explanation string     `json:"explanation,omitempty"`
	Steps       []PlanStep `json:"steps,omitempty"`
	UpdatedAt   time.Time  `json:"updated_at,omitempty"`
}

type PlanEventKind string

const (
	PlanEventKindToolUpdated   PlanEventKind = "tool_updated"
	PlanEventKindBrokerageSeed PlanEventKind = "brokerage_seed"
	PlanEventKindRehydrated    PlanEventKind = "rehydrated"
)

type PlanEvent struct {
	ID        int64
	SessionID string
	Kind      PlanEventKind
	PlanState PlanState
	CreatedAt time.Time
}

type OperationStatus string

const (
	OperationStatusIdle      OperationStatus = "idle"
	OperationStatusActive    OperationStatus = "active"
	OperationStatusBlocked   OperationStatus = "blocked"
	OperationStatusCompleted OperationStatus = "completed"
	OperationStatusFailed    OperationStatus = "failed"
)

type ProposalStatus string

const (
	ProposalStatusPending    ProposalStatus = "pending"
	ProposalStatusApproved   ProposalStatus = "approved"
	ProposalStatusDenied     ProposalStatus = "denied"
	ProposalStatusExpired    ProposalStatus = "expired"
	ProposalStatusSuperseded ProposalStatus = "superseded"
)

type FindingConfidence string

const (
	FindingConfidenceLow    FindingConfidence = "low"
	FindingConfidenceMedium FindingConfidence = "medium"
	FindingConfidenceHigh   FindingConfidence = "high"
)

type OperationProposal struct {
	ID            string         `json:"id,omitempty"`
	Kind          string         `json:"kind,omitempty"`
	Summary       string         `json:"summary,omitempty"`
	WhyNow        string         `json:"why_now,omitempty"`
	BoundedEffect string         `json:"bounded_effect,omitempty"`
	Status        ProposalStatus `json:"status,omitempty"`
	UpdatedAt     time.Time      `json:"updated_at,omitempty"`
}

type OperationFinding struct {
	Claim      string            `json:"claim"`
	Confidence FindingConfidence `json:"confidence,omitempty"`
	Basis      string            `json:"basis,omitempty"`
}

type OperationArtifact struct {
	Label string `json:"label,omitempty"`
	Ref   string `json:"ref"`
}

type OperationState struct {
	ID        string              `json:"id,omitempty"`
	Objective string              `json:"objective,omitempty"`
	Status    OperationStatus     `json:"status,omitempty"`
	Stage     string              `json:"stage,omitempty"`
	Summary   string              `json:"summary,omitempty"`
	Proposal  OperationProposal   `json:"proposal,omitempty"`
	Findings  []OperationFinding  `json:"findings,omitempty"`
	Artifacts []OperationArtifact `json:"artifacts,omitempty"`
	UpdatedAt time.Time           `json:"updated_at,omitempty"`
}

type CapabilityKind string

const (
	CapabilityKindTool              CapabilityKind = "tool"
	CapabilityKindLocalDevice       CapabilityKind = "local_device"
	CapabilityKindExternalAccount   CapabilityKind = "external_account"
	CapabilityKindPurchase          CapabilityKind = "purchase"
	CapabilityKindPublicWeb         CapabilityKind = "public_web"
	CapabilityKindCommunication     CapabilityKind = "communication"
	CapabilityKindFileAccess        CapabilityKind = "file_access"
	CapabilityKindNetworkAccess     CapabilityKind = "network_access"
	CapabilityKindGenericDelegation CapabilityKind = "generic_delegation"
)

type CapabilityReviewStatus string

const (
	CapabilityReviewStatusProposed       CapabilityReviewStatus = "proposed"
	CapabilityReviewStatusParentApproved CapabilityReviewStatus = "parent_approved"
	CapabilityReviewStatusApproved       CapabilityReviewStatus = "approved"
	CapabilityReviewStatusRejected       CapabilityReviewStatus = "rejected"
)

type CapabilityGrantStatus string

const (
	CapabilityGrantStatusPending CapabilityGrantStatus = "pending"
	CapabilityGrantStatusActive  CapabilityGrantStatus = "active"
	CapabilityGrantStatusStale   CapabilityGrantStatus = "stale"
	CapabilityGrantStatusRevoked CapabilityGrantStatus = "revoked"
	CapabilityGrantStatusExpired CapabilityGrantStatus = "expired"
	CapabilityGrantStatusFailed  CapabilityGrantStatus = "failed"
)

type CapabilityRequest struct {
	RequestID       string                 `json:"request_id"`
	RequestedBy     string                 `json:"requested_by,omitempty"`
	RequestedFor    string                 `json:"requested_for,omitempty"`
	ParentPrincipal string                 `json:"parent_principal,omitempty"`
	AdminPrincipal  string                 `json:"admin_principal,omitempty"`
	Kind            CapabilityKind         `json:"kind,omitempty"`
	TargetResource  string                 `json:"target_resource,omitempty"`
	Purpose         string                 `json:"purpose,omitempty"`
	RiskClass       string                 `json:"risk_class,omitempty"`
	Contract        string                 `json:"contract,omitempty"`
	Constraints     string                 `json:"constraints,omitempty"`
	ReviewStatus    CapabilityReviewStatus `json:"review_status,omitempty"`
	GrantID         string                 `json:"grant_id,omitempty"`
	CreatedAt       time.Time              `json:"created_at,omitempty"`
	UpdatedAt       time.Time              `json:"updated_at,omitempty"`
}

type CapabilityReview struct {
	ReviewID     string                 `json:"review_id"`
	RequestID    string                 `json:"request_id"`
	Reviewer     string                 `json:"reviewer,omitempty"`
	ReviewerRole string                 `json:"reviewer_role,omitempty"`
	Status       CapabilityReviewStatus `json:"status,omitempty"`
	Rationale    string                 `json:"rationale,omitempty"`
	CreatedAt    time.Time              `json:"created_at,omitempty"`
}

type DurableChildAgreementStatus string

const (
	DurableChildAgreementStatusProposed   DurableChildAgreementStatus = "proposed"
	DurableChildAgreementStatusApproved   DurableChildAgreementStatus = "approved"
	DurableChildAgreementStatusRejected   DurableChildAgreementStatus = "rejected"
	DurableChildAgreementStatusSuperseded DurableChildAgreementStatus = "superseded"
)

type DurableChildAgreement struct {
	AgreementID         string                      `json:"agreement_id"`
	AgentID             string                      `json:"agent_id,omitempty"`
	ParentPrincipal     string                      `json:"parent_principal,omitempty"`
	ChildPrincipal      string                      `json:"child_principal,omitempty"`
	SourceSurface       string                      `json:"source_surface,omitempty"`
	SourceRequestID     string                      `json:"source_request_id,omitempty"`
	SourceReviewEventID int64                       `json:"source_review_event_id,omitempty"`
	Summary             string                      `json:"summary,omitempty"`
	BoundedEffect       string                      `json:"bounded_effect,omitempty"`
	Status              DurableChildAgreementStatus `json:"status,omitempty"`
	ArtifactRefs        []RecordReference           `json:"artifact_refs,omitempty"`
	CreatedAt           time.Time                   `json:"created_at,omitempty"`
	UpdatedAt           time.Time                   `json:"updated_at,omitempty"`
}

type CapabilityGrant struct {
	GrantID            string                `json:"grant_id"`
	RequestID          string                `json:"request_id,omitempty"`
	GrantedBy          string                `json:"granted_by,omitempty"`
	GrantedTo          string                `json:"granted_to,omitempty"`
	Kind               CapabilityKind        `json:"kind,omitempty"`
	TargetResource     string                `json:"target_resource,omitempty"`
	AllowedActions     []string              `json:"allowed_actions,omitempty"`
	Contract           string                `json:"contract,omitempty"`
	Constraints        string                `json:"constraints,omitempty"`
	Status             CapabilityGrantStatus `json:"status,omitempty"`
	BaselinePolicyHash string                `json:"baseline_policy_hash,omitempty"`
	CurrentPolicyHash  string                `json:"current_policy_hash,omitempty"`
	AnchorFingerprint  string                `json:"anchor_fingerprint,omitempty"`
	DriftSource        ToolDriftSource       `json:"drift_source,omitempty"`
	StaleReason        string                `json:"stale_reason,omitempty"`
	InvocationCount    int                   `json:"invocation_count,omitempty"`
	FailureCount       int                   `json:"failure_count,omitempty"`
	CreatedAt          time.Time             `json:"created_at,omitempty"`
	UpdatedAt          time.Time             `json:"updated_at,omitempty"`
	GrantedAt          time.Time             `json:"granted_at,omitempty"`
	ExpiresAt          time.Time             `json:"expires_at,omitempty"`
	RevokedAt          time.Time             `json:"revoked_at,omitempty"`
	LastInvokedAt      time.Time             `json:"last_invoked_at,omitempty"`
	LastFailureAt      time.Time             `json:"last_failure_at,omitempty"`
}

type CapabilityInvocation struct {
	InvocationID int64     `json:"invocation_id,omitempty"`
	GrantID      string    `json:"grant_id"`
	Principal    string    `json:"principal,omitempty"`
	Action       string    `json:"action,omitempty"`
	Status       string    `json:"status,omitempty"`
	ErrorText    string    `json:"error_text,omitempty"`
	CreatedAt    time.Time `json:"created_at,omitempty"`
}

type RegisteredTool struct {
	ToolName          string    `json:"tool_name"`
	ImplementationRef string    `json:"implementation_ref,omitempty"`
	Registered        bool      `json:"registered"`
	CreatedAt         time.Time `json:"created_at,omitempty"`
	UpdatedAt         time.Time `json:"updated_at,omitempty"`
}

type ToolInstallStatus string

const (
	ToolInstallStatusPending   ToolInstallStatus = "pending"
	ToolInstallStatusInstalled ToolInstallStatus = "installed"
	ToolInstallStatusVerified  ToolInstallStatus = "verified"
	ToolInstallStatusFailed    ToolInstallStatus = "failed"
	ToolInstallStatusStale     ToolInstallStatus = "stale"
)

type ToolProbeStatus string

const (
	ToolProbeStatusPassed ToolProbeStatus = "passed"
	ToolProbeStatusFailed ToolProbeStatus = "failed"
)

type ToolAuditStatus string

const (
	ToolAuditStatusPassed ToolAuditStatus = "passed"
	ToolAuditStatusFailed ToolAuditStatus = "failed"
)

type ToolDriftSource string

const (
	ToolDriftSourceManifestDrift     ToolDriftSource = "manifest_drift"
	ToolDriftSourceWorkspaceDrift    ToolDriftSource = "workspace_drift"
	ToolDriftSourceContainerDrift    ToolDriftSource = "container_drift"
	ToolDriftSourceInstallRefChanged ToolDriftSource = "install_ref_changed"
	ToolDriftSourceMissingBaseline   ToolDriftSource = "missing_baseline"
	ToolDriftSourceFingerprintError  ToolDriftSource = "fingerprint_error"
	ToolDriftSourceAuditFailure      ToolDriftSource = "audit_failure"
	ToolDriftSourceProbeFailure      ToolDriftSource = "probe_failure"
	ToolDriftSourcePolicyViolation   ToolDriftSource = "policy_violation"
)

type ToolInstallRecord struct {
	ToolName                     string            `json:"tool_name"`
	Installer                    string            `json:"installer,omitempty"`
	InstallRef                   string            `json:"install_ref,omitempty"`
	Status                       ToolInstallStatus `json:"status,omitempty"`
	ProbeStatus                  ToolProbeStatus   `json:"probe_status,omitempty"`
	ProbeOutput                  string            `json:"probe_output,omitempty"`
	Rationale                    string            `json:"rationale,omitempty"`
	ArtifactRefs                 []RecordReference `json:"artifact_refs,omitempty"`
	BaselineFingerprint          string            `json:"baseline_fingerprint,omitempty"`
	CurrentFingerprint           string            `json:"current_fingerprint,omitempty"`
	BaselineInstallRef           string            `json:"baseline_install_ref,omitempty"`
	CurrentInstallRef            string            `json:"current_install_ref,omitempty"`
	BaselineManifestHash         string            `json:"baseline_manifest_hash,omitempty"`
	CurrentManifestHash          string            `json:"current_manifest_hash,omitempty"`
	BaselineWorkspaceFingerprint string            `json:"baseline_workspace_fingerprint,omitempty"`
	CurrentWorkspaceFingerprint  string            `json:"current_workspace_fingerprint,omitempty"`
	StaleReason                  string            `json:"stale_reason,omitempty"`
	DriftSource                  ToolDriftSource   `json:"drift_source,omitempty"`
	ConsecutiveFailures          int               `json:"consecutive_failures,omitempty"`
	CreatedAt                    time.Time         `json:"created_at,omitempty"`
	UpdatedAt                    time.Time         `json:"updated_at,omitempty"`
	InstalledAt                  time.Time         `json:"installed_at,omitempty"`
	LastProbedAt                 time.Time         `json:"last_probed_at,omitempty"`
	LastFailureAt                time.Time         `json:"last_failure_at,omitempty"`
	AttestedAt                   time.Time         `json:"attested_at,omitempty"`
}

type ToolAuditRecord struct {
	ToolName                     string            `json:"tool_name"`
	Status                       ToolAuditStatus   `json:"status,omitempty"`
	AuditOutput                  string            `json:"audit_output,omitempty"`
	Rationale                    string            `json:"rationale,omitempty"`
	ArtifactRefs                 []RecordReference `json:"artifact_refs,omitempty"`
	BaselineFingerprint          string            `json:"baseline_fingerprint,omitempty"`
	CurrentFingerprint           string            `json:"current_fingerprint,omitempty"`
	BaselineInstallRef           string            `json:"baseline_install_ref,omitempty"`
	CurrentInstallRef            string            `json:"current_install_ref,omitempty"`
	BaselineManifestHash         string            `json:"baseline_manifest_hash,omitempty"`
	CurrentManifestHash          string            `json:"current_manifest_hash,omitempty"`
	BaselineWorkspaceFingerprint string            `json:"baseline_workspace_fingerprint,omitempty"`
	CurrentWorkspaceFingerprint  string            `json:"current_workspace_fingerprint,omitempty"`
	StaleReason                  string            `json:"stale_reason,omitempty"`
	DriftSource                  ToolDriftSource   `json:"drift_source,omitempty"`
	ConsecutiveFailures          int               `json:"consecutive_failures,omitempty"`
	CreatedAt                    time.Time         `json:"created_at,omitempty"`
	UpdatedAt                    time.Time         `json:"updated_at,omitempty"`
	AuditedAt                    time.Time         `json:"audited_at,omitempty"`
	LastFailureAt                time.Time         `json:"last_failure_at,omitempty"`
}

type ToolProbeRecord struct {
	ToolName                     string            `json:"tool_name"`
	Status                       ToolProbeStatus   `json:"status,omitempty"`
	ProbeOutput                  string            `json:"probe_output,omitempty"`
	Rationale                    string            `json:"rationale,omitempty"`
	ArtifactRefs                 []RecordReference `json:"artifact_refs,omitempty"`
	BaselineFingerprint          string            `json:"baseline_fingerprint,omitempty"`
	CurrentFingerprint           string            `json:"current_fingerprint,omitempty"`
	BaselineInstallRef           string            `json:"baseline_install_ref,omitempty"`
	CurrentInstallRef            string            `json:"current_install_ref,omitempty"`
	BaselineManifestHash         string            `json:"baseline_manifest_hash,omitempty"`
	CurrentManifestHash          string            `json:"current_manifest_hash,omitempty"`
	BaselineWorkspaceFingerprint string            `json:"baseline_workspace_fingerprint,omitempty"`
	CurrentWorkspaceFingerprint  string            `json:"current_workspace_fingerprint,omitempty"`
	StaleReason                  string            `json:"stale_reason,omitempty"`
	DriftSource                  ToolDriftSource   `json:"drift_source,omitempty"`
	ConsecutiveFailures          int               `json:"consecutive_failures,omitempty"`
	CreatedAt                    time.Time         `json:"created_at,omitempty"`
	UpdatedAt                    time.Time         `json:"updated_at,omitempty"`
	ProbedAt                     time.Time         `json:"probed_at,omitempty"`
	LastFailureAt                time.Time         `json:"last_failure_at,omitempty"`
}

type TurnAuthorizationKind string

const (
	TurnAuthorizationKindContinuation TurnAuthorizationKind = "continuation"
)

type TurnAuthorizationStatus string

const (
	TurnAuthorizationStatusIdle     TurnAuthorizationStatus = "idle"
	TurnAuthorizationStatusPending  TurnAuthorizationStatus = "pending"
	TurnAuthorizationStatusApproved TurnAuthorizationStatus = "approved"
	TurnAuthorizationStatusRevoked  TurnAuthorizationStatus = "revoked"
)

type ContinuationIntentDecision string

const (
	ContinuationIntentDecisionContinue ContinuationIntentDecision = "continue"
	ContinuationIntentDecisionStop     ContinuationIntentDecision = "stop"
	ContinuationIntentDecisionHold     ContinuationIntentDecision = "hold"
)

type ContinuationIntent struct {
	Decision    ContinuationIntentDecision `json:"decision,omitempty"`
	Rationale   string                     `json:"rationale,omitempty"`
	NextStep    string                     `json:"next_step,omitempty"`
	Constraints string                     `json:"constraints,omitempty"`
	Confidence  string                     `json:"confidence,omitempty"`
	Ratified    bool                       `json:"ratified,omitempty"`
	UpdatedAt   time.Time                  `json:"updated_at,omitempty"`
}

type TurnAuthorizationState struct {
	Kind                   TurnAuthorizationKind   `json:"kind,omitempty"`
	Status                 TurnAuthorizationStatus `json:"status,omitempty"`
	DecisionID             string                  `json:"decision_id,omitempty"`
	Objective              string                  `json:"objective,omitempty"`
	StageSummary           string                  `json:"stage_summary,omitempty"`
	RemainingTurns         int                     `json:"remaining_turns,omitempty"`
	ApprovedBy             int64                   `json:"approved_by,omitempty"`
	PersonaIntent          ContinuationIntent      `json:"persona_intent,omitempty"`
	GovernorIntent         ContinuationIntent      `json:"governor_intent,omitempty"`
	HandshakeBlockedReason string                  `json:"handshake_blocked_reason,omitempty"`
	UpdatedAt              time.Time               `json:"updated_at,omitempty"`
}

type ContinuationStatus = TurnAuthorizationStatus

const (
	ContinuationStatusIdle     = TurnAuthorizationStatusIdle
	ContinuationStatusPending  = TurnAuthorizationStatusPending
	ContinuationStatusApproved = TurnAuthorizationStatusApproved
	ContinuationStatusRevoked  = TurnAuthorizationStatusRevoked
)

type ContinuationState = TurnAuthorizationState

// SessionKey identifies a unique session.
type SessionKey struct {
	ChatID int64
	UserID int64 // 0 for shared group sessions or DMs
	Scope  ScopeRef
}

// Session stores conversation state, cache metadata, and accounting.
type Session struct {
	SessionID    string
	ChatID       int64
	UserID       int64 // 0 for shared group sessions
	Scope        ScopeRef
	Messages     []Message
	SystemPrompt string
	// LastFloorText stores the governor-owned floor text sidecar for audit.
	// The visible transcript in Messages stores the delivered scene text.
	LastFloorText     string
	LastFloorMetadata string
	CreatedAt         time.Time
	UpdatedAt         time.Time
	TurnCount         int

	// Cache tracking
	CacheState CacheState

	// Compaction
	CompactionLog []CompactionEntry

	// Planning
	PlanState PlanState

	// Operations
	OperationState OperationState

	// Continuation approval state
	ContinuationState ContinuationState

	// Token accounting (cumulative across all turns)
	TotalInputTokens  int64
	TotalOutputTokens int64
	TotalCacheRead    int64
	TotalCacheWrite   int64

	// Provider state
	LastProvider string
	LastModel    string

	// Agent state
	ActiveToolCalls int
	LastError       string

	// Chat metadata
	ChatType  string // "dm" or "group"
	ChatTitle string
	UserName  string
}

// CacheState tracks prompt cache behavior over time.
type CacheState struct {
	LastWriteBlock    int
	BlocksSinceWrite  int
	LastWriteTime     time.Time
	HitRate           float64
	ConsecutiveMisses int
}

// CompactionEntry records a single compaction event.
type CompactionEntry struct {
	Timestamp    time.Time
	TurnsBefore  int
	TurnsAfter   int
	TokensBefore int
	TokensAfter  int
	Summary      string
	Strategy     string // "summarize" or "truncate"
}

// ReviewEvent is a bounded digest from a source session to the admin DM.
type ReviewEvent struct {
	ID                int64
	SourceSessionID   string
	SourceChatID      int64
	SourceUserID      int64
	SourceRole        string
	SourceScope       ScopeRef
	TargetSessionID   string
	TargetAdminChatID int64
	TargetScope       ScopeRef
	TurnFrom          int
	TurnTo            int
	Summary           string
	MetadataJSON      string
	Status            string // "pending" | "delivered" | "dismissed"
	CreatedAt         time.Time
	DeliveredAt       time.Time
}

type DurableAgentPolicyUpdate struct {
	ID                  int64
	AgentID             string
	SourceReviewEventID int64
	PreviousVersion     int64
	NewVersion          int64
	PolicyHash          string
	PolicyJSON          string
	Reason              string
	AppliedAt           time.Time
}

type DurableAgentBootstrapUpdate struct {
	ID                  int64
	AgentID             string
	SourceReviewEventID int64
	ActorUserID         int64
	ActorRole           string
	UpdateKind          string
	PreviousBootstrap   core.NodeLLMBootstrap
	NewBootstrap        core.NodeLLMBootstrap
	Reason              string
	AppliedAt           time.Time
}

// TurnRun stores machine-authored facts about a turn lifecycle for recovery.
type TurnRun struct {
	ID                    int64
	SessionID             string
	ChatID                int64
	UserID                int64
	Scope                 ScopeRef
	Kind                  TurnRunKind
	Status                TurnRunStatus
	RequestText           string
	StartedAt             time.Time
	CompletedAt           time.Time
	LastActivityAt        time.Time
	LastToolName          string
	LastToolPreview       string
	ToolCallsStarted      int
	ToolCallsFinished     int
	LastToolResultPreview string
	LastToolError         string
	ProgressMessageID     int64
	ErrorText             string
	RecoverySummary       string
	RecoveryLoggedAt      time.Time
}

// ExecutionEvent stores one append-only event in the transparent execution sequence.
type ExecutionEvent struct {
	ID          int64
	SessionID   string
	ChatID      int64
	UserID      int64
	Scope       ScopeRef
	Seq         int64
	EventType   string
	Stage       string
	Status      string
	CausedBySeq int64
	PayloadJSON string
	CreatedAt   time.Time
}

// ExecutionEventInput is the write input for append-only execution events.
type ExecutionEventInput struct {
	EventType   string
	Stage       string
	Status      string
	CausedBySeq int64
	PayloadJSON string
	CreatedAt   time.Time
}

type RecordReference struct {
	Kind  string `json:"kind"`
	Ref   string `json:"ref"`
	Label string `json:"label,omitempty"`
}

func NormalizeRecordReferences(refs []RecordReference) []RecordReference {
	out := make([]RecordReference, 0, len(refs))
	for _, ref := range refs {
		kind := strings.TrimSpace(ref.Kind)
		value := strings.TrimSpace(ref.Ref)
		if kind == "" || value == "" {
			continue
		}
		out = append(out, RecordReference{Kind: kind, Ref: value, Label: strings.TrimSpace(ref.Label)})
	}
	return out
}

// PendingDecisionRecord persists broker decisions that are awaiting callback resolution.
type PendingDecisionRecord struct {
	ID                string
	Sequence          uint64
	OwnerKey          string
	Kind              string
	ChatID            int64
	SenderID          int64
	MessageID         int64
	Prompt            string
	Details           string
	Rationale         string
	ArtifactRefs      []RecordReference
	ChoicesJSON       string
	DefaultChoice     string
	TimeoutNanos      int64
	DeliveryMessageID int64
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

// PendingArtifactRetentionRecord persists inbound artifact context while a
// retention decision is outstanding so routing can resume asynchronously.
type PendingArtifactRetentionRecord struct {
	OwnerKey           string
	ChatID             int64
	SenderID           int64
	InboundMessageJSON string
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

// PendingBusyDecisionRecord persists inbound message context while a busy
// stop/queue decision is outstanding so routing can resume asynchronously.
type PendingBusyDecisionRecord struct {
	OwnerKey           string
	ChatID             int64
	SenderID           int64
	InboundMessageJSON string
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

type ContinuationStateRecord struct {
	Key       SessionKey
	State     ContinuationState
	UpdatedAt time.Time
}

type SessionStatusState struct {
	PlanState           PlanState
	OperationState      OperationState
	LastFloorMetadata   string
	TurnCount           int
	OutboundCountAtTurn int
}

// Message is one persisted conversation message.
type Message struct {
	ID                int64
	SessionID         string
	ChatID            int64
	UserID            int64
	ActorUserID       int64
	ActorRole         string
	EventOrigin       string
	EventOriginDetail string
	Role              string
	Content           string
	FloorContent      string
	FloorMetadata     string
	ToolCalls         string
	ToolID            string
	ToolName          string
	Thinking          string
	CreatedAt         time.Time
	TurnIndex         int
	ContentChars      int
	Compacted         bool
}

type TurnMessageContext struct {
	ActorUserID       int64
	ActorRole         string
	EventOrigin       string
	EventOriginDetail string
}

type SearchHit struct {
	SessionID    string
	ChatID       int64
	UserID       int64
	TurnIndex    int
	Role         string
	Content      string
	FloorContent string
	CreatedAt    time.Time
}

type ArtifactRecord struct {
	ArtifactID       string
	SessionID        string
	ChatID           int64
	UserID           int64
	TurnIndex        int
	SourceType       string
	Kind             string
	Summary          string
	Handling         string
	Retention        string
	FetchState       string
	MaterializedPath string
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

type RhizomeNode struct {
	ID         int64
	Scope      string
	Name       string
	EventCount int
	LastSeenAt time.Time
}

func NormalizePlanState(state PlanState) PlanState {
	state.Explanation = strings.TrimSpace(state.Explanation)
	steps := make([]PlanStep, 0, len(state.Steps))
	for _, step := range state.Steps {
		text := strings.TrimSpace(step.Step)
		if text == "" {
			continue
		}
		status := NormalizePlanStatus(step.Status)
		if status == "" {
			status = PlanStatusPending
		}
		steps = append(steps, PlanStep{
			Step:   text,
			Status: status,
		})
	}
	state.Steps = steps
	if state.UpdatedAt.IsZero() && (len(state.Steps) > 0 || state.Explanation != "") {
		state.UpdatedAt = time.Now().UTC()
	}
	return state
}

func (s PlanState) Active() bool {
	return len(NormalizePlanState(s).Steps) > 0
}

func (s PlanState) FormattedSteps() []string {
	normalized := NormalizePlanState(s)
	out := make([]string, 0, len(normalized.Steps))
	for _, step := range normalized.Steps {
		out = append(out, "["+string(step.Status)+"] "+step.Step)
	}
	return out
}

func NormalizePlanStatus(status PlanStatus) PlanStatus {
	value := strings.ToLower(strings.TrimSpace(string(status)))
	value = strings.ReplaceAll(value, "-", "_")
	value = strings.ReplaceAll(value, " ", "_")
	switch PlanStatus(value) {
	case PlanStatusPending, PlanStatusInProgress, PlanStatusCompleted:
		return PlanStatus(value)
	default:
		return ""
	}
}

func NormalizeOperationState(state OperationState) OperationState {
	state.ID = strings.TrimSpace(state.ID)
	state.Objective = strings.TrimSpace(state.Objective)
	state.Status = NormalizeOperationStatus(state.Status)
	state.Stage = normalizeOperationStage(state.Stage)
	state.Summary = strings.TrimSpace(state.Summary)
	state.Proposal = normalizeOperationProposal(state.Proposal)

	findings := make([]OperationFinding, 0, len(state.Findings))
	for _, finding := range state.Findings {
		claim := strings.TrimSpace(finding.Claim)
		if claim == "" {
			continue
		}
		confidence := NormalizeFindingConfidence(finding.Confidence)
		if confidence == "" {
			confidence = FindingConfidenceMedium
		}
		findings = append(findings, OperationFinding{
			Claim:      claim,
			Confidence: confidence,
			Basis:      strings.TrimSpace(finding.Basis),
		})
	}
	state.Findings = findings

	artifacts := make([]OperationArtifact, 0, len(state.Artifacts))
	for _, artifact := range state.Artifacts {
		ref := strings.TrimSpace(artifact.Ref)
		if ref == "" {
			continue
		}
		artifacts = append(artifacts, OperationArtifact{
			Label: strings.TrimSpace(artifact.Label),
			Ref:   ref,
		})
	}
	state.Artifacts = artifacts

	if state.UpdatedAt.IsZero() && state.Active() {
		state.UpdatedAt = time.Now().UTC()
	}
	return state
}

func (s OperationState) Active() bool {
	normalized := s
	return strings.TrimSpace(normalized.ID) != "" ||
		strings.TrimSpace(normalized.Objective) != "" ||
		strings.TrimSpace(string(normalized.Status)) != "" ||
		strings.TrimSpace(normalized.Stage) != "" ||
		strings.TrimSpace(normalized.Summary) != "" ||
		normalized.Proposal.Active() ||
		len(normalized.Findings) > 0 ||
		len(normalized.Artifacts) > 0
}

func (r CapabilityRequest) Active() bool {
	return strings.TrimSpace(r.RequestID) != "" ||
		strings.TrimSpace(r.RequestedBy) != "" ||
		strings.TrimSpace(r.RequestedFor) != "" ||
		strings.TrimSpace(r.ParentPrincipal) != "" ||
		strings.TrimSpace(r.AdminPrincipal) != "" ||
		strings.TrimSpace(string(r.Kind)) != "" ||
		strings.TrimSpace(r.TargetResource) != "" ||
		strings.TrimSpace(r.Purpose) != "" ||
		strings.TrimSpace(r.RiskClass) != "" ||
		strings.TrimSpace(r.Contract) != "" ||
		strings.TrimSpace(r.Constraints) != "" ||
		strings.TrimSpace(string(r.ReviewStatus)) != "" ||
		strings.TrimSpace(r.GrantID) != ""
}

func NormalizeCapabilityKind(kind CapabilityKind) CapabilityKind {
	value := normalizeEnumValue(string(kind))
	switch CapabilityKind(value) {
	case CapabilityKindTool,
		CapabilityKindLocalDevice,
		CapabilityKindExternalAccount,
		CapabilityKindPurchase,
		CapabilityKindPublicWeb,
		CapabilityKindCommunication,
		CapabilityKindFileAccess,
		CapabilityKindNetworkAccess,
		CapabilityKindGenericDelegation:
		return CapabilityKind(value)
	default:
		return ""
	}
}

func NormalizeCapabilityReviewStatus(status CapabilityReviewStatus) CapabilityReviewStatus {
	value := normalizeEnumValue(string(status))
	switch CapabilityReviewStatus(value) {
	case CapabilityReviewStatusProposed,
		CapabilityReviewStatusParentApproved,
		CapabilityReviewStatusApproved,
		CapabilityReviewStatusRejected:
		return CapabilityReviewStatus(value)
	default:
		return ""
	}
}

func NormalizeCapabilityGrantStatus(status CapabilityGrantStatus) CapabilityGrantStatus {
	value := normalizeEnumValue(string(status))
	switch CapabilityGrantStatus(value) {
	case CapabilityGrantStatusPending,
		CapabilityGrantStatusActive,
		CapabilityGrantStatusStale,
		CapabilityGrantStatusRevoked,
		CapabilityGrantStatusExpired,
		CapabilityGrantStatusFailed:
		return CapabilityGrantStatus(value)
	default:
		return ""
	}
}

func NormalizeCapabilityActions(actions []string) []string {
	seen := make(map[string]struct{}, len(actions))
	out := make([]string, 0, len(actions))
	for _, action := range actions {
		action = normalizeEnumValue(action)
		if action == "" {
			continue
		}
		if _, ok := seen[action]; ok {
			continue
		}
		seen[action] = struct{}{}
		out = append(out, action)
	}
	return out
}

func NormalizeDurableChildAgreementStatus(status DurableChildAgreementStatus) DurableChildAgreementStatus {
	switch DurableChildAgreementStatus(normalizeEnumValue(string(status))) {
	case DurableChildAgreementStatusProposed,
		DurableChildAgreementStatusApproved,
		DurableChildAgreementStatusRejected,
		DurableChildAgreementStatusSuperseded:
		return DurableChildAgreementStatus(normalizeEnumValue(string(status)))
	default:
		return ""
	}
}

func DurableChildAgreementStatusFromCapabilityReview(status CapabilityReviewStatus) DurableChildAgreementStatus {
	switch NormalizeCapabilityReviewStatus(status) {
	case CapabilityReviewStatusApproved:
		return DurableChildAgreementStatusApproved
	case CapabilityReviewStatusRejected:
		return DurableChildAgreementStatusRejected
	case CapabilityReviewStatusProposed, CapabilityReviewStatusParentApproved:
		return DurableChildAgreementStatusProposed
	default:
		return ""
	}
}

func NormalizeDurableChildAgreement(record DurableChildAgreement) DurableChildAgreement {
	record.AgreementID = strings.TrimSpace(record.AgreementID)
	record.AgentID = strings.TrimSpace(record.AgentID)
	record.ParentPrincipal = strings.TrimSpace(record.ParentPrincipal)
	record.ChildPrincipal = strings.TrimSpace(record.ChildPrincipal)
	record.SourceSurface = strings.TrimSpace(record.SourceSurface)
	record.SourceRequestID = strings.TrimSpace(record.SourceRequestID)
	record.Summary = strings.TrimSpace(record.Summary)
	record.BoundedEffect = strings.TrimSpace(record.BoundedEffect)
	record.Status = NormalizeDurableChildAgreementStatus(record.Status)
	record.ArtifactRefs = NormalizeRecordReferences(record.ArtifactRefs)
	if record.Status == "" && record.Active() {
		record.Status = DurableChildAgreementStatusProposed
	}
	if record.CreatedAt.IsZero() && record.Active() {
		record.CreatedAt = time.Now().UTC()
	}
	if record.UpdatedAt.IsZero() && record.Active() {
		record.UpdatedAt = time.Now().UTC()
	}
	return record
}

func (r DurableChildAgreement) Active() bool {
	return strings.TrimSpace(r.AgreementID) != "" ||
		strings.TrimSpace(r.AgentID) != "" ||
		strings.TrimSpace(r.ParentPrincipal) != "" ||
		strings.TrimSpace(r.ChildPrincipal) != "" ||
		strings.TrimSpace(r.SourceSurface) != "" ||
		strings.TrimSpace(r.SourceRequestID) != "" ||
		r.SourceReviewEventID != 0 ||
		strings.TrimSpace(r.Summary) != "" ||
		strings.TrimSpace(r.BoundedEffect) != "" ||
		strings.TrimSpace(string(r.Status)) != "" ||
		len(r.ArtifactRefs) > 0
}

func NormalizeCapabilityRequest(request CapabilityRequest) CapabilityRequest {
	request.RequestID = strings.TrimSpace(request.RequestID)
	request.RequestedBy = strings.TrimSpace(request.RequestedBy)
	request.RequestedFor = strings.TrimSpace(request.RequestedFor)
	request.ParentPrincipal = strings.TrimSpace(request.ParentPrincipal)
	request.AdminPrincipal = strings.TrimSpace(request.AdminPrincipal)
	request.Kind = NormalizeCapabilityKind(request.Kind)
	request.TargetResource = strings.TrimSpace(request.TargetResource)
	request.Purpose = strings.TrimSpace(request.Purpose)
	request.RiskClass = normalizeEnumValue(request.RiskClass)
	request.Contract = strings.TrimSpace(request.Contract)
	request.Constraints = strings.TrimSpace(request.Constraints)
	request.ReviewStatus = NormalizeCapabilityReviewStatus(request.ReviewStatus)
	request.GrantID = strings.TrimSpace(request.GrantID)
	if request.Kind == "" && request.Active() {
		request.Kind = CapabilityKindGenericDelegation
	}
	if request.ReviewStatus == "" && request.Active() {
		request.ReviewStatus = CapabilityReviewStatusProposed
	}
	if request.Contract == "" && request.Active() {
		request.Contract = "{}"
	}
	if request.Constraints == "" && request.Active() {
		request.Constraints = "{}"
	}
	if request.CreatedAt.IsZero() && request.Active() {
		request.CreatedAt = time.Now().UTC()
	}
	if request.UpdatedAt.IsZero() && request.Active() {
		request.UpdatedAt = time.Now().UTC()
	}
	return request
}

func NormalizeCapabilityReview(review CapabilityReview) CapabilityReview {
	review.ReviewID = strings.TrimSpace(review.ReviewID)
	review.RequestID = strings.TrimSpace(review.RequestID)
	review.Reviewer = strings.TrimSpace(review.Reviewer)
	review.ReviewerRole = normalizeEnumValue(review.ReviewerRole)
	review.Status = NormalizeCapabilityReviewStatus(review.Status)
	review.Rationale = strings.TrimSpace(review.Rationale)
	if review.CreatedAt.IsZero() && (review.ReviewID != "" || review.RequestID != "" || review.Reviewer != "" || review.Status != "" || review.Rationale != "") {
		review.CreatedAt = time.Now().UTC()
	}
	return review
}

func NormalizeCapabilityGrant(grant CapabilityGrant) CapabilityGrant {
	grant.GrantID = strings.TrimSpace(grant.GrantID)
	grant.RequestID = strings.TrimSpace(grant.RequestID)
	grant.GrantedBy = strings.TrimSpace(grant.GrantedBy)
	grant.GrantedTo = strings.TrimSpace(grant.GrantedTo)
	grant.Kind = NormalizeCapabilityKind(grant.Kind)
	grant.TargetResource = strings.TrimSpace(grant.TargetResource)
	grant.AllowedActions = NormalizeCapabilityActions(grant.AllowedActions)
	grant.Contract = strings.TrimSpace(grant.Contract)
	grant.Constraints = strings.TrimSpace(grant.Constraints)
	grant.Status = NormalizeCapabilityGrantStatus(grant.Status)
	grant.BaselinePolicyHash = strings.TrimSpace(grant.BaselinePolicyHash)
	grant.CurrentPolicyHash = strings.TrimSpace(grant.CurrentPolicyHash)
	grant.AnchorFingerprint = strings.TrimSpace(grant.AnchorFingerprint)
	grant.DriftSource = ToolDriftSource(strings.TrimSpace(string(grant.DriftSource)))
	grant.StaleReason = strings.TrimSpace(grant.StaleReason)
	if grant.Kind == "" && grant.GrantID != "" {
		grant.Kind = CapabilityKindGenericDelegation
	}
	if len(grant.AllowedActions) == 0 && grant.GrantID != "" {
		grant.AllowedActions = []string{"invoke"}
	}
	if grant.Status == "" && grant.GrantID != "" {
		grant.Status = CapabilityGrantStatusPending
	}
	if grant.Contract == "" && grant.GrantID != "" {
		grant.Contract = "{}"
	}
	if grant.Constraints == "" && grant.GrantID != "" {
		grant.Constraints = "{}"
	}
	if grant.CreatedAt.IsZero() && grant.GrantID != "" {
		grant.CreatedAt = time.Now().UTC()
	}
	if grant.UpdatedAt.IsZero() && grant.GrantID != "" {
		grant.UpdatedAt = time.Now().UTC()
	}
	if grant.GrantedAt.IsZero() && grant.Status == CapabilityGrantStatusActive {
		grant.GrantedAt = grant.UpdatedAt
	}
	return grant
}

func NormalizeCapabilityInvocation(invocation CapabilityInvocation) CapabilityInvocation {
	invocation.GrantID = strings.TrimSpace(invocation.GrantID)
	invocation.Principal = strings.TrimSpace(invocation.Principal)
	invocation.Action = normalizeEnumValue(invocation.Action)
	invocation.Status = normalizeEnumValue(invocation.Status)
	invocation.ErrorText = strings.TrimSpace(invocation.ErrorText)
	if invocation.CreatedAt.IsZero() && invocation.GrantID != "" {
		invocation.CreatedAt = time.Now().UTC()
	}
	return invocation
}

func NormalizeRegisteredTool(tool RegisteredTool) RegisteredTool {
	tool.ToolName = strings.TrimSpace(tool.ToolName)
	tool.ImplementationRef = strings.TrimSpace(tool.ImplementationRef)
	if tool.CreatedAt.IsZero() && tool.ToolName != "" {
		tool.CreatedAt = time.Now().UTC()
	}
	if tool.UpdatedAt.IsZero() && tool.ToolName != "" {
		tool.UpdatedAt = time.Now().UTC()
	}
	return tool
}

func NormalizeToolInstallStatus(status ToolInstallStatus) ToolInstallStatus {
	switch ToolInstallStatus(strings.TrimSpace(string(status))) {
	case ToolInstallStatusPending:
		return ToolInstallStatusPending
	case ToolInstallStatusInstalled:
		return ToolInstallStatusInstalled
	case ToolInstallStatusVerified:
		return ToolInstallStatusVerified
	case ToolInstallStatusFailed:
		return ToolInstallStatusFailed
	case ToolInstallStatusStale:
		return ToolInstallStatusStale
	default:
		return ""
	}
}

func NormalizeToolProbeStatus(status ToolProbeStatus) ToolProbeStatus {
	switch ToolProbeStatus(strings.TrimSpace(string(status))) {
	case ToolProbeStatusPassed:
		return ToolProbeStatusPassed
	case ToolProbeStatusFailed:
		return ToolProbeStatusFailed
	default:
		return ""
	}
}

func NormalizeToolAuditStatus(status ToolAuditStatus) ToolAuditStatus {
	switch ToolAuditStatus(strings.TrimSpace(string(status))) {
	case ToolAuditStatusPassed:
		return ToolAuditStatusPassed
	case ToolAuditStatusFailed:
		return ToolAuditStatusFailed
	default:
		return ""
	}
}

func NormalizeToolInstallRecord(record ToolInstallRecord) ToolInstallRecord {
	record.ToolName = strings.TrimSpace(record.ToolName)
	record.Installer = strings.TrimSpace(record.Installer)
	record.InstallRef = strings.TrimSpace(record.InstallRef)
	record.ProbeOutput = strings.TrimSpace(record.ProbeOutput)
	record.Rationale = strings.TrimSpace(record.Rationale)
	record.BaselineFingerprint = strings.TrimSpace(record.BaselineFingerprint)
	record.CurrentFingerprint = strings.TrimSpace(record.CurrentFingerprint)
	record.BaselineInstallRef = strings.TrimSpace(record.BaselineInstallRef)
	record.CurrentInstallRef = strings.TrimSpace(record.CurrentInstallRef)
	record.BaselineManifestHash = strings.TrimSpace(record.BaselineManifestHash)
	record.CurrentManifestHash = strings.TrimSpace(record.CurrentManifestHash)
	record.BaselineWorkspaceFingerprint = strings.TrimSpace(record.BaselineWorkspaceFingerprint)
	record.CurrentWorkspaceFingerprint = strings.TrimSpace(record.CurrentWorkspaceFingerprint)
	record.StaleReason = strings.TrimSpace(record.StaleReason)
	record.DriftSource = ToolDriftSource(strings.TrimSpace(string(record.DriftSource)))
	record.ArtifactRefs = NormalizeRecordReferences(record.ArtifactRefs)
	record.Status = NormalizeToolInstallStatus(record.Status)
	record.ProbeStatus = NormalizeToolProbeStatus(record.ProbeStatus)
	if record.CreatedAt.IsZero() && record.ToolName != "" {
		record.CreatedAt = time.Now().UTC()
	}
	if record.UpdatedAt.IsZero() && record.ToolName != "" {
		record.UpdatedAt = time.Now().UTC()
	}
	return record
}

func NormalizeToolAuditRecord(record ToolAuditRecord) ToolAuditRecord {
	record.ToolName = strings.TrimSpace(record.ToolName)
	record.Status = NormalizeToolAuditStatus(record.Status)
	record.AuditOutput = strings.TrimSpace(record.AuditOutput)
	record.Rationale = strings.TrimSpace(record.Rationale)
	record.BaselineFingerprint = strings.TrimSpace(record.BaselineFingerprint)
	record.CurrentFingerprint = strings.TrimSpace(record.CurrentFingerprint)
	record.BaselineInstallRef = strings.TrimSpace(record.BaselineInstallRef)
	record.CurrentInstallRef = strings.TrimSpace(record.CurrentInstallRef)
	record.BaselineManifestHash = strings.TrimSpace(record.BaselineManifestHash)
	record.CurrentManifestHash = strings.TrimSpace(record.CurrentManifestHash)
	record.BaselineWorkspaceFingerprint = strings.TrimSpace(record.BaselineWorkspaceFingerprint)
	record.CurrentWorkspaceFingerprint = strings.TrimSpace(record.CurrentWorkspaceFingerprint)
	record.StaleReason = strings.TrimSpace(record.StaleReason)
	record.DriftSource = ToolDriftSource(strings.TrimSpace(string(record.DriftSource)))
	record.ArtifactRefs = NormalizeRecordReferences(record.ArtifactRefs)
	if record.ConsecutiveFailures < 0 {
		record.ConsecutiveFailures = 0
	}
	if record.CreatedAt.IsZero() && record.ToolName != "" {
		record.CreatedAt = time.Now().UTC()
	}
	if record.UpdatedAt.IsZero() && record.ToolName != "" {
		record.UpdatedAt = time.Now().UTC()
	}
	return record
}

func NormalizeToolProbeRecord(record ToolProbeRecord) ToolProbeRecord {
	record.ToolName = strings.TrimSpace(record.ToolName)
	record.Status = NormalizeToolProbeStatus(record.Status)
	record.ProbeOutput = strings.TrimSpace(record.ProbeOutput)
	record.Rationale = strings.TrimSpace(record.Rationale)
	record.BaselineFingerprint = strings.TrimSpace(record.BaselineFingerprint)
	record.CurrentFingerprint = strings.TrimSpace(record.CurrentFingerprint)
	record.BaselineInstallRef = strings.TrimSpace(record.BaselineInstallRef)
	record.CurrentInstallRef = strings.TrimSpace(record.CurrentInstallRef)
	record.BaselineManifestHash = strings.TrimSpace(record.BaselineManifestHash)
	record.CurrentManifestHash = strings.TrimSpace(record.CurrentManifestHash)
	record.BaselineWorkspaceFingerprint = strings.TrimSpace(record.BaselineWorkspaceFingerprint)
	record.CurrentWorkspaceFingerprint = strings.TrimSpace(record.CurrentWorkspaceFingerprint)
	record.StaleReason = strings.TrimSpace(record.StaleReason)
	record.DriftSource = ToolDriftSource(strings.TrimSpace(string(record.DriftSource)))
	record.ArtifactRefs = NormalizeRecordReferences(record.ArtifactRefs)
	if record.ConsecutiveFailures < 0 {
		record.ConsecutiveFailures = 0
	}
	if record.CreatedAt.IsZero() && record.ToolName != "" {
		record.CreatedAt = time.Now().UTC()
	}
	if record.UpdatedAt.IsZero() && record.ToolName != "" {
		record.UpdatedAt = time.Now().UTC()
	}
	return record
}

func NormalizeTurnAuthorizationState(state TurnAuthorizationState) TurnAuthorizationState {
	state.Kind = TurnAuthorizationKind(strings.TrimSpace(string(state.Kind)))
	state.Status = TurnAuthorizationStatus(strings.TrimSpace(string(state.Status)))
	state.DecisionID = strings.TrimSpace(state.DecisionID)
	state.Objective = strings.TrimSpace(state.Objective)
	state.StageSummary = strings.TrimSpace(state.StageSummary)
	state.PersonaIntent = normalizeContinuationIntent(state.PersonaIntent)
	state.GovernorIntent = normalizeContinuationIntent(state.GovernorIntent)
	state.HandshakeBlockedReason = normalizeContinuationStage(state.HandshakeBlockedReason)
	if state.Kind == "" && (state.Status != "" || state.DecisionID != "" || state.Objective != "" || state.StageSummary != "" || state.RemainingTurns > 0 || state.ApprovedBy > 0) {
		state.Kind = TurnAuthorizationKindContinuation
	}
	if state.RemainingTurns < 0 {
		state.RemainingTurns = 0
	}
	if state.Status == TurnAuthorizationStatusIdle || state.Status == TurnAuthorizationStatusRevoked {
		state.ApprovedBy = 0
		state.DecisionID = ""
	}
	if state.UpdatedAt.IsZero() && (state.Kind != "" || state.Status != "" || state.DecisionID != "" || state.Objective != "" || state.StageSummary != "" || state.RemainingTurns > 0 || state.ApprovedBy > 0) {
		state.UpdatedAt = time.Now().UTC()
	}
	return state
}

func NormalizeContinuationState(state ContinuationState) ContinuationState {
	if strings.TrimSpace(string(state.Kind)) == "" {
		state.Kind = TurnAuthorizationKindContinuation
	}
	return NormalizeTurnAuthorizationState(state)
}

func (s TurnAuthorizationState) Active() bool {
	state := NormalizeTurnAuthorizationState(s)
	return state.Status == TurnAuthorizationStatusPending || state.Status == TurnAuthorizationStatusApproved
}

func (p OperationProposal) Active() bool {
	return strings.TrimSpace(p.ID) != "" ||
		strings.TrimSpace(p.Kind) != "" ||
		strings.TrimSpace(p.Summary) != "" ||
		strings.TrimSpace(p.WhyNow) != "" ||
		strings.TrimSpace(p.BoundedEffect) != "" ||
		strings.TrimSpace(string(p.Status)) != ""
}

func NormalizeOperationStatus(status OperationStatus) OperationStatus {
	value := normalizeEnumValue(string(status))
	switch OperationStatus(value) {
	case OperationStatusIdle, OperationStatusActive, OperationStatusBlocked, OperationStatusCompleted, OperationStatusFailed:
		return OperationStatus(value)
	default:
		return ""
	}
}

func NormalizeProposalStatus(status ProposalStatus) ProposalStatus {
	value := normalizeEnumValue(string(status))
	switch ProposalStatus(value) {
	case ProposalStatusPending, ProposalStatusApproved, ProposalStatusDenied, ProposalStatusExpired, ProposalStatusSuperseded:
		return ProposalStatus(value)
	default:
		return ""
	}
}

func NormalizeFindingConfidence(confidence FindingConfidence) FindingConfidence {
	value := normalizeEnumValue(string(confidence))
	switch FindingConfidence(value) {
	case FindingConfidenceLow, FindingConfidenceMedium, FindingConfidenceHigh:
		return FindingConfidence(value)
	default:
		return ""
	}
}

func normalizeOperationProposal(proposal OperationProposal) OperationProposal {
	proposal.ID = strings.TrimSpace(proposal.ID)
	proposal.Kind = normalizeOperationStage(proposal.Kind)
	proposal.Summary = strings.TrimSpace(proposal.Summary)
	proposal.WhyNow = strings.TrimSpace(proposal.WhyNow)
	proposal.BoundedEffect = strings.TrimSpace(proposal.BoundedEffect)
	proposal.Status = NormalizeProposalStatus(proposal.Status)
	if proposal.Status == "" && proposal.Active() {
		proposal.Status = ProposalStatusPending
	}
	if proposal.UpdatedAt.IsZero() && proposal.Active() {
		proposal.UpdatedAt = time.Now().UTC()
	}
	return proposal
}

func normalizeOperationStage(stage string) string {
	return normalizeEnumValue(stage)
}

func normalizeContinuationIntent(intent ContinuationIntent) ContinuationIntent {
	intent.Decision = normalizeContinuationIntentDecision(intent.Decision)
	intent.Rationale = strings.TrimSpace(intent.Rationale)
	intent.NextStep = strings.TrimSpace(intent.NextStep)
	intent.Constraints = strings.TrimSpace(intent.Constraints)
	intent.Confidence = normalizeContinuationConfidence(intent.Confidence)
	if intent.UpdatedAt.IsZero() && (intent.Decision != "" || intent.Rationale != "" || intent.NextStep != "" || intent.Constraints != "" || intent.Confidence != "" || intent.Ratified) {
		intent.UpdatedAt = time.Now().UTC()
	}
	return intent
}

func normalizeContinuationIntentDecision(decision ContinuationIntentDecision) ContinuationIntentDecision {
	switch normalizeEnumValue(string(decision)) {
	case string(ContinuationIntentDecisionContinue):
		return ContinuationIntentDecisionContinue
	case string(ContinuationIntentDecisionStop):
		return ContinuationIntentDecisionStop
	case string(ContinuationIntentDecisionHold):
		return ContinuationIntentDecisionHold
	default:
		return ""
	}
}

func normalizeContinuationConfidence(confidence string) string {
	switch normalizeEnumValue(confidence) {
	case "low", "medium", "high":
		return normalizeEnumValue(confidence)
	default:
		return ""
	}
}

func normalizeContinuationStage(value string) string {
	return normalizeEnumValue(value)
}

func normalizeEnumValue(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, "-", "_")
	value = strings.ReplaceAll(value, " ", "_")
	return value
}

type RhizomeEvent struct {
	ID        int64
	Scope     string
	Source    string
	Salience  float64
	Concepts  []string
	CreatedAt time.Time
}

type RhizomeEdge struct {
	ID               int64
	Scope            string
	LeftConcept      string
	RightConcept     string
	Strength         float64
	RecurrenceCount  int
	LastReinforcedAt time.Time
	DecayState       string
	LastSource       string
}
