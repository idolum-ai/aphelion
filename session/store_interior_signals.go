//go:build linux

package session

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

func (s *SQLiteStore) RecordInteriorSignalObservations(key SessionKey, inputs []InteriorSignalObservationInput, now time.Time) ([]InteriorSignalState, error) {
	if s == nil {
		return nil, fmt.Errorf("store is nil")
	}
	key.Scope = defaultScopeForKey(key)
	sessionID := SessionIDForKey(key)
	if sessionID == "" {
		return nil, fmt.Errorf("record interior signal observations: session_id is required")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	normalized := make([]InteriorSignalObservationInput, 0, len(inputs))
	for _, input := range inputs {
		input = NormalizeInteriorSignalObservationInput(input, now)
		if input.Category == "" || input.SubjectKey == "" || input.Summary == "" || input.Weight <= 0 {
			continue
		}
		normalized = append(normalized, input)
	}

	tx, err := s.db.Begin()
	if err != nil {
		return nil, fmt.Errorf("begin interior signal tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if err := decayInteriorSignalStatesTx(tx, key, now); err != nil {
		return nil, err
	}
	for _, input := range normalized {
		if err := recordInteriorSignalObservationTx(tx, key, sessionID, input, now); err != nil {
			return nil, err
		}
	}
	states, err := interiorSignalStatesTx(tx, key)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit interior signal tx: %w", err)
	}
	return states, nil
}

func (s *SQLiteStore) InteriorSignalStates(key SessionKey, now time.Time) ([]InteriorSignalState, error) {
	if s == nil {
		return nil, fmt.Errorf("store is nil")
	}
	key.Scope = defaultScopeForKey(key)
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	tx, err := s.db.Begin()
	if err != nil {
		return nil, fmt.Errorf("begin interior signal state tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := decayInteriorSignalStatesTx(tx, key, now); err != nil {
		return nil, err
	}
	states, err := interiorSignalStatesTx(tx, key)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit interior signal state tx: %w", err)
	}
	return states, nil
}

func (s *SQLiteStore) MarkInteriorSignalsSurfaced(key SessionKey, refs []InteriorSignalRef, now time.Time) error {
	if s == nil {
		return fmt.Errorf("store is nil")
	}
	key.Scope = defaultScopeForKey(key)
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	sessionID := SessionIDForKey(key)
	if sessionID == "" || len(refs) == 0 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin mark interior signal surfaced tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := decayInteriorSignalStatesTx(tx, key, now); err != nil {
		return err
	}
	cooldownUntil := now.Add(InteriorSignalDefaultCooldown).UTC().Format(time.RFC3339Nano)
	for _, ref := range refs {
		ref = NormalizeInteriorSignalRef(ref)
		if ref.Category == "" || ref.SubjectKey == "" {
			continue
		}
		if _, err := tx.Exec(`
			UPDATE interior_signal_states
			SET last_surfaced_at = ?,
				cooldown_until = ?,
				updated_at = ?
			WHERE session_id = ? AND category = ? AND subject_key = ?
		`, now.Format(time.RFC3339Nano), cooldownUntil, now.Format(time.RFC3339Nano), sessionID, ref.Category, ref.SubjectKey); err != nil {
			return fmt.Errorf("mark interior signal surfaced: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit mark interior signal surfaced: %w", err)
	}
	return nil
}

func recordInteriorSignalObservationTx(tx *sql.Tx, key SessionKey, sessionID string, input InteriorSignalObservationInput, now time.Time) error {
	evidenceJSON, err := encodeInteriorSignalEvidence(input.Evidence)
	if err != nil {
		return err
	}
	applied := input.Weight
	threshold := input.ObservedAt.Add(-InteriorSignalDefaultDedupeWindow).UTC().Format(time.RFC3339Nano)
	var existing int
	if err := tx.QueryRow(`
		SELECT COUNT(*)
		FROM interior_signal_observations
		WHERE session_id = ?
			AND category = ?
			AND subject_key = ?
			AND source_fingerprint = ?
			AND observed_at >= ?
	`, sessionID, input.Category, input.SubjectKey, input.SourceFingerprint, threshold).Scan(&existing); err != nil {
		return fmt.Errorf("check interior signal fingerprint: %w", err)
	}
	if existing > 0 {
		applied = 0
	}
	if _, err := tx.Exec(`
		INSERT INTO interior_signal_observations(
			session_id, chat_id, user_id, scope_kind, scope_id, scope_durable_agent_id,
			category, subject_key, summary, source, evidence_json, source_fingerprint,
			weight, applied_weight, confidence, observed_at, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, sessionID, key.ChatID, key.UserID, string(key.Scope.Kind), key.Scope.ID, key.Scope.DurableAgentID,
		input.Category, input.SubjectKey, input.Summary, input.Source, evidenceJSON, input.SourceFingerprint,
		input.Weight, applied, input.Confidence, input.ObservedAt.UTC().Format(time.RFC3339Nano), now.Format(time.RFC3339Nano)); err != nil {
		return fmt.Errorf("insert interior signal observation: %w", err)
	}
	if applied <= 0 {
		return nil
	}
	state, ok, err := interiorSignalStateTx(tx, sessionID, input.Category, input.SubjectKey)
	if err != nil {
		return err
	}
	intensity := clampInteriorSignal(applied)
	count := 1
	confidence := input.Confidence
	createdAt := now
	if ok {
		intensity = clampInteriorSignal(state.Intensity + applied)
		count = state.ObservationCount + 1
		confidence = clampInteriorSignal(maxFloat64(state.Confidence, input.Confidence, 0.2+float64(count)*0.15))
		createdAt = nonZeroTimeOrNow(state.CreatedAt, now)
	}
	if confidence == 0 {
		confidence = clampInteriorSignal(0.2 + float64(count)*0.15)
	}
	if ok {
		_, err = tx.Exec(`
			UPDATE interior_signal_states
			SET chat_id = ?,
				user_id = ?,
				scope_kind = ?,
				scope_id = ?,
				scope_durable_agent_id = ?,
				summary = ?,
				evidence_json = ?,
				intensity = ?,
				confidence = ?,
				observation_count = ?,
				last_observed_at = ?,
				last_decayed_at = ?,
				updated_at = ?
			WHERE session_id = ? AND category = ? AND subject_key = ?
		`, key.ChatID, key.UserID, string(key.Scope.Kind), key.Scope.ID, key.Scope.DurableAgentID,
			input.Summary, evidenceJSON, intensity, confidence, count,
			input.ObservedAt.UTC().Format(time.RFC3339Nano), now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano),
			sessionID, input.Category, input.SubjectKey)
		if err != nil {
			return fmt.Errorf("update interior signal state: %w", err)
		}
		return nil
	}
	if _, err := tx.Exec(`
		INSERT INTO interior_signal_states(
			session_id, chat_id, user_id, scope_kind, scope_id, scope_durable_agent_id,
			category, subject_key, summary, evidence_json, intensity, confidence, observation_count,
			last_observed_at, last_decayed_at, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, sessionID, key.ChatID, key.UserID, string(key.Scope.Kind), key.Scope.ID, key.Scope.DurableAgentID,
		input.Category, input.SubjectKey, input.Summary, evidenceJSON, intensity, confidence, count,
		input.ObservedAt.UTC().Format(time.RFC3339Nano), now.Format(time.RFC3339Nano), createdAt.UTC().Format(time.RFC3339Nano), now.Format(time.RFC3339Nano)); err != nil {
		return fmt.Errorf("insert interior signal state: %w", err)
	}
	return nil
}

func decayInteriorSignalStatesTx(tx *sql.Tx, key SessionKey, now time.Time) error {
	sessionID := SessionIDForKey(key)
	if sessionID == "" {
		return nil
	}
	states, err := interiorSignalStatesTx(tx, key)
	if err != nil {
		return err
	}
	for _, state := range states {
		lastDecayedAt := state.LastDecayedAt
		if lastDecayedAt.IsZero() {
			lastDecayedAt = state.UpdatedAt
		}
		decayed := DecayInteriorSignalIntensity(state.Intensity, lastDecayedAt, now)
		if decayed == state.Intensity && !lastDecayedAt.IsZero() {
			continue
		}
		if _, err := tx.Exec(`
			UPDATE interior_signal_states
			SET intensity = ?,
				last_decayed_at = ?,
				updated_at = ?
			WHERE session_id = ? AND category = ? AND subject_key = ?
		`, decayed, now.UTC().Format(time.RFC3339Nano), now.UTC().Format(time.RFC3339Nano), sessionID, state.Category, state.SubjectKey); err != nil {
			return fmt.Errorf("decay interior signal state: %w", err)
		}
	}
	return nil
}

func interiorSignalStatesTx(tx *sql.Tx, key SessionKey) ([]InteriorSignalState, error) {
	sessionID := SessionIDForKey(key)
	if sessionID == "" {
		return nil, nil
	}
	rows, err := tx.Query(interiorSignalStateSelectSQL()+`
		WHERE session_id = ?
		ORDER BY intensity DESC, updated_at DESC, category ASC, subject_key ASC
	`, sessionID)
	if err != nil {
		return nil, fmt.Errorf("query interior signal states: %w", err)
	}
	defer rows.Close()
	out := make([]InteriorSignalState, 0, 8)
	for rows.Next() {
		state, err := scanInteriorSignalState(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, state)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate interior signal states: %w", err)
	}
	return out, nil
}

func interiorSignalStateTx(tx *sql.Tx, sessionID, category, subjectKey string) (InteriorSignalState, bool, error) {
	row := tx.QueryRow(interiorSignalStateSelectSQL()+`
		WHERE session_id = ? AND category = ? AND subject_key = ?
	`, sessionID, category, subjectKey)
	state, err := scanInteriorSignalState(row)
	if errors.Is(err, sql.ErrNoRows) {
		return InteriorSignalState{}, false, nil
	}
	if err != nil {
		return InteriorSignalState{}, false, err
	}
	return state, true, nil
}

func interiorSignalStateSelectSQL() string {
	return `SELECT session_id, chat_id, user_id, scope_kind, scope_id, scope_durable_agent_id,
		category, subject_key, summary, evidence_json, intensity, confidence, observation_count,
		last_observed_at, last_decayed_at, last_surfaced_at, cooldown_until, created_at, updated_at
		FROM interior_signal_states`
}

func scanInteriorSignalState(scanner interface {
	Scan(dest ...any) error
}) (InteriorSignalState, error) {
	var state InteriorSignalState
	var scopeKind, scopeID, scopeDurableAgentID string
	var evidenceRaw string
	var lastObservedRaw, lastDecayedRaw, lastSurfacedRaw, cooldownRaw sql.NullString
	var createdRaw, updatedRaw string
	if err := scanner.Scan(
		&state.SessionID,
		&state.ChatID,
		&state.UserID,
		&scopeKind,
		&scopeID,
		&scopeDurableAgentID,
		&state.Category,
		&state.SubjectKey,
		&state.Summary,
		&evidenceRaw,
		&state.Intensity,
		&state.Confidence,
		&state.ObservationCount,
		&lastObservedRaw,
		&lastDecayedRaw,
		&lastSurfacedRaw,
		&cooldownRaw,
		&createdRaw,
		&updatedRaw,
	); err != nil {
		return InteriorSignalState{}, err
	}
	evidence, err := decodeInteriorSignalEvidence(evidenceRaw)
	if err != nil {
		return InteriorSignalState{}, err
	}
	state.Evidence = evidence
	state.Scope = ScopeRef{Kind: ScopeKind(scopeKind), ID: scopeID, DurableAgentID: scopeDurableAgentID}
	state.Intensity = clampInteriorSignal(state.Intensity)
	state.Confidence = clampInteriorSignal(state.Confidence)
	if state.ObservationCount < 0 {
		state.ObservationCount = 0
	}
	if lastObservedRaw.Valid && strings.TrimSpace(lastObservedRaw.String) != "" {
		if state.LastObservedAt, err = parseSQLiteTime(lastObservedRaw.String); err != nil {
			return InteriorSignalState{}, fmt.Errorf("parse interior signal last_observed_at: %w", err)
		}
	}
	if lastDecayedRaw.Valid && strings.TrimSpace(lastDecayedRaw.String) != "" {
		if state.LastDecayedAt, err = parseSQLiteTime(lastDecayedRaw.String); err != nil {
			return InteriorSignalState{}, fmt.Errorf("parse interior signal last_decayed_at: %w", err)
		}
	}
	if lastSurfacedRaw.Valid && strings.TrimSpace(lastSurfacedRaw.String) != "" {
		if state.LastSurfacedAt, err = parseSQLiteTime(lastSurfacedRaw.String); err != nil {
			return InteriorSignalState{}, fmt.Errorf("parse interior signal last_surfaced_at: %w", err)
		}
	}
	if cooldownRaw.Valid && strings.TrimSpace(cooldownRaw.String) != "" {
		if state.CooldownUntil, err = parseSQLiteTime(cooldownRaw.String); err != nil {
			return InteriorSignalState{}, fmt.Errorf("parse interior signal cooldown_until: %w", err)
		}
	}
	if state.CreatedAt, err = parseSQLiteTime(createdRaw); err != nil {
		return InteriorSignalState{}, fmt.Errorf("parse interior signal created_at: %w", err)
	}
	if state.UpdatedAt, err = parseSQLiteTime(updatedRaw); err != nil {
		return InteriorSignalState{}, fmt.Errorf("parse interior signal updated_at: %w", err)
	}
	state.Category = normalizeInteriorSignalToken(state.Category)
	state.SubjectKey = normalizeInteriorSignalSubject(state.SubjectKey)
	return state, nil
}

func maxFloat64(values ...float64) float64 {
	var out float64
	for _, value := range values {
		if value > out {
			out = value
		}
	}
	return out
}
