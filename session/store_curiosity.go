//go:build linux

package session

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

func (s *SQLiteStore) EnsureCuriosityLease(lease CuriosityLease, now time.Time) (CuriosityLease, error) {
	if s == nil {
		return CuriosityLease{}, fmt.Errorf("store is nil")
	}
	lease = NormalizeCuriosityLease(lease, now)
	if lease.ID == "" {
		return CuriosityLease{}, fmt.Errorf("curiosity lease id is required")
	}
	kindsJSON, err := encodeCuriosityStringList(lease.AllowedSourceKinds)
	if err != nil {
		return CuriosityLease{}, err
	}
	refsJSON, err := encodeCuriosityStringList(lease.AllowedSourceRefs)
	if err != nil {
		return CuriosityLease{}, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return CuriosityLease{}, fmt.Errorf("begin curiosity lease tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`
		INSERT INTO curiosity_leases(
			id, status, scope_kind, scope_id, scope_durable_agent_id, lease_class, work_action,
			allowed_source_kinds_json, allowed_source_refs_json, daily_turn_budget, max_looks_per_turn,
			turns_used, period_start, approved_by, created_at, expires_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			scope_kind = excluded.scope_kind,
			scope_id = excluded.scope_id,
			scope_durable_agent_id = excluded.scope_durable_agent_id,
			lease_class = excluded.lease_class,
			work_action = excluded.work_action,
			allowed_source_kinds_json = excluded.allowed_source_kinds_json,
			allowed_source_refs_json = excluded.allowed_source_refs_json,
			daily_turn_budget = excluded.daily_turn_budget,
			max_looks_per_turn = excluded.max_looks_per_turn,
			approved_by = excluded.approved_by,
			expires_at = excluded.expires_at,
			updated_at = excluded.updated_at
	`, lease.ID, lease.Status, string(lease.Scope.Kind), lease.Scope.ID, lease.Scope.DurableAgentID, lease.LeaseClass, lease.WorkAction,
		kindsJSON, refsJSON, lease.DailyTurnBudget, lease.MaxLooksPerTurn, lease.TurnsUsed, lease.PeriodStart, lease.ApprovedBy,
		lease.CreatedAt.UTC().Format(time.RFC3339Nano), lease.ExpiresAt.UTC().Format(time.RFC3339Nano), lease.UpdatedAt.UTC().Format(time.RFC3339Nano)); err != nil {
		return CuriosityLease{}, fmt.Errorf("upsert curiosity lease: %w", err)
	}
	out, err := curiosityLeaseByIDTx(tx, lease.ID)
	if err != nil {
		return CuriosityLease{}, err
	}
	if err := pruneCuriosityRecordsTx(tx, now); err != nil {
		return CuriosityLease{}, err
	}
	if err := tx.Commit(); err != nil {
		return CuriosityLease{}, fmt.Errorf("commit curiosity lease tx: %w", err)
	}
	return out, nil
}

func (s *SQLiteStore) ConsumeCuriosityLeaseTurn(id string, now time.Time) (CuriosityLease, bool, error) {
	if s == nil {
		return CuriosityLease{}, false, fmt.Errorf("store is nil")
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return CuriosityLease{}, false, fmt.Errorf("curiosity lease id is required")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	tx, err := s.db.Begin()
	if err != nil {
		return CuriosityLease{}, false, fmt.Errorf("begin curiosity consume tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	lease, err := curiosityLeaseByIDTx(tx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return CuriosityLease{}, false, nil
	}
	if err != nil {
		return CuriosityLease{}, false, err
	}
	if lease.Status != CuriosityLeaseStatusActive {
		if err := tx.Commit(); err != nil {
			return CuriosityLease{}, false, fmt.Errorf("commit curiosity inactive tx: %w", err)
		}
		return lease, false, nil
	}
	if !lease.ExpiresAt.IsZero() && !now.Before(lease.ExpiresAt) {
		lease.Status = CuriosityLeaseStatusExpired
		lease.UpdatedAt = now
		if err := updateCuriosityLeaseStatusTx(tx, lease.ID, lease.Status, now); err != nil {
			return CuriosityLease{}, false, err
		}
		if err := tx.Commit(); err != nil {
			return CuriosityLease{}, false, fmt.Errorf("commit curiosity expired tx: %w", err)
		}
		return lease, false, nil
	}
	if lease.DailyTurnBudget <= 0 || lease.TurnsUsed >= lease.DailyTurnBudget {
		lease.Status = CuriosityLeaseStatusExhausted
		lease.UpdatedAt = now
		if err := updateCuriosityLeaseStatusTx(tx, lease.ID, lease.Status, now); err != nil {
			return CuriosityLease{}, false, err
		}
		if err := tx.Commit(); err != nil {
			return CuriosityLease{}, false, fmt.Errorf("commit curiosity exhausted tx: %w", err)
		}
		return lease, false, nil
	}
	lease.TurnsUsed++
	lease.UpdatedAt = now
	if lease.TurnsUsed >= lease.DailyTurnBudget {
		lease.Status = CuriosityLeaseStatusExhausted
	}
	if _, err := tx.Exec(`
		UPDATE curiosity_leases
		SET turns_used = ?,
			status = ?,
			updated_at = ?
		WHERE id = ?
	`, lease.TurnsUsed, lease.Status, now.Format(time.RFC3339Nano), lease.ID); err != nil {
		return CuriosityLease{}, false, fmt.Errorf("consume curiosity lease turn: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return CuriosityLease{}, false, fmt.Errorf("commit curiosity consume tx: %w", err)
	}
	return lease, true, nil
}

func (s *SQLiteStore) RecordCuriosityObservation(key SessionKey, input CuriosityObservationInput, now time.Time) (CuriosityObservation, error) {
	if s == nil {
		return CuriosityObservation{}, fmt.Errorf("store is nil")
	}
	key.Scope = defaultScopeForKey(key)
	sessionID := SessionIDForKey(key)
	if sessionID == "" {
		return CuriosityObservation{}, fmt.Errorf("record curiosity observation: session_id is required")
	}
	input = NormalizeCuriosityObservationInput(input, now)
	if input.LeaseID == "" {
		return CuriosityObservation{}, fmt.Errorf("record curiosity observation: lease_id is required")
	}
	if input.CandidateID == "" {
		return CuriosityObservation{}, fmt.Errorf("record curiosity observation: candidate_id is required")
	}
	if input.SourceKind == "" || input.SourceRef == "" {
		return CuriosityObservation{}, fmt.Errorf("record curiosity observation: source is required")
	}
	if input.Summary == "" {
		return CuriosityObservation{}, fmt.Errorf("record curiosity observation: summary is required")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	evidenceJSON := encodeRecordReferences(input.Evidence)
	tx, err := s.db.Begin()
	if err != nil {
		return CuriosityObservation{}, fmt.Errorf("begin curiosity observation tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	res, err := tx.Exec(`
		INSERT OR IGNORE INTO curiosity_observations(
			lease_id, session_id, chat_id, user_id, scope_kind, scope_id, scope_durable_agent_id,
			candidate_id, source_kind, source_ref, subject_key, summary, evidence_json,
			content_hash, confidence, observed_at, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, input.LeaseID, sessionID, key.ChatID, key.UserID, string(key.Scope.Kind), key.Scope.ID, key.Scope.DurableAgentID,
		input.CandidateID, input.SourceKind, input.SourceRef, input.SubjectKey, input.Summary, evidenceJSON,
		input.ContentHash, input.Confidence, input.ObservedAt.UTC().Format(time.RFC3339Nano), now.Format(time.RFC3339Nano))
	if err != nil {
		return CuriosityObservation{}, fmt.Errorf("insert curiosity observation: %w", err)
	}
	var out CuriosityObservation
	rows, _ := res.RowsAffected()
	if rows == 0 {
		row := tx.QueryRow(curiosityObservationSelectSQL()+`
			WHERE lease_id = ? AND candidate_id = ? AND content_hash = ?
		`, input.LeaseID, input.CandidateID, input.ContentHash)
		out, err = scanCuriosityObservation(row)
	} else {
		id, _ := res.LastInsertId()
		row := tx.QueryRow(curiosityObservationSelectSQL()+` WHERE id = ?`, id)
		out, err = scanCuriosityObservation(row)
	}
	if err != nil {
		return CuriosityObservation{}, err
	}
	if err := pruneCuriosityRecordsTx(tx, now); err != nil {
		return CuriosityObservation{}, err
	}
	if err := tx.Commit(); err != nil {
		return CuriosityObservation{}, fmt.Errorf("commit curiosity observation tx: %w", err)
	}
	return out, nil
}

func (s *SQLiteStore) CuriosityObservations(limit int) ([]CuriosityObservation, error) {
	if s == nil {
		return nil, fmt.Errorf("store is nil")
	}
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	rows, err := s.db.Query(curiosityObservationSelectSQL()+`
		ORDER BY observed_at DESC, id DESC
		LIMIT ?
	`, limit)
	if err != nil {
		return nil, fmt.Errorf("query curiosity observations: %w", err)
	}
	defer rows.Close()
	out := make([]CuriosityObservation, 0, limit)
	for rows.Next() {
		record, err := scanCuriosityObservation(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate curiosity observations: %w", err)
	}
	return out, nil
}

func curiosityLeaseByIDTx(tx *sql.Tx, id string) (CuriosityLease, error) {
	row := tx.QueryRow(`
		SELECT id, status, scope_kind, scope_id, scope_durable_agent_id, lease_class, work_action,
			allowed_source_kinds_json, allowed_source_refs_json, daily_turn_budget, max_looks_per_turn,
			turns_used, period_start, approved_by, created_at, expires_at, updated_at
		FROM curiosity_leases
		WHERE id = ?
	`, id)
	return scanCuriosityLease(row)
}

func updateCuriosityLeaseStatusTx(tx *sql.Tx, id string, status string, now time.Time) error {
	if _, err := tx.Exec(`
		UPDATE curiosity_leases
		SET status = ?, updated_at = ?
		WHERE id = ?
	`, strings.TrimSpace(status), now.UTC().Format(time.RFC3339Nano), strings.TrimSpace(id)); err != nil {
		return fmt.Errorf("update curiosity lease status: %w", err)
	}
	return nil
}

func pruneCuriosityRecordsTx(tx *sql.Tx, now time.Time) error {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	observationCutoff := now.Add(-CuriosityObservationRetention).Format(time.RFC3339Nano)
	if _, err := tx.Exec(`
		DELETE FROM curiosity_observations
		WHERE observed_at < ?
	`, observationCutoff); err != nil {
		return fmt.Errorf("prune curiosity observations: %w", err)
	}
	leaseCutoff := now.Add(-CuriosityLeaseRetention).Format(time.RFC3339Nano)
	if _, err := tx.Exec(`
		DELETE FROM curiosity_leases
		WHERE expires_at < ?
	`, leaseCutoff); err != nil {
		return fmt.Errorf("prune curiosity leases: %w", err)
	}
	return nil
}

func scanCuriosityLease(scanner interface {
	Scan(dest ...any) error
}) (CuriosityLease, error) {
	var lease CuriosityLease
	var scopeKind, scopeID, scopeDurableAgentID string
	var kindsRaw, refsRaw string
	var createdRaw, expiresRaw, updatedRaw string
	if err := scanner.Scan(
		&lease.ID,
		&lease.Status,
		&scopeKind,
		&scopeID,
		&scopeDurableAgentID,
		&lease.LeaseClass,
		&lease.WorkAction,
		&kindsRaw,
		&refsRaw,
		&lease.DailyTurnBudget,
		&lease.MaxLooksPerTurn,
		&lease.TurnsUsed,
		&lease.PeriodStart,
		&lease.ApprovedBy,
		&createdRaw,
		&expiresRaw,
		&updatedRaw,
	); err != nil {
		return CuriosityLease{}, err
	}
	var err error
	lease.Scope = ScopeRef{Kind: ScopeKind(scopeKind), ID: scopeID, DurableAgentID: scopeDurableAgentID}
	lease.AllowedSourceKinds = decodeCuriosityStringList(kindsRaw)
	lease.AllowedSourceRefs = decodeCuriosityStringList(refsRaw)
	if lease.CreatedAt, err = parseSQLiteTime(createdRaw); err != nil {
		return CuriosityLease{}, fmt.Errorf("parse curiosity lease created_at: %w", err)
	}
	if lease.ExpiresAt, err = parseSQLiteTime(expiresRaw); err != nil {
		return CuriosityLease{}, fmt.Errorf("parse curiosity lease expires_at: %w", err)
	}
	if lease.UpdatedAt, err = parseSQLiteTime(updatedRaw); err != nil {
		return CuriosityLease{}, fmt.Errorf("parse curiosity lease updated_at: %w", err)
	}
	return NormalizeCuriosityLease(lease, lease.UpdatedAt), nil
}

func curiosityObservationSelectSQL() string {
	return `SELECT id, lease_id, session_id, chat_id, user_id, scope_kind, scope_id, scope_durable_agent_id,
		candidate_id, source_kind, source_ref, subject_key, summary, evidence_json, content_hash,
		confidence, observed_at, created_at
		FROM curiosity_observations`
}

func scanCuriosityObservation(scanner interface {
	Scan(dest ...any) error
}) (CuriosityObservation, error) {
	var record CuriosityObservation
	var scopeKind, scopeID, scopeDurableAgentID string
	var evidenceRaw string
	var observedRaw, createdRaw string
	if err := scanner.Scan(
		&record.ID,
		&record.LeaseID,
		&record.SessionID,
		&record.ChatID,
		&record.UserID,
		&scopeKind,
		&scopeID,
		&scopeDurableAgentID,
		&record.CandidateID,
		&record.SourceKind,
		&record.SourceRef,
		&record.SubjectKey,
		&record.Summary,
		&evidenceRaw,
		&record.ContentHash,
		&record.Confidence,
		&observedRaw,
		&createdRaw,
	); err != nil {
		return CuriosityObservation{}, err
	}
	var err error
	record.Scope = ScopeRef{Kind: ScopeKind(scopeKind), ID: scopeID, DurableAgentID: scopeDurableAgentID}
	record.Evidence = decodeRecordReferences(evidenceRaw)
	record.Confidence = clampCuriosityConfidence(record.Confidence)
	if record.ObservedAt, err = parseSQLiteTime(observedRaw); err != nil {
		return CuriosityObservation{}, fmt.Errorf("parse curiosity observation observed_at: %w", err)
	}
	if record.CreatedAt, err = parseSQLiteTime(createdRaw); err != nil {
		return CuriosityObservation{}, fmt.Errorf("parse curiosity observation created_at: %w", err)
	}
	return record, nil
}

func encodeCuriosityStringList(values []string) (string, error) {
	values = normalizeCuriosityRefs(values)
	if len(values) == 0 {
		return "[]", nil
	}
	raw, err := json.Marshal(values)
	if err != nil {
		return "", fmt.Errorf("encode curiosity string list: %w", err)
	}
	return string(raw), nil
}

func decodeCuriosityStringList(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	var values []string
	if err := json.Unmarshal([]byte(raw), &values); err != nil {
		return nil
	}
	return normalizeCuriosityRefs(values)
}
