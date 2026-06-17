//go:build linux

package memory

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSemanticEngineSearchFindsCuratedMemory(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeSemanticFile(t, filepath.Join(root, "MEMORY.md"), "# MEMORY.md\n\nOperator prefers concise progress updates during long tasks.")
	writeSemanticFile(t, filepath.Join(root, "memory", "knowledge.md"), "# knowledge.md\n\n- Prefers concise progress updates [observed, confidence: 0.90]")
	writeSemanticFile(t, filepath.Join(root, "memory", "decisions.md"), "# decisions.md\n\n- Use heartbeat reflection to preserve durable memory.")

	engine := NewSemanticEngine(SemanticOptions{
		Enabled:             true,
		DBPath:              filepath.Join(root, "semantic.db"),
		Sources:             []string{"MEMORY.md", "memory/knowledge.md", "memory/decisions.md"},
		InteractiveTopK:     5,
		HeartbeatTopK:       12,
		InteractiveMaxChars: 4000,
		HeartbeatMaxChars:   12000,
		DailyNotesDir:       "memory/daily",
	})

	hits, err := engine.Search(context.Background(), SemanticSearchRequest{
		Root:  root,
		Scope: "shared",
		Query: "brief progress updates",
		Mode:  SemanticModeInteractive,
		Now:   time.Date(2026, 4, 10, 12, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("Search() err = %v", err)
	}
	if len(hits) == 0 {
		t.Fatal("Search() returned no hits, want curated-memory result")
	}
	if hits[0].Kind != "knowledge" && hits[0].Kind != "memory" {
		t.Fatalf("top hit = %#v, want knowledge or memory", hits[0])
	}
	if !strings.Contains(strings.ToLower(hits[0].Excerpt), "concise progress updates") {
		t.Fatalf("top hit excerpt = %q, want progress-updates content", hits[0].Excerpt)
	}
}

func TestSemanticDocumentUpsertReturnsCanonicalIDOnConflict(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	engine := NewSemanticEngine(SemanticOptions{
		Enabled: true,
		DBPath:  filepath.Join(root, "semantic.db"),
	})
	db, err := engine.ensureDB()
	if err != nil {
		t.Fatalf("ensureDB() err = %v", err)
	}
	defer db.Close()

	tx, err := db.Begin()
	if err != nil {
		t.Fatalf("Begin() err = %v", err)
	}
	firstID, err := upsertSemanticDocumentTx(tx, SemanticDocument{
		Scope:            "shared",
		SourcePath:       "memory/knowledge.md",
		SourceKind:       "knowledge",
		ProvenanceSource: "native",
		ImportState:      SemanticImportStateApproved,
		Checksum:         "first",
	})
	if err != nil {
		t.Fatalf("first upsert err = %v", err)
	}
	secondID, err := upsertSemanticDocumentTx(tx, SemanticDocument{
		Scope:            "shared",
		SourcePath:       "memory/decisions.md",
		SourceKind:       "decision",
		ProvenanceSource: "native",
		ImportState:      SemanticImportStateApproved,
		Checksum:         "second",
	})
	if err != nil {
		t.Fatalf("second upsert err = %v", err)
	}
	if secondID == firstID {
		t.Fatalf("secondID = firstID = %d, want distinct documents", firstID)
	}
	conflictID, err := upsertSemanticDocumentTx(tx, SemanticDocument{
		Scope:            "shared",
		SourcePath:       "memory/knowledge.md",
		SourceKind:       "knowledge",
		ProvenanceSource: "native",
		ImportState:      SemanticImportStateApproved,
		Checksum:         "updated",
	})
	if err != nil {
		t.Fatalf("conflict upsert err = %v", err)
	}
	if conflictID != firstID {
		t.Fatalf("conflictID = %d, want canonical first document id %d", conflictID, firstID)
	}
	if err := replaceSemanticChunksTx(tx, conflictID, []semanticChunkDraft{{ordinal: 1, text: "updated durable memory"}}); err != nil {
		t.Fatalf("replaceSemanticChunksTx() err = %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit() err = %v", err)
	}
}

func TestSemanticEngineStripsMemoryInstrumentation(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeSemanticFile(t, filepath.Join(root, "memory", "knowledge.md"), strings.Join([]string{
		"<!-- aphelion-memory-file:v1",
		"scope: shared",
		"store: knowledge",
		"-->",
		"",
		"<!-- aphelion-memory-entry:v1",
		"id: mem_test",
		"scope: shared",
		"store: knowledge",
		"-->",
		"",
		"- Instrumentation should not appear in semantic excerpts; concise updates should.",
	}, "\n"))
	engine := NewSemanticEngine(SemanticOptions{
		Enabled:             true,
		DBPath:              filepath.Join(root, "semantic.db"),
		Sources:             []string{"memory/knowledge.md"},
		InteractiveTopK:     5,
		HeartbeatTopK:       12,
		InteractiveMaxChars: 4000,
		HeartbeatMaxChars:   12000,
		DailyNotesDir:       "memory/daily",
	})

	hits, err := engine.Search(context.Background(), SemanticSearchRequest{
		Root:  root,
		Scope: "shared",
		Query: "concise updates",
		Mode:  SemanticModeInteractive,
		Now:   time.Date(2026, 4, 10, 12, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("Search() err = %v", err)
	}
	if len(hits) == 0 {
		t.Fatal("Search() returned no hits")
	}
	if strings.Contains(hits[0].Excerpt, "aphelion-memory-entry") || !strings.Contains(hits[0].Excerpt, "concise updates") {
		t.Fatalf("excerpt = %q, want stripped metadata and retained content", hits[0].Excerpt)
	}
}

func TestSemanticEngineHeartbeatIncludesRecentDailyNotes(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeSemanticFile(t, filepath.Join(root, "memory", "daily", "2026-04-10.md"), "Need to preserve the recurring preference for concise updates.")
	writeSemanticFile(t, filepath.Join(root, "memory", "knowledge.md"), "# knowledge.md\n\n- Prefers concise updates [observed]")

	engine := NewSemanticEngine(SemanticOptions{
		Enabled:             true,
		DBPath:              filepath.Join(root, "semantic.db"),
		Sources:             []string{"memory/knowledge.md"},
		IncludeDailyNotes:   true,
		InteractiveTopK:     5,
		HeartbeatTopK:       12,
		InteractiveMaxChars: 4000,
		HeartbeatMaxChars:   12000,
		DailyNotesDir:       "memory/daily",
	})

	hits, err := engine.Search(context.Background(), SemanticSearchRequest{
		Root:  root,
		Scope: "shared",
		Query: "recurring concise updates",
		Mode:  SemanticModeHeartbeat,
		Now:   time.Date(2026, 4, 10, 12, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("Search() err = %v", err)
	}
	if len(hits) == 0 {
		t.Fatal("Search() returned no hits, want heartbeat semantic recall")
	}
	foundDaily := false
	for _, hit := range hits {
		if hit.Kind == "daily_note" {
			foundDaily = true
			break
		}
	}
	if !foundDaily {
		t.Fatalf("hits = %#v, want daily_note hit", hits)
	}
}

func TestSemanticEngineExcludesQuarantinedImportsFromSearch(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	engine := NewSemanticEngine(SemanticOptions{
		Enabled:             true,
		DBPath:              filepath.Join(root, "semantic.db"),
		Sources:             []string{"memory/knowledge.md"},
		InteractiveTopK:     5,
		HeartbeatTopK:       12,
		InteractiveMaxChars: 4000,
		HeartbeatMaxChars:   12000,
		DailyNotesDir:       "memory/daily",
	})

	if _, err := engine.ImportDocument(context.Background(), SemanticImportRequest{
		Scope:            "shared",
		SourcePath:       "imports/openclaw/knowledge.md",
		SourceKind:       "knowledge",
		SourceClass:      "imported_archive",
		ProvenanceSource: "openclaw_import",
		ImportState:      SemanticImportStateQuarantine,
		Content:          "- Secret imported preference",
		MTime:            time.Date(2026, 4, 10, 12, 0, 0, 0, time.UTC),
	}); err != nil {
		t.Fatalf("ImportDocument() err = %v", err)
	}

	hits, err := engine.Search(context.Background(), SemanticSearchRequest{
		Root:  root,
		Scope: "shared",
		Query: "secret imported preference",
		Mode:  SemanticModeInteractive,
		Now:   time.Date(2026, 4, 10, 12, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("Search() err = %v", err)
	}
	if len(hits) != 0 {
		t.Fatalf("Search() hits = %#v, want quarantined imports excluded", hits)
	}
}

func TestSemanticImportAuditApproveMakesDocumentSearchable(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	engine := NewSemanticEngine(SemanticOptions{
		Enabled:             true,
		DBPath:              filepath.Join(root, "semantic.db"),
		Sources:             []string{"memory/knowledge.md"},
		InteractiveTopK:     5,
		HeartbeatTopK:       12,
		InteractiveMaxChars: 4000,
		HeartbeatMaxChars:   12000,
		DailyNotesDir:       "memory/daily",
	})

	docID, err := engine.ImportDocument(context.Background(), SemanticImportRequest{
		Scope:            "principal",
		PrincipalID:      "42",
		SourcePath:       "imports/openclaw/preferences.md",
		SourceKind:       "knowledge",
		SourceClass:      "imported_archive",
		ProvenanceSource: "openclaw_import",
		ImportState:      SemanticImportStateQuarantine,
		Content:          "- Prefers concise updates from imported archive",
		MTime:            time.Date(2026, 4, 10, 12, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("ImportDocument() err = %v", err)
	}

	docs, err := engine.ListImportAudit(context.Background(), SemanticAuditFilter{
		State:       SemanticImportStateQuarantine,
		Scope:       "principal",
		PrincipalID: "42",
	})
	if err != nil {
		t.Fatalf("ListImportAudit() err = %v", err)
	}
	if len(docs) != 1 || docs[0].ID != docID {
		t.Fatalf("ListImportAudit() = %#v, want imported document %d", docs, docID)
	}

	if err := engine.SetImportState(context.Background(), docID, SemanticImportStateApproved); err != nil {
		t.Fatalf("SetImportState() err = %v", err)
	}

	hits, err := engine.Search(context.Background(), SemanticSearchRequest{
		Root:        root,
		Scope:       "principal",
		PrincipalID: "42",
		Query:       "concise updates imported archive",
		Mode:        SemanticModeInteractive,
		Now:         time.Date(2026, 4, 10, 12, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("Search() err = %v", err)
	}
	if len(hits) == 0 {
		t.Fatal("Search() returned no hits, want approved imported document")
	}
	if hits[0].PrincipalID != "42" || hits[0].Provenance != "openclaw_import" {
		t.Fatalf("top hit = %#v, want principal discriminator and provenance", hits[0])
	}

	if _, err := engine.ReviewImportDocument(context.Background(), docID, 4, 2000); err == nil {
		t.Fatal("ReviewImportDocument() err = nil after approval, want quarantine-only review")
	}
	if err := engine.SetImportState(context.Background(), docID, SemanticImportStateRejected); err == nil {
		t.Fatal("SetImportState() err = nil after approval, want quarantine-only transitions")
	}
}

func TestSemanticEngineImportOpenClawPreservesQuarantineAndMetadata(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	foreignDBPath := filepath.Join(root, "openclaw.db")
	createOpenClawFixtureDB(t, foreignDBPath)

	engine := NewSemanticEngine(SemanticOptions{
		Enabled:             true,
		DBPath:              filepath.Join(root, "semantic.db"),
		Sources:             []string{"memory/knowledge.md"},
		InteractiveTopK:     5,
		HeartbeatTopK:       12,
		InteractiveMaxChars: 4000,
		HeartbeatMaxChars:   12000,
		DailyNotesDir:       "memory/daily",
	})

	summary, err := engine.ImportOpenClaw(context.Background(), SemanticOpenClawImportRequest{
		DBPath:           foreignDBPath,
		Scope:            "principal",
		PrincipalID:      "telegram:42",
		ProvenanceSource: "openclaw_import",
		ImportState:      SemanticImportStateQuarantine,
	})
	if err != nil {
		t.Fatalf("ImportOpenClaw() err = %v", err)
	}
	if summary.Documents != 1 || summary.Chunks != 2 {
		t.Fatalf("summary = %#v, want 1 document and 2 chunks", summary)
	}
	if summary.Contract != openClawObservedSchemaContract || summary.EmbeddingUse != "preserved_only" || summary.EmbeddedChunkCount != 2 {
		t.Fatalf("summary = %#v, want observed contract and preserved embedding summary", summary)
	}

	docs, err := engine.ListImportAudit(context.Background(), SemanticAuditFilter{
		State:       SemanticImportStateQuarantine,
		Scope:       "principal",
		PrincipalID: "telegram:42",
	})
	if err != nil {
		t.Fatalf("ListImportAudit() err = %v", err)
	}
	if len(docs) != 1 {
		t.Fatalf("ListImportAudit() len = %d, want 1", len(docs))
	}
	if docs[0].SourceKind != "knowledge" || docs[0].PrincipalID != "telegram:42" {
		t.Fatalf("doc = %#v, want knowledge doc scoped to principal", docs[0])
	}
	var meta importedDocumentMetadata
	if err := json.Unmarshal([]byte(docs[0].MetadataJSON), &meta); err != nil {
		t.Fatalf("json.Unmarshal(metadata_json) err = %v", err)
	}
	if meta.ImportContract != openClawObservedSchemaContract || meta.Embeddings != "preserved_only" || meta.ForeignSource != "memory" {
		t.Fatalf("metadata = %#v, want observed import contract and preserved embedding metadata", meta)
	}

	review, err := engine.ReviewImportDocument(context.Background(), docs[0].ID, 4, 2000)
	if err != nil {
		t.Fatalf("ReviewImportDocument() err = %v", err)
	}
	if review.ChunkCount != 2 {
		t.Fatalf("review.ChunkCount = %d, want 2", review.ChunkCount)
	}

	db, err := engine.ensureDB()
	if err != nil {
		t.Fatalf("ensureDB() err = %v", err)
	}
	var dims int
	var model string
	var startLine int
	if err := db.QueryRow(`
		SELECT embedding_dims, embedding_model, start_line
		FROM semantic_chunks
		ORDER BY ordinal
		LIMIT 1
	`).Scan(&dims, &model, &startLine); err != nil {
		t.Fatalf("QueryRow(semantic_chunks) err = %v", err)
	}
	if dims != 2 || model != "text-embedding-3-small" || startLine != 1 {
		t.Fatalf("imported chunk metadata = dims:%d model:%q start:%d, want dims:2 model:text-embedding-3-small start:1", dims, model, startLine)
	}
}

func TestSemanticEngineImportOpenClawAcceptsObservedFloatEpochMillis(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	foreignDBPath := filepath.Join(root, "openclaw-float-times.db")
	db, err := sql.Open("sqlite3", foreignDBPath)
	if err != nil {
		t.Fatalf("sql.Open(%s) err = %v", foreignDBPath, err)
	}
	defer db.Close()

	for _, stmt := range []string{
		`CREATE TABLE files (
			path TEXT PRIMARY KEY,
			source TEXT NOT NULL,
			hash TEXT NOT NULL DEFAULT '',
			mtime INTEGER NOT NULL DEFAULT 0,
			size INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE TABLE chunks (
			id TEXT PRIMARY KEY,
			path TEXT NOT NULL,
			source TEXT NOT NULL,
			start_line INTEGER NOT NULL DEFAULT 0,
			end_line INTEGER NOT NULL DEFAULT 0,
			hash TEXT NOT NULL DEFAULT '',
			model TEXT NOT NULL DEFAULT '',
			text TEXT NOT NULL,
			embedding TEXT NOT NULL DEFAULT '',
			updated_at INTEGER NOT NULL DEFAULT 0
		)`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("Exec(%q) err = %v", stmt, err)
		}
	}
	if _, err := db.Exec(`
		INSERT INTO files (path, source, hash, mtime, size)
		VALUES (?, ?, ?, ?, ?)
	`, "memory/knowledge.md", "memory", "abc123", 1774795995958.402, 42); err != nil {
		t.Fatalf("insert files err = %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO chunks (id, path, source, start_line, end_line, hash, model, text, embedding, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, "chunk-1", "memory/knowledge.md", "memory", 1, 2, "", "text-embedding-3-small", "- Float epoch millis should import.", "[0.1, 0.2]", 1774795996001.125); err != nil {
		t.Fatalf("insert chunks err = %v", err)
	}

	engine := NewSemanticEngine(SemanticOptions{
		Enabled: true,
		DBPath:  filepath.Join(root, "semantic.db"),
	})
	summary, err := engine.ImportOpenClaw(context.Background(), SemanticOpenClawImportRequest{
		DBPath:      foreignDBPath,
		Scope:       "shared",
		ImportState: SemanticImportStateQuarantine,
	})
	if err != nil {
		t.Fatalf("ImportOpenClaw() err = %v", err)
	}
	if summary.Documents != 1 || summary.Chunks != 1 || summary.EmbeddedChunkCount != 1 {
		t.Fatalf("summary = %#v, want one imported float-time document/chunk", summary)
	}
}

func TestSemanticEngineImportOpenClawRejectsUnknownObservedSchema(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	foreignDBPath := filepath.Join(root, "broken-openclaw.db")
	db, err := sql.Open("sqlite3", foreignDBPath)
	if err != nil {
		t.Fatalf("sql.Open(%s) err = %v", foreignDBPath, err)
	}
	if _, err := db.Exec(`CREATE TABLE files (path TEXT PRIMARY KEY, source TEXT NOT NULL)`); err != nil {
		t.Fatalf("create files table err = %v", err)
	}
	_ = db.Close()

	engine := NewSemanticEngine(SemanticOptions{
		Enabled: true,
		DBPath:  filepath.Join(root, "semantic.db"),
	})
	_, err = engine.ImportOpenClaw(context.Background(), SemanticOpenClawImportRequest{
		DBPath:      foreignDBPath,
		Scope:       "shared",
		ImportState: SemanticImportStateQuarantine,
	})
	if err == nil {
		t.Fatal("ImportOpenClaw() err = nil, want observed-schema validation error")
	}
	if !strings.Contains(err.Error(), openClawObservedSchemaContract) {
		t.Fatalf("ImportOpenClaw() err = %v, want observed schema contract in error", err)
	}
}

func TestSemanticEngineSetImportStateRejectsNonImportedArchive(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	engine := NewSemanticEngine(SemanticOptions{
		Enabled: true,
		DBPath:  filepath.Join(root, "semantic.db"),
	})
	docID, err := engine.ImportDocument(context.Background(), SemanticImportRequest{
		Scope:            "shared",
		SourcePath:       "imports/manual/notes.md",
		SourceKind:       "knowledge",
		SourceClass:      "curated",
		ProvenanceSource: "manual_import",
		ImportState:      SemanticImportStateQuarantine,
		Content:          "- Imported manually without archive classification.",
	})
	if err != nil {
		t.Fatalf("ImportDocument() err = %v", err)
	}
	if err := engine.SetImportState(context.Background(), docID, SemanticImportStateApproved); err == nil {
		t.Fatal("SetImportState() err = nil, want imported-archive guard")
	}
}

func createOpenClawFixtureDB(t *testing.T, path string) {
	t.Helper()
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatalf("sql.Open(%s) err = %v", path, err)
	}
	defer db.Close()

	statements := []string{
		`CREATE TABLE files (
			path TEXT PRIMARY KEY,
			source TEXT NOT NULL,
			hash TEXT NOT NULL DEFAULT '',
			mtime INTEGER NOT NULL DEFAULT 0,
			size INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE TABLE chunks (
			id TEXT PRIMARY KEY,
			path TEXT NOT NULL,
			source TEXT NOT NULL,
			start_line INTEGER NOT NULL DEFAULT 0,
			end_line INTEGER NOT NULL DEFAULT 0,
			hash TEXT NOT NULL DEFAULT '',
			model TEXT NOT NULL DEFAULT '',
			text TEXT NOT NULL,
			embedding TEXT NOT NULL DEFAULT '',
			updated_at INTEGER NOT NULL DEFAULT 0
		)`,
	}
	for _, stmt := range statements {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("Exec(%q) err = %v", stmt, err)
		}
	}

	if _, err := db.Exec(`
		INSERT INTO files (path, source, hash, mtime, size)
		VALUES (?, ?, ?, ?, ?)
	`, "memory/knowledge.md", "memory", "", int64(1712798400000), 256); err != nil {
		t.Fatalf("insert files row err = %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO chunks (id, path, source, start_line, end_line, hash, model, text, embedding, updated_at)
		VALUES
			(?, ?, ?, ?, ?, ?, ?, ?, ?, ?),
			(?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		"chunk-1", "memory/knowledge.md", "memory", 1, 2, "", "text-embedding-3-small", "- Prefers concise progress updates.", "[0.1, 0.2]", int64(1712798400000),
		"chunk-2", "memory/knowledge.md", "memory", 4, 5, "", "text-embedding-3-small", "- Values reviewable import boundaries.", "[0.3, 0.4]", int64(1712798460000),
	); err != nil {
		t.Fatalf("insert chunks rows err = %v", err)
	}
}

func writeSemanticFile(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll(%s) err = %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile(%s) err = %v", path, err)
	}
}
