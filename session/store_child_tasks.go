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

func (s *SQLiteStore) RecordChildTaskPacket(input ChildTaskPacketInput) (ChildTaskPacket, error) {
	if s == nil || s.db == nil {
		return ChildTaskPacket{}, fmt.Errorf("child task store unavailable")
	}
	input = NormalizeChildTaskPacketInput(input)
	if input.PacketID == "" {
		return ChildTaskPacket{}, fmt.Errorf("child task packet_id is required")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return ChildTaskPacket{}, fmt.Errorf("begin child task packet tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	packet, err := recordChildTaskPacketTx(tx, input)
	if err != nil {
		return ChildTaskPacket{}, err
	}
	if err := tx.Commit(); err != nil {
		return ChildTaskPacket{}, fmt.Errorf("commit child task packet tx: %w", err)
	}
	return packet, nil
}

func recordChildTaskPacketTx(tx *sql.Tx, input ChildTaskPacketInput) (ChildTaskPacket, error) {
	input = NormalizeChildTaskPacketInput(input)
	if existing, ok, err := childTaskPacketByIDTx(tx, input.PacketID); err != nil {
		return ChildTaskPacket{}, err
	} else if ok {
		return existing, nil
	}
	scope := defaultScopeForKey(input.Key)
	sessionID := SessionIDForKey(input.Key)
	createdAt := input.CreatedAt.UTC()
	inputJSON := strings.TrimSpace(input.InputJSON)
	if inputJSON == "" {
		inputJSON = "{}"
	}
	if _, err := tx.Exec(`
		INSERT INTO child_task_packets(
			packet_id, task_lease_id, agent_id, session_id, chat_id, user_id,
			scope_kind, scope_id, durable_agent_id, task_kind, status, authority_kind,
			authority_id, grant_id, request_id, target_resource, required_action,
			input_json, result_id, created_at, updated_at, terminal_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, '', ?, ?, NULL)
	`, input.PacketID, input.TaskLeaseID, input.AgentID, sessionID, input.Key.ChatID, input.Key.UserID,
		string(scope.Kind), scope.ID, scope.DurableAgentID, input.TaskKind, string(input.Status), input.AuthorityKind,
		input.AuthorityID, input.GrantID, input.RequestID, input.TargetResource, input.RequiredAction,
		inputJSON, createdAt.Format(time.RFC3339Nano), createdAt.Format(time.RFC3339Nano)); err != nil {
		return ChildTaskPacket{}, fmt.Errorf("insert child task packet %s: %w", input.PacketID, err)
	}
	payloadRaw, _ := json.Marshal(map[string]any{
		"packet_id":       input.PacketID,
		"task_lease_id":   input.TaskLeaseID,
		"agent_id":        input.AgentID,
		"task_kind":       input.TaskKind,
		"status":          string(input.Status),
		"authority_kind":  input.AuthorityKind,
		"authority_id":    input.AuthorityID,
		"grant_id":        input.GrantID,
		"request_id":      input.RequestID,
		"target_resource": input.TargetResource,
		"required_action": input.RequiredAction,
	})
	if _, err := appendExecutionEventsTx(tx, input.Key, []ExecutionEventInput{{
		EventType:   core.ExecutionEventDurableChildTaskQueued,
		Stage:       "child_task",
		Status:      string(input.Status),
		PayloadJSON: string(payloadRaw),
		CreatedAt:   createdAt,
	}}); err != nil {
		return ChildTaskPacket{}, fmt.Errorf("append child task packet event: %w", err)
	}
	packet, ok, err := childTaskPacketByIDTx(tx, input.PacketID)
	if err != nil {
		return ChildTaskPacket{}, err
	}
	if !ok {
		return ChildTaskPacket{}, fmt.Errorf("child task packet %s not found after insert", input.PacketID)
	}
	return packet, nil
}

func (s *SQLiteStore) RecordChildTaskResult(input ChildTaskResultInput) (ChildTaskResult, error) {
	if s == nil || s.db == nil {
		return ChildTaskResult{}, fmt.Errorf("child task store unavailable")
	}
	input = NormalizeChildTaskResultInput(input)
	if input.ResultID == "" {
		return ChildTaskResult{}, fmt.Errorf("child task result_id is required")
	}
	if input.PacketID == "" {
		return ChildTaskResult{}, fmt.Errorf("child task result packet_id is required")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return ChildTaskResult{}, fmt.Errorf("begin child task result tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	result, err := recordChildTaskResultTx(tx, input)
	if err != nil {
		return ChildTaskResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return ChildTaskResult{}, fmt.Errorf("commit child task result tx: %w", err)
	}
	return result, nil
}

func recordChildTaskResultTx(tx *sql.Tx, input ChildTaskResultInput) (ChildTaskResult, error) {
	input = NormalizeChildTaskResultInput(input)
	if existing, ok, err := childTaskResultByIDTx(tx, input.ResultID); err != nil {
		return ChildTaskResult{}, err
	} else if ok {
		return existing, nil
	}
	packet, ok, err := childTaskPacketByIDTx(tx, input.PacketID)
	if err != nil {
		return ChildTaskResult{}, err
	}
	if !ok {
		return ChildTaskResult{}, fmt.Errorf("child task packet %s not found", input.PacketID)
	}
	if input.AgentID == "" {
		input.AgentID = packet.AgentID
	}
	if input.TaskLeaseID == "" {
		input.TaskLeaseID = packet.TaskLeaseID
	}
	sessionID := firstNonEmptyString(SessionIDForKey(input.Key), packet.SessionID)
	createdAt := input.CreatedAt.UTC()
	evidenceRefs := encodeStringList(input.EvidenceRefs)
	if _, err := tx.Exec(`
		INSERT INTO child_task_results(
			result_id, packet_id, task_lease_id, agent_id, session_id, status,
			result_kind, summary, blocker_kind, error_text, evidence_refs_json,
			next_state, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, input.ResultID, input.PacketID, input.TaskLeaseID, input.AgentID, sessionID, string(input.Status),
		input.ResultKind, input.Summary, input.BlockerKind, input.ErrorText, evidenceRefs,
		string(input.NextState), createdAt.Format(time.RFC3339Nano)); err != nil {
		return ChildTaskResult{}, fmt.Errorf("insert child task result %s: %w", input.ResultID, err)
	}
	packetStatus := childTaskPacketStatusForResult(input.Status)
	if _, err := tx.Exec(`
		UPDATE child_task_packets
		SET status = ?, result_id = ?, updated_at = ?, terminal_at = ?
		WHERE packet_id = ?
	`, string(packetStatus), input.ResultID, createdAt.Format(time.RFC3339Nano), createdAt.Format(time.RFC3339Nano), input.PacketID); err != nil {
		return ChildTaskResult{}, fmt.Errorf("update child task packet terminal state: %w", err)
	}
	payloadRaw, _ := json.Marshal(map[string]any{
		"result_id":     input.ResultID,
		"packet_id":     input.PacketID,
		"task_lease_id": input.TaskLeaseID,
		"agent_id":      input.AgentID,
		"status":        string(input.Status),
		"result_kind":   input.ResultKind,
		"blocker_kind":  input.BlockerKind,
		"evidence_refs": input.EvidenceRefs,
		"next_state":    string(input.NextState),
	})
	if _, err := appendExecutionEventsTx(tx, input.Key, []ExecutionEventInput{{
		EventType:   core.ExecutionEventDurableChildTaskResult,
		Stage:       "child_task",
		Status:      string(input.Status),
		PayloadJSON: string(payloadRaw),
		CreatedAt:   createdAt,
	}}); err != nil {
		return ChildTaskResult{}, fmt.Errorf("append child task result event: %w", err)
	}
	result, ok, err := childTaskResultByIDTx(tx, input.ResultID)
	if err != nil {
		return ChildTaskResult{}, err
	}
	if !ok {
		return ChildTaskResult{}, fmt.Errorf("child task result %s not found after insert", input.ResultID)
	}
	return result, nil
}

func (s *SQLiteStore) ChildTaskPacket(packetID string) (ChildTaskPacket, bool, error) {
	if s == nil || s.db == nil {
		return ChildTaskPacket{}, false, nil
	}
	return childTaskPacketByIDTx(s.db, packetID)
}

func childTaskPacketByIDTx(queryer interface {
	QueryRow(query string, args ...any) *sql.Row
}, packetID string) (ChildTaskPacket, bool, error) {
	packetID = strings.TrimSpace(packetID)
	if packetID == "" {
		return ChildTaskPacket{}, false, nil
	}
	row := queryer.QueryRow(childTaskPacketSelectSQL()+` WHERE packet_id = ?`, packetID)
	packet, err := scanChildTaskPacket(row)
	if errors.Is(err, sql.ErrNoRows) {
		return ChildTaskPacket{}, false, nil
	}
	if err != nil {
		return ChildTaskPacket{}, false, err
	}
	return packet, true, nil
}

func (s *SQLiteStore) ChildTaskResult(resultID string) (ChildTaskResult, bool, error) {
	if s == nil || s.db == nil {
		return ChildTaskResult{}, false, nil
	}
	return childTaskResultByIDTx(s.db, resultID)
}

func childTaskResultByIDTx(queryer interface {
	QueryRow(query string, args ...any) *sql.Row
}, resultID string) (ChildTaskResult, bool, error) {
	resultID = strings.TrimSpace(resultID)
	if resultID == "" {
		return ChildTaskResult{}, false, nil
	}
	row := queryer.QueryRow(childTaskResultSelectSQL()+` WHERE result_id = ?`, resultID)
	result, err := scanChildTaskResult(row)
	if errors.Is(err, sql.ErrNoRows) {
		return ChildTaskResult{}, false, nil
	}
	if err != nil {
		return ChildTaskResult{}, false, err
	}
	return result, true, nil
}

func childTaskPacketSelectSQL() string {
	return `
		SELECT packet_id, task_lease_id, agent_id, session_id, chat_id, user_id,
			scope_kind, scope_id, durable_agent_id, task_kind, status, authority_kind,
			authority_id, grant_id, request_id, target_resource, required_action,
			input_json, result_id, created_at, updated_at, terminal_at
		FROM child_task_packets
	`
}

func childTaskResultSelectSQL() string {
	return `
		SELECT result_id, packet_id, task_lease_id, agent_id, session_id, status,
			result_kind, summary, blocker_kind, error_text, evidence_refs_json,
			next_state, created_at
		FROM child_task_results
	`
}

func scanChildTaskPacket(scanner interface{ Scan(dest ...any) error }) (ChildTaskPacket, error) {
	var (
		packet            ChildTaskPacket
		scopeKindRaw      string
		scopeIDRaw        string
		durableAgentIDRaw string
		statusRaw         string
		createdAtRaw      string
		updatedAtRaw      string
		terminalAtRaw     sql.NullString
	)
	if err := scanner.Scan(
		&packet.PacketID,
		&packet.TaskLeaseID,
		&packet.AgentID,
		&packet.SessionID,
		&packet.ChatID,
		&packet.UserID,
		&scopeKindRaw,
		&scopeIDRaw,
		&durableAgentIDRaw,
		&packet.TaskKind,
		&statusRaw,
		&packet.AuthorityKind,
		&packet.AuthorityID,
		&packet.GrantID,
		&packet.RequestID,
		&packet.TargetResource,
		&packet.RequiredAction,
		&packet.InputJSON,
		&packet.ResultID,
		&createdAtRaw,
		&updatedAtRaw,
		&terminalAtRaw,
	); err != nil {
		return ChildTaskPacket{}, err
	}
	createdAt, err := parseSQLiteTime(createdAtRaw)
	if err != nil {
		return ChildTaskPacket{}, fmt.Errorf("parse child task packet created_at: %w", err)
	}
	updatedAt, err := parseSQLiteTime(updatedAtRaw)
	if err != nil {
		return ChildTaskPacket{}, fmt.Errorf("parse child task packet updated_at: %w", err)
	}
	packet.Scope = NormalizeScopeRef(ScopeRef{
		Kind:           ScopeKind(strings.TrimSpace(scopeKindRaw)),
		ID:             strings.TrimSpace(scopeIDRaw),
		DurableAgentID: strings.TrimSpace(durableAgentIDRaw),
	})
	packet.Status = NormalizeChildTaskPacketStatus(ChildTaskPacketStatus(statusRaw))
	packet.CreatedAt = createdAt
	packet.UpdatedAt = updatedAt
	if terminalAtRaw.Valid && strings.TrimSpace(terminalAtRaw.String) != "" {
		terminalAt, err := parseSQLiteTime(terminalAtRaw.String)
		if err != nil {
			return ChildTaskPacket{}, fmt.Errorf("parse child task packet terminal_at: %w", err)
		}
		packet.TerminalAt = terminalAt
	}
	return packet, nil
}

func scanChildTaskResult(scanner interface{ Scan(dest ...any) error }) (ChildTaskResult, error) {
	var (
		result          ChildTaskResult
		statusRaw       string
		nextStateRaw    string
		evidenceRefsRaw string
		createdAtRaw    string
	)
	if err := scanner.Scan(
		&result.ResultID,
		&result.PacketID,
		&result.TaskLeaseID,
		&result.AgentID,
		&result.SessionID,
		&statusRaw,
		&result.ResultKind,
		&result.Summary,
		&result.BlockerKind,
		&result.ErrorText,
		&evidenceRefsRaw,
		&nextStateRaw,
		&createdAtRaw,
	); err != nil {
		return ChildTaskResult{}, err
	}
	evidenceRefs := decodeStringList(evidenceRefsRaw)
	createdAt, err := parseSQLiteTime(createdAtRaw)
	if err != nil {
		return ChildTaskResult{}, fmt.Errorf("parse child task result created_at: %w", err)
	}
	result.Status = NormalizeChildTaskResultStatus(ChildTaskResultStatus(statusRaw))
	result.NextState = NormalizeNextActionState(NextActionState(nextStateRaw))
	result.EvidenceRefs = evidenceRefs
	result.CreatedAt = createdAt
	return result, nil
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}
