//go:build linux

package session

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
)

func (s *SQLiteStore) SaveDurableAgentState(state core.DurableAgentState) error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin save durable agent state tx: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()
	if err := saveDurableAgentRuntimeStateExec(tx, core.DurableAgentRuntimeStateFrom(state)); err != nil {
		return err
	}
	if err := saveDurableAgentIdentityStateExec(tx, core.DurableAgentIdentityStateFrom(state)); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit save durable agent state tx: %w", err)
	}
	return nil
}

func (s *SQLiteStore) SaveDurableAgentRuntimeState(state core.DurableAgentRuntimeState) error {
	return saveDurableAgentRuntimeStateExec(s.db, state)
}

func (s *SQLiteStore) TryMarkDurableAgentAwake(agentID string, cursor string, now time.Time, staleAfter time.Duration) (bool, error) {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return false, fmt.Errorf("mark durable agent awake: agent_id is required")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	if staleAfter <= 0 {
		staleAfter = 30 * time.Minute
	}
	cutoff := now.Add(-staleAfter).UTC().Format(time.RFC3339Nano)
	nowRaw := now.Format(time.RFC3339Nano)

	result, err := s.db.Exec(`
		UPDATE durable_agent_state
		SET cursor = ?, status = 'awake', last_wake_at = ?, dormant_at = NULL, updated_at = ?
		WHERE agent_id = ?
		  AND (
			COALESCE(status, '') <> 'awake'
			OR COALESCE(last_wake_at, updated_at, '') = ''
			OR COALESCE(last_wake_at, updated_at) <= ?
		  )
	`, nullableString(cursor), nowRaw, nowRaw, agentID, cutoff)
	if err != nil {
		return false, fmt.Errorf("mark durable agent awake: %w", err)
	}
	if rows, _ := result.RowsAffected(); rows > 0 {
		return true, nil
	}

	result, err = s.db.Exec(`
		INSERT INTO durable_agent_state(agent_id, cursor, status, state_json, last_wake_at, dormant_at, updated_at)
		VALUES (?, ?, 'awake', '', ?, NULL, ?)
		ON CONFLICT(agent_id) DO NOTHING
	`, agentID, nullableString(cursor), nowRaw, nowRaw)
	if err != nil {
		return false, fmt.Errorf("mark durable agent awake: %w", err)
	}
	rows, _ := result.RowsAffected()
	return rows > 0, nil
}

func (s *SQLiteStore) DurableAgentReviewEventCountSince(agentID string, since time.Time) (int, error) {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" || since.IsZero() {
		return 0, nil
	}
	var count int
	if err := s.db.QueryRow(`
		SELECT COUNT(1)
		FROM review_events
		WHERE source_durable_agent_id = ?
		  AND created_at >= ?
	`, agentID, since.UTC().Format(time.RFC3339Nano)).Scan(&count); err != nil {
		return 0, fmt.Errorf("count durable agent review events: %w", err)
	}
	return count, nil
}

func saveDurableAgentRuntimeStateExec(exec sqlExecer, state core.DurableAgentRuntimeState) error {
	state.AgentID = strings.TrimSpace(state.AgentID)
	if state.AgentID == "" {
		return fmt.Errorf("save durable agent runtime state: agent_id is required")
	}
	now := time.Now().UTC()
	_, err := exec.Exec(`
		INSERT INTO durable_agent_state(
			agent_id, cursor, status, state_json,
			last_apply_status, last_apply_error,
			last_wake_at, last_review_at, dormant_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(agent_id) DO UPDATE SET
			cursor = excluded.cursor,
			status = excluded.status,
			state_json = excluded.state_json,
			last_apply_status = excluded.last_apply_status,
			last_apply_error = excluded.last_apply_error,
			last_wake_at = excluded.last_wake_at,
			last_review_at = excluded.last_review_at,
			dormant_at = excluded.dormant_at,
			updated_at = excluded.updated_at
	`,
		state.AgentID, nullableString(state.Cursor), nullableString(state.Status), nullableString(state.StateJSON),
		strings.TrimSpace(state.LastApplyStatus), strings.TrimSpace(state.LastApplyError),
		nullableTime(state.LastWakeAt), nullableTime(state.LastReviewAt), nullableTime(state.DormantAt), now.Format(time.RFC3339Nano),
	)
	if err != nil {
		return fmt.Errorf("save durable agent runtime state: %w", err)
	}
	return nil
}

