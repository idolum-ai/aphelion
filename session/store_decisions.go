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

func (s *SQLiteStore) UpsertPendingArtifactRetention(record PendingArtifactRetentionRecord) error {
	record.OwnerKey = strings.TrimSpace(record.OwnerKey)
	if record.OwnerKey == "" {
		return fmt.Errorf("pending artifact retention owner_key is required")
	}
	record.InboundMessageJSON = strings.TrimSpace(record.InboundMessageJSON)
	if record.InboundMessageJSON == "" {
		record.InboundMessageJSON = "{}"
	}
	now := time.Now().UTC()
	createdAt := record.CreatedAt
	if createdAt.IsZero() {
		createdAt = now
	}
	updatedAt := record.UpdatedAt
	if updatedAt.IsZero() {
		updatedAt = now
	}
	_, err := s.db.Exec(`
		INSERT INTO pending_artifact_retention(
			owner_key, chat_id, sender_id, inbound_message_json, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(owner_key) DO UPDATE SET
			chat_id = excluded.chat_id,
			sender_id = excluded.sender_id,
			inbound_message_json = excluded.inbound_message_json,
			updated_at = excluded.updated_at
	`, record.OwnerKey, record.ChatID, record.SenderID, record.InboundMessageJSON, createdAt.UTC().Format(time.RFC3339Nano), updatedAt.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("upsert pending artifact retention: %w", err)
	}
	return nil
}

func (s *SQLiteStore) PendingArtifactRetention(ownerKey string) (*PendingArtifactRetentionRecord, error) {
	ownerKey = strings.TrimSpace(ownerKey)
	if ownerKey == "" {
		return nil, sql.ErrNoRows
	}
	var record PendingArtifactRetentionRecord
	var createdAtRaw, updatedAtRaw string
	err := s.db.QueryRow(`
		SELECT owner_key, chat_id, sender_id, inbound_message_json, created_at, updated_at
		FROM pending_artifact_retention
		WHERE owner_key = ?
	`, ownerKey).Scan(&record.OwnerKey, &record.ChatID, &record.SenderID, &record.InboundMessageJSON, &createdAtRaw, &updatedAtRaw)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		return nil, fmt.Errorf("load pending artifact retention: %w", err)
	}
	createdAt, err := parseSQLiteTime(createdAtRaw)
	if err != nil {
		return nil, fmt.Errorf("parse pending artifact retention created_at: %w", err)
	}
	updatedAt, err := parseSQLiteTime(updatedAtRaw)
	if err != nil {
		return nil, fmt.Errorf("parse pending artifact retention updated_at: %w", err)
	}
	record.CreatedAt = createdAt
	record.UpdatedAt = updatedAt
	return &record, nil
}

func (s *SQLiteStore) DeletePendingArtifactRetention(ownerKey string) error {
	ownerKey = strings.TrimSpace(ownerKey)
	if ownerKey == "" {
		return nil
	}
	if _, err := s.db.Exec(`DELETE FROM pending_artifact_retention WHERE owner_key = ?`, ownerKey); err != nil {
		return fmt.Errorf("delete pending artifact retention: %w", err)
	}
	return nil
}

func (s *SQLiteStore) UpsertPendingBusyDecision(record PendingBusyDecisionRecord) error {
	record.OwnerKey = strings.TrimSpace(record.OwnerKey)
	if record.OwnerKey == "" {
		return fmt.Errorf("pending busy decision owner_key is required")
	}
	record.InboundMessageJSON = strings.TrimSpace(record.InboundMessageJSON)
	if record.InboundMessageJSON == "" {
		record.InboundMessageJSON = "{}"
	}
	now := time.Now().UTC()
	createdAt := record.CreatedAt
	if createdAt.IsZero() {
		createdAt = now
	}
	updatedAt := record.UpdatedAt
	if updatedAt.IsZero() {
		updatedAt = now
	}
	_, err := s.db.Exec(`
		INSERT INTO pending_busy_decisions(
			owner_key, chat_id, sender_id, inbound_message_json, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(owner_key) DO UPDATE SET
			chat_id = excluded.chat_id,
			sender_id = excluded.sender_id,
			inbound_message_json = excluded.inbound_message_json,
			updated_at = excluded.updated_at
	`, record.OwnerKey, record.ChatID, record.SenderID, record.InboundMessageJSON, createdAt.UTC().Format(time.RFC3339Nano), updatedAt.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("upsert pending busy decision: %w", err)
	}
	return nil
}

