//go:build linux

package session

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

func (s *SQLiteStore) UpsertEffectAttempt(input EffectAttemptInput) (EffectAttempt, error) {
	if s == nil || s.db == nil {
		return EffectAttempt{}, fmt.Errorf("effect attempt store unavailable")
	}
	input = NormalizeEffectAttemptInput(input)
	if strings.TrimSpace(input.AttemptID) == "" {
		return EffectAttempt{}, fmt.Errorf("effect attempt requires attempt_id")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return EffectAttempt{}, fmt.Errorf("begin effect attempt tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	attempt, err := upsertEffectAttemptTx(tx, input)
	if err != nil {
		return EffectAttempt{}, err
	}
	if err := tx.Commit(); err != nil {
		return EffectAttempt{}, fmt.Errorf("commit effect attempt tx: %w", err)
	}
	return attempt, nil
}

func (s *SQLiteStore) EffectAttemptsByTurnRun(key SessionKey, turnRunID int64) ([]EffectAttempt, error) {
	if s == nil || s.db == nil || turnRunID <= 0 {
		return nil, nil
	}
	rows, err := s.db.Query(`
		SELECT `+effectAttemptColumns()+`
		FROM effect_attempts
		WHERE session_id = ? AND turn_run_id = ?
		ORDER BY started_at ASC, attempt_id ASC
	`, SessionIDForKey(key), turnRunID)
	if err != nil {
		return nil, fmt.Errorf("query effect attempts by turn run: %w", err)
	}
	defer rows.Close()
	return scanEffectAttempts(rows)
}

func (s *SQLiteStore) EffectAttemptsForWork(key SessionKey, operationID string, phaseID string, leaseID string, proposalID string) ([]EffectAttempt, error) {
	if s == nil || s.db == nil {
		return nil, nil
	}
	rows, err := s.db.Query(`
		SELECT `+effectAttemptColumns()+`
		FROM effect_attempts
		WHERE session_id = ?
			AND (? = '' OR operation_id = ?)
			AND (? = '' OR phase_id = ?)
			AND (? = '' OR lease_id = ?)
			AND (? = '' OR proposal_id = ?)
		ORDER BY started_at ASC, attempt_id ASC
	`, SessionIDForKey(key), strings.TrimSpace(operationID), strings.TrimSpace(operationID), strings.TrimSpace(phaseID), strings.TrimSpace(phaseID), strings.TrimSpace(leaseID), strings.TrimSpace(leaseID), strings.TrimSpace(proposalID), strings.TrimSpace(proposalID))
	if err != nil {
		return nil, fmt.Errorf("query effect attempts for work: %w", err)
	}
	defer rows.Close()
	return scanEffectAttempts(rows)
}

func (s *SQLiteStore) UnresolvedSideEffectAttemptsForWork(key SessionKey, operationID string, phaseID string, leaseID string, proposalID string) ([]EffectAttempt, error) {
	attempts, err := s.EffectAttemptsForWork(key, operationID, phaseID, leaseID, proposalID)
	if err != nil {
		return nil, err
	}
	var out []EffectAttempt
	for _, attempt := range attempts {
		if EffectAttemptHasSideEffects(attempt) && EffectAttemptStatusRetryBlocking(attempt.Status) {
			out = append(out, attempt)
		}
	}
	return out, nil
}

func upsertEffectAttemptTx(tx *sql.Tx, input EffectAttemptInput) (EffectAttempt, error) {
	input = NormalizeEffectAttemptInput(input)
	existing, ok, err := effectAttemptByIDTx(tx, input.AttemptID)
	if err != nil {
		return EffectAttempt{}, err
	}
	if ok && EffectAttemptStatusTerminal(existing.Status) && input.Status != EffectAttemptStatusSuperseded {
		return existing, nil
	}
	scope := defaultScopeForKey(input.Key)
	sessionID := SessionIDForKey(input.Key)
	startedAt := nullableTime(input.StartedAt)
	completedAt := nullableTime(input.CompletedAt)
	evidenceRefs := append([]string(nil), input.EvidenceRefs...)
	if ok {
		evidenceRefs = append(evidenceRefs, existing.EvidenceRefs...)
		evidenceRefs = normalizeStringList(evidenceRefs)
		if input.StartedAt.IsZero() {
			input.StartedAt = existing.StartedAt
			startedAt = nullableTime(input.StartedAt)
		}
		if input.CompletedAt.IsZero() {
			input.CompletedAt = existing.CompletedAt
			completedAt = nullableTime(input.CompletedAt)
		}
		if input.OperationID == "" {
			input.OperationID = existing.OperationID
		}
		if input.PhaseID == "" {
			input.PhaseID = existing.PhaseID
		}
		if input.LeaseID == "" {
			input.LeaseID = existing.LeaseID
		}
		if input.ProposalID == "" {
			input.ProposalID = existing.ProposalID
		}
		if input.WorkMode == "" {
			input.WorkMode = existing.WorkMode
		}
		if input.Executor == "" {
			input.Executor = existing.Executor
		}
		if input.Tool == "" {
			input.Tool = existing.Tool
		}
		if input.Command == "" {
			input.Command = existing.Command
		}
		if input.EffectKind == "" {
			input.EffectKind = existing.EffectKind
		}
		if input.EffectReason == "" {
			input.EffectReason = existing.EffectReason
		}
		if input.BoundaryKind == "" {
			input.BoundaryKind = existing.BoundaryKind
		}
		if input.AuthorizationReason == "" {
			input.AuthorizationReason = existing.AuthorizationReason
		}
		if input.SubjectJSON == "{}" {
			input.SubjectJSON = existing.SubjectJSON
		}
		if input.ErrorText == "" {
			input.ErrorText = existing.ErrorText
		}
	}
	commandHash := ""
	if strings.TrimSpace(input.Command) != "" {
		commandHash = EffectAttemptCommandHash(input.Command)
	}
	if _, err := tx.Exec(`
		INSERT INTO effect_attempts(
			attempt_id, session_id, chat_id, user_id, scope_kind, scope_id, durable_agent_id,
			turn_run_id, operation_id, phase_id, lease_id, proposal_id, work_mode, executor, tool,
			command, command_hash, effect_kind, effect_reason, boundary_kind, authorization_reason,
			subject_json, status, error_text, evidence_refs_json, started_at, completed_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(attempt_id) DO UPDATE SET
			session_id = excluded.session_id,
			chat_id = excluded.chat_id,
			user_id = excluded.user_id,
			scope_kind = excluded.scope_kind,
			scope_id = excluded.scope_id,
			durable_agent_id = excluded.durable_agent_id,
			turn_run_id = excluded.turn_run_id,
			operation_id = excluded.operation_id,
			phase_id = excluded.phase_id,
			lease_id = excluded.lease_id,
			proposal_id = excluded.proposal_id,
			work_mode = excluded.work_mode,
			executor = excluded.executor,
			tool = excluded.tool,
			command = excluded.command,
			command_hash = excluded.command_hash,
			effect_kind = excluded.effect_kind,
			effect_reason = excluded.effect_reason,
			boundary_kind = excluded.boundary_kind,
			authorization_reason = excluded.authorization_reason,
			subject_json = excluded.subject_json,
			status = excluded.status,
			error_text = excluded.error_text,
			evidence_refs_json = excluded.evidence_refs_json,
			started_at = COALESCE(effect_attempts.started_at, excluded.started_at),
			completed_at = COALESCE(excluded.completed_at, effect_attempts.completed_at),
			updated_at = excluded.updated_at
	`, input.AttemptID, sessionID, input.Key.ChatID, input.Key.UserID, string(scope.Kind), scope.ID, scope.DurableAgentID,
		input.TurnRunID, input.OperationID, input.PhaseID, input.LeaseID, input.ProposalID, input.WorkMode, input.Executor, input.Tool,
		input.Command, commandHash, input.EffectKind, input.EffectReason, input.BoundaryKind, input.AuthorizationReason,
		input.SubjectJSON, string(input.Status), input.ErrorText, encodeStringList(evidenceRefs), startedAt, completedAt, input.UpdatedAt.Format(time.RFC3339Nano)); err != nil {
		return EffectAttempt{}, fmt.Errorf("upsert effect attempt %s: %w", input.AttemptID, err)
	}
	attempt, ok, err := effectAttemptByIDTx(tx, input.AttemptID)
	if err != nil {
		return EffectAttempt{}, err
	}
	if !ok {
		return EffectAttempt{}, fmt.Errorf("effect attempt %s disappeared after upsert", input.AttemptID)
	}
	return attempt, nil
}

func effectAttemptByIDTx(tx *sql.Tx, attemptID string) (EffectAttempt, bool, error) {
	row := tx.QueryRow(`SELECT `+effectAttemptColumns()+` FROM effect_attempts WHERE attempt_id = ?`, strings.TrimSpace(attemptID))
	attempt, err := scanEffectAttempt(row)
	if err == sql.ErrNoRows {
		return EffectAttempt{}, false, nil
	}
	if err != nil {
		return EffectAttempt{}, false, err
	}
	return attempt, true, nil
}

func effectAttemptColumns() string {
	return `attempt_id, session_id, chat_id, user_id, scope_kind, scope_id, durable_agent_id,
		turn_run_id, operation_id, phase_id, lease_id, proposal_id, work_mode, executor, tool,
		command, command_hash, effect_kind, effect_reason, boundary_kind, authorization_reason,
		subject_json, status, error_text, evidence_refs_json, started_at, completed_at, updated_at`
}

type effectAttemptScanner interface {
	Scan(dest ...any) error
}

func scanEffectAttempt(scanner effectAttemptScanner) (EffectAttempt, error) {
	var attempt EffectAttempt
	var scopeKind, scopeID, durableAgentID string
	var status string
	var refsRaw string
	var startedRaw, completedRaw sql.NullString
	var updatedRaw string
	if err := scanner.Scan(
		&attempt.AttemptID, &attempt.SessionID, &attempt.ChatID, &attempt.UserID, &scopeKind, &scopeID, &durableAgentID,
		&attempt.TurnRunID, &attempt.OperationID, &attempt.PhaseID, &attempt.LeaseID, &attempt.ProposalID, &attempt.WorkMode, &attempt.Executor, &attempt.Tool,
		&attempt.Command, &attempt.CommandHash, &attempt.EffectKind, &attempt.EffectReason, &attempt.BoundaryKind, &attempt.AuthorizationReason,
		&attempt.SubjectJSON, &status, &attempt.ErrorText, &refsRaw, &startedRaw, &completedRaw, &updatedRaw,
	); err != nil {
		return EffectAttempt{}, err
	}
	attempt.Scope = ScopeRef{Kind: ScopeKind(scopeKind), ID: scopeID, DurableAgentID: durableAgentID}
	attempt.Status = NormalizeEffectAttemptStatus(EffectAttemptStatus(status))
	attempt.EvidenceRefs = decodeStringList(refsRaw)
	if startedRaw.Valid {
		attempt.StartedAt, _ = parseSQLiteTime(startedRaw.String)
	}
	if completedRaw.Valid {
		attempt.CompletedAt, _ = parseSQLiteTime(completedRaw.String)
	}
	attempt.UpdatedAt, _ = parseSQLiteTime(updatedRaw)
	return attempt, nil
}

func scanEffectAttempts(rows *sql.Rows) ([]EffectAttempt, error) {
	var out []EffectAttempt
	for rows.Next() {
		attempt, err := scanEffectAttempt(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, attempt)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate effect attempts: %w", err)
	}
	return out, nil
}
