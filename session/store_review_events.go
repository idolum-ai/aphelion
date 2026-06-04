//go:build linux

package session

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

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
			turn_from, turn_to, summary, metadata_json, status, created_at, delivered_at, delivery_message_id
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
			turn_from, turn_to, summary, metadata_json, status, created_at, delivered_at, delivery_message_id
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
			turn_from, turn_to, summary, metadata_json, status, created_at, delivered_at, delivery_message_id
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

func (s *SQLiteStore) MarkReviewDeliveredWithMessage(id int64, messageID int64) error {
	if id <= 0 {
		return nil
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := s.db.Exec(`
		UPDATE review_events
		SET status = 'delivered', delivered_at = ?, delivery_message_id = ?
		WHERE id = ? AND status = 'pending'
	`, now, messageID, id); err != nil {
		return fmt.Errorf("mark review delivered with message id=%d: %w", id, err)
	}
	return nil
}

func (s *SQLiteStore) DismissPendingCapabilityReviewEvents(targetChatID int64, requestID string, exceptEventID int64) ([]ReviewEvent, error) {
	requestID = strings.TrimSpace(requestID)
	if targetChatID == 0 || requestID == "" {
		return nil, nil
	}
	pattern := "%\"request_id\":\"" + requestID + "\"%"
	rows, err := s.db.Query(`
		SELECT
			id, source_session_id, source_chat_id, source_user_id, source_role, source_scope_kind, source_scope_id, source_durable_agent_id,
			target_session_id, target_chat_id, target_scope_kind, target_scope_id, target_durable_agent_id,
			turn_from, turn_to, summary, metadata_json, status, created_at, delivered_at, delivery_message_id
		FROM review_events
		WHERE target_chat_id = ? AND source_role = 'capability_request' AND status IN ('pending', 'delivered') AND id != ? AND metadata_json LIKE ?
		ORDER BY created_at ASC, id ASC
	`, targetChatID, exceptEventID, pattern)
	if err != nil {
		return nil, fmt.Errorf("query stale capability review events: %w", err)
	}
	defer rows.Close()
	events := []ReviewEvent{}
	ids := []int64{}
	for rows.Next() {
		event, err := scanReviewEvent(rows)
		if err != nil {
			return nil, err
		}
		if reviewEventMetadataRequestID(event.MetadataJSON) != requestID {
			continue
		}
		events = append(events, event)
		ids = append(ids, event.ID)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate stale capability review events: %w", err)
	}
	if len(ids) == 0 {
		return events, nil
	}
	placeholders := strings.TrimRight(strings.Repeat("?,", len(ids)), ",")
	args := make([]any, 0, len(ids))
	for _, id := range ids {
		args = append(args, id)
	}
	if _, err := s.db.Exec("UPDATE review_events SET status = 'dismissed' WHERE id IN ("+placeholders+")", args...); err != nil {
		return nil, fmt.Errorf("dismiss stale capability review events: %w", err)
	}
	return events, nil
}

func reviewEventMetadataRequestID(raw string) string {
	var meta map[string]any
	if err := json.Unmarshal([]byte(raw), &meta); err != nil {
		return ""
	}
	if v, ok := meta["request_id"].(string); ok {
		return strings.TrimSpace(v)
	}
	return ""
}

func (s *SQLiteStore) ReviewEventByID(id int64) (*ReviewEvent, error) {
	if id <= 0 {
		return nil, fmt.Errorf("review event id is required")
	}
	rows, err := s.db.Query(`
		SELECT
			id, source_session_id, source_chat_id, source_user_id, source_role, source_scope_kind, source_scope_id, source_durable_agent_id,
			target_session_id, target_chat_id, target_scope_kind, target_scope_id, target_durable_agent_id,
			turn_from, turn_to, summary, metadata_json, status, created_at, delivered_at, delivery_message_id
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
		event                ReviewEvent
		createdAtRaw         string
		deliveredAtRaw       sql.NullString
		turnFromRaw          sql.NullInt64
		turnToRaw            sql.NullInt64
		targetChatIDRaw      int64
		sourceSessionID      sql.NullString
		sourceScopeKind      sql.NullString
		sourceScopeID        sql.NullString
		sourceAgentID        sql.NullString
		targetSessionID      sql.NullString
		targetScopeKind      sql.NullString
		targetScopeID        sql.NullString
		targetAgentID        sql.NullString
		metadataJSON         sql.NullString
		deliveryMessageIDRaw sql.NullInt64
	)

	if err := scanner.Scan(
		&event.ID, &sourceSessionID, &event.SourceChatID, &event.SourceUserID, &event.SourceRole, &sourceScopeKind, &sourceScopeID, &sourceAgentID,
		&targetSessionID, &targetChatIDRaw, &targetScopeKind, &targetScopeID, &targetAgentID,
		&turnFromRaw, &turnToRaw, &event.Summary, &metadataJSON, &event.Status, &createdAtRaw, &deliveredAtRaw, &deliveryMessageIDRaw,
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
	if deliveryMessageIDRaw.Valid {
		event.DeliveryMessageID = deliveryMessageIDRaw.Int64
	}
	return event, nil
}