func (s *SQLiteStore) PendingBusyDecision(ownerKey string) (*PendingBusyDecisionRecord, error) {
	ownerKey = strings.TrimSpace(ownerKey)
	if ownerKey == "" {
		return nil, sql.ErrNoRows
	}
	var record PendingBusyDecisionRecord
	var createdAtRaw, updatedAtRaw string
	err := s.db.QueryRow(`
		SELECT owner_key, chat_id, sender_id, inbound_message_json, created_at, updated_at
		FROM pending_busy_decisions
		WHERE owner_key = ?
	`, ownerKey).Scan(&record.OwnerKey, &record.ChatID, &record.SenderID, &record.InboundMessageJSON, &createdAtRaw, &updatedAtRaw)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		return nil, fmt.Errorf("load pending busy decision: %w", err)
	}
	createdAt, err := parseSQLiteTime(createdAtRaw)
	if err != nil {
		return nil, fmt.Errorf("parse pending busy decision created_at: %w", err)
	}
	updatedAt, err := parseSQLiteTime(updatedAtRaw)
	if err != nil {
		return nil, fmt.Errorf("parse pending busy decision updated_at: %w", err)
	}
	record.CreatedAt = createdAt
	record.UpdatedAt = updatedAt
	return &record, nil
}

func (s *SQLiteStore) DeletePendingBusyDecision(ownerKey string) error {
	ownerKey = strings.TrimSpace(ownerKey)
	if ownerKey == "" {
		return nil
	}
	if _, err := s.db.Exec(`DELETE FROM pending_busy_decisions WHERE owner_key = ?`, ownerKey); err != nil {
		return fmt.Errorf("delete pending busy decision: %w", err)
	}
	return nil
}

func (s *SQLiteStore) RecordOutbound(key SessionKey, turnIndex int, telegramMsgID int64, msgType string) error {
	if telegramMsgID == 0 {
		return fmt.Errorf("record outbound: telegram_msg_id is required")
	}
	if strings.TrimSpace(msgType) == "" {
		msgType = "text"
	}
	sessionID := SessionIDForKey(key)

	_, err := s.db.Exec(`
		INSERT INTO outbound_messages(session_id, chat_id, user_id, turn_index, telegram_msg_id, msg_type)
		VALUES (?, ?, ?, ?, ?, ?)
	`, sessionID, key.ChatID, key.UserID, turnIndex, telegramMsgID, msgType)
	if err != nil {
		return fmt.Errorf("record outbound: %w", err)
	}
	return nil
}

