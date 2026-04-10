//go:build linux

package session

import (
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/idolum-ai/aphelion/core"
	_ "github.com/mattn/go-sqlite3"
)

func TestSQLiteStoreCreatesReviewEventsTable(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	var count int
	err := store.db.QueryRow(`
		SELECT COUNT(1)
		FROM sqlite_master
		WHERE type = 'table' AND name = 'review_events'
	`).Scan(&count)
	if err != nil {
		t.Fatalf("query sqlite_master: %v", err)
	}
	if count != 1 {
		t.Fatalf("review_events table count = %d, want 1", count)
	}
}

func TestReviewEventsPendingOrderingAndFiltering(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	target := int64(700)
	otherTarget := int64(701)
	for _, event := range []ReviewEvent{
		{
			SourceChatID:      10,
			SourceUserID:      0,
			SourceRole:        "approved_user",
			TargetAdminChatID: target,
			TurnFrom:          1,
			TurnTo:            3,
			Summary:           "first",
		},
		{
			SourceChatID:      11,
			SourceUserID:      0,
			SourceRole:        "approved_user",
			TargetAdminChatID: otherTarget,
			TurnFrom:          4,
			TurnTo:            5,
			Summary:           "wrong target",
		},
		{
			SourceChatID:      12,
			SourceUserID:      0,
			SourceRole:        "approved_user",
			TargetAdminChatID: target,
			TurnFrom:          6,
			TurnTo:            8,
			Summary:           "second",
		},
		{
			SourceChatID:      13,
			SourceUserID:      0,
			SourceRole:        "approved_user",
			TargetAdminChatID: target,
			TurnFrom:          9,
			TurnTo:            10,
			Summary:           "already delivered",
			Status:            "delivered",
		},
	} {
		if err := store.EnqueueReviewEvent(event); err != nil {
			t.Fatalf("EnqueueReviewEvent() err = %v", err)
		}
	}

	pending, err := store.PendingReviewEvents(target, 10)
	if err != nil {
		t.Fatalf("PendingReviewEvents() err = %v", err)
	}

	if len(pending) != 2 {
		t.Fatalf("pending len = %d, want 2", len(pending))
	}
	if pending[0].Summary != "first" || pending[1].Summary != "second" {
		t.Fatalf("pending summaries = [%q, %q], want [first, second]", pending[0].Summary, pending[1].Summary)
	}
	if pending[0].ID >= pending[1].ID {
		t.Fatalf("pending IDs not ordered: first=%d second=%d", pending[0].ID, pending[1].ID)
	}
}

func TestReviewEventsLimitAndMarkDelivered(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	target := int64(800)
	for _, summary := range []string{"one", "two", "three"} {
		if err := store.EnqueueReviewEvent(ReviewEvent{
			SourceChatID:      20,
			SourceRole:        "approved_user",
			TargetAdminChatID: target,
			Summary:           summary,
		}); err != nil {
			t.Fatalf("EnqueueReviewEvent() err = %v", err)
		}
	}

	firstBatch, err := store.PendingReviewEvents(target, 2)
	if err != nil {
		t.Fatalf("PendingReviewEvents(limit=2) err = %v", err)
	}
	if len(firstBatch) != 2 {
		t.Fatalf("first batch len = %d, want 2", len(firstBatch))
	}

	if err := store.MarkReviewDelivered([]int64{firstBatch[0].ID}); err != nil {
		t.Fatalf("MarkReviewDelivered() err = %v", err)
	}

	var status string
	var deliveredAt string
	if err := store.db.QueryRow(`
		SELECT status, COALESCE(delivered_at, '')
		FROM review_events
		WHERE id = ?
	`, firstBatch[0].ID).Scan(&status, &deliveredAt); err != nil {
		t.Fatalf("query delivered review event: %v", err)
	}
	if status != "delivered" {
		t.Fatalf("status = %q, want delivered", status)
	}
	if deliveredAt == "" {
		t.Fatal("delivered_at is empty, want populated timestamp")
	}

	remaining, err := store.PendingReviewEvents(target, 10)
	if err != nil {
		t.Fatalf("PendingReviewEvents() err = %v", err)
	}
	if len(remaining) != 2 {
		t.Fatalf("remaining pending len = %d, want 2", len(remaining))
	}
	if remaining[0].Summary != "two" || remaining[1].Summary != "three" {
		t.Fatalf("remaining summaries = [%q, %q], want [two, three]", remaining[0].Summary, remaining[1].Summary)
	}
}

