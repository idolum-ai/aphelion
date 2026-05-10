//go:build linux

package session

import (
	"fmt"
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
	TurnRunKindDoctor      TurnRunKind = "doctor"
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
	OperatorTitle string         `json:"operator_title,omitempty"`
	PlanTitle     string         `json:"plan_title,omitempty"`
	Summary       string         `json:"summary,omitempty"`
	WhyNow        string         `json:"why_now,omitempty"`
	BoundedEffect string         `json:"bounded_effect,omitempty"`
	Status        ProposalStatus `json:"status,omitempty"`
	UpdatedAt     time.Time      `json:"updated_at,omitempty"`
}

type OperationPhase struct {
	ID                  string     `json:"id,omitempty"`
	OperatorTitle       string     `json:"operator_title,omitempty"`
	PlanTitle           string     `json:"plan_title,omitempty"`
	Summary             string     `json:"summary,omitempty"`
	Status              PlanStatus `json:"status,omitempty"`
	AuthorityClass      string     `json:"authority_class,omitempty"`
	WhyNow              string     `json:"why_now,omitempty"`
	BoundedEffect       string     `json:"bounded_effect,omitempty"`
	AllowedActions      []string   `json:"allowed_actions,omitempty"`
	ForbiddenActions    []string   `json:"forbidden_actions,omitempty"`
	ValidationPlan      []string   `json:"validation_plan,omitempty"`
	GateLevel           string     `json:"gate_level,omitempty"`
	GateReasonCode      string     `json:"gate_reason_code,omitempty"`
	ApprovalSubject     string     `json:"approval_subject,omitempty"`
	AutoApproveEligible *bool      `json:"autoapprove_eligible,omitempty"`
	BlockedReasonCode   string     `json:"blocked_reason_code,omitempty"`
	RequiresConsent     bool       `json:"requires_consent,omitempty"`
	RequiresOptIn       bool       `json:"requires_opt_in,omitempty"`
	SupersedesPhaseIDs  []string   `json:"supersedes_phase_ids,omitempty"`
	StaleAuthority      bool       `json:"stale_authority,omitempty"`
	RequiresApproval    bool       `json:"requires_approval,omitempty"`
	LeaseID             string     `json:"lease_id,omitempty"`
	CompletedAt         time.Time  `json:"completed_at,omitempty"`
}

type OperationPhasePlan struct {
	ID             string           `json:"id,omitempty"`
	Goal           string           `json:"goal,omitempty"`
	CurrentPhaseID string           `json:"current_phase_id,omitempty"`
	Phases         []OperationPhase `json:"phases,omitempty"`
	UpdatedAt      time.Time        `json:"updated_at,omitempty"`
}

type ActionProposal struct {
	ID                  string         `json:"id,omitempty"`
	OperationID         string         `json:"operation_id,omitempty"`
	MissionID           string         `json:"mission_id,omitempty"`
	OperatorTitle       string         `json:"operator_title,omitempty"`
	PlanTitle           string         `json:"plan_title,omitempty"`
	Summary             string         `json:"summary,omitempty"`
	WhyNow              string         `json:"why_now,omitempty"`
	BoundedEffect       string         `json:"bounded_effect,omitempty"`
	RiskClass           string         `json:"risk_class,omitempty"`
	AllowedActions      []string       `json:"allowed_actions,omitempty"`
	ForbiddenActions    []string       `json:"forbidden_actions,omitempty"`
	ValidationPlan      []string       `json:"validation_plan,omitempty"`
	AutoApproveEligible *bool          `json:"autoapprove_eligible,omitempty"`
	ExpiresAt           time.Time      `json:"expires_at,omitempty"`
	PlanHash            string         `json:"plan_hash,omitempty"`
	Status              ProposalStatus `json:"status,omitempty"`
	CreatedAt           time.Time      `json:"created_at,omitempty"`
	UpdatedAt           time.Time      `json:"updated_at,omitempty"`
}

type ContinuationLeaseStatus string

const (
	ContinuationLeaseStatusPending  ContinuationLeaseStatus = "pending"
	ContinuationLeaseStatusActive   ContinuationLeaseStatus = "active"
	ContinuationLeaseStatusConsumed ContinuationLeaseStatus = "consumed"
	ContinuationLeaseStatusRevoked  ContinuationLeaseStatus = "revoked"
	ContinuationLeaseStatusExpired  ContinuationLeaseStatus = "expired"
)

type ContinuationLeaseClass string

const (
	ContinuationLeaseClassLocalWorkspace  ContinuationLeaseClass = "local_workspace"
	ContinuationLeaseClassDataAccess      ContinuationLeaseClass = "data_access"
	ContinuationLeaseClassChildWake       ContinuationLeaseClass = "child_wake"
	ContinuationLeaseClassCapabilityGrant ContinuationLeaseClass = "capability_grant"
	ContinuationLeaseClassDeployRestart   ContinuationLeaseClass = "deploy_restart"
)

type ContinuationLease struct {
	ID               string                  `json:"id,omitempty"`
	ProposalID       string                  `json:"proposal_id,omitempty"`
	MissionID        string                  `json:"mission_id,omitempty"`
	OperatorTitle    string                  `json:"operator_title,omitempty"`
	PlanTitle        string                  `json:"plan_title,omitempty"`
	Status           ContinuationLeaseStatus `json:"status,omitempty"`
	MaxTurns         int                     `json:"max_turns,omitempty"`
	RemainingTurns   int                     `json:"remaining_turns,omitempty"`
	ApprovedBy       int64                   `json:"approved_by,omitempty"`
	LeaseClass       ContinuationLeaseClass  `json:"lease_class,omitempty"`
	Constraints      map[string]string       `json:"constraints,omitempty"`
	AllowedActions   []string                `json:"allowed_actions,omitempty"`
	ForbiddenActions []string                `json:"forbidden_actions,omitempty"`
	ValidationPlan   []string                `json:"validation_plan,omitempty"`
	ExpiresAt        time.Time               `json:"expires_at,omitempty"`
	PlanHash         string                  `json:"plan_hash,omitempty"`
	CreatedAt        time.Time               `json:"created_at,omitempty"`
	UpdatedAt        time.Time               `json:"updated_at,omitempty"`
	ApprovedAt       time.Time               `json:"approved_at,omitempty"`
	ConsumedAt       time.Time               `json:"consumed_at,omitempty"`
	RevokedAt        time.Time               `json:"revoked_at,omitempty"`
}

type ContinuationApprovalBundlePhase struct {
	ID               string                  `json:"id,omitempty"`
	OperationPhaseID string                  `json:"operation_phase_id,omitempty"`
	Index            int                     `json:"index,omitempty"`
	OperatorTitle    string                  `json:"operator_title,omitempty"`
	PlanTitle        string                  `json:"plan_title,omitempty"`
	Summary          string                  `json:"summary,omitempty"`
	AuthorityClass   string                  `json:"authority_class,omitempty"`
	WhyNow           string                  `json:"why_now,omitempty"`
	BoundedEffect    string                  `json:"bounded_effect,omitempty"`
	AllowedActions   []string                `json:"allowed_actions,omitempty"`
	ForbiddenActions []string                `json:"forbidden_actions,omitempty"`
	ValidationPlan   []string                `json:"validation_plan,omitempty"`
	Status           ContinuationLeaseStatus `json:"status,omitempty"`
}

