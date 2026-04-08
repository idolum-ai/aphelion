//go:build linux

package session

import "time"

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
	CreatedAt    time.Time
	UpdatedAt    time.Time
	TurnCount    int

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

// Message is one persisted conversation message.
type Message struct {
	ID           int64
	ChatID       int64
	UserID       int64
	Role         string
	Content      string
	ToolCalls    string
	ToolID       string
	ToolName     string
	Thinking     string
	CreatedAt    time.Time
	TurnIndex    int
	ContentChars int
	Compacted    bool
}