func TestSearchMessagesFiltersByScopeAndReturnsNewestFirst(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	for _, tc := range []struct {
		key      SessionKey
		turn     int
		userText string
		reply    string
	}{
		{SessionKey{ChatID: 1, UserID: 0}, 1, "alpha first", "reply one"},
		{SessionKey{ChatID: 1, UserID: 0}, 2, "alpha second", "reply two"},
		{SessionKey{ChatID: 2, UserID: 0}, 1, "beta alpha", "reply three"},
	} {
		sess, err := store.Load(tc.key)
		if err != nil {
			t.Fatalf("Load(%v) err = %v", tc.key, err)
		}
		sess.TurnCount = tc.turn
		if err := store.Save(sess, []Message{
			{Role: "user", Content: tc.userText, TurnIndex: tc.turn},
			{Role: "assistant", Content: tc.reply, CanonicalContent: tc.reply, TurnIndex: tc.turn},
		}, core.TokenUsage{}); err != nil {
			t.Fatalf("Save(%v) err = %v", tc.key, err)
		}
	}

	allHits, err := store.SearchMessages("alpha", 10, nil)
	if err != nil {
		t.Fatalf("SearchMessages(all) err = %v", err)
	}
	if len(allHits) != 3 {
		t.Fatalf("all hits len = %d, want 3", len(allHits))
	}
	if allHits[0].ChatID != 2 || allHits[1].TurnIndex != 2 {
		t.Fatalf("all hits ordering = %#v, want newest first", allHits)
	}

	scope := SessionKey{ChatID: 1, UserID: 0}
	scopedHits, err := store.SearchMessages("alpha", 10, &scope)
	if err != nil {
		t.Fatalf("SearchMessages(scoped) err = %v", err)
	}
	if len(scopedHits) != 2 {
		t.Fatalf("scoped hits len = %d, want 2", len(scopedHits))
	}
	for _, hit := range scopedHits {
		if hit.ChatID != 1 {
			t.Fatalf("scoped hit chat id = %d, want 1", hit.ChatID)
		}
	}
}

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

func TestSaveAndLoadCanonicalReplySidecar(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	key := SessionKey{ChatID: 1234, UserID: 0}
	sess, err := store.Load(key)
	if err != nil {
		t.Fatalf("Load() err = %v", err)
	}

	sess.TurnCount = 1
	sess.LastCanonicalReply = "governor canonical"
	if err := store.Save(sess, []Message{
		{
			Role:      "user",
			Content:   "hello",
			TurnIndex: 1,
		},
		{
			Role:             "assistant",
			Content:          "idolum rendered",
			CanonicalContent: "governor canonical",
			TurnIndex:        1,
		},
	}, core.TokenUsage{}); err != nil {
		t.Fatalf("Save() err = %v", err)
	}

	got, err := store.Load(key)
	if err != nil {
		t.Fatalf("Load() after save err = %v", err)
	}
	if got.LastCanonicalReply != "governor canonical" {
		t.Fatalf("LastCanonicalReply = %q, want governor canonical", got.LastCanonicalReply)
	}
	if len(got.Messages) != 2 {
		t.Fatalf("messages len = %d, want 2", len(got.Messages))
	}
	if got.Messages[1].Content != "idolum rendered" {
		t.Fatalf("assistant visible content = %q, want idolum rendered", got.Messages[1].Content)
	}
	if got.Messages[1].CanonicalContent != "governor canonical" {
		t.Fatalf("assistant canonical content = %q, want governor canonical", got.Messages[1].CanonicalContent)
	}
}

