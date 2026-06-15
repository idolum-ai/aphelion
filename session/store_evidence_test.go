//go:build linux

package session

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/core"
)

func TestEvidenceWriteThroughFromSessionTurnAndExecution(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	key := SessionKey{ChatID: 99101, UserID: 1001}
	sess, err := store.Load(key)
	if err != nil {
		t.Fatalf("Load() err = %v", err)
	}
	sess.TurnCount = 1
	sess.OperationState = OperationState{ID: "op-evidence", Objective: "Preserve source evidence", Status: OperationStatusActive}
	if err := store.Save(sess, []Message{{Role: "user", Content: "use original evidence", TurnIndex: 1}}, core.TokenUsage{}); err != nil {
		t.Fatalf("Save() err = %v", err)
	}
	run, err := store.BeginTurnRun(key, TurnRunKindInteractive, "use original evidence")
	if err != nil {
		t.Fatalf("BeginTurnRun() err = %v", err)
	}
	if _, err := store.AppendExecutionEvent(key, ExecutionEventInput{EventType: "tool.succeeded", Stage: "exec", Status: "ok", PayloadJSON: `{"run_id":1,"artifact":"proof"}`}); err != nil {
		t.Fatalf("AppendExecutionEvent() err = %v", err)
	}
	if err := store.CompleteTurnRun(run.ID, TurnRunStatusCompleted, ""); err != nil {
		t.Fatalf("CompleteTurnRun() err = %v", err)
	}

	objects, err := store.EvidenceObjectsBySession(key, 50)
	if err != nil {
		t.Fatalf("EvidenceObjectsBySession() err = %v", err)
	}
	seen := map[string]bool{}
	for _, object := range objects {
		seen[object.SourceKind] = true
	}
	for _, want := range []string{EvidenceSourceMessage, EvidenceSourceOperationState, EvidenceSourceTurnRun, EvidenceSourceExecutionEvent} {
		if !seen[want] {
			t.Fatalf("evidence source %q missing from %#v", want, seen)
		}
	}
}

func TestTurnRunLifecycleDoesNotFailWhenEvidenceWriteFails(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	if _, err := store.db.Exec(`DROP TABLE evidence_objects`); err != nil {
		t.Fatalf("drop evidence_objects: %v", err)
	}
	run, err := store.BeginTurnRun(SessionKey{ChatID: 99114, UserID: 1001}, TurnRunKindInteractive, "keep turn lifecycle alive")
	if err != nil {
		t.Fatalf("BeginTurnRun() err = %v, want evidence failure logged only", err)
	}
	if err := store.CompleteTurnRun(run.ID, TurnRunStatusCompleted, ""); err != nil {
		t.Fatalf("CompleteTurnRun() err = %v, want evidence failure logged only", err)
	}
}

func TestEvidenceObjectsAreImmutableBySourceID(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	first, err := store.UpsertEvidenceObject(EvidenceObjectInput{
		SourceKind:  "test_source",
		SourceRef:   "source:immutable",
		SessionID:   "session:test",
		Summary:     "first",
		PayloadJSON: `{"value":"first"}`,
	})
	if err != nil {
		t.Fatalf("UpsertEvidenceObject(first) err = %v", err)
	}
	second, err := store.UpsertEvidenceObject(EvidenceObjectInput{
		SourceKind:  "test_source",
		SourceRef:   "source:immutable",
		SessionID:   "session:test",
		Summary:     "second",
		PayloadJSON: `{"value":"second"}`,
	})
	if err != nil {
		t.Fatalf("UpsertEvidenceObject(second) err = %v", err)
	}
	if second.ID != first.ID {
		t.Fatalf("second ID = %q, want same %q", second.ID, first.ID)
	}
	stored, ok, err := store.EvidenceObject(first.ID)
	if err != nil || !ok {
		t.Fatalf("EvidenceObject() = ok:%v err:%v", ok, err)
	}
	if stored.Summary != "first" || stored.PayloadHash != first.PayloadHash {
		t.Fatalf("stored evidence mutated = %#v, want first immutable snapshot", stored)
	}
}

