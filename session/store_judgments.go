//go:build linux

package session

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

func (s *SQLiteStore) RecordJudgment(input JudgmentInput) (Judgment, error) {
	if s == nil || s.db == nil {
		return Judgment{}, fmt.Errorf("judgment store unavailable")
	}
	input, err := NormalizeJudgmentInput(input)
	if err != nil {
		return Judgment{}, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return Judgment{}, fmt.Errorf("begin judgment tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	judgment, err := recordJudgmentTx(tx, input)
	if err != nil {
		return Judgment{}, err
	}
	if err := tx.Commit(); err != nil {
		return Judgment{}, fmt.Errorf("commit judgment tx: %w", err)
	}
	return judgment, nil
}

func (s *SQLiteStore) Judgment(id string) (Judgment, bool, error) {
	if s == nil || s.db == nil || strings.TrimSpace(id) == "" {
		return Judgment{}, false, nil
	}
	row := s.db.QueryRow(`SELECT `+judgmentColumns()+` FROM judgments WHERE judgment_id = ?`, strings.TrimSpace(id))
	judgment, err := scanJudgment(row)
	if err == sql.ErrNoRows {
		return Judgment{}, false, nil
	}
	if err != nil {
		return Judgment{}, false, err
	}
	return judgment, true, nil
}

func (s *SQLiteStore) JudgmentsBySession(key SessionKey, limit int) ([]Judgment, error) {
	if s == nil || s.db == nil {
		return nil, nil
	}
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.Query(`
		SELECT `+judgmentColumns()+`
		FROM judgments
		WHERE session_id = ?
		ORDER BY created_at DESC, judgment_id DESC
		LIMIT ?
	`, SessionIDForKey(key), limit)
	if err != nil {
		return nil, fmt.Errorf("query judgments by session: %w", err)
	}
	defer rows.Close()
	return scanJudgments(rows)
}

func (s *SQLiteStore) JudgmentsByKind(key SessionKey, kind string, limit int) ([]Judgment, error) {
	if s == nil || s.db == nil {
		return nil, nil
	}
	kind = judgmentUseToken(kind)
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.Query(`
		SELECT `+judgmentColumns()+`
		FROM judgments
		WHERE session_id = ? AND kind = ?
		ORDER BY created_at DESC, judgment_id DESC
		LIMIT ?
	`, SessionIDForKey(key), kind, limit)
	if err != nil {
		return nil, fmt.Errorf("query judgments by kind: %w", err)
	}
	defer rows.Close()
	return scanJudgments(rows)
}

func (s *SQLiteStore) AppendJudgmentChallengeEvent(input JudgmentChallengeEventInput) (JudgmentChallengeEvent, error) {
	if s == nil || s.db == nil {
		return JudgmentChallengeEvent{}, fmt.Errorf("judgment challenge store unavailable")
	}
	input, err := NormalizeJudgmentChallengeEventInput(input)
	if err != nil {
		return JudgmentChallengeEvent{}, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return JudgmentChallengeEvent{}, fmt.Errorf("begin judgment challenge event tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	event, err := appendJudgmentChallengeEventTx(tx, input)
	if err != nil {
		return JudgmentChallengeEvent{}, err
	}
	if err := tx.Commit(); err != nil {
		return JudgmentChallengeEvent{}, fmt.Errorf("commit judgment challenge event tx: %w", err)
	}
	return event, nil
}

func (s *SQLiteStore) JudgmentChallengeEvents(judgmentID string, limit int) ([]JudgmentChallengeEvent, error) {
	if s == nil || s.db == nil || strings.TrimSpace(judgmentID) == "" {
		return nil, nil
	}
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.Query(`
		SELECT `+judgmentChallengeEventColumns()+`
		FROM judgment_challenge_events
		WHERE judgment_id = ?
		ORDER BY created_at ASC, event_id ASC
		LIMIT ?
	`, strings.TrimSpace(judgmentID), limit)
	if err != nil {
		return nil, fmt.Errorf("query judgment challenge events: %w", err)
	}
	defer rows.Close()
	return scanJudgmentChallengeEvents(rows)
}

func recordJudgmentTx(tx *sql.Tx, input JudgmentInput) (Judgment, error) {
	input, err := NormalizeJudgmentInput(input)
	if err != nil {
		return Judgment{}, err
	}
	scope := defaultScopeForKey(input.Key)
	if strings.TrimSpace(string(scope.Kind)) == "" && input.Key.ChatID == 0 {
		scope = NormalizeScopeRef(input.Key.Scope)
	}
	inputRefs := encodeStringList(input.InputRefs)
	unknowns := encodeUnknownPredicates(input.Unknowns)
	dependencyRefs := encodeJudgmentDependencyRefs(input.DependencyRefs)
	faultDomains := encodeStringList(input.SourceFaultDomains)
	_, err = tx.Exec(`
		INSERT INTO judgments(
			judgment_id, session_id, chat_id, user_id, scope_kind, scope_id, durable_agent_id,
			turn_run_id, operation_id, kind, schema_version, subject_key, claim_key, interpreter_id,
			interpreter_version, input_refs_json, input_hash, result_json, completeness, unknowns_json,
			dependency_refs_json, source_fault_domains_json, sensitivity, content_hash, as_of, expires_at, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, input.ID, input.SessionID, input.Key.ChatID, input.Key.UserID, string(scope.Kind), scope.ID, scope.DurableAgentID,
		input.TurnRunID, input.OperationID, input.Kind, input.SchemaVersion, input.SubjectKey, input.ClaimKey, input.InterpreterID,
		input.InterpreterVersion, inputRefs, input.InputHash, input.ResultJSON, string(input.Completeness), unknowns,
		dependencyRefs, faultDomains, input.Sensitivity, input.ContentHash, input.AsOf.UTC().Format(time.RFC3339Nano),
		nullableTime(input.ExpiresAt), input.CreatedAt.UTC().Format(time.RFC3339Nano))
	if err != nil && !strings.Contains(err.Error(), "UNIQUE constraint failed") {
		return Judgment{}, fmt.Errorf("record judgment %s: %w", input.ID, err)
	}
	if err != nil {
		judgment, ok, loadErr := judgmentByIDTx(tx, input.ID)
		if loadErr != nil {
			return Judgment{}, loadErr
		}
		if !ok {
			return Judgment{}, fmt.Errorf("judgment %s conflicted but existing row was not found", input.ID)
		}
		if mismatch := judgmentImmutableMismatch(judgment, input, scope, inputRefs, unknowns, dependencyRefs, faultDomains); mismatch != "" {
			return Judgment{}, fmt.Errorf("judgment %s immutable commitment mismatch: %s", input.ID, mismatch)
		}
		return judgment, nil
	}
	judgment, ok, err := judgmentByIDTx(tx, input.ID)
	if err != nil {
		return Judgment{}, err
	}
	if !ok {
		return Judgment{}, fmt.Errorf("judgment %s disappeared after insert", input.ID)
	}
	return judgment, nil
}

func appendJudgmentChallengeEventTx(tx *sql.Tx, input JudgmentChallengeEventInput) (JudgmentChallengeEvent, error) {
	input, err := NormalizeJudgmentChallengeEventInput(input)
	if err != nil {
		return JudgmentChallengeEvent{}, err
	}
	scope := defaultScopeForKey(input.Key)
	if strings.TrimSpace(string(scope.Kind)) == "" && input.Key.ChatID == 0 {
		scope = NormalizeScopeRef(input.Key.Scope)
	}
	groundRefs := encodeJudgmentDependencyRefs(input.GroundRefs)
	if _, err := tx.Exec(`
		INSERT INTO judgment_challenge_events(
			event_id, challenge_id, judgment_id, session_id, chat_id, user_id, scope_kind, scope_id, durable_agent_id,
			event_kind, ground_refs_json, disposition, eligibility_status, operational_response, reason, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, input.EventID, input.ChallengeID, input.JudgmentID, input.SessionID, input.Key.ChatID, input.Key.UserID,
		string(scope.Kind), scope.ID, scope.DurableAgentID, string(input.EventKind), groundRefs, string(input.Disposition),
		string(input.EligibilityStatus), string(input.OperationalResponse), input.Reason, input.CreatedAt.UTC().Format(time.RFC3339Nano)); err != nil {
		return JudgmentChallengeEvent{}, fmt.Errorf("append judgment challenge event %s: %w", input.EventID, err)
	}
	event, ok, err := judgmentChallengeEventByIDTx(tx, input.EventID)
	if err != nil {
		return JudgmentChallengeEvent{}, err
	}
	if !ok {
		return JudgmentChallengeEvent{}, fmt.Errorf("judgment challenge event %s disappeared after insert", input.EventID)
	}
	return event, nil
}

func judgmentByIDTx(tx *sql.Tx, id string) (Judgment, bool, error) {
	row := tx.QueryRow(`SELECT `+judgmentColumns()+` FROM judgments WHERE judgment_id = ?`, strings.TrimSpace(id))
	judgment, err := scanJudgment(row)
	if err == sql.ErrNoRows {
		return Judgment{}, false, nil
	}
	if err != nil {
		return Judgment{}, false, err
	}
	return judgment, true, nil
}

func judgmentChallengeEventByIDTx(tx *sql.Tx, id string) (JudgmentChallengeEvent, bool, error) {
	row := tx.QueryRow(`SELECT `+judgmentChallengeEventColumns()+` FROM judgment_challenge_events WHERE event_id = ?`, strings.TrimSpace(id))
	event, err := scanJudgmentChallengeEvent(row)
	if err == sql.ErrNoRows {
		return JudgmentChallengeEvent{}, false, nil
	}
	if err != nil {
		return JudgmentChallengeEvent{}, false, err
	}
	return event, true, nil
}

func judgmentImmutableMismatch(judgment Judgment, input JudgmentInput, scope ScopeRef, inputRefs string, unknowns string, dependencyRefs string, faultDomains string) string {
	checks := []struct {
		name string
		got  string
		want string
	}{
		{"session_id", judgment.SessionID, input.SessionID},
		{"scope_kind", string(judgment.Scope.Kind), string(scope.Kind)},
		{"scope_id", judgment.Scope.ID, scope.ID},
		{"durable_agent_id", judgment.Scope.DurableAgentID, scope.DurableAgentID},
		{"operation_id", judgment.OperationID, input.OperationID},
		{"kind", judgment.Kind, input.Kind},
		{"schema_version", judgment.SchemaVersion, input.SchemaVersion},
		{"subject_key", judgment.SubjectKey, input.SubjectKey},
		{"claim_key", judgment.ClaimKey, input.ClaimKey},
		{"interpreter_id", judgment.InterpreterID, input.InterpreterID},
		{"interpreter_version", judgment.InterpreterVersion, input.InterpreterVersion},
		{"input_refs", encodeStringList(judgment.InputRefs), inputRefs},
		{"input_hash", judgment.InputHash, input.InputHash},
		{"result_json", judgment.ResultJSON, input.ResultJSON},
		{"completeness", string(judgment.Completeness), string(input.Completeness)},
		{"unknowns", encodeUnknownPredicates(judgment.Unknowns), unknowns},
		{"dependency_refs", encodeJudgmentDependencyRefs(judgment.DependencyRefs), dependencyRefs},
		{"source_fault_domains", encodeStringList(judgment.SourceFaultDomains), faultDomains},
		{"sensitivity", judgment.Sensitivity, input.Sensitivity},
		{"content_hash", judgment.ContentHash, input.ContentHash},
	}
	for _, check := range checks {
		if strings.TrimSpace(check.got) != strings.TrimSpace(check.want) {
			return check.name
		}
	}
	if judgment.ChatID != input.Key.ChatID {
		return "chat_id"
	}
	if judgment.UserID != input.Key.UserID {
		return "user_id"
	}
	if judgment.TurnRunID != input.TurnRunID {
		return "turn_run_id"
	}
	return ""
}

func judgmentColumns() string {
	return strings.Join([]string{
		"judgment_id", "session_id", "chat_id", "user_id", "scope_kind", "scope_id", "durable_agent_id",
		"turn_run_id", "operation_id", "kind", "schema_version", "subject_key", "claim_key", "interpreter_id",
		"interpreter_version", "input_refs_json", "input_hash", "result_json", "completeness", "unknowns_json",
		"dependency_refs_json", "source_fault_domains_json", "sensitivity", "content_hash", "as_of", "expires_at", "created_at",
	}, ", ")
}

func judgmentChallengeEventColumns() string {
	return strings.Join([]string{
		"event_id", "challenge_id", "judgment_id", "session_id", "chat_id", "user_id", "scope_kind", "scope_id", "durable_agent_id",
		"event_kind", "ground_refs_json", "disposition", "eligibility_status", "operational_response", "reason", "created_at",
	}, ", ")
}

type judgmentScanner interface {
	Scan(dest ...any) error
}

func scanJudgment(scanner judgmentScanner) (Judgment, error) {
	var judgment Judgment
	var scopeKind, scopeID, durableAgentID string
	var inputRefsRaw, unknownsRaw, dependencyRefsRaw, faultDomainsRaw string
	var completeness string
	var asOfRaw, expiresRaw sql.NullString
	var createdRaw string
	if err := scanner.Scan(
		&judgment.ID, &judgment.SessionID, &judgment.ChatID, &judgment.UserID, &scopeKind, &scopeID, &durableAgentID,
		&judgment.TurnRunID, &judgment.OperationID, &judgment.Kind, &judgment.SchemaVersion, &judgment.SubjectKey, &judgment.ClaimKey,
		&judgment.InterpreterID, &judgment.InterpreterVersion, &inputRefsRaw, &judgment.InputHash, &judgment.ResultJSON,
		&completeness, &unknownsRaw, &dependencyRefsRaw, &faultDomainsRaw, &judgment.Sensitivity, &judgment.ContentHash,
		&asOfRaw, &expiresRaw, &createdRaw,
	); err != nil {
		return Judgment{}, err
	}
	judgment.Scope = ScopeRef{Kind: ScopeKind(scopeKind), ID: scopeID, DurableAgentID: durableAgentID}
	judgment.InputRefs = decodeStringList(inputRefsRaw)
	judgment.Completeness = NormalizeJudgmentCompleteness(JudgmentCompleteness(completeness))
	judgment.Unknowns = decodeUnknownPredicates(unknownsRaw)
	judgment.DependencyRefs = decodeJudgmentDependencyRefs(dependencyRefsRaw)
	judgment.SourceFaultDomains = decodeStringList(faultDomainsRaw)
	if raw := nullToString(asOfRaw); raw != "" {
		judgment.AsOf, _ = parseSQLiteTime(raw)
	}
	if raw := nullToString(expiresRaw); raw != "" {
		judgment.ExpiresAt, _ = parseSQLiteTime(raw)
	}
	judgment.CreatedAt, _ = parseSQLiteTime(createdRaw)
	return judgment, nil
}

func scanJudgments(rows *sql.Rows) ([]Judgment, error) {
	var out []Judgment
	for rows.Next() {
		judgment, err := scanJudgment(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, judgment)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate judgments: %w", err)
	}
	return out, nil
}

type judgmentChallengeEventScanner interface {
	Scan(dest ...any) error
}

func scanJudgmentChallengeEvent(scanner judgmentChallengeEventScanner) (JudgmentChallengeEvent, error) {
	var event JudgmentChallengeEvent
	var scopeKind, scopeID, durableAgentID string
	var eventKind, disposition, eligibility, response string
	var groundRefsRaw, createdRaw string
	if err := scanner.Scan(
		&event.EventID, &event.ChallengeID, &event.JudgmentID, &event.SessionID, &event.ChatID, &event.UserID,
		&scopeKind, &scopeID, &durableAgentID, &eventKind, &groundRefsRaw, &disposition, &eligibility, &response, &event.Reason, &createdRaw,
	); err != nil {
		return JudgmentChallengeEvent{}, err
	}
	event.Scope = ScopeRef{Kind: ScopeKind(scopeKind), ID: scopeID, DurableAgentID: durableAgentID}
	event.EventKind = NormalizeJudgmentChallengeEventKind(JudgmentChallengeEventKind(eventKind))
	event.GroundRefs = decodeJudgmentDependencyRefs(groundRefsRaw)
	event.Disposition = NormalizeJudgmentChallengeDisposition(JudgmentChallengeDisposition(disposition))
	event.EligibilityStatus = NormalizeJudgmentEligibilityStatus(JudgmentEligibilityStatus(eligibility))
	event.OperationalResponse = NormalizeJudgmentOperationalResponse(JudgmentOperationalResponse(response))
	event.CreatedAt, _ = parseSQLiteTime(createdRaw)
	return event, nil
}

func scanJudgmentChallengeEvents(rows *sql.Rows) ([]JudgmentChallengeEvent, error) {
	var out []JudgmentChallengeEvent
	for rows.Next() {
		event, err := scanJudgmentChallengeEvent(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate judgment challenge events: %w", err)
	}
	return out, nil
}
