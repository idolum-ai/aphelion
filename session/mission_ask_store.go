//go:build linux

package session

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	MissionAskLowConfidenceCooldown      = 24 * time.Hour
	MissionAskHighConfidenceCooldown     = 4 * time.Hour
	MissionAskSameAssociationCooldown    = 24 * time.Hour
	MissionAskIgnoredAssociationCooldown = 7 * 24 * time.Hour
)

func (s *SQLiteStore) CreateMissionAskPromptIfAllowed(prompt MissionAskPrompt, now time.Time) (MissionAskPrompt, bool, string, error) {
	if s == nil {
		return MissionAskPrompt{}, false, "", fmt.Errorf("store is nil")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	prompt = NormalizeMissionAskPrompt(prompt)
	if prompt.ID == "" {
		prompt.ID = generatedMissionID("mission-ask")
	}
	if prompt.Owner == "" {
		return MissionAskPrompt{}, false, "", fmt.Errorf("mission ask owner is required")
	}
	if prompt.SessionID == "" {
		return MissionAskPrompt{}, false, "", fmt.Errorf("mission ask session_id is required")
	}
	if prompt.QuestionText == "" {
		return MissionAskPrompt{}, false, "", fmt.Errorf("mission ask question_text is required")
	}
	if prompt.SourceFingerprint == "" {
		return MissionAskPrompt{}, false, "", fmt.Errorf("mission ask source_fingerprint is required")
	}
	prompt.Status = MissionAskStatusPending
	prompt.CreatedAt = nonZeroTimeOrNow(prompt.CreatedAt, now).UTC()
	prompt.UpdatedAt = nonZeroTimeOrNow(prompt.UpdatedAt, now).UTC()

	tx, err := s.db.Begin()
	if err != nil {
		return MissionAskPrompt{}, false, "", fmt.Errorf("begin mission ask prompt tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	allowed, reason, err := missionAskPromptAllowedTx(tx, prompt, now)
	if err != nil || !allowed {
		return MissionAskPrompt{}, allowed, reason, err
	}
	if err := insertMissionAskPromptTx(tx, prompt); err != nil {
		return MissionAskPrompt{}, false, "", err
	}
	if err := tx.Commit(); err != nil {
		return MissionAskPrompt{}, false, "", fmt.Errorf("commit mission ask prompt: %w", err)
	}
	stored, ok, err := s.MissionAskPrompt(prompt.ID)
	if err != nil {
		return MissionAskPrompt{}, false, "", err
	}
	if !ok {
		return MissionAskPrompt{}, false, "", fmt.Errorf("mission ask prompt %q not found after insert", prompt.ID)
	}
	return stored, true, "", nil
}

func missionAskPromptAllowedTx(tx *sql.Tx, prompt MissionAskPrompt, now time.Time) (bool, string, error) {
	owner := strings.TrimSpace(prompt.Owner)
	fingerprint := strings.TrimSpace(prompt.SourceFingerprint)
	if owner == "" || fingerprint == "" {
		return false, "missing_scope", nil
	}
	var ignored int
	if err := tx.QueryRow(`
		SELECT COUNT(*)
		FROM mission_ask_prompts
		WHERE owner = ?
			AND source_fingerprint = ?
			AND status = ?
			AND updated_at >= ?
	`, owner, fingerprint, string(MissionAskStatusIgnored), now.Add(-MissionAskIgnoredAssociationCooldown).UTC().Format(time.RFC3339Nano)).Scan(&ignored); err != nil {
		return false, "", fmt.Errorf("check mission ask ignored cooldown: %w", err)
	}
	if ignored > 0 {
		return false, "ignored_association_cooldown", nil
	}
	var same int
	if err := tx.QueryRow(`
		SELECT COUNT(*)
		FROM mission_ask_prompts
		WHERE owner = ?
			AND source_fingerprint = ?
			AND created_at >= ?
	`, owner, fingerprint, now.Add(-MissionAskSameAssociationCooldown).UTC().Format(time.RFC3339Nano)).Scan(&same); err != nil {
		return false, "", fmt.Errorf("check mission ask same-association cooldown: %w", err)
	}
	if same > 0 {
		return false, "same_association_cooldown", nil
	}
	confidence := NormalizeMissionAskConfidence(prompt.Confidence)
	window := MissionAskLowConfidenceCooldown
	if confidence == MissionAskConfidenceHigh {
		window = MissionAskHighConfidenceCooldown
	}
	var recent int
	if err := tx.QueryRow(`
		SELECT COUNT(*)
		FROM mission_ask_prompts
		WHERE owner = ?
			AND confidence = ?
			AND created_at >= ?
	`, owner, string(confidence), now.Add(-window).UTC().Format(time.RFC3339Nano)).Scan(&recent); err != nil {
		return false, "", fmt.Errorf("check mission ask confidence cooldown: %w", err)
	}
	if recent > 0 {
		return false, string(confidence) + "_confidence_cooldown", nil
	}
	return true, "", nil
}

func insertMissionAskPromptTx(tx *sql.Tx, prompt MissionAskPrompt) error {
	prompt = NormalizeMissionAskPrompt(prompt)
	_, err := tx.Exec(`
		INSERT INTO mission_ask_prompts(
			id, owner, chat_id, sender_id, session_id, scope_kind, scope_id, scope_durable_agent_id,
			source_message_id, source_turn_run_id, mission_id, confidence, status, question_text,
			source_fingerprint, evidence_json, result_summary, created_at, asked_at, resolved_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, prompt.ID, prompt.Owner, prompt.ChatID, prompt.SenderID, prompt.SessionID, string(prompt.Scope.Kind), prompt.Scope.ID, prompt.Scope.DurableAgentID, prompt.SourceMessageID, prompt.SourceTurnRunID, prompt.MissionID, string(prompt.Confidence), string(prompt.Status), prompt.QuestionText, prompt.SourceFingerprint, prompt.EvidenceJSON, prompt.ResultSummary, prompt.CreatedAt.UTC().Format(time.RFC3339Nano), nullableTimeRFC3339(prompt.AskedAt), nullableTimeRFC3339(prompt.ResolvedAt), prompt.UpdatedAt.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("insert mission ask prompt: %w", err)
	}
	return nil
}

func (s *SQLiteStore) MissionAskPrompt(id string) (MissionAskPrompt, bool, error) {
	if s == nil {
		return MissionAskPrompt{}, false, fmt.Errorf("store is nil")
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return MissionAskPrompt{}, false, nil
	}
	row := s.db.QueryRow(missionAskPromptSelectSQL()+` WHERE id = ?`, id)
	prompt, err := scanMissionAskPrompt(row)
	if errors.Is(err, sql.ErrNoRows) {
		return MissionAskPrompt{}, false, nil
	}
	if err != nil {
		return MissionAskPrompt{}, false, err
	}
	return prompt, true, nil
}

func (s *SQLiteStore) MissionAskPromptForOwner(id string, owner string) (MissionAskPrompt, bool, error) {
	if s == nil {
		return MissionAskPrompt{}, false, fmt.Errorf("store is nil")
	}
	id = strings.TrimSpace(id)
	owner = strings.TrimSpace(owner)
	if id == "" || owner == "" {
		return MissionAskPrompt{}, false, nil
	}
	row := s.db.QueryRow(missionAskPromptSelectSQL()+` WHERE id = ? AND owner = ?`, id, owner)
	prompt, err := scanMissionAskPrompt(row)
	if errors.Is(err, sql.ErrNoRows) {
		return MissionAskPrompt{}, false, nil
	}
	if err != nil {
		return MissionAskPrompt{}, false, err
	}
	return prompt, true, nil
}

func (s *SQLiteStore) MissionAskPrompts(filter MissionAskPromptFilter) ([]MissionAskPrompt, error) {
	if s == nil {
		return nil, fmt.Errorf("store is nil")
	}
	if filter.Limit <= 0 || filter.Limit > 100 {
		filter.Limit = 20
	}
	filter.Owner = strings.TrimSpace(filter.Owner)
	filter.SessionID = strings.TrimSpace(filter.SessionID)
	if strings.TrimSpace(string(filter.Status)) != "" {
		filter.Status = NormalizeMissionAskStatus(filter.Status)
	}
	query := missionAskPromptSelectSQL()
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
		return nil, fmt.Errorf("query mission ask prompts: %w", err)
	}
	defer rows.Close()
	out := make([]MissionAskPrompt, 0, filter.Limit)
	for rows.Next() {
		prompt, err := scanMissionAskPrompt(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, prompt)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate mission ask prompts: %w", err)
	}
	return out, nil
}

func (s *SQLiteStore) PendingMissionAskPromptForSession(owner string, key SessionKey) (MissionAskPrompt, bool, error) {
	if s == nil {
		return MissionAskPrompt{}, false, fmt.Errorf("store is nil")
	}
	owner = strings.TrimSpace(owner)
	if owner == "" {
		return MissionAskPrompt{}, false, nil
	}
	sessionID := SessionIDForKey(key)
	prompts, err := s.MissionAskPrompts(MissionAskPromptFilter{Owner: owner, SessionID: sessionID, Status: MissionAskStatusAsked, Limit: 1})
	if err != nil {
		return MissionAskPrompt{}, false, err
	}
	if len(prompts) == 0 {
		return MissionAskPrompt{}, false, nil
	}
	return prompts[0], true, nil
}

func (s *SQLiteStore) UpdateMissionAskPromptStatus(id string, owner string, status MissionAskStatus, summary string, at time.Time) (MissionAskPrompt, error) {
	if s == nil {
		return MissionAskPrompt{}, fmt.Errorf("store is nil")
	}
	id = strings.TrimSpace(id)
	owner = strings.TrimSpace(owner)
	if id == "" || owner == "" {
		return MissionAskPrompt{}, fmt.Errorf("mission ask prompt id and owner are required")
	}
	status = NormalizeMissionAskStatus(status)
	if at.IsZero() {
		at = time.Now().UTC()
	}
	var askedAt any
	var resolvedAt any
	if status == MissionAskStatusAsked {
		askedAt = at.UTC().Format(time.RFC3339Nano)
	}
	if MissionAskStatusTerminal(status) {
		resolvedAt = at.UTC().Format(time.RFC3339Nano)
	}
	res, err := s.db.Exec(`
		UPDATE mission_ask_prompts
		SET status = ?,
			result_summary = CASE WHEN ? != '' THEN ? ELSE result_summary END,
			asked_at = COALESCE(?, asked_at),
			resolved_at = COALESCE(?, resolved_at),
			updated_at = ?
		WHERE id = ? AND owner = ?
	`, string(status), strings.TrimSpace(summary), strings.TrimSpace(summary), askedAt, resolvedAt, at.UTC().Format(time.RFC3339Nano), id, owner)
	if err != nil {
		return MissionAskPrompt{}, fmt.Errorf("update mission ask prompt status: %w", err)
	}
	if changed, _ := res.RowsAffected(); changed == 0 {
		return MissionAskPrompt{}, fmt.Errorf("mission ask prompt %q not found", id)
	}
	prompt, ok, err := s.MissionAskPromptForOwner(id, owner)
	if err != nil {
		return MissionAskPrompt{}, err
	}
	if !ok {
		return MissionAskPrompt{}, fmt.Errorf("mission ask prompt %q not found after update", id)
	}
	return prompt, nil
}

func missionAskPromptSelectSQL() string {
	return `SELECT id, owner, chat_id, sender_id, session_id, scope_kind, scope_id, scope_durable_agent_id,
		source_message_id, source_turn_run_id, mission_id, confidence, status, question_text,
		source_fingerprint, evidence_json, result_summary, created_at, asked_at, resolved_at, updated_at
		FROM mission_ask_prompts`
}

func scanMissionAskPrompt(scanner interface {
	Scan(dest ...any) error
}) (MissionAskPrompt, error) {
	var prompt MissionAskPrompt
	var scopeKind, scopeID, scopeDurableAgentID string
	var confidence, status string
	var createdRaw, updatedRaw string
	var askedRaw, resolvedRaw sql.NullString
	if err := scanner.Scan(
		&prompt.ID,
		&prompt.Owner,
		&prompt.ChatID,
		&prompt.SenderID,
		&prompt.SessionID,
		&scopeKind,
		&scopeID,
		&scopeDurableAgentID,
		&prompt.SourceMessageID,
		&prompt.SourceTurnRunID,
		&prompt.MissionID,
		&confidence,
		&status,
		&prompt.QuestionText,
		&prompt.SourceFingerprint,
		&prompt.EvidenceJSON,
		&prompt.ResultSummary,
		&createdRaw,
		&askedRaw,
		&resolvedRaw,
		&updatedRaw,
	); err != nil {
		return MissionAskPrompt{}, err
	}
	createdAt, err := parseSQLiteTime(createdRaw)
	if err != nil {
		return MissionAskPrompt{}, fmt.Errorf("parse mission ask created_at: %w", err)
	}
	updatedAt, err := parseSQLiteTime(updatedRaw)
	if err != nil {
		return MissionAskPrompt{}, fmt.Errorf("parse mission ask updated_at: %w", err)
	}
	prompt.Scope = ScopeRef{Kind: ScopeKind(scopeKind), ID: scopeID, DurableAgentID: scopeDurableAgentID}
	prompt.Confidence = MissionAskConfidence(confidence)
	prompt.Status = MissionAskStatus(status)
	prompt.CreatedAt = createdAt
	prompt.UpdatedAt = updatedAt
	if askedRaw.Valid && strings.TrimSpace(askedRaw.String) != "" {
		if prompt.AskedAt, err = parseSQLiteTime(askedRaw.String); err != nil {
			return MissionAskPrompt{}, fmt.Errorf("parse mission ask asked_at: %w", err)
		}
	}
	if resolvedRaw.Valid && strings.TrimSpace(resolvedRaw.String) != "" {
		if prompt.ResolvedAt, err = parseSQLiteTime(resolvedRaw.String); err != nil {
			return MissionAskPrompt{}, fmt.Errorf("parse mission ask resolved_at: %w", err)
		}
	}
	return NormalizeMissionAskPrompt(prompt), nil
}