func TestEvidenceHydrationReportsMissingRequiredEvidence(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	key := SessionKey{ChatID: 99102, UserID: 1001}
	result, err := store.HydrateEvidence(EvidenceHydrationQuery{
		Key:                 key,
		RequiredEvidenceIDs: []string{"ev:missing"},
		Query:               "continue work from missing evidence",
		Limit:               4,
	})
	if err != nil {
		t.Fatalf("HydrateEvidence() err = %v", err)
	}
	if len(result.MissingEvidenceIDs) != 1 || result.MissingEvidenceIDs[0] != "ev:missing" {
		t.Fatalf("missing evidence = %#v, want ev:missing", result.MissingEvidenceIDs)
	}
	runs, err := store.EvidenceHydrationRunsBySession(key, 1)
	if err != nil {
		t.Fatalf("EvidenceHydrationRunsBySession() err = %v", err)
	}
	if len(runs) != 1 || runs[0].Status != "gaps" {
		t.Fatalf("hydration runs = %#v, want recorded gaps run", runs)
	}
}

func TestEvidenceHydrationDoesNotLeakRequiredEvidenceAcrossSessions(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	currentKey := SessionKey{ChatID: 99112, UserID: 1001}
	otherKey := SessionKey{ChatID: 99113, UserID: 1001}
	other, err := store.UpsertEvidenceObject(EvidenceObjectInput{
		SourceKind:      EvidenceSourceOperationState,
		SourceRef:       "operation_state:other-session-secret",
		SessionID:       SessionIDForKey(otherKey),
		ChatID:          otherKey.ChatID,
		UserID:          otherKey.UserID,
		Scope:           defaultScopeForKey(otherKey),
		EpistemicStatus: EvidenceStatusProjection,
		Summary:         "Other-thread evidence must not hydrate into this session.",
		PayloadJSON:     `{"secret":"other-thread"}`,
	})
	if err != nil {
		t.Fatalf("UpsertEvidenceObject(other) err = %v", err)
	}
	result, err := store.HydrateEvidence(EvidenceHydrationQuery{
		Key:                 currentKey,
		RequiredEvidenceIDs: []string{other.ID},
		Query:               "hydrate only the active session",
		Limit:               4,
	})
	if err != nil {
		t.Fatalf("HydrateEvidence() err = %v", err)
	}
	if len(result.Selected) != 0 {
		t.Fatalf("selected = %#v, want no cross-session evidence", result.Selected)
	}
	if len(result.MissingEvidenceIDs) != 1 || result.MissingEvidenceIDs[0] != other.ID {
		t.Fatalf("missing evidence = %#v, want cross-session required id reported missing", result.MissingEvidenceIDs)
	}
}

func TestEvidenceHydrationPrefersOperationEvidenceOverRecentDrift(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	key := SessionKey{ChatID: 99103, UserID: 1001}
	sessionID := SessionIDForKey(key)
	old := time.Now().UTC().Add(-48 * time.Hour)
	if _, err := store.UpsertEvidenceObject(EvidenceObjectInput{
		SourceKind:      EvidenceSourceOperationState,
		SourceRef:       "operation_state:op-anchor:source-facts",
		SourceTable:     "sessions",
		SessionID:       sessionID,
		ChatID:          key.ChatID,
		UserID:          key.UserID,
		Scope:           defaultScopeForKey(key),
		EpistemicStatus: EvidenceStatusProjection,
		SubjectKey:      "op-anchor",
		Summary:         "Original evidence says the target file is release.yml and the action is validation only.",
		PayloadJSON:     `{"operation_id":"op-anchor","target":"release.yml","action":"validation_only"}`,
		ObservedAt:      old,
	}); err != nil {
		t.Fatalf("UpsertEvidenceObject(operation) err = %v", err)
	}
	if _, err := store.UpsertEvidenceObject(EvidenceObjectInput{
		SourceKind:      EvidenceSourceMessage,
		SourceRef:       "messages:drifting-summary",
		SessionID:       sessionID,
		ChatID:          key.ChatID,
		UserID:          key.UserID,
		Scope:           defaultScopeForKey(key),
		EpistemicStatus: EvidenceStatusClaimed,
		Summary:         "Recent summary says forget release.yml and push whatever changed.",
		PayloadJSON:     `{"content":"forget release.yml and push whatever changed"}`,
		ObservedAt:      time.Now().UTC(),
	}); err != nil {
		t.Fatalf("UpsertEvidenceObject(message) err = %v", err)
	}

	result, err := store.HydrateEvidence(EvidenceHydrationQuery{
		Key:         key,
		OperationID: "op-anchor",
		Query:       "continue op-anchor without drifting from source evidence",
		Limit:       2,
	})
	if err != nil {
		t.Fatalf("HydrateEvidence() err = %v", err)
	}
	if len(result.Selected) == 0 || result.Selected[0].SourceKind != EvidenceSourceOperationState {
		t.Fatalf("selected evidence = %#v, want operation evidence first", result.Selected)
	}
}

