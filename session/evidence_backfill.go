//go:build linux

package session

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

func backfillEvidenceLedgerTx(tx *sql.Tx) error {
	if err := ensureEvidenceLedgerTables(tx); err != nil {
		return err
	}
	for _, source := range evidenceBackfillSources() {
		exists, err := schemaTableExists(tx, source.table)
		if err != nil {
			return err
		}
		if !exists {
			continue
		}
		if ok, err := schemaColumnsExist(tx, source.table, source.requiredColumns...); err != nil {
			return err
		} else if !ok {
			continue
		}
		if err := backfillEvidenceQueryTx(tx, source.query); err != nil {
			return fmt.Errorf("backfill %s evidence: %w", source.table, err)
		}
	}
	if err := backfillSessionStateEvidenceTx(tx); err != nil {
		return err
	}
	return nil
}

type evidenceBackfillSource struct {
	table           string
	requiredColumns []string
	query           string
}

func evidenceBackfillSources() []evidenceBackfillSource {
	return []evidenceBackfillSource{
		{
			table:           "execution_events",
			requiredColumns: []string{"scope_kind", "scope_id", "durable_agent_id", "payload_json"},
			query: `
				SELECT 'execution_event', 'execution_events:' || id, 'execution_events', CAST(id AS TEXT), session_id, chat_id, user_id,
					scope_kind, scope_id, durable_agent_id, 'execution_event', 'attested', '', event_type, event_type || ' ' || stage || ' ' || status,
					payload_json, payload_json, created_at
				FROM execution_events`,
		},
		{
			table:           "turn_runs",
			requiredColumns: []string{"scope_kind", "scope_id", "durable_agent_id", "completed_at", "recovery_summary"},
			query: `
				SELECT 'turn_run', 'turn_runs:' || id || ':' || status || ':' || COALESCE(completed_at, started_at), 'turn_runs', CAST(id AS TEXT), session_id, chat_id, user_id,
					scope_kind, scope_id, durable_agent_id, 'turn_run', 'attested', '', kind, kind || ' turn ' || status,
					COALESCE(recovery_summary, request_text, ''), json_object('id', id, 'kind', kind, 'status', status, 'request_text', request_text, 'turn_index', turn_index, 'error_text', COALESCE(error_text, ''), 'last_tool_name', COALESCE(last_tool_name, ''), 'last_tool_error', COALESCE(last_tool_error, '')),
					COALESCE(completed_at, last_activity_at, started_at)
				FROM turn_runs`,
		},
		{
			table:           "messages",
			requiredColumns: []string{"floor_content", "floor_metadata", "tool_name"},
			query: `
				SELECT 'message', 'messages:' || id, 'messages', CAST(id AS TEXT), session_id, chat_id, user_id,
					'', '', '', 'message', CASE WHEN role = 'assistant' THEN 'claimed' ELSE 'observed' END, '', role, role || ' message turn ' || turn_index,
					SUBSTR(content, 1, 1000), json_object('id', id, 'role', role, 'content', content, 'floor_content', COALESCE(floor_content, ''), 'floor_metadata', COALESCE(floor_metadata, ''), 'tool_name', COALESCE(tool_name, ''), 'turn_index', turn_index),
					created_at
				FROM messages`,
		},
		{
			table:           "plan_events",
			requiredColumns: []string{"plan_state_json"},
			query: `
				SELECT 'plan_state', 'plan_events:' || id, 'plan_events', CAST(id AS TEXT), session_id, 0, 0,
					'', '', '', 'plan_state', 'projection', '', event_kind, 'plan event ' || event_kind,
					plan_state_json, json_object('id', id, 'event_kind', event_kind, 'plan_state_json', plan_state_json), created_at
				FROM plan_events`,
		},
		{
			table:           "review_events",
			requiredColumns: []string{"source_scope_kind", "source_scope_id", "source_durable_agent_id"},
			query: `
				SELECT 'review_event', 'review_events:' || id, 'review_events', CAST(id AS TEXT), COALESCE(source_session_id, target_session_id, ''), source_chat_id, source_user_id,
					source_scope_kind, source_scope_id, source_durable_agent_id, 'review_event', 'projection', '', status, 'review event ' || status,
					summary, json_object('id', id, 'summary', summary, 'metadata_json', COALESCE(metadata_json, ''), 'status', status, 'target_session_id', COALESCE(target_session_id, '')), created_at
				FROM review_events`,
		},
		{
			table:           "capability_requests",
			requiredColumns: []string{"request_id", "requested_for", "contract_json"},
			query: `
				SELECT 'capability_request', 'capability_requests:' || request_id, 'capability_requests', request_id, '', 0, 0,
					'', '', '', 'capability_request', 'claimed', kind, requested_for, kind || ' request ' || review_status,
					purpose, json_object('request_id', request_id, 'requested_by', requested_by, 'requested_for', requested_for, 'kind', kind, 'target_resource', target_resource, 'purpose', purpose, 'review_status', review_status, 'grant_id', grant_id), updated_at
				FROM capability_requests`,
		},
		{
			table:           "capability_grants",
			requiredColumns: []string{"grant_id", "allowed_actions_json", "expires_at"},
			query: `
				SELECT 'capability_grant', 'capability_grants:' || grant_id || ':' || status || ':' || updated_at, 'capability_grants', grant_id, '', 0, 0,
					'', '', '', 'capability_grant', 'attested', kind, granted_to, kind || ' grant ' || status,
					target_resource, json_object('grant_id', grant_id, 'request_id', request_id, 'granted_by', granted_by, 'granted_to', granted_to, 'kind', kind, 'target_resource', target_resource, 'allowed_actions_json', allowed_actions_json, 'status', status, 'expires_at', COALESCE(expires_at, '')), updated_at
				FROM capability_grants`,
		},
		{
			table:           "capability_invocations",
			requiredColumns: []string{"turn_run_id", "continuation_lease_id", "operation_plan_lease_id"},
			query: `
				SELECT 'capability_invocation', 'capability_invocations:' || id, 'capability_invocations', CAST(id AS TEXT), session_id, 0, 0,
					'', '', '', 'capability_invocation', 'attested', authority_source, principal, action || ' ' || status,
					error_text, json_object('id', id, 'grant_id', grant_id, 'principal', principal, 'action', action, 'status', status, 'error_text', error_text, 'turn_run_id', turn_run_id, 'continuation_lease_id', continuation_lease_id, 'operation_plan_lease_id', operation_plan_lease_id), created_at
				FROM capability_invocations`,
		},
		{
			table:           "mission_ledger",
			requiredColumns: []string{"objective", "authority_json", "evidence_json"},
			query: `
				SELECT 'mission', 'mission_ledger:' || id || ':' || status || ':' || updated_at, 'mission_ledger', id, '', 0, 0,
					'', scope, '', 'mission', 'projection', '', id, title || ' ' || status,
					objective, json_object('id', id, 'scope', scope, 'owner', owner, 'title', title, 'objective', objective, 'status', status, 'evidence_json', COALESCE(evidence_json, ''), 'current_plan_json', COALESCE(current_plan_json, '')), updated_at
				FROM mission_ledger`,
		},
		{
			table:           "mission_events",
			requiredColumns: []string{"payload_json"},
			query: `
				SELECT 'mission_event', 'mission_events:' || seq, 'mission_events', CAST(seq AS TEXT), '', 0, 0,
					'', '', '', 'mission_event', 'projection', '', mission_id, event_type,
					summary, json_object('seq', seq, 'mission_id', mission_id, 'event_type', event_type, 'actor', actor, 'summary', summary, 'payload_json', payload_json), created_at
				FROM mission_events`,
		},
		{
			table:           "mission_handoffs",
			requiredColumns: []string{"expected_evidence_json", "recovery_question"},
			query: `
				SELECT 'mission_handoff', 'mission_handoffs:' || id || ':' || status || ':' || updated_at, 'mission_handoffs', id, '', 0, 0,
					'', '', '', 'mission_handoff', 'projection', '', COALESCE(mission_id, ''), planned_action || ' ' || status,
					recovery_question, json_object('id', id, 'mission_id', COALESCE(mission_id, ''), 'operation_id', COALESCE(operation_id, ''), 'planned_action', planned_action, 'expected_evidence_json', COALESCE(expected_evidence_json, ''), 'status', status), updated_at
				FROM mission_handoffs`,
		},
		{
			table:           "mission_results",
			requiredColumns: []string{"evidence_refs_json", "remaining_risk"},
			query: `
				SELECT 'mission_result', 'mission_results:' || id, 'mission_results', id, '', 0, 0,
					'', '', '', 'mission_result', 'attested', '', COALESCE(mission_id, ''), status,
					summary, json_object('id', id, 'handoff_id', handoff_id, 'mission_id', COALESCE(mission_id, ''), 'operation_id', COALESCE(operation_id, ''), 'status', status, 'evidence_refs_json', COALESCE(evidence_refs_json, ''), 'summary', summary, 'remaining_risk', COALESCE(remaining_risk, '')), recorded_at
				FROM mission_results`,
		},
		{
			table:           "curiosity_observations",
			requiredColumns: []string{"scope_kind", "scope_id", "scope_durable_agent_id", "content_hash"},
			query: `
				SELECT 'curiosity_observation', 'curiosity_observations:' || id, 'curiosity_observations', CAST(id AS TEXT), session_id, chat_id, user_id,
					scope_kind, scope_id, scope_durable_agent_id, 'curiosity_observation', 'observed', 'read_only', subject_key, source_kind || ':' || source_ref,
					summary, json_object('id', id, 'lease_id', lease_id, 'candidate_id', candidate_id, 'source_kind', source_kind, 'source_ref', source_ref, 'subject_key', subject_key, 'summary', summary, 'evidence_json', evidence_json, 'content_hash', content_hash, 'confidence', confidence), observed_at
				FROM curiosity_observations`,
		},
		{
			table:           "interior_signal_observations",
			requiredColumns: []string{"scope_kind", "scope_id", "scope_durable_agent_id", "applied_weight"},
			query: `
				SELECT 'interior_signal', 'interior_signal_observations:' || id, 'interior_signal_observations', CAST(id AS TEXT), session_id, chat_id, user_id,
					scope_kind, scope_id, scope_durable_agent_id, 'interior_signal', 'observed', '', subject_key, category,
					summary, json_object('id', id, 'category', category, 'subject_key', subject_key, 'summary', summary, 'source', source, 'evidence_json', evidence_json, 'source_fingerprint', source_fingerprint, 'weight', weight, 'applied_weight', applied_weight, 'confidence', confidence), observed_at
				FROM interior_signal_observations`,
		},
		{
			table:           "interior_signal_states",
			requiredColumns: []string{"scope_kind", "scope_id", "scope_durable_agent_id", "intensity"},
			query: `
				SELECT 'interior_signal_state', 'interior_signal_states:' || session_id || ':' || category || ':' || subject_key || ':' || updated_at, 'interior_signal_states', session_id || ':' || category || ':' || subject_key, session_id, chat_id, user_id,
					scope_kind, scope_id, scope_durable_agent_id, 'interior_signal_state', 'projection', '', subject_key, category || ' pressure',
					summary, json_object('category', category, 'subject_key', subject_key, 'summary', summary, 'evidence_json', evidence_json, 'intensity', intensity, 'confidence', confidence, 'observation_count', observation_count), updated_at
				FROM interior_signal_states`,
		},
		{
			table:           "reentry_recommendations",
			requiredColumns: []string{"scope_kind", "scope_id", "scope_durable_agent_id", "terminal_fingerprint"},
			query: `
				SELECT 'reentry_recommendation', 'reentry_recommendations:' || id || ':' || status || ':' || updated_at, 'reentry_recommendations', id, session_id, chat_id, sender_id,
					scope_kind, scope_id, scope_durable_agent_id, 'reentry_recommendation', 'projection', '', terminal_fingerprint, 'reentry recommendation ' || status,
					result_summary, json_object('id', id, 'owner', owner, 'source_turn_run_id', source_turn_run_id, 'terminal_fingerprint', terminal_fingerprint, 'status', status, 'candidates_json', candidates_json, 'selected_candidate_id', selected_candidate_id, 'result_summary', result_summary), updated_at
				FROM reentry_recommendations`,
		},
		{
			table:           "telegram_ingress_updates",
			requiredColumns: []string{"payload_json", "updated_at"},
			query: `
				SELECT 'telegram_ingress', 'telegram_ingress_updates:' || surface || ':' || update_id || ':' || status || ':' || updated_at, 'telegram_ingress_updates', surface || ':' || update_id, session_id, chat_id, sender_id,
					'', '', '', 'telegram_ingress', 'attested', '', update_kind, 'telegram ingress ' || status,
					error_text, json_object('surface', surface, 'update_id', update_id, 'status', status, 'update_kind', update_kind, 'message_id', message_id, 'turn_run_id', turn_run_id, 'error_text', error_text, 'payload_json', payload_json), updated_at
				FROM telegram_ingress_updates`,
		},
		{
			table:           "telegram_media_thread_pickers",
			requiredColumns: []string{"source_ingress_surface", "source_ingress_update_id"},
			query: `
				SELECT 'telegram_media_picker', 'telegram_media_thread_pickers:' || chat_id || ':' || picker_message_id || ':' || status || ':' || updated_at, 'telegram_media_thread_pickers', chat_id || ':' || picker_message_id, '', chat_id, 0,
					'', '', '', 'telegram_media_picker', 'attested', '', status, 'media thread picker ' || status,
					inbound_json, json_object('chat_id', chat_id, 'picker_message_id', picker_message_id, 'source_message_id', source_message_id, 'source_ingress_surface', source_ingress_surface, 'source_ingress_update_id', source_ingress_update_id, 'status', status, 'inbound_json', inbound_json), updated_at
				FROM telegram_media_thread_pickers`,
		},
		{
			table:           "artifact_index",
			requiredColumns: []string{"fetch_state", "materialized_path"},
			query: `
				SELECT 'artifact', 'artifact_index:' || session_id || ':' || turn_index || ':' || artifact_id || ':' || updated_at, 'artifact_index', session_id || ':' || turn_index || ':' || artifact_id, session_id, chat_id, user_id,
					'', '', '', 'artifact', 'observed', '', artifact_id, kind || ' artifact',
					summary, json_object('session_id', session_id, 'turn_index', turn_index, 'artifact_id', artifact_id, 'source_type', source_type, 'kind', kind, 'summary', summary, 'handling', handling, 'retention', retention, 'fetch_state', fetch_state, 'materialized_path', materialized_path), updated_at
				FROM artifact_index`,
		},
	}
}

