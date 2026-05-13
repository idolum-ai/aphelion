//go:build linux

package session

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type encodedMissionFields struct {
	recurrence      any
	authority       string
	budget          string
	decay           string
	successCriteria string
	evidence        string
	plan            string
	tags            string
	sourceRefs      string
}

func encodeMissionFields(m MissionState) (encodedMissionFields, error) {
	recurrence := any(nil)
	if m.Recurrence != nil {
		raw, err := json.Marshal(m.Recurrence)
		if err != nil {
			return encodedMissionFields{}, fmt.Errorf("encode mission recurrence: %w", err)
		}
		recurrence = string(raw)
	}
	authority, err := json.Marshal(m.Authority)
	if err != nil {
		return encodedMissionFields{}, fmt.Errorf("encode mission authority: %w", err)
	}
	budget, err := json.Marshal(m.Budget)
	if err != nil {
		return encodedMissionFields{}, fmt.Errorf("encode mission budget: %w", err)
	}
	decay, err := json.Marshal(m.Decay)
	if err != nil {
		return encodedMissionFields{}, fmt.Errorf("encode mission decay: %w", err)
	}
	success, err := json.Marshal(m.SuccessCriteria)
	if err != nil {
		return encodedMissionFields{}, fmt.Errorf("encode mission success criteria: %w", err)
	}
	evidence, err := json.Marshal(m.EvidenceChecklist)
	if err != nil {
		return encodedMissionFields{}, fmt.Errorf("encode mission evidence: %w", err)
	}
	plan, err := json.Marshal(m.CurrentPlan)
	if err != nil {
		return encodedMissionFields{}, fmt.Errorf("encode mission plan: %w", err)
	}
	tags, err := json.Marshal(m.Tags)
	if err != nil {
		return encodedMissionFields{}, fmt.Errorf("encode mission tags: %w", err)
	}
	sourceRefs, err := json.Marshal(m.SourceRefs)
	if err != nil {
		return encodedMissionFields{}, fmt.Errorf("encode mission source refs: %w", err)
	}
	return encodedMissionFields{recurrence: recurrence, authority: string(authority), budget: string(budget), decay: string(decay), successCriteria: string(success), evidence: string(evidence), plan: string(plan), tags: string(tags), sourceRefs: string(sourceRefs)}, nil
}

func missionSelectSQL() string {
	return `SELECT id, scope, owner, title, objective, origin, status, pinned, recurrence_json, authority_json, budget_json, decay_json,
		success_criteria_json, evidence_json, current_plan_json, next_allowed_action, blocked_reason, waiting_for,
		tags_json, source_refs_json, created_at, updated_at, last_touched_at, last_summoned_at
		FROM mission_ledger`
}

type missionScanner interface{ Scan(dest ...any) error }

func scanMission(scanner missionScanner) (MissionState, error) {
	var m MissionState
	var pinned int
	var recurrenceRaw, authorityRaw, budgetRaw, decayRaw, successRaw, evidenceRaw, planRaw, tagsRaw, sourceRefsRaw sql.NullString
	var nextRaw, blockedRaw, waitingRaw sql.NullString
	var createdRaw, updatedRaw string
	var touchedRaw, summonedRaw sql.NullString
	if err := scanner.Scan(&m.ID, &m.Scope, &m.Owner, &m.Title, &m.Objective, &m.Origin, &m.Status, &pinned, &recurrenceRaw, &authorityRaw, &budgetRaw, &decayRaw, &successRaw, &evidenceRaw, &planRaw, &nextRaw, &blockedRaw, &waitingRaw, &tagsRaw, &sourceRefsRaw, &createdRaw, &updatedRaw, &touchedRaw, &summonedRaw); err != nil {
		return MissionState{}, err
	}
	m.Pinned = pinned != 0
	m.NextAllowedAction = nullToString(nextRaw)
	m.BlockedReason = nullToString(blockedRaw)
	m.WaitingFor = nullToString(waitingRaw)
	m.Recurrence = decodeMissionRecurrence(recurrenceRaw.String)
	m.Authority = decodeMissionAuthority(authorityRaw.String)
	m.Budget = decodeMissionBudget(budgetRaw.String)
	m.Decay = decodeMissionDecay(decayRaw.String)
	m.SuccessCriteria = decodeMissionStringSlice(successRaw.String)
	m.EvidenceChecklist = decodeMissionEvidence(evidenceRaw.String)
	m.CurrentPlan = decodeMissionPlan(planRaw.String)
	m.Tags = decodeMissionStringSlice(tagsRaw.String)
	m.SourceRefs = decodeMissionStringSlice(sourceRefsRaw.String)
	m.CreatedAt = mustParseSQLiteTime(createdRaw)
	m.UpdatedAt = mustParseSQLiteTime(updatedRaw)
	m.LastTouchedAt = mustParseSQLiteTime(touchedRaw.String)
	m.LastSummonedAt = mustParseSQLiteTime(summonedRaw.String)
	return NormalizeMissionState(m), nil
}

func scanMissionEvent(scanner missionScanner) (MissionEvent, error) {
	var event MissionEvent
	var rawTime string
	if err := scanner.Scan(&event.Seq, &event.MissionID, &event.EventType, &event.Actor, &event.Summary, &event.Payload, &rawTime); err != nil {
		return MissionEvent{}, err
	}
	event.CreatedAt = mustParseSQLiteTime(rawTime)
	return event, nil
}

