//go:build linux

package session

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

func (s *SQLiteStore) UpsertDurableChildAgreement(record DurableChildAgreement) (DurableChildAgreement, error) {
	record = NormalizeDurableChildAgreement(record)
	if record.AgreementID == "" {
		return DurableChildAgreement{}, fmt.Errorf("durable child agreement id is required")
	}
	if record.AgentID == "" {
		return DurableChildAgreement{}, fmt.Errorf("durable child agreement agent_id is required")
	}
	if record.Summary == "" {
		return DurableChildAgreement{}, fmt.Errorf("durable child agreement summary is required")
	}
	now := time.Now().UTC()
	createdAt := nonZeroTimeOrNow(record.CreatedAt, now).UTC()
	updatedAt := nonZeroTimeOrNow(record.UpdatedAt, now).UTC()
	if _, err := s.db.Exec(`
		INSERT INTO durable_child_agreements(
			agreement_id, agent_id, parent_principal, child_principal, source_surface, source_request_id,
			source_review_event_id, summary, bounded_effect, status, artifact_refs_json, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(agreement_id) DO UPDATE SET
			agent_id = excluded.agent_id,
			parent_principal = excluded.parent_principal,
			child_principal = excluded.child_principal,
			source_surface = excluded.source_surface,
			source_request_id = excluded.source_request_id,
			source_review_event_id = excluded.source_review_event_id,
			summary = excluded.summary,
			bounded_effect = excluded.bounded_effect,
			status = excluded.status,
			artifact_refs_json = excluded.artifact_refs_json,
			updated_at = excluded.updated_at
	`,
		record.AgreementID,
		record.AgentID,
		record.ParentPrincipal,
		record.ChildPrincipal,
		record.SourceSurface,
		record.SourceRequestID,
		record.SourceReviewEventID,
		record.Summary,
		record.BoundedEffect,
		string(record.Status),
		encodeRecordReferences(record.ArtifactRefs),
		createdAt.Format(time.RFC3339Nano),
		updatedAt.Format(time.RFC3339Nano),
	); err != nil {
		return DurableChildAgreement{}, fmt.Errorf("upsert durable child agreement: %w", err)
	}
	stored, ok, err := s.DurableChildAgreement(record.AgreementID)
	if err != nil {
		return DurableChildAgreement{}, err
	}
	if !ok {
		return DurableChildAgreement{}, fmt.Errorf("durable child agreement %q not found after upsert", record.AgreementID)
	}
	return stored, nil
}

func (s *SQLiteStore) DurableChildAgreement(agreementID string) (DurableChildAgreement, bool, error) {
	agreementID = strings.TrimSpace(agreementID)
	if agreementID == "" {
		return DurableChildAgreement{}, false, nil
	}
	row := s.db.QueryRow(`
		SELECT agreement_id, agent_id, parent_principal, child_principal, source_surface, source_request_id,
			source_review_event_id, summary, bounded_effect, status, artifact_refs_json, created_at, updated_at
		FROM durable_child_agreements
		WHERE agreement_id = ?
	`, agreementID)
	record, err := scanDurableChildAgreement(row)
	if errors.Is(err, sql.ErrNoRows) {
		return DurableChildAgreement{}, false, nil
	}
	if err != nil {
		return DurableChildAgreement{}, false, err
	}
	return record, true, nil
}

func (s *SQLiteStore) DurableChildAgreementsForAgent(agentID string, limit int) ([]DurableChildAgreement, error) {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return nil, nil
	}
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.db.Query(`
		SELECT agreement_id, agent_id, parent_principal, child_principal, source_surface, source_request_id,
			source_review_event_id, summary, bounded_effect, status, artifact_refs_json, created_at, updated_at
		FROM durable_child_agreements
		WHERE agent_id = ?
		ORDER BY updated_at DESC, agreement_id ASC
		LIMIT ?
	`, agentID, limit)
	if err != nil {
		return nil, fmt.Errorf("query durable child agreements: %w", err)
	}
	defer rows.Close()
	out := make([]DurableChildAgreement, 0, limit)
	for rows.Next() {
		record, err := scanDurableChildAgreement(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate durable child agreements: %w", err)
	}
	return out, nil
}

func (s *SQLiteStore) UpdateDurableChildAgreementStatusForRequest(requestID string, status DurableChildAgreementStatus) error {
	requestID = strings.TrimSpace(requestID)
	status = NormalizeDurableChildAgreementStatus(status)
	if requestID == "" || status == "" {
		return nil
	}
	if _, err := s.db.Exec(`
		UPDATE durable_child_agreements
		SET status = ?, updated_at = ?
		WHERE source_request_id = ?
	`, string(status), time.Now().UTC().Format(time.RFC3339Nano), requestID); err != nil {
		return fmt.Errorf("update durable child agreement status: %w", err)
	}
	return nil
}

type durableChildAgreementScanner interface {
	Scan(dest ...any) error
}

func scanDurableChildAgreement(scanner durableChildAgreementScanner) (DurableChildAgreement, error) {
	var record DurableChildAgreement
	var status string
	var artifactRefsRaw string
	var createdAtRaw string
	var updatedAtRaw string
	if err := scanner.Scan(
		&record.AgreementID,
		&record.AgentID,
		&record.ParentPrincipal,
		&record.ChildPrincipal,
		&record.SourceSurface,
		&record.SourceRequestID,
		&record.SourceReviewEventID,
		&record.Summary,
		&record.BoundedEffect,
		&status,
		&artifactRefsRaw,
		&createdAtRaw,
		&updatedAtRaw,
	); err != nil {
		return DurableChildAgreement{}, err
	}
	record.Status = DurableChildAgreementStatus(status)
	record.ArtifactRefs = decodeRecordReferences(artifactRefsRaw)
	createdAt, err := parseSQLiteTime(createdAtRaw)
	if err != nil {
		return DurableChildAgreement{}, fmt.Errorf("parse durable child agreement created_at: %w", err)
	}
	updatedAt, err := parseSQLiteTime(updatedAtRaw)
	if err != nil {
		return DurableChildAgreement{}, fmt.Errorf("parse durable child agreement updated_at: %w", err)
	}
	record.CreatedAt = createdAt
	record.UpdatedAt = updatedAt
	return NormalizeDurableChildAgreement(record), nil
}
