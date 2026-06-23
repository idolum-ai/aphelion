//go:build linux

package session

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
)

func (s *SQLiteStore) RecordExposureProjection(input ExposureProjectionInput) (ExposureProjectionRecord, error) {
	if s == nil || s.db == nil {
		return ExposureProjectionRecord{}, fmt.Errorf("exposure projection store unavailable")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return ExposureProjectionRecord{}, fmt.Errorf("begin exposure projection tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	record, err := recordExposureProjectionTx(tx, input)
	if err != nil {
		return ExposureProjectionRecord{}, err
	}
	if err := tx.Commit(); err != nil {
		return ExposureProjectionRecord{}, fmt.Errorf("commit exposure projection tx: %w", err)
	}
	return record, nil
}

func recordExposureProjectionTx(tx *sql.Tx, input ExposureProjectionInput) (ExposureProjectionRecord, error) {
	input = NormalizeExposureProjectionInput(input)
	sessionID := SessionIDForKey(input.Key)
	scope := defaultScopeForKey(input.Key)
	projection := ProjectToolResultForPurpose(input.RawText, input.Audience, input.Purpose)
	if input.ForceProtectedEvidence && strings.TrimSpace(input.RawText) != "" {
		projection.ProjectionKind = ExposureProjectionProtectedRef
		projection.Projection = string(ExposureProjectionProtectedRef)
		projection.Text = renderProtectedToolOutputProjection(input.Audience)
		projection.ProjectedBytes = len(projection.Text)
		projection.SensitivityProvenance = appendUniqueEvidenceString(projection.SensitivityProvenance, "policy:forced_protected_evidence")
		if projection.Sensitivity == "" || projection.Sensitivity == EvidenceRedactionNone || projection.Sensitivity == EvidenceRedactionDigest {
			projection.Sensitivity = EvidenceRedactionBlocked
		}
	}
	sourceHash := projection.SourceHash
	if input.ProjectionID == "" {
		input.ProjectionID = ExposureProjectionID(sessionID, input.SourceRef, input.Audience, input.Purpose, sourceHash)
	}
	if existing, ok, err := exposureProjectionByIDTx(tx, input.ProjectionID); err != nil {
		return ExposureProjectionRecord{}, err
	} else if ok {
		return existing, nil
	}

	protectedRef := ""
	if input.ForceProtectedEvidence || exposureProjectionNeedsProtectedEvidence(projection, input.RawText) {
		obj, err := upsertEvidenceObjectTx(tx, protectedToolOutputEvidenceInput(input, scope, projection))
		if err != nil {
			return ExposureProjectionRecord{}, fmt.Errorf("write protected tool output evidence: %w", err)
		}
		protectedRef = obj.ID
		projection.ProtectedRef = protectedRef
	}

	record := ExposureProjectionRecord{
		ProjectionID:          input.ProjectionID,
		SessionID:             sessionID,
		ChatID:                input.Key.ChatID,
		UserID:                input.Key.UserID,
		Scope:                 scope,
		TurnRunID:             input.TurnRunID,
		InvocationID:          input.InvocationID,
		ToolName:              input.ToolName,
		Audience:              projection.Audience,
		Purpose:               projection.Purpose,
		PolicyRef:             projection.PolicyRef,
		ProjectionKind:        projection.ProjectionKind,
		Sensitivity:           projection.Sensitivity,
		SensitivityProvenance: normalizeStringList(projection.SensitivityProvenance),
		SourceKind:            input.SourceKind,
		SourceRef:             input.SourceRef,
		SourceHash:            sourceHash,
		ProtectedEvidenceRef:  protectedRef,
		ProjectedText:         projection.Text,
		ProjectedHash:         exposureTextHash(projection.Text),
		RawBytes:              len(input.RawText),
		ProjectedBytes:        len(projection.Text),
		CreatedAt:             input.CreatedAt.UTC(),
	}
	if _, err := tx.Exec(`
		INSERT INTO exposure_projection_events(
			projection_id, session_id, chat_id, user_id, scope_kind, scope_id, durable_agent_id,
			turn_run_id, invocation_id, tool_name, audience, purpose, policy_ref,
			projection_kind, sensitivity, sensitivity_provenance_json,
			source_kind, source_ref, source_hash, protected_evidence_ref,
			projected_text, projected_hash, raw_bytes, projected_bytes, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, record.ProjectionID, record.SessionID, record.ChatID, record.UserID, string(record.Scope.Kind), record.Scope.ID, record.Scope.DurableAgentID,
		record.TurnRunID, record.InvocationID, record.ToolName, string(record.Audience), string(record.Purpose), record.PolicyRef,
		string(record.ProjectionKind), record.Sensitivity, encodeStringList(record.SensitivityProvenance),
		record.SourceKind, record.SourceRef, record.SourceHash, record.ProtectedEvidenceRef,
		record.ProjectedText, record.ProjectedHash, record.RawBytes, record.ProjectedBytes, record.CreatedAt.Format(time.RFC3339Nano)); err != nil {
		return ExposureProjectionRecord{}, fmt.Errorf("insert exposure projection %s: %w", record.ProjectionID, err)
	}
	payloadRaw, _ := json.Marshal(map[string]any{
		"projection_id":          record.ProjectionID,
		"run_id":                 record.TurnRunID,
		"turn_run_id":            record.TurnRunID,
		"invocation_id":          record.InvocationID,
		"tool":                   record.ToolName,
		"audience":               string(record.Audience),
		"purpose":                string(record.Purpose),
		"policy_ref":             record.PolicyRef,
		"projection_kind":        string(record.ProjectionKind),
		"sensitivity":            record.Sensitivity,
		"sensitivity_provenance": record.SensitivityProvenance,
		"source_kind":            record.SourceKind,
		"source_ref":             record.SourceRef,
		"source_hash":            record.SourceHash,
		"protected_evidence_ref": record.ProtectedEvidenceRef,
		"projected_hash":         record.ProjectedHash,
		"raw_bytes":              record.RawBytes,
		"projected_bytes":        record.ProjectedBytes,
	})
	if _, err := appendExecutionEventsTx(tx, input.Key, []ExecutionEventInput{{
		EventType:   core.ExecutionEventExposureProjected,
		Stage:       "exposure",
		Status:      string(record.ProjectionKind),
		PayloadJSON: string(payloadRaw),
		CreatedAt:   record.CreatedAt,
	}}); err != nil {
		return ExposureProjectionRecord{}, fmt.Errorf("append exposure projection event: %w", err)
	}
	return record, nil
}

func (s *SQLiteStore) ExposureProjectionsByTurnRun(key SessionKey, turnRunID int64, limit int) ([]ExposureProjectionRecord, error) {
	if s == nil || s.db == nil || turnRunID <= 0 {
		return nil, nil
	}
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.db.Query(exposureProjectionSelectSQL()+`
		WHERE session_id = ? AND turn_run_id = ?
		ORDER BY created_at ASC, projection_id ASC
		LIMIT ?
	`, SessionIDForKey(key), turnRunID, limit)
	if err != nil {
		return nil, fmt.Errorf("query exposure projections by turn run: %w", err)
	}
	defer rows.Close()
	return scanExposureProjectionRows(rows)
}

func exposureProjectionByIDTx(tx *sql.Tx, projectionID string) (ExposureProjectionRecord, bool, error) {
	projectionID = strings.TrimSpace(projectionID)
	if projectionID == "" {
		return ExposureProjectionRecord{}, false, nil
	}
	row := tx.QueryRow(exposureProjectionSelectSQL()+` WHERE projection_id = ?`, projectionID)
	record, err := scanExposureProjection(row)
	if errors.Is(err, sql.ErrNoRows) {
		return ExposureProjectionRecord{}, false, nil
	}
	if err != nil {
		return ExposureProjectionRecord{}, false, err
	}
	return record, true, nil
}

func exposureProjectionSelectSQL() string {
	return `SELECT projection_id, session_id, chat_id, user_id, scope_kind, scope_id, durable_agent_id,
		turn_run_id, invocation_id, tool_name, audience, purpose, policy_ref,
		projection_kind, sensitivity, sensitivity_provenance_json,
		source_kind, source_ref, source_hash, protected_evidence_ref,
		projected_text, projected_hash, raw_bytes, projected_bytes, created_at
		FROM exposure_projection_events`
}

func scanExposureProjectionRows(rows *sql.Rows) ([]ExposureProjectionRecord, error) {
	out := []ExposureProjectionRecord{}
	for rows.Next() {
		record, err := scanExposureProjection(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate exposure projection rows: %w", err)
	}
	return out, nil
}

func scanExposureProjection(scanner interface{ Scan(dest ...any) error }) (ExposureProjectionRecord, error) {
	var (
		record        ExposureProjectionRecord
		scopeKind     string
		scopeID       string
		durableID     string
		audience      string
		purpose       string
		kind          string
		provenanceRaw string
		createdRaw    string
	)
	if err := scanner.Scan(
		&record.ProjectionID, &record.SessionID, &record.ChatID, &record.UserID, &scopeKind, &scopeID, &durableID,
		&record.TurnRunID, &record.InvocationID, &record.ToolName, &audience, &purpose, &record.PolicyRef,
		&kind, &record.Sensitivity, &provenanceRaw,
		&record.SourceKind, &record.SourceRef, &record.SourceHash, &record.ProtectedEvidenceRef,
		&record.ProjectedText, &record.ProjectedHash, &record.RawBytes, &record.ProjectedBytes, &createdRaw,
	); err != nil {
		return ExposureProjectionRecord{}, fmt.Errorf("scan exposure projection: %w", err)
	}
	record.Scope = NormalizeScopeRef(ScopeRef{Kind: ScopeKind(scopeKind), ID: scopeID, DurableAgentID: durableID})
	record.Audience = normalizeExposureAudience(ExposureAudience(audience))
	record.Purpose = normalizeExposurePurpose(ExposurePurpose(purpose))
	record.ProjectionKind = normalizeExposureProjectionKind(ExposureProjectionKind(kind))
	record.SensitivityProvenance = decodeStringList(provenanceRaw)
	createdAt, err := parseSQLiteTime(createdRaw)
	if err != nil {
		return ExposureProjectionRecord{}, fmt.Errorf("parse exposure projection created_at: %w", err)
	}
	record.CreatedAt = createdAt
	return record, nil
}

func protectedToolOutputEvidenceInput(input ExposureProjectionInput, scope ScopeRef, projection ToolResultProjection) EvidenceObjectInput {
	sourceHashToken := strings.TrimPrefix(strings.TrimSpace(projection.SourceHash), "sha256:")
	if len(sourceHashToken) > 20 {
		sourceHashToken = sourceHashToken[:20]
	}
	sourceRef := strings.Join([]string{
		"tool_output",
		fmt.Sprintf("%d", input.TurnRunID),
		strings.TrimSpace(input.ToolName),
		strings.TrimSpace(input.InvocationID),
		sourceHashToken,
	}, ":")
	retentionNote := "raw tool output retained as protected evidence; ordinary projections must use the exposure projection ledger"
	payloadRaw, _ := json.Marshal(map[string]any{
		"turn_run_id":    input.TurnRunID,
		"invocation_id":  input.InvocationID,
		"tool":           input.ToolName,
		"output":         input.RawText,
		"source_ref":     input.SourceRef,
		"source_hash":    projection.SourceHash,
		"retention_note": retentionNote,
	})
	digest := projection.Text
	if projection.ProjectionKind == ExposureProjectionProtectedRef {
		digest = "<tool_output_protected>"
	}
	return EvidenceObjectInput{
		EvidenceType:    EvidenceSourceToolOutput,
		SourceKind:      EvidenceSourceToolOutput,
		SourceRef:       sourceRef,
		SourceTable:     EvidenceSourceToolOutput,
		SourceID:        input.SourceRef,
		SessionID:       SessionIDForKey(input.Key),
		ChatID:          input.Key.ChatID,
		UserID:          input.Key.UserID,
		Scope:           scope,
		EpistemicStatus: EvidenceStatusAttested,
		RedactionClass:  EvidenceRedactionBlocked,
		SubjectKey:      strings.TrimSpace(input.ToolName),
		Summary:         fmt.Sprintf("protected tool output tool=%s bytes=%d hash=%s", input.ToolName, len(input.RawText), projection.SourceHash),
		Digest:          clampStoreText(digest, 1000),
		PayloadJSON:     string(payloadRaw),
		ObservedAt:      input.CreatedAt.UTC(),
	}
}

func exposureProjectionNeedsProtectedEvidence(projection ToolResultProjection, raw string) bool {
	if strings.TrimSpace(raw) == "" {
		return false
	}
	switch projection.ProjectionKind {
	case ExposureProjectionRedacted, ExposureProjectionDigest, ExposureProjectionWithheld, ExposureProjectionProtectedRef:
		return true
	default:
		return false
	}
}
