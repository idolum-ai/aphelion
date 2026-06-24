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
			input_json, active_attempt_id, lease_owner, lease_generation, fencing_token,
			lease_expires_at, lease_heartbeat_at, lease_released_at, result_id,
			created_at, updated_at, terminal_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, '', '', 0, '', '', '', NULL, '', ?, ?, NULL)
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

func (s *SQLiteStore) ClaimChildTaskAttempt(input ChildTaskAttemptClaimInput) (ChildTaskPacket, error) {
	if s == nil || s.db == nil {
		return ChildTaskPacket{}, fmt.Errorf("child task store unavailable")
	}
	input = NormalizeChildTaskAttemptClaimInput(input)
	if input.PacketID == "" {
		return ChildTaskPacket{}, fmt.Errorf("child task attempt packet_id is required")
	}
	if input.AttemptID == "" {
		return ChildTaskPacket{}, fmt.Errorf("child task attempt_id is required")
	}
	if input.LeaseOwner == "" {
		return ChildTaskPacket{}, fmt.Errorf("child task attempt lease_owner is required")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return ChildTaskPacket{}, fmt.Errorf("begin child task attempt tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	packet, err := claimChildTaskAttemptTx(tx, input)
	if err != nil {
		return ChildTaskPacket{}, err
	}
	if err := tx.Commit(); err != nil {
		return ChildTaskPacket{}, fmt.Errorf("commit child task attempt tx: %w", err)
	}
	return packet, nil
}

func claimChildTaskAttemptTx(tx *sql.Tx, input ChildTaskAttemptClaimInput) (ChildTaskPacket, error) {
	input = NormalizeChildTaskAttemptClaimInput(input)
	packet, ok, err := childTaskPacketByIDTx(tx, input.PacketID)
	if err != nil {
		return ChildTaskPacket{}, err
	}
	if !ok {
		return ChildTaskPacket{}, fmt.Errorf("child task packet %s not found", input.PacketID)
	}
	if ChildTaskPacketStatusTerminal(packet.Status) {
		return ChildTaskPacket{}, fmt.Errorf("child task packet %s is terminal (%s); explicit reopen required before claiming another attempt", input.PacketID, packet.Status)
	}
	claimedAt := input.ClaimedAt.UTC()
	leaseExpiresAt := input.LeaseExpiresAt.UTC()
	if !leaseExpiresAt.After(claimedAt) {
		return ChildTaskPacket{}, fmt.Errorf("child task attempt lease for packet %s must expire after claim time", input.PacketID)
	}
	if packet.ActiveAttemptID == input.AttemptID && packet.LeaseOwner == input.LeaseOwner && childTaskPacketLeaseLive(packet, claimedAt) {
		return packet, nil
	}
	if packet.ActiveAttemptID == input.AttemptID && packet.LeaseGeneration > 0 {
		return ChildTaskPacket{}, fmt.Errorf("child task attempt %s for packet %s was already used; use a new attempt id", input.AttemptID, input.PacketID)
	}
	if childTaskPacketLeaseLive(packet, claimedAt) {
		return ChildTaskPacket{}, fmt.Errorf("child task packet %s has a live lease held by owner %s attempt %s", input.PacketID, packet.LeaseOwner, packet.ActiveAttemptID)
	}
	nextGeneration := packet.LeaseGeneration + 1
	if nextGeneration <= 0 {
		nextGeneration = 1
	}
	fencingToken := ChildTaskFencingToken(input.PacketID, input.AttemptID, nextGeneration)
	if fencingToken == "" {
		return ChildTaskPacket{}, fmt.Errorf("child task attempt fence could not be generated for packet %s", input.PacketID)
	}
	if _, err := tx.Exec(`
		UPDATE child_task_packets
		SET status = ?, active_attempt_id = ?, lease_owner = ?, lease_generation = ?, fencing_token = ?,
			lease_expires_at = ?, lease_heartbeat_at = ?, lease_released_at = NULL,
			updated_at = ?, terminal_at = NULL
		WHERE packet_id = ?
			AND status NOT IN (?, ?, ?, ?, ?)
	`, string(ChildTaskPacketInProgress), input.AttemptID, input.LeaseOwner, nextGeneration, fencingToken,
		leaseExpiresAt.Format(time.RFC3339Nano), claimedAt.Format(time.RFC3339Nano),
		claimedAt.Format(time.RFC3339Nano), input.PacketID,
		string(ChildTaskPacketCompleted), string(ChildTaskPacketBlocked), string(ChildTaskPacketFailed), string(ChildTaskPacketRevoked), string(ChildTaskPacketExpired)); err != nil {
		return ChildTaskPacket{}, fmt.Errorf("claim child task attempt %s/%s: %w", input.PacketID, input.AttemptID, err)
	}
	claimed, ok, err := childTaskPacketByIDTx(tx, input.PacketID)
	if err != nil {
		return ChildTaskPacket{}, err
	}
	if !ok {
		return ChildTaskPacket{}, fmt.Errorf("child task packet %s not found after attempt claim", input.PacketID)
	}
	if claimed.ActiveAttemptID != input.AttemptID || claimed.LeaseOwner != input.LeaseOwner || claimed.LeaseGeneration != nextGeneration || claimed.FencingToken != fencingToken {
		return ChildTaskPacket{}, fmt.Errorf("child task attempt claim for packet %s lost fence ownership", input.PacketID)
	}
	return claimed, nil
}

func (s *SQLiteStore) HeartbeatChildTaskAttempt(input ChildTaskAttemptHeartbeatInput) (ChildTaskPacket, error) {
	if s == nil || s.db == nil {
		return ChildTaskPacket{}, fmt.Errorf("child task store unavailable")
	}
	input = NormalizeChildTaskAttemptHeartbeatInput(input)
	if input.PacketID == "" || input.AttemptID == "" || input.LeaseOwner == "" || input.LeaseGeneration <= 0 || input.FencingToken == "" {
		return ChildTaskPacket{}, fmt.Errorf("child task heartbeat requires packet, attempt, owner, generation, and fencing token")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return ChildTaskPacket{}, fmt.Errorf("begin child task heartbeat tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	packet, err := heartbeatChildTaskAttemptTx(tx, input)
	if err != nil {
		return ChildTaskPacket{}, err
	}
	if err := tx.Commit(); err != nil {
		return ChildTaskPacket{}, fmt.Errorf("commit child task heartbeat tx: %w", err)
	}
	return packet, nil
}

func heartbeatChildTaskAttemptTx(tx *sql.Tx, input ChildTaskAttemptHeartbeatInput) (ChildTaskPacket, error) {
	input = NormalizeChildTaskAttemptHeartbeatInput(input)
	packet, ok, err := childTaskPacketByIDTx(tx, input.PacketID)
	if err != nil {
		return ChildTaskPacket{}, err
	}
	if !ok {
		return ChildTaskPacket{}, fmt.Errorf("child task packet %s not found", input.PacketID)
	}
	if !childTaskAttemptOwnsPacketLease(packet, input.AttemptID, input.LeaseOwner, input.LeaseGeneration, input.FencingToken) {
		return ChildTaskPacket{}, fmt.Errorf("child task heartbeat attempt %s does not own active packet lease", input.AttemptID)
	}
	if !childTaskPacketLeaseLive(packet, input.HeartbeatAt) {
		return ChildTaskPacket{}, fmt.Errorf("child task heartbeat attempt %s lease is not live", input.AttemptID)
	}
	if !input.LeaseExpiresAt.After(input.HeartbeatAt) {
		return ChildTaskPacket{}, fmt.Errorf("child task heartbeat lease for packet %s must expire after heartbeat time", input.PacketID)
	}
	if _, err := tx.Exec(`
		UPDATE child_task_packets
		SET lease_expires_at = ?, lease_heartbeat_at = ?, updated_at = ?
		WHERE packet_id = ?
			AND active_attempt_id = ?
			AND lease_owner = ?
			AND lease_generation = ?
			AND fencing_token = ?
			AND lease_released_at IS NULL
	`, input.LeaseExpiresAt.Format(time.RFC3339Nano), input.HeartbeatAt.Format(time.RFC3339Nano), input.HeartbeatAt.Format(time.RFC3339Nano),
		input.PacketID, input.AttemptID, input.LeaseOwner, input.LeaseGeneration, input.FencingToken); err != nil {
		return ChildTaskPacket{}, fmt.Errorf("heartbeat child task attempt %s/%s: %w", input.PacketID, input.AttemptID, err)
	}
	updated, ok, err := childTaskPacketByIDTx(tx, input.PacketID)
	if err != nil {
		return ChildTaskPacket{}, err
	}
	if !ok || !childTaskAttemptOwnsPacketLease(updated, input.AttemptID, input.LeaseOwner, input.LeaseGeneration, input.FencingToken) {
		return ChildTaskPacket{}, fmt.Errorf("child task heartbeat for packet %s lost fence ownership", input.PacketID)
	}
	return updated, nil
}

func (s *SQLiteStore) ReleaseChildTaskAttempt(input ChildTaskAttemptReleaseInput) (ChildTaskPacket, error) {
	if s == nil || s.db == nil {
		return ChildTaskPacket{}, fmt.Errorf("child task store unavailable")
	}
	input = NormalizeChildTaskAttemptReleaseInput(input)
	if input.PacketID == "" || input.AttemptID == "" || input.LeaseOwner == "" || input.LeaseGeneration <= 0 || input.FencingToken == "" {
		return ChildTaskPacket{}, fmt.Errorf("child task release requires packet, attempt, owner, generation, and fencing token")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return ChildTaskPacket{}, fmt.Errorf("begin child task release tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	packet, err := releaseChildTaskAttemptTx(tx, input)
	if err != nil {
		return ChildTaskPacket{}, err
	}
	if err := tx.Commit(); err != nil {
		return ChildTaskPacket{}, fmt.Errorf("commit child task release tx: %w", err)
	}
	return packet, nil
}

func releaseChildTaskAttemptTx(tx *sql.Tx, input ChildTaskAttemptReleaseInput) (ChildTaskPacket, error) {
	input = NormalizeChildTaskAttemptReleaseInput(input)
	packet, ok, err := childTaskPacketByIDTx(tx, input.PacketID)
	if err != nil {
		return ChildTaskPacket{}, err
	}
	if !ok {
		return ChildTaskPacket{}, fmt.Errorf("child task packet %s not found", input.PacketID)
	}
	if !childTaskAttemptOwnsPacketLease(packet, input.AttemptID, input.LeaseOwner, input.LeaseGeneration, input.FencingToken) {
		return ChildTaskPacket{}, fmt.Errorf("child task release attempt %s does not own active packet lease", input.AttemptID)
	}
	if !packet.LeaseReleasedAt.IsZero() {
		return packet, nil
	}
	if _, err := tx.Exec(`
		UPDATE child_task_packets
		SET lease_released_at = ?, updated_at = ?
		WHERE packet_id = ?
			AND active_attempt_id = ?
			AND lease_owner = ?
			AND lease_generation = ?
			AND fencing_token = ?
			AND lease_released_at IS NULL
	`, input.ReleasedAt.Format(time.RFC3339Nano), input.ReleasedAt.Format(time.RFC3339Nano),
		input.PacketID, input.AttemptID, input.LeaseOwner, input.LeaseGeneration, input.FencingToken); err != nil {
		return ChildTaskPacket{}, fmt.Errorf("release child task attempt %s/%s: %w", input.PacketID, input.AttemptID, err)
	}
	released, ok, err := childTaskPacketByIDTx(tx, input.PacketID)
	if err != nil {
		return ChildTaskPacket{}, err
	}
	if !ok || released.LeaseReleasedAt.IsZero() {
		return ChildTaskPacket{}, fmt.Errorf("child task release for packet %s was not recorded", input.PacketID)
	}
	return released, nil
}

func (s *SQLiteStore) recordChildTaskResultForTest(input ChildTaskResultInput) (ChildTaskResult, error) {
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
	if input.AttemptID == "" {
		return ChildTaskResult{}, fmt.Errorf("child task result attempt_id is required")
	}
	if input.LeaseOwner == "" {
		return ChildTaskResult{}, fmt.Errorf("child task result lease_owner is required")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return ChildTaskResult{}, fmt.Errorf("begin child task result tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	result, _, err := recordChildTaskResultTx(tx, input)
	if err != nil {
		return ChildTaskResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return ChildTaskResult{}, fmt.Errorf("commit child task result tx: %w", err)
	}
	return result, nil
}

func (s *SQLiteStore) RecordChildTaskResultAndAdvance(input ChildTaskResultInput, nextAction *NextActionInput, resolvedAt time.Time) (ChildTaskResult, error) {
	if s == nil || s.db == nil {
		return ChildTaskResult{}, fmt.Errorf("child task store unavailable")
	}
	input = NormalizeChildTaskResultInput(input)
	if resolvedAt.IsZero() {
		resolvedAt = input.CreatedAt
	}
	tx, err := s.db.Begin()
	if err != nil {
		return ChildTaskResult{}, fmt.Errorf("begin child task result advancement tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	result, created, err := recordChildTaskResultTx(tx, input)
	if err != nil {
		return ChildTaskResult{}, err
	}
	if created {
		if nextAction == nil {
			if err := resolveNextActionTx(tx, NextActionResolutionInput{
				Key:         input.Key,
				Owner:       "child_task",
				SubjectKind: "task_packet",
				SubjectRef:  input.PacketID,
				Reason:      "durable_child_task_result",
				ResolvedAt:  resolvedAt,
			}); err != nil {
				return ChildTaskResult{}, err
			}
		} else {
			next := *nextAction
			next.Key = input.Key
			next.SubjectKind = "task_packet"
			next.SubjectRef = input.PacketID
			if next.CreatedAt.IsZero() {
				next.CreatedAt = resolvedAt
			}
			if _, err := recordNextActionTx(tx, next); err != nil {
				return ChildTaskResult{}, err
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return ChildTaskResult{}, fmt.Errorf("commit child task result advancement tx: %w", err)
	}
	return result, nil
}

func recordChildTaskResultTx(tx *sql.Tx, input ChildTaskResultInput) (ChildTaskResult, bool, error) {
	input = NormalizeChildTaskResultInput(input)
	if existing, ok, err := childTaskResultByIDTx(tx, input.ResultID); err != nil {
		return ChildTaskResult{}, false, err
	} else if ok {
		return existing, false, nil
	}
	packet, ok, err := childTaskPacketByIDTx(tx, input.PacketID)
	if err != nil {
		return ChildTaskResult{}, false, err
	}
	if !ok {
		return ChildTaskResult{}, false, fmt.Errorf("child task packet %s not found", input.PacketID)
	}
	if ChildTaskPacketStatusTerminal(packet.Status) {
		return ChildTaskResult{}, false, fmt.Errorf("child task packet %s is terminal (%s); stale result for attempt %s rejected", input.PacketID, packet.Status, input.AttemptID)
	}
	if !childTaskAttemptOwnsPacketLease(packet, input.AttemptID, input.LeaseOwner, input.LeaseGeneration, input.FencingToken) {
		return ChildTaskResult{}, false, fmt.Errorf("child task result attempt %s does not own active packet lease", input.AttemptID)
	}
	createdAt := input.CreatedAt.UTC()
	if !childTaskPacketLeaseLive(packet, createdAt) {
		return ChildTaskResult{}, false, fmt.Errorf("child task result attempt %s lease is not live", input.AttemptID)
	}
	if input.AgentID == "" {
		input.AgentID = packet.AgentID
	}
	if input.TaskLeaseID == "" {
		input.TaskLeaseID = packet.TaskLeaseID
	}
	sessionID := firstNonEmptyString(SessionIDForKey(input.Key), packet.SessionID)
	evidenceRefs := encodeStringList(input.EvidenceRefs)
	if _, err := tx.Exec(`
		INSERT INTO child_task_results(
			result_id, packet_id, attempt_id, lease_owner, lease_generation, fencing_token,
			task_lease_id, agent_id, session_id, status,
			result_kind, summary, blocker_kind, error_text, evidence_refs_json,
			next_state, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, input.ResultID, input.PacketID, input.AttemptID, input.LeaseOwner, input.LeaseGeneration, input.FencingToken,
		input.TaskLeaseID, input.AgentID, sessionID, string(input.Status),
		input.ResultKind, input.Summary, input.BlockerKind, input.ErrorText, evidenceRefs,
		string(input.NextState), createdAt.Format(time.RFC3339Nano)); err != nil {
		return ChildTaskResult{}, false, fmt.Errorf("insert child task result %s: %w", input.ResultID, err)
	}
	packetStatus := childTaskPacketStatusForResult(input.Status)
	if input.Status == ChildTaskResultUpdate {
		if _, err := tx.Exec(`
			UPDATE child_task_packets
			SET status = ?, result_id = ?, lease_released_at = ?, updated_at = ?, terminal_at = NULL
			WHERE packet_id = ?
				AND active_attempt_id = ?
				AND lease_owner = ?
				AND lease_generation = ?
				AND fencing_token = ?
				AND lease_released_at IS NULL
				AND status NOT IN (?, ?, ?, ?, ?)
		`, string(packetStatus), input.ResultID, createdAt.Format(time.RFC3339Nano), createdAt.Format(time.RFC3339Nano), input.PacketID,
			input.AttemptID, input.LeaseOwner, input.LeaseGeneration, input.FencingToken,
			string(ChildTaskPacketCompleted), string(ChildTaskPacketBlocked), string(ChildTaskPacketFailed), string(ChildTaskPacketRevoked), string(ChildTaskPacketExpired)); err != nil {
			return ChildTaskResult{}, false, fmt.Errorf("update child task packet nonterminal state: %w", err)
		}
	} else {
		if _, err := tx.Exec(`
			UPDATE child_task_packets
			SET status = ?, result_id = ?, lease_released_at = ?, updated_at = ?, terminal_at = ?
			WHERE packet_id = ?
				AND active_attempt_id = ?
				AND lease_owner = ?
				AND lease_generation = ?
				AND fencing_token = ?
				AND lease_released_at IS NULL
				AND status NOT IN (?, ?, ?, ?, ?)
		`, string(packetStatus), input.ResultID, createdAt.Format(time.RFC3339Nano), createdAt.Format(time.RFC3339Nano), createdAt.Format(time.RFC3339Nano), input.PacketID,
			input.AttemptID, input.LeaseOwner, input.LeaseGeneration, input.FencingToken,
			string(ChildTaskPacketCompleted), string(ChildTaskPacketBlocked), string(ChildTaskPacketFailed), string(ChildTaskPacketRevoked), string(ChildTaskPacketExpired)); err != nil {
			return ChildTaskResult{}, false, fmt.Errorf("update child task packet terminal state: %w", err)
		}
	}
	updatedPacket, ok, err := childTaskPacketByIDTx(tx, input.PacketID)
	if err != nil {
		return ChildTaskResult{}, false, err
	}
	if !ok || updatedPacket.ResultID != input.ResultID {
		return ChildTaskResult{}, false, fmt.Errorf("child task packet %s did not accept result %s", input.PacketID, input.ResultID)
	}
	payloadRaw, _ := json.Marshal(map[string]any{
		"result_id":        input.ResultID,
		"packet_id":        input.PacketID,
		"attempt_id":       input.AttemptID,
		"lease_owner":      input.LeaseOwner,
		"lease_generation": input.LeaseGeneration,
		"task_lease_id":    input.TaskLeaseID,
		"agent_id":         input.AgentID,
		"status":           string(input.Status),
		"result_kind":      input.ResultKind,
		"blocker_kind":     input.BlockerKind,
		"evidence_refs":    input.EvidenceRefs,
		"next_state":       string(input.NextState),
	})
	if _, err := appendExecutionEventsTx(tx, input.Key, []ExecutionEventInput{{
		EventType:   core.ExecutionEventDurableChildTaskResult,
		Stage:       "child_task",
		Status:      string(input.Status),
		PayloadJSON: string(payloadRaw),
		CreatedAt:   createdAt,
	}}); err != nil {
		return ChildTaskResult{}, false, fmt.Errorf("append child task result event: %w", err)
	}
	result, ok, err := childTaskResultByIDTx(tx, input.ResultID)
	if err != nil {
		return ChildTaskResult{}, false, err
	}
	if !ok {
		return ChildTaskResult{}, false, fmt.Errorf("child task result %s not found after insert", input.ResultID)
	}
	return result, true, nil
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
			input_json, active_attempt_id, lease_owner, lease_generation, fencing_token,
			lease_expires_at, lease_heartbeat_at, lease_released_at, result_id,
			created_at, updated_at, terminal_at
		FROM child_task_packets
	`
}

func childTaskResultSelectSQL() string {
	return `
		SELECT result_id, packet_id, attempt_id, lease_owner, lease_generation, fencing_token,
			task_lease_id, agent_id, session_id, status,
			result_kind, summary, blocker_kind, error_text, evidence_refs_json,
			next_state, created_at
		FROM child_task_results
	`
}

func scanChildTaskPacket(scanner interface{ Scan(dest ...any) error }) (ChildTaskPacket, error) {
	var (
		packet              ChildTaskPacket
		scopeKindRaw        string
		scopeIDRaw          string
		durableAgentIDRaw   string
		statusRaw           string
		createdAtRaw        string
		updatedAtRaw        string
		leaseExpiresAtRaw   string
		leaseHeartbeatAtRaw string
		leaseReleasedAtRaw  sql.NullString
		terminalAtRaw       sql.NullString
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
		&packet.ActiveAttemptID,
		&packet.LeaseOwner,
		&packet.LeaseGeneration,
		&packet.FencingToken,
		&leaseExpiresAtRaw,
		&leaseHeartbeatAtRaw,
		&leaseReleasedAtRaw,
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
	if leaseExpiresAt, err := parseOptionalSQLiteTime(leaseExpiresAtRaw); err != nil {
		return ChildTaskPacket{}, fmt.Errorf("parse child task packet lease_expires_at: %w", err)
	} else {
		packet.LeaseExpiresAt = leaseExpiresAt
	}
	if leaseHeartbeatAt, err := parseOptionalSQLiteTime(leaseHeartbeatAtRaw); err != nil {
		return ChildTaskPacket{}, fmt.Errorf("parse child task packet lease_heartbeat_at: %w", err)
	} else {
		packet.LeaseHeartbeatAt = leaseHeartbeatAt
	}
	if leaseReleasedAtRaw.Valid {
		if leaseReleasedAt, err := parseOptionalSQLiteTime(leaseReleasedAtRaw.String); err != nil {
			return ChildTaskPacket{}, fmt.Errorf("parse child task packet lease_released_at: %w", err)
		} else {
			packet.LeaseReleasedAt = leaseReleasedAt
		}
	}
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
		&result.AttemptID,
		&result.LeaseOwner,
		&result.LeaseGeneration,
		&result.FencingToken,
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

func childTaskPacketLeaseLive(packet ChildTaskPacket, at time.Time) bool {
	if packet.ActiveAttemptID == "" || packet.LeaseOwner == "" || packet.LeaseGeneration <= 0 || packet.FencingToken == "" {
		return false
	}
	if !packet.LeaseReleasedAt.IsZero() || packet.LeaseExpiresAt.IsZero() {
		return false
	}
	if at.IsZero() {
		at = time.Now().UTC()
	} else {
		at = at.UTC()
	}
	return packet.LeaseExpiresAt.After(at)
}

func childTaskAttemptOwnsPacketLease(packet ChildTaskPacket, attemptID string, leaseOwner string, leaseGeneration int64, fencingToken string) bool {
	return strings.TrimSpace(attemptID) != "" &&
		strings.TrimSpace(leaseOwner) != "" &&
		strings.TrimSpace(fencingToken) != "" &&
		packet.ActiveAttemptID == strings.TrimSpace(attemptID) &&
		packet.LeaseOwner == strings.TrimSpace(leaseOwner) &&
		packet.LeaseGeneration == leaseGeneration &&
		packet.FencingToken == strings.TrimSpace(fencingToken)
}

func parseOptionalSQLiteTime(raw string) (time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, nil
	}
	return parseSQLiteTime(raw)
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
