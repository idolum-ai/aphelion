//go:build linux

package memory

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func (e *SemanticEngine) ensureDB() (*sql.DB, error) {
	if e == nil {
		return nil, fmt.Errorf("semantic engine is nil")
	}

	e.mu.Lock()
	defer e.mu.Unlock()
	if e.db != nil {
		return e.db, nil
	}

	path := strings.TrimSpace(e.opts.DBPath)
	if path == "" {
		return nil, fmt.Errorf("semantic db path is required")
	}
	if path != ":memory:" {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return nil, fmt.Errorf("create semantic db directory: %w", err)
		}
	}

	db, err := sql.Open("sqlite3", path)
	if err != nil {
		return nil, fmt.Errorf("open semantic sqlite db: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(0)

	if err := initSemanticDB(db); err != nil {
		_ = db.Close()
		return nil, err
	}
	e.db = db
	return e.db, nil
}

func initSemanticDB(db *sql.DB) error {
	pragmas := []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA synchronous=NORMAL",
		"PRAGMA foreign_keys=ON",
	}
	for _, p := range pragmas {
		if _, err := db.Exec(p); err != nil {
			return fmt.Errorf("apply semantic pragma %q: %w", p, err)
		}
	}

	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin semantic schema tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	statements := []string{
		`CREATE TABLE IF NOT EXISTS semantic_schema_version (
			version INTEGER NOT NULL,
			applied_at TEXT NOT NULL DEFAULT (datetime('now'))
		)`,
		`CREATE TABLE IF NOT EXISTS semantic_documents (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			scope TEXT NOT NULL,
			principal_id TEXT NOT NULL DEFAULT '',
			source_path TEXT NOT NULL,
			source_kind TEXT NOT NULL,
			source_class TEXT NOT NULL,
			provenance_source TEXT NOT NULL,
			import_state TEXT NOT NULL CHECK(import_state IN ('quarantine', 'approved', 'rejected')),
			checksum TEXT NOT NULL,
			mtime TEXT,
			created_at TEXT NOT NULL DEFAULT (datetime('now')),
			updated_at TEXT NOT NULL DEFAULT (datetime('now')),
			metadata_json TEXT NOT NULL DEFAULT '',
			UNIQUE(scope, principal_id, source_path, provenance_source)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_semantic_documents_scope ON semantic_documents(scope, principal_id, import_state, source_kind)`,
		`CREATE INDEX IF NOT EXISTS idx_semantic_documents_import ON semantic_documents(import_state, provenance_source, updated_at, id)`,
		`CREATE TABLE IF NOT EXISTS semantic_chunks (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			document_id INTEGER NOT NULL,
			ordinal INTEGER NOT NULL,
			text TEXT NOT NULL,
			text_hash TEXT NOT NULL,
			start_line INTEGER,
			end_line INTEGER,
			start_offset INTEGER,
			end_offset INTEGER,
			embedding_model TEXT NOT NULL DEFAULT '',
			embedding_dims INTEGER NOT NULL DEFAULT 0,
			embedding_json TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL DEFAULT (datetime('now')),
			updated_at TEXT NOT NULL DEFAULT (datetime('now')),
			FOREIGN KEY (document_id) REFERENCES semantic_documents(id) ON DELETE CASCADE
		)`,
		`CREATE INDEX IF NOT EXISTS idx_semantic_chunks_document ON semantic_chunks(document_id, ordinal)`,
	}
	for _, stmt := range statements {
		if _, err := tx.Exec(stmt); err != nil {
			return fmt.Errorf("apply semantic schema statement: %w", err)
		}
	}

	var versionCount int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM semantic_schema_version`).Scan(&versionCount); err != nil {
		return fmt.Errorf("load semantic schema version: %w", err)
	}
	if versionCount == 0 {
		if _, err := tx.Exec(`INSERT INTO semantic_schema_version (version) VALUES (?)`, semanticSchemaVersion); err != nil {
			return fmt.Errorf("insert semantic schema version: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit semantic schema tx: %w", err)
	}
	return nil
}

func upsertSemanticDocumentTx(tx *sql.Tx, doc SemanticDocument) (int64, error) {
	scope := normalizeSemanticScope(doc.Scope)
	principalID := normalizePrincipalID(doc.PrincipalID)
	if scope == "principal" && principalID == "" {
		return 0, fmt.Errorf("principal_id is required for principal documents")
	}
	if doc.ImportState == "" {
		doc.ImportState = SemanticImportStateApproved
	}
	if err := validateImportState(doc.ImportState); err != nil {
		return 0, err
	}
	sourcePath := filepath.ToSlash(strings.TrimSpace(doc.SourcePath))
	if sourcePath == "" {
		return 0, fmt.Errorf("semantic document source_path is required")
	}

	now := utcTimestamp(time.Now().UTC())
	mtime := nullableTimestamp(doc.MTime)
	result, err := tx.Exec(`
		INSERT INTO semantic_documents (
			scope, principal_id, source_path, source_kind, source_class, provenance_source,
			import_state, checksum, mtime, metadata_json, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(scope, principal_id, source_path, provenance_source)
		DO UPDATE SET
			source_kind = excluded.source_kind,
			source_class = excluded.source_class,
			import_state = excluded.import_state,
			checksum = excluded.checksum,
			mtime = excluded.mtime,
			metadata_json = excluded.metadata_json,
			updated_at = excluded.updated_at
	`,
		scope,
		principalID,
		sourcePath,
		strings.TrimSpace(doc.SourceKind),
		strings.TrimSpace(doc.SourceClass),
		firstNonEmpty(strings.TrimSpace(doc.ProvenanceSource), "native"),
		string(doc.ImportState),
		doc.Checksum,
		mtime,
		strings.TrimSpace(doc.MetadataJSON),
		now,
		now,
	)
	if err != nil {
		return 0, fmt.Errorf("upsert semantic document %s: %w", sourcePath, err)
	}
	if id, err := result.LastInsertId(); err == nil && id > 0 {
		return id, nil
	}

	var id int64
	if err := tx.QueryRow(`
		SELECT id
		FROM semantic_documents
		WHERE scope = ? AND principal_id = ? AND source_path = ? AND provenance_source = ?
	`,
		scope,
		principalID,
		sourcePath,
		firstNonEmpty(strings.TrimSpace(doc.ProvenanceSource), "native"),
	).Scan(&id); err != nil {
		return 0, fmt.Errorf("reload semantic document id %s: %w", sourcePath, err)
	}
	return id, nil
}

func replaceSemanticChunksTx(tx *sql.Tx, documentID int64, chunks []semanticChunkDraft) error {
	if _, err := tx.Exec(`DELETE FROM semantic_chunks WHERE document_id = ?`, documentID); err != nil {
		return fmt.Errorf("clear semantic chunks for %d: %w", documentID, err)
	}
	now := utcTimestamp(time.Now().UTC())
	for _, chunk := range chunks {
		text := strings.TrimSpace(chunk.text)
		if text == "" {
			continue
		}
		if _, err := tx.Exec(`
			INSERT INTO semantic_chunks (
				document_id, ordinal, text, text_hash, start_line, end_line, embedding_model, embedding_dims, embedding_json, created_at, updated_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`,
			documentID,
			chunk.ordinal,
			text,
			checksumText(text),
			chunk.startLine,
			chunk.endLine,
			strings.TrimSpace(chunk.embeddingModel),
			chunk.embeddingDims,
			strings.TrimSpace(chunk.embeddingJSON),
			now,
			now,
		); err != nil {
			return fmt.Errorf("insert semantic chunk for document %d: %w", documentID, err)
		}
	}
	return nil
}

func loadIndexedDocumentsTx(tx *sql.Tx, scope string, principalID string, provenance string) (map[string]semanticIndexedDocument, error) {
	rows, err := tx.Query(`
		SELECT id, source_path, checksum, mtime, import_state
		FROM semantic_documents
		WHERE scope = ? AND principal_id = ? AND provenance_source = ?
	`, scope, principalID, provenance)
	if err != nil {
		return nil, fmt.Errorf("load indexed semantic documents: %w", err)
	}
	defer rows.Close()

	out := make(map[string]semanticIndexedDocument)
	for rows.Next() {
		var (
			doc      semanticIndexedDocument
			mtimeRaw string
			stateRaw string
		)
		if err := rows.Scan(&doc.ID, &doc.SourcePath, &doc.Checksum, &mtimeRaw, &stateRaw); err != nil {
			return nil, fmt.Errorf("scan indexed semantic document: %w", err)
		}
		doc.MTime, err = parseOptionalTime(mtimeRaw)
		if err != nil {
			return nil, fmt.Errorf("parse indexed semantic mtime: %w", err)
		}
		doc.ImportState = SemanticImportState(stateRaw)
		out[doc.SourcePath] = doc
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate indexed semantic documents: %w", err)
	}
	return out, nil
}

func scanSemanticDocument(scanner interface{ Scan(dest ...any) error }) (SemanticDocument, error) {
	var (
		doc        SemanticDocument
		importRaw  string
		mtimeRaw   string
		createdRaw string
		updatedRaw string
	)
	if err := scanner.Scan(
		&doc.ID,
		&doc.Scope,
		&doc.PrincipalID,
		&doc.SourcePath,
		&doc.SourceKind,
		&doc.SourceClass,
		&doc.ProvenanceSource,
		&importRaw,
		&doc.Checksum,
		&mtimeRaw,
		&createdRaw,
		&updatedRaw,
		&doc.MetadataJSON,
	); err != nil {
		return SemanticDocument{}, err
	}
	doc.ImportState = SemanticImportState(importRaw)
	var err error
	doc.MTime, err = parseOptionalTime(mtimeRaw)
	if err != nil {
		return SemanticDocument{}, fmt.Errorf("parse semantic document mtime: %w", err)
	}
	doc.CreatedAt, err = parseOptionalTime(createdRaw)
	if err != nil {
		return SemanticDocument{}, fmt.Errorf("parse semantic document created_at: %w", err)
	}
	doc.UpdatedAt, err = parseOptionalTime(updatedRaw)
	if err != nil {
		return SemanticDocument{}, fmt.Errorf("parse semantic document updated_at: %w", err)
	}
	return doc, nil
}
