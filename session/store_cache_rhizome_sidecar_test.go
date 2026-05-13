//go:build linux

package session

import (
	"github.com/idolum-ai/aphelion/core"
	_ "github.com/mattn/go-sqlite3"
	"path/filepath"
	"testing"
)

func TestSaveUpdatesCacheTotalsAndState(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	key := SessionKey{ChatID: 91, UserID: 0}
	sess, err := store.Load(key)
	if err != nil {
		t.Fatalf("Load() err = %v", err)
	}

	sess.TurnCount = 1
	if err := store.Save(sess, []Message{{Role: "assistant", Content: "first", TurnIndex: 1}}, core.TokenUsage{
		InputTokens:      10,
		OutputTokens:     2,
		CacheWriteTokens: 100,
	}); err != nil {
		t.Fatalf("Save(first) err = %v", err)
	}

	reloaded, err := store.Load(key)
	if err != nil {
		t.Fatalf("Load(reloaded) err = %v", err)
	}
	if reloaded.TotalCacheWrite != 100 {
		t.Fatalf("TotalCacheWrite = %d, want 100", reloaded.TotalCacheWrite)
	}
	if reloaded.CacheState.LastWriteBlock != 1 || reloaded.CacheState.BlocksSinceWrite != 0 {
		t.Fatalf("cache state after write = %#v", reloaded.CacheState)
	}

	reloaded.TurnCount = 2
	if err := store.Save(reloaded, []Message{{Role: "assistant", Content: "second", TurnIndex: 2}}, core.TokenUsage{
		InputTokens:     8,
		OutputTokens:    3,
		CacheReadTokens: 80,
	}); err != nil {
		t.Fatalf("Save(second) err = %v", err)
	}

	finalSession, err := store.Load(key)
	if err != nil {
		t.Fatalf("Load(final) err = %v", err)
	}
	if finalSession.TotalCacheRead != 80 {
		t.Fatalf("TotalCacheRead = %d, want 80", finalSession.TotalCacheRead)
	}
	if finalSession.CacheState.BlocksSinceWrite != 1 {
		t.Fatalf("BlocksSinceWrite = %d, want 1", finalSession.CacheState.BlocksSinceWrite)
	}
	if finalSession.CacheState.ConsecutiveMisses != 0 {
		t.Fatalf("ConsecutiveMisses = %d, want 0", finalSession.CacheState.ConsecutiveMisses)
	}
	if finalSession.CacheState.HitRate <= 0 {
		t.Fatalf("HitRate = %f, want positive", finalSession.CacheState.HitRate)
	}
}

func TestCompactMarksOldMessagesAndResetsCacheState(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	key := SessionKey{ChatID: 99, UserID: 0}
	sess, err := store.Load(key)
	if err != nil {
		t.Fatalf("Load() err = %v", err)
	}
	sess.TurnCount = 3
	sess.CacheState.LastWriteBlock = 3
	sess.CacheState.BlocksSinceWrite = 2
	sess.CacheState.HitRate = 0.5
	sess.CacheState.ConsecutiveMisses = 2
	if err := store.Save(sess, []Message{
		{Role: "user", Content: "turn 1", TurnIndex: 1},
		{Role: "assistant", Content: "reply 1", TurnIndex: 1},
		{Role: "user", Content: "turn 2", TurnIndex: 2},
		{Role: "assistant", Content: "reply 2", TurnIndex: 2},
		{Role: "user", Content: "turn 3", TurnIndex: 3},
		{Role: "assistant", Content: "reply 3", TurnIndex: 3},
	}, core.TokenUsage{}); err != nil {
		t.Fatalf("Save() err = %v", err)
	}

	if err := store.Compact(key, "summary block", 3); err != nil {
		t.Fatalf("Compact() err = %v", err)
	}

	reloaded, err := store.Load(key)
	if err != nil {
		t.Fatalf("Load(reloaded) err = %v", err)
	}
	if len(reloaded.CompactionLog) != 1 {
		t.Fatalf("compaction log len = %d, want 1", len(reloaded.CompactionLog))
	}
	if reloaded.CompactionLog[0].Strategy != "summarize" {
		t.Fatalf("compaction strategy = %q, want summarize", reloaded.CompactionLog[0].Strategy)
	}
	if reloaded.CacheState.LastWriteBlock != 0 || reloaded.CacheState.BlocksSinceWrite != 0 || reloaded.CacheState.HitRate != 0 || reloaded.CacheState.ConsecutiveMisses != 0 {
		t.Fatalf("cache state after compact = %#v, want reset", reloaded.CacheState)
	}

	compacted := 0
	for _, msg := range reloaded.Messages {
		if msg.Compacted {
			compacted++
		}
	}
	if compacted == 0 {
		t.Fatal("compacted message count = 0, want old messages soft-deleted")
	}
}