func scanMissionHandoff(scanner missionScanner) (MissionHandoff, error) {
	var handoff MissionHandoff
	var missionID, operationID, expectedRaw sql.NullString
	var createdRaw, updatedRaw string
	if err := scanner.Scan(
		&handoff.ID,
		&missionID,
		&operationID,
		&handoff.PlannedAction,
		&expectedRaw,
		&handoff.RecoveryQuestion,
		&handoff.Status,
		&createdRaw,
		&updatedRaw,
	); err != nil {
		return MissionHandoff{}, err
	}
	handoff.MissionID = nullToString(missionID)
	handoff.OperationID = nullToString(operationID)
	handoff.ExpectedEvidenceJSON = nullToString(expectedRaw)
	handoff.CreatedAt = mustParseSQLiteTime(createdRaw)
	handoff.UpdatedAt = mustParseSQLiteTime(updatedRaw)
	return handoff, nil
}

func scanMissionResult(scanner missionScanner) (MissionResult, error) {
	var result MissionResult
	var missionID, operationID, evidenceRaw, remainingRisk sql.NullString
	var recordedRaw string
	if err := scanner.Scan(
		&result.ID,
		&result.HandoffID,
		&missionID,
		&operationID,
		&result.Status,
		&evidenceRaw,
		&result.Summary,
		&remainingRisk,
		&recordedRaw,
	); err != nil {
		return MissionResult{}, err
	}
	result.MissionID = nullToString(missionID)
	result.OperationID = nullToString(operationID)
	result.EvidenceRefsJSON = nullToString(evidenceRaw)
	result.RemainingRisk = nullToString(remainingRisk)
	result.RecordedAt = mustParseSQLiteTime(recordedRaw)
	return result, nil
}

func appendMissionEventTx(tx *sql.Tx, event MissionEvent) error {
	if tx == nil {
		return nil
	}
	event.MissionID = strings.TrimSpace(event.MissionID)
	event.EventType = strings.TrimSpace(event.EventType)
	if event.MissionID == "" || event.EventType == "" {
		return nil
	}
	if event.CreatedAt.IsZero() {
		event.CreatedAt = time.Now().UTC()
	}
	if _, err := tx.Exec(`INSERT INTO mission_events(mission_id, event_type, actor, summary, payload_json, created_at) VALUES (?, ?, ?, ?, ?, ?)`, event.MissionID, event.EventType, strings.TrimSpace(event.Actor), strings.TrimSpace(event.Summary), strings.TrimSpace(event.Payload), event.CreatedAt.UTC().Format(time.RFC3339Nano)); err != nil {
		return fmt.Errorf("append mission event: %w", err)
	}
	return nil
}

func missionUpdateEvent(previous MissionState, next MissionState) string {
	switch {
	case previous.Pinned != next.Pinned && next.Pinned:
		return "mission.pinned"
	case previous.Pinned != next.Pinned:
		return "mission.unpinned"
	case previous.Status != next.Status:
		switch next.Status {
		case MissionStatusActive:
			return "mission.activated"
		case MissionStatusBlocked:
			return "mission.blocked"
		case MissionStatusCompleted:
			return "mission.completed"
		case MissionStatusExpired:
			return "mission.expired"
		case MissionStatusArchived:
			return "mission.archived"
		default:
			return "mission.updated"
		}
	case (previous.Recurrence == nil) != (next.Recurrence == nil):
		if next.Recurrence != nil {
			return "mission.recurrence_enabled"
		}
		return "mission.recurrence_disabled"
	case !next.LastSummonedAt.IsZero() && !next.LastSummonedAt.Equal(previous.LastSummonedAt):
		return "mission.summoned"
	default:
		return "mission.updated"
	}
}

func decodeMissionRecurrence(raw string) *MissionRecurrence {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	var recurrence MissionRecurrence
	if err := json.Unmarshal([]byte(raw), &recurrence); err != nil {
		return nil
	}
	recurrence = NormalizeMissionRecurrence(recurrence)
	if recurrence.Empty() {
		return nil
	}
	return &recurrence
}

func decodeMissionAuthority(raw string) MissionAuthorityContract {
	var authority MissionAuthorityContract
	if strings.TrimSpace(raw) != "" {
		_ = json.Unmarshal([]byte(raw), &authority)
	}
	return NormalizeMissionAuthority(authority)
}

func decodeMissionBudget(raw string) MissionBudget {
	var budget MissionBudget
	if strings.TrimSpace(raw) != "" {
		_ = json.Unmarshal([]byte(raw), &budget)
	}
	return budget
}

func decodeMissionDecay(raw string) MissionDecayPolicy {
	var decay MissionDecayPolicy
	if strings.TrimSpace(raw) != "" {
		_ = json.Unmarshal([]byte(raw), &decay)
	}
	return NormalizeMissionDecay(decay)
}

func decodeMissionStringSlice(raw string) []string {
	var values []string
	if strings.TrimSpace(raw) != "" {
		_ = json.Unmarshal([]byte(raw), &values)
	}
	return normalizeMissionStringSlice(values)
}

func decodeMissionEvidence(raw string) []MissionEvidenceItem {
	var evidence []MissionEvidenceItem
	if strings.TrimSpace(raw) != "" {
		_ = json.Unmarshal([]byte(raw), &evidence)
	}
	return normalizeMissionEvidence(evidence)
}

func decodeMissionPlan(raw string) []MissionPlanStep {
	var plan []MissionPlanStep
	if strings.TrimSpace(raw) != "" {
		_ = json.Unmarshal([]byte(raw), &plan)
	}
	return normalizeMissionPlan(plan)
}

func decodeWorkingObjective(raw string) WorkingObjective {
	var objective WorkingObjective
	if strings.TrimSpace(raw) != "" {
		_ = json.Unmarshal([]byte(raw), &objective)
	}
	return NormalizeWorkingObjective(objective)
}
