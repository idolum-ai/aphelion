//go:build linux

package memory

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

type importedDocumentMetadata struct {
	ImportContract string `json:"import_contract,omitempty"`
	ForeignSource  string `json:"foreign_source,omitempty"`
	Embeddings     string `json:"embeddings,omitempty"`
}

func (e *SemanticEngine) ImportOpenClaw(ctx context.Context, req SemanticOpenClawImportRequest) (*SemanticImportSummary, error) {
	if e == nil || !e.opts.Enabled {
		return nil, fmt.Errorf("semantic retrieval is not enabled")
	}
	dbPath := strings.TrimSpace(req.DBPath)
	if dbPath == "" {
		return nil, fmt.Errorf("openclaw import db path is required")
	}
	scope := normalizeSemanticScope(req.Scope)
	principalID := normalizePrincipalID(req.PrincipalID)
	if scope == "principal" && principalID == "" {
		return nil, fmt.Errorf("openclaw import principal_id is required for principal scope")
	}
	provenance := strings.TrimSpace(req.ProvenanceSource)
	if provenance == "" {
		provenance = "openclaw_import"
	}
	importState := req.ImportState
	if importState == "" {
		importState = SemanticImportStateQuarantine
	}
	if err := validateImportState(importState); err != nil {
		return nil, err
	}

	foreignDB, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return nil, fmt.Errorf("open foreign sqlite db: %w", err)
	}
	defer foreignDB.Close()
	if err := validateOpenClawObservedSchema(ctx, foreignDB); err != nil {
		return nil, err
	}

	files, err := loadOpenClawFiles(ctx, foreignDB)
	if err != nil {
		return nil, err
	}

	localDB, err := e.ensureDB()
	if err != nil {
		return nil, err
	}
	tx, err := localDB.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin semantic import tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	summary := &SemanticImportSummary{
		Source:       "openclaw",
		Contract:     openClawObservedSchemaContract,
		Provenance:   provenance,
		Scope:        scope,
		PrincipalID:  principalID,
		EmbeddingUse: "preserved_only",
	}
	for _, file := range files {
		chunks, err := loadOpenClawChunks(ctx, foreignDB, file.path)
		if err != nil {
			return nil, err
		}
		if len(chunks) == 0 {
			continue
		}
		docID, chunkCount, embeddedChunkCount, err := importOpenClawDocumentTx(tx, file, chunks, scope, principalID, provenance, importState)
		if err != nil {
			return nil, err
		}
		if docID <= 0 {
			continue
		}
		summary.Documents++
		summary.Chunks += chunkCount
		summary.EmbeddedChunkCount += embeddedChunkCount
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit openclaw semantic import tx: %w", err)
	}
	return summary, nil
}

func loadOpenClawFiles(ctx context.Context, db *sql.DB) ([]openClawFileRow, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT path, source, hash, mtime, size
		FROM files
		ORDER BY path
	`)
	if err != nil {
		return nil, fmt.Errorf("load openclaw files: %w", err)
	}
	defer rows.Close()

	var out []openClawFileRow
	for rows.Next() {
		var row openClawFileRow
		if err := rows.Scan(&row.path, &row.source, &row.hash, &row.mtime, &row.size); err != nil {
			return nil, fmt.Errorf("scan openclaw file row: %w", err)
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate openclaw files: %w", err)
	}
	return out, nil
}

func validateOpenClawObservedSchema(ctx context.Context, db *sql.DB) error {
	if err := requireSQLiteColumns(ctx, db, "files", []string{"path", "source", "hash", "mtime", "size"}); err != nil {
		return fmt.Errorf("openclaw importer requires %s schema for files table: %w", openClawObservedSchemaContract, err)
	}
	if err := requireSQLiteColumns(ctx, db, "chunks", []string{"id", "path", "source", "start_line", "end_line", "hash", "model", "text", "embedding", "updated_at"}); err != nil {
		return fmt.Errorf("openclaw importer requires %s schema for chunks table: %w", openClawObservedSchemaContract, err)
	}
	return nil
}

func requireSQLiteColumns(ctx context.Context, db *sql.DB, table string, required []string) error {
	rows, err := db.QueryContext(ctx, `PRAGMA table_info(`+table+`)`)
	if err != nil {
		return fmt.Errorf("inspect table %s: %w", table, err)
	}
	defer rows.Close()

	columns := make(map[string]struct{})
	for rows.Next() {
		var (
			cid        int
			name       string
			dataType   string
			notNull    int
			defaultVal sql.NullString
			primaryKey int
		)
		if err := rows.Scan(&cid, &name, &dataType, &notNull, &defaultVal, &primaryKey); err != nil {
			return fmt.Errorf("scan table_info(%s): %w", table, err)
		}
		columns[strings.ToLower(strings.TrimSpace(name))] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate table_info(%s): %w", table, err)
	}
	if len(columns) == 0 {
		return fmt.Errorf("table %s not found", table)
	}
	var missing []string
	for _, column := range required {
		if _, ok := columns[strings.ToLower(strings.TrimSpace(column))]; !ok {
			missing = append(missing, column)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("table %s missing columns: %s", table, strings.Join(missing, ", "))
	}
	return nil
}

func loadOpenClawChunks(ctx context.Context, db *sql.DB, path string) ([]openClawChunkRow, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT id, path, source, start_line, end_line, hash, model, text, embedding, updated_at
		FROM chunks
		WHERE path = ?
		ORDER BY start_line, end_line, id
	`, path)
	if err != nil {
		return nil, fmt.Errorf("load openclaw chunks for %s: %w", path, err)
	}
	defer rows.Close()

	var out []openClawChunkRow
	for rows.Next() {
		var row openClawChunkRow
		if err := rows.Scan(&row.id, &row.path, &row.source, &row.startLine, &row.endLine, &row.hash, &row.model, &row.text, &row.embedding, &row.updatedAt); err != nil {
			return nil, fmt.Errorf("scan openclaw chunk row for %s: %w", path, err)
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate openclaw chunks for %s: %w", path, err)
	}
	return out, nil
}