func TestInitMigratesLegacySessionsWithCanonicalColumn(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "legacy.db")
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("open legacy db: %v", err)
	}

	legacyDDL := []string{
		`CREATE TABLE schema_version (
			version INTEGER NOT NULL,
			applied_at TEXT NOT NULL DEFAULT (datetime('now'))
		)`,
		`INSERT INTO schema_version(version) VALUES (1)`,
		`CREATE TABLE sessions (
			chat_id INTEGER NOT NULL,
			user_id INTEGER NOT NULL DEFAULT 0,
			system_prompt TEXT,
			created_at TEXT NOT NULL DEFAULT (datetime('now')),
			updated_at TEXT NOT NULL DEFAULT (datetime('now')),
			turn_count INTEGER NOT NULL DEFAULT 0,
			chat_type TEXT NOT NULL DEFAULT 'dm',
			chat_title TEXT,
			user_name TEXT,
			cache_last_write_block INTEGER NOT NULL DEFAULT 0,
			cache_blocks_since INTEGER NOT NULL DEFAULT 0,
			cache_last_write_time TEXT,
			cache_hit_rate REAL NOT NULL DEFAULT 0.0,
			cache_consecutive_misses INTEGER NOT NULL DEFAULT 0,
			total_input_tokens INTEGER NOT NULL DEFAULT 0,
			total_output_tokens INTEGER NOT NULL DEFAULT 0,
			total_cache_read INTEGER NOT NULL DEFAULT 0,
			total_cache_write INTEGER NOT NULL DEFAULT 0,
			last_provider TEXT,
			last_model TEXT,
			active_tool_calls INTEGER NOT NULL DEFAULT 0,
			last_error TEXT,
			PRIMARY KEY (chat_id, user_id)
		)`,
	}
	for _, ddl := range legacyDDL {
		if _, err := db.Exec(ddl); err != nil {
			t.Fatalf("apply legacy ddl: %v", err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close legacy db: %v", err)
	}

	store, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore() migration err = %v", err)
	}
	defer store.Close()

	var hasColumn int
	err = store.db.QueryRow(`
			SELECT COUNT(1)
			FROM pragma_table_info('sessions')
			WHERE name = 'last_canonical_reply'
		`).Scan(&hasColumn)
	if err != nil {
		t.Fatalf("query pragma_table_info: %v", err)
	}
	if hasColumn != 1 {
		t.Fatalf("last_canonical_reply column count = %d, want 1", hasColumn)
	}

	err = store.db.QueryRow(`
			SELECT COUNT(1)
			FROM pragma_table_info('messages')
			WHERE name = 'canonical_content'
		`).Scan(&hasColumn)
	if err != nil {
		t.Fatalf("query pragma_table_info(messages): %v", err)
	}
	if hasColumn != 1 {
		t.Fatalf("canonical_content column count = %d, want 1", hasColumn)
	}

	err = store.db.QueryRow(`
			SELECT COUNT(1)
			FROM sqlite_master
			WHERE type = 'table' AND name = 'turn_runs'
		`).Scan(&hasColumn)
	if err != nil {
		t.Fatalf("query sqlite_master(turn_runs): %v", err)
	}
	if hasColumn != 1 {
		t.Fatalf("turn_runs table count = %d, want 1", hasColumn)
	}

	var maxVersion int
	if err := store.db.QueryRow(`SELECT COALESCE(MAX(version), 0) FROM schema_version`).Scan(&maxVersion); err != nil {
		t.Fatalf("query schema_version: %v", err)
	}
	if maxVersion != schemaVersion {
		t.Fatalf("schema version max = %d, want %d", maxVersion, schemaVersion)
	}
}

func TestRecordOutboundAndQueryAfterTurn(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	key := SessionKey{ChatID: 77, UserID: 0}
	sess, err := store.Load(key)
	if err != nil {
		t.Fatalf("Load() err = %v", err)
	}
	sess.TurnCount = 2
	if err := store.Save(sess, nil, core.TokenUsage{}); err != nil {
		t.Fatalf("Save() err = %v", err)
	}

	if err := store.RecordOutbound(key, 1, 100, "text"); err != nil {
		t.Fatalf("RecordOutbound(turn=1) err = %v", err)
	}
	if err := store.RecordOutbound(key, 3, 101, "voice"); err != nil {
		t.Fatalf("RecordOutbound(turn=3) err = %v", err)
	}

	got, err := store.OutboundAfterTurn(key, 1)
	if err != nil {
		t.Fatalf("OutboundAfterTurn() err = %v", err)
	}
	if len(got) != 1 || got[0] != 101 {
		t.Fatalf("OutboundAfterTurn() = %#v, want [101]", got)
	}
}

