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

type MissionStatus string

const (
	MissionStatusDormant   MissionStatus = "dormant"
	MissionStatusCandidate MissionStatus = "candidate"
	MissionStatusActive    MissionStatus = "active"
	MissionStatusBlocked   MissionStatus = "blocked"
	MissionStatusCompleted MissionStatus = "completed"
	MissionStatusExpired   MissionStatus = "expired"
	MissionStatusArchived  MissionStatus = "archived"
)

type MissionAuthorityContract struct {
	CanSelfSummon      bool     `json:"can_self_summon"`
	CanSelfContinue    bool     `json:"can_self_continue"`
	RequiresUserReview bool     `json:"requires_user_review"`
	AllowedTools       []string `json:"allowed_tools,omitempty"`
	AllowedSurfaces    []string `json:"allowed_surfaces,omitempty"`
	AllowedPaths       []string `json:"allowed_paths,omitempty"`
	AllowedDomains     []string `json:"allowed_domains,omitempty"`
	CapabilityGrantIDs []string `json:"capability_grant_ids,omitempty"`
	MaxAutonomousSteps int      `json:"max_autonomous_steps,omitempty"`
	ReviewCadence      string   `json:"review_cadence,omitempty"`
}

type MissionBudget struct {
	MaxTurns           int `json:"max_turns,omitempty"`
	MaxToolCalls       int `json:"max_tool_calls,omitempty"`
	MaxRuntimeSeconds  int `json:"max_runtime_seconds,omitempty"`
	MaxTokens          int `json:"max_tokens,omitempty"`
	TurnsUsed          int `json:"turns_used,omitempty"`
	ToolCallsUsed      int `json:"tool_calls_used,omitempty"`
	RuntimeSecondsUsed int `json:"runtime_seconds_used,omitempty"`
	TokensUsed         int `json:"tokens_used,omitempty"`
}

type MissionDecayPolicy struct {
	ReviewAfterDays   int `json:"review_after_days,omitempty"`
	ExpireAfterDays   int `json:"expire_after_days,omitempty"`
	ArchiveAfterDays  int `json:"archive_after_days,omitempty"`
	MaxSummonsPerWeek int `json:"max_summons_per_week,omitempty"`
}

type MissionRecurrence struct {
	Cadence       string    `json:"cadence,omitempty"`
	NextDueAt     time.Time `json:"next_due_at,omitempty"`
	LastRanAt     time.Time `json:"last_ran_at,omitempty"`
	MaxRuns       int       `json:"max_runs,omitempty"`
	RunsUsed      int       `json:"runs_used,omitempty"`
	RequiresTrace bool      `json:"requires_trace,omitempty"`
}

type MissionEvidenceItem struct {
	Claim       string    `json:"claim"`
	Required    bool      `json:"required"`
	EvidenceRef string    `json:"evidence_ref,omitempty"`
	Status      string    `json:"status,omitempty"`
	UpdatedAt   time.Time `json:"updated_at,omitempty"`
}

type MissionPlanStep struct {
	Step   string `json:"step"`
	Status string `json:"status,omitempty"`
}

type MissionState struct {
	ID                string                   `json:"id"`
	Title             string                   `json:"title"`
	Objective         string                   `json:"objective"`
	Origin            string                   `json:"origin,omitempty"`
	Scope             string                   `json:"scope,omitempty"`
	Owner             string                   `json:"owner,omitempty"`
	Status            MissionStatus            `json:"status,omitempty"`
	Pinned            bool                     `json:"pinned,omitempty"`
	Recurrence        *MissionRecurrence       `json:"recurrence,omitempty"`
	CreatedAt         time.Time                `json:"created_at,omitempty"`
	UpdatedAt         time.Time                `json:"updated_at,omitempty"`
	LastTouchedAt     time.Time                `json:"last_touched_at,omitempty"`
	LastSummonedAt    time.Time                `json:"last_summoned_at,omitempty"`
	SuccessCriteria   []string                 `json:"success_criteria,omitempty"`
	EvidenceChecklist []MissionEvidenceItem    `json:"evidence_checklist,omitempty"`
	CurrentPlan       []MissionPlanStep        `json:"current_plan,omitempty"`
	NextAllowedAction string                   `json:"next_allowed_action,omitempty"`
	BlockedReason     string                   `json:"blocked_reason,omitempty"`
	WaitingFor        string                   `json:"waiting_for,omitempty"`
	Authority         MissionAuthorityContract `json:"authority"`
	Budget            MissionBudget            `json:"budget"`
	Decay             MissionDecayPolicy       `json:"decay"`
	Tags              []string                 `json:"tags,omitempty"`
	SourceRefs        []string                 `json:"source_refs,omitempty"`
}

