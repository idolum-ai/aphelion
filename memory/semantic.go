//go:build linux

package memory

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	_ "github.com/mattn/go-sqlite3"
)

const semanticSchemaVersion = 1

const openClawObservedSchemaContract = "openclaw_observed_v1"

type SemanticMode string

const (
	SemanticModeInteractive SemanticMode = "interactive"
	SemanticModeHeartbeat   SemanticMode = "heartbeat"
)

type SemanticImportState string

const (
	SemanticImportStateQuarantine SemanticImportState = "quarantine"
	SemanticImportStateApproved   SemanticImportState = "approved"
	SemanticImportStateRejected   SemanticImportState = "rejected"
)

type SemanticOptions struct {
	Enabled             bool
	DBPath              string
	Sources             []string
	IncludeDailyNotes   bool
	IncludeQuestions    bool
	IncludeRhizome      bool
	InteractiveTopK     int
	HeartbeatTopK       int
	InteractiveMaxChars int
	HeartbeatMaxChars   int
	DailyNotesDir       string
}

type SemanticSearchRequest struct {
	Root        string
	Scope       string
	PrincipalID string
	Query       string
	Mode        SemanticMode
	Limit       int
	MaxLen      int
	Now         time.Time
}

type SemanticHit struct {
	Source      string
	Scope       string
	PrincipalID string
	Kind        string
	Provenance  string
	Score       float64
	Excerpt     string
}

type SemanticDocument struct {
	ID               int64
	Scope            string
	PrincipalID      string
	SourcePath       string
	SourceKind       string
	SourceClass      string
	ProvenanceSource string
	ImportState      SemanticImportState
	Checksum         string
	MTime            time.Time
	CreatedAt        time.Time
	UpdatedAt        time.Time
	MetadataJSON     string
}

type SemanticDocumentReview struct {
	Document   SemanticDocument
	ChunkCount int
	Excerpts   []string
}

type SemanticImportRequest struct {
	Scope            string
	PrincipalID      string
	SourcePath       string
	SourceKind       string
	SourceClass      string
	ProvenanceSource string
	ImportState      SemanticImportState
	Content          string
	MTime            time.Time
	MetadataJSON     string
}

type SemanticAuditFilter struct {
	State       SemanticImportState
	Scope       string
	PrincipalID string
	Limit       int
}

type SemanticImportSummary struct {
	Source             string
	Contract           string
	Provenance         string
	Scope              string
	PrincipalID        string
	Documents          int
	Chunks             int
	EmbeddedChunkCount int
	EmbeddingUse       string
}

type SemanticOpenClawImportRequest struct {
	DBPath           string
	Scope            string
	PrincipalID      string
	ProvenanceSource string
	ImportState      SemanticImportState
}

type SemanticEngine struct {
	opts SemanticOptions

	mu sync.Mutex
	db *sql.DB
}

type semanticChunk struct {
	source      string
	scope       string
	principalID string
	kind        string
	provenance  string
	text        string
	terms       []string
	mtime       time.Time
}

type semanticSource struct {
	path        string
	kind        string
	class       string
	content     string
	checksum    string
	mtime       time.Time
	provenance  string
	importState SemanticImportState
}

type openClawFileRow struct {
	path   string
	source string
	hash   string
	mtime  int64
	size   int64
}

type openClawChunkRow struct {
	id        string
	path      string
	source    string
	startLine int64
	endLine   int64
	hash      string
	model     string
	text      string
	embedding string
	updatedAt int64
}

type importedDocumentMetadata struct {
	ImportContract string `json:"import_contract,omitempty"`
	ForeignSource  string `json:"foreign_source,omitempty"`
	Embeddings     string `json:"embeddings,omitempty"`
}

type semanticChunkDraft struct {
	ordinal        int
	text           string
	startLine      *int
	endLine        *int
	embeddingModel string
	embeddingDims  int
	embeddingJSON  string
}

type semanticIndexedDocument struct {
	ID          int64
	SourcePath  string
	Checksum    string
	MTime       time.Time
	ImportState SemanticImportState
}

