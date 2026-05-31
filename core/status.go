//go:build linux

package core

import "time"

type PendingItemKind string

const (
	PendingItemKindDecision     PendingItemKind = "decision"
	PendingItemKindContinuation PendingItemKind = "continuation"
	PendingItemKindReview       PendingItemKind = "review"
	PendingItemKindMission      PendingItemKind = "mission"
	PendingItemKindQueue        PendingItemKind = "queue"
	PendingItemKindRecovery     PendingItemKind = "recovery"
	PendingItemKindStaleTurn    PendingItemKind = "stale_turn"
)

type PendingItem struct {
	Kind            PendingItemKind
	ChatID          int64
	SessionID       string
	ScopeKind       string
	ScopeID         string
	DurableAgentID  string
	ID              string
	Summary         string
	Age             time.Duration
	CreatedAt       time.Time
	UpdatedAt       time.Time
	Stale           bool
	SourceClass     string
	SourceSurface   string
	DebugBreadcrumb DebugBreadcrumb
}

type AuthorityFindingSnapshot struct {
	FindingID       string
	Code            string
	Severity        string
	SourceKind      string
	SourceID        string
	SessionID       string
	ChatID          int64
	Detail          string
	SuggestedRepair string
	ApplyAction     string
	ApplyScope      string
	Applicable      bool
}

type AuthorityStatusSnapshot struct {
	GeneratedAt            time.Time
	Status                 string
	ContinuationRecords    int
	OperationRecords       int
	PendingDecisions       int
	AutoApprovalLeases     int
	CapabilityGrants       int
	ActiveProposals        int
	ActiveLeases           int
	ActivePlanLeases       int
	FindingCount           int
	ErrorCount             int
	WarningCount           int
	Findings               []AuthorityFindingSnapshot
	TruncatedCapabilitySet bool
}

type ExecutionEventSummary struct {
	SessionID string
	ChatID    int64
	ScopeKind string
	ScopeID   string
	AgentID   string
	Seq       int64
	EventType string
	Stage     string
	Status    string
	Summary   string
	CreatedAt time.Time
}

type PerceptionBudgetStatusSnapshot struct {
	SessionID               string
	ChatID                  int64
	ScopeKind               string
	ScopeID                 string
	AgentID                 string
	Seq                     int64
	Posture                 string
	TotalBudgetTokens       int64
	TotalEstimatedTokens    int64
	MemoryBudgetTokens      int64
	MemoryEstimatedTokens   int64
	CurrentInputTokens      int64
	ToolEvidenceTokens      int64
	RemainingHeadroomTokens int64
	AdmittedLayers          []string
	SuppressedLayers        []string
	ObservedEvidenceSources []string
	Risks                   []string
	CreatedAt               time.Time
}

type AdjudicationStatusSnapshot struct {
	SessionID     string
	ChatID        int64
	Seq           int64
	Kind          string
	Surface       string
	SubjectID     string
	OperatorLabel string
	VisibleAction string
	Findings      []RuntimeFinding
	EvidenceRefs  []string
	CreatedAt     time.Time
}

type TurnRunStatusSnapshot struct {
	ID                    int64
	SessionID             string
	ChatID                int64
	ScopeKind             string
	ScopeID               string
	DurableAgentID        string
	Kind                  string
	Status                string
	RequestText           string
	LastActivityAt        time.Time
	ProgressMessageID     int64
	LastToolName          string
	LastToolPreview       string
	LastToolResultPreview string
	LastToolError         string
	ErrorText             string
	StartedAt             time.Time
	Source                string
}

type ContinuationStatusSnapshot struct {
	SessionID        string
	ChatID           int64
	ScopeKind        string
	ScopeID          string
	DurableAgentID   string
	Status           string
	RemainingTurns   int
	DecisionID       string
	ApprovedBy       int64
	PersonaIntent    string
	GovernorIntent   string
	GovernorRatified bool
	BlockedReason    string
	UpdatedAt        time.Time
	Source           string
}