type MissionEvent struct {
	Seq       int64     `json:"seq,omitempty"`
	MissionID string    `json:"mission_id"`
	EventType string    `json:"event_type"`
	Actor     string    `json:"actor,omitempty"`
	Summary   string    `json:"summary,omitempty"`
	Payload   string    `json:"payload_json,omitempty"`
	CreatedAt time.Time `json:"created_at,omitempty"`
}

type MissionHandoff struct {
	ID                   string    `json:"id"`
	MissionID            string    `json:"mission_id,omitempty"`
	OperationID          string    `json:"operation_id,omitempty"`
	PlannedAction        string    `json:"planned_action"`
	ExpectedEvidenceJSON string    `json:"expected_evidence_json,omitempty"`
	RecoveryQuestion     string    `json:"recovery_question"`
	Status               string    `json:"status,omitempty"`
	CreatedAt            time.Time `json:"created_at,omitempty"`
	UpdatedAt            time.Time `json:"updated_at,omitempty"`
}

type MissionResult struct {
	ID               string    `json:"id"`
	HandoffID        string    `json:"handoff_id"`
	MissionID        string    `json:"mission_id,omitempty"`
	OperationID      string    `json:"operation_id,omitempty"`
	Status           string    `json:"status"`
	EvidenceRefsJSON string    `json:"evidence_refs_json,omitempty"`
	Summary          string    `json:"summary"`
	RemainingRisk    string    `json:"remaining_risk,omitempty"`
	RecordedAt       time.Time `json:"recorded_at,omitempty"`
}

type MissionFilter struct {
	Scope  string
	Owner  string
	Status MissionStatus
	Pinned *bool
	Limit  int
}

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

func NormalizeMissionStatus(status MissionStatus) MissionStatus {
	switch MissionStatus(strings.ToLower(strings.TrimSpace(string(status)))) {
	case MissionStatusDormant:
		return MissionStatusDormant
	case MissionStatusCandidate:
		return MissionStatusCandidate
	case MissionStatusActive:
		return MissionStatusActive
	case MissionStatusBlocked:
		return MissionStatusBlocked
	case MissionStatusCompleted:
		return MissionStatusCompleted
	case MissionStatusExpired:
		return MissionStatusExpired
	case MissionStatusArchived:
		return MissionStatusArchived
	default:
		return ""
	}
}

func DefaultMissionAuthority() MissionAuthorityContract {
	return MissionAuthorityContract{CanSelfSummon: true, RequiresUserReview: true}
}

func DefaultMissionDecay() MissionDecayPolicy {
	return MissionDecayPolicy{ReviewAfterDays: 7, ExpireAfterDays: 60, ArchiveAfterDays: 30, MaxSummonsPerWeek: 3}
}

func NormalizeMissionState(m MissionState) MissionState {
	m.ID = strings.TrimSpace(m.ID)
	m.Title = strings.TrimSpace(m.Title)
	m.Objective = strings.TrimSpace(m.Objective)
	m.Origin = strings.TrimSpace(m.Origin)
	m.Scope = strings.TrimSpace(m.Scope)
	m.Owner = strings.TrimSpace(m.Owner)
	m.Status = NormalizeMissionStatus(m.Status)
	if m.Status == "" {
		m.Status = MissionStatusCandidate
	}
	if m.Title == "" {
		m.Title = missionTitleFromObjective(m.Objective)
	}
	m.NextAllowedAction = strings.TrimSpace(m.NextAllowedAction)
	m.BlockedReason = strings.TrimSpace(m.BlockedReason)
	m.WaitingFor = strings.TrimSpace(m.WaitingFor)
	m.Tags = normalizeMissionStringSlice(m.Tags)
	m.SourceRefs = normalizeMissionStringSlice(m.SourceRefs)
	m.SuccessCriteria = normalizeMissionStringSlice(m.SuccessCriteria)
	m.EvidenceChecklist = normalizeMissionEvidence(m.EvidenceChecklist)
	m.CurrentPlan = normalizeMissionPlan(m.CurrentPlan)
	m.Authority = NormalizeMissionAuthority(m.Authority)
	m.Decay = NormalizeMissionDecay(m.Decay)
	if m.Recurrence != nil {
		recur := NormalizeMissionRecurrence(*m.Recurrence)
		if recur.Empty() {
			m.Recurrence = nil
		} else {
			m.Recurrence = &recur
		}
	}
	return m
}