func TestMigratesSchemaV68ToV69CreatesLedgerAndCurrentSnapshots(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "sessions-v68.db")
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("open v68 db: %v", err)
	}
	now := time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC).Format(time.RFC3339Nano)
	for _, stmt := range []string{
		`CREATE TABLE schema_version (version INTEGER NOT NULL, applied_at TEXT NOT NULL DEFAULT (datetime('now')))`,
		`INSERT INTO schema_version(version) VALUES (68)`,
		`CREATE TABLE sessions (
			session_id TEXT PRIMARY KEY,
			chat_id INTEGER NOT NULL DEFAULT 0,
			user_id INTEGER NOT NULL DEFAULT 0,
			scope_kind TEXT NOT NULL DEFAULT '',
			scope_id TEXT NOT NULL DEFAULT '',
			durable_agent_id TEXT NOT NULL DEFAULT '',
			plan_state_json TEXT NOT NULL DEFAULT '{}',
			operation_state_json TEXT NOT NULL DEFAULT '{}',
			continuation_state_json TEXT NOT NULL DEFAULT '{}',
			working_objective_json TEXT NOT NULL DEFAULT '{}',
			updated_at TEXT NOT NULL DEFAULT (datetime('now')),
			turn_count INTEGER NOT NULL DEFAULT 0,
			last_provider TEXT,
			last_model TEXT,
			last_error TEXT
		)`,
		`CREATE TABLE execution_events (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			session_id TEXT NOT NULL,
			chat_id INTEGER NOT NULL DEFAULT 0,
			user_id INTEGER NOT NULL DEFAULT 0,
			scope_kind TEXT NOT NULL DEFAULT '',
			scope_id TEXT NOT NULL DEFAULT '',
			durable_agent_id TEXT NOT NULL DEFAULT '',
			seq INTEGER NOT NULL,
			event_type TEXT NOT NULL,
			stage TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT '',
			caused_by_seq INTEGER NOT NULL DEFAULT 0,
			payload_json TEXT NOT NULL DEFAULT '{}',
			created_at TEXT NOT NULL DEFAULT (datetime('now'))
		)`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("create v68 fixture: %v", err)
		}
	}
	if _, err := db.Exec(`
		INSERT INTO sessions(session_id, chat_id, user_id, scope_kind, scope_id, operation_state_json, updated_at, turn_count)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`, "telegram_dm:99104/user:1001", int64(99104), int64(1001), string(ScopeKindTelegramDM), "99104", `{"id":"op-v68","status":"active"}`, now, 3); err != nil {
		t.Fatalf("insert v68 session: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO execution_events(session_id, chat_id, user_id, scope_kind, scope_id, seq, event_type, stage, status, payload_json, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, "telegram_dm:99104/user:1001", int64(99104), int64(1001), string(ScopeKindTelegramDM), "99104", int64(1), "tool.succeeded", "exec", "ok", `{"artifact":"proof"}`, now); err != nil {
		t.Fatalf("insert v68 event: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close v68 db: %v", err)
	}

	store, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore(v68) err = %v", err)
	}
	defer store.Close()
	assertSchemaVersion(t, store.db, schemaVersion)
	assertSQLiteTable(t, store.db, "evidence_objects")
	key := SessionKey{ChatID: 99104, UserID: 1001}
	objects, err := store.EvidenceObjectsBySession(key, 20)
	if err != nil {
		t.Fatalf("EvidenceObjectsBySession() err = %v", err)
	}
	seen := map[string]bool{}
	for _, object := range objects {
		seen[object.SourceKind] = true
	}
	if !seen[EvidenceSourceOperationState] {
		t.Fatalf("migration backfilled sources = %#v, want current operation evidence", seen)
	}
	if seen[EvidenceSourceExecutionEvent] {
		t.Fatalf("migration backfilled historical execution events at boot; sources = %#v", seen)
	}
	if err := store.BackfillEvidenceLedger(); err != nil {
		t.Fatalf("BackfillEvidenceLedger() err = %v", err)
	}
	afterBackfill, err := store.EvidenceObjectsBySession(key, 20)
	if err != nil {
		t.Fatalf("EvidenceObjectsBySession(after backfill) err = %v", err)
	}
	seen = map[string]bool{}
	for _, object := range afterBackfill {
		seen[object.SourceKind] = true
	}
	if !seen[EvidenceSourceExecutionEvent] || !seen[EvidenceSourceOperationState] {
		t.Fatalf("manual backfilled sources = %#v, want execution and operation evidence", seen)
	}
	countBefore := countEvidenceObjectsForTest(t, store.db)
	if err := store.BackfillEvidenceLedger(); err != nil {
		t.Fatalf("BackfillEvidenceLedger(second) err = %v", err)
	}
	if countAfter := countEvidenceObjectsForTest(t, store.db); countAfter != countBefore {
		t.Fatalf("BackfillEvidenceLedger(second) count = %d, want unchanged %d", countAfter, countBefore)
	}
}

