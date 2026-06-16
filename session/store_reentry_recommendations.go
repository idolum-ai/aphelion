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

func (s *SQLiteStore) CreateReentryRecommendationIfAllowed(record ReentryRecommendation, now time.Time) (ReentryRecommendation, bool, string, error) {
	if s == nil {
		return ReentryRecommendation{}, false, "", fmt.Errorf("store is nil")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	record = NormalizeReentryRecommendation(record)
	if record.ID == "" {
		record.ID = generatedMissionID("reentry")
	}
	if record.Owner == "" {
		return ReentryRecommendation{}, false, "", fmt.Errorf("reentry recommendation owner is required")
	}
	if record.SessionID == "" {
		return ReentryRecommendation{}, false, "", fmt.Errorf("reentry recommendation session_id is required")
	}
	if record.TerminalFingerprint == "" {
		return ReentryRecommendation{}, false, "", fmt.Errorf("reentry recommendation terminal_fingerprint is required")
	}
	if len(record.Candidates) == 0 {
		return ReentryRecommendation{}, false, "", fmt.Errorf("reentry recommendation candidates are required")
	}
	record.Status = ReentryRecommendationStatusPending
	record.CreatedAt = nonZeroTimeOrNow(record.CreatedAt, now).UTC()
	record.UpdatedAt = nonZeroTimeOrNow(record.UpdatedAt, now).UTC()
	candidatesJSON, err := json.Marshal(record.Candidates)
	if err != nil {
		return ReentryRecommendation{}, false, "", fmt.Errorf("encode reentry recommendation candidates: %w", err)
	}

	tx, err := s.db.Begin()
	if err != nil {
		return ReentryRecommendation{}, false, "", fmt.Errorf("begin reentry recommendation tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	allowed, reason, err := reentryRecommendationAllowedTx(tx, record)
	if err != nil || !allowed {
		return ReentryRecommendation{}, allowed, reason, err
	}
	if _, err := tx.Exec(`
		INSERT INTO reentry_recommendations(
			id, owner, chat_id, sender_id, session_id, scope_kind, scope_id, scope_durable_agent_id,
			source_turn_run_id, terminal_fingerprint, status, candidates_json, selected_candidate_id,
			result_summary, delivery_message_id, created_at, shown_at, selected_at, ignored_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, record.ID, record.Owner, record.ChatID, record.SenderID, record.SessionID, string(record.Scope.Kind), record.Scope.ID, record.Scope.DurableAgentID,
		record.SourceTurnRunID, record.TerminalFingerprint, string(record.Status), string(candidatesJSON), record.SelectedCandidateID,
		record.ResultSummary, record.DeliveryMessageID, record.CreatedAt.UTC().Format(time.RFC3339Nano), nullableTimeRFC3339(record.ShownAt), nullableTimeRFC3339(record.SelectedAt), nullableTimeRFC3339(record.IgnoredAt), record.UpdatedAt.UTC().Format(time.RFC3339Nano)); err != nil {
		return ReentryRecommendation{}, false, "", fmt.Errorf("insert reentry recommendation: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return ReentryRecommendation{}, false, "", fmt.Errorf("commit reentry recommendation: %w", err)
	}
	stored, ok, err := s.ReentryRecommendation(record.ID)
	if err != nil {
		return ReentryRecommendation{}, false, "", err
	}
	if !ok {
		return ReentryRecommendation{}, false, "", fmt.Errorf("reentry recommendation %q not found after insert", record.ID)
	}
	return stored, true, "", nil
}

func reentryRecommendationAllowedTx(tx *sql.Tx, record ReentryRecommendation) (bool, string, error) {
	var existing int
	if err := tx.QueryRow(`
		SELECT COUNT(*)
		FROM reentry_recommendations
		WHERE session_id = ? AND terminal_fingerprint = ?
	`, record.SessionID, record.TerminalFingerprint).Scan(&existing); err != nil {
		return false, "", fmt.Errorf("check reentry recommendation fingerprint: %w", err)
	}
	if existing > 0 {
		return false, "same_terminal_fingerprint", nil
	}
	return true, "", nil
}

func (s *SQLiteStore) ReentryRecommendationTerminalFingerprintExists(sessionID string, fingerprint string) (bool, error) {
	if s == nil {
		return false, fmt.Errorf("store is nil")
	}
	sessionID = strings.TrimSpace(sessionID)
	fingerprint = strings.TrimSpace(fingerprint)
	if sessionID == "" || fingerprint == "" {
		return false, nil
	}
	var existing int
	if err := s.db.QueryRow(`
		SELECT COUNT(*)
		FROM reentry_recommendations
		WHERE session_id = ? AND terminal_fingerprint = ?
	`, sessionID, fingerprint).Scan(&existing); err != nil {
		return false, fmt.Errorf("check reentry recommendation fingerprint: %w", err)
	}
	return existing > 0, nil
}

func (s *SQLiteStore) ReentryRecommendation(id string) (ReentryRecommendation, bool, error) {
	if s == nil {
		return ReentryRecommendation{}, false, fmt.Errorf("store is nil")
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return ReentryRecommendation{}, false, nil
	}
	row := s.db.QueryRow(reentryRecommendationSelectSQL()+` WHERE id = ?`, id)
	record, err := scanReentryRecommendation(row)
	if errors.Is(err, sql.ErrNoRows) {
		return ReentryRecommendation{}, false, nil
	}
	if err != nil {
		return ReentryRecommendation{}, false, err
	}
	return record, true, nil
}

func (s *SQLiteStore) ReentryRecommendations(filter ReentryRecommendationFilter) ([]ReentryRecommendation, error) {
	if s == nil {
		return nil, fmt.Errorf("store is nil")
	}
	if filter.Limit <= 0 || filter.Limit > 100 {
		filter.Limit = 20
	}
	filter.Owner = strings.TrimSpace(filter.Owner)
	filter.SessionID = strings.TrimSpace(filter.SessionID)
	if strings.TrimSpace(string(filter.Status)) != "" {
		filter.Status = NormalizeReentryRecommendationStatus(filter.Status)
	}
	query := reentryRecommendationSelectSQL()
	args := make([]any, 0, 4)
	clauses := make([]string, 0, 3)
	if filter.Owner != "" {
		clauses = append(clauses, "owner = ?")
		args = append(args, filter.Owner)
	}
	if filter.SessionID != "" {
		clauses = append(clauses, "session_id = ?")
		args = append(args, filter.SessionID)
	}
	if filter.Status != "" {
		clauses = append(clauses, "status = ?")
		args = append(args, string(filter.Status))
	}
	if len(clauses) > 0 {
		query += " WHERE " + strings.Join(clauses, " AND ")
	}
	query += " ORDER BY updated_at DESC, created_at DESC, id ASC LIMIT ?"
	args = append(args, filter.Limit)
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("query reentry recommendations: %w", err)
	}
	defer rows.Close()
	out := make([]ReentryRecommendation, 0, filter.Limit)
	for rows.Next() {
		record, err := scanReentryRecommendation(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate reentry recommendations: %w", err)
	}
	return out, nil
}

func (s *SQLiteStore) MarkReentryRecommendationShown(id string, messageID int64, at time.Time) (ReentryRecommendation, bool, error) {
	return s.updateReentryRecommendationStatus(id, "", ReentryRecommendationStatusShown, "", messageID, at)
}

func (s *SQLiteStore) MarkReentryRecommendationSelected(id string, candidateID string, summary string, at time.Time) (ReentryRecommendation, bool, error) {
	return s.updateReentryRecommendationStatus(id, candidateID, ReentryRecommendationStatusSelected, summary, 0, at)
}

func (s *SQLiteStore) MarkReentryRecommendationIgnored(id string, summary string, at time.Time) (ReentryRecommendation, bool, error) {
	return s.updateReentryRecommendationStatus(id, "", ReentryRecommendationStatusIgnored, summary, 0, at)
}

func (s *SQLiteStore) MarkReentryRecommendationStale(id string, summary string, at time.Time) (ReentryRecommendation, bool, error) {
	return s.updateReentryRecommendationStatus(id, "", ReentryRecommendationStatusStale, summary, 0, at)
}

func (s *SQLiteStore) updateReentryRecommendationStatus(id string, candidateID string, status ReentryRecommendationStatus, summary string, messageID int64, at time.Time) (ReentryRecommendation, bool, error) {
	if s == nil {
		return ReentryRecommendation{}, false, fmt.Errorf("store is nil")
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return ReentryRecommendation{}, false, nil
	}
	status = NormalizeReentryRecommendationStatus(status)
	if at.IsZero() {
		at = time.Now().UTC()
	}
	var shownAt any
	var selectedAt any
	var ignoredAt any
	switch status {
	case ReentryRecommendationStatusShown:
		shownAt = at.UTC().Format(time.RFC3339Nano)
	case ReentryRecommendationStatusSelected:
		selectedAt = at.UTC().Format(time.RFC3339Nano)
	case ReentryRecommendationStatusIgnored:
		ignoredAt = at.UTC().Format(time.RFC3339Nano)
	}
	res, err := s.db.Exec(`
		UPDATE reentry_recommendations
		SET status = ?,
			selected_candidate_id = CASE WHEN ? != '' THEN ? ELSE selected_candidate_id END,
			result_summary = CASE WHEN ? != '' THEN ? ELSE result_summary END,
			delivery_message_id = CASE WHEN ? > 0 THEN ? ELSE delivery_message_id END,
			shown_at = COALESCE(?, shown_at),
			selected_at = COALESCE(?, selected_at),
			ignored_at = COALESCE(?, ignored_at),
			updated_at = ?
		WHERE id = ?
			AND status IN (?, ?)
	`, string(status), strings.TrimSpace(candidateID), strings.TrimSpace(candidateID), strings.TrimSpace(summary), strings.TrimSpace(summary), messageID, messageID,
		shownAt, selectedAt, ignoredAt, at.UTC().Format(time.RFC3339Nano), id,
		string(ReentryRecommendationStatusPending), string(ReentryRecommendationStatusShown))
	if err != nil {
		return ReentryRecommendation{}, false, fmt.Errorf("update reentry recommendation status: %w", err)
	}
	if changed, _ := res.RowsAffected(); changed == 0 {
		record, ok, err := s.ReentryRecommendation(id)
		return record, ok, err
	}
	record, ok, err := s.ReentryRecommendation(id)
	if err != nil || !ok {
		return record, ok, err
	}
	return record, true, nil
}

func reentryRecommendationSelectSQL() string {
	return `SELECT id, owner, chat_id, sender_id, session_id, scope_kind, scope_id, scope_durable_agent_id,
		source_turn_run_id, terminal_fingerprint, status, candidates_json, selected_candidate_id,
		result_summary, delivery_message_id, created_at, shown_at, selected_at, ignored_at, updated_at
		FROM reentry_recommendations`
}

func scanReentryRecommendation(scanner interface {
	Scan(dest ...any) error
}) (ReentryRecommendation, error) {
	var record ReentryRecommendation
	var scopeKind, scopeID, scopeDurableAgentID string
	var status string
	var candidatesRaw string
	var createdRaw, updatedRaw string
	var shownRaw, selectedRaw, ignoredRaw sql.NullString
	if err := scanner.Scan(
		&record.ID,
		&record.Owner,
		&record.ChatID,
		&record.SenderID,
		&record.SessionID,
		&scopeKind,
		&scopeID,
		&scopeDurableAgentID,
		&record.SourceTurnRunID,
		&record.TerminalFingerprint,
		&status,
		&candidatesRaw,
		&record.SelectedCandidateID,
		&record.ResultSummary,
		&record.DeliveryMessageID,
		&createdRaw,
		&shownRaw,
		&selectedRaw,
		&ignoredRaw,
		&updatedRaw,
	); err != nil {
		return ReentryRecommendation{}, err
	}
	createdAt, err := parseSQLiteTime(createdRaw)
	if err != nil {
		return ReentryRecommendation{}, fmt.Errorf("parse reentry recommendation created_at: %w", err)
	}
	updatedAt, err := parseSQLiteTime(updatedRaw)
	if err != nil {
		return ReentryRecommendation{}, fmt.Errorf("parse reentry recommendation updated_at: %w", err)
	}
	var candidates []ReentryRecommendationCandidate
	if strings.TrimSpace(candidatesRaw) != "" {
		if err := json.Unmarshal([]byte(candidatesRaw), &candidates); err != nil {
			return ReentryRecommendation{}, fmt.Errorf("decode reentry recommendation candidates: %w", err)
		}
	}
	record.Scope = ScopeRef{Kind: ScopeKind(scopeKind), ID: scopeID, DurableAgentID: scopeDurableAgentID}
	record.Status = ReentryRecommendationStatus(status)
	record.Candidates = candidates
	record.CreatedAt = createdAt
	record.UpdatedAt = updatedAt
	if shownRaw.Valid && strings.TrimSpace(shownRaw.String) != "" {
		if record.ShownAt, err = parseSQLiteTime(shownRaw.String); err != nil {
			return ReentryRecommendation{}, fmt.Errorf("parse reentry recommendation shown_at: %w", err)
		}
	}
	if selectedRaw.Valid && strings.TrimSpace(selectedRaw.String) != "" {
		if record.SelectedAt, err = parseSQLiteTime(selectedRaw.String); err != nil {
			return ReentryRecommendation{}, fmt.Errorf("parse reentry recommendation selected_at: %w", err)
		}
	}
	if ignoredRaw.Valid && strings.TrimSpace(ignoredRaw.String) != "" {
		if record.IgnoredAt, err = parseSQLiteTime(ignoredRaw.String); err != nil {
			return ReentryRecommendation{}, fmt.Errorf("parse reentry recommendation ignored_at: %w", err)
		}
	}
	return NormalizeReentryRecommendation(record), nil
}