func (s *SQLiteStore) SaveDurableAgentIdentityState(state core.DurableAgentIdentityState) error {
	return saveDurableAgentIdentityStateExec(s.db, state)
}

func saveDurableAgentIdentityStateExec(exec sqlExecer, state core.DurableAgentIdentityState) error {
	state.AgentID = strings.TrimSpace(state.AgentID)
	if state.AgentID == "" {
		return fmt.Errorf("save durable agent identity state: agent_id is required")
	}
	now := time.Now().UTC()
	_, err := exec.Exec(`
		INSERT INTO durable_agent_identity_state(
			agent_id,
			last_offered_policy_version, last_offered_policy_hash, last_offered_policy_at,
			last_acknowledged_policy_version, last_acknowledged_policy_hash, last_acknowledged_policy_at,
			last_applied_policy_version, last_applied_policy_hash, last_applied_policy_at,
			updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(agent_id) DO UPDATE SET
			last_offered_policy_version = excluded.last_offered_policy_version,
			last_offered_policy_hash = excluded.last_offered_policy_hash,
			last_offered_policy_at = excluded.last_offered_policy_at,
			last_acknowledged_policy_version = excluded.last_acknowledged_policy_version,
			last_acknowledged_policy_hash = excluded.last_acknowledged_policy_hash,
			last_acknowledged_policy_at = excluded.last_acknowledged_policy_at,
			last_applied_policy_version = excluded.last_applied_policy_version,
			last_applied_policy_hash = excluded.last_applied_policy_hash,
			last_applied_policy_at = excluded.last_applied_policy_at,
			updated_at = excluded.updated_at
	`,
		state.AgentID,
		state.LastOfferedPolicyVersion, strings.TrimSpace(state.LastOfferedPolicyHash), nullableTime(state.LastOfferedPolicyAt),
		state.LastAcknowledgedPolicyVersion, strings.TrimSpace(state.LastAcknowledgedPolicyHash), nullableTime(state.LastAcknowledgedPolicyAt),
		state.LastAppliedPolicyVersion, strings.TrimSpace(state.LastAppliedPolicyHash), nullableTime(state.LastAppliedPolicyAt),
		now.Format(time.RFC3339Nano),
	)
	if err != nil {
		return fmt.Errorf("save durable agent identity state: %w", err)
	}
	return nil
}

func (s *SQLiteStore) DurableAgentRuntimeState(agentID string) (*core.DurableAgentRuntimeState, error) {
	return queryDurableAgentRuntimeState(s.db, agentID)
}

func queryDurableAgentRuntimeState(queryer sqlQueryer, agentID string) (*core.DurableAgentRuntimeState, error) {
	rows, err := queryer.Query(`
		SELECT
			agent_id, cursor, status, state_json,
			last_apply_status, last_apply_error,
			last_wake_at, last_review_at, dormant_at, updated_at
		FROM durable_agent_state
		WHERE agent_id = ?
	`, strings.TrimSpace(agentID))
	if err != nil {
		return nil, fmt.Errorf("query durable agent runtime state: %w", err)
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, sql.ErrNoRows
	}
	state, err := scanDurableAgentRuntimeState(rows)
	if err != nil {
		return nil, err
	}
	return &state, nil
}

func (s *SQLiteStore) DurableAgentIdentityState(agentID string) (*core.DurableAgentIdentityState, error) {
	return queryDurableAgentIdentityState(s.db, agentID)
}

func queryDurableAgentIdentityState(queryer sqlQueryer, agentID string) (*core.DurableAgentIdentityState, error) {
	rows, err := queryer.Query(`
		SELECT
			agent_id,
			last_offered_policy_version, last_offered_policy_hash, last_offered_policy_at,
			last_acknowledged_policy_version, last_acknowledged_policy_hash, last_acknowledged_policy_at,
			last_applied_policy_version, last_applied_policy_hash, last_applied_policy_at,
			updated_at
		FROM durable_agent_identity_state
		WHERE agent_id = ?
	`, strings.TrimSpace(agentID))
	if err != nil {
		return nil, fmt.Errorf("query durable agent identity state: %w", err)
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, sql.ErrNoRows
	}
	state, err := scanDurableAgentIdentityState(rows)
	if err != nil {
		return nil, err
	}
	return &state, nil
}

