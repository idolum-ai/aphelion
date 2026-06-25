//go:build linux

package session

import (
	"bytes"
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
	operationInputJSON, operationInputRedacted, err := normalizeNextActionOperationInputJSON(input.OperationInputJSON)
	if err != nil {
		return NextActionRecord{}, err
	}
	input.OperationInputJSON = operationInputJSON
	if operationInputRedacted && input.State == NextActionReadyToExecute {
		input = downgradeReadyNextActionForRedactedOperationInput(input, operationInputJSON)
	}
	input.NextAction = RedactEvidenceText(input.NextAction).Text
	input.OperatorProjection = RedactEvidenceText(input.OperatorProjection).Text
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
			AND record_id != ?
			AND resolved_at IS NULL
	`, createdAt.Format(time.RFC3339Nano), sessionID, input.SubjectKind, input.SubjectRef, input.RecordID); err != nil {
		return NextActionRecord{}, fmt.Errorf("resolve prior next actions: %w", err)
	}
	causalRefs := encodeStringList(input.CausalRefs)
	result, err := tx.Exec(`
		INSERT OR IGNORE INTO next_action_records(
			record_id, session_id, chat_id, user_id, scope_kind, scope_id, durable_agent_id,
			turn_run_id, owner, state, subject_kind, subject_ref, causal_refs_json,
			next_action, required_authority, resource_blocker, verifier, retry_policy,
			operation_kind, operation_tool, operation_input_json, operator_projection, created_at, resolved_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, NULL)
	`, input.RecordID, sessionID, input.Key.ChatID, input.Key.UserID, string(scope.Kind), scope.ID, scope.DurableAgentID,
		input.TurnRunID, input.Owner, string(input.State), input.SubjectKind, input.SubjectRef, causalRefs,
		input.NextAction, input.RequiredAuthority, input.ResourceBlocker, input.Verifier, input.RetryPolicy,
		input.OperationKind, input.OperationTool, input.OperationInputJSON,
		input.OperatorProjection, createdAt.Format(time.RFC3339Nano))
	if err != nil {
		return NextActionRecord{}, fmt.Errorf("insert next action %s: %w", input.RecordID, err)
	}
	if rows, _ := result.RowsAffected(); rows == 0 {
		if existing, ok, err := nextActionByRecordIDTx(tx, input.RecordID); err != nil {
			return NextActionRecord{}, err
		} else if ok {
			return existing, nil
		}
		return NextActionRecord{}, fmt.Errorf("insert next action %s ignored without existing record", input.RecordID)
	}
	payload := map[string]any{
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
	}
	if operation := nextActionOperationPayload(input); len(operation) > 0 {
		payload["operation"] = operation
	}
	payloadRaw, _ := json.Marshal(payload)
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
		OperationKind:      input.OperationKind,
		OperationTool:      input.OperationTool,
		OperationInputJSON: input.OperationInputJSON,
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
			operation_kind, operation_tool, operation_input_json, operator_projection, created_at, resolved_at
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

func (s *SQLiteStore) OpenNextActionsBySubject(subjectKind string, subjectRef string, limit int) ([]NextActionRecord, error) {
	if s == nil || s.db == nil {
		return nil, nil
	}
	subjectKind = normalizeEnumValue(subjectKind)
	subjectRef = strings.TrimSpace(subjectRef)
	if subjectKind == "" || subjectRef == "" {
		return nil, nil
	}
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.Query(`
		SELECT record_id, session_id, chat_id, user_id, scope_kind, scope_id, durable_agent_id,
			turn_run_id, owner, state, subject_kind, subject_ref, causal_refs_json,
			next_action, required_authority, resource_blocker, verifier, retry_policy,
			operation_kind, operation_tool, operation_input_json, operator_projection, created_at, resolved_at
		FROM next_action_records
		WHERE subject_kind = ? AND subject_ref = ? AND resolved_at IS NULL
		ORDER BY created_at DESC, record_id DESC
		LIMIT ?
	`, subjectKind, subjectRef, limit)
	if err != nil {
		return nil, fmt.Errorf("query open next actions by subject: %w", err)
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
			operation_kind, operation_tool, operation_input_json, operator_projection, created_at, resolved_at
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
		OperationKind:      "resource_repair",
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
	classification := core.ClassifyPersistenceLatency(component, latency)
	payloadRaw, _ := json.Marshal(map[string]any{
		"component":      component,
		"latency_ms":     latency.Milliseconds(),
		"classification": classification.Condition,
		"status_class":   classification.StatusClass,
		"failure_class":  classification.FailureClass,
		"retry_policy":   classification.RetryPolicy,
		"next_action":    classification.NextAction,
	})
	_, err := s.AppendExecutionEvent(key, ExecutionEventInput{
		EventType:   core.ExecutionEventPersistenceLatency,
		Stage:       "persistence",
		Status:      classification.Condition,
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
		&record.OperationKind, &record.OperationTool, &record.OperationInputJSON, &record.OperatorProjection, &createdAtRaw, &resolvedAtRaw,
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

func normalizeNextActionOperationInputJSON(raw string) (string, bool, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", false, nil
	}
	if !json.Valid([]byte(raw)) {
		return "", false, fmt.Errorf("next action operation input must be valid JSON")
	}
	var payload any
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return "", false, fmt.Errorf("decode next action operation input: %w", err)
	}
	redactedPayload, redacted := redactNextActionOperationPayload(payload)
	sanitized, err := json.Marshal(redactedPayload)
	if err != nil {
		return "", false, fmt.Errorf("redact next action operation input: %w", err)
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, sanitized); err != nil {
		return "", false, fmt.Errorf("compact next action operation input: %w", err)
	}
	return compact.String(), redacted, nil
}

func redactNextActionOperationPayload(value any) (any, bool) {
	switch v := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(v))
		redactedAny := false
		for key, child := range v {
			redactedChild, childRedacted := redactNextActionOperationPayload(child)
			out[key] = redactedChild
			if childRedacted {
				redactedAny = true
			}
			text, ok := child.(string)
			if !ok || !nextActionOperationFingerprintField(key) {
				continue
			}
			redacted := RedactEvidenceText(text)
			if !redacted.Redacted {
				continue
			}
			fingerprintKey := normalizeEnumValue(key) + "_fingerprint"
			if _, exists := v[fingerprintKey]; !exists {
				out[fingerprintKey] = EffectAttemptCommandHash(text)
			}
		}
		return out, redactedAny
	case []any:
		out := make([]any, 0, len(v))
		redactedAny := false
		for _, child := range v {
			redactedChild, childRedacted := redactNextActionOperationPayload(child)
			out = append(out, redactedChild)
			if childRedacted {
				redactedAny = true
			}
		}
		return out, redactedAny
	case string:
		redacted := RedactEvidenceText(v)
		return redacted.Text, redacted.Redacted
	default:
		return value, false
	}
}

func downgradeReadyNextActionForRedactedOperationInput(input NextActionInput, redactedOperationInputJSON string) NextActionInput {
	originalKind := input.OperationKind
	originalTool := input.OperationTool
	payload := map[string]any{
		"reason":                  "ready_operation_input_redacted",
		"original_operation_kind": originalKind,
		"original_operation_tool": originalTool,
		"redacted_input_hash":     EffectAttemptCommandHash(redactedOperationInputJSON),
	}
	var redactedInput any
	if err := json.Unmarshal([]byte(redactedOperationInputJSON), &redactedInput); err == nil && redactedInput != nil {
		payload["redacted_operation_input"] = redactedInput
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		raw = []byte(`{"reason":"ready_operation_input_redacted"}`)
	}
	input.State = NextActionWaitingForOperator
	input.OperationKind = "typed_operation_required"
	input.OperationTool = "update_operation"
	input.OperationInputJSON = string(raw)
	input.NextAction = "rewrite the recommended operation with exact, non-redacted operands before execution"
	input.RequiredAuthority = "typed_operation_required"
	input.Verifier = ""
	input.RetryPolicy = "do_not_execute_redacted_operation_payload"
	input.OperatorProjection = "Ready operation payload contained redacted operand(s); rewrite it with exact typed operands before execution."
	input.CausalRefs = append(input.CausalRefs, "next_action:redacted_ready_operation_downgrade")
	return input
}

func nextActionOperationFingerprintField(field string) bool {
	switch normalizeEnumValue(field) {
	case "command", "command_preview", "path", "query", "snippet", "shell", "rejected_shell", "workdir", "url":
		return true
	default:
		return false
	}
}

func nextActionOperationPayload(input NextActionInput) map[string]any {
	operation := map[string]any{}
	if input.OperationKind != "" {
		operation["kind"] = input.OperationKind
	}
	if input.OperationTool != "" {
		operation["tool"] = input.OperationTool
	}
	if strings.TrimSpace(input.OperationInputJSON) != "" {
		var payload any
		if err := json.Unmarshal([]byte(input.OperationInputJSON), &payload); err == nil {
			operation["input"] = payload
		} else {
			operation["input_json"] = input.OperationInputJSON
		}
	}
	return operation
}