func TestTurnRunLifecycleAndRecovery(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	key := SessionKey{ChatID: 900, UserID: 0}
	if _, err := store.Load(key); err != nil {
		t.Fatalf("Load() err = %v", err)
	}

	run, err := store.BeginTurnRun(key, TurnRunKindInteractive, "inspect repo")
	if err != nil {
		t.Fatalf("BeginTurnRun() err = %v", err)
	}
	if run.Status != TurnRunStatusRunning {
		t.Fatalf("begin status = %q, want running", run.Status)
	}

	if err := store.NoteTurnRunToolStart(run.ID, "exec", `{"command":"rg foo"}`); err != nil {
		t.Fatalf("NoteTurnRunToolStart() err = %v", err)
	}
	if err := store.UpdateTurnRunProgressMessage(run.ID, 12345); err != nil {
		t.Fatalf("UpdateTurnRunProgressMessage() err = %v", err)
	}

	interrupted, err := store.InterruptRunningTurnRuns()
	if err != nil {
		t.Fatalf("InterruptRunningTurnRuns() err = %v", err)
	}
	if len(interrupted) != 1 {
		t.Fatalf("interrupted len = %d, want 1", len(interrupted))
	}
	if interrupted[0].ID != run.ID {
		t.Fatalf("interrupted run id = %d, want %d", interrupted[0].ID, run.ID)
	}
	if interrupted[0].ToolCallsStarted != 1 {
		t.Fatalf("tool_calls_started = %d, want 1", interrupted[0].ToolCallsStarted)
	}
	if interrupted[0].ProgressMessageID != 12345 {
		t.Fatalf("progress_message_id = %d, want 12345", interrupted[0].ProgressMessageID)
	}

	pending, err := store.PendingRecoveryTurnRuns(10)
	if err != nil {
		t.Fatalf("PendingRecoveryTurnRuns() err = %v", err)
	}
	if len(pending) != 1 {
		t.Fatalf("pending len = %d, want 1", len(pending))
	}
	if pending[0].Status != TurnRunStatusInterrupted {
		t.Fatalf("pending status = %q, want interrupted", pending[0].Status)
	}

	if err := store.MarkTurnRunsRecovered([]int64{run.ID}, "check logs before retry"); err != nil {
		t.Fatalf("MarkTurnRunsRecovered() err = %v", err)
	}

	pending, err = store.PendingRecoveryTurnRuns(10)
	if err != nil {
		t.Fatalf("PendingRecoveryTurnRuns() after recovery err = %v", err)
	}
	if len(pending) != 0 {
		t.Fatalf("pending len after recovery = %d, want 0", len(pending))
	}
}

func TestCompleteTurnRun(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	key := SessionKey{ChatID: 901, UserID: 0}
	if _, err := store.Load(key); err != nil {
		t.Fatalf("Load() err = %v", err)
	}

	run, err := store.BeginTurnRun(key, TurnRunKindCron, "cron work")
	if err != nil {
		t.Fatalf("BeginTurnRun() err = %v", err)
	}
	if err := store.CompleteTurnRun(run.ID, TurnRunStatusCompleted, ""); err != nil {
		t.Fatalf("CompleteTurnRun() err = %v", err)
	}

	rows, err := store.db.Query(`
		SELECT
			id, chat_id, user_id, kind, status, request_text, started_at, completed_at,
			last_activity_at, last_tool_name, last_tool_preview, tool_calls_started,
			progress_message_id, error_text, recovery_summary, recovery_logged_at
		FROM turn_runs
		WHERE id = ?
	`, run.ID)
	if err != nil {
		t.Fatalf("query completed turn run: %v", err)
	}
	defer rows.Close()
	if !rows.Next() {
		t.Fatal("expected completed turn run row")
	}
	got, err := scanTurnRun(rows)
	if err != nil {
		t.Fatalf("scanTurnRun() err = %v", err)
	}
	if got.Status != TurnRunStatusCompleted {
		t.Fatalf("status = %q, want completed", got.Status)
	}
	if got.CompletedAt.IsZero() {
		t.Fatal("completed_at is zero, want populated timestamp")
	}
}
