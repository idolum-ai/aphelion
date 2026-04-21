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

type MemoryReviewSnapshot struct {
	GeneratedAt time.Time
	Source      MemoryReviewSource
	Query       string
	Items       []MemoryReviewItem
}

type MemoryFocus struct {
	Source  MemoryReviewSource
	ItemID  string
	Label   string
	Excerpt string
	Query   string
	SetAt   time.Time
}

func (f MemoryFocus) Active() bool {
	return strings.TrimSpace(f.ItemID) != ""
}
