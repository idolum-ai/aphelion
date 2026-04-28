//go:build linux

package core

import "time"

type PendingItemKind string

const (
	PendingItemKindDecision     PendingItemKind = "decision"
	PendingItemKindContinuation PendingItemKind = "continuation"
	PendingItemKindReview       PendingItemKind = "review"
	PendingItemKindQueue        PendingItemKind = "queue"
	PendingItemKindRecovery     PendingItemKind = "recovery"
	PendingItemKindStaleTurn    PendingItemKind = "stale_turn"
)

type PendingItem struct {
	Kind          PendingItemKind
	ChatID        int64
	ID            string
	Summary       string
	Age           time.Duration
	CreatedAt     time.Time
	UpdatedAt     time.Time
	Stale         bool
	SourceClass   string
	SourceSurface string
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

type TurnRunStatusSnapshot struct {
	ID                    int64
	ChatID                int64
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
	ChatID           int64
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
	WatchdogTriggered  bool
	StaleTurnThreshold time.Duration
	StaleTurnLimit     int
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
	GrantID           string
	RequestID         string
	Kind              string
	TargetResource    string
	Status            string
	GrantedTo         string
	GrantedBy         string
	AllowedActions    []string
	AnchorFingerprint string
	DriftSource       string
	StaleReason       string
	InvocationCount   int
	FailureCount      int
	GrantedAt         time.Time
	ExpiresAt         time.Time
	RevokedAt         time.Time
	LastInvokedAt     time.Time
}

type ChatStatusSnapshot struct {
	GeneratedAt           time.Time
	ChatID                int64
	ActiveTurnIDs         []uint64
	QueueDepth            int
	TurnPhase             string
	TurnPhaseSummary      string
	TurnPhaseUpdatedAt    time.Time
	OperationStatus       string
	OperationStage        string
	OperationSummary      string
	PlanStepStatus        string
	PlanStep              string
	PlanCompletedSteps    int
	PlanTotalSteps        int
	PlanFullyExecuted     bool
	HiddenInputCategories []string
	HiddenInputSummary    string
	DeliveryStatus        string
	DeliverySummary       string
	PendingItems          []PendingItem
	Continuation          *ContinuationStatusSnapshot
	LatestTurnRun         *TurnRunStatusSnapshot
	RecentExecution       []ExecutionEventSummary
	ToolLifecycle         []ToolLifecycleStatusSnapshot
	CapabilityRequests    []CapabilityRequestStatusSnapshot
	CapabilityGrants      []CapabilityGrantStatusSnapshot
	StaleRunningTurns     []TurnRunStatusSnapshot
	RestartHealth         RestartHealthSnapshot
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
	GeneratedAt          time.Time
	ActiveTurnCount      int
	ActiveChatIDs        []int64
	ActiveTurnsByChat    map[int64][]uint64
	QueueDepthByChat     map[int64]int
	PendingItems         []PendingItem
	Continuations        []ContinuationStatusSnapshot
	LatestTurnRunsByChat map[int64]TurnRunStatusSnapshot
	RecentExecution      []ExecutionEventSummary
	StaleRunningTurns    []TurnRunStatusSnapshot
	HotChats             []ChatStatusRollup
	RestartHealth        RestartHealthSnapshot
	Tailnet              *TailnetStatusSnapshot
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
	ActiveTurnsByChat map[int64][]uint64
	QueueDepthByChat  map[int64]int
}
