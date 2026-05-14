//go:build linux

package session

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
)

func (s *SQLiteStore) UpsertDurableAgentRemoteEnrollment(enrollment core.DurableAgentRemoteEnrollment) error {
	enrollment = core.NormalizeDurableAgentRemoteEnrollment(enrollment)
	if enrollment.AgentID == "" {
		return fmt.Errorf("upsert durable agent remote enrollment: agent_id is required")
	}
	if enrollment.ParentControlURL == "" {
		return fmt.Errorf("upsert durable agent remote enrollment: parent_control_url is required")
	}
	tagsJSON, err := marshalStringSlice(enrollment.TailnetIdentity.Tags)
	if err != nil {
		return fmt.Errorf("marshal durable agent remote enrollment tailnet tags: %w", err)
	}
	now := time.Now().UTC()
	if enrollment.EnrolledAt.IsZero() {
		enrollment.EnrolledAt = now
	}
	_, err = s.db.Exec(`
			INSERT INTO durable_agent_remote_enrollments(
				agent_id, parent_control_url, protocol_version, status, last_sequence, enrolled_at, last_seen_at, revoked_at,
				tailnet_stable_node_id, tailnet_node_name, tailnet_computed_name, tailnet_login_name, tailnet_tags_json, updated_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(agent_id) DO UPDATE SET
				parent_control_url = excluded.parent_control_url,
				protocol_version = excluded.protocol_version,
				status = excluded.status,
				last_sequence = excluded.last_sequence,
				enrolled_at = excluded.enrolled_at,
				last_seen_at = excluded.last_seen_at,
				revoked_at = excluded.revoked_at,
				tailnet_stable_node_id = excluded.tailnet_stable_node_id,
				tailnet_node_name = excluded.tailnet_node_name,
				tailnet_computed_name = excluded.tailnet_computed_name,
				tailnet_login_name = excluded.tailnet_login_name,
				tailnet_tags_json = excluded.tailnet_tags_json,
				updated_at = excluded.updated_at
		`,
		enrollment.AgentID, enrollment.ParentControlURL, enrollment.ProtocolVersion, enrollment.Status,
		maxInt64(enrollment.LastSequence, 0), nullableTime(enrollment.EnrolledAt), nullableTime(enrollment.LastSeenAt), nullableTime(enrollment.RevokedAt),
		enrollment.TailnetIdentity.StableNodeID, enrollment.TailnetIdentity.NodeName, enrollment.TailnetIdentity.ComputedName, enrollment.TailnetIdentity.LoginName, string(tagsJSON),
		now.Format(time.RFC3339Nano),
	)
	if err != nil {
		return fmt.Errorf("upsert durable agent remote enrollment: %w", err)
	}
	return nil
}

func (s *SQLiteStore) DurableAgentRemoteEnrollment(agentID string) (*core.DurableAgentRemoteEnrollment, error) {
	rows, err := s.db.Query(`
			SELECT agent_id, parent_control_url, protocol_version, status, last_sequence, enrolled_at, last_seen_at, revoked_at,
				tailnet_stable_node_id, tailnet_node_name, tailnet_computed_name, tailnet_login_name, tailnet_tags_json
			FROM durable_agent_remote_enrollments
			WHERE agent_id = ?
	`, strings.TrimSpace(agentID))
	if err != nil {
		return nil, fmt.Errorf("query durable agent remote enrollment: %w", err)
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, sql.ErrNoRows
	}
	enrollment, err := scanDurableAgentRemoteEnrollment(rows)
	if err != nil {
		return nil, err
	}
	return &enrollment, nil
}

func (s *SQLiteStore) AcceptDurableAgentControlEnvelope(envelope core.DurableAgentControlEnvelope, receivedAt time.Time) error {
	envelope = core.NormalizeDurableAgentControlEnvelope(envelope)
	if err := core.ValidateDurableAgentControlEnvelope(envelope); err != nil {
		return err
	}
	if receivedAt.IsZero() {
		receivedAt = time.Now().UTC()
	}
	return s.acceptDurableAgentControlEnvelope(envelope, receivedAt, nil)
}

