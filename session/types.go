//go:build linux

package session

import "time"

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

// SessionKey identifies a unique session.
type SessionKey struct {
	ChatID int64
	UserID int64 // 0 for shared group sessions or DMs
}

// Session stores conversation state, cache metadata, and accounting.
type Session struct {
	ChatID       int64
	UserID       int64 // 0 for shared group sessions
	Messages     []Message
	SystemPrompt string
	// LastCanonicalReply stores the governor-canonical assistant text for audit.
	// The visible transcript in Messages stores the rendered Idolum reply.
	LastCanonicalReply string
	CreatedAt          time.Time
	UpdatedAt          time.Time
	TurnCount          int

	// Cache tracking
	CacheState CacheState

	// Compaction
	CompactionLog []CompactionEntry

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
	SourceChatID      int64
	SourceUserID      int64
	SourceRole        string
	TargetAdminChatID int64
	TurnFrom          int
	TurnTo            int
	Summary           string
	Status            string // "pending" | "delivered" | "dismissed"
	CreatedAt         time.Time
	DeliveredAt       time.Time
}

// TurnRun stores machine-authored facts about a turn lifecycle for recovery.
type TurnRun struct {
	ID                int64
	ChatID            int64
	UserID            int64
	Kind              TurnRunKind
	Status            TurnRunStatus
	RequestText       string
	StartedAt         time.Time
	CompletedAt       time.Time
	LastActivityAt    time.Time
	LastToolName      string
	LastToolPreview   string
	ToolCallsStarted  int
	ProgressMessageID int64
	ErrorText         string
	RecoverySummary   string
	RecoveryLoggedAt  time.Time
}

// Message is one persisted conversation message.
type Message struct {
	ID               int64
	ChatID           int64
	UserID           int64
	Role             string
	Content          string
	CanonicalContent string
	ToolCalls        string
	ToolID           string
	ToolName         string
	Thinking         string
	CreatedAt        time.Time
	TurnIndex        int
	ContentChars     int
	Compacted        bool
}