func DefaultSemanticDBPath(sessionDBPath string) string {
	if strings.TrimSpace(sessionDBPath) == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(sessionDBPath), "semantic.db")
}

func NewSemanticEngine(opts SemanticOptions) *SemanticEngine {
	return &SemanticEngine{opts: opts}
}

func (e *SemanticEngine) Close() error {
	if e == nil {
		return nil
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.db == nil {
		return nil
	}
	err := e.db.Close()
	e.db = nil
	return err
}

func (e *SemanticEngine) Enabled() bool {
	return e != nil && e.opts.Enabled
}

func (e *SemanticEngine) Search(ctx context.Context, req SemanticSearchRequest) ([]SemanticHit, error) {
	if e == nil || !e.opts.Enabled {
		return nil, fmt.Errorf("semantic retrieval is not enabled")
	}
	root := strings.TrimSpace(req.Root)
	if root == "" {
		return nil, fmt.Errorf("semantic search root is required")
	}
	scope := normalizeSemanticScope(req.Scope)
	principalID := normalizePrincipalID(req.PrincipalID)
	if scope == "principal" && principalID == "" {
		return nil, fmt.Errorf("semantic search principal_id is required for principal scope")
	}
	query := strings.TrimSpace(req.Query)
	if query == "" {
		return nil, fmt.Errorf("semantic search query is required")
	}
	now := req.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}

	if err := e.syncNativeCorpus(ctx, root, scope, principalID, now); err != nil {
		return nil, err
	}

	mode := req.Mode
	if mode == "" {
		mode = SemanticModeInteractive
	}
	corpus, err := e.loadApprovedCorpus(ctx, scope, principalID, mode, now)
	if err != nil {
		return nil, err
	}
	if len(corpus) == 0 {
		return nil, nil
	}

	queryTerms := tokenize(query)
	if len(queryTerms) == 0 {
		return nil, nil
	}

	df := make(map[string]int)
	for _, chunk := range corpus {
		seen := make(map[string]struct{}, len(chunk.terms))
		for _, term := range chunk.terms {
			if _, ok := seen[term]; ok {
				continue
			}
			seen[term] = struct{}{}
			df[term]++
		}
	}

	queryVec := make(map[string]float64)
	for _, term := range queryTerms {
		queryVec[term]++
	}
	totalDocs := float64(len(corpus))
	for term, weight := range queryVec {
		queryVec[term] = weight * idf(totalDocs, float64(df[term]))
	}

	scored := make([]SemanticHit, 0, len(corpus))
	for _, chunk := range corpus {
		score := similarityScore(query, queryVec, totalDocs, df, chunk, mode, now)
		if score <= 0 {
			continue
		}
		scored = append(scored, SemanticHit{
			Source:      chunk.source,
			Scope:       chunk.scope,
			PrincipalID: chunk.principalID,
			Kind:        chunk.kind,
			Provenance:  chunk.provenance,
			Score:       score,
			Excerpt:     chunk.text,
		})
	}

	sort.Slice(scored, func(i, j int) bool {
		if scored[i].Score == scored[j].Score {
			if scored[i].Source == scored[j].Source {
				return scored[i].Excerpt < scored[j].Excerpt
			}
			return scored[i].Source < scored[j].Source
		}
		return scored[i].Score > scored[j].Score
	})

	limit := req.Limit
	if limit <= 0 {
		switch mode {
		case SemanticModeHeartbeat:
			limit = e.opts.HeartbeatTopK
		default:
			limit = e.opts.InteractiveTopK
		}
	}
	maxChars := req.MaxLen
	if maxChars <= 0 {
		switch mode {
		case SemanticModeHeartbeat:
			maxChars = e.opts.HeartbeatMaxChars
		default:
			maxChars = e.opts.InteractiveMaxChars
		}
	}

	out := make([]SemanticHit, 0, min(limit, len(scored)))
	chars := 0
	for _, hit := range scored {
		if len(out) >= limit {
			break
		}
		nextCost := len(hit.Excerpt) + len(hit.Source) + len(hit.Kind) + len(hit.Provenance) + len(hit.PrincipalID) + 64
		if len(out) > 0 && chars+nextCost > maxChars {
			break
		}
		out = append(out, hit)
		chars += nextCost
	}
	return out, nil
}