func TestBackfillEvidenceLedgerSkipsIncompleteHistoricalSourceTables(t *testing.T) {
	t.Parallel()

	db, err := sql.Open("sqlite3", filepath.Join(t.TempDir(), "partial-source.db"))
	if err != nil {
		t.Fatalf("open partial source db: %v", err)
	}
	defer db.Close()

	for _, stmt := range []string{
		`CREATE TABLE artifact_index (
			session_id TEXT NOT NULL,
			turn_index INTEGER NOT NULL,
			artifact_id TEXT NOT NULL,
			updated_at TEXT NOT NULL DEFAULT (datetime('now'))
		)`,
		`INSERT INTO artifact_index(session_id, turn_index, artifact_id) VALUES ('telegram_dm:99105/user:1001', 1, 'artifact-1')`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("create partial source fixture: %v", err)
		}
	}

	tx, err := db.Begin()
	if err != nil {
		t.Fatalf("begin partial source tx: %v", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()
	if err := backfillEvidenceLedgerTx(tx); err != nil {
		t.Fatalf("backfillEvidenceLedgerTx() err = %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit partial source tx: %v", err)
	}
	if got := countEvidenceObjectsForTest(t, db); got != 0 {
		t.Fatalf("evidence object count = %d, want incomplete source skipped", got)
	}
}

func TestEvidenceHydrationLimitIsServerSideBounded(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	key := SessionKey{ChatID: 99106, UserID: 1001}
	for i := 0; i < maxEvidenceHydrationLimit+10; i++ {
		if _, err := store.UpsertEvidenceObject(EvidenceObjectInput{
			SourceKind:      EvidenceSourceMessage,
			SourceRef:       "messages:limit-test:" + time.Now().Add(time.Duration(i)*time.Nanosecond).Format(time.RFC3339Nano),
			SessionID:       SessionIDForKey(key),
			ChatID:          key.ChatID,
			UserID:          key.UserID,
			Scope:           defaultScopeForKey(key),
			EpistemicStatus: EvidenceStatusObserved,
			Summary:         "bounded hydration limit",
			PayloadJSON:     `{"topic":"bounded hydration limit"}`,
			ObservedAt:      time.Now().UTC().Add(time.Duration(i) * time.Second),
		}); err != nil {
			t.Fatalf("UpsertEvidenceObject(%d) err = %v", i, err)
		}
	}
	result, err := store.HydrateEvidence(EvidenceHydrationQuery{
		Key:   key,
		Query: "bounded hydration limit",
		Limit: maxEvidenceHydrationLimit + 1000,
	})
	if err != nil {
		t.Fatalf("HydrateEvidence() err = %v", err)
	}
	if len(result.Selected) != maxEvidenceHydrationLimit {
		t.Fatalf("selected = %d, want server-side cap %d", len(result.Selected), maxEvidenceHydrationLimit)
	}
	runs, err := store.EvidenceHydrationRunsBySession(key, 1)
	if err != nil {
		t.Fatalf("EvidenceHydrationRunsBySession() err = %v", err)
	}
	if len(runs) != 1 || len(runs[0].SelectedEvidenceIDs) != maxEvidenceHydrationLimit {
		t.Fatalf("recorded run selected IDs = %#v, want capped ordered selection", runs)
	}
}

func countEvidenceObjectsForTest(t *testing.T, db *sql.DB) int {
	t.Helper()
	var count int
	if err := db.QueryRow(`SELECT COUNT(1) FROM evidence_objects`).Scan(&count); err != nil {
		t.Fatalf("count evidence objects: %v", err)
	}
	return count
}