func (s *SQLiteStore) InsertReviewEvent(event ReviewEvent) (int64, error) {
	if strings.TrimSpace(event.SourceRole) == "" {
		return 0, fmt.Errorf("enqueue review event: source_role is required")
	}
	event.SourceScope = NormalizeScopeRef(event.SourceScope)
	event.TargetScope = NormalizeScopeRef(event.TargetScope)
	if event.SourceChatID == 0 && event.SourceScope.IsZero() {
		return 0, fmt.Errorf("enqueue review event: source provenance is required")
	}
	if event.TargetAdminChatID == 0 {
		return 0, fmt.Errorf("enqueue review event: target_chat_id is required")
	}
	if strings.TrimSpace(event.Summary) == "" {
		return 0, fmt.Errorf("enqueue review event: summary is required")
	}

	status := strings.TrimSpace(event.Status)
	if status == "" {
		status = "pending"
	}
	if strings.TrimSpace(event.SourceSessionID) == "" {
		event.SourceSessionID = SessionIDFromParts(event.SourceChatID, event.SourceUserID, event.SourceScope)
	}
	if strings.TrimSpace(event.TargetSessionID) == "" {
		event.TargetSessionID = SessionIDFromParts(event.TargetAdminChatID, 0, event.TargetScope)
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	res, err := s.db.Exec(`
		INSERT INTO review_events(
			source_session_id, source_chat_id, source_user_id, source_role, source_scope_kind, source_scope_id, source_durable_agent_id,
			target_session_id, target_chat_id, target_scope_kind, target_scope_id, target_durable_agent_id,
			turn_from, turn_to, summary, metadata_json, status, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		nullableString(event.SourceSessionID), event.SourceChatID, event.SourceUserID, event.SourceRole,
		string(event.SourceScope.Kind), event.SourceScope.ID, event.SourceScope.DurableAgentID,
		nullableString(event.TargetSessionID), event.TargetAdminChatID,
		string(event.TargetScope.Kind), event.TargetScope.ID, event.TargetScope.DurableAgentID,
		event.TurnFrom, event.TurnTo, event.Summary, nullableString(event.MetadataJSON), status, now,
	)
	if err != nil {
		return 0, fmt.Errorf("enqueue review event: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("review event last insert id: %w", err)
	}
	return id, nil
}

func (s *SQLiteStore) EnqueueReviewEvent(event ReviewEvent) error {
	_, err := s.InsertReviewEvent(event)
	return err
}

func (s *SQLiteStore) PendingReviewEvents(targetChatID int64, limit int) ([]ReviewEvent, error) {
	if targetChatID == 0 {
		return nil, fmt.Errorf("pending review events: target_chat_id is required")
	}
	if limit <= 0 {
		limit = 20
	}

	rows, err := s.db.Query(`
		SELECT
			id, source_session_id, source_chat_id, source_user_id, source_role, source_scope_kind, source_scope_id, source_durable_agent_id,
			target_session_id, target_chat_id, target_scope_kind, target_scope_id, target_durable_agent_id,
			turn_from, turn_to, summary, metadata_json, status, created_at, delivered_at
		FROM review_events
		WHERE target_chat_id = ? AND status = 'pending'
		ORDER BY created_at ASC, id ASC
		LIMIT ?
	`, targetChatID, limit)
	if err != nil {
		return nil, fmt.Errorf("query pending review events: %w", err)
	}
	defer rows.Close()

	events := make([]ReviewEvent, 0, limit)
	for rows.Next() {
		event, err := scanReviewEvent(rows)
		if err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate pending review events: %w", err)
	}
	return events, nil
}

func (s *SQLiteStore) PendingReviewEventsAll(limit int) ([]ReviewEvent, error) {
	if limit <= 0 {
		limit = 100
	}

	rows, err := s.db.Query(`
		SELECT
			id, source_session_id, source_chat_id, source_user_id, source_role, source_scope_kind, source_scope_id, source_durable_agent_id,
			target_session_id, target_chat_id, target_scope_kind, target_scope_id, target_durable_agent_id,
			turn_from, turn_to, summary, metadata_json, status, created_at, delivered_at
		FROM review_events
		WHERE status = 'pending'
		ORDER BY created_at ASC, id ASC
		LIMIT ?
	`, limit)
	if err != nil {
		return nil, fmt.Errorf("query pending review events: %w", err)
	}
	defer rows.Close()

	events := make([]ReviewEvent, 0, limit)
	for rows.Next() {
		event, err := scanReviewEvent(rows)
		if err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate pending review events: %w", err)
	}
	return events, nil
}

func (s *SQLiteStore) ReviewEventsWithRedactedSummary(limit int) ([]ReviewEvent, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.Query(`
		SELECT
			id, source_session_id, source_chat_id, source_user_id, source_role, source_scope_kind, source_scope_id, source_durable_agent_id,
			target_session_id, target_chat_id, target_scope_kind, target_scope_id, target_durable_agent_id,
			turn_from, turn_to, summary, metadata_json, status, created_at, delivered_at
		FROM review_events
		WHERE summary LIKE ? OR metadata_json LIKE ?
		ORDER BY created_at ASC, id ASC
		LIMIT ?
	`, "%[REDACTED: summary]%", "%[REDACTED: summary]%", limit)
	if err != nil {
		return nil, fmt.Errorf("query redacted-summary review events: %w", err)
	}
	defer rows.Close()

	events := make([]ReviewEvent, 0, limit)
	for rows.Next() {
		event, err := scanReviewEvent(rows)
		if err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate redacted-summary review events: %w", err)
	}
	return events, nil
}

func (s *SQLiteStore) UpdateReviewEventProjection(id int64, summary string, metadataJSON string) error {
	if id <= 0 {
		return fmt.Errorf("update review event projection: id is required")
	}
	if strings.TrimSpace(summary) == "" {
		return fmt.Errorf("update review event projection: summary is required")
	}
	res, err := s.db.Exec(`
		UPDATE review_events
		SET summary = ?, metadata_json = ?
		WHERE id = ?
	`, summary, nullableString(metadataJSON), id)
	if err != nil {
		return fmt.Errorf("update review event projection: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("review event projection rows affected: %w", err)
	}
	if affected == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *SQLiteStore) MarkReviewDelivered(ids []int64) error {
	if len(ids) == 0 {
		return nil
	}

	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin mark review delivered tx: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	stmt, err := tx.Prepare(`
		UPDATE review_events
		SET status = 'delivered', delivered_at = ?
		WHERE id = ? AND status = 'pending'
	`)
	if err != nil {
		return fmt.Errorf("prepare mark review delivered statement: %w", err)
	}
	defer stmt.Close()

	now := time.Now().UTC().Format(time.RFC3339Nano)
	for _, id := range ids {
		if id <= 0 {
			continue
		}
		if _, err := stmt.Exec(now, id); err != nil {
			return fmt.Errorf("mark review delivered id=%d: %w", id, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit mark review delivered tx: %w", err)
	}
	return nil
}

func encodeRecordReferences(refs []RecordReference) string {
	normalized := NormalizeRecordReferences(refs)
	if len(normalized) == 0 {
		return "[]"
	}
	buf, err := json.Marshal(normalized)
	if err != nil {
		return "[]"
	}
	return string(buf)
}

func decodeRecordReferences(raw string) []RecordReference {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	var refs []RecordReference
	if err := json.Unmarshal([]byte(raw), &refs); err != nil {
		return nil
	}
	return NormalizeRecordReferences(refs)
}

func (s *SQLiteStore) UpsertPendingDecision(record PendingDecisionRecord) error {
	record.ID = strings.TrimSpace(record.ID)
	if record.ID == "" {
		return fmt.Errorf("pending decision id is required")
	}
	if record.Sequence > 9223372036854775807 {
		return fmt.Errorf("pending decision sequence is too large")
	}

	now := time.Now().UTC()
	createdAt := record.CreatedAt
	if createdAt.IsZero() {
		createdAt = now
	}
	updatedAt := record.UpdatedAt
	if updatedAt.IsZero() {
		updatedAt = now
	}
	record.OwnerKey = strings.TrimSpace(record.OwnerKey)
	record.Kind = strings.TrimSpace(record.Kind)
	record.Prompt = strings.TrimSpace(record.Prompt)
	record.Details = strings.TrimSpace(record.Details)
	record.Rationale = strings.TrimSpace(record.Rationale)
	record.ArtifactRefs = NormalizeRecordReferences(record.ArtifactRefs)
	record.DefaultChoice = strings.TrimSpace(record.DefaultChoice)
	choicesJSON := strings.TrimSpace(record.ChoicesJSON)
	if choicesJSON == "" {
		choicesJSON = "[]"
	}

	_, err := s.db.Exec(`
		INSERT INTO pending_decisions(
			decision_id, decision_seq, owner_key, kind, chat_id, sender_id, message_id,
			prompt, details, rationale, artifact_refs_json, choices_json, default_choice, timeout_ns, delivery_message_id,
			created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(decision_id) DO UPDATE SET
			decision_seq = excluded.decision_seq,
			owner_key = excluded.owner_key,
			kind = excluded.kind,
			chat_id = excluded.chat_id,
			sender_id = excluded.sender_id,
			message_id = excluded.message_id,
			prompt = excluded.prompt,
			details = excluded.details,
			rationale = excluded.rationale,
			artifact_refs_json = excluded.artifact_refs_json,
			choices_json = excluded.choices_json,
			default_choice = excluded.default_choice,
			timeout_ns = excluded.timeout_ns,
			delivery_message_id = excluded.delivery_message_id,
			updated_at = excluded.updated_at
	`,
		record.ID,
		int64(record.Sequence),
		record.OwnerKey,
		record.Kind,
		record.ChatID,
		record.SenderID,
		record.MessageID,
		record.Prompt,
		record.Details,
		record.Rationale,
		encodeRecordReferences(record.ArtifactRefs),
		choicesJSON,
		record.DefaultChoice,
		record.TimeoutNanos,
		record.DeliveryMessageID,
		createdAt.UTC().Format(time.RFC3339Nano),
		updatedAt.UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		return fmt.Errorf("upsert pending decision: %w", err)
	}
	return nil
}

func (s *SQLiteStore) DeletePendingDecision(id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil
	}
	if _, err := s.db.Exec(`DELETE FROM pending_decisions WHERE decision_id = ?`, id); err != nil {
		return fmt.Errorf("delete pending decision: %w", err)
	}
	return nil
}

func (s *SQLiteStore) DeletePendingDecisionsByOwner(ownerKey string) (int, error) {
	ownerKey = strings.TrimSpace(ownerKey)
	if ownerKey == "" {
		return 0, nil
	}
	res, err := s.db.Exec(`DELETE FROM pending_decisions WHERE owner_key = ?`, ownerKey)
	if err != nil {
		return 0, fmt.Errorf("delete pending decisions by owner: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("rows affected deleting pending decisions by owner: %w", err)
	}
	return int(affected), nil
}

func (s *SQLiteStore) DeleteAllPendingDecisions() (int, error) {
	res, err := s.db.Exec(`DELETE FROM pending_decisions`)
	if err != nil {
		return 0, fmt.Errorf("delete all pending decisions: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("rows affected deleting all pending decisions: %w", err)
	}
	return int(affected), nil
}

func (s *SQLiteStore) PendingDecisions() ([]PendingDecisionRecord, error) {
	rows, err := s.db.Query(`
		SELECT
			decision_id, decision_seq, owner_key, kind, chat_id, sender_id, message_id,
			prompt, details, rationale, artifact_refs_json, choices_json, default_choice, timeout_ns, delivery_message_id,
			created_at, updated_at
		FROM pending_decisions
		ORDER BY decision_seq ASC, decision_id ASC
	`)
	if err != nil {
		return nil, fmt.Errorf("query pending decisions: %w", err)
	}
	defer rows.Close()

	records := make([]PendingDecisionRecord, 0)
	for rows.Next() {
		var (
			record          PendingDecisionRecord
			sequenceRaw     int64
			artifactRefsRaw string
			createdAtRaw    string
			updatedAtRaw    string
		)
		if err := rows.Scan(
			&record.ID, &sequenceRaw, &record.OwnerKey, &record.Kind, &record.ChatID, &record.SenderID, &record.MessageID,
			&record.Prompt, &record.Details, &record.Rationale, &artifactRefsRaw, &record.ChoicesJSON, &record.DefaultChoice, &record.TimeoutNanos, &record.DeliveryMessageID,
			&createdAtRaw, &updatedAtRaw,
		); err != nil {
			return nil, fmt.Errorf("scan pending decision: %w", err)
		}
		if sequenceRaw > 0 {
			record.Sequence = uint64(sequenceRaw)
		}
		record.Prompt = strings.TrimSpace(record.Prompt)
		record.Details = strings.TrimSpace(record.Details)
		record.OwnerKey = strings.TrimSpace(record.OwnerKey)
		record.Kind = strings.TrimSpace(record.Kind)
		record.Rationale = strings.TrimSpace(record.Rationale)
		record.ArtifactRefs = decodeRecordReferences(artifactRefsRaw)
		record.DefaultChoice = strings.TrimSpace(record.DefaultChoice)
		record.ChoicesJSON = strings.TrimSpace(record.ChoicesJSON)
		if record.ChoicesJSON == "" {
			record.ChoicesJSON = "[]"
		}
		createdAt, err := parseSQLiteTime(createdAtRaw)
		if err != nil {
			return nil, fmt.Errorf("parse pending decision created_at: %w", err)
		}
		updatedAt, err := parseSQLiteTime(updatedAtRaw)
		if err != nil {
			return nil, fmt.Errorf("parse pending decision updated_at: %w", err)
		}
		record.CreatedAt = createdAt
		record.UpdatedAt = updatedAt
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate pending decisions: %w", err)
	}
	return records, nil
}

func (s *SQLiteStore) ReviewEventByID(id int64) (*ReviewEvent, error) {
	if id <= 0 {
		return nil, fmt.Errorf("review event id is required")
	}
	rows, err := s.db.Query(`
		SELECT
			id, source_session_id, source_chat_id, source_user_id, source_role, source_scope_kind, source_scope_id, source_durable_agent_id,
			target_session_id, target_chat_id, target_scope_kind, target_scope_id, target_durable_agent_id,
			turn_from, turn_to, summary, metadata_json, status, created_at, delivered_at
		FROM review_events
		WHERE id = ?
	`, id)
	if err != nil {
		return nil, fmt.Errorf("query review event: %w", err)
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, sql.ErrNoRows
	}
	event, err := scanReviewEvent(rows)
	if err != nil {
		return nil, err
	}
	return &event, nil
}

func scanReviewEvent(scanner interface{ Scan(dest ...any) error }) (ReviewEvent, error) {
	var (
		event           ReviewEvent
		createdAtRaw    string
		deliveredAtRaw  sql.NullString
		turnFromRaw     sql.NullInt64
		turnToRaw       sql.NullInt64
		targetChatIDRaw int64
		sourceSessionID sql.NullString
		sourceScopeKind sql.NullString
		sourceScopeID   sql.NullString
		sourceAgentID   sql.NullString
		targetSessionID sql.NullString
		targetScopeKind sql.NullString
		targetScopeID   sql.NullString
		targetAgentID   sql.NullString
		metadataJSON    sql.NullString
	)

	if err := scanner.Scan(
		&event.ID, &sourceSessionID, &event.SourceChatID, &event.SourceUserID, &event.SourceRole, &sourceScopeKind, &sourceScopeID, &sourceAgentID,
		&targetSessionID, &targetChatIDRaw, &targetScopeKind, &targetScopeID, &targetAgentID,
		&turnFromRaw, &turnToRaw, &event.Summary, &metadataJSON, &event.Status, &createdAtRaw, &deliveredAtRaw,
	); err != nil {
		return ReviewEvent{}, fmt.Errorf("scan review event: %w", err)
	}

	event.SourceSessionID = nullToString(sourceSessionID)
	event.TargetAdminChatID = targetChatIDRaw
	event.TargetSessionID = nullToString(targetSessionID)
	event.SourceScope = NormalizeScopeRef(ScopeRef{
		Kind:           ScopeKind(nullToString(sourceScopeKind)),
		ID:             nullToString(sourceScopeID),
		DurableAgentID: nullToString(sourceAgentID),
	})
	event.TargetScope = NormalizeScopeRef(ScopeRef{
		Kind:           ScopeKind(nullToString(targetScopeKind)),
		ID:             nullToString(targetScopeID),
		DurableAgentID: nullToString(targetAgentID),
	})
	event.MetadataJSON = nullToString(metadataJSON)
	if turnFromRaw.Valid {
		event.TurnFrom = int(turnFromRaw.Int64)
	}
	if turnToRaw.Valid {
		event.TurnTo = int(turnToRaw.Int64)
	}
	createdAt, err := parseSQLiteTime(createdAtRaw)
	if err != nil {
		return ReviewEvent{}, fmt.Errorf("parse review event created_at: %w", err)
	}
	event.CreatedAt = createdAt
	if deliveredAtRaw.Valid && deliveredAtRaw.String != "" {
		deliveredAt, err := parseSQLiteTime(deliveredAtRaw.String)
		if err != nil {
			return ReviewEvent{}, fmt.Errorf("parse review event delivered_at: %w", err)
		}
		event.DeliveredAt = deliveredAt
	}
	return event, nil
}