func (s *SQLiteStore) DurableAgentState(agentID string) (*core.DurableAgentState, error) {
	return queryDurableAgentState(s.db, agentID)
}

func queryDurableAgentState(queryer sqlQueryer, agentID string) (*core.DurableAgentState, error) {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return nil, sql.ErrNoRows
	}

	runtimeState, runtimeErr := queryDurableAgentRuntimeState(queryer, agentID)
	if runtimeErr != nil && !errors.Is(runtimeErr, sql.ErrNoRows) {
		return nil, runtimeErr
	}
	identityState, identityErr := queryDurableAgentIdentityState(queryer, agentID)
	if identityErr != nil && !errors.Is(identityErr, sql.ErrNoRows) {
		return nil, identityErr
	}
	if errors.Is(runtimeErr, sql.ErrNoRows) && errors.Is(identityErr, sql.ErrNoRows) {
		return nil, sql.ErrNoRows
	}

	state := core.DurableAgentState{AgentID: agentID}
	if runtimeState != nil {
		state.Cursor = runtimeState.Cursor
		state.Status = runtimeState.Status
		state.StateJSON = runtimeState.StateJSON
		state.LastApplyStatus = runtimeState.LastApplyStatus
		state.LastApplyError = runtimeState.LastApplyError
		state.LastWakeAt = runtimeState.LastWakeAt
		state.LastReviewAt = runtimeState.LastReviewAt
		state.DormantAt = runtimeState.DormantAt
		state.UpdatedAt = runtimeState.UpdatedAt
	}
	if identityState != nil {
		state.LastOfferedPolicyVersion = identityState.LastOfferedPolicyVersion
		state.LastOfferedPolicyHash = identityState.LastOfferedPolicyHash
		state.LastOfferedPolicyAt = identityState.LastOfferedPolicyAt
		state.LastAcknowledgedPolicyVersion = identityState.LastAcknowledgedPolicyVersion
		state.LastAcknowledgedPolicyHash = identityState.LastAcknowledgedPolicyHash
		state.LastAcknowledgedPolicyAt = identityState.LastAcknowledgedPolicyAt
		state.LastAppliedPolicyVersion = identityState.LastAppliedPolicyVersion
		state.LastAppliedPolicyHash = identityState.LastAppliedPolicyHash
		state.LastAppliedPolicyAt = identityState.LastAppliedPolicyAt
		if state.UpdatedAt.IsZero() || (!identityState.UpdatedAt.IsZero() && identityState.UpdatedAt.After(state.UpdatedAt)) {
			state.UpdatedAt = identityState.UpdatedAt
		}
	}
	return &state, nil
}

func scanDurableAgentRuntimeState(scanner interface{ Scan(dest ...any) error }) (core.DurableAgentRuntimeState, error) {
	var (
		state         core.DurableAgentRuntimeState
		cursorRaw     sql.NullString
		statusRaw     sql.NullString
		stateJSONRaw  sql.NullString
		lastStatusRaw sql.NullString
		lastErrorRaw  sql.NullString
		lastWakeAtRaw sql.NullString
		lastReviewRaw sql.NullString
		dormantAtRaw  sql.NullString
		updatedAtRaw  string
	)
	if err := scanner.Scan(
		&state.AgentID, &cursorRaw, &statusRaw, &stateJSONRaw,
		&lastStatusRaw, &lastErrorRaw,
		&lastWakeAtRaw, &lastReviewRaw, &dormantAtRaw, &updatedAtRaw,
	); err != nil {
		return core.DurableAgentRuntimeState{}, fmt.Errorf("scan durable agent runtime state: %w", err)
	}
	state.Cursor = nullToString(cursorRaw)
	state.Status = nullToString(statusRaw)
	state.StateJSON = nullToString(stateJSONRaw)
	state.LastApplyStatus = nullToString(lastStatusRaw)
	state.LastApplyError = nullToString(lastErrorRaw)
	var err error
	if lastWakeAtRaw.Valid && lastWakeAtRaw.String != "" {
		state.LastWakeAt, err = parseSQLiteTime(lastWakeAtRaw.String)
		if err != nil {
			return core.DurableAgentRuntimeState{}, fmt.Errorf("parse durable agent runtime last_wake_at: %w", err)
		}
	}
	if lastReviewRaw.Valid && lastReviewRaw.String != "" {
		state.LastReviewAt, err = parseSQLiteTime(lastReviewRaw.String)
		if err != nil {
			return core.DurableAgentRuntimeState{}, fmt.Errorf("parse durable agent runtime last_review_at: %w", err)
		}
	}
	if dormantAtRaw.Valid && dormantAtRaw.String != "" {
		state.DormantAt, err = parseSQLiteTime(dormantAtRaw.String)
		if err != nil {
			return core.DurableAgentRuntimeState{}, fmt.Errorf("parse durable agent runtime dormant_at: %w", err)
		}
	}
	state.UpdatedAt, err = parseSQLiteTime(updatedAtRaw)
	if err != nil {
		return core.DurableAgentRuntimeState{}, fmt.Errorf("parse durable agent runtime updated_at: %w", err)
	}
	return state, nil
}

