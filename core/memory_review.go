//go:build linux

package core

import (
	"strings"
	"time"
)

type MemoryReviewSource string

const (
	MemoryReviewSourceSessionRecent  MemoryReviewSource = "session_recent"
	MemoryReviewSourceSemanticShared MemoryReviewSource = "semantic_shared"
	MemoryReviewSourceSemanticLocal  MemoryReviewSource = "semantic_local"
)

func NormalizeMemoryReviewSource(raw string) MemoryReviewSource {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case string(MemoryReviewSourceSessionRecent), "", "session":
		return MemoryReviewSourceSessionRecent
	case string(MemoryReviewSourceSemanticShared), "shared":
		return MemoryReviewSourceSemanticShared
	case string(MemoryReviewSourceSemanticLocal), "local", "principal":
		return MemoryReviewSourceSemanticLocal
	default:
		return MemoryReviewSourceSessionRecent
	}
}

type MemoryReviewItem struct {
	ID      string
	Label   string
	Excerpt string
	Score   float64
}

type MemoryReviewStats struct {
	StoreCounts         map[string]int
	SemanticSharedCount int
	SemanticLocalCount  int
	SessionRecentCount  int
	Partial             bool
	Missing             []string
}

type MemoryReviewSnapshot struct {
	GeneratedAt time.Time
	Source      MemoryReviewSource
	Query       string
	Items       []MemoryReviewItem
	Stats       MemoryReviewStats
}

type ContextSnapshot struct {
	GeneratedAt time.Time
	Chat        ChatStatusSnapshot
	Recent      []MemoryReviewItem
}
