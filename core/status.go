//go:build linux

package core

import "time"

type PendingItemKind string

const (
	PendingItemKindDecision     PendingItemKind = "decision"
	PendingItemKindContinuation PendingItemKind = "continuation"
	PendingItemKindQueue        PendingItemKind = "queue"
	PendingItemKindRecovery     PendingItemKind = "recovery"
	PendingItemKindStaleTurn    PendingItemKind = "stale_turn"
)

type PendingItem struct {
	Kind      PendingItemKind
	ChatID    int64
	ID        string
	Summary   string
	Age       time.Duration
	CreatedAt time.Time
	UpdatedAt time.Time
	Stale     bool
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
}

type RestartHealthSnapshot struct {
	WatchdogTriggered  bool
	StaleTurnThreshold time.Duration
	StaleTurnLimit     int
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
	StaleRunningTurns    []TurnRunStatusSnapshot
	HotChats             []ChatStatusRollup
	RestartHealth        RestartHealthSnapshot
}

type DurableAgentStatusSnapshot struct {
	AgentID                      string
	ChannelKind                  string
	Status                       string
	Health                       string
	ReviewTargetChatID           int64
	ParentScopeKind              string
	ParentScopeID                string
	WakeupMode                   string
	NetworkPolicy                string
	PolicyVersion                int64
	PolicyHash                   string
	PolicyOutboundMode           string
	PolicyDrift                  string
	CapabilityEnvelope           []string
	AllowedTelegramUserIDs       []int64
	CapacityState                string
	CapacityCanCount             int
	CapacityCannotCount          int
	CapacityUncertainCount       int
	CapacitySuccessCriteriaCount int
	CapacityEvidenceSignalCount  int
	CapacityLastNegotiatedAt     time.Time
	CapacityLastProbedAt         time.Time
	CapacityLastAttestedAt       time.Time
	LastWakeAt                   time.Time
	LastReviewAt                 time.Time
	DormantAt                    time.Time
	LastAppliedPolicyVersion     int64
	LastAppliedPolicyAt          time.Time
	LastApplyStatus              string
	LastApplyError               string
	EnrollmentStatus             string
	EnrollmentLastSeenAt         time.Time
	EnrollmentLastSequence       int64
	EnrollmentRevokedAt          time.Time
	EnrollmentParentControlURL   string
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