func (e *SemanticEngine) ImportDocument(ctx context.Context, req SemanticImportRequest) (int64, error) {
	db, err := e.ensureDB()
	if err != nil {
		return 0, err
	}
	scope := normalizeSemanticScope(req.Scope)
	principalID := normalizePrincipalID(req.PrincipalID)
	if scope == "principal" && principalID == "" {
		return 0, fmt.Errorf("principal_id is required for principal imports")
	}
	sourcePath := filepath.ToSlash(strings.TrimSpace(req.SourcePath))
	if sourcePath == "" {
		return 0, fmt.Errorf("source_path is required")
	}
	sourceKind := strings.TrimSpace(req.SourceKind)
	if sourceKind == "" {
		sourceKind = detectSemanticKind(sourcePath)
	}
	sourceClass := strings.TrimSpace(req.SourceClass)
	if sourceClass == "" {
		sourceClass = classifySemanticSource(sourcePath, sourceKind)
	}
	provenance := strings.TrimSpace(req.ProvenanceSource)
	if provenance == "" {
		provenance = "imported"
	}
	importState := req.ImportState
	if importState == "" {
		importState = SemanticImportStateQuarantine
	}
	if err := validateImportState(importState); err != nil {
		return 0, err
	}

	content := strings.TrimSpace(req.Content)
	if content == "" {
		return 0, fmt.Errorf("import content is required")
	}
	mtime := req.MTime
	if mtime.IsZero() {
		mtime = time.Now().UTC()
	}
	checksum := checksumText(content)
	chunks := chunkText(sourcePath, sourceKind, content)

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin semantic import tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	docID, err := upsertSemanticDocumentTx(tx, SemanticDocument{
		Scope:            scope,
		PrincipalID:      principalID,
		SourcePath:       sourcePath,
		SourceKind:       sourceKind,
		SourceClass:      sourceClass,
		ProvenanceSource: provenance,
		ImportState:      importState,
		Checksum:         checksum,
		MTime:            mtime,
		MetadataJSON:     strings.TrimSpace(req.MetadataJSON),
	})
	if err != nil {
		return 0, err
	}
	if err := replaceSemanticChunksTx(tx, docID, chunks); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit semantic import tx: %w", err)
	}
	return docID, nil
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

func (e *SemanticEngine) ListImportAudit(ctx context.Context, filter SemanticAuditFilter) ([]SemanticDocument, error) {
	db, err := e.ensureDB()
	if err != nil {
		return nil, err
	}
	state := filter.State
	if state == "" {
		state = SemanticImportStateQuarantine
	}
	if err := validateImportState(state); err != nil {
		return nil, err
	}
	scope := strings.ToLower(strings.TrimSpace(filter.Scope))
	principalID := normalizePrincipalID(filter.PrincipalID)
	limit := filter.Limit
	if limit <= 0 {
		limit = 50
	}

	clauses := []string{"provenance_source <> 'native'", "source_class = 'imported_archive'", "import_state = ?"}
	args := []any{string(state)}
	if scope != "" {
		clauses = append(clauses, "scope = ?")
		args = append(args, scope)
	}
	if principalID != "" {
		clauses = append(clauses, "principal_id = ?")
		args = append(args, principalID)
	}
	args = append(args, limit)

	rows, err := db.QueryContext(ctx, `
		SELECT id, scope, principal_id, source_path, source_kind, source_class, provenance_source,
		       import_state, checksum, mtime, created_at, updated_at, metadata_json
		FROM semantic_documents
		WHERE `+strings.Join(clauses, " AND ")+`
		ORDER BY updated_at DESC, id DESC
		LIMIT ?
	`, args...)
	if err != nil {
		return nil, fmt.Errorf("list import-audit documents: %w", err)
	}
	defer rows.Close()

	var out []SemanticDocument
	for rows.Next() {
		doc, err := scanSemanticDocument(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, doc)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate import-audit documents: %w", err)
	}
	return out, nil
}

