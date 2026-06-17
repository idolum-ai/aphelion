//go:build linux

package session

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

const (
	defaultEvidenceHydrationLimit = 16
	maxEvidenceHydrationLimit     = 50
)

func EvidenceIDForSource(sourceKind, sourceRef string) string {
	sourceKind = evidenceToken(sourceKind)
	if sourceKind == "" {
		sourceKind = "unknown"
	}
	sourceRef = strings.TrimSpace(sourceRef)
	sum := sha256.Sum256([]byte(sourceKind + "\x00" + sourceRef))
	return "ev:" + sourceKind + ":" + hex.EncodeToString(sum[:])[:20]
}

func (s *SQLiteStore) UpsertEvidenceObject(input EvidenceObjectInput) (EvidenceObject, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return EvidenceObject{}, fmt.Errorf("begin evidence object tx: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()
	obj, err := upsertEvidenceObjectTx(tx, input)
	if err != nil {
		return EvidenceObject{}, err
	}
	if err := tx.Commit(); err != nil {
		return EvidenceObject{}, fmt.Errorf("commit evidence object tx: %w", err)
	}
	return obj, nil
}

func (s *SQLiteStore) EvidenceObject(id string) (EvidenceObject, bool, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return EvidenceObject{}, false, nil
	}
	row := s.db.QueryRow(evidenceObjectSelectSQL()+` WHERE evidence_id = ?`, id)
	obj, err := scanEvidenceObject(row)
	if err == sql.ErrNoRows {
		return EvidenceObject{}, false, nil
	}
	if err != nil {
		return EvidenceObject{}, false, err
	}
	return obj, true, nil
}

func (s *SQLiteStore) EvidenceObjectsBySession(key SessionKey, limit int) ([]EvidenceObject, error) {
	sessionID := SessionIDForKey(key)
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.Query(evidenceObjectSelectSQL()+`
		WHERE session_id = ?
		ORDER BY observed_at DESC, evidence_id DESC
		LIMIT ?
	`, sessionID, limit)
	if err != nil {
		return nil, fmt.Errorf("query evidence objects by session: %w", err)
	}
	defer rows.Close()
	return scanEvidenceObjects(rows)
}

func (s *SQLiteStore) EvidenceHydrationRunsBySession(key SessionKey, limit int) ([]EvidenceHydrationRunInput, error) {
	sessionID := SessionIDForKey(key)
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.db.Query(`
		SELECT run_id, session_id, chat_id, user_id, scope_kind, scope_id, durable_agent_id, operation_id, query_text, mode, status,
			selected_evidence_ids_json, missing_evidence_ids_json, fallback_used, fallback_reason, created_at
		FROM evidence_hydration_runs
		WHERE session_id = ?
		ORDER BY created_at DESC, run_id DESC
		LIMIT ?
	`, sessionID, limit)
	if err != nil {
		return nil, fmt.Errorf("query evidence hydration runs: %w", err)
	}
	defer rows.Close()
	out := make([]EvidenceHydrationRunInput, 0, limit)
	for rows.Next() {
		var run EvidenceHydrationRunInput
		var scopeKind, durableAgentID string
		var selectedRaw, missingRaw string
		var fallbackInt int
		var createdRaw string
		if err := rows.Scan(
			&run.ID, &run.SessionID, &run.ChatID, &run.UserID, &scopeKind, &run.Scope.ID, &durableAgentID, &run.OperationID, &run.Query, &run.Mode, &run.Status,
			&selectedRaw, &missingRaw, &fallbackInt, &run.FallbackReason, &createdRaw,
		); err != nil {
			return nil, fmt.Errorf("scan evidence hydration run: %w", err)
		}
		run.Scope.Kind = ScopeKind(scopeKind)
		run.Scope.DurableAgentID = durableAgentID
		run.SelectedEvidenceIDs = decodeStringList(selectedRaw)
		run.MissingEvidenceIDs = decodeStringList(missingRaw)
		run.FallbackUsed = fallbackInt != 0
		run.CreatedAt = mustParseSQLiteTime(createdRaw)
		out = append(out, run)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate evidence hydration runs: %w", err)
	}
	return out, nil
}