func (s *SQLiteStore) AcceptDurableAgentControlEnvelopeFromTailnetPeer(envelope core.DurableAgentControlEnvelope, identity core.TailnetPeerIdentity, receivedAt time.Time) error {
	identity = core.NormalizeTailnetPeerIdentity(identity)
	if strings.TrimSpace(identity.StableNodeID) == "" {
		return fmt.Errorf("durable agent tailnet stable node id is required")
	}
	return s.acceptDurableAgentControlEnvelope(envelope, receivedAt, func(enrollment *core.DurableAgentRemoteEnrollment) error {
		storedStableNodeID := strings.TrimSpace(enrollment.TailnetIdentity.StableNodeID)
		switch {
		case storedStableNodeID == "":
			enrollment.TailnetIdentity = identity
		case storedStableNodeID != strings.TrimSpace(identity.StableNodeID):
			return fmt.Errorf("durable agent control request came from a different tailnet node")
		default:
			enrollment.TailnetIdentity = identity
		}
		return nil
	})
}

func (s *SQLiteStore) AcceptDurableAgentEnrollment(envelope core.DurableAgentControlEnvelope, enrollment core.DurableAgentRemoteEnrollment, receivedAt time.Time) error {
	envelope = core.NormalizeDurableAgentControlEnvelope(envelope)
	if err := core.ValidateDurableAgentControlEnvelope(envelope); err != nil {
		return err
	}
	enrollment = core.NormalizeDurableAgentRemoteEnrollment(enrollment)
	if enrollment.AgentID == "" {
		return fmt.Errorf("accept durable agent enrollment: agent_id is required")
	}
	if enrollment.AgentID != envelope.AgentID {
		return fmt.Errorf("accept durable agent enrollment: agent_id does not match envelope")
	}
	if enrollment.ParentControlURL == "" {
		return fmt.Errorf("accept durable agent enrollment: parent_control_url is required")
	}
	if receivedAt.IsZero() {
		receivedAt = time.Now().UTC()
	}
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin durable agent enrollment tx: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	existing, err := queryDurableAgentRemoteEnrollment(tx, envelope.AgentID)
	if err != nil {
		if err == sql.ErrNoRows {
			existing = nil
		} else {
			return err
		}
	}

	if existing != nil {
		if existing.Status != "active" {
			return fmt.Errorf("durable agent remote enrollment %s is not active", existing.AgentID)
		}
		if enrollment.EnrolledAt.IsZero() {
			enrollment.EnrolledAt = existing.EnrolledAt
		}
		enrollment.LastSequence = existing.LastSequence
		if enrollment.TailnetIdentity.StableNodeID == "" && existing.TailnetIdentity.StableNodeID != "" {
			enrollment.TailnetIdentity = existing.TailnetIdentity
		}
		if enrollment.TailnetIdentity.StableNodeID != "" && existing.TailnetIdentity.StableNodeID != "" &&
			enrollment.TailnetIdentity.StableNodeID != existing.TailnetIdentity.StableNodeID {
			return fmt.Errorf("durable agent enrollment came from a different tailnet node")
		}
	}
	if enrollment.Status == "" {
		enrollment.Status = "active"
	}
	if enrollment.Status != "active" {
		return fmt.Errorf("durable agent remote enrollment %s is not active", enrollment.AgentID)
	}
	if err := insertDurableAgentControlReceiptExec(tx, envelope, receivedAt); err != nil {
		return err
	}
	if envelope.Sequence <= enrollment.LastSequence {
		return fmt.Errorf("out-of-order durable agent control envelope for %s", enrollment.AgentID)
	}
	enrollment.LastSequence = envelope.Sequence
	enrollment.LastSeenAt = receivedAt.UTC()
	if err := upsertDurableAgentRemoteEnrollmentExec(tx, enrollment); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit durable agent enrollment tx: %w", err)
	}
	return nil
}

