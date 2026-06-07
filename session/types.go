//go:build linux

package session

const (
	TurnRunKindInteractive TurnRunKind = "interactive"
	TurnRunKindHeartbeat   TurnRunKind = "heartbeat"
	TurnRunKindCron        TurnRunKind = "cron"
	TurnRunKindRecovery    TurnRunKind = "recovery"
	TurnRunKindDoctor      TurnRunKind = "doctor"
)

const (
	TurnRunStatusRunning     TurnRunStatus = "running"
	TurnRunStatusCompleted   TurnRunStatus = "completed"
	TurnRunStatusFailed      TurnRunStatus = "failed"
	TurnRunStatusInterrupted TurnRunStatus = "interrupted"
)

const (
	ScopeKindTelegramDM     ScopeKind = "telegram_dm"
	ScopeKindTelegramGroup  ScopeKind = "telegram_group"
	ScopeKindTelegramThread ScopeKind = "telegram_thread"
	ScopeKindHeartbeat      ScopeKind = "heartbeat"
	ScopeKindCron           ScopeKind = "cron"
	ScopeKindRecovery       ScopeKind = "recovery"
	ScopeKindDurableAgent   ScopeKind = "durable_agent"
)

const (
	PlanStatusPending    PlanStatus = "pending"
	PlanStatusInProgress PlanStatus = "in_progress"
	PlanStatusCompleted  PlanStatus = "completed"
)

const (
	PlanEventKindToolUpdated        PlanEventKind = "tool_updated"
	PlanEventKindBrokerageSeed      PlanEventKind = "brokerage_seed"
	PlanEventKindRehydrated         PlanEventKind = "rehydrated"
	PlanEventKindPhaseEntered       PlanEventKind = "phase.entered"
	PlanEventKindPhaseCompleted     PlanEventKind = "phase.completed"
	PlanEventKindDirectionChanged   PlanEventKind = "direction.changed"
	PlanEventKindDependencyResolved PlanEventKind = "dependency.resolved"
)

const (
	OperationStatusIdle      OperationStatus = "idle"
	OperationStatusActive    OperationStatus = "active"
	OperationStatusBlocked   OperationStatus = "blocked"
	OperationStatusCompleted OperationStatus = "completed"
	OperationStatusFailed    OperationStatus = "failed"
)

const (
	ProposalStatusPending    ProposalStatus = "pending"
	ProposalStatusApproved   ProposalStatus = "approved"
	ProposalStatusDenied     ProposalStatus = "denied"
	ProposalStatusExpired    ProposalStatus = "expired"
	ProposalStatusSuperseded ProposalStatus = "superseded"
)

const (
	FindingConfidenceLow    FindingConfidence = "low"
	FindingConfidenceMedium FindingConfidence = "medium"
	FindingConfidenceHigh   FindingConfidence = "high"
)

const (
	ContinuationLeaseStatusPending  ContinuationLeaseStatus = "pending"
	ContinuationLeaseStatusActive   ContinuationLeaseStatus = "active"
	ContinuationLeaseStatusConsumed ContinuationLeaseStatus = "consumed"
	ContinuationLeaseStatusDeferred ContinuationLeaseStatus = "deferred"
	ContinuationLeaseStatusRevoked  ContinuationLeaseStatus = "revoked"
	ContinuationLeaseStatusExpired  ContinuationLeaseStatus = "expired"
)

const (
	ContinuationLeaseClassLocalWorkspace  ContinuationLeaseClass = "local_workspace"
	ContinuationLeaseClassDataAccess      ContinuationLeaseClass = "data_access"
	ContinuationLeaseClassChildWake       ContinuationLeaseClass = "child_wake"
	ContinuationLeaseClassCapabilityGrant ContinuationLeaseClass = "capability_grant"
	ContinuationLeaseClassDeployRestart   ContinuationLeaseClass = "deploy_restart"
)