func (s *SQLiteStore) EvidenceLedgerStatsForChat(chatID int64) (EvidenceLedgerStats, error) {
	var stats EvidenceLedgerStats
	if chatID == 0 {
		return stats, nil
	}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM evidence_objects WHERE chat_id = ?`, chatID).Scan(&stats.ObjectCount); err != nil {
		return stats, fmt.Errorf("count evidence objects for chat: %w", err)
	}
	var evidenceID, sourceKind, observedRaw sql.NullString
	if err := s.db.QueryRow(`
		SELECT evidence_id, source_kind, observed_at
		FROM evidence_objects
		WHERE chat_id = ?
		ORDER BY observed_at DESC, evidence_id DESC
		LIMIT 1
	`, chatID).Scan(&evidenceID, &sourceKind, &observedRaw); err != nil && err != sql.ErrNoRows {
		return stats, fmt.Errorf("latest evidence object for chat: %w", err)
	}
	stats.LatestEvidenceID = nullToString(evidenceID)
	stats.LatestSourceKind = nullToString(sourceKind)
	if raw := nullToString(observedRaw); raw != "" {
		stats.LatestObservedAt = mustParseSQLiteTime(raw)
	}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM evidence_hydration_runs WHERE chat_id = ?`, chatID).Scan(&stats.HydrationRunCount); err != nil {
		return stats, fmt.Errorf("count evidence hydration runs for chat: %w", err)
	}
	var hydrationID, hydratedRaw sql.NullString
	if err := s.db.QueryRow(`
		SELECT run_id, created_at
		FROM evidence_hydration_runs
		WHERE chat_id = ?
		ORDER BY created_at DESC, run_id DESC
		LIMIT 1
	`, chatID).Scan(&hydrationID, &hydratedRaw); err != nil && err != sql.ErrNoRows {
		return stats, fmt.Errorf("latest evidence hydration run for chat: %w", err)
	}
	stats.LatestHydrationID = nullToString(hydrationID)
	if raw := nullToString(hydratedRaw); raw != "" {
		stats.LatestHydratedAt = mustParseSQLiteTime(raw)
	}
	return stats, nil
}

func (s *SQLiteStore) AppendEvidenceLink(input EvidenceLinkInput) (EvidenceLink, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return EvidenceLink{}, fmt.Errorf("begin evidence link tx: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()
	link, err := appendEvidenceLinkTx(tx, input)
	if err != nil {
		return EvidenceLink{}, err
	}
	if err := tx.Commit(); err != nil {
		return EvidenceLink{}, fmt.Errorf("commit evidence link tx: %w", err)
	}
	return link, nil
}

func (s *SQLiteStore) HydrateEvidence(query EvidenceHydrationQuery) (EvidenceHydrationResult, error) {
	if query.Now.IsZero() {
		query.Now = time.Now().UTC()
	}
	query.Limit = boundedEvidenceHydrationLimit(query.Limit)
	sessionID := strings.TrimSpace(query.SessionID)
	if sessionID == "" {
		sessionID = SessionIDForKey(query.Key)
	}
	result := EvidenceHydrationResult{
		SessionID: sessionID,
		Query:     strings.TrimSpace(query.Query),
		CreatedAt: query.Now,
	}
	seen := map[string]struct{}{}
	for _, id := range normalizeStringSet(query.RequiredEvidenceIDs) {
		obj, ok, err := s.EvidenceObject(id)
		if err != nil {
			return result, err
		}
		if !ok || !evidenceObjectAllowedForHydration(obj, query, sessionID) {
			result.MissingEvidenceIDs = append(result.MissingEvidenceIDs, id)
			continue
		}
		result.Required = append(result.Required, obj)
		result.Selected = append(result.Selected, obj)
		seen[obj.ID] = struct{}{}
	}

	candidateLimit := query.Limit * 4
	if candidateLimit < 64 {
		candidateLimit = 64
	}
	candidates, err := s.evidenceHydrationCandidates(query, sessionID, candidateLimit)
	if err != nil {
		return result, err
	}
	scoreEvidenceCandidates(candidates, query)
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].score != candidates[j].score {
			return candidates[i].score > candidates[j].score
		}
		return candidates[i].object.ObservedAt.After(candidates[j].object.ObservedAt)
	})
	for _, candidate := range candidates {
		if len(result.Selected) >= query.Limit {
			break
		}
		if _, ok := seen[candidate.object.ID]; ok {
			continue
		}
		result.Selected = append(result.Selected, candidate.object)
		seen[candidate.object.ID] = struct{}{}
	}
	if len(result.Selected) == 0 {
		fallback, err := s.latestEvidenceSnapshots(sessionID, query.Limit)
		if err != nil {
			return result, err
		}
		result.Selected = fallback
		result.FallbackUsed = len(fallback) > 0
		if len(fallback) > 0 {
			result.FallbackReason = "no lexical or linked evidence matched; used latest low-authority ledger snapshots"
		} else {
			result.FallbackReason = "no evidence objects available for session"
		}
	}

	runID, err := s.AppendEvidenceHydrationRun(EvidenceHydrationRunInput{
		SessionID:           sessionID,
		ChatID:              query.Key.ChatID,
		UserID:              query.Key.UserID,
		Scope:               defaultScopeForKey(query.Key),
		OperationID:         query.OperationID,
		Query:               query.Query,
		Mode:                "deterministic",
		Status:              evidenceHydrationStatus(result),
		SelectedEvidenceIDs: evidenceObjectIDs(result.Selected),
		MissingEvidenceIDs:  result.MissingEvidenceIDs,
		FallbackUsed:        result.FallbackUsed,
		FallbackReason:      result.FallbackReason,
		CreatedAt:           query.Now,
	})
	if err != nil {
		return result, err
	}
	result.RunID = runID
	return result, nil
}

