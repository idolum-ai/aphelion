//go:build linux

package session

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

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
	record.SessionID = strings.TrimSpace(record.SessionID)
	record.ScopeKind = strings.TrimSpace(record.ScopeKind)
	record.ScopeID = strings.TrimSpace(record.ScopeID)
	record.DurableAgentID = strings.TrimSpace(record.DurableAgentID)
	record.Kind = strings.TrimSpace(record.Kind)
	record.Prompt = strings.TrimSpace(record.Prompt)
	record.Details = strings.TrimSpace(record.Details)
	metadataJSON, err := normalizePendingDecisionMetadataJSON(record.MetadataJSON)
	if err != nil {
		return err
	}
	record.Rationale = strings.TrimSpace(record.Rationale)
	record.ArtifactRefs = NormalizeRecordReferences(record.ArtifactRefs)
	record.DefaultChoice = strings.TrimSpace(record.DefaultChoice)
	choicesJSON := strings.TrimSpace(record.ChoicesJSON)
	if choicesJSON == "" {
		choicesJSON = "[]"
	}

	_, err = s.db.Exec(`
		INSERT INTO pending_decisions(
			decision_id, decision_seq, owner_key, session_id, scope_kind, scope_id, durable_agent_id, kind, chat_id, sender_id, message_id,
			prompt, details, metadata_json, rationale, artifact_refs_json, choices_json, default_choice, timeout_ns, delivery_message_id,
			created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(decision_id) DO UPDATE SET
			decision_seq = excluded.decision_seq,
			owner_key = excluded.owner_key,
			session_id = excluded.session_id,
			scope_kind = excluded.scope_kind,
			scope_id = excluded.scope_id,
			durable_agent_id = excluded.durable_agent_id,
			kind = excluded.kind,
			chat_id = excluded.chat_id,
			sender_id = excluded.sender_id,
			message_id = excluded.message_id,
			prompt = excluded.prompt,
			details = excluded.details,
			metadata_json = excluded.metadata_json,
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
		record.SessionID,
		record.ScopeKind,
		record.ScopeID,
		record.DurableAgentID,
		record.Kind,
		record.ChatID,
		record.SenderID,
		record.MessageID,
		record.Prompt,
		record.Details,
		metadataJSON,
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

func (s *SQLiteStore) DeletePendingDecisionsByChatSender(chatID int64, senderID int64) (int, error) {
	if chatID == 0 || senderID == 0 {
		return 0, nil
	}
	res, err := s.db.Exec(`DELETE FROM pending_decisions WHERE chat_id = ? AND sender_id = ?`, chatID, senderID)
	if err != nil {
		return 0, fmt.Errorf("delete pending decisions by chat sender: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("rows affected deleting pending decisions by chat sender: %w", err)
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
			decision_id, decision_seq, owner_key, session_id, scope_kind, scope_id, durable_agent_id, kind, chat_id, sender_id, message_id,
			prompt, details, metadata_json, rationale, artifact_refs_json, choices_json, default_choice, timeout_ns, delivery_message_id,
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
			&record.ID, &sequenceRaw, &record.OwnerKey, &record.SessionID, &record.ScopeKind, &record.ScopeID, &record.DurableAgentID, &record.Kind, &record.ChatID, &record.SenderID, &record.MessageID,
			&record.Prompt, &record.Details, &record.MetadataJSON, &record.Rationale, &artifactRefsRaw, &record.ChoicesJSON, &record.DefaultChoice, &record.TimeoutNanos, &record.DeliveryMessageID,
			&createdAtRaw, &updatedAtRaw,
		); err != nil {
			return nil, fmt.Errorf("scan pending decision: %w", err)
		}
		if sequenceRaw > 0 {
			record.Sequence = uint64(sequenceRaw)
		}
		record.Prompt = strings.TrimSpace(record.Prompt)
		record.Details = strings.TrimSpace(record.Details)
		record.MetadataJSON = strings.TrimSpace(record.MetadataJSON)
		if record.MetadataJSON == "" {
			record.MetadataJSON = "{}"
		}
		record.OwnerKey = strings.TrimSpace(record.OwnerKey)
		record.SessionID = strings.TrimSpace(record.SessionID)
		record.ScopeKind = strings.TrimSpace(record.ScopeKind)
		record.ScopeID = strings.TrimSpace(record.ScopeID)
		record.DurableAgentID = strings.TrimSpace(record.DurableAgentID)
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

func normalizePendingDecisionMetadataJSON(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "{}", nil
	}
	var metadata map[string]string
	if err := json.Unmarshal([]byte(raw), &metadata); err != nil {
		return "", fmt.Errorf("decode pending decision metadata: %w", err)
	}
	normalized := make(map[string]string, len(metadata))
	for key, value := range metadata {
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key == "" || value == "" {
			continue
		}
		normalized[key] = value
	}
	encoded, err := json.Marshal(normalized)
	if err != nil {
		return "", fmt.Errorf("encode pending decision metadata: %w", err)
	}
	return string(encoded), nil
}