func (s *SQLiteStore) acceptDurableAgentControlEnvelope(envelope core.DurableAgentControlEnvelope, receivedAt time.Time, updateEnrollment func(*core.DurableAgentRemoteEnrollment) error) error {
	envelope = core.NormalizeDurableAgentControlEnvelope(envelope)
	if err := core.ValidateDurableAgentControlEnvelope(envelope); err != nil {
		return err
	}
	if receivedAt.IsZero() {
		receivedAt = time.Now().UTC()
	}
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin durable agent control envelope tx: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	enrollment, err := queryDurableAgentRemoteEnrollment(tx, envelope.AgentID)
	if err != nil {
		return err
	}
	if enrollment.Status != "active" {
		return fmt.Errorf("durable agent remote enrollment %s is not active", enrollment.AgentID)
	}
	if err := insertDurableAgentControlReceiptExec(tx, envelope, receivedAt); err != nil {
		return err
	}
	if envelope.Sequence <= enrollment.LastSequence {
		return fmt.Errorf("out-of-order durable agent control envelope for %s", enrollment.AgentID)
	}
	if updateEnrollment != nil {
		if err := updateEnrollment(enrollment); err != nil {
			return err
		}
	}
	enrollment.LastSequence = envelope.Sequence
	enrollment.LastSeenAt = receivedAt.UTC()
	if err := upsertDurableAgentRemoteEnrollmentExec(tx, *enrollment); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit durable agent control envelope tx: %w", err)
	}
	return nil
}

func (s *SQLiteStore) DurableAgentControlReceipt(agentID string, messageID string) (*core.DurableAgentControlReceipt, error) {
	row := s.db.QueryRow(`
		SELECT agent_id, message_id, message_kind, sequence, signature, received_at, response_status, response_json
		FROM durable_agent_control_receipts
		WHERE agent_id = ? AND message_id = ?
	`, strings.TrimSpace(agentID), strings.TrimSpace(messageID))
	receipt, err := scanDurableAgentControlReceipt(row)
	if err != nil {
		return nil, err
	}
	return &receipt, nil
}

