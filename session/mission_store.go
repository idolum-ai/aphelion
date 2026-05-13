//go:build linux

package session

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

type MissionLedgerHealth struct {
	ActiveCount                  int
	CandidateCount               int
	PinnedCount                  int
	RecurringCount               int
	BlockedCount                 int
	SelfContinuationEnabledCount int
	StaleCandidateCount          int
	PendingHandoffCount          int
}

type WorkingObjective struct {
	Objective  string    `json:"objective,omitempty"`
	Source     string    `json:"source,omitempty"`
	Confidence string    `json:"confidence,omitempty"`
	CreatedAt  time.Time `json:"created_at,omitempty"`
	ExpiresAt  time.Time `json:"expires_at,omitempty"`
}

func (s *SQLiteStore) UpsertMission(m MissionState, actor string, eventSummary string) (MissionState, error) {
	if s == nil {
		return MissionState{}, fmt.Errorf("store is nil")
	}
	m = NormalizeMissionState(m)
	if m.ID == "" {
		m.ID = generatedMissionID("mission")
	}
	if m.Objective == "" {
		return MissionState{}, fmt.Errorf("mission objective is required")
	}
	if m.Scope == "" {
		m.Scope = "principal"
	}
	if m.Owner == "" {
		m.Owner = "system"
	}
	now := time.Now().UTC()
	m.CreatedAt = nonZeroTimeOrNow(m.CreatedAt, now).UTC()
	m.UpdatedAt = nonZeroTimeOrNow(m.UpdatedAt, now).UTC()
	if m.LastTouchedAt.IsZero() {
		m.LastTouchedAt = m.UpdatedAt
	}
	encoded, err := encodeMissionFields(m)
	if err != nil {
		return MissionState{}, err
	}
	previous, existed, err := s.Mission(m.ID)
	if err != nil {
		return MissionState{}, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return MissionState{}, fmt.Errorf("begin mission upsert tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`
		INSERT INTO mission_ledger(
			id, scope, owner, title, objective, origin, status, pinned, recurrence_json, authority_json, budget_json, decay_json,
			success_criteria_json, evidence_json, current_plan_json, next_allowed_action, blocked_reason, waiting_for,
			tags_json, source_refs_json, created_at, updated_at, last_touched_at, last_summoned_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			scope = excluded.scope,
			owner = excluded.owner,
			title = excluded.title,
			objective = excluded.objective,
			origin = excluded.origin,
			status = excluded.status,
			pinned = excluded.pinned,
			recurrence_json = excluded.recurrence_json,
			authority_json = excluded.authority_json,
			budget_json = excluded.budget_json,
			decay_json = excluded.decay_json,
			success_criteria_json = excluded.success_criteria_json,
			evidence_json = excluded.evidence_json,
			current_plan_json = excluded.current_plan_json,
			next_allowed_action = excluded.next_allowed_action,
			blocked_reason = excluded.blocked_reason,
			waiting_for = excluded.waiting_for,
			tags_json = excluded.tags_json,
			source_refs_json = excluded.source_refs_json,
			updated_at = excluded.updated_at,
			last_touched_at = excluded.last_touched_at,
			last_summoned_at = excluded.last_summoned_at
	`, m.ID, m.Scope, m.Owner, m.Title, m.Objective, m.Origin, string(m.Status), boolToInt(m.Pinned), encoded.recurrence, encoded.authority, encoded.budget, encoded.decay, encoded.successCriteria, encoded.evidence, encoded.plan, m.NextAllowedAction, m.BlockedReason, m.WaitingFor, encoded.tags, encoded.sourceRefs, m.CreatedAt.Format(time.RFC3339Nano), m.UpdatedAt.Format(time.RFC3339Nano), nullableTimeRFC3339(m.LastTouchedAt), nullableTimeRFC3339(m.LastSummonedAt)); err != nil {
		return MissionState{}, fmt.Errorf("upsert mission: %w", err)
	}
	eventType := "mission.created"
	if existed {
		eventType = missionUpdateEvent(previous, m)
	}
	if strings.TrimSpace(eventSummary) == "" {
		eventSummary = eventType
	}
	if err := appendMissionEventTx(tx, MissionEvent{MissionID: m.ID, EventType: eventType, Actor: actor, Summary: eventSummary, CreatedAt: m.UpdatedAt}); err != nil {
		return MissionState{}, err
	}
	if err := tx.Commit(); err != nil {
		return MissionState{}, fmt.Errorf("commit mission upsert tx: %w", err)
	}
	stored, ok, err := s.Mission(m.ID)
	if err != nil {
		return MissionState{}, err
	}
	if !ok {
		return MissionState{}, fmt.Errorf("mission %q not found after upsert", m.ID)
	}
	return stored, nil
}

func (s *SQLiteStore) Mission(id string) (MissionState, bool, error) {
	if s == nil {
		return MissionState{}, false, fmt.Errorf("store is nil")
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return MissionState{}, false, nil
	}
	row := s.db.QueryRow(missionSelectSQL()+` WHERE id = ?`, id)
	m, err := scanMission(row)
	if errors.Is(err, sql.ErrNoRows) {
		return MissionState{}, false, nil
	}
	if err != nil {
		return MissionState{}, false, err
	}
	return m, true, nil
}

func (s *SQLiteStore) Missions(filter MissionFilter) ([]MissionState, error) {
	if s == nil {
		return nil, fmt.Errorf("store is nil")
	}
	if filter.Limit <= 0 || filter.Limit > 200 {
		filter.Limit = 50
	}
	filter.Scope = strings.TrimSpace(filter.Scope)
	filter.Owner = strings.TrimSpace(filter.Owner)
	filter.Status = NormalizeMissionStatus(filter.Status)
	query := missionSelectSQL()
	args := make([]any, 0, 5)
	clauses := make([]string, 0, 4)
	if filter.Scope != "" {
		clauses = append(clauses, "scope = ?")
		args = append(args, filter.Scope)
	}
	if filter.Owner != "" {
		clauses = append(clauses, "owner = ?")
		args = append(args, filter.Owner)
	}
	if filter.Status != "" {
		clauses = append(clauses, "status = ?")
		args = append(args, string(filter.Status))
	}
	if filter.Pinned != nil {
		clauses = append(clauses, "pinned = ?")
		args = append(args, boolToInt(*filter.Pinned))
	}
	if len(clauses) > 0 {
		query += " WHERE " + strings.Join(clauses, " AND ")
	}
	query += ` ORDER BY CASE status
		WHEN 'active' THEN 0
		WHEN 'blocked' THEN 1
		WHEN 'candidate' THEN 2
		WHEN 'dormant' THEN 3
		WHEN 'completed' THEN 4
		WHEN 'expired' THEN 5
		WHEN 'archived' THEN 6
		ELSE 7 END, pinned DESC, updated_at DESC, id ASC LIMIT ?`
	args = append(args, filter.Limit)
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("query missions: %w", err)
	}
	defer rows.Close()
	out := make([]MissionState, 0, filter.Limit)
	for rows.Next() {
		m, err := scanMission(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate missions: %w", err)
	}
	return out, nil
}

func (s *SQLiteStore) UpdateMissionStatus(id string, status MissionStatus, actor string, summary string) (MissionState, error) {
	m, ok, err := s.Mission(id)
	if err != nil {
		return MissionState{}, err
	}
	if !ok {
		return MissionState{}, fmt.Errorf("mission %q not found", strings.TrimSpace(id))
	}
	status = NormalizeMissionStatus(status)
	if status == "" {
		return MissionState{}, fmt.Errorf("invalid mission status")
	}
	m.Status = status
	m.UpdatedAt = time.Now().UTC()
	m.LastTouchedAt = m.UpdatedAt
	if status != MissionStatusBlocked {
		m.BlockedReason = ""
	}
	return s.UpsertMission(m, actor, firstMissionNonEmpty(summary, "mission status updated"))
}

func (s *SQLiteStore) SetMissionPinned(id string, pinned bool, actor string, summary string) (MissionState, error) {
	m, ok, err := s.Mission(id)
	if err != nil {
		return MissionState{}, err
	}
	if !ok {
		return MissionState{}, fmt.Errorf("mission %q not found", strings.TrimSpace(id))
	}
	m.Pinned = pinned
	m.UpdatedAt = time.Now().UTC()
	m.LastTouchedAt = m.UpdatedAt
	return s.UpsertMission(m, actor, firstMissionNonEmpty(summary, "mission pinned state updated"))
}

func (s *SQLiteStore) BlockMission(id string, reason string, actor string) (MissionState, error) {
	m, ok, err := s.Mission(id)
	if err != nil {
		return MissionState{}, err
	}
	if !ok {
		return MissionState{}, fmt.Errorf("mission %q not found", strings.TrimSpace(id))
	}
	m.Status = MissionStatusBlocked
	m.BlockedReason = strings.TrimSpace(reason)
	m.UpdatedAt = time.Now().UTC()
	m.LastTouchedAt = m.UpdatedAt
	return s.UpsertMission(m, actor, firstMissionNonEmpty(reason, "mission blocked"))
}

func (s *SQLiteStore) UpdateMissionEvidence(id string, evidence []MissionEvidenceItem, actor string, summary string) (MissionState, error) {
	m, ok, err := s.Mission(id)
	if err != nil {
		return MissionState{}, err
	}
	if !ok {
		return MissionState{}, fmt.Errorf("mission %q not found", strings.TrimSpace(id))
	}
	m.EvidenceChecklist = normalizeMissionEvidence(append(m.EvidenceChecklist, evidence...))
	m.UpdatedAt = time.Now().UTC()
	m.LastTouchedAt = m.UpdatedAt
	return s.UpsertMission(m, actor, firstMissionNonEmpty(summary, "mission evidence updated"))
}

func (s *SQLiteStore) SummonMissions(filter MissionFilter, contextText string, limit int) ([]MissionState, error) {
	if limit <= 0 || limit > 20 {
		limit = 8
	}
	filter.Limit = 200
	missions, err := s.Missions(filter)
	if err != nil {
		return nil, err
	}
	contextText = strings.ToLower(strings.TrimSpace(contextText))
	now := time.Now().UTC()
	type scored struct {
		mission MissionState
		score   int
	}
	scoredMissions := make([]scored, 0, len(missions))
	for _, m := range missions {
		if m.Status == MissionStatusArchived || m.Status == MissionStatusCompleted || m.Status == MissionStatusExpired {
			continue
		}
		score := 0
		if m.Pinned {
			score += 25
		}
		if m.Status == MissionStatusActive {
			score += 30
		}
		if m.Status == MissionStatusBlocked {
			score += 5
		}
		if m.Recurrence != nil {
			score += 20
			if !m.Recurrence.NextDueAt.IsZero() && !m.Recurrence.NextDueAt.After(now) {
				score += 30
			}
		}
		if contextText != "" && missionMatchesContext(m, contextText) {
			score += 40
		}
		if !m.LastSummonedAt.IsZero() && now.Sub(m.LastSummonedAt) < 24*time.Hour {
			score -= 20
		}
		if score <= 0 {
			continue
		}
		scoredMissions = append(scoredMissions, scored{mission: m, score: score})
	}
	sort.Slice(scoredMissions, func(i, j int) bool {
		if scoredMissions[i].score != scoredMissions[j].score {
			return scoredMissions[i].score > scoredMissions[j].score
		}
		return scoredMissions[i].mission.UpdatedAt.After(scoredMissions[j].mission.UpdatedAt)
	})
	out := make([]MissionState, 0, minMissionInt(limit, len(scoredMissions)))
	for i, item := range scoredMissions {
		if i >= limit {
			break
		}
		m := item.mission
		m.LastSummonedAt = now
		m.UpdatedAt = now
		stored, err := s.UpsertMission(m, "system:mission_summon", "mission summoned for review")
		if err != nil {
			return nil, err
		}
		out = append(out, stored)
	}
	return out, nil
}

func (s *SQLiteStore) AppendMissionEvent(event MissionEvent) (MissionEvent, error) {
	if s == nil {
		return MissionEvent{}, fmt.Errorf("store is nil")
	}
	event.EventType = strings.TrimSpace(event.EventType)
	event.MissionID = strings.TrimSpace(event.MissionID)
	if event.MissionID == "" {
		return MissionEvent{}, fmt.Errorf("mission event mission_id is required")
	}
	if event.EventType == "" {
		return MissionEvent{}, fmt.Errorf("mission event event_type is required")
	}
	if event.CreatedAt.IsZero() {
		event.CreatedAt = time.Now().UTC()
	}
	res, err := s.db.Exec(`
		INSERT INTO mission_events(mission_id, event_type, actor, summary, payload_json, created_at)
		VALUES (?, ?, ?, ?, ?, ?)
	`, event.MissionID, event.EventType, strings.TrimSpace(event.Actor), strings.TrimSpace(event.Summary), strings.TrimSpace(event.Payload), event.CreatedAt.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return MissionEvent{}, fmt.Errorf("append mission event: %w", err)
	}
	seq, _ := res.LastInsertId()
	event.Seq = seq
	return event, nil
}

func (s *SQLiteStore) MissionEvents(missionID string, limit int) ([]MissionEvent, error) {
	if s == nil {
		return nil, fmt.Errorf("store is nil")
	}
	missionID = strings.TrimSpace(missionID)
	if missionID == "" {
		return nil, nil
	}
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	rows, err := s.db.Query(`
		SELECT seq, mission_id, event_type, actor, summary, payload_json, created_at
		FROM mission_events
		WHERE mission_id = ?
		ORDER BY seq DESC
		LIMIT ?
	`, missionID, limit)
	if err != nil {
		return nil, fmt.Errorf("query mission events: %w", err)
	}
	defer rows.Close()
	out := make([]MissionEvent, 0, limit)
	for rows.Next() {
		event, err := scanMissionEvent(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate mission events: %w", err)
	}
	return out, nil
}

func (s *SQLiteStore) CreateMissionHandoff(h MissionHandoff) (MissionHandoff, error) {
	if s == nil {
		return MissionHandoff{}, fmt.Errorf("store is nil")
	}
	h.ID = strings.TrimSpace(h.ID)
	if h.ID == "" {
		h.ID = generatedMissionID("handoff")
	}
	h.PlannedAction = strings.TrimSpace(h.PlannedAction)
	h.RecoveryQuestion = strings.TrimSpace(h.RecoveryQuestion)
	if h.PlannedAction == "" {
		return MissionHandoff{}, fmt.Errorf("mission handoff planned_action is required")
	}
	if h.RecoveryQuestion == "" {
		h.RecoveryQuestion = "Did the planned mission action complete, and what evidence proves it?"
	}
	if strings.TrimSpace(h.Status) == "" {
		h.Status = "pending"
	}
	now := time.Now().UTC()
	h.CreatedAt = nonZeroTimeOrNow(h.CreatedAt, now).UTC()
	h.UpdatedAt = nonZeroTimeOrNow(h.UpdatedAt, now).UTC()
	if _, err := s.db.Exec(`
		INSERT INTO mission_handoffs(id, mission_id, operation_id, planned_action, expected_evidence_json, recovery_question, status, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			mission_id = excluded.mission_id,
			operation_id = excluded.operation_id,
			planned_action = excluded.planned_action,
			expected_evidence_json = excluded.expected_evidence_json,
			recovery_question = excluded.recovery_question,
			status = excluded.status,
			updated_at = excluded.updated_at
	`, h.ID, strings.TrimSpace(h.MissionID), strings.TrimSpace(h.OperationID), h.PlannedAction, strings.TrimSpace(h.ExpectedEvidenceJSON), h.RecoveryQuestion, strings.TrimSpace(h.Status), h.CreatedAt.Format(time.RFC3339Nano), h.UpdatedAt.Format(time.RFC3339Nano)); err != nil {
		return MissionHandoff{}, fmt.Errorf("create mission handoff: %w", err)
	}
	return h, nil
}

func (s *SQLiteStore) MissionHandoffs(filter MissionHandoffFilter) ([]MissionHandoff, error) {
	if s == nil {
		return nil, fmt.Errorf("store is nil")
	}
	if filter.Limit <= 0 || filter.Limit > 100 {
		filter.Limit = 20
	}
	filter.MissionID = strings.TrimSpace(filter.MissionID)
	filter.OperationID = strings.TrimSpace(filter.OperationID)
	filter.Status = strings.TrimSpace(filter.Status)
	query := `
		SELECT id, mission_id, operation_id, planned_action, expected_evidence_json, recovery_question, status, created_at, updated_at
		FROM mission_handoffs
	`
	args := make([]any, 0, 4)
	clauses := make([]string, 0, 3)
	if filter.MissionID != "" {
		clauses = append(clauses, "mission_id = ?")
		args = append(args, filter.MissionID)
	}
	if filter.OperationID != "" {
		clauses = append(clauses, "operation_id = ?")
		args = append(args, filter.OperationID)
	}
	if filter.Status != "" {
		clauses = append(clauses, "status = ?")
		args = append(args, filter.Status)
	}
	if len(clauses) > 0 {
		query += " WHERE " + strings.Join(clauses, " AND ")
	}
	query += ` ORDER BY updated_at DESC, created_at DESC, id ASC LIMIT ?`
	args = append(args, filter.Limit)
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("query mission handoffs: %w", err)
	}
	defer rows.Close()
	out := make([]MissionHandoff, 0, filter.Limit)
	for rows.Next() {
		handoff, err := scanMissionHandoff(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, handoff)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate mission handoffs: %w", err)
	}
	return out, nil
}

func (s *SQLiteStore) RecordMissionResult(result MissionResult) (MissionResult, error) {
	if s == nil {
		return MissionResult{}, fmt.Errorf("store is nil")
	}
	result.ID = strings.TrimSpace(result.ID)
	if result.ID == "" {
		result.ID = generatedMissionID("result")
	}
	result.HandoffID = strings.TrimSpace(result.HandoffID)
	result.Status = strings.TrimSpace(result.Status)
	result.Summary = strings.TrimSpace(result.Summary)
	if result.HandoffID == "" {
		return MissionResult{}, fmt.Errorf("mission result handoff_id is required")
	}
	if result.Status == "" {
		return MissionResult{}, fmt.Errorf("mission result status is required")
	}
	if result.Summary == "" {
		return MissionResult{}, fmt.Errorf("mission result summary is required")
	}
	if result.RecordedAt.IsZero() {
		result.RecordedAt = time.Now().UTC()
	}
	tx, err := s.db.Begin()
	if err != nil {
		return MissionResult{}, fmt.Errorf("begin mission result tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`
		INSERT INTO mission_results(id, handoff_id, mission_id, operation_id, status, evidence_refs_json, summary, remaining_risk, recorded_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, result.ID, result.HandoffID, strings.TrimSpace(result.MissionID), strings.TrimSpace(result.OperationID), result.Status, strings.TrimSpace(result.EvidenceRefsJSON), result.Summary, strings.TrimSpace(result.RemainingRisk), result.RecordedAt.UTC().Format(time.RFC3339Nano)); err != nil {
		return MissionResult{}, fmt.Errorf("record mission result: %w", err)
	}
	if _, err := tx.Exec(`UPDATE mission_handoffs SET status = ?, updated_at = ? WHERE id = ?`, result.Status, result.RecordedAt.UTC().Format(time.RFC3339Nano), result.HandoffID); err != nil {
		return MissionResult{}, fmt.Errorf("update mission handoff status: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return MissionResult{}, fmt.Errorf("commit mission result tx: %w", err)
	}
	return result, nil
}

func (s *SQLiteStore) MissionResults(limit int) ([]MissionResult, error) {
	if s == nil {
		return nil, fmt.Errorf("store is nil")
	}
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	rows, err := s.db.Query(`
		SELECT id, handoff_id, mission_id, operation_id, status, evidence_refs_json, summary, remaining_risk, recorded_at
		FROM mission_results
		ORDER BY recorded_at DESC, id ASC
		LIMIT ?
	`, limit)
	if err != nil {
		return nil, fmt.Errorf("query mission results: %w", err)
	}
	defer rows.Close()
	out := make([]MissionResult, 0, limit)
	for rows.Next() {
		result, err := scanMissionResult(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, result)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate mission results: %w", err)
	}
	return out, nil
}

func (s *SQLiteStore) MissionLedgerHealth(now time.Time) (MissionLedgerHealth, error) {
	if s == nil {
		return MissionLedgerHealth{}, fmt.Errorf("store is nil")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	missions, err := s.Missions(MissionFilter{Limit: 500})
	if err != nil {
		return MissionLedgerHealth{}, err
	}
	health := MissionLedgerHealth{}
	for _, m := range missions {
		switch m.Status {
		case MissionStatusActive:
			health.ActiveCount++
		case MissionStatusBlocked:
			health.BlockedCount++
		case MissionStatusCandidate:
			health.CandidateCount++
			if !m.Pinned && !m.UpdatedAt.IsZero() && now.Sub(m.UpdatedAt) > 60*24*time.Hour {
				health.StaleCandidateCount++
			}
		}
		if m.Pinned {
			health.PinnedCount++
		}
		if m.Recurrence != nil {
			health.RecurringCount++
		}
		if m.Authority.CanSelfContinue {
			health.SelfContinuationEnabledCount++
		}
	}
	var pending sql.NullInt64
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM mission_handoffs WHERE status = 'pending'`).Scan(&pending); err == nil && pending.Valid {
		health.PendingHandoffCount = int(pending.Int64)
	}
	return health, nil
}

func (s *SQLiteStore) UpdateWorkingObjective(key SessionKey, w WorkingObjective) error {
	if s == nil {
		return fmt.Errorf("store is nil")
	}
	if _, err := s.Load(key); err != nil {
		return err
	}
	w = NormalizeWorkingObjective(w)
	if w.Objective != "" && w.CreatedAt.IsZero() {
		w.CreatedAt = time.Now().UTC()
	}
	raw, err := json.Marshal(w)
	if err != nil {
		return fmt.Errorf("encode working objective: %w", err)
	}
	_, err = s.db.Exec(`UPDATE sessions SET working_objective_json = ?, updated_at = ? WHERE session_id = ?`, string(raw), time.Now().UTC().Format(time.RFC3339Nano), SessionIDForKey(key))
	if err != nil {
		return fmt.Errorf("update working objective: %w", err)
	}
	return nil
}

func (s *SQLiteStore) WorkingObjective(key SessionKey) (WorkingObjective, error) {
	if s == nil {
		return WorkingObjective{}, fmt.Errorf("store is nil")
	}
	var raw sql.NullString
	err := s.db.QueryRow(`SELECT working_objective_json FROM sessions WHERE session_id = ?`, SessionIDForKey(key)).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return WorkingObjective{}, nil
	}
	if err != nil {
		return WorkingObjective{}, fmt.Errorf("load working objective: %w", err)
	}
	return decodeWorkingObjective(raw.String), nil
}