type ContinuationApprovalBundle struct {
	ID             string                            `json:"id,omitempty"`
	Status         ContinuationLeaseStatus           `json:"status,omitempty"`
	CurrentPhaseID string                            `json:"current_phase_id,omitempty"`
	ApprovedBy     int64                             `json:"approved_by,omitempty"`
	Phases         []ContinuationApprovalBundlePhase `json:"phases,omitempty"`
	ExpiresAt      time.Time                         `json:"expires_at,omitempty"`
	CreatedAt      time.Time                         `json:"created_at,omitempty"`
	UpdatedAt      time.Time                         `json:"updated_at,omitempty"`
	ApprovedAt     time.Time                         `json:"approved_at,omitempty"`
	ConsumedAt     time.Time                         `json:"consumed_at,omitempty"`
	RevokedAt      time.Time                         `json:"revoked_at,omitempty"`
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

type PlanLeaseStatus string

const (
	PlanLeaseStatusProposed  PlanLeaseStatus = "proposed"
	PlanLeaseStatusApproved  PlanLeaseStatus = "approved"
	PlanLeaseStatusActive    PlanLeaseStatus = "active"
	PlanLeaseStatusPaused    PlanLeaseStatus = "paused"
	PlanLeaseStatusRevoked   PlanLeaseStatus = "revoked"
	PlanLeaseStatusExpired   PlanLeaseStatus = "expired"
	PlanLeaseStatusCompleted PlanLeaseStatus = "completed"
)

type OperationPlanLeaseLane struct {
	ID               string   `json:"id,omitempty"`
	OperatorTitle    string   `json:"operator_title,omitempty"`
	PlanTitle        string   `json:"plan_title,omitempty"`
	Summary          string   `json:"summary,omitempty"`
	AuthorityClass   string   `json:"authority_class,omitempty"`
	ExpectedTurns    int      `json:"expected_turns,omitempty"`
	AllowedActions   []string `json:"allowed_actions,omitempty"`
	ForbiddenActions []string `json:"forbidden_actions,omitempty"`
}

type OperationPlanLeaseEvidenceDigest struct {
	TurnsSpent         int       `json:"turns_spent,omitempty"`
	LanesUsed          []string  `json:"lanes_used,omitempty"`
	Completed          []string  `json:"completed,omitempty"`
	Blocked            []string  `json:"blocked,omitempty"`
	InterruptsRaised   []string  `json:"interrupts_raised,omitempty"`
	EvidenceRefs       []string  `json:"evidence_refs,omitempty"`
	ChangesMade        []string  `json:"changes_made,omitempty"`
	ResidualRisk       string    `json:"residual_risk,omitempty"`
	SuggestedNextLease string    `json:"suggested_next_lease,omitempty"`
	UpdatedAt          time.Time `json:"updated_at,omitempty"`
}

type OperationPlanLease struct {
	ID                   string                           `json:"id,omitempty"`
	OperatorTitle        string                           `json:"operator_title,omitempty"`
	PlanTitle            string                           `json:"plan_title,omitempty"`
	Summary              string                           `json:"summary,omitempty"`
	Objective            string                           `json:"objective,omitempty"`
	MissionID            string                           `json:"mission_id,omitempty"`
	OperationID          string                           `json:"operation_id,omitempty"`
	Status               PlanLeaseStatus                  `json:"status,omitempty"`
	TurnBudget           int                              `json:"turn_budget,omitempty"`
	RemainingTurns       int                              `json:"remaining_turns,omitempty"`
	CoveredPhaseIDs      []string                         `json:"covered_phase_ids,omitempty"`
	ExpiresAt            time.Time                        `json:"expires_at,omitempty"`
	Lanes                []OperationPlanLeaseLane         `json:"lanes,omitempty"`
	AllowedActions       []string                         `json:"allowed_actions,omitempty"`
	ForbiddenActions     []string                         `json:"forbidden_actions,omitempty"`
	ValidationGates      []string                         `json:"validation_gates,omitempty"`
	ExitConditions       []string                         `json:"exit_conditions,omitempty"`
	HardInterrupts       []string                         `json:"hard_interrupts,omitempty"`
	ChildInitiationLanes []string                         `json:"child_initiation_lanes,omitempty"`
	EvidenceDigest       OperationPlanLeaseEvidenceDigest `json:"evidence_digest,omitempty"`
	ApprovedBy           int64                            `json:"approved_by,omitempty"`
	ApprovedAt           time.Time                        `json:"approved_at,omitempty"`
	CreatedAt            time.Time                        `json:"created_at,omitempty"`
	UpdatedAt            time.Time                        `json:"updated_at,omitempty"`
}

type WorkCodexEvent struct {
	Kind     string `json:"kind,omitempty"`
	Method   string `json:"method,omitempty"`
	Status   string `json:"status,omitempty"`
	Subject  string `json:"subject,omitempty"`
	Path     string `json:"path,omitempty"`
	Command  string `json:"command,omitempty"`
	Preview  string `json:"preview,omitempty"`
	ThreadID string `json:"thread_id,omitempty"`
	TurnID   string `json:"turn_id,omitempty"`
	AgentID  string `json:"agent_id,omitempty"`
	Server   string `json:"server,omitempty"`
	Tool     string `json:"tool,omitempty"`
}

type WorkOperationMetadata struct {
	Executor              string           `json:"executor,omitempty"`
	ConfiguredExecutor    string           `json:"configured_executor,omitempty"`
	PreferredExecutor     string           `json:"preferred_executor,omitempty"`
	FallbackReason        string           `json:"fallback_reason,omitempty"`
	CodexThreadID         string           `json:"codex_thread_id,omitempty"`
	CodexLastTurnID       string           `json:"codex_last_turn_id,omitempty"`
	CodexLaneMode         string           `json:"codex_lane_mode,omitempty"`
	RepoRoot              string           `json:"repo_root,omitempty"`
	Workdir               string           `json:"workdir,omitempty"`
	ChangedFiles          []string         `json:"changed_files,omitempty"`
	Commands              []string         `json:"commands,omitempty"`
	CodexEvents           []WorkCodexEvent `json:"codex_events,omitempty"`
	PatchPreview          string           `json:"patch_preview,omitempty"`
	CommitLaneStatus      string           `json:"commit_lane_status,omitempty"`
	LastSummary           string           `json:"last_summary,omitempty"`
	LastError             string           `json:"last_error,omitempty"`
	PendingCodexApproval  string           `json:"pending_codex_approval,omitempty"`
	LastCompletedAt       time.Time        `json:"last_completed_at,omitempty"`
	LastExecutorUpdatedAt time.Time        `json:"last_executor_updated_at,omitempty"`
}

type OperationState struct {
	ID        string                `json:"id,omitempty"`
	Objective string                `json:"objective,omitempty"`
	Status    OperationStatus       `json:"status,omitempty"`
	Stage     string                `json:"stage,omitempty"`
	Summary   string                `json:"summary,omitempty"`
	Proposal  OperationProposal     `json:"proposal,omitempty"`
	PhasePlan OperationPhasePlan    `json:"phase_plan,omitempty"`
	PlanLease OperationPlanLease    `json:"plan_lease,omitempty"`
	Findings  []OperationFinding    `json:"findings,omitempty"`
	Artifacts []OperationArtifact   `json:"artifacts,omitempty"`
	Work      WorkOperationMetadata `json:"work,omitempty"`
	UpdatedAt time.Time             `json:"updated_at,omitempty"`
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
	CapabilityKindSystemChange      CapabilityKind = "system_change"
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
	InvocationID         int64     `json:"invocation_id,omitempty"`
	GrantID              string    `json:"grant_id"`
	Principal            string    `json:"principal,omitempty"`
	Action               string    `json:"action,omitempty"`
	Status               string    `json:"status,omitempty"`
	ErrorText            string    `json:"error_text,omitempty"`
	SessionID            string    `json:"session_id,omitempty"`
	TurnRunID            int64     `json:"turn_run_id,omitempty"`
	ContinuationLeaseID  string    `json:"continuation_lease_id,omitempty"`
	OperationPlanLeaseID string    `json:"operation_plan_lease_id,omitempty"`
	AuthoritySource      string    `json:"authority_source,omitempty"`
	CreatedAt            time.Time `json:"created_at,omitempty"`
}

type AuthorityUseRef struct {
	SessionID            string `json:"session_id,omitempty"`
	TurnRunID            int64  `json:"turn_run_id,omitempty"`
	ContinuationLeaseID  string `json:"continuation_lease_id,omitempty"`
	OperationPlanLeaseID string `json:"operation_plan_lease_id,omitempty"`
	AuthoritySource      string `json:"authority_source,omitempty"`
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
	Kind                   TurnAuthorizationKind      `json:"kind,omitempty"`
	Status                 TurnAuthorizationStatus    `json:"status,omitempty"`
	DecisionID             string                     `json:"decision_id,omitempty"`
	Objective              string                     `json:"objective,omitempty"`
	StageSummary           string                     `json:"stage_summary,omitempty"`
	RemainingTurns         int                        `json:"remaining_turns,omitempty"`
	ApprovedBy             int64                      `json:"approved_by,omitempty"`
	PersonaIntent          ContinuationIntent         `json:"persona_intent,omitempty"`
	GovernorIntent         ContinuationIntent         `json:"governor_intent,omitempty"`
	ActionProposal         ActionProposal             `json:"action_proposal,omitempty"`
	ContinuationLease      ContinuationLease          `json:"continuation_lease,omitempty"`
	ApprovalBundle         ContinuationApprovalBundle `json:"approval_bundle,omitempty"`
	HandshakeBlockedReason string                     `json:"handshake_blocked_reason,omitempty"`
	ParkedAt               time.Time                  `json:"parked_at,omitempty"`
	ParkedReason           string                     `json:"parked_reason,omitempty"`
	ParkedSource           string                     `json:"parked_source,omitempty"`
	UpdatedAt              time.Time                  `json:"updated_at,omitempty"`
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

const (
	OperatorAutoApprovalScopeAll       = "all"
	OperatorAutoApprovalScopeWorkspace = "workspace"
	OperatorAutoApprovalScopeDeploy    = "deploy"
)

type OperatorAutoApprovalLease struct {
	ID          string
	AdminUserID int64
	ChatID      int64
	Scope       string
	Reason      string
	MaxUses     int
	UsedCount   int
	CreatedAt   time.Time
	ExpiresAt   time.Time
	RevokedAt   time.Time
	UpdatedAt   time.Time
}

func NormalizeOperatorAutoApprovalScope(scope string) string {
	switch normalizeEnumValue(scope) {
	case OperatorAutoApprovalScopeWorkspace:
		return OperatorAutoApprovalScopeWorkspace
	case OperatorAutoApprovalScopeDeploy:
		return OperatorAutoApprovalScopeDeploy
	default:
		return OperatorAutoApprovalScopeAll
	}
}

func NormalizeOperatorAutoApprovalLease(lease OperatorAutoApprovalLease) OperatorAutoApprovalLease {
	lease.ID = strings.TrimSpace(lease.ID)
	lease.Scope = NormalizeOperatorAutoApprovalScope(lease.Scope)
	lease.Reason = strings.TrimSpace(lease.Reason)
	if lease.MaxUses < 0 {
		lease.MaxUses = 0
	}
	if lease.UsedCount < 0 {
		lease.UsedCount = 0
	}
	if !lease.CreatedAt.IsZero() {
		lease.CreatedAt = lease.CreatedAt.UTC()
	}
	if !lease.ExpiresAt.IsZero() {
		lease.ExpiresAt = lease.ExpiresAt.UTC()
	}
	if !lease.RevokedAt.IsZero() {
		lease.RevokedAt = lease.RevokedAt.UTC()
	}
	if !lease.UpdatedAt.IsZero() {
		lease.UpdatedAt = lease.UpdatedAt.UTC()
	}
	return lease
}

func (l OperatorAutoApprovalLease) ActiveAt(now time.Time) bool {
	lease := NormalizeOperatorAutoApprovalLease(l)
	if lease.ID == "" || lease.AdminUserID <= 0 || lease.ChatID == 0 {
		return false
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	if !lease.RevokedAt.IsZero() {
		return false
	}
	if lease.ExpiresAt.IsZero() || !lease.ExpiresAt.After(now) {
		return false
	}
	return lease.MaxUses <= 0 || lease.UsedCount < lease.MaxUses
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
	RawJSON   string
	UpdatedAt time.Time
}

type OperationStateRecord struct {
	Key       SessionKey
	State     OperationState
	UpdatedAt time.Time
}

type SessionStatusState struct {
	PlanState           PlanState
	OperationState      OperationState
	LastFloorMetadata   string
	TurnCount           int
	OutboundCountAtTurn int
}

type DoctorReportRecord struct {
	SessionID      string    `json:"session_id"`
	ChatID         int64     `json:"chat_id"`
	UserID         int64     `json:"user_id"`
	TurnIndex      int       `json:"turn_index"`
	FullReport     string    `json:"full_report"`
	TelegramReport string    `json:"telegram_report"`
	FloorMetadata  string    `json:"floor_metadata,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
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
	state.PhasePlan = normalizeOperationPhasePlan(state.PhasePlan)
	state.PlanLease = NormalizeOperationPlanLease(state.PlanLease)

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
	state.Work = NormalizeWorkOperationMetadata(state.Work)

	if state.UpdatedAt.IsZero() && state.Active() {
		state.UpdatedAt = time.Now().UTC()
	}
	return state
}

func NormalizePlanLeaseStatus(status PlanLeaseStatus) PlanLeaseStatus {
	value := normalizeEnumValue(string(status))
	switch PlanLeaseStatus(value) {
	case PlanLeaseStatusProposed, PlanLeaseStatusApproved, PlanLeaseStatusActive, PlanLeaseStatusPaused, PlanLeaseStatusRevoked, PlanLeaseStatusExpired, PlanLeaseStatusCompleted:
		return PlanLeaseStatus(value)
	default:
		return ""
	}
}

func NormalizeOperationPlanLease(lease OperationPlanLease) OperationPlanLease {
	lease.ID = strings.TrimSpace(lease.ID)
	lease.OperatorTitle = strings.TrimSpace(lease.OperatorTitle)
	lease.PlanTitle = strings.TrimSpace(lease.PlanTitle)
	lease.Summary = strings.TrimSpace(lease.Summary)
	lease.Objective = strings.TrimSpace(lease.Objective)
	lease.MissionID = strings.TrimSpace(lease.MissionID)
	lease.OperationID = strings.TrimSpace(lease.OperationID)
	lease.Status = NormalizePlanLeaseStatus(lease.Status)
	if lease.TurnBudget < 0 {
		lease.TurnBudget = 0
	}
	if lease.RemainingTurns < 0 {
		lease.RemainingTurns = 0
	}
	if lease.TurnBudget > 0 && lease.RemainingTurns == 0 && (lease.Status == "" || lease.Status == PlanLeaseStatusProposed || lease.Status == PlanLeaseStatusApproved || lease.Status == PlanLeaseStatusActive) {
		lease.RemainingTurns = lease.TurnBudget
	}
	lease.CoveredPhaseIDs = normalizeActionStringSlice(lease.CoveredPhaseIDs)
	if !lease.ExpiresAt.IsZero() {
		lease.ExpiresAt = lease.ExpiresAt.UTC()
	}
	lease.Lanes = normalizeOperationPlanLeaseLanes(lease.Lanes)
	if lease.TurnBudget == 0 {
		for _, lane := range lease.Lanes {
			lease.TurnBudget += lane.ExpectedTurns
		}
	}
	if lease.TurnBudget > 0 && lease.RemainingTurns == 0 && (lease.Status == "" || lease.Status == PlanLeaseStatusProposed || lease.Status == PlanLeaseStatusApproved || lease.Status == PlanLeaseStatusActive) {
		lease.RemainingTurns = lease.TurnBudget
	}
	lease.AllowedActions = normalizeActionStringSlice(lease.AllowedActions)
	lease.ForbiddenActions = normalizeActionStringSlice(lease.ForbiddenActions)
	lease.AllowedActions = sanitizeAllowedActionsAgainstForbidden(lease.AllowedActions, lease.ForbiddenActions)
	lease.ValidationGates = normalizeActionStringSlice(lease.ValidationGates)
	lease.ExitConditions = normalizeActionStringSlice(lease.ExitConditions)
	lease.HardInterrupts = normalizeActionStringSlice(lease.HardInterrupts)
	lease.ChildInitiationLanes = normalizeActionStringSlice(lease.ChildInitiationLanes)
	if lease.Active() {
		if len(lease.HardInterrupts) == 0 {
			lease.HardInterrupts = defaultPlanLeaseHardInterrupts()
		}
		if len(lease.ChildInitiationLanes) == 0 {
			lease.ChildInitiationLanes = defaultPlanLeaseChildInitiationLanes()
		}
	}
	lease.EvidenceDigest = normalizeOperationPlanLeaseEvidenceDigest(lease.EvidenceDigest)
	if !lease.ApprovedAt.IsZero() {
		lease.ApprovedAt = lease.ApprovedAt.UTC()
	}
	if !lease.CreatedAt.IsZero() {
		lease.CreatedAt = lease.CreatedAt.UTC()
	}
	if !lease.UpdatedAt.IsZero() {
		lease.UpdatedAt = lease.UpdatedAt.UTC()
	}
	if lease.Status == "" && lease.Active() {
		lease.Status = PlanLeaseStatusProposed
	}
	if lease.CreatedAt.IsZero() && lease.Active() {
		lease.CreatedAt = time.Now().UTC()
	}
	if lease.UpdatedAt.IsZero() && lease.Active() {
		lease.UpdatedAt = time.Now().UTC()
	}
	return lease
}

func normalizeOperationPlanLeaseLanes(values []OperationPlanLeaseLane) []OperationPlanLeaseLane {
	out := make([]OperationPlanLeaseLane, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for index, lane := range values {
		lane.ID = strings.TrimSpace(lane.ID)
		lane.OperatorTitle = strings.TrimSpace(lane.OperatorTitle)
		lane.PlanTitle = strings.TrimSpace(lane.PlanTitle)
		lane.Summary = strings.TrimSpace(lane.Summary)
		lane.AuthorityClass = normalizeEnumValue(lane.AuthorityClass)
		if lane.ExpectedTurns < 0 {
			lane.ExpectedTurns = 0
		}
		lane.AllowedActions = normalizeActionStringSlice(lane.AllowedActions)
		lane.ForbiddenActions = normalizeActionStringSlice(lane.ForbiddenActions)
		lane.AllowedActions = sanitizeAllowedActionsAgainstForbidden(lane.AllowedActions, lane.ForbiddenActions)
		if !lane.Active() {
			continue
		}
		baseID := lane.ID
		if baseID == "" {
			baseID = fmt.Sprintf("lane-%d", index+1)
		}
		id := baseID
		for suffix := 2; ; suffix++ {
			if _, exists := seen[id]; !exists {
				break
			}
			id = fmt.Sprintf("%s-%d", baseID, suffix)
		}
		lane.ID = id
		seen[id] = struct{}{}
		out = append(out, lane)
	}
	return out
}

func normalizeOperationPlanLeaseEvidenceDigest(summary OperationPlanLeaseEvidenceDigest) OperationPlanLeaseEvidenceDigest {
	if summary.TurnsSpent < 0 {
		summary.TurnsSpent = 0
	}
	summary.LanesUsed = normalizeOperationStringList(summary.LanesUsed)
	summary.Completed = normalizeOperationStringList(summary.Completed)
	summary.Blocked = normalizeOperationStringList(summary.Blocked)
	summary.InterruptsRaised = normalizeOperationStringList(summary.InterruptsRaised)
	summary.EvidenceRefs = normalizeOperationStringList(summary.EvidenceRefs)
	summary.ChangesMade = normalizeOperationStringList(summary.ChangesMade)
	summary.ResidualRisk = strings.TrimSpace(summary.ResidualRisk)
	summary.SuggestedNextLease = strings.TrimSpace(summary.SuggestedNextLease)
	if !summary.UpdatedAt.IsZero() {
		summary.UpdatedAt = summary.UpdatedAt.UTC()
	}
	return summary
}

func defaultPlanLeaseHardInterrupts() []string {
	return []string{
		"credentials_or_tokens",
		"mailbox_content_or_mutation",
		"external_account_mutation",
		"public_contact_or_posting",
		"purchases_or_spend",
		"policy_or_grant_change",
		"deploy_or_restart_without_parking",
		"destructive_migration",
		"child_authority_expansion",
	}
}

func defaultPlanLeaseChildInitiationLanes() []string {
	return []string{"scheduled_digest", "blocked_question", "capability_request", "urgent_interrupt", "result_report"}
}

func normalizeOperationPhasePlan(plan OperationPhasePlan) OperationPhasePlan {
	plan.ID = strings.TrimSpace(plan.ID)
	plan.Goal = strings.TrimSpace(plan.Goal)
	plan.CurrentPhaseID = strings.TrimSpace(plan.CurrentPhaseID)
	phases := make([]OperationPhase, 0, len(plan.Phases))
	seenIDs := make(map[string]struct{}, len(plan.Phases))
	for index, phase := range plan.Phases {
		phase = normalizeOperationPhase(phase, index)
		if !phase.Active() {
			continue
		}
		baseID := phase.ID
		if baseID == "" {
			baseID = fmt.Sprintf("phase-%d", index+1)
		}
		id := baseID
		for suffix := 2; ; suffix++ {
			if _, exists := seenIDs[id]; !exists {
				break
			}
			id = fmt.Sprintf("%s-%d", baseID, suffix)
		}
		phase.ID = id
		seenIDs[id] = struct{}{}
		phases = append(phases, phase)
	}
	plan.Phases = phases
	if plan.CurrentPhaseID != "" {
		currentStatus := PlanStatus("")
		for _, phase := range plan.Phases {
			if phase.ID == plan.CurrentPhaseID {
				currentStatus = phase.Status
				break
			}
		}
		if _, ok := seenIDs[plan.CurrentPhaseID]; !ok {
			plan.CurrentPhaseID = ""
		} else if currentStatus == PlanStatusCompleted {
			for _, phase := range plan.Phases {
				if phase.Status == PlanStatusInProgress || phase.Status == PlanStatusPending {
					plan.CurrentPhaseID = ""
					break
				}
			}
		}
	}
	if plan.CurrentPhaseID == "" {
		for _, phase := range plan.Phases {
			if phase.Status == PlanStatusInProgress || phase.Status == PlanStatusPending {
				plan.CurrentPhaseID = phase.ID
				break
			}
		}
	}
	if plan.CurrentPhaseID == "" && len(plan.Phases) > 0 {
		plan.CurrentPhaseID = plan.Phases[0].ID
	}
	if !plan.UpdatedAt.IsZero() {
		plan.UpdatedAt = plan.UpdatedAt.UTC()
	}
	if plan.UpdatedAt.IsZero() && plan.Active() {
		plan.UpdatedAt = time.Now().UTC()
	}
	return plan
}

func normalizeOperationPhase(phase OperationPhase, index int) OperationPhase {
	_ = index
	phase.ID = strings.TrimSpace(phase.ID)
	phase.OperatorTitle = strings.TrimSpace(phase.OperatorTitle)
	phase.PlanTitle = strings.TrimSpace(phase.PlanTitle)
	phase.Summary = strings.TrimSpace(phase.Summary)
	phase.Status = NormalizePlanStatus(phase.Status)
	phase.AuthorityClass = normalizeEnumValue(phase.AuthorityClass)
	phase.WhyNow = strings.TrimSpace(phase.WhyNow)
	phase.BoundedEffect = strings.TrimSpace(phase.BoundedEffect)
	phase.AllowedActions = normalizeActionStringSlice(phase.AllowedActions)
	phase.ForbiddenActions = normalizeActionStringSlice(phase.ForbiddenActions)
	phase.AllowedActions = sanitizeAllowedActionsAgainstForbidden(phase.AllowedActions, phase.ForbiddenActions)
	phase.ValidationPlan = normalizeActionStringSlice(phase.ValidationPlan)
	phase.GateLevel = normalizeEnumValue(phase.GateLevel)
	phase.GateReasonCode = normalizeEnumValue(phase.GateReasonCode)
	phase.ApprovalSubject = normalizeEnumValue(phase.ApprovalSubject)
	phase.BlockedReasonCode = normalizeEnumValue(phase.BlockedReasonCode)
	phase.SupersedesPhaseIDs = normalizeActionStringSlice(phase.SupersedesPhaseIDs)
	phase.LeaseID = strings.TrimSpace(phase.LeaseID)
	if !phase.CompletedAt.IsZero() {
		phase.CompletedAt = phase.CompletedAt.UTC()
	}
	if phase.Status == "" && phase.Active() {
		phase.Status = PlanStatusPending
	}
	if phase.Status != PlanStatusCompleted {
		phase.CompletedAt = time.Time{}
	}
	if phase.Status == PlanStatusCompleted && phase.CompletedAt.IsZero() {
		phase.CompletedAt = time.Now().UTC()
	}
	return phase
}

func NormalizeWorkOperationMetadata(work WorkOperationMetadata) WorkOperationMetadata {
	work.Executor = strings.TrimSpace(work.Executor)
	work.ConfiguredExecutor = strings.TrimSpace(work.ConfiguredExecutor)
	work.PreferredExecutor = strings.TrimSpace(work.PreferredExecutor)
	work.FallbackReason = strings.TrimSpace(work.FallbackReason)
	work.CodexThreadID = strings.TrimSpace(work.CodexThreadID)
	work.CodexLastTurnID = strings.TrimSpace(work.CodexLastTurnID)
	work.CodexLaneMode = strings.TrimSpace(work.CodexLaneMode)
	work.RepoRoot = strings.TrimSpace(work.RepoRoot)
	work.Workdir = strings.TrimSpace(work.Workdir)
	work.LastSummary = strings.TrimSpace(work.LastSummary)
	work.LastError = strings.TrimSpace(work.LastError)
	work.PendingCodexApproval = strings.TrimSpace(work.PendingCodexApproval)
	work.PatchPreview = truncateOperationString(strings.TrimSpace(work.PatchPreview), 4000)
	work.CommitLaneStatus = strings.TrimSpace(work.CommitLaneStatus)
	work.ChangedFiles = normalizeOperationStringList(work.ChangedFiles)
	work.Commands = normalizeOperationStringList(work.Commands)
	work.CodexEvents = normalizeWorkCodexEvents(work.CodexEvents)
	if work.LastExecutorUpdatedAt.IsZero() && (work.Executor != "" || work.LastSummary != "" || work.LastError != "") {
		work.LastExecutorUpdatedAt = time.Now().UTC()
	}
	return work
}

func normalizeWorkCodexEvents(values []WorkCodexEvent) []WorkCodexEvent {
	out := make([]WorkCodexEvent, 0, len(values))
	seen := map[string]struct{}{}
	for _, value := range values {
		event := WorkCodexEvent{
			Kind:     strings.TrimSpace(value.Kind),
			Method:   strings.TrimSpace(value.Method),
			Status:   strings.TrimSpace(value.Status),
			Subject:  strings.TrimSpace(value.Subject),
			Path:     strings.TrimSpace(value.Path),
			Command:  strings.TrimSpace(value.Command),
			Preview:  truncateOperationString(strings.TrimSpace(value.Preview), 1000),
			ThreadID: strings.TrimSpace(value.ThreadID),
			TurnID:   strings.TrimSpace(value.TurnID),
			AgentID:  strings.TrimSpace(value.AgentID),
			Server:   strings.TrimSpace(value.Server),
			Tool:     strings.TrimSpace(value.Tool),
		}
		if event.Kind == "" && event.Method == "" && event.Subject == "" && event.Path == "" && event.Command == "" {
			continue
		}
		key := strings.Join([]string{event.Kind, event.Method, event.Status, event.Subject, event.Path, event.Command, event.ThreadID, event.TurnID, event.AgentID, event.Server, event.Tool, event.Preview}, "\x00")
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, event)
	}
	return out
}

func normalizeOperationStringList(values []string) []string {
	out := make([]string, 0, len(values))
	seen := map[string]struct{}{}
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		out = append(out, trimmed)
	}
	return out
}

func truncateOperationString(value string, limit int) string {
	value = strings.TrimSpace(value)
	if limit <= 0 || len(value) <= limit {
		return value
	}
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	if limit <= 12 {
		return string(runes[:limit])
	}
	return strings.TrimSpace(string(runes[:limit-12])) + " [truncated]"
}

func (p OperationPhasePlan) Active() bool {
	return strings.TrimSpace(p.ID) != "" ||
		strings.TrimSpace(p.Goal) != "" ||
		strings.TrimSpace(p.CurrentPhaseID) != "" ||
		len(p.Phases) > 0
}

func (p OperationPhase) Active() bool {
	return strings.TrimSpace(p.ID) != "" ||
		strings.TrimSpace(p.OperatorTitle) != "" ||
		strings.TrimSpace(p.PlanTitle) != "" ||
		strings.TrimSpace(p.Summary) != "" ||
		strings.TrimSpace(string(p.Status)) != "" ||
		strings.TrimSpace(p.AuthorityClass) != "" ||
		strings.TrimSpace(p.WhyNow) != "" ||
		strings.TrimSpace(p.BoundedEffect) != "" ||
		len(p.AllowedActions) > 0 ||
		len(p.ForbiddenActions) > 0 ||
		len(p.ValidationPlan) > 0 ||
		strings.TrimSpace(p.GateLevel) != "" ||
		strings.TrimSpace(p.GateReasonCode) != "" ||
		strings.TrimSpace(p.ApprovalSubject) != "" ||
		p.AutoApproveEligible != nil ||
		strings.TrimSpace(p.BlockedReasonCode) != "" ||
		p.RequiresConsent ||
		p.RequiresOptIn ||
		len(p.SupersedesPhaseIDs) > 0 ||
		p.StaleAuthority ||
		strings.TrimSpace(p.LeaseID) != "" ||
		!p.CompletedAt.IsZero()
}

func (l OperationPlanLease) Active() bool {
	return strings.TrimSpace(l.ID) != "" ||
		strings.TrimSpace(l.OperatorTitle) != "" ||
		strings.TrimSpace(l.PlanTitle) != "" ||
		strings.TrimSpace(l.Summary) != "" ||
		strings.TrimSpace(l.Objective) != "" ||
		strings.TrimSpace(l.MissionID) != "" ||
		strings.TrimSpace(l.OperationID) != "" ||
		strings.TrimSpace(string(l.Status)) != "" ||
		l.TurnBudget > 0 ||
		l.RemainingTurns > 0 ||
		len(l.CoveredPhaseIDs) > 0 ||
		!l.ExpiresAt.IsZero() ||
		len(l.Lanes) > 0 ||
		len(l.AllowedActions) > 0 ||
		len(l.ForbiddenActions) > 0 ||
		len(l.ValidationGates) > 0 ||
		len(l.ExitConditions) > 0 ||
		len(l.HardInterrupts) > 0 ||
		len(l.ChildInitiationLanes) > 0 ||
		l.EvidenceDigest.Active() ||
		l.ApprovedBy > 0 ||
		!l.ApprovedAt.IsZero()
}

func (l OperationPlanLeaseLane) Active() bool {
	return strings.TrimSpace(l.ID) != "" ||
		strings.TrimSpace(l.OperatorTitle) != "" ||
		strings.TrimSpace(l.PlanTitle) != "" ||
		strings.TrimSpace(l.Summary) != "" ||
		strings.TrimSpace(l.AuthorityClass) != "" ||
		l.ExpectedTurns > 0 ||
		len(l.AllowedActions) > 0 ||
		len(l.ForbiddenActions) > 0
}

func (s OperationPlanLeaseEvidenceDigest) Active() bool {
	return s.TurnsSpent > 0 ||
		len(s.LanesUsed) > 0 ||
		len(s.Completed) > 0 ||
		len(s.Blocked) > 0 ||
		len(s.InterruptsRaised) > 0 ||
		len(s.EvidenceRefs) > 0 ||
		len(s.ChangesMade) > 0 ||
		strings.TrimSpace(s.ResidualRisk) != "" ||
		strings.TrimSpace(s.SuggestedNextLease) != "" ||
		!s.UpdatedAt.IsZero()
}

func (s OperationState) Active() bool {
	normalized := s
	return strings.TrimSpace(normalized.ID) != "" ||
		strings.TrimSpace(normalized.Objective) != "" ||
		strings.TrimSpace(string(normalized.Status)) != "" ||
		strings.TrimSpace(normalized.Stage) != "" ||
		strings.TrimSpace(normalized.Summary) != "" ||
		normalized.Proposal.Active() ||
		normalized.PhasePlan.Active() ||
		normalized.PlanLease.Active() ||
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
		CapabilityKindGenericDelegation,
		CapabilityKindSystemChange:
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
	ref := NormalizeAuthorityUseRef(AuthorityUseRef{
		SessionID:            invocation.SessionID,
		TurnRunID:            invocation.TurnRunID,
		ContinuationLeaseID:  invocation.ContinuationLeaseID,
		OperationPlanLeaseID: invocation.OperationPlanLeaseID,
		AuthoritySource:      invocation.AuthoritySource,
	})
	invocation.SessionID = ref.SessionID
	invocation.TurnRunID = ref.TurnRunID
	invocation.ContinuationLeaseID = ref.ContinuationLeaseID
	invocation.OperationPlanLeaseID = ref.OperationPlanLeaseID
	invocation.AuthoritySource = ref.AuthoritySource
	if invocation.CreatedAt.IsZero() && invocation.GrantID != "" {
		invocation.CreatedAt = time.Now().UTC()
	}
	return invocation
}

func NormalizeAuthorityUseRef(ref AuthorityUseRef) AuthorityUseRef {
	ref.SessionID = strings.TrimSpace(ref.SessionID)
	if ref.TurnRunID < 0 {
		ref.TurnRunID = 0
	}
	ref.ContinuationLeaseID = strings.TrimSpace(ref.ContinuationLeaseID)
	ref.OperationPlanLeaseID = strings.TrimSpace(ref.OperationPlanLeaseID)
	ref.AuthoritySource = normalizeEnumValue(ref.AuthoritySource)
	return ref
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
	state.ActionProposal = NormalizeActionProposal(state.ActionProposal)
	state.ContinuationLease = NormalizeContinuationLease(state.ContinuationLease)
	state.ApprovalBundle = NormalizeContinuationApprovalBundle(state.ApprovalBundle)
	state.HandshakeBlockedReason = normalizeContinuationStage(state.HandshakeBlockedReason)
	if !state.ParkedAt.IsZero() {
		state.ParkedAt = state.ParkedAt.UTC()
	}
	state.ParkedReason = strings.TrimSpace(state.ParkedReason)
	state.ParkedSource = strings.TrimSpace(state.ParkedSource)
	if state.Kind == "" && (state.Status != "" || state.DecisionID != "" || state.Objective != "" || state.StageSummary != "" || state.RemainingTurns > 0 || state.ApprovedBy > 0 || state.ActionProposal.Active() || state.ContinuationLease.ID != "" || state.ContinuationLease.ProposalID != "" || state.ApprovalBundle.Active()) {
		state.Kind = TurnAuthorizationKindContinuation
	}
	if state.RemainingTurns < 0 {
		state.RemainingTurns = 0
	}
	if state.Status == TurnAuthorizationStatusIdle || state.Status == TurnAuthorizationStatusRevoked {
		state.ApprovedBy = 0
		state.DecisionID = ""
		state.ParkedAt = time.Time{}
		state.ParkedReason = ""
		state.ParkedSource = ""
	}
	if state.UpdatedAt.IsZero() && (state.Kind != "" || state.Status != "" || state.DecisionID != "" || state.Objective != "" || state.StageSummary != "" || state.RemainingTurns > 0 || state.ApprovedBy > 0 || state.ActionProposal.Active() || state.ContinuationLease.ID != "" || state.ContinuationLease.ProposalID != "" || state.ApprovalBundle.Active() || !state.ParkedAt.IsZero() || state.ParkedReason != "" || state.ParkedSource != "") {
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
		strings.TrimSpace(p.OperatorTitle) != "" ||
		strings.TrimSpace(p.PlanTitle) != "" ||
		strings.TrimSpace(p.Summary) != "" ||
		strings.TrimSpace(p.WhyNow) != "" ||
		strings.TrimSpace(p.BoundedEffect) != "" ||
		strings.TrimSpace(string(p.Status)) != ""
}

func (p ActionProposal) Active() bool {
	return strings.TrimSpace(p.ID) != "" ||
		strings.TrimSpace(p.OperationID) != "" ||
		strings.TrimSpace(p.MissionID) != "" ||
		strings.TrimSpace(p.OperatorTitle) != "" ||
		strings.TrimSpace(p.PlanTitle) != "" ||
		strings.TrimSpace(p.Summary) != "" ||
		strings.TrimSpace(p.WhyNow) != "" ||
		strings.TrimSpace(p.BoundedEffect) != "" ||
		strings.TrimSpace(p.RiskClass) != "" ||
		len(p.AllowedActions) > 0 ||
		len(p.ForbiddenActions) > 0 ||
		len(p.ValidationPlan) > 0 ||
		p.AutoApproveEligible != nil ||
		!p.ExpiresAt.IsZero() ||
		strings.TrimSpace(p.PlanHash) != "" ||
		strings.TrimSpace(string(p.Status)) != ""
}

func NormalizeContinuationLeaseClass(class ContinuationLeaseClass) ContinuationLeaseClass {
	value := normalizeEnumValue(string(class))
	switch ContinuationLeaseClass(value) {
	case ContinuationLeaseClassLocalWorkspace,
		ContinuationLeaseClassDataAccess,
		ContinuationLeaseClassChildWake,
		ContinuationLeaseClassCapabilityGrant,
		ContinuationLeaseClassDeployRestart:
		return ContinuationLeaseClass(value)
	default:
		return ""
	}
}

func InferContinuationLeaseClass(riskClass string, allowedActions []string, boundedEffect string) ContinuationLeaseClass {
	_ = boundedEffect
	if contract, ok := AuthorityContractFor(riskClass, allowedActions, ""); ok && contract.LeaseClass != "" {
		return contract.LeaseClass
	}
	return ""
}

func ContinuationLeaseClassLabel(class ContinuationLeaseClass) string {
	switch NormalizeContinuationLeaseClass(class) {
	case ContinuationLeaseClassLocalWorkspace:
		return "local workspace"
	case ContinuationLeaseClassDataAccess:
		return "data access"
	case ContinuationLeaseClassChildWake:
		return "child wake"
	case ContinuationLeaseClassCapabilityGrant:
		return "capability grant"
	case ContinuationLeaseClassDeployRestart:
		return "deploy/restart"
	default:
		return "generic"
	}
}

func ContinuationLeaseClassBoundary(class ContinuationLeaseClass) string {
	switch NormalizeContinuationLeaseClass(class) {
	case ContinuationLeaseClassLocalWorkspace:
		return "local repo/workspace work only; no repository history, deploy, restart, credentials, or external effects unless separately granted"
	case ContinuationLeaseClassDataAccess:
		return "read exactly the approved resource descriptors; no silent broad ingestion, retention, or external-account access"
	case ContinuationLeaseClassChildWake:
		return "wake only the named child and approved count; no policy drift, grants, or external effects beyond the child charter"
	case ContinuationLeaseClassCapabilityGrant:
		return "request/review authority only unless a separate active capability grant exists; leases do not grant capabilities by themselves"
	case ContinuationLeaseClassDeployRestart:
		return "release-class work requires fresh evidence, handoff, verification, and rollback/stop gates; no unbounded restart/deploy loops"
	default:
		return "bounded continuation only; do not infer authority outside explicit allowed actions"
	}
}

func DefaultContinuationLeaseConstraints(class ContinuationLeaseClass) map[string]string {
	switch NormalizeContinuationLeaseClass(class) {
	case ContinuationLeaseClassLocalWorkspace:
		return map[string]string{
			"scope":       "local workspace/repository only",
			"history":     "commit requires explicit lease authority; push requires separate lease",
			"externality": "no deploy, restart, credentials, purchases, public contact, or external accounts",
			"validation":  "focused tests or diff checks before report",
		}
	case ContinuationLeaseClassDataAccess:
		return map[string]string{
			"resource":  "explicit descriptor required: artifact/file/attachment/url/account surface",
			"scope":     "one approved resource or bounded resource set",
			"retention": "ephemeral by default; durable retention requires explicit approval",
			"redaction": "apply connector redaction before model consumption when available",
		}
	case ContinuationLeaseClassChildWake:
		return map[string]string{
			"agent":      "named durable child required",
			"wake_count": "bounded count/cadence required",
			"outbound":   "child policy controls outbound effects",
			"no_drift":   "no policy/bootstrap/grant changes without separate approval",
		}
	case ContinuationLeaseClassCapabilityGrant:
		return map[string]string{
			"request":    "request_id or target_resource required",
			"grant":      "grant_set/access_check remains separate capability_authority state",
			"actions":    "allowed actions must be explicit; wildcard is insufficient",
			"activation": "approved lease may prepare/review, not silently activate broad authority",
		}
	case ContinuationLeaseClassDeployRestart:
		return map[string]string{
			"handoff":      "pre-restart/deploy handoff required",
			"verification": "post-action status/journal/smoke evidence required",
			"rollback":     "stop or rollback path must be named when risk is nontrivial",
			"separation":   "commit/push/deploy/restart should remain separately visible steps",
		}
	default:
		return nil
	}
}

func normalizeContinuationLeaseConstraints(class ContinuationLeaseClass, constraints map[string]string) map[string]string {
	defaults := DefaultContinuationLeaseConstraints(class)
	if len(defaults) == 0 && len(constraints) == 0 {
		return nil
	}
	out := make(map[string]string, len(defaults)+len(constraints))
	for key, value := range defaults {
		key = normalizeEnumValue(key)
		value = strings.TrimSpace(value)
		if key != "" && value != "" {
			out[key] = value
		}
	}
	for key, value := range constraints {
		key = normalizeEnumValue(key)
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

func continuationLeaseClassRequiresExactActions(class ContinuationLeaseClass) bool {
	switch NormalizeContinuationLeaseClass(class) {
	case ContinuationLeaseClassDataAccess, ContinuationLeaseClassChildWake, ContinuationLeaseClassCapabilityGrant, ContinuationLeaseClassDeployRestart:
		return true
	default:
		return false
	}
}

type ContinuationLeaseAccessDecision struct {
	LeaseID string `json:"lease_id,omitempty"`
	Action  string `json:"action,omitempty"`
	Allowed bool   `json:"allowed"`
	Reason  string `json:"reason,omitempty"`
}

func (l ContinuationLease) Active() bool {
	return l.ActiveAt(time.Now().UTC())
}

func (l ContinuationLease) ActiveAt(now time.Time) bool {
	lease := NormalizeContinuationLease(l)
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	if lease.Status != ContinuationLeaseStatusActive || lease.RemainingTurns <= 0 {
		return false
	}
	return lease.ExpiresAt.IsZero() || lease.ExpiresAt.After(now)
}

func CheckContinuationLeaseAction(lease ContinuationLease, action string, now time.Time) ContinuationLeaseAccessDecision {
	lease = NormalizeContinuationLease(lease)
	action = normalizeEnumValue(action)
	decision := ContinuationLeaseAccessDecision{LeaseID: lease.ID, Action: action}
	if action == "" {
		decision.Reason = "action_required"
		return decision
	}
	if !lease.ActiveAt(now) {
		decision.Reason = "lease_inactive_or_expired"
		return decision
	}
	if actionListMatches(lease.ForbiddenActions, action) {
		decision.Reason = "action_forbidden"
		return decision
	}
	exactAllowed := actionListMatches(lease.AllowedActions, action)
	if actionListMatches(lease.AllowedActions, "*") && !exactAllowed && continuationLeaseClassRequiresExactActions(lease.LeaseClass) {
		decision.Reason = "lease_class_requires_explicit_action"
		return decision
	}
	if actionListMatches(lease.AllowedActions, "*") || exactAllowed {
		decision.Allowed = true
		decision.Reason = "allowed"
		return decision
	}
	decision.Reason = "action_not_allowed"
	return decision
}

func actionListMatches(values []string, action string) bool {
	action = normalizeEnumValue(action)
	if action == "" {
		return false
	}
	for _, value := range values {
		if normalizeEnumValue(value) == action {
			return true
		}
	}
	return false
}

func SanitizeActionProposalAuthority(proposal ActionProposal) ActionProposal {
	proposal.AllowedActions = sanitizeAllowedActionsAgainstForbidden(proposal.AllowedActions, proposal.ForbiddenActions)
	return proposal
}

func ContinuationStateAuthorityNeedsSanitization(state ContinuationState) bool {
	return actionAuthorityHasContradiction(state.ActionProposal.AllowedActions, state.ActionProposal.ForbiddenActions) ||
		actionAuthorityHasContradiction(state.ContinuationLease.AllowedActions, state.ContinuationLease.ForbiddenActions) ||
		continuationLeaseClassContradictedByActions(state.ContinuationLease.LeaseClass, sanitizeAllowedActionsAgainstForbidden(state.ContinuationLease.AllowedActions, state.ContinuationLease.ForbiddenActions), state.ContinuationLease.ForbiddenActions)
}

func actionAuthorityHasContradiction(allowedActions []string, forbiddenActions []string) bool {
	allowed := normalizeActionStringSlice(allowedActions)
	forbidden := normalizeActionStringSlice(forbiddenActions)
	if len(allowed) == 0 || len(forbidden) == 0 {
		return false
	}
	forbiddenExact := make(map[string]struct{}, len(forbidden))
	for _, action := range forbidden {
		if normalized := normalizeAuthorityMatchText(action); normalized != "" {
			forbiddenExact[normalized] = struct{}{}
		}
	}
	broadDeployRestartForbidden := authorityForbiddenIncludesDeployRestart(forbidden)
	for _, action := range allowed {
		normalized := normalizeAuthorityMatchText(action)
		if normalized == "" {
			continue
		}
		if _, forbidden := forbiddenExact[normalized]; forbidden {
			return true
		}
		if broadDeployRestartForbidden && authorityActionIsDeployRestartGrant(normalized) {
			return true
		}
	}
	return false
}

func sanitizeAllowedActionsAgainstForbidden(allowedActions []string, forbiddenActions []string) []string {
	allowed := normalizeActionStringSlice(allowedActions)
	if len(allowed) == 0 {
		return nil
	}
	forbidden := normalizeActionStringSlice(forbiddenActions)
	if len(forbidden) == 0 {
		return allowed
	}
	forbiddenExact := make(map[string]struct{}, len(forbidden))
	for _, action := range forbidden {
		if normalized := normalizeAuthorityMatchText(action); normalized != "" {
			forbiddenExact[normalized] = struct{}{}
		}
	}
	broadDeployRestartForbidden := authorityForbiddenIncludesDeployRestart(forbidden)
	out := make([]string, 0, len(allowed))
	for _, action := range allowed {
		normalized := normalizeAuthorityMatchText(action)
		if normalized == "" {
			continue
		}
		if _, forbidden := forbiddenExact[normalized]; forbidden {
			continue
		}
		if broadDeployRestartForbidden && authorityActionIsDeployRestartGrant(normalized) {
			continue
		}
		out = append(out, action)
	}
	return normalizeActionStringSlice(out)
}

func authorityForbiddenIncludesDeployRestart(actions []string) bool {
	for _, action := range actions {
		switch normalizeAuthorityMatchText(action) {
		case "deploy",
			"restart",
			"restart_service",
			"service_restart",
			"deploy_restart",
			"restart_deploy",
			"deploy_or_restart",
			"restart_or_deploy",
			"deploy_or_enable_systemd",
			"deploy_or_enable_service",
			"deploy_service_restart",
			"restart_or_service_restart":
			return true
		}
	}
	return false
}

func authorityActionIsDeployRestartGrant(action string) bool {
	switch normalizeAuthorityMatchText(action) {
	case "deploy",
		"restart",
		"restart_service",
		"service_restart",
		"live_deploy",
		"run_deploy",
		"system_change",
		"git_push",
		"push_remote",
		"prepare_release_handoff",
		"run_explicit_release_step",
		"post_restart_verification",
		"report_release_result":
		return true
	default:
		return false
	}
}

func continuationAllowedSupportsDeployRestart(actions []string) bool {
	for _, action := range actions {
		if authorityActionIsDeployRestartGrant(action) {
			return true
		}
	}
	return false
}

func continuationLeaseClassContradictedByActions(class ContinuationLeaseClass, allowedActions []string, forbiddenActions []string) bool {
	return NormalizeContinuationLeaseClass(class) == ContinuationLeaseClassDeployRestart &&
		authorityForbiddenIncludesDeployRestart(forbiddenActions) &&
		!continuationAllowedSupportsDeployRestart(allowedActions)
}

func NormalizeActionProposal(proposal ActionProposal) ActionProposal {
	proposal.ID = strings.TrimSpace(proposal.ID)
	proposal.OperationID = strings.TrimSpace(proposal.OperationID)
	proposal.MissionID = strings.TrimSpace(proposal.MissionID)
	proposal.OperatorTitle = strings.TrimSpace(proposal.OperatorTitle)
	proposal.PlanTitle = strings.TrimSpace(proposal.PlanTitle)
	proposal.Summary = strings.TrimSpace(proposal.Summary)
	proposal.WhyNow = strings.TrimSpace(proposal.WhyNow)
	proposal.BoundedEffect = strings.TrimSpace(proposal.BoundedEffect)
	proposal.RiskClass = normalizeEnumValue(proposal.RiskClass)
	proposal.AllowedActions = normalizeActionStringSlice(proposal.AllowedActions)
	proposal.ForbiddenActions = normalizeActionStringSlice(proposal.ForbiddenActions)
	proposal.AllowedActions = sanitizeAllowedActionsAgainstForbidden(proposal.AllowedActions, proposal.ForbiddenActions)
	proposal.ValidationPlan = normalizeActionStringSlice(proposal.ValidationPlan)
	proposal.PlanHash = strings.TrimSpace(proposal.PlanHash)
	proposal.Status = NormalizeProposalStatus(proposal.Status)
	if !proposal.ExpiresAt.IsZero() {
		proposal.ExpiresAt = proposal.ExpiresAt.UTC()
	}
	if !proposal.CreatedAt.IsZero() {
		proposal.CreatedAt = proposal.CreatedAt.UTC()
	}
	if !proposal.UpdatedAt.IsZero() {
		proposal.UpdatedAt = proposal.UpdatedAt.UTC()
	}
	if proposal.Status == "" && proposal.Active() {
		proposal.Status = ProposalStatusPending
	}
	if proposal.CreatedAt.IsZero() && proposal.Active() {
		proposal.CreatedAt = time.Now().UTC()
	}
	if proposal.UpdatedAt.IsZero() && proposal.Active() {
		proposal.UpdatedAt = time.Now().UTC()
	}
	return proposal
}

func NormalizeContinuationLeaseStatus(status ContinuationLeaseStatus) ContinuationLeaseStatus {
	value := normalizeEnumValue(string(status))
	switch ContinuationLeaseStatus(value) {
	case ContinuationLeaseStatusPending, ContinuationLeaseStatusActive, ContinuationLeaseStatusConsumed, ContinuationLeaseStatusRevoked, ContinuationLeaseStatusExpired:
		return ContinuationLeaseStatus(value)
	default:
		return ""
	}
}

func NormalizeContinuationApprovalBundle(bundle ContinuationApprovalBundle) ContinuationApprovalBundle {
	bundle.ID = strings.TrimSpace(bundle.ID)
	bundle.Status = NormalizeContinuationLeaseStatus(bundle.Status)
	bundle.CurrentPhaseID = strings.TrimSpace(bundle.CurrentPhaseID)
	phases := make([]ContinuationApprovalBundlePhase, 0, len(bundle.Phases))
	seen := make(map[string]struct{}, len(bundle.Phases))
	for i, phase := range bundle.Phases {
		phase = NormalizeContinuationApprovalBundlePhase(phase)
		if !phase.Active() {
			continue
		}
		if phase.Index <= 0 {
			phase.Index = i + 1
		}
		baseID := strings.TrimSpace(phase.ID)
		if baseID == "" {
			baseID = strings.TrimSpace(phase.OperationPhaseID)
		}
		if baseID == "" {
			baseID = fmt.Sprintf("phase-%d", phase.Index)
		}
		id := baseID
		for suffix := 2; ; suffix++ {
			if _, exists := seen[id]; !exists {
				break
			}
			id = fmt.Sprintf("%s-%d", baseID, suffix)
		}
		phase.ID = id
		seen[id] = struct{}{}
		phases = append(phases, phase)
	}
	bundle.Phases = phases
	if bundle.CurrentPhaseID != "" {
		if _, ok := seen[bundle.CurrentPhaseID]; !ok {
			bundle.CurrentPhaseID = ""
		}
	}
	if bundle.CurrentPhaseID == "" {
		for _, phase := range bundle.Phases {
			if phase.Status == ContinuationLeaseStatusActive || phase.Status == ContinuationLeaseStatusPending || phase.Status == "" {
				bundle.CurrentPhaseID = phase.ID
				break
			}
		}
	}
	if !bundle.ExpiresAt.IsZero() {
		bundle.ExpiresAt = bundle.ExpiresAt.UTC()
	}
	if !bundle.CreatedAt.IsZero() {
		bundle.CreatedAt = bundle.CreatedAt.UTC()
	}
	if !bundle.UpdatedAt.IsZero() {
		bundle.UpdatedAt = bundle.UpdatedAt.UTC()
	}
	if !bundle.ApprovedAt.IsZero() {
		bundle.ApprovedAt = bundle.ApprovedAt.UTC()
	}
	if !bundle.ConsumedAt.IsZero() {
		bundle.ConsumedAt = bundle.ConsumedAt.UTC()
	}
	if !bundle.RevokedAt.IsZero() {
		bundle.RevokedAt = bundle.RevokedAt.UTC()
	}
	if bundle.Status == "" && bundle.Active() {
		bundle.Status = ContinuationLeaseStatusPending
	}
	if bundle.CreatedAt.IsZero() && bundle.Active() {
		bundle.CreatedAt = time.Now().UTC()
	}
	if bundle.UpdatedAt.IsZero() && bundle.Active() {
		bundle.UpdatedAt = time.Now().UTC()
	}
	return bundle
}

func NormalizeContinuationApprovalBundlePhase(phase ContinuationApprovalBundlePhase) ContinuationApprovalBundlePhase {
	phase.ID = strings.TrimSpace(phase.ID)
	phase.OperationPhaseID = strings.TrimSpace(phase.OperationPhaseID)
	phase.OperatorTitle = strings.TrimSpace(phase.OperatorTitle)
	phase.PlanTitle = strings.TrimSpace(phase.PlanTitle)
	phase.Summary = strings.TrimSpace(phase.Summary)
	phase.AuthorityClass = normalizeEnumValue(phase.AuthorityClass)
	phase.WhyNow = strings.TrimSpace(phase.WhyNow)
	phase.BoundedEffect = strings.TrimSpace(phase.BoundedEffect)
	phase.AllowedActions = normalizeActionStringSlice(phase.AllowedActions)
	phase.ForbiddenActions = normalizeActionStringSlice(phase.ForbiddenActions)
	phase.AllowedActions = sanitizeAllowedActionsAgainstForbidden(phase.AllowedActions, phase.ForbiddenActions)
	phase.ValidationPlan = normalizeActionStringSlice(phase.ValidationPlan)
	phase.Status = NormalizeContinuationLeaseStatus(phase.Status)
	if phase.Index < 0 {
		phase.Index = 0
	}
	return phase
}

func (b ContinuationApprovalBundle) Active() bool {
	return strings.TrimSpace(b.ID) != "" ||
		strings.TrimSpace(string(b.Status)) != "" ||
		strings.TrimSpace(b.CurrentPhaseID) != "" ||
		b.ApprovedBy > 0 ||
		len(b.Phases) > 0 ||
		!b.ExpiresAt.IsZero() ||
		!b.ApprovedAt.IsZero() ||
		!b.ConsumedAt.IsZero() ||
		!b.RevokedAt.IsZero()
}

func (p ContinuationApprovalBundlePhase) Active() bool {
	return strings.TrimSpace(p.ID) != "" ||
		strings.TrimSpace(p.OperationPhaseID) != "" ||
		p.Index > 0 ||
		strings.TrimSpace(p.OperatorTitle) != "" ||
		strings.TrimSpace(p.PlanTitle) != "" ||
		strings.TrimSpace(p.Summary) != "" ||
		strings.TrimSpace(p.AuthorityClass) != "" ||
		strings.TrimSpace(p.WhyNow) != "" ||
		strings.TrimSpace(p.BoundedEffect) != "" ||
		len(p.AllowedActions) > 0 ||
		len(p.ForbiddenActions) > 0 ||
		len(p.ValidationPlan) > 0 ||
		strings.TrimSpace(string(p.Status)) != ""
}

func NormalizeContinuationLease(lease ContinuationLease) ContinuationLease {
	lease.ID = strings.TrimSpace(lease.ID)
	lease.ProposalID = strings.TrimSpace(lease.ProposalID)
	lease.MissionID = strings.TrimSpace(lease.MissionID)
	lease.OperatorTitle = strings.TrimSpace(lease.OperatorTitle)
	lease.PlanTitle = strings.TrimSpace(lease.PlanTitle)
	lease.Status = NormalizeContinuationLeaseStatus(lease.Status)
	lease.LeaseClass = NormalizeContinuationLeaseClass(lease.LeaseClass)
	lease.AllowedActions = normalizeActionStringSlice(lease.AllowedActions)
	lease.ForbiddenActions = normalizeActionStringSlice(lease.ForbiddenActions)
	lease.AllowedActions = sanitizeAllowedActionsAgainstForbidden(lease.AllowedActions, lease.ForbiddenActions)
	lease.ValidationPlan = normalizeActionStringSlice(lease.ValidationPlan)
	if continuationLeaseClassContradictedByActions(lease.LeaseClass, lease.AllowedActions, lease.ForbiddenActions) {
		lease.LeaseClass = ""
		lease.Constraints = nil
	}
	if lease.LeaseClass == "" {
		lease.LeaseClass = InferContinuationLeaseClass("", lease.AllowedActions, "")
	}
	lease.Constraints = normalizeContinuationLeaseConstraints(lease.LeaseClass, lease.Constraints)
	lease.PlanHash = strings.TrimSpace(lease.PlanHash)
	if lease.MaxTurns < 0 {
		lease.MaxTurns = 0
	}
	if lease.RemainingTurns < 0 {
		lease.RemainingTurns = 0
	}
	if lease.MaxTurns == 0 && lease.RemainingTurns > 0 {
		lease.MaxTurns = lease.RemainingTurns
	}
	if !lease.ExpiresAt.IsZero() {
		lease.ExpiresAt = lease.ExpiresAt.UTC()
	}
	if !lease.CreatedAt.IsZero() {
		lease.CreatedAt = lease.CreatedAt.UTC()
	}
	if !lease.UpdatedAt.IsZero() {
		lease.UpdatedAt = lease.UpdatedAt.UTC()
	}
	if !lease.ApprovedAt.IsZero() {
		lease.ApprovedAt = lease.ApprovedAt.UTC()
	}
	if !lease.ConsumedAt.IsZero() {
		lease.ConsumedAt = lease.ConsumedAt.UTC()
	}
	if !lease.RevokedAt.IsZero() {
		lease.RevokedAt = lease.RevokedAt.UTC()
	}
	if lease.Status == "" && (lease.ID != "" || lease.ProposalID != "" || lease.RemainingTurns > 0 || lease.MaxTurns > 0) {
		lease.Status = ContinuationLeaseStatusPending
	}
	switch lease.Status {
	case ContinuationLeaseStatusConsumed, ContinuationLeaseStatusRevoked, ContinuationLeaseStatusExpired:
		lease.RemainingTurns = 0
	}
	if lease.CreatedAt.IsZero() && (lease.ID != "" || lease.ProposalID != "" || lease.Status != "") {
		lease.CreatedAt = time.Now().UTC()
	}
	if lease.UpdatedAt.IsZero() && (lease.ID != "" || lease.ProposalID != "" || lease.Status != "") {
		lease.UpdatedAt = time.Now().UTC()
	}
	return lease
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
	proposal.OperatorTitle = strings.TrimSpace(proposal.OperatorTitle)
	proposal.PlanTitle = strings.TrimSpace(proposal.PlanTitle)
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

func normalizeActionStringSlice(values []string) []string {
	out := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
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