func TestRhizomeEventRecordingAndProjectionEdges(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	if err := store.RecordRhizomeEvent("shared", "heartbeat", 1.0, []string{"governor", "memory", "reflection"}); err != nil {
		t.Fatalf("RecordRhizomeEvent(1) err = %v", err)
	}
	if err := store.RecordRhizomeEvent("shared", "heartbeat", 1.0, []string{"memory", "reflection"}); err != nil {
		t.Fatalf("RecordRhizomeEvent(2) err = %v", err)
	}

	edges, err := store.TopRhizomeEdges("shared", 10)
	if err != nil {
		t.Fatalf("TopRhizomeEdges() err = %v", err)
	}
	if len(edges) == 0 {
		t.Fatal("TopRhizomeEdges() returned no edges, want at least one")
	}
	if edges[0].LeftConcept != "memory" || edges[0].RightConcept != "reflection" {
		t.Fatalf("top edge = %#v, want memory/reflection strongest edge", edges[0])
	}
	if edges[0].RecurrenceCount != 2 {
		t.Fatalf("top edge recurrence = %d, want 2", edges[0].RecurrenceCount)
	}
}

func TestResetAllRhizomeClearsGraph(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	if err := store.RecordRhizomeEvent("shared", "heartbeat", 1.0, []string{"a", "b"}); err != nil {
		t.Fatalf("RecordRhizomeEvent() err = %v", err)
	}
	if err := store.ResetAllRhizome(); err != nil {
		t.Fatalf("ResetAllRhizome() err = %v", err)
	}

	edges, err := store.TopRhizomeEdges("shared", 10)
	if err != nil {
		t.Fatalf("TopRhizomeEdges() err = %v", err)
	}
	if len(edges) != 0 {
		t.Fatalf("edges len = %d, want 0 after reset", len(edges))
	}
}

func newTestSQLiteStore(t *testing.T) *SQLiteStore {
	t.Helper()

	dbPath := filepath.Join(t.TempDir(), "sessions.db")
	store, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	return store
}

func TestSaveAndLoadFloorSidecar(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	key := SessionKey{ChatID: 1234, UserID: 0}
	sess, err := store.Load(key)
	if err != nil {
		t.Fatalf("Load() err = %v", err)
	}

	sess.TurnCount = 1
	sess.LastFloorText = "governor canonical"
	if err := store.Save(sess, []Message{
		{
			Role:      "user",
			Content:   "hello",
			TurnIndex: 1,
		},
		{
			Role:         "assistant",
			Content:      "idolum rendered",
			FloorContent: "governor canonical",
			TurnIndex:    1,
		},
	}, core.TokenUsage{}); err != nil {
		t.Fatalf("Save() err = %v", err)
	}

	got, err := store.Load(key)
	if err != nil {
		t.Fatalf("Load() after save err = %v", err)
	}
	if got.LastFloorText != "governor canonical" {
		t.Fatalf("LastFloorText = %q, want governor canonical", got.LastFloorText)
	}
	if len(got.Messages) != 2 {
		t.Fatalf("messages len = %d, want 2", len(got.Messages))
	}
	if got.Messages[1].Content != "idolum rendered" {
		t.Fatalf("assistant visible content = %q, want idolum rendered", got.Messages[1].Content)
	}
	if got.Messages[1].FloorContent != "governor canonical" {
		t.Fatalf("assistant floor content = %q, want governor canonical", got.Messages[1].FloorContent)
	}
}

func TestSavePersistsSessionScopeMetadata(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	key := SessionKey{
		ChatID: 5001,
		Scope: ScopeRef{
			Kind: ScopeKindHeartbeat,
			ID:   "admin-house",
		},
	}
	sess, err := store.Load(key)
	if err != nil {
		t.Fatalf("Load() err = %v", err)
	}

	sess.Scope = key.Scope
	sess.TurnCount = 1
	if err := store.Save(sess, []Message{{Role: "assistant", Content: "ok", TurnIndex: 1}}, core.TokenUsage{}); err != nil {
		t.Fatalf("Save() err = %v", err)
	}

	got, err := store.Load(key)
	if err != nil {
		t.Fatalf("Load(reloaded) err = %v", err)
	}
	if got.Scope.Kind != ScopeKindHeartbeat || got.Scope.ID != "admin-house" {
		t.Fatalf("Scope = %#v, want heartbeat admin-house", got.Scope)
	}
}

func stringSliceContains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