func (s *SQLiteStore) StoreDurableAgentControlReceiptResponse(agentID string, messageID string, status int, responseJSON string) error {
	agentID = strings.TrimSpace(agentID)
	messageID = strings.TrimSpace(messageID)
	if agentID == "" {
		return fmt.Errorf("store durable agent control receipt response: agent_id is required")
	}
	if messageID == "" {
		return fmt.Errorf("store durable agent control receipt response: message_id is required")
	}
	if status <= 0 {
		return fmt.Errorf("store durable agent control receipt response: response status is required")
	}
	if responseJSON == "" {
		return fmt.Errorf("store durable agent control receipt response: response json is required")
	}
	result, err := s.db.Exec(`
		UPDATE durable_agent_control_receipts
		SET response_status = ?, response_json = ?
		WHERE agent_id = ? AND message_id = ?
	`, status, responseJSON, agentID, messageID)
	if err != nil {
		return fmt.Errorf("store durable agent control receipt response: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("store durable agent control receipt response affected rows: %w", err)
	}
	if affected == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func insertDurableAgentControlReceiptExec(exec sqlExecer, envelope core.DurableAgentControlEnvelope, receivedAt time.Time) error {
	envelope = core.NormalizeDurableAgentControlEnvelope(envelope)
	if receivedAt.IsZero() {
		receivedAt = time.Now().UTC()
	}
	_, err := exec.Exec(`
		INSERT INTO durable_agent_control_receipts(agent_id, message_id, message_kind, sequence, signature, received_at)
		VALUES (?, ?, ?, ?, ?, ?)
	`,
		envelope.AgentID, envelope.MessageID, envelope.MessageKind, envelope.Sequence, envelope.Signature, receivedAt.UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return fmt.Errorf("replay durable agent control envelope for %s", envelope.AgentID)
		}
		return fmt.Errorf("insert durable agent control receipt: %w", err)
	}
	return nil
}

func upsertDurableAgentRemoteEnrollmentExec(exec sqlExecer, enrollment core.DurableAgentRemoteEnrollment) error {
	enrollment = core.NormalizeDurableAgentRemoteEnrollment(enrollment)
	if enrollment.AgentID == "" {
		return fmt.Errorf("upsert durable agent remote enrollment: agent_id is required")
	}
	if enrollment.ParentControlURL == "" {
		return fmt.Errorf("upsert durable agent remote enrollment: parent_control_url is required")
	}
	tagsJSON, err := marshalStringSlice(enrollment.TailnetIdentity.Tags)
	if err != nil {
		return fmt.Errorf("marshal durable agent remote enrollment tailnet tags: %w", err)
	}
	now := time.Now().UTC()
	if enrollment.EnrolledAt.IsZero() {
		enrollment.EnrolledAt = now
	}
	_, err = exec.Exec(`
			INSERT INTO durable_agent_remote_enrollments(
				agent_id, parent_control_url, protocol_version, status, last_sequence, enrolled_at, last_seen_at, revoked_at,
				tailnet_stable_node_id, tailnet_node_name, tailnet_computed_name, tailnet_login_name, tailnet_tags_json, updated_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(agent_id) DO UPDATE SET
				parent_control_url = excluded.parent_control_url,
				protocol_version = excluded.protocol_version,
				status = excluded.status,
				last_sequence = excluded.last_sequence,
				enrolled_at = excluded.enrolled_at,
				last_seen_at = excluded.last_seen_at,
				revoked_at = excluded.revoked_at,
				tailnet_stable_node_id = excluded.tailnet_stable_node_id,
				tailnet_node_name = excluded.tailnet_node_name,
				tailnet_computed_name = excluded.tailnet_computed_name,
				tailnet_login_name = excluded.tailnet_login_name,
				tailnet_tags_json = excluded.tailnet_tags_json,
				updated_at = excluded.updated_at
		`,
		enrollment.AgentID, enrollment.ParentControlURL, enrollment.ProtocolVersion, enrollment.Status,
		maxInt64(enrollment.LastSequence, 0), nullableTime(enrollment.EnrolledAt), nullableTime(enrollment.LastSeenAt), nullableTime(enrollment.RevokedAt),
		enrollment.TailnetIdentity.StableNodeID, enrollment.TailnetIdentity.NodeName, enrollment.TailnetIdentity.ComputedName, enrollment.TailnetIdentity.LoginName, string(tagsJSON),
		now.Format(time.RFC3339Nano),
	)
	if err != nil {
		return fmt.Errorf("upsert durable agent remote enrollment: %w", err)
	}
	return nil
}

func queryDurableAgentRemoteEnrollment(q sqlQueryer, agentID string) (*core.DurableAgentRemoteEnrollment, error) {
	rows, err := q.Query(`
			SELECT agent_id, parent_control_url, protocol_version, status, last_sequence, enrolled_at, last_seen_at, revoked_at,
				tailnet_stable_node_id, tailnet_node_name, tailnet_computed_name, tailnet_login_name, tailnet_tags_json
			FROM durable_agent_remote_enrollments
			WHERE agent_id = ?
	`, strings.TrimSpace(agentID))
	if err != nil {
		return nil, fmt.Errorf("query durable agent remote enrollment: %w", err)
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, sql.ErrNoRows
	}
	enrollment, err := scanDurableAgentRemoteEnrollment(rows)
	if err != nil {
		return nil, err
	}
	return &enrollment, nil
}

func scanDurableAgentRemoteEnrollment(scanner interface{ Scan(dest ...any) error }) (core.DurableAgentRemoteEnrollment, error) {
	var (
		enrollment      core.DurableAgentRemoteEnrollment
		protocolVersion sql.NullString
		statusRaw       sql.NullString
		enrolledAtRaw   sql.NullString
		lastSeenAtRaw   sql.NullString
		revokedAtRaw    sql.NullString
		tailnetID       sql.NullString
		tailnetName     sql.NullString
		tailnetComputed sql.NullString
		tailnetLogin    sql.NullString
		tailnetTagsJSON sql.NullString
	)
	if err := scanner.Scan(
		&enrollment.AgentID, &enrollment.ParentControlURL, &protocolVersion, &statusRaw, &enrollment.LastSequence, &enrolledAtRaw, &lastSeenAtRaw, &revokedAtRaw,
		&tailnetID, &tailnetName, &tailnetComputed, &tailnetLogin, &tailnetTagsJSON,
	); err != nil {
		return core.DurableAgentRemoteEnrollment{}, fmt.Errorf("scan durable agent remote enrollment: %w", err)
	}
	enrollment.ProtocolVersion = nullToString(protocolVersion)
	enrollment.Status = nullToString(statusRaw)
	var err error
	if enrolledAtRaw.Valid && enrolledAtRaw.String != "" {
		enrollment.EnrolledAt, err = parseSQLiteTime(enrolledAtRaw.String)
		if err != nil {
			return core.DurableAgentRemoteEnrollment{}, fmt.Errorf("parse durable agent remote enrollment enrolled_at: %w", err)
		}
	}
	if lastSeenAtRaw.Valid && lastSeenAtRaw.String != "" {
		enrollment.LastSeenAt, err = parseSQLiteTime(lastSeenAtRaw.String)
		if err != nil {
			return core.DurableAgentRemoteEnrollment{}, fmt.Errorf("parse durable agent remote enrollment last_seen_at: %w", err)
		}
	}
	if revokedAtRaw.Valid && revokedAtRaw.String != "" {
		enrollment.RevokedAt, err = parseSQLiteTime(revokedAtRaw.String)
		if err != nil {
			return core.DurableAgentRemoteEnrollment{}, fmt.Errorf("parse durable agent remote enrollment revoked_at: %w", err)
		}
	}
	tags, err := unmarshalStringSlice(nullToString(tailnetTagsJSON))
	if err != nil {
		return core.DurableAgentRemoteEnrollment{}, fmt.Errorf("decode durable agent remote enrollment tailnet tags: %w", err)
	}
	enrollment.TailnetIdentity = core.TailnetPeerIdentity{
		StableNodeID: nullToString(tailnetID),
		NodeName:     nullToString(tailnetName),
		ComputedName: nullToString(tailnetComputed),
		LoginName:    nullToString(tailnetLogin),
		Tags:         tags,
	}
	return core.NormalizeDurableAgentRemoteEnrollment(enrollment), nil
}

func scanDurableAgentControlReceipt(scanner interface{ Scan(dest ...any) error }) (core.DurableAgentControlReceipt, error) {
	var (
		receipt      core.DurableAgentControlReceipt
		signatureRaw sql.NullString
		receivedRaw  string
		responseRaw  sql.NullString
	)
	if err := scanner.Scan(
		&receipt.AgentID, &receipt.MessageID, &receipt.MessageKind, &receipt.Sequence, &signatureRaw, &receivedRaw, &receipt.ResponseStatus, &responseRaw,
	); err != nil {
		return core.DurableAgentControlReceipt{}, err
	}
	receipt.Signature = nullToString(signatureRaw)
	receipt.ResponseJSON = nullToString(responseRaw)
	receivedAt, err := parseSQLiteTime(receivedRaw)
	if err != nil {
		return core.DurableAgentControlReceipt{}, fmt.Errorf("parse durable agent control receipt received_at: %w", err)
	}
	receipt.ReceivedAt = receivedAt
	return receipt, nil
}