const (
	PlanLeaseStatusProposed  PlanLeaseStatus = "proposed"
	PlanLeaseStatusApproved  PlanLeaseStatus = "approved"
	PlanLeaseStatusActive    PlanLeaseStatus = "active"
	PlanLeaseStatusPaused    PlanLeaseStatus = "paused"
	PlanLeaseStatusRevoked   PlanLeaseStatus = "revoked"
	PlanLeaseStatusExpired   PlanLeaseStatus = "expired"
	PlanLeaseStatusCompleted PlanLeaseStatus = "completed"
)

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

const (
	CapabilityReviewStatusProposed       CapabilityReviewStatus = "proposed"
	CapabilityReviewStatusParentApproved CapabilityReviewStatus = "parent_approved"
	CapabilityReviewStatusApproved       CapabilityReviewStatus = "approved"
	CapabilityReviewStatusRejected       CapabilityReviewStatus = "rejected"
)

const (
	CapabilityGrantStatusPending CapabilityGrantStatus = "pending"
	CapabilityGrantStatusActive  CapabilityGrantStatus = "active"
	CapabilityGrantStatusStale   CapabilityGrantStatus = "stale"
	CapabilityGrantStatusRevoked CapabilityGrantStatus = "revoked"
	CapabilityGrantStatusExpired CapabilityGrantStatus = "expired"
	CapabilityGrantStatusFailed  CapabilityGrantStatus = "failed"
)

const (
	DurableChildAgreementStatusProposed   DurableChildAgreementStatus = "proposed"
	DurableChildAgreementStatusApproved   DurableChildAgreementStatus = "approved"
	DurableChildAgreementStatusRejected   DurableChildAgreementStatus = "rejected"
	DurableChildAgreementStatusSuperseded DurableChildAgreementStatus = "superseded"
)

const (
	ToolInstallStatusPending   ToolInstallStatus = "pending"
	ToolInstallStatusInstalled ToolInstallStatus = "installed"
	ToolInstallStatusVerified  ToolInstallStatus = "verified"
	ToolInstallStatusFailed    ToolInstallStatus = "failed"
	ToolInstallStatusStale     ToolInstallStatus = "stale"
)

const (
	ToolProbeStatusPassed ToolProbeStatus = "passed"
	ToolProbeStatusFailed ToolProbeStatus = "failed"
)

const (
	ToolAuditStatusPassed ToolAuditStatus = "passed"
	ToolAuditStatusFailed ToolAuditStatus = "failed"
)

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
	ToolDriftSourceRollback          ToolDriftSource = "rollback"
	ToolDriftSourceRemoval           ToolDriftSource = "removal"
)

const (
	TurnAuthorizationKindContinuation TurnAuthorizationKind = "continuation"
)

const (
	TurnAuthorizationStatusIdle     TurnAuthorizationStatus = "idle"
	TurnAuthorizationStatusPending  TurnAuthorizationStatus = "pending"
	TurnAuthorizationStatusApproved TurnAuthorizationStatus = "approved"
	TurnAuthorizationStatusRevoked  TurnAuthorizationStatus = "revoked"
)

const (
	ContinuationIntentDecisionContinue ContinuationIntentDecision = "continue"
	ContinuationIntentDecisionStop     ContinuationIntentDecision = "stop"
	ContinuationIntentDecisionHold     ContinuationIntentDecision = "hold"
)

const (
	ContinuationStatusIdle     = TurnAuthorizationStatusIdle
	ContinuationStatusPending  = TurnAuthorizationStatusPending
	ContinuationStatusApproved = TurnAuthorizationStatusApproved
	ContinuationStatusRevoked  = TurnAuthorizationStatusRevoked
)

const (
	OperatorAutoApprovalScopeAll       = "all"
	OperatorAutoApprovalScopeWorkspace = "workspace"
	OperatorAutoApprovalScopeDeploy    = "deploy"
)
