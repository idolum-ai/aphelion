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

func (s *SQLiteStore) RecordNextAction(input NextActionInput) (NextActionRecord, error) {
	if s == nil || s.db == nil {
		return NextActionRecord{}, fmt.Errorf("next action store unavailable")
	}
	input = NormalizeNextActionInput(input)
	if input.RecordID == "" {
		return NextActionRecord{}, fmt.Errorf("next action record_id is required")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return NextActionRecord{}, fmt.Errorf("begin next action tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	record, err := recordNextActionTx(tx, input)
	if err != nil {
		return NextActionRecord{}, err
	}
	if err := tx.Commit(); err != nil {
		return NextActionRecord{}, fmt.Errorf("commit next action tx: %w", err)
	}
	return record, nil
}

func recordNextActionTx(tx *sql.Tx, input NextActionInput) (NextActionRecord, error) {
	input = NormalizeNextActionInput(input)
	scope := defaultScopeForKey(input.Key)
	sessionID := SessionIDForKey(input.Key)
	createdAt := input.CreatedAt.UTC()
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	if existing, ok, err := nextActionByRecordIDTx(tx, input.RecordID); err != nil {
		return NextActionRecord{}, err
	} else if ok {
		return existing, nil
	}
	if _, err := tx.Exec(`
		UPDATE next_action_records
		SET resolved_at = ?
		WHERE session_id = ?
			AND subject_kind = ?
			AND subject_ref = ?
			AND resolved_at IS NULL
	`, createdAt.Format(time.RFC3339Nano), sessionID, input.SubjectKind, input.SubjectRef); err != nil {
		return NextActionRecord{}, fmt.Errorf("resolve prior next actions: %w", err)
	}
	causalRefs := encodeStringList(input.CausalRefs)
	if _, err := tx.Exec(`
		INSERT INTO next_action_records(
			record_id, session_id, chat_id, user_id, scope_kind, scope_id, durable_agent_id,
			turn_run_id, owner, state, subject_kind, subject_ref, causal_refs_json,
			next_action, required_authority, resource_blocker, verifier, retry_policy,
			operator_projection, created_at, resolved_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, NULL)
	`, input.RecordID, sessionID, input.Key.ChatID, input.Key.UserID, string(scope.Kind), scope.ID, scope.DurableAgentID,
		input.TurnRunID, input.Owner, string(input.State), input.SubjectKind, input.SubjectRef, causalRefs,
		input.NextAction, input.RequiredAuthority, input.ResourceBlocker, input.Verifier, input.RetryPolicy,
		input.OperatorProjection, createdAt.Format(time.RFC3339Nano)); err != nil {
		return NextActionRecord{}, fmt.Errorf("insert next action %s: %w", input.RecordID, err)
	}
	payloadRaw, _ := json.Marshal(map[string]any{
		"record_id":           input.RecordID,
		"turn_run_id":         input.TurnRunID,
		"owner":               input.Owner,
		"state":               string(input.State),
		"subject_kind":        input.SubjectKind,
		"subject_ref":         input.SubjectRef,
		"causal_refs":         input.CausalRefs,
		"next_action":         input.NextAction,
		"required_authority":  input.RequiredAuthority,
		"resource_blocker":    input.ResourceBlocker,
		"verifier":            input.Verifier,
		"retry_policy":        input.RetryPolicy,
		"operator_projection": input.OperatorProjection,
	})
	if _, err := appendExecutionEventsTx(tx, input.Key, []ExecutionEventInput{{
		EventType:   core.ExecutionEventWorkflowNextState,
		Stage:       input.SubjectKind,
		Status:      string(input.State),
		PayloadJSON: string(payloadRaw),
		CreatedAt:   createdAt,
	}}); err != nil {
		return NextActionRecord{}, fmt.Errorf("append next action event: %w", err)
	}
	return NextActionRecord{
		RecordID:           input.RecordID,
		SessionID:          sessionID,
		ChatID:             input.Key.ChatID,
		UserID:             input.Key.UserID,
		Scope:              scope,
		TurnRunID:          input.TurnRunID,
		Owner:              input.Owner,
		State:              input.State,
		SubjectKind:        input.SubjectKind,
		SubjectRef:         input.SubjectRef,
		CausalRefs:         input.CausalRefs,
		NextAction:         input.NextAction,
		RequiredAuthority:  input.RequiredAuthority,
		ResourceBlocker:    input.ResourceBlocker,
		Verifier:           input.Verifier,
		RetryPolicy:        input.RetryPolicy,
		OperatorProjection: input.OperatorProjection,
		CreatedAt:          createdAt,
	}, nil
}

func (s *SQLiteStore) ResolveNextAction(input NextActionResolutionInput) error {
	if s == nil || s.db == nil {
		return nil
	}
	input = NormalizeNextActionResolutionInput(input)
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin next action resolution tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := resolveNextActionTx(tx, input); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit next action resolution tx: %w", err)
	}
	return nil
}

func resolveNextActionTx(tx *sql.Tx, input NextActionResolutionInput) error {
	input = NormalizeNextActionResolutionInput(input)
	sessionID := SessionIDForKey(input.Key)
	resolvedAt := input.ResolvedAt.UTC()
	if resolvedAt.IsZero() {
		resolvedAt = time.Now().UTC()
	}
	result, err := tx.Exec(`
		UPDATE next_action_records
		SET resolved_at = ?
		WHERE session_id = ?
			AND subject_kind = ?
			AND subject_ref = ?
			AND resolved_at IS NULL
	`, resolvedAt.Format(time.RFC3339Nano), sessionID, input.SubjectKind, input.SubjectRef)
	if err != nil {
		return fmt.Errorf("resolve next action %s/%s: %w", input.SubjectKind, input.SubjectRef, err)
	}
	changed, _ := result.RowsAffected()
	if changed == 0 {
		return nil
	}
	payloadRaw, _ := json.Marshal(map[string]any{
		"owner":        input.Owner,
		"state":        string(NextActionTerminal),
		"subject_kind": input.SubjectKind,
		"subject_ref":  input.SubjectRef,
		"reason":       input.Reason,
	})
	if _, err := appendExecutionEventsTx(tx, input.Key, []ExecutionEventInput{{
		EventType:   core.ExecutionEventWorkflowNextState,
		Stage:       input.SubjectKind,
		Status:      string(NextActionTerminal),
		PayloadJSON: string(payloadRaw),
		CreatedAt:   resolvedAt,
	}}); err != nil {
		return fmt.Errorf("append next action resolution event: %w", err)
	}
	return nil
}

func (s *SQLiteStore) OpenNextActionsBySession(key SessionKey, limit int) ([]NextActionRecord, error) {
	if s == nil || s.db == nil {
		return nil, nil
	}
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.Query(`
		SELECT record_id, session_id, chat_id, user_id, scope_kind, scope_id, durable_agent_id,
			turn_run_id, owner, state, subject_kind, subject_ref, causal_refs_json,
			next_action, required_authority, resource_blocker, verifier, retry_policy,
			operator_projection, created_at, resolved_at
		FROM next_action_records
		WHERE session_id = ? AND resolved_at IS NULL
		ORDER BY created_at DESC, record_id DESC
		LIMIT ?
	`, SessionIDForKey(key), limit)
	if err != nil {
		return nil, fmt.Errorf("query open next actions: %w", err)
	}
	defer rows.Close()
	return scanNextActionRows(rows)
}

func nextActionByRecordIDTx(tx *sql.Tx, recordID string) (NextActionRecord, bool, error) {
	recordID = strings.TrimSpace(recordID)
	if recordID == "" {
		return NextActionRecord{}, false, nil
	}
	row := tx.QueryRow(`
		SELECT record_id, session_id, chat_id, user_id, scope_kind, scope_id, durable_agent_id,
			turn_run_id, owner, state, subject_kind, subject_ref, causal_refs_json,
			next_action, required_authority, resource_blocker, verifier, retry_policy,
			operator_projection, created_at, resolved_at
		FROM next_action_records
		WHERE record_id = ?
	`, recordID)
	record, err := scanNextActionRecord(row)
	if errors.Is(err, sql.ErrNoRows) {
		return NextActionRecord{}, false, nil
	}
	if err != nil {
		return NextActionRecord{}, false, err
	}
	return record, true, nil
}

func (s *SQLiteStore) RecordResourcePreflight(key SessionKey, turnRunID int64, resource string, reason string, message string, at time.Time) error {
	if s == nil || s.db == nil {
		return nil
	}
	reason = normalizeEnumValue(reason)
	if reason == "" {
		reason = "resource_denied"
	}
	resource = strings.TrimSpace(resource)
	if resource == "" {
		resource = "resource"
	}
	message = strings.TrimSpace(message)
	if message == "" {
		message = "resource preflight denied the requested operation"
	}
	if at.IsZero() {
		at = time.Now().UTC()
	}
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin resource preflight tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	payloadRaw, _ := json.Marshal(map[string]any{
		"turn_run_id": turnRunID,
		"resource":    resource,
		"reason":      reason,
		"message":     message,
	})
	if _, err := appendExecutionEventsTx(tx, key, []ExecutionEventInput{{
		EventType:   core.ExecutionEventResourcePreflight,
		Stage:       "resource",
		Status:      reason,
		PayloadJSON: string(payloadRaw),
		CreatedAt:   at,
	}}); err != nil {
		return err
	}
	if _, err := recordNextActionTx(tx, NextActionInput{
		Key:                key,
		TurnRunID:          turnRunID,
		Owner:              "resource_preflight",
		State:              NextActionBlockedNeedsResourceRepair,
		SubjectKind:        "resource",
		SubjectRef:         resource,
		CausalRefs:         []string{"resource_preflight:" + reason},
		NextAction:         "repair the resource boundary before retrying",
		ResourceBlocker:    reason,
		RetryPolicy:        "retry_after_resource_repair",
		OperatorProjection: message,
		CreatedAt:          at,
	}); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit resource preflight tx: %w", err)
	}
	return nil
}

