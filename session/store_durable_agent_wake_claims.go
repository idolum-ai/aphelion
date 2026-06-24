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

type DurableAgentWakeClaimInput struct {
	LeaseID          string
	AgentID          string
	TurnRunID        int64
	MessageBatchHash string
	MessageIDs       []string
	CreatedAt        time.Time
}

type DurableAgentWakeClaim struct {
	ClaimID          string
	LeaseID          string
	AgentID          string
	TurnRunID        int64
	MessageBatchHash string
	MessageIDs       []string
	CreatedAt        time.Time
}

func NormalizeDurableAgentWakeClaimInput(input DurableAgentWakeClaimInput) DurableAgentWakeClaimInput {
	input.LeaseID = strings.TrimSpace(input.LeaseID)
	input.AgentID = strings.TrimSpace(input.AgentID)
	input.MessageBatchHash = strings.TrimSpace(input.MessageBatchHash)
	input.MessageIDs = normalizeStringSet(input.MessageIDs)
	if input.CreatedAt.IsZero() {
		input.CreatedAt = time.Now().UTC()
	} else {
		input.CreatedAt = input.CreatedAt.UTC()
	}
	return input
}

func DurableAgentWakeClaimID(input DurableAgentWakeClaimInput) string {
	input = NormalizeDurableAgentWakeClaimInput(input)
	return "wake_claim:" + EffectAttemptCommandHash(strings.Join([]string{
		input.LeaseID,
		input.AgentID,
		input.MessageBatchHash,
	}, "\x00"))[7:31]
}

func DurableAgentWakeMessageBatchHash(agentID string, messageIDs []string) string {
	messageIDs = normalizeStringSet(messageIDs)
	return EffectAttemptCommandHash(strings.Join(append([]string{strings.TrimSpace(agentID)}, messageIDs...), "\x00"))
}

func (s *SQLiteStore) ClaimDurableAgentWakeOnce(input DurableAgentWakeClaimInput) (DurableAgentWakeClaim, error) {
	input = NormalizeDurableAgentWakeClaimInput(input)
	if input.LeaseID == "" {
		return DurableAgentWakeClaim{}, fmt.Errorf("durable agent wake claim lease_id is required")
	}
	if input.AgentID == "" {
		return DurableAgentWakeClaim{}, fmt.Errorf("durable agent wake claim agent_id is required")
	}
	if input.TurnRunID <= 0 {
		return DurableAgentWakeClaim{}, fmt.Errorf("durable agent wake claim turn_run_id is required")
	}
	if input.MessageBatchHash == "" || len(input.MessageIDs) == 0 {
		return DurableAgentWakeClaim{}, fmt.Errorf("durable agent wake claim message batch is required")
	}
	claimID := DurableAgentWakeClaimID(input)
	messageIDsJSON, err := json.Marshal(input.MessageIDs)
	if err != nil {
		return DurableAgentWakeClaim{}, fmt.Errorf("marshal durable agent wake message ids: %w", err)
	}
	_, err = s.db.Exec(`
		INSERT INTO durable_agent_wake_claims(
			claim_id, lease_id, agent_id, turn_run_id, message_batch_hash, message_ids_json, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?)
	`, claimID, input.LeaseID, input.AgentID, input.TurnRunID, input.MessageBatchHash, string(messageIDsJSON), input.CreatedAt.Format(time.RFC3339Nano))
	if err != nil {
		if durableAgentWakeClaimConflict(s.db, input.LeaseID) {
			return DurableAgentWakeClaim{}, fmt.Errorf("durable agent wake lease %q already claimed", input.LeaseID)
		}
		return DurableAgentWakeClaim{}, fmt.Errorf("claim durable agent wake: %w", err)
	}
	return DurableAgentWakeClaim{
		ClaimID:          claimID,
		LeaseID:          input.LeaseID,
		AgentID:          input.AgentID,
		TurnRunID:        input.TurnRunID,
		MessageBatchHash: input.MessageBatchHash,
		MessageIDs:       input.MessageIDs,
		CreatedAt:        input.CreatedAt,
	}, nil
}

func durableAgentWakeClaimConflict(queryer interface {
	QueryRow(query string, args ...any) *sql.Row
}, leaseID string) bool {
	leaseID = strings.TrimSpace(leaseID)
	if leaseID == "" {
		return false
	}
	var existing string
	if err := queryer.QueryRow(`
		SELECT claim_id
		FROM durable_agent_wake_claims
		WHERE lease_id = ?
		LIMIT 1
	`, leaseID).Scan(&existing); err != nil {
		return !errors.Is(err, sql.ErrNoRows)
	}
	return strings.TrimSpace(existing) != ""
}