type RestartHealthSnapshot struct {
	WatchdogEnabled              bool
	WatchdogTriggered            bool
	StaleTurnThreshold           time.Duration
	StaleTurnLimit               int
	LastWatchdogStatus           string
	LastWatchdogReason           string
	LastWatchdogAt               time.Time
	NextWatchdogAttemptAt        time.Time
	LastWatchdogStaleCount       int
	LastWatchdogInterruptedCount int
}

type ProviderHealthSnapshot struct {
	GeneratedAt         time.Time
	Window              time.Duration
	Status              string
	RecentFailures      int
	RecentRetries       int
	RecentFailovers     int
	RecentSuccesses     int
	LastEventAt         time.Time
	LastFailureAt       time.Time
	LastFailureProvider string
	LastFailureModel    string
	LastFailureReason   string
	LastFailureError    string
	LastSuccessAt       time.Time
}

type AutoApprovalStatusSnapshot struct {
	Active        bool
	Usable        bool
	BlockedReason string
	LeaseID       string
	AdminUserID   int64
	Scope         string
	UsedCount     int
	MaxUses       int
	Reason        string
	CreatedAt     time.Time
	UpdatedAt     time.Time
	ExpiresAt     time.Time
}

type ToolLifecycleStatusSnapshot struct {
	ToolName             string
	InstallStatus        string
	ProbeStatus          string
	AuditStatus          string
	InstallRef           string
	BaselineFingerprint  string
	CurrentFingerprint   string
	ManifestHash         string
	WorkspaceFingerprint string
	DriftSource          string
	StaleReason          string
	AttestationStatus    string
	InstallFailures      int
	ProbeFailures        int
	AuditFailures        int
	TraceStage           string
	TraceSummary         string
	TraceArtifactCount   int
	InstalledAt          time.Time
	LastProbedAt         time.Time
	AuditedAt            time.Time
	AttestedAt           time.Time
}

