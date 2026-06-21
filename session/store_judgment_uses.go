//go:build linux

package session

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

func (s *SQLiteStore) UpsertJudgmentUse(input JudgmentUseInput) (JudgmentUse, error) {
	if s == nil || s.db == nil {
		return JudgmentUse{}, fmt.Errorf("judgment use store unavailable")
	}
	input, err := NormalizeJudgmentUseInput(input)
	if err != nil {
		return JudgmentUse{}, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return JudgmentUse{}, fmt.Errorf("begin judgment use tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	use, err := upsertJudgmentUseTx(tx, input)
	if err != nil {
		return JudgmentUse{}, err
	}
	if err := tx.Commit(); err != nil {
		return JudgmentUse{}, fmt.Errorf("commit judgment use tx: %w", err)
	}
	return use, nil
}

func (s *SQLiteStore) UpsertEffectAttemptWithJudgmentUse(attemptInput EffectAttemptInput, useInput JudgmentUseInput) (EffectAttempt, JudgmentUse, error) {
	if s == nil || s.db == nil {
		return EffectAttempt{}, JudgmentUse{}, fmt.Errorf("effect attempt store unavailable")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return EffectAttempt{}, JudgmentUse{}, fmt.Errorf("begin effect attempt judgment use tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	attempt, err := upsertEffectAttemptTx(tx, attemptInput)
	if err != nil {
		return EffectAttempt{}, JudgmentUse{}, err
	}
	if strings.TrimSpace(useInput.ResultRef) == "" {
		useInput.ResultRef = JudgmentUseRef("effect_attempt", attempt.AttemptID)
	}
	useInput.Key = attemptInput.Key
	if useInput.SessionID == "" {
		useInput.SessionID = attempt.SessionID
	}
	use, err := upsertJudgmentUseTx(tx, useInput)
	if err != nil {
		return EffectAttempt{}, JudgmentUse{}, err
	}
	if err := tx.Commit(); err != nil {
		return EffectAttempt{}, JudgmentUse{}, fmt.Errorf("commit effect attempt judgment use tx: %w", err)
	}
	return attempt, use, nil
}

func (s *SQLiteStore) JudgmentUsesBySession(key SessionKey, limit int) ([]JudgmentUse, error) {
	if s == nil || s.db == nil {
		return nil, nil
	}
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.Query(`
		SELECT `+judgmentUseColumns()+`
		FROM judgment_uses
		WHERE session_id = ?
		ORDER BY updated_at DESC, use_id DESC
		LIMIT ?
	`, SessionIDForKey(key), limit)
	if err != nil {
		return nil, fmt.Errorf("query judgment uses by session: %w", err)
	}
	defer rows.Close()
	return scanJudgmentUses(rows)
}

func (s *SQLiteStore) JudgmentUsesByResultRef(resultRef string, limit int) ([]JudgmentUse, error) {
	if s == nil || s.db == nil {
		return nil, nil
	}
	resultRef = strings.TrimSpace(resultRef)
	if resultRef == "" {
		return nil, nil
	}
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.db.Query(`
		SELECT `+judgmentUseColumns()+`
		FROM judgment_uses
		WHERE result_ref = ?
		ORDER BY updated_at DESC, use_id DESC
		LIMIT ?
	`, resultRef, limit)
	if err != nil {
		return nil, fmt.Errorf("query judgment uses by result ref: %w", err)
	}
	defer rows.Close()
	return scanJudgmentUses(rows)
}

func (s *SQLiteStore) MarkJudgmentUsesForResultRefReconciliation(resultRef string, status JudgmentUseReconciliationStatus, reason string, at time.Time) error {
	if s == nil || s.db == nil {
		return nil
	}
	resultRef = strings.TrimSpace(resultRef)
	if resultRef == "" {
		return nil
	}
	status = NormalizeJudgmentUseReconciliation(status)
	if at.IsZero() {
		at = time.Now().UTC()
	}
	if _, err := s.db.Exec(`
		UPDATE judgment_uses
		SET reconciliation_status = ?, reason = CASE WHEN ? != '' THEN ? ELSE reason END, updated_at = ?
		WHERE result_ref = ?
	`, string(status), strings.TrimSpace(reason), strings.TrimSpace(reason), at.UTC().Format(time.RFC3339Nano), resultRef); err != nil {
		return fmt.Errorf("mark judgment use reconciliation: %w", err)
	}
	return nil
}

func markJudgmentUsesForResultRefReconciliationTx(tx *sql.Tx, resultRef string, status JudgmentUseReconciliationStatus, reason string, at time.Time) error {
	resultRef = strings.TrimSpace(resultRef)
	if tx == nil || resultRef == "" {
		return nil
	}
	status = NormalizeJudgmentUseReconciliation(status)
	if at.IsZero() {
		at = time.Now().UTC()
	}
	if _, err := tx.Exec(`
		UPDATE judgment_uses
		SET reconciliation_status = ?, reason = CASE WHEN ? != '' THEN ? ELSE reason END, updated_at = ?
		WHERE result_ref = ?
	`, string(status), strings.TrimSpace(reason), strings.TrimSpace(reason), at.UTC().Format(time.RFC3339Nano), resultRef); err != nil {
		return fmt.Errorf("mark judgment use reconciliation: %w", err)
	}
	return nil
}

func upsertJudgmentUseTx(tx *sql.Tx, input JudgmentUseInput) (JudgmentUse, error) {
	input, err := NormalizeJudgmentUseInput(input)
	if err != nil {
		return JudgmentUse{}, err
	}
	scope := defaultScopeForKey(input.Key)
	if strings.TrimSpace(string(scope.Kind)) == "" && input.Key.ChatID == 0 {
		scope = NormalizeScopeRef(input.Key.Scope)
	}
	judgmentRefs := encodeStringList(input.JudgmentRefs)
	dependencyRefs := encodeJudgmentDependencyRefs(input.DependencyRefs)
	if _, err := tx.Exec(`
		INSERT INTO judgment_uses(
			use_id, session_id, chat_id, user_id, scope_kind, scope_id, durable_agent_id,
			turn_run_id, operation_id, phase_id, lease_id, proposal_id, consumer_id, consequence,
			judgment_refs_json, dependency_refs_json, policy_ref, dependency_snapshot, result_ref, irreversible,
			qualification_status, reconciliation_status, reason, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(use_id) DO UPDATE SET
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
			consumer_id = excluded.consumer_id,
			consequence = excluded.consequence,
			judgment_refs_json = excluded.judgment_refs_json,
			dependency_refs_json = excluded.dependency_refs_json,
			policy_ref = excluded.policy_ref,
			dependency_snapshot = excluded.dependency_snapshot,
			result_ref = excluded.result_ref,
			irreversible = excluded.irreversible,
			qualification_status = excluded.qualification_status,
			reconciliation_status = excluded.reconciliation_status,
			reason = excluded.reason,
			updated_at = excluded.updated_at
	`, input.ID, input.SessionID, input.Key.ChatID, input.Key.UserID, string(scope.Kind), scope.ID, scope.DurableAgentID,
		input.TurnRunID, input.OperationID, input.PhaseID, input.LeaseID, input.ProposalID, input.ConsumerID, string(input.Consequence),
		judgmentRefs, dependencyRefs, input.PolicyRef, input.DependencySnapshot, input.ResultRef, boolToInt(input.Irreversible),
		string(input.QualificationStatus), string(input.ReconciliationStatus), input.Reason, input.CreatedAt.UTC().Format(time.RFC3339Nano), input.UpdatedAt.UTC().Format(time.RFC3339Nano)); err != nil {
		return JudgmentUse{}, fmt.Errorf("upsert judgment use %s: %w", input.ID, err)
	}
	use, ok, err := judgmentUseByIDTx(tx, input.ID)
	if err != nil {
		return JudgmentUse{}, err
	}
	if !ok {
		return JudgmentUse{}, fmt.Errorf("judgment use %s disappeared after upsert", input.ID)
	}
	return use, nil
}

func judgmentUseByIDTx(tx *sql.Tx, id string) (JudgmentUse, bool, error) {
	row := tx.QueryRow(`SELECT `+judgmentUseColumns()+` FROM judgment_uses WHERE use_id = ?`, strings.TrimSpace(id))
	use, err := scanJudgmentUse(row)
	if err == sql.ErrNoRows {
		return JudgmentUse{}, false, nil
	}
	if err != nil {
		return JudgmentUse{}, false, err
	}
	return use, true, nil
}

func judgmentUseColumns() string {
	return `use_id, session_id, chat_id, user_id, scope_kind, scope_id, durable_agent_id,
		turn_run_id, operation_id, phase_id, lease_id, proposal_id, consumer_id, consequence,
		judgment_refs_json, dependency_refs_json, policy_ref, dependency_snapshot, result_ref, irreversible,
		qualification_status, reconciliation_status, reason, created_at, updated_at`
}

type judgmentUseScanner interface {
	Scan(dest ...any) error
}

func scanJudgmentUse(scanner judgmentUseScanner) (JudgmentUse, error) {
	var use JudgmentUse
	var scopeKind, scopeID, durableAgentID string
	var consequence, qualification, reconciliation string
	var judgmentRefsRaw, dependencyRefsRaw string
	var irreversible int
	var createdRaw, updatedRaw string
	if err := scanner.Scan(
		&use.ID, &use.SessionID, &use.ChatID, &use.UserID, &scopeKind, &scopeID, &durableAgentID,
		&use.TurnRunID, &use.OperationID, &use.PhaseID, &use.LeaseID, &use.ProposalID, &use.ConsumerID, &consequence,
		&judgmentRefsRaw, &dependencyRefsRaw, &use.PolicyRef, &use.DependencySnapshot, &use.ResultRef, &irreversible,
		&qualification, &reconciliation, &use.Reason, &createdRaw, &updatedRaw,
	); err != nil {
		return JudgmentUse{}, err
	}
	use.Scope = ScopeRef{Kind: ScopeKind(scopeKind), ID: scopeID, DurableAgentID: durableAgentID}
	use.Consequence = NormalizeJudgmentUseConsequence(JudgmentUseConsequence(consequence))
	use.JudgmentRefs = decodeStringList(judgmentRefsRaw)
	use.DependencyRefs = decodeJudgmentDependencyRefs(dependencyRefsRaw)
	use.Irreversible = irreversible != 0
	use.QualificationStatus = NormalizeJudgmentUseQualification(JudgmentUseQualificationStatus(qualification))
	use.ReconciliationStatus = NormalizeJudgmentUseReconciliation(JudgmentUseReconciliationStatus(reconciliation))
	use.CreatedAt, _ = parseSQLiteTime(createdRaw)
	use.UpdatedAt, _ = parseSQLiteTime(updatedRaw)
	return use, nil
}

func scanJudgmentUses(rows *sql.Rows) ([]JudgmentUse, error) {
	var out []JudgmentUse
	for rows.Next() {
		use, err := scanJudgmentUse(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, use)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate judgment uses: %w", err)
	}
	return out, nil
}
