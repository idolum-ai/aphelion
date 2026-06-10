//go:build linux

package session

import (
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
)

type TurnRunKind string

type TurnRunStatus string

type ScopeKind string

type ScopeRef struct {
	Kind            ScopeKind
	ID              string
	DurableAgentID  string
	ParentScopeKind ScopeKind
	ParentScopeID   string
}

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
	DeliveryMessageID int64
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
	ID                       int64
	SessionID                string
	ChatID                   int64
	UserID                   int64
	Scope                    ScopeRef
	Kind                     TurnRunKind
	TurnIndex                int
	Status                   TurnRunStatus
	RequestText              string
	StartedAt                time.Time
	CompletedAt              time.Time
	LastActivityAt           time.Time
	LastToolName             string
	LastToolPreview          string
	ToolCallsStarted         int
	ToolCallsFinished        int
	TotalToolCharsIn         int64
	TotalAssistantCharsOut   int64
	ProviderInputTokens      int64
	ProviderOutputTokens     int64
	ProviderCacheReadTokens  int64
	ProviderCacheWriteTokens int64
	LastToolResultPreview    string
	LastToolError            string
	ProgressMessageID        int64
	ErrorText                string
	RecoverySummary          string
	RecoveryLoggedAt         time.Time
}

const (
	TurnProgressViewSummary = "summary"
	TurnProgressViewDetails = "details"
)

type TurnProgressViewState struct {
	RunID        int64
	MessageID    int64
	SelectedView string
	SummaryText  string
	DetailsText  string
	UpdatedAt    time.Time
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
	SessionID         string
	ScopeKind         string
	ScopeID           string
	DurableAgentID    string
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
	SessionID          string
	ScopeKind          string
	ScopeID            string
	DurableAgentID     string
	MessageID          int64
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
	SessionID          string
	ScopeKind          string
	ScopeID            string
	DurableAgentID     string
	MessageID          int64
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
