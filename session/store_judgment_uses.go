//go:build linux

package session

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

func (s *SQLiteStore) RecordJudgmentUseCommitment(input JudgmentUseInput) (JudgmentUse, error) {
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
	use, err := recordJudgmentUseCommitmentTx(tx, input)
	if err != nil {
		return JudgmentUse{}, err
	}
	if err := tx.Commit(); err != nil {
		return JudgmentUse{}, fmt.Errorf("commit judgment use tx: %w", err)
	}
	return use, nil
}

func (s *SQLiteStore) UpsertJudgmentUse(input JudgmentUseInput) (JudgmentUse, error) {
	return s.RecordJudgmentUseCommitment(input)
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
	use, err := recordJudgmentUseCommitmentTx(tx, useInput)
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

func (s *SQLiteStore) JudgmentUsesByJudgmentRef(judgmentID string, limit int) ([]JudgmentUse, error) {
	if s == nil || s.db == nil {
		return nil, nil
	}
	ref := JudgmentRef(judgmentID)
	if ref == "" {
		return nil, nil
	}
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.Query(`
		SELECT `+judgmentUseColumns()+`
		FROM judgment_uses AS ju
		WHERE EXISTS (
			SELECT 1
			FROM json_each(ju.judgment_refs_json)
			WHERE value = ?
		)
		ORDER BY updated_at DESC, use_id DESC
		LIMIT ?
	`, ref, limit)
	if err != nil {
		return nil, fmt.Errorf("query judgment uses by judgment ref: %w", err)
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

func (s *SQLiteStore) MarkJudgmentUsesForJudgmentReconciliation(judgmentID string, status JudgmentUseReconciliationStatus, reason string, at time.Time) error {
	if s == nil || s.db == nil {
		return nil
	}
	ref := JudgmentRef(judgmentID)
	if ref == "" {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin mark judgment reconciliation tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := markJudgmentUsesForJudgmentRefReconciliationTx(tx, ref, status, reason, at); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit mark judgment reconciliation tx: %w", err)
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

func markJudgmentUsesForJudgmentRefReconciliationTx(tx *sql.Tx, judgmentRef string, status JudgmentUseReconciliationStatus, reason string, at time.Time) error {
	judgmentRef = strings.TrimSpace(judgmentRef)
	if judgmentRef == "" {
		return nil
	}
	status = NormalizeJudgmentUseReconciliation(status)
	if strings.TrimSpace(reason) == "" {
		reason = "judgment reconciliation requested"
	}
	if at.IsZero() {
		at = time.Now().UTC()
	}
	if _, err := tx.Exec(`
		UPDATE judgment_uses
		SET reconciliation_status = ?, reason = CASE
			WHEN reason = '' THEN ?
			ELSE reason || '; ' || ?
		END, updated_at = ?
		WHERE EXISTS (
			SELECT 1
			FROM json_each(judgment_uses.judgment_refs_json)
			WHERE value = ?
		)
	`, string(status), strings.TrimSpace(reason), strings.TrimSpace(reason), at.UTC().Format(time.RFC3339Nano), judgmentRef); err != nil {
		return fmt.Errorf("mark judgment uses by judgment ref reconciliation: %w", err)
	}
	return nil
}

func recordJudgmentUseCommitmentTx(tx *sql.Tx, input JudgmentUseInput) (JudgmentUse, error) {
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
	result, err := tx.Exec(`
		INSERT INTO judgment_uses(
			use_id, session_id, chat_id, user_id, scope_kind, scope_id, durable_agent_id,
			turn_run_id, operation_id, phase_id, lease_id, proposal_id, consumer_id, consequence,
			judgment_refs_json, dependency_refs_json, policy_ref, dependency_snapshot, result_ref, irreversible,
			qualification_status, reconciliation_status, reason, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, input.ID, input.SessionID, input.Key.ChatID, input.Key.UserID, string(scope.Kind), scope.ID, scope.DurableAgentID,
		input.TurnRunID, input.OperationID, input.PhaseID, input.LeaseID, input.ProposalID, input.ConsumerID, string(input.Consequence),
		judgmentRefs, dependencyRefs, input.PolicyRef, input.DependencySnapshot, input.ResultRef, boolToInt(input.Irreversible),
		string(input.QualificationStatus), string(input.ReconciliationStatus), input.Reason, input.CreatedAt.UTC().Format(time.RFC3339Nano), input.UpdatedAt.UTC().Format(time.RFC3339Nano))
	if err != nil && !strings.Contains(err.Error(), "UNIQUE constraint failed") {
		return JudgmentUse{}, fmt.Errorf("record judgment use %s: %w", input.ID, err)
	}
	if err != nil {
		use, ok, loadErr := judgmentUseByIDTx(tx, input.ID)
		if loadErr != nil {
			return JudgmentUse{}, loadErr
		}
		if !ok {
			return JudgmentUse{}, fmt.Errorf("judgment use %s conflicted but existing row was not found", input.ID)
		}
		if mismatch := judgmentUseImmutableMismatch(use, input, scope, judgmentRefs, dependencyRefs); mismatch != "" {
			return JudgmentUse{}, fmt.Errorf("judgment use %s immutable commitment mismatch: %s", input.ID, mismatch)
		}
		return use, nil
	}
	if result != nil {
		if affected, _ := result.RowsAffected(); affected == 0 {
			use, ok, loadErr := judgmentUseByIDTx(tx, input.ID)
			if loadErr != nil {
				return JudgmentUse{}, loadErr
			}
			if !ok {
				return JudgmentUse{}, fmt.Errorf("judgment use %s insert ignored but existing row was not found", input.ID)
			}
			if mismatch := judgmentUseImmutableMismatch(use, input, scope, judgmentRefs, dependencyRefs); mismatch != "" {
				return JudgmentUse{}, fmt.Errorf("judgment use %s immutable commitment mismatch: %s", input.ID, mismatch)
			}
			return use, nil
		}
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

func judgmentUseImmutableMismatch(use JudgmentUse, input JudgmentUseInput, scope ScopeRef, judgmentRefs string, dependencyRefs string) string {
	checks := []struct {
		name string
		got  string
		want string
	}{
		{"session_id", use.SessionID, input.SessionID},
		{"scope_kind", string(use.Scope.Kind), string(scope.Kind)},
		{"scope_id", use.Scope.ID, scope.ID},
		{"durable_agent_id", use.Scope.DurableAgentID, scope.DurableAgentID},
		{"operation_id", use.OperationID, input.OperationID},
		{"phase_id", use.PhaseID, input.PhaseID},
		{"lease_id", use.LeaseID, input.LeaseID},
		{"proposal_id", use.ProposalID, input.ProposalID},
		{"consumer_id", use.ConsumerID, input.ConsumerID},
		{"consequence", string(use.Consequence), string(input.Consequence)},
		{"judgment_refs", encodeStringList(use.JudgmentRefs), judgmentRefs},
		{"dependency_refs", encodeJudgmentDependencyRefs(use.DependencyRefs), dependencyRefs},
		{"policy_ref", use.PolicyRef, input.PolicyRef},
		{"dependency_snapshot", use.DependencySnapshot, input.DependencySnapshot},
		{"result_ref", use.ResultRef, input.ResultRef},
		{"qualification_status", string(use.QualificationStatus), string(input.QualificationStatus)},
	}
	for _, check := range checks {
		if strings.TrimSpace(check.got) != strings.TrimSpace(check.want) {
			return check.name
		}
	}
	if use.ChatID != input.Key.ChatID {
		return "chat_id"
	}
	if use.UserID != input.Key.UserID {
		return "user_id"
	}
	if use.TurnRunID != input.TurnRunID {
		return "turn_run_id"
	}
	if use.Irreversible != input.Irreversible {
		return "irreversible"
	}
	return ""
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