func (e *SemanticEngine) ReviewImportDocument(ctx context.Context, documentID int64, chunkLimit int, maxChars int) (*SemanticDocumentReview, error) {
	if documentID <= 0 {
		return nil, fmt.Errorf("document id must be > 0")
	}
	db, err := e.ensureDB()
	if err != nil {
		return nil, err
	}
	row := db.QueryRowContext(ctx, `
		SELECT id, scope, principal_id, source_path, source_kind, source_class, provenance_source,
		       import_state, checksum, mtime, created_at, updated_at, metadata_json
		FROM semantic_documents
		WHERE id = ? AND provenance_source <> 'native' AND source_class = 'imported_archive' AND import_state = ?
	`, documentID, string(SemanticImportStateQuarantine))
	doc, err := scanSemanticDocument(row)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("import-audit document %d not found", documentID)
		}
		return nil, err
	}

	if chunkLimit <= 0 {
		chunkLimit = 6
	}
	if maxChars <= 0 {
		maxChars = 4000
	}

	rows, err := db.QueryContext(ctx, `
		SELECT text
		FROM semantic_chunks
		WHERE document_id = ?
		ORDER BY ordinal
	`, documentID)
	if err != nil {
		return nil, fmt.Errorf("load import-audit chunks: %w", err)
	}
	defer rows.Close()

	review := &SemanticDocumentReview{Document: doc}
	chars := 0
	for rows.Next() {
		var text string
		if err := rows.Scan(&text); err != nil {
			return nil, fmt.Errorf("scan import-audit chunk: %w", err)
		}
		review.ChunkCount++
		if len(review.Excerpts) >= chunkLimit {
			continue
		}
		text = strings.TrimSpace(text)
		if text == "" {
			continue
		}
		next := truncate(text, 900)
		if len(review.Excerpts) > 0 && chars+len(next) > maxChars {
			continue
		}
		review.Excerpts = append(review.Excerpts, next)
		chars += len(next)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate import-audit chunks: %w", err)
	}
	return review, nil
}