func schemaColumnsExist(tx *sql.Tx, tableName string, columns ...string) (bool, error) {
	for _, column := range columns {
		if strings.TrimSpace(column) == "" {
			continue
		}
		exists, err := schemaColumnExists(tx, tableName, column)
		if err != nil {
			return false, err
		}
		if !exists {
			return false, nil
		}
	}
	return true, nil
}

func backfillEvidenceQueryTx(tx *sql.Tx, query string) error {
	rows, err := tx.Query(query)
	if err != nil {
		if isEvidenceBackfillSchemaDriftError(err) {
			return nil
		}
		return err
	}
	defer rows.Close()
	for rows.Next() {
		input, err := scanEvidenceBackfillInput(rows)
		if err != nil {
			return err
		}
		if _, err := upsertEvidenceObjectTx(tx, input); err != nil {
			return err
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	return nil
}

func scanEvidenceBackfillInput(rows *sql.Rows) (EvidenceObjectInput, error) {
	var (
		input           EvidenceObjectInput
		scopeKind       sql.NullString
		scopeID         sql.NullString
		durableAgentID  sql.NullString
		sourceKind      sql.NullString
		sourceRef       sql.NullString
		sourceTable     sql.NullString
		sourceID        sql.NullString
		sessionID       sql.NullString
		evidenceType    sql.NullString
		epistemicStatus sql.NullString
		authorityClass  sql.NullString
		subjectKey      sql.NullString
		summary         sql.NullString
		digest          sql.NullString
		payloadJSON     sql.NullString
		observedRaw     sql.NullString
	)
	if err := rows.Scan(
		&sourceKind, &sourceRef, &sourceTable, &sourceID, &sessionID, &input.ChatID, &input.UserID,
		&scopeKind, &scopeID, &durableAgentID, &evidenceType, &epistemicStatus, &authorityClass, &subjectKey, &summary,
		&digest, &payloadJSON, &observedRaw,
	); err != nil {
		return EvidenceObjectInput{}, fmt.Errorf("scan evidence backfill row: %w", err)
	}
	input.SourceKind = nullToString(sourceKind)
	input.SourceRef = nullToString(sourceRef)
	input.SourceTable = nullToString(sourceTable)
	input.SourceID = nullToString(sourceID)
	input.SessionID = nullToString(sessionID)
	input.Scope = ScopeRef{Kind: ScopeKind(nullToString(scopeKind)), ID: nullToString(scopeID), DurableAgentID: nullToString(durableAgentID)}
	input.EvidenceType = nullToString(evidenceType)
	input.EpistemicStatus = nullToString(epistemicStatus)
	input.AuthorityClass = nullToString(authorityClass)
	input.SubjectKey = nullToString(subjectKey)
	input.Summary = nullToString(summary)
	input.Digest = nullToString(digest)
	input.PayloadJSON = nullToString(payloadJSON)
	if raw := strings.TrimSpace(nullToString(observedRaw)); raw != "" {
		input.ObservedAt = parseEvidenceBackfillTime(raw)
	}
	return input, nil
}

func backfillSessionStateEvidenceTx(tx *sql.Tx) error {
	exists, err := schemaTableExists(tx, "sessions")
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}
	required := []string{
		"session_id", "chat_id", "user_id", "scope_kind", "scope_id", "durable_agent_id",
		"plan_state_json", "operation_state_json", "continuation_state_json", "working_objective_json",
		"updated_at", "turn_count", "last_provider", "last_model", "last_error",
	}
	ok, err := schemaColumnsExist(tx, "sessions", required...)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	rows, err := tx.Query(`
		SELECT session_id, chat_id, user_id, scope_kind, scope_id, durable_agent_id,
			plan_state_json, operation_state_json, continuation_state_json, working_objective_json,
			updated_at, turn_count, last_provider, last_model, last_error
		FROM sessions
	`)
	if err != nil {
		return fmt.Errorf("query sessions for evidence backfill: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var (
			sessionID               string
			chatID, userID          int64
			scopeKind, scopeID      string
			durableAgentID          string
			planJSON, opJSON        string
			continuationJSON        string
			workingObjectiveJSON    string
			updatedRaw              string
			turnCount               int
			lastProvider, lastModel sql.NullString
			lastError               sql.NullString
		)
		if err := rows.Scan(&sessionID, &chatID, &userID, &scopeKind, &scopeID, &durableAgentID, &planJSON, &opJSON, &continuationJSON, &workingObjectiveJSON, &updatedRaw, &turnCount, &lastProvider, &lastModel, &lastError); err != nil {
			return fmt.Errorf("scan session evidence snapshot: %w", err)
		}
		observed := parseEvidenceBackfillTime(updatedRaw)
		scope := ScopeRef{Kind: ScopeKind(scopeKind), ID: scopeID, DurableAgentID: durableAgentID}
		sessionPayload := map[string]any{
			"turn_count":             turnCount,
			"working_objective_json": workingObjectiveJSON,
			"last_provider":          nullToString(lastProvider),
			"last_model":             nullToString(lastModel),
			"last_error":             nullToString(lastError),
		}
		sessionPayloadJSON, err := json.Marshal(sessionPayload)
		if err != nil {
			return fmt.Errorf("marshal session evidence snapshot: %w", err)
		}
		snapshots := []EvidenceObjectInput{
			sessionStateEvidenceInput(sessionID, chatID, userID, scope, "plan_state", EvidenceSourcePlanState, planJSON, observed),
			sessionStateEvidenceInput(sessionID, chatID, userID, scope, "operation_state", EvidenceSourceOperationState, opJSON, observed),
			sessionStateEvidenceInput(sessionID, chatID, userID, scope, "continuation_state", EvidenceSourceContinuationState, continuationJSON, observed),
			sessionStateEvidenceInput(sessionID, chatID, userID, scope, "session_state", EvidenceSourceSessionState, string(sessionPayloadJSON), observed),
		}
		for _, snapshot := range snapshots {
			if _, err := upsertEvidenceObjectTx(tx, snapshot); err != nil {
				return err
			}
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate session evidence snapshots: %w", err)
	}
	return nil
}

func parseEvidenceBackfillTime(raw string) time.Time {
	t, err := parseSQLiteTime(raw)
	if err != nil {
		return time.Unix(0, 0).UTC()
	}
	return t
}

func isEvidenceBackfillSchemaDriftError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "no such column") || strings.Contains(msg, "no such table")
}

func sessionStateEvidenceInput(sessionID string, chatID int64, userID int64, scope ScopeRef, label string, sourceKind string, payload string, observed time.Time) EvidenceObjectInput {
	payload, _ = normalizeEvidencePayloadJSON(payload)
	hash := evidencePayloadHash(payload)
	ref := "sessions:" + sessionID + ":" + label + ":" + hash
	return EvidenceObjectInput{
		SourceKind:      sourceKind,
		SourceRef:       ref,
		SourceTable:     "sessions",
		SourceID:        sessionID,
		SessionID:       sessionID,
		ChatID:          chatID,
		UserID:          userID,
		Scope:           scope,
		EvidenceType:    sourceKind,
		EpistemicStatus: EvidenceStatusProjection,
		Summary:         strings.ReplaceAll(label, "_", " ") + " snapshot",
		Digest:          payload,
		PayloadJSON:     payload,
		ObservedAt:      observed,
	}
}