func importOpenClawDocumentTx(
	tx *sql.Tx,
	file openClawFileRow,
	chunks []openClawChunkRow,
	scope string,
	principalID string,
	provenance string,
	importState SemanticImportState,
) (int64, int, int, error) {
	sourcePath := filepath.ToSlash(strings.TrimSpace(file.path))
	if sourcePath == "" {
		return 0, 0, 0, nil
	}
	kind := detectImportedSemanticKind(sourcePath, file.source)
	sourceClass := "imported_archive"
	mtime := epochMillisNumberToTime(file.mtime)
	if mtime.IsZero() {
		mtime = epochMillisNumberToTime(latestOpenClawUpdate(chunks))
	}

	drafts := make([]semanticChunkDraft, 0, len(chunks))
	embeddedChunkCount := 0
	for i, chunk := range chunks {
		text := strings.TrimSpace(chunk.text)
		if text == "" {
			continue
		}
		embeddingJSON, embeddingDims := normalizeImportedEmbedding(chunk.embedding)
		if strings.TrimSpace(embeddingJSON) != "" {
			embeddedChunkCount++
		}
		var startLine *int
		if chunk.startLine > 0 {
			value := int(chunk.startLine)
			startLine = &value
		}
		var endLine *int
		if chunk.endLine > 0 {
			value := int(chunk.endLine)
			endLine = &value
		}
		drafts = append(drafts, semanticChunkDraft{
			ordinal:        i,
			text:           text,
			startLine:      startLine,
			endLine:        endLine,
			embeddingModel: strings.TrimSpace(chunk.model),
			embeddingDims:  embeddingDims,
			embeddingJSON:  embeddingJSON,
		})
	}
	if len(drafts) == 0 {
		return 0, 0, 0, nil
	}

	checksum := strings.TrimSpace(file.hash)
	if checksum == "" {
		checksum = checksumText(joinChunkTexts(drafts))
	}
	metadataJSON, err := marshalImportedDocumentMetadata(importedDocumentMetadata{
		ImportContract: openClawObservedSchemaContract,
		ForeignSource:  strings.TrimSpace(file.source),
		Embeddings:     "preserved_only",
	})
	if err != nil {
		return 0, 0, 0, err
	}
	docID, err := upsertSemanticDocumentTx(tx, SemanticDocument{
		Scope:            scope,
		PrincipalID:      principalID,
		SourcePath:       sourcePath,
		SourceKind:       kind,
		SourceClass:      sourceClass,
		ProvenanceSource: provenance,
		ImportState:      importState,
		Checksum:         checksum,
		MTime:            mtime,
		MetadataJSON:     metadataJSON,
	})
	if err != nil {
		return 0, 0, 0, err
	}
	if err := replaceSemanticChunksTx(tx, docID, drafts); err != nil {
		return 0, 0, 0, err
	}
	return docID, len(drafts), embeddedChunkCount, nil
}

func normalizeImportedEmbedding(raw string) (string, int) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", 0
	}
	var values []float64
	if err := json.Unmarshal([]byte(raw), &values); err == nil {
		normalized, _ := json.Marshal(values)
		return string(normalized), len(values)
	}
	return raw, 0
}

func marshalImportedDocumentMetadata(meta importedDocumentMetadata) (string, error) {
	raw, err := json.Marshal(meta)
	if err != nil {
		return "", fmt.Errorf("marshal imported document metadata: %w", err)
	}
	return string(raw), nil
}

func latestOpenClawUpdate(chunks []openClawChunkRow) float64 {
	var latest float64
	for _, chunk := range chunks {
		if chunk.updatedAt > latest {
			latest = chunk.updatedAt
		}
	}
	return latest
}

func epochMillisNumberToTime(raw float64) time.Time {
	if raw <= 0 {
		return time.Time{}
	}
	if raw < 1_000_000_000_000 {
		sec := int64(raw)
		frac := raw - float64(sec)
		return time.Unix(sec, int64(frac*float64(time.Second))).UTC()
	}
	sec := int64(raw / 1000)
	fracMillis := raw - float64(sec*1000)
	return time.Unix(sec, int64(fracMillisToNanos(fracMillis))).UTC()
}

func fracMillisToNanos(ms float64) int64 {
	if ms <= 0 {
		return 0
	}
	return int64(ms * float64(time.Millisecond))
}
