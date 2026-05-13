//go:build linux

package memory

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

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
	mtime  float64
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
	updatedAt float64
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
