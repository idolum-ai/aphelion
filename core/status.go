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
	ID             int64
	ChatID         int64
	Kind           string
	Status         string
	LastActivityAt time.Time
	LastToolName   string
	ErrorText      string
	StartedAt      time.Time
}

type ContinuationStatusSnapshot struct {
	ChatID         int64
	Status         string
	RemainingTurns int
	DecisionID     string
	ApprovedBy     int64
	UpdatedAt      time.Time
}

type RestartHealthSnapshot struct {
	WatchdogTriggered  bool
	StaleTurnThreshold time.Duration
	StaleTurnLimit     int
}

type ChatStatusSnapshot struct {
	GeneratedAt       time.Time
	ChatID            int64
	ActiveTurnIDs     []uint64
	QueueDepth        int
	PendingItems      []PendingItem
	Continuation      *ContinuationStatusSnapshot
	LatestTurnRun     *TurnRunStatusSnapshot
	StaleRunningTurns []TurnRunStatusSnapshot
	RestartHealth     RestartHealthSnapshot
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

type RouterStatusSnapshot struct {
	ActiveTurnsByChat map[int64][]uint64
	QueueDepthByChat  map[int64]int
}