type CapabilityRequestStatusSnapshot struct {
	RequestID       string
	Kind            string
	TargetResource  string
	ReviewStatus    string
	RequestedBy     string
	RequestedFor    string
	ParentPrincipal string
	AdminPrincipal  string
	RiskClass       string
	Purpose         string
	GrantID         string
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

type CapabilityGrantStatusSnapshot struct {
	GrantID                string
	RequestID              string
	Kind                   string
	TargetResource         string
	Status                 string
	GrantedTo              string
	GrantedBy              string
	AllowedActions         []string
	AnchorFingerprint      string
	DriftSource            string
	StaleReason            string
	ToolInvocationScope    string
	ChildRuntimePresent    bool
	RuntimeMaterialMissing string
	InvocationCount        int
	FailureCount           int
	GrantedAt              time.Time
	ExpiresAt              time.Time
	RevokedAt              time.Time
	LastInvokedAt          time.Time
}

type ExternalToolInvocationReadinessSnapshot struct {
	GeneratedAt      time.Time
	ToolName         string
	ChildPrincipal   string
	Action           string
	SelectorName     string
	SelectorValue    string
	Ready            bool
	Status           string
	Why              string
	NextRepairAction string
}

type SandboxReadinessIssue struct {
	Role             string
	Mode             string
	Network          string
	Code             string
	Severity         string
	Summary          string
	NextRepairAction string
}

type SandboxReadinessSnapshot struct {
	GeneratedAt time.Time
	Issues      []SandboxReadinessIssue
}

type TelegramIngressFailureSnapshot struct {
	Surface    string
	UpdateID   int64
	UpdateKind string
	ChatID     int64
	SenderID   int64
	MessageID  int64
	ErrorText  string
	CreatedAt  time.Time
}

type TelegramIngressUpdateSnapshot struct {
	Surface     string
	UpdateID    int64
	UpdateKind  string
	ChatID      int64
	MessageID   int64
	SessionID   string
	Status      string
	TurnRunID   int64
	ErrorText   string
	AcceptedAt  time.Time
	QueuedAt    time.Time
	StartedAt   time.Time
	CompletedAt time.Time
	UpdatedAt   time.Time
}

type MissionLedgerStatusSnapshot struct {
	ActiveCount                  int
	CandidateCount               int
	PinnedCount                  int
	RecurringCount               int
	BlockedCount                 int
	SelfContinuationEnabledCount int
	StaleCandidateCount          int
	PendingHandoffCount          int
	WorkingObjective             string
}

type ChatStatusSnapshot struct {
	GeneratedAt                     time.Time
	ChatID                          int64
	SessionID                       string
	ScopeKind                       string
	ScopeID                         string
	DurableAgentID                  string
	ActiveTurnIDs                   []uint64
	QueueDepth                      int
	TurnPhase                       string
	TurnPhaseSummary                string
	TurnPhaseUpdatedAt              time.Time
	OperationStatus                 string
	OperationStage                  string
	OperationSummary                string
	PlanStepStatus                  string
	PlanStep                        string
	PlanCompletedSteps              int
	PlanTotalSteps                  int
	PlanFullyExecuted               bool
	HiddenInputCategories           []string
	HiddenInputSummary              string
	DeliveryStatus                  string
	DeliverySummary                 string
	PendingItems                    []PendingItem
	Continuation                    *ContinuationStatusSnapshot
	LatestTurnRun                   *TurnRunStatusSnapshot
	LatestPerceptionBudget          *PerceptionBudgetStatusSnapshot
	RecentExecution                 []ExecutionEventSummary
	RecentAdjudications             []AdjudicationStatusSnapshot
	ToolLifecycle                   []ToolLifecycleStatusSnapshot
	CapabilityRequests              []CapabilityRequestStatusSnapshot
	CapabilityGrants                []CapabilityGrantStatusSnapshot
	ExternalToolInvocationReadiness []ExternalToolInvocationReadinessSnapshot
	AutoApproval                    *AutoApprovalStatusSnapshot
	StaleRunningTurns               []TurnRunStatusSnapshot
	RestartHealth                   RestartHealthSnapshot
	MissionLedger                   MissionLedgerStatusSnapshot
	Authority                       AuthorityStatusSnapshot
}

type ChatStatusRollup struct {
	ChatID          int64
	ActiveTurnCount int
	QueueDepth      int
	PendingCount    int
	LatestStatus    string
	LastActivityAt  time.Time
}

type SystemStatusSnapshot struct {
	GeneratedAt                  time.Time
	ActiveTurnCount              int
	ActiveChatIDs                []int64
	ActiveTurnsByChat            map[int64][]uint64
	QueueDepthByChat             map[int64]int
	TotalQueuedMessages          int
	MaxQueueDepth                int
	MaxQueueDepthChatID          int64
	OldestQueuedAge              time.Duration
	OldestQueuedChatID           int64
	PendingItems                 []PendingItem
	Continuations                []ContinuationStatusSnapshot
	LatestTurnRunsByChat         map[int64]TurnRunStatusSnapshot
	LatestPerceptionBudgetByChat map[int64]PerceptionBudgetStatusSnapshot
	RecentExecution              []ExecutionEventSummary
	RecentAdjudications          []AdjudicationStatusSnapshot
	StaleRunningTurns            []TurnRunStatusSnapshot
	HotChats                     []ChatStatusRollup
	RestartHealth                RestartHealthSnapshot
	ProviderHealth               ProviderHealthSnapshot
	Tailnet                      *TailnetStatusSnapshot
	Autonomy                     AutonomyStatusSnapshot
	Sandbox                      SandboxReadinessSnapshot
	TelegramIngressUpdates       []TelegramIngressUpdateSnapshot
	TelegramIngress              []TelegramIngressFailureSnapshot
	MissionLedger                MissionLedgerStatusSnapshot
	Authority                    AuthorityStatusSnapshot
}

type AutonomyStatusSnapshot struct {
	GeneratedAt          time.Time
	DefaultMode          string
	Ceiling              string
	AllowLiveOverrides   bool
	MaxOverrideDuration  time.Duration
	ActiveOverrideMode   string
	ActiveOverrideActor  string
	ActiveOverrideScope  string
	ActiveOverrideExpiry time.Time
	Source               string
	AuthorityBehavior    string
}

type TailnetStatusSnapshot struct {
	GeneratedAt       time.Time
	Enabled           bool
	Backend           string
	Status            string
	Summary           string
	TailscaleVersion  string
	BackendState      string
	HostName          string
	DNSName           string
	TailnetName       string
	User              string
	Online            bool
	Authenticated     bool
	TailscaleIPs      []string
	Tags              []string
	MagicDNSEnabled   bool
	NetcheckAvailable bool
	NetcheckSummary   string
	ExpectedTailnet   string
	ExpectedHostname  string
	ExpectedTags      []string
	Parent            *TailnetParentStatus
	Surfaces          []TailnetSurfaceStatus
	GrantBindings     []TailnetGrantBindingStatus
	Issues            []TailnetIssue
	RawStatusError    string
	RawIPError        string
	RawNetcheckError  string
}

type TailnetIssue struct {
	Code     string
	Severity string
	Summary  string
}

type TailnetParentStatus struct {
	Enabled       bool
	Running       bool
	Hostname      string
	StateDir      string
	ListenAddr    string
	MagicDNSURL   string
	AuthKeySource string
	Tags          []string
	LastError     string
}

type TailnetSurfaceStatus struct {
	SurfaceID      string
	OwnerKind      string
	OwnerID        string
	SurfaceKind    string
	Name           string
	Hostname       string
	TailnetName    string
	ListenAddr     string
	URL            string
	Tags           []string
	Status         string
	LastError      string
	DeclaredAt     time.Time
	ActivatedAt    time.Time
	LastObservedAt time.Time
	RevokedAt      time.Time
	UpdatedAt      time.Time
}

type TailnetGrantBindingStatus struct {
	BindingID          string
	GrantID            string
	SurfaceID          string
	GrantedTo          string
	CapabilityKind     string
	TargetResource     string
	DesiredPolicyJSON  string
	AppliedPolicyHash  string
	ObservedPolicyHash string
	Status             string
	DriftReason        string
	CreatedAt          time.Time
	UpdatedAt          time.Time
	AppliedAt          time.Time
	RevokedAt          time.Time
}

type DurableAgentStatusSnapshot struct {
	AgentID                    string
	ChannelKind                string
	Status                     string
	Health                     string
	ReviewTargetChatID         int64
	ParentScopeKind            string
	ParentScopeID              string
	WakeupMode                 string
	NetworkPolicy              string
	PolicyVersion              int64
	PolicyHash                 string
	PolicyOutboundMode         string
	PolicyDrift                string
	CapabilityEnvelope         []string
	AllowedTelegramUserIDs     []int64
	LastWakeAt                 time.Time
	LastReviewAt               time.Time
	DormantAt                  time.Time
	LastAppliedPolicyVersion   int64
	LastAppliedPolicyAt        time.Time
	LastApplyStatus            string
	LastApplyError             string
	EnrollmentStatus           string
	EnrollmentLastSeenAt       time.Time
	EnrollmentLastSequence     int64
	EnrollmentRevokedAt        time.Time
	EnrollmentParentControlURL string
	IdentitySource             string
	RuntimePostureSource       string
	CanonicalPrincipal         string
	ChildRuntimeGrantCount     int
	ChildRuntimeBlockedReason  string
	ChildRuntimeRepairHint     string
	SubstrateLabels            []string
	TailnetMode                string
	TailnetHostname            string
	TailnetTags                []string
	TailnetSurfacePolicy       string
	TailnetSurfaceID           string
	ProfileManifestStatus      string
	ProfileManifestPolicyHash  string
	ProfileManifestFileCount   int
}

type DurableAgentsStatusSnapshot struct {
	GeneratedAt    time.Time
	TotalAgents    int
	ActiveAgents   int
	DormantAgents  int
	DegradedAgents int
	InactiveAgents int
	Agents         []DurableAgentStatusSnapshot
}

type RouterStatusSnapshot struct {
	ActiveTurnsByChat   map[int64][]uint64
	QueueDepthByChat    map[int64]int
	TotalActiveTurns    int
	TotalQueuedMessages int
	MaxQueueDepth       int
	MaxQueueDepthChatID int64
	OldestQueuedAt      time.Time
	OldestQueuedAge     time.Duration
	OldestQueuedChatID  int64
}