func (e *SemanticEngine) SetImportState(ctx context.Context, documentID int64, state SemanticImportState) error {
	if documentID <= 0 {
		return fmt.Errorf("document id must be > 0")
	}
	if err := validateImportState(state); err != nil {
		return err
	}
	db, err := e.ensureDB()
	if err != nil {
		return err
	}
	var (
		currentState string
		sourceClass  string
	)
	row := db.QueryRowContext(ctx, `
		SELECT import_state, source_class
		FROM semantic_documents
		WHERE id = ? AND provenance_source <> 'native'
	`, documentID)
	if err := row.Scan(&currentState, &sourceClass); err != nil {
		if err == sql.ErrNoRows {
			return fmt.Errorf("import-audit document %d not found", documentID)
		}
		return fmt.Errorf("load import-audit document %d: %w", documentID, err)
	}
	if strings.TrimSpace(sourceClass) != "imported_archive" {
		return fmt.Errorf("import-audit document %d is not an imported archive", documentID)
	}
	if SemanticImportState(strings.TrimSpace(currentState)) != SemanticImportStateQuarantine {
		return fmt.Errorf("import-audit document %d is not in quarantine", documentID)
	}
	result, err := db.ExecContext(ctx, `
		UPDATE semantic_documents
		SET import_state = ?, updated_at = ?
		WHERE id = ? AND provenance_source <> 'native' AND source_class = 'imported_archive' AND import_state = ?
	`, string(state), utcTimestamp(time.Now().UTC()), documentID, string(SemanticImportStateQuarantine))
	if err != nil {
		return fmt.Errorf("update import_state: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected: %w", err)
	}
	if rows == 0 {
		return fmt.Errorf("import-audit document %d not found", documentID)
	}
	return nil
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
	mtime := epochMillisToTime(file.mtime)
	if mtime.IsZero() {
		mtime = epochMillisToTime(latestOpenClawUpdate(chunks))
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

func (e *SemanticEngine) syncNativeCorpus(ctx context.Context, root string, scope string, principalID string, now time.Time) error {
	db, err := e.ensureDB()
	if err != nil {
		return err
	}
	sources, err := e.collectNativeSources(root, now)
	if err != nil {
		return err
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin semantic sync tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	existing, err := loadIndexedDocumentsTx(tx, scope, principalID, "native")
	if err != nil {
		return err
	}
	seen := make(map[string]struct{}, len(sources))
	for _, src := range sources {
		seen[src.path] = struct{}{}
		doc, ok := existing[src.path]
		if ok && doc.Checksum == src.checksum && sameInstant(doc.MTime, src.mtime) && doc.ImportState == SemanticImportStateApproved {
			continue
		}
		docID, err := upsertSemanticDocumentTx(tx, SemanticDocument{
			ID:               doc.ID,
			Scope:            scope,
			PrincipalID:      principalID,
			SourcePath:       src.path,
			SourceKind:       src.kind,
			SourceClass:      src.class,
			ProvenanceSource: src.provenance,
			ImportState:      src.importState,
			Checksum:         src.checksum,
			MTime:            src.mtime,
		})
		if err != nil {
			return err
		}
		if err := replaceSemanticChunksTx(tx, docID, chunkText(src.path, src.kind, src.content)); err != nil {
			return err
		}
	}

	for path, doc := range existing {
		if _, ok := seen[path]; ok {
			continue
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM semantic_documents WHERE id = ?`, doc.ID); err != nil {
			return fmt.Errorf("delete removed semantic document %s: %w", path, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit semantic sync tx: %w", err)
	}
	return nil
}

func (e *SemanticEngine) collectNativeSources(root string, now time.Time) ([]semanticSource, error) {
	var out []semanticSource
	for _, rel := range e.semanticSourceList() {
		path := filepath.Join(root, filepath.FromSlash(rel))
		raw, info, err := readSemanticFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, fmt.Errorf("read semantic source %s: %w", path, err)
		}
		out = append(out, semanticSource{
			path:        filepath.ToSlash(rel),
			kind:        detectSemanticKind(rel),
			class:       classifySemanticSource(rel, detectSemanticKind(rel)),
			content:     string(raw),
			checksum:    checksumBytes(raw),
			mtime:       info.ModTime().UTC(),
			provenance:  "native",
			importState: SemanticImportStateApproved,
		})
	}

	if e.opts.IncludeDailyNotes {
		dir := strings.TrimSpace(e.opts.DailyNotesDir)
		if dir == "" {
			dir = "memory/daily"
		}
		noteRoot := filepath.Join(root, filepath.FromSlash(dir))
		entries, err := collectMarkdownFiles(noteRoot)
		if err != nil {
			if !os.IsNotExist(err) {
				return nil, fmt.Errorf("collect semantic daily notes %s: %w", noteRoot, err)
			}
		} else {
			for _, entry := range entries {
				rel, err := filepath.Rel(root, entry.Path)
				if err != nil {
					return nil, fmt.Errorf("relative daily note path: %w", err)
				}
				out = append(out, semanticSource{
					path:        filepath.ToSlash(rel),
					kind:        "daily_note",
					class:       "daily_note",
					content:     string(entry.Raw),
					checksum:    checksumBytes(entry.Raw),
					mtime:       entry.ModTime.UTC(),
					provenance:  "native",
					importState: SemanticImportStateApproved,
				})
			}
		}
	}

	sort.Slice(out, func(i, j int) bool { return out[i].path < out[j].path })
	return out, nil
}

func (e *SemanticEngine) loadApprovedCorpus(ctx context.Context, scope string, principalID string, mode SemanticMode, now time.Time) ([]semanticChunk, error) {
	db, err := e.ensureDB()
	if err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, `
		SELECT d.source_path, d.scope, d.principal_id, d.source_kind, d.provenance_source, d.mtime, c.text
		FROM semantic_documents d
		JOIN semantic_chunks c ON c.document_id = d.id
		WHERE d.scope = ? AND d.principal_id = ? AND d.import_state = ?
		ORDER BY d.source_path, c.ordinal
	`, scope, principalID, string(SemanticImportStateApproved))
	if err != nil {
		return nil, fmt.Errorf("load semantic corpus: %w", err)
	}
	defer rows.Close()

	var out []semanticChunk
	for rows.Next() {
		var (
			source     string
			docScope   string
			docPID     string
			kind       string
			provenance string
			mtimeRaw   string
			text       string
		)
		if err := rows.Scan(&source, &docScope, &docPID, &kind, &provenance, &mtimeRaw, &text); err != nil {
			return nil, fmt.Errorf("scan semantic corpus row: %w", err)
		}
		mtime, err := parseOptionalTime(mtimeRaw)
		if err != nil {
			return nil, fmt.Errorf("parse semantic mtime: %w", err)
		}
		if kind == "daily_note" && !withinDailyWindow(mode, now, source, mtime) {
			continue
		}
		terms := tokenize(text)
		if len(terms) == 0 {
			continue
		}
		out = append(out, semanticChunk{
			source:      source,
			scope:       docScope,
			principalID: docPID,
			kind:        kind,
			provenance:  provenance,
			text:        text,
			terms:       terms,
			mtime:       mtime,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate semantic corpus: %w", err)
	}
	return out, nil
}

func (e *SemanticEngine) semanticSourceList() []string {
	sources := append([]string(nil), e.opts.Sources...)
	if e.opts.IncludeQuestions {
		sources = append(sources, "memory/questions.md")
	}
	if e.opts.IncludeRhizome {
		sources = append(sources, "memory/rhizome.md")
	}
	return uniqueStrings(sources)
}

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

func readSemanticFile(path string) ([]byte, os.FileInfo, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, nil, err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}
	return raw, info, nil
}

type markdownFile struct {
	Path    string
	Raw     []byte
	ModTime time.Time
}

func collectMarkdownFiles(root string) ([]markdownFile, error) {
	info, err := os.Stat(root)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%s is not a directory", root)
	}
	var out []markdownFile
	err = filepath.Walk(root, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if info.IsDir() {
			return nil
		}
		if strings.ToLower(filepath.Ext(path)) != ".md" {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		out = append(out, markdownFile{
			Path:    path,
			Raw:     raw,
			ModTime: info.ModTime(),
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

func normalizeSemanticScope(scope string) string {
	switch strings.ToLower(strings.TrimSpace(scope)) {
	case "principal":
		return "principal"
	default:
		return "shared"
	}
}

func normalizePrincipalID(principalID string) string {
	return strings.TrimSpace(principalID)
}

func validateImportState(state SemanticImportState) error {
	switch state {
	case SemanticImportStateQuarantine, SemanticImportStateApproved, SemanticImportStateRejected:
		return nil
	default:
		return fmt.Errorf("invalid semantic import_state %q", state)
	}
}

func sameInstant(a time.Time, b time.Time) bool {
	if a.IsZero() && b.IsZero() {
		return true
	}
	return a.UTC().Equal(b.UTC())
}

func nullableTimestamp(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return utcTimestamp(t)
}

func utcTimestamp(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}

func parseOptionalTime(raw string) (time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, nil
	}
	if t, err := time.Parse(time.RFC3339Nano, raw); err == nil {
		return t.UTC(), nil
	}
	if t, err := time.Parse("2006-01-02 15:04:05", raw); err == nil {
		return t.UTC(), nil
	}
	return time.Time{}, fmt.Errorf("unsupported time format %q", raw)
}

func checksumBytes(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func checksumText(raw string) string {
	return checksumBytes([]byte(raw))
}

func chunkText(source string, kind string, raw string) []semanticChunkDraft {
	paragraphs := splitMarkdownParagraphs(raw)
	if len(paragraphs) == 0 {
		return nil
	}

	var chunks []string
	switch kind {
	case "knowledge", "decision", "question", "daily_note":
		chunks = paragraphs
	case "rhizome":
		chunks = broaderChunks(paragraphs, 2)
	default:
		chunks = broaderChunks(paragraphs, 2)
	}

	out := make([]semanticChunkDraft, 0, len(chunks))
	for i, chunk := range chunks {
		chunk = strings.TrimSpace(chunk)
		if chunk == "" {
			continue
		}
		out = append(out, semanticChunkDraft{
			ordinal: i,
			text:    chunk,
		})
	}
	return out
}

func classifySemanticSource(source string, kind string) string {
	switch kind {
	case "daily_note":
		return "daily_note"
	case "memory", "knowledge", "decision", "question", "rhizome":
		return "curated"
	default:
		if strings.Contains(filepath.ToSlash(source), "archive/") {
			return "archive"
		}
		return "curated"
	}
}

func detectImportedSemanticKind(sourcePath string, source string) string {
	kind := detectSemanticKind(sourcePath)
	if kind != "memory" {
		return kind
	}
	cleanPath := strings.ToLower(filepath.ToSlash(strings.TrimSpace(sourcePath)))
	switch {
	case cleanPath == "memory.md":
		return "memory"
	case strings.Contains(cleanPath, "/memory/knowledge.md"), strings.HasSuffix(cleanPath, "knowledge.md"):
		return "knowledge"
	case strings.Contains(cleanPath, "/memory/decisions.md"), strings.HasSuffix(cleanPath, "decisions.md"):
		return "decision"
	case strings.Contains(cleanPath, "/memory/questions.md"), strings.HasSuffix(cleanPath, "questions.md"):
		return "question"
	case strings.Contains(cleanPath, "/memory/rhizome.md"), strings.HasSuffix(cleanPath, "rhizome.md"):
		return "rhizome"
	case strings.Contains(cleanPath, "/daily/"):
		return "daily_note"
	case strings.TrimSpace(source) != "" && strings.ToLower(strings.TrimSpace(source)) != "memory":
		return "imported_archive"
	default:
		return "imported_archive"
	}
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

func joinChunkTexts(chunks []semanticChunkDraft) string {
	parts := make([]string, 0, len(chunks))
	for _, chunk := range chunks {
		text := strings.TrimSpace(chunk.text)
		if text == "" {
			continue
		}
		parts = append(parts, text)
	}
	return strings.Join(parts, "\n\n")
}

func latestOpenClawUpdate(chunks []openClawChunkRow) int64 {
	var latest int64
	for _, chunk := range chunks {
		if chunk.updatedAt > latest {
			latest = chunk.updatedAt
		}
	}
	return latest
}

func epochMillisToTime(raw int64) time.Time {
	if raw <= 0 {
		return time.Time{}
	}
	if raw < 1_000_000_000_000 {
		return time.Unix(raw, 0).UTC()
	}
	return time.Unix(0, raw*int64(time.Millisecond)).UTC()
}

func withinDailyWindow(mode SemanticMode, now time.Time, source string, mtime time.Time) bool {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	threshold := 48 * time.Hour
	if mode == SemanticModeHeartbeat {
		threshold = 7 * 24 * time.Hour
	}
	ts := dailyNoteTime(source, mtime)
	if ts.IsZero() {
		return true
	}
	return ts.After(now.Add(-threshold))
}

func dailyNoteTime(source string, fallback time.Time) time.Time {
	base := strings.TrimSuffix(filepath.Base(filepath.ToSlash(source)), filepath.Ext(source))
	if t, err := time.Parse("2006-01-02", base); err == nil {
		return t.UTC()
	}
	return fallback.UTC()
}

func detectSemanticKind(source string) string {
	switch strings.ToLower(filepath.ToSlash(strings.TrimSpace(source))) {
	case "memory.md":
		return "memory"
	case "memory/knowledge.md":
		return "knowledge"
	case "memory/decisions.md":
		return "decision"
	case "memory/questions.md":
		return "question"
	case "memory/rhizome.md":
		return "rhizome"
	default:
		if strings.Contains(source, "daily/") || strings.Contains(source, "daily\\") {
			return "daily_note"
		}
		return "memory"
	}
}

func broaderChunks(parts []string, width int) []string {
	if len(parts) <= width {
		return []string{strings.Join(parts, "\n\n")}
	}
	out := make([]string, 0, len(parts))
	for i := 0; i < len(parts); i += width {
		end := i + width
		if end > len(parts) {
			end = len(parts)
		}
		out = append(out, strings.Join(parts[i:end], "\n\n"))
	}
	return out
}

func splitMarkdownParagraphs(raw string) []string {
	raw = strings.ReplaceAll(raw, "\r\n", "\n")
	chunks := strings.Split(raw, "\n\n")
	out := make([]string, 0, len(chunks))
	for _, chunk := range chunks {
		chunk = strings.TrimSpace(chunk)
		if chunk == "" {
			continue
		}
		out = append(out, chunk)
	}
	return out
}

func similarityScore(query string, queryVec map[string]float64, totalDocs float64, df map[string]int, chunk semanticChunk, mode SemanticMode, now time.Time) float64 {
	docVec := make(map[string]float64)
	for _, term := range chunk.terms {
		docVec[term]++
	}
	for term, weight := range docVec {
		docVec[term] = weight * idf(totalDocs, float64(df[term]))
	}

	score := cosineSimilarity(queryVec, docVec)
	lowerQuery := strings.ToLower(strings.TrimSpace(query))
	lowerText := strings.ToLower(chunk.text)
	if lowerQuery != "" && strings.Contains(lowerText, lowerQuery) {
		score += 0.35
	}
	matchedTerms := 0
	seen := make(map[string]struct{}, len(chunk.terms))
	for _, term := range chunk.terms {
		seen[term] = struct{}{}
	}
	for term := range queryVec {
		if _, ok := seen[term]; ok {
			matchedTerms++
		}
	}
	score += float64(matchedTerms) * 0.03
	switch chunk.kind {
	case "knowledge", "decision":
		score += 0.05
	case "question", "rhizome":
		score -= 0.02
	}
	if chunk.kind == "daily_note" && withinDailyWindow(mode, now, chunk.source, chunk.mtime) {
		score += 0.01
	}
	return score
}

func idf(totalDocs float64, df float64) float64 {
	return math.Log(1 + totalDocs/(1+df))
}

func cosineSimilarity(a map[string]float64, b map[string]float64) float64 {
	var dot float64
	var aNorm float64
	var bNorm float64
	for k, av := range a {
		aNorm += av * av
		if bv, ok := b[k]; ok {
			dot += av * bv
		}
	}
	for _, bv := range b {
		bNorm += bv * bv
	}
	if aNorm == 0 || bNorm == 0 {
		return 0
	}
	return dot / (math.Sqrt(aNorm) * math.Sqrt(bNorm))
}

func tokenize(raw string) []string {
	raw = strings.ToLower(raw)
	var b strings.Builder
	for _, r := range raw {
		switch {
		case unicode.IsLetter(r), unicode.IsDigit(r):
			b.WriteRune(r)
		default:
			b.WriteRune(' ')
		}
	}
	fields := strings.Fields(b.String())
	out := make([]string, 0, len(fields))
	for _, field := range fields {
		if len(field) <= 1 {
			continue
		}
		if _, skip := semanticStopwords[field]; skip {
			continue
		}
		out = append(out, field)
	}
	return out
}

var semanticStopwords = map[string]struct{}{
	"the": {}, "and": {}, "for": {}, "that": {}, "with": {}, "this": {}, "from": {},
	"into": {}, "what": {}, "when": {}, "where": {}, "which": {}, "while": {}, "have": {},
	"has": {}, "had": {}, "was": {}, "were": {}, "will": {}, "would": {}, "should": {},
	"could": {}, "about": {}, "there": {}, "their": {}, "them": {}, "they": {}, "then": {},
	"than": {}, "your": {}, "ours": {}, "our": {}, "you": {}, "not": {}, "but": {},
	"are": {}, "is": {}, "be": {}, "to": {}, "of": {}, "in": {}, "on": {}, "it": {},
	"as": {}, "or": {}, "at": {}, "by": {}, "an": {}, "if": {}, "we": {}, "do": {},
}

func uniqueStrings(items []string) []string {
	seen := make(map[string]struct{}, len(items))
	out := make([]string, 0, len(items))
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		out = append(out, item)
	}
	return out
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func truncate(raw string, max int) string {
	raw = strings.TrimSpace(raw)
	if max <= 0 || len(raw) <= max {
		return raw
	}
	if max <= 1 {
		return raw[:max]
	}
	return raw[:max-1] + "…"
}

func min(a int, b int) int {
	if a < b {
		return a
	}
	return b
}