func (s *SQLiteStore) RecordPersistenceLatencyClassification(key SessionKey, component string, latency time.Duration, at time.Time) error {
	if s == nil || s.db == nil {
		return nil
	}
	component = strings.TrimSpace(component)
	if component == "" {
		component = "persistence"
	}
	if at.IsZero() {
		at = time.Now().UTC()
	}
	classification := "normal"
	retryPolicy := "none"
	if latency >= 250*time.Millisecond {
		classification = "slow_write"
		retryPolicy = "batch_or_backpressure"
	}
	payloadRaw, _ := json.Marshal(map[string]any{
		"component":      component,
		"latency_ms":     latency.Milliseconds(),
		"classification": classification,
		"retry_policy":   retryPolicy,
	})
	_, err := s.AppendExecutionEvent(key, ExecutionEventInput{
		EventType:   core.ExecutionEventPersistenceLatency,
		Stage:       "persistence",
		Status:      classification,
		PayloadJSON: string(payloadRaw),
		CreatedAt:   at,
	})
	return err
}

func scanNextActionRows(rows *sql.Rows) ([]NextActionRecord, error) {
	out := []NextActionRecord{}
	for rows.Next() {
		record, err := scanNextActionRecord(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate next actions: %w", err)
	}
	return out, nil
}

func scanNextActionRecord(scanner interface{ Scan(dest ...any) error }) (NextActionRecord, error) {
	var (
		record            NextActionRecord
		scopeKindRaw      sql.NullString
		scopeIDRaw        sql.NullString
		durableAgentIDRaw sql.NullString
		stateRaw          string
		causalRefsRaw     string
		createdAtRaw      string
		resolvedAtRaw     sql.NullString
	)
	if err := scanner.Scan(
		&record.RecordID, &record.SessionID, &record.ChatID, &record.UserID, &scopeKindRaw, &scopeIDRaw, &durableAgentIDRaw,
		&record.TurnRunID, &record.Owner, &stateRaw, &record.SubjectKind, &record.SubjectRef, &causalRefsRaw,
		&record.NextAction, &record.RequiredAuthority, &record.ResourceBlocker, &record.Verifier, &record.RetryPolicy,
		&record.OperatorProjection, &createdAtRaw, &resolvedAtRaw,
	); err != nil {
		return NextActionRecord{}, fmt.Errorf("scan next action: %w", err)
	}
	record.Scope = NormalizeScopeRef(ScopeRef{
		Kind:           ScopeKind(nullToString(scopeKindRaw)),
		ID:             nullToString(scopeIDRaw),
		DurableAgentID: nullToString(durableAgentIDRaw),
	})
	record.State = NormalizeNextActionState(NextActionState(stateRaw))
	record.CausalRefs = decodeStringList(causalRefsRaw)
	var err error
	record.CreatedAt, err = parseSQLiteTime(createdAtRaw)
	if err != nil {
		return NextActionRecord{}, fmt.Errorf("parse next action created_at: %w", err)
	}
	if resolvedAtRaw.Valid && strings.TrimSpace(resolvedAtRaw.String) != "" {
		record.ResolvedAt, err = parseSQLiteTime(resolvedAtRaw.String)
		if err != nil {
			return NextActionRecord{}, fmt.Errorf("parse next action resolved_at: %w", err)
		}
	}
	return record, nil
}