func NormalizeMissionAuthority(a MissionAuthorityContract) MissionAuthorityContract {
	a.AllowedTools = normalizeMissionStringSlice(a.AllowedTools)
	a.AllowedSurfaces = normalizeMissionStringSlice(a.AllowedSurfaces)
	a.AllowedPaths = normalizeMissionStringSlice(a.AllowedPaths)
	a.AllowedDomains = normalizeMissionStringSlice(a.AllowedDomains)
	a.CapabilityGrantIDs = normalizeMissionStringSlice(a.CapabilityGrantIDs)
	a.ReviewCadence = strings.TrimSpace(a.ReviewCadence)
	// Empty authority defaults to review-only self-summon.
	if !a.CanSelfSummon && !a.CanSelfContinue && !a.RequiresUserReview && len(a.AllowedTools) == 0 && len(a.AllowedSurfaces) == 0 && len(a.AllowedPaths) == 0 && len(a.AllowedDomains) == 0 && len(a.CapabilityGrantIDs) == 0 && a.MaxAutonomousSteps == 0 && a.ReviewCadence == "" {
		return DefaultMissionAuthority()
	}
	if a.CanSelfContinue {
		a.RequiresUserReview = false
	}
	return a
}

func NormalizeMissionDecay(d MissionDecayPolicy) MissionDecayPolicy {
	if d.ReviewAfterDays == 0 && d.ExpireAfterDays == 0 && d.ArchiveAfterDays == 0 && d.MaxSummonsPerWeek == 0 {
		return DefaultMissionDecay()
	}
	return d
}

func NormalizeMissionRecurrence(r MissionRecurrence) MissionRecurrence {
	r.Cadence = strings.TrimSpace(r.Cadence)
	return r
}

func (r MissionRecurrence) Empty() bool {
	return strings.TrimSpace(r.Cadence) == "" && r.NextDueAt.IsZero() && r.LastRanAt.IsZero() && r.MaxRuns == 0 && r.RunsUsed == 0 && !r.RequiresTrace
}