func scanDurableAgentIdentityState(scanner interface{ Scan(dest ...any) error }) (core.DurableAgentIdentityState, error) {
	var (
		state                         core.DurableAgentIdentityState
		lastOfferedPolicyHashRaw      sql.NullString
		lastOfferedPolicyAtRaw        sql.NullString
		lastAcknowledgedPolicyHashRaw sql.NullString
		lastAcknowledgedPolicyAtRaw   sql.NullString
		lastAppliedPolicyHashRaw      sql.NullString
		lastAppliedPolicyAtRaw        sql.NullString
		updatedAtRaw                  string
	)
	if err := scanner.Scan(
		&state.AgentID,
		&state.LastOfferedPolicyVersion, &lastOfferedPolicyHashRaw, &lastOfferedPolicyAtRaw,
		&state.LastAcknowledgedPolicyVersion, &lastAcknowledgedPolicyHashRaw, &lastAcknowledgedPolicyAtRaw,
		&state.LastAppliedPolicyVersion, &lastAppliedPolicyHashRaw, &lastAppliedPolicyAtRaw,
		&updatedAtRaw,
	); err != nil {
		return core.DurableAgentIdentityState{}, fmt.Errorf("scan durable agent identity state: %w", err)
	}
	state.LastOfferedPolicyHash = nullToString(lastOfferedPolicyHashRaw)
	state.LastAcknowledgedPolicyHash = nullToString(lastAcknowledgedPolicyHashRaw)
	state.LastAppliedPolicyHash = nullToString(lastAppliedPolicyHashRaw)
	var err error
	if lastOfferedPolicyAtRaw.Valid && lastOfferedPolicyAtRaw.String != "" {
		state.LastOfferedPolicyAt, err = parseSQLiteTime(lastOfferedPolicyAtRaw.String)
		if err != nil {
			return core.DurableAgentIdentityState{}, fmt.Errorf("parse durable agent identity last_offered_policy_at: %w", err)
		}
	}
	if lastAcknowledgedPolicyAtRaw.Valid && lastAcknowledgedPolicyAtRaw.String != "" {
		state.LastAcknowledgedPolicyAt, err = parseSQLiteTime(lastAcknowledgedPolicyAtRaw.String)
		if err != nil {
			return core.DurableAgentIdentityState{}, fmt.Errorf("parse durable agent identity last_acknowledged_policy_at: %w", err)
		}
	}
	if lastAppliedPolicyAtRaw.Valid && lastAppliedPolicyAtRaw.String != "" {
		state.LastAppliedPolicyAt, err = parseSQLiteTime(lastAppliedPolicyAtRaw.String)
		if err != nil {
			return core.DurableAgentIdentityState{}, fmt.Errorf("parse durable agent identity last_applied_policy_at: %w", err)
		}
	}
	state.UpdatedAt, err = parseSQLiteTime(updatedAtRaw)
	if err != nil {
		return core.DurableAgentIdentityState{}, fmt.Errorf("parse durable agent identity updated_at: %w", err)
	}
	return state, nil
}