func evidenceObjectAllowedForHydration(obj EvidenceObject, query EvidenceHydrationQuery, sessionID string) bool {
	if strings.TrimSpace(obj.SessionID) == strings.TrimSpace(sessionID) {
		return true
	}
	return query.IncludeCrossScope && query.Key.ChatID != 0 && obj.ChatID == query.Key.ChatID
}

func (s *SQLiteStore) AppendEvidenceHydrationRun(input EvidenceHydrationRunInput) (string, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return "", fmt.Errorf("begin evidence hydration run tx: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()
	id, err := appendEvidenceHydrationRunTx(tx, input)
	if err != nil {
		return "", err
	}
	if err := tx.Commit(); err != nil {
		return "", fmt.Errorf("commit evidence hydration run tx: %w", err)
	}
	return id, nil
}

func (s *SQLiteStore) BackfillEvidenceLedger() error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin evidence backfill tx: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()
	if err := backfillEvidenceLedgerTx(tx); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit evidence backfill tx: %w", err)
	}
	return nil
}

func upsertEvidenceObjectTx(tx *sql.Tx, input EvidenceObjectInput) (EvidenceObject, error) {
	obj, err := normalizeEvidenceObjectInput(input)
	if err != nil {
		return EvidenceObject{}, err
	}
	if _, err := tx.Exec(`
		INSERT OR IGNORE INTO evidence_objects(
			evidence_id, evidence_type, source_kind, source_ref, source_table, source_id,
			session_id, chat_id, user_id, scope_kind, scope_id, durable_agent_id,
			authority_class, epistemic_status, redaction_class, subject_key,
			summary, digest, payload_json, payload_hash, observed_at, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		obj.ID, obj.EvidenceType, obj.SourceKind, obj.SourceRef, obj.SourceTable, obj.SourceID,
		obj.SessionID, obj.ChatID, obj.UserID, string(obj.Scope.Kind), obj.Scope.ID, obj.Scope.DurableAgentID,
		obj.AuthorityClass, obj.EpistemicStatus, obj.RedactionClass, obj.SubjectKey,
		obj.Summary, obj.Digest, obj.PayloadJSON, obj.PayloadHash, obj.ObservedAt.Format(time.RFC3339Nano), obj.CreatedAt.Format(time.RFC3339Nano),
	); err != nil {
		return EvidenceObject{}, fmt.Errorf("insert evidence object %s: %w", obj.ID, err)
	}
	return obj, nil
}

func appendEvidenceLinkTx(tx *sql.Tx, input EvidenceLinkInput) (EvidenceLink, error) {
	input.FromEvidenceID = strings.TrimSpace(input.FromEvidenceID)
	input.ToEvidenceID = strings.TrimSpace(input.ToEvidenceID)
	input.LinkType = evidenceToken(input.LinkType)
	input.Source = evidenceToken(input.Source)
	if input.FromEvidenceID == "" || input.ToEvidenceID == "" || input.LinkType == "" {
		return EvidenceLink{}, fmt.Errorf("evidence link requires from, to, and link type")
	}
	if input.Confidence <= 0 {
		input.Confidence = 1
	}
	if input.CreatedAt.IsZero() {
		input.CreatedAt = time.Now().UTC()
	}
	metadata, err := normalizeEvidencePayloadJSON(input.MetadataJSON)
	if err != nil {
		return EvidenceLink{}, err
	}
	res, err := tx.Exec(`
		INSERT OR IGNORE INTO evidence_links(from_evidence_id, to_evidence_id, link_type, source, confidence, metadata_json, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, input.FromEvidenceID, input.ToEvidenceID, input.LinkType, input.Source, input.Confidence, metadata, input.CreatedAt.Format(time.RFC3339Nano))
	if err != nil {
		return EvidenceLink{}, fmt.Errorf("insert evidence link: %w", err)
	}
	id, _ := res.LastInsertId()
	return EvidenceLink{
		ID:             id,
		FromEvidenceID: input.FromEvidenceID,
		ToEvidenceID:   input.ToEvidenceID,
		LinkType:       input.LinkType,
		Source:         input.Source,
		Confidence:     input.Confidence,
		MetadataJSON:   metadata,
		CreatedAt:      input.CreatedAt,
	}, nil
}

func appendEvidenceHydrationRunTx(tx *sql.Tx, input EvidenceHydrationRunInput) (string, error) {
	if input.CreatedAt.IsZero() {
		input.CreatedAt = time.Now().UTC()
	}
	input.Scope = NormalizeScopeRef(input.Scope)
	if strings.TrimSpace(input.ID) == "" {
		seed := strings.Join([]string{
			strings.TrimSpace(input.SessionID),
			strings.TrimSpace(input.OperationID),
			strings.TrimSpace(input.Query),
			input.CreatedAt.Format(time.RFC3339Nano),
			strings.Join(normalizeStringList(input.SelectedEvidenceIDs), ","),
			strings.Join(normalizeStringList(input.MissingEvidenceIDs), ","),
		}, "\x00")
		sum := sha256.Sum256([]byte(seed))
		input.ID = "hydr:" + hex.EncodeToString(sum[:])[:20]
	}
	input.Mode = firstEvidenceValue(evidenceToken(input.Mode), "deterministic")
	input.Status = firstEvidenceValue(evidenceToken(input.Status), "completed")
	selectedJSON := encodeStringList(input.SelectedEvidenceIDs)
	missingJSON := encodeStringList(input.MissingEvidenceIDs)
	if _, err := tx.Exec(`
		INSERT OR IGNORE INTO evidence_hydration_runs(
			run_id, session_id, chat_id, user_id, scope_kind, scope_id, durable_agent_id, operation_id, query_text, mode, status,
			selected_evidence_ids_json, missing_evidence_ids_json, fallback_used, fallback_reason, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, input.ID, strings.TrimSpace(input.SessionID), input.ChatID, input.UserID, string(input.Scope.Kind), input.Scope.ID, input.Scope.DurableAgentID,
		strings.TrimSpace(input.OperationID), strings.TrimSpace(input.Query), input.Mode, input.Status, selectedJSON, missingJSON, boolToInt(input.FallbackUsed),
		clampStoreText(input.FallbackReason, 1000), input.CreatedAt.UTC().Format(time.RFC3339Nano)); err != nil {
		return "", fmt.Errorf("insert evidence hydration run: %w", err)
	}
	return input.ID, nil
}

func normalizeEvidenceObjectInput(input EvidenceObjectInput) (EvidenceObject, error) {
	input.SourceKind = evidenceToken(input.SourceKind)
	input.SourceRef = strings.TrimSpace(input.SourceRef)
	if input.SourceKind == "" || input.SourceRef == "" {
		return EvidenceObject{}, fmt.Errorf("evidence object requires source kind and source ref")
	}
	payload, err := normalizeEvidencePayloadJSON(input.PayloadJSON)
	if err != nil {
		return EvidenceObject{}, fmt.Errorf("normalize evidence payload: %w", err)
	}
	hash := evidencePayloadHash(payload)
	if strings.TrimSpace(input.ID) == "" {
		input.ID = EvidenceIDForSource(input.SourceKind, input.SourceRef)
	}
	if input.ObservedAt.IsZero() {
		input.ObservedAt = time.Now().UTC()
	}
	if input.CreatedAt.IsZero() {
		input.CreatedAt = input.ObservedAt
	}
	status := evidenceToken(input.EpistemicStatus)
	if status == "" {
		status = EvidenceStatusObserved
	}
	redaction := evidenceToken(input.RedactionClass)
	if redaction == "" {
		redaction = EvidenceRedactionNone
	}
	if strings.TrimSpace(input.EvidenceType) == "" {
		input.EvidenceType = input.SourceKind
	}
	return EvidenceObject{
		ID:              strings.TrimSpace(input.ID),
		EvidenceType:    evidenceToken(input.EvidenceType),
		SourceKind:      input.SourceKind,
		SourceRef:       input.SourceRef,
		SourceTable:     evidenceToken(input.SourceTable),
		SourceID:        strings.TrimSpace(input.SourceID),
		SessionID:       strings.TrimSpace(input.SessionID),
		ChatID:          input.ChatID,
		UserID:          input.UserID,
		Scope:           NormalizeScopeRef(input.Scope),
		AuthorityClass:  evidenceToken(input.AuthorityClass),
		EpistemicStatus: status,
		RedactionClass:  redaction,
		SubjectKey:      strings.TrimSpace(input.SubjectKey),
		Summary:         clampStoreText(input.Summary, 2000),
		Digest:          clampStoreText(input.Digest, 8000),
		PayloadJSON:     payload,
		PayloadHash:     hash,
		ObservedAt:      input.ObservedAt.UTC(),
		CreatedAt:       input.CreatedAt.UTC(),
	}, nil
}

func normalizeEvidencePayloadJSON(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "{}", nil
	}
	var value any
	if err := json.Unmarshal([]byte(raw), &value); err != nil {
		wrapped, marshalErr := json.Marshal(map[string]string{"text": raw})
		if marshalErr != nil {
			return "", marshalErr
		}
		return string(wrapped), nil
	}
	canonical, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return string(canonical), nil
}

func evidencePayloadHash(payload string) string {
	sum := sha256.Sum256([]byte(payload))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func evidenceToken(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return ""
	}
	var b strings.Builder
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '_' || r == '-' || r == '.' || r == ':':
			b.WriteRune(r)
		case r == ' ' || r == '/' || r == '\\':
			b.WriteRune('_')
		}
	}
	return strings.Trim(b.String(), "_")
}

func evidenceObjectSelectSQL() string {
	return `SELECT evidence_id, evidence_type, source_kind, source_ref, source_table, source_id,
		session_id, chat_id, user_id, scope_kind, scope_id, durable_agent_id,
		authority_class, epistemic_status, redaction_class, subject_key,
		summary, digest, payload_json, payload_hash, observed_at, created_at
		FROM evidence_objects`
}

type evidenceObjectScanner interface {
	Scan(dest ...any) error
}

func scanEvidenceObject(scanner evidenceObjectScanner) (EvidenceObject, error) {
	var obj EvidenceObject
	var scopeKind, durableAgentID string
	var observedRaw, createdRaw string
	if err := scanner.Scan(
		&obj.ID, &obj.EvidenceType, &obj.SourceKind, &obj.SourceRef, &obj.SourceTable, &obj.SourceID,
		&obj.SessionID, &obj.ChatID, &obj.UserID, &scopeKind, &obj.Scope.ID, &durableAgentID,
		&obj.AuthorityClass, &obj.EpistemicStatus, &obj.RedactionClass, &obj.SubjectKey,
		&obj.Summary, &obj.Digest, &obj.PayloadJSON, &obj.PayloadHash, &observedRaw, &createdRaw,
	); err != nil {
		return EvidenceObject{}, err
	}
	obj.Scope.Kind = ScopeKind(scopeKind)
	obj.Scope.DurableAgentID = durableAgentID
	obj.ObservedAt = mustParseSQLiteTime(observedRaw)
	obj.CreatedAt = mustParseSQLiteTime(createdRaw)
	return obj, nil
}

func scanEvidenceObjects(rows *sql.Rows) ([]EvidenceObject, error) {
	var out []EvidenceObject
	for rows.Next() {
		obj, err := scanEvidenceObject(rows)
		if err != nil {
			return nil, fmt.Errorf("scan evidence object: %w", err)
		}
		out = append(out, obj)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate evidence objects: %w", err)
	}
	return out, nil
}

type evidenceCandidate struct {
	object EvidenceObject
	score  float64
}

func (s *SQLiteStore) evidenceHydrationCandidates(query EvidenceHydrationQuery, sessionID string, limit int) ([]evidenceCandidate, error) {
	if limit <= 0 {
		limit = 64
	}
	args := []any{sessionID}
	where := `WHERE session_id = ?`
	if query.IncludeCrossScope && query.Key.ChatID != 0 {
		where = `WHERE (session_id = ? OR chat_id = ?)`
		args = append(args, query.Key.ChatID)
	}
	args = append(args, limit)
	rows, err := s.db.Query(evidenceObjectSelectSQL()+`
		`+where+`
		ORDER BY observed_at DESC, evidence_id DESC
		LIMIT ?
	`, args...)
	if err != nil {
		return nil, fmt.Errorf("query evidence hydration candidates: %w", err)
	}
	defer rows.Close()
	objects, err := scanEvidenceObjects(rows)
	if err != nil {
		return nil, err
	}
	out := make([]evidenceCandidate, 0, len(objects))
	for _, obj := range objects {
		out = append(out, evidenceCandidate{object: obj})
	}
	return out, nil
}

func scoreEvidenceCandidates(candidates []evidenceCandidate, query EvidenceHydrationQuery) {
	terms := evidenceQueryTerms(query.Query)
	operationID := strings.TrimSpace(query.OperationID)
	now := query.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	for i := range candidates {
		obj := candidates[i].object
		score := 1.0
		switch obj.SourceKind {
		case EvidenceSourceOperationState, EvidenceSourceContinuationState, EvidenceSourcePlanState:
			score += 4
		case EvidenceSourceExecutionEvent, EvidenceSourceTurnRun, EvidenceSourceToolOutput:
			score += 3
		case EvidenceSourceCapabilityGrant, EvidenceSourceCapabilityInvocation, EvidenceSourceCapabilityRequest:
			score += 2.5
		case EvidenceSourceMessage:
			score += 1
		}
		switch obj.EpistemicStatus {
		case EvidenceStatusAttested:
			score += 2.5
		case EvidenceStatusObserved:
			score += 2
		case EvidenceStatusProjection:
			score += 1
		case EvidenceStatusClaimed:
			score -= 1
		case EvidenceStatusGap:
			score -= 2
		}
		if operationID != "" && (strings.Contains(obj.SourceRef, operationID) || strings.Contains(obj.SubjectKey, operationID) || strings.Contains(obj.PayloadJSON, operationID)) {
			score += 5
		}
		haystack := strings.ToLower(strings.Join([]string{obj.Summary, obj.Digest, obj.SubjectKey, obj.SourceRef, obj.PayloadJSON}, " "))
		for _, term := range terms {
			if strings.Contains(haystack, term) {
				score += 1.5
			}
		}
		age := now.Sub(obj.ObservedAt)
		switch {
		case age < 0:
			score += 1
		case age <= time.Hour:
			score += 3
		case age <= 24*time.Hour:
			score += 2
		case age <= 7*24*time.Hour:
			score += 1
		}
		candidates[i].score = score
	}
}

func (s *SQLiteStore) latestEvidenceSnapshots(sessionID string, limit int) ([]EvidenceObject, error) {
	rows, err := s.db.Query(evidenceObjectSelectSQL()+`
		WHERE session_id = ?
			AND source_kind IN (?, ?, ?, ?)
		ORDER BY observed_at DESC, evidence_id DESC
		LIMIT ?
	`, sessionID, EvidenceSourceOperationState, EvidenceSourceContinuationState, EvidenceSourcePlanState, EvidenceSourceSessionState, limit)
	if err != nil {
		return nil, fmt.Errorf("query latest evidence snapshots: %w", err)
	}
	defer rows.Close()
	return scanEvidenceObjects(rows)
}

func evidenceQueryTerms(query string) []string {
	words := strings.Fields(strings.ToLower(query))
	seen := map[string]struct{}{}
	out := make([]string, 0, len(words))
	for _, word := range words {
		word = strings.Trim(word, " \t\r\n.,:;!?()[]{}\"'`")
		if len(word) < 4 {
			continue
		}
		if _, ok := seen[word]; ok {
			continue
		}
		seen[word] = struct{}{}
		out = append(out, word)
	}
	return out
}

func evidenceHydrationStatus(result EvidenceHydrationResult) string {
	if len(result.MissingEvidenceIDs) > 0 {
		return "gaps"
	}
	if result.FallbackUsed {
		return "fallback"
	}
	return "completed"
}

func evidenceObjectIDs(objects []EvidenceObject) []string {
	out := make([]string, 0, len(objects))
	for _, obj := range objects {
		if strings.TrimSpace(obj.ID) != "" {
			out = append(out, obj.ID)
		}
	}
	return normalizeStringList(out)
}

func encodeStringList(values []string) string {
	values = normalizeStringList(values)
	raw, err := json.Marshal(values)
	if err != nil {
		return "[]"
	}
	return string(raw)
}

func decodeStringList(raw string) []string {
	var values []string
	if err := json.Unmarshal([]byte(strings.TrimSpace(raw)), &values); err != nil {
		return nil
	}
	return normalizeStringList(values)
}

func boundedEvidenceHydrationLimit(limit int) int {
	if limit <= 0 {
		return defaultEvidenceHydrationLimit
	}
	if limit > maxEvidenceHydrationLimit {
		return maxEvidenceHydrationLimit
	}
	return limit
}

func normalizeStringList(values []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func normalizeStringSet(values []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func firstEvidenceValue(value string, fallback string) string {
	if strings.TrimSpace(value) != "" {
		return value
	}
	return fallback
}

func executionEventEvidenceInput(event ExecutionEvent) EvidenceObjectInput {
	payload := event.PayloadJSON
	if strings.TrimSpace(payload) == "" {
		payload = "{}"
	}
	return EvidenceObjectInput{
		SourceKind:      EvidenceSourceExecutionEvent,
		SourceRef:       fmt.Sprintf("execution_events:%d", event.ID),
		SourceTable:     "execution_events",
		SourceID:        fmt.Sprintf("%d", event.ID),
		SessionID:       event.SessionID,
		ChatID:          event.ChatID,
		UserID:          event.UserID,
		Scope:           event.Scope,
		EvidenceType:    EvidenceSourceExecutionEvent,
		EpistemicStatus: EvidenceStatusAttested,
		SubjectKey:      event.EventType,
		Summary:         strings.TrimSpace(event.EventType + " " + event.Stage + " " + event.Status),
		Digest:          payload,
		PayloadJSON:     payload,
		ObservedAt:      event.CreatedAt,
	}
}

func messageEvidenceInput(msg Message, scope ScopeRef) EvidenceObjectInput {
	status := EvidenceStatusObserved
	if strings.TrimSpace(msg.Role) == "assistant" {
		status = EvidenceStatusClaimed
	}
	payloadRaw, _ := json.Marshal(map[string]any{
		"id":                  msg.ID,
		"role":                msg.Role,
		"content":             msg.Content,
		"floor_content":       msg.FloorContent,
		"floor_metadata":      msg.FloorMetadata,
		"tool_calls":          msg.ToolCalls,
		"tool_id":             msg.ToolID,
		"tool_name":           msg.ToolName,
		"turn_index":          msg.TurnIndex,
		"actor_user_id":       msg.ActorUserID,
		"actor_role":          msg.ActorRole,
		"event_origin":        msg.EventOrigin,
		"event_origin_detail": msg.EventOriginDetail,
	})
	return EvidenceObjectInput{
		SourceKind:      EvidenceSourceMessage,
		SourceRef:       fmt.Sprintf("messages:%d", msg.ID),
		SourceTable:     "messages",
		SourceID:        fmt.Sprintf("%d", msg.ID),
		SessionID:       msg.SessionID,
		ChatID:          msg.ChatID,
		UserID:          msg.UserID,
		Scope:           scope,
		EvidenceType:    EvidenceSourceMessage,
		EpistemicStatus: status,
		SubjectKey:      strings.TrimSpace(msg.Role),
		Summary:         fmt.Sprintf("%s message turn %d", strings.TrimSpace(msg.Role), msg.TurnIndex),
		Digest:          clampStoreText(msg.Content, 1000),
		PayloadJSON:     string(payloadRaw),
		ObservedAt:      nonZeroTimeOrNow(msg.CreatedAt, time.Now().UTC()),
	}
}

func turnRunEvidenceInput(run TurnRun) EvidenceObjectInput {
	observed := run.LastActivityAt
	if !run.CompletedAt.IsZero() {
		observed = run.CompletedAt
	}
	if observed.IsZero() {
		observed = run.StartedAt
	}
	payloadRaw, _ := json.Marshal(map[string]any{
		"id":                          run.ID,
		"kind":                        run.Kind,
		"status":                      run.Status,
		"request_text":                run.RequestText,
		"turn_index":                  run.TurnIndex,
		"tool_calls_started":          run.ToolCallsStarted,
		"tool_calls_finished":         run.ToolCallsFinished,
		"total_tool_chars_in":         run.TotalToolCharsIn,
		"total_assistant_chars_out":   run.TotalAssistantCharsOut,
		"provider_input_tokens":       run.ProviderInputTokens,
		"provider_output_tokens":      run.ProviderOutputTokens,
		"provider_cache_read_tokens":  run.ProviderCacheReadTokens,
		"provider_cache_write_tokens": run.ProviderCacheWriteTokens,
		"last_tool_name":              run.LastToolName,
		"last_tool_preview":           run.LastToolPreview,
		"last_tool_result_preview":    run.LastToolResultPreview,
		"last_tool_error":             run.LastToolError,
		"error_text":                  run.ErrorText,
		"recovery_summary":            run.RecoverySummary,
	})
	return EvidenceObjectInput{
		SourceKind:      EvidenceSourceTurnRun,
		SourceRef:       fmt.Sprintf("turn_runs:%d:%s:%s", run.ID, run.Status, observed.UTC().Format(time.RFC3339Nano)),
		SourceTable:     "turn_runs",
		SourceID:        fmt.Sprintf("%d", run.ID),
		SessionID:       run.SessionID,
		ChatID:          run.ChatID,
		UserID:          run.UserID,
		Scope:           run.Scope,
		EvidenceType:    EvidenceSourceTurnRun,
		EpistemicStatus: EvidenceStatusAttested,
		SubjectKey:      string(run.Kind),
		Summary:         fmt.Sprintf("%s turn %s", run.Kind, run.Status),
		Digest:          firstEvidenceValue(run.RecoverySummary, run.RequestText),
		PayloadJSON:     string(payloadRaw),
		ObservedAt:      observed,
	}
}

func upsertSessionEvidenceSnapshotsTx(tx *sql.Tx, sess *Session, now time.Time) error {
	if sess == nil {
		return nil
	}
	scope := NormalizeScopeRef(sess.Scope)
	if scope.IsZero() {
		scope = defaultScopeForKey(SessionKey{ChatID: sess.ChatID, UserID: sess.UserID, Scope: sess.Scope})
	}
	sessionID := strings.TrimSpace(sess.SessionID)
	if sessionID == "" {
		sessionID = SessionIDFromParts(sess.ChatID, sess.UserID, scope)
	}
	snapshots := []EvidenceObjectInput{
		sessionStateEvidenceInput(sessionID, sess.ChatID, sess.UserID, scope, "plan_state", EvidenceSourcePlanState, encodePlanState(sess.PlanState), now),
		sessionStateEvidenceInput(sessionID, sess.ChatID, sess.UserID, scope, "operation_state", EvidenceSourceOperationState, encodeOperationState(sess.OperationState), now),
		sessionStateEvidenceInput(sessionID, sess.ChatID, sess.UserID, scope, "continuation_state", EvidenceSourceContinuationState, encodeContinuationState(sess.ContinuationState), now),
	}
	sessionPayload, _ := json.Marshal(map[string]any{
		"session_id":          sessionID,
		"turn_count":          sess.TurnCount,
		"last_provider":       sess.LastProvider,
		"last_model":          sess.LastModel,
		"last_error":          sess.LastError,
		"total_input_tokens":  sess.TotalInputTokens,
		"total_output_tokens": sess.TotalOutputTokens,
	})
	snapshots = append(snapshots, sessionStateEvidenceInput(sessionID, sess.ChatID, sess.UserID, scope, "session_state", EvidenceSourceSessionState, string(sessionPayload), now))
	for _, snapshot := range snapshots {
		if _, err := upsertEvidenceObjectTx(tx, snapshot); err != nil {
			return fmt.Errorf("write session evidence snapshot: %w", err)
		}
	}
	return nil
}