func NormalizeWorkingObjective(w WorkingObjective) WorkingObjective {
	w.Objective = strings.TrimSpace(w.Objective)
	w.Source = strings.TrimSpace(w.Source)
	w.Confidence = strings.TrimSpace(w.Confidence)
	return w
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

func ensureMissionLedgerTables(tx *sql.Tx) error {
	for _, stmt := range []string{
		`CREATE TABLE IF NOT EXISTS mission_ledger (
			id TEXT PRIMARY KEY,
			scope TEXT NOT NULL,
			owner TEXT NOT NULL,
			title TEXT NOT NULL,
			objective TEXT NOT NULL,
			origin TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL,
			pinned INTEGER NOT NULL DEFAULT 0,
			recurrence_json TEXT,
			authority_json TEXT NOT NULL DEFAULT '{}',
			budget_json TEXT NOT NULL DEFAULT '{}',
			decay_json TEXT NOT NULL DEFAULT '{}',
			success_criteria_json TEXT,
			evidence_json TEXT,
			current_plan_json TEXT,
			next_allowed_action TEXT,
			blocked_reason TEXT,
			waiting_for TEXT,
			tags_json TEXT,
			source_refs_json TEXT,
			created_at TEXT NOT NULL DEFAULT (datetime('now')),
			updated_at TEXT NOT NULL DEFAULT (datetime('now')),
			last_touched_at TEXT,
			last_summoned_at TEXT
		)`,
		`CREATE TABLE IF NOT EXISTS mission_events (
			seq INTEGER PRIMARY KEY AUTOINCREMENT,
			mission_id TEXT NOT NULL,
			event_type TEXT NOT NULL,
			actor TEXT NOT NULL DEFAULT '',
			summary TEXT NOT NULL DEFAULT '',
			payload_json TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL DEFAULT (datetime('now')),
			FOREIGN KEY(mission_id) REFERENCES mission_ledger(id) ON DELETE CASCADE
		)`,
		`CREATE TABLE IF NOT EXISTS mission_handoffs (
			id TEXT PRIMARY KEY,
			mission_id TEXT,
			operation_id TEXT,
			planned_action TEXT NOT NULL,
			expected_evidence_json TEXT,
			recovery_question TEXT NOT NULL,
			status TEXT NOT NULL DEFAULT 'pending',
			created_at TEXT NOT NULL DEFAULT (datetime('now')),
			updated_at TEXT NOT NULL DEFAULT (datetime('now')),
			FOREIGN KEY(mission_id) REFERENCES mission_ledger(id) ON DELETE SET NULL
		)`,
		`CREATE TABLE IF NOT EXISTS mission_results (
			id TEXT PRIMARY KEY,
			handoff_id TEXT NOT NULL,
			mission_id TEXT,
			operation_id TEXT,
			status TEXT NOT NULL,
			evidence_refs_json TEXT,
			summary TEXT NOT NULL,
			remaining_risk TEXT,
			recorded_at TEXT NOT NULL DEFAULT (datetime('now')),
			FOREIGN KEY(handoff_id) REFERENCES mission_handoffs(id) ON DELETE CASCADE,
			FOREIGN KEY(mission_id) REFERENCES mission_ledger(id) ON DELETE SET NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_mission_ledger_scope_owner_status ON mission_ledger(scope, owner, status)`,
		`CREATE INDEX IF NOT EXISTS idx_mission_ledger_pinned ON mission_ledger(scope, owner, pinned)`,
		`CREATE INDEX IF NOT EXISTS idx_mission_ledger_last_touched ON mission_ledger(last_touched_at)`,
		`CREATE INDEX IF NOT EXISTS idx_mission_events_mission_id_seq ON mission_events(mission_id, seq)`,
		`CREATE INDEX IF NOT EXISTS idx_mission_handoffs_status ON mission_handoffs(status, created_at)`,
		`CREATE INDEX IF NOT EXISTS idx_mission_results_handoff ON mission_results(handoff_id, recorded_at)`,
	} {
		if _, err := tx.Exec(stmt); err != nil {
			return fmt.Errorf("ensure mission ledger tables: %w", err)
		}
	}
	return nil
}

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

func encodeWorkingObjective(w WorkingObjective) string {
	w = NormalizeWorkingObjective(w)
	raw, err := json.Marshal(w)
	if err != nil {
		return "{}"
	}
	return string(raw)
}

func normalizeMissionEvidence(evidence []MissionEvidenceItem) []MissionEvidenceItem {
	out := make([]MissionEvidenceItem, 0, len(evidence))
	seen := map[string]struct{}{}
	for _, item := range evidence {
		item.Claim = strings.TrimSpace(item.Claim)
		item.EvidenceRef = strings.TrimSpace(item.EvidenceRef)
		item.Status = strings.TrimSpace(item.Status)
		if item.Claim == "" {
			continue
		}
		key := item.Claim + "\x00" + item.EvidenceRef + "\x00" + item.Status
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, item)
	}
	return out
}

func normalizeMissionPlan(plan []MissionPlanStep) []MissionPlanStep {
	out := make([]MissionPlanStep, 0, len(plan))
	for _, step := range plan {
		step.Step = strings.TrimSpace(step.Step)
		step.Status = strings.TrimSpace(step.Status)
		if step.Step == "" {
			continue
		}
		out = append(out, step)
	}
	return out
}

func missionTitleFromObjective(objective string) string {
	objective = strings.TrimSpace(objective)
	if objective == "" {
		return "Untitled mission"
	}
	runes := []rune(objective)
	if len(runes) > 80 {
		objective = strings.TrimSpace(string(runes[:80])) + "…"
	}
	return objective
}

func generatedMissionID(prefix string) string {
	prefix = strings.TrimSpace(prefix)
	if prefix == "" {
		prefix = "mission"
	}
	return fmt.Sprintf("%s-%d", prefix, time.Now().UTC().UnixNano())
}

func firstMissionNonEmpty(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}

func missionMatchesContext(m MissionState, contextText string) bool {
	needles := []string{m.ID, m.Title, m.Objective, m.BlockedReason, m.NextAllowedAction}
	needles = append(needles, m.Tags...)
	for _, needle := range needles {
		needle = strings.ToLower(strings.TrimSpace(needle))
		if needle != "" && strings.Contains(contextText, needle) {
			return true
		}
		for _, token := range strings.Fields(needle) {
			if len(token) >= 5 && strings.Contains(contextText, token) {
				return true
			}
		}
	}
	return false
}

func normalizeMissionStringSlice(values []string) []string {
	out := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func minMissionInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
