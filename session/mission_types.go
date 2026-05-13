//go:build linux

package session

import (
	"fmt"
	"strings"
	"time"
)

type MissionStatus string

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

type MissionHandoffFilter struct {
	MissionID   string
	OperationID string
	Status      string
	Limit       int
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
