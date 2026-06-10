//go:build linux

package session

import (
	"strings"
	"time"
)

type ReentryRecommendationStatus string

const (
	ReentryRecommendationStatusPending  ReentryRecommendationStatus = "pending"
	ReentryRecommendationStatusShown    ReentryRecommendationStatus = "shown"
	ReentryRecommendationStatusSelected ReentryRecommendationStatus = "selected"
	ReentryRecommendationStatusIgnored  ReentryRecommendationStatus = "ignored"
	ReentryRecommendationStatusStale    ReentryRecommendationStatus = "stale"
	ReentryRecommendationStatusExpired  ReentryRecommendationStatus = "expired"
)

type ReentryCandidateKind string

const (
	ReentryCandidateContinueOperation      ReentryCandidateKind = "continue_operation"
	ReentryCandidateRequestNextLease       ReentryCandidateKind = "request_next_lease"
	ReentryCandidateResumeMission          ReentryCandidateKind = "resume_mission"
	ReentryCandidateReviewReleaseReadiness ReentryCandidateKind = "review_release_readiness"
	ReentryCandidateReviewMemoryHealth     ReentryCandidateKind = "review_memory_health"
	ReentryCandidateReflectWithOperator    ReentryCandidateKind = "reflect_with_operator"
	ReentryCandidateClarifyGoal            ReentryCandidateKind = "clarify_goal"
)

type ReentryRecommendationCandidate struct {
	ID               string               `json:"id"`
	Kind             ReentryCandidateKind `json:"kind"`
	Label            string               `json:"label"`
	Summary          string               `json:"summary,omitempty"`
	PromptText       string               `json:"prompt_text"`
	AuthorityClass   string               `json:"authority_class,omitempty"`
	RequiresApproval bool                 `json:"requires_approval,omitempty"`
	BasisRefs        []string             `json:"basis_refs,omitempty"`
}

type ReentryRecommendation struct {
	ID                  string                           `json:"id"`
	Owner               string                           `json:"owner"`
	ChatID              int64                            `json:"chat_id"`
	SenderID            int64                            `json:"sender_id,omitempty"`
	SessionID           string                           `json:"session_id"`
	Scope               ScopeRef                         `json:"scope"`
	SourceTurnRunID     int64                            `json:"source_turn_run_id,omitempty"`
	TerminalFingerprint string                           `json:"terminal_fingerprint"`
	Status              ReentryRecommendationStatus      `json:"status"`
	Candidates          []ReentryRecommendationCandidate `json:"candidates"`
	SelectedCandidateID string                           `json:"selected_candidate_id,omitempty"`
	ResultSummary       string                           `json:"result_summary,omitempty"`
	DeliveryMessageID   int64                            `json:"delivery_message_id,omitempty"`
	CreatedAt           time.Time                        `json:"created_at,omitempty"`
	ShownAt             time.Time                        `json:"shown_at,omitempty"`
	SelectedAt          time.Time                        `json:"selected_at,omitempty"`
	IgnoredAt           time.Time                        `json:"ignored_at,omitempty"`
	UpdatedAt           time.Time                        `json:"updated_at,omitempty"`
}

type ReentryRecommendationFilter struct {
	Owner     string
	SessionID string
	Status    ReentryRecommendationStatus
	Limit     int
}

func NormalizeReentryRecommendationStatus(status ReentryRecommendationStatus) ReentryRecommendationStatus {
	switch ReentryRecommendationStatus(strings.ToLower(strings.TrimSpace(string(status)))) {
	case ReentryRecommendationStatusPending:
		return ReentryRecommendationStatusPending
	case ReentryRecommendationStatusShown:
		return ReentryRecommendationStatusShown
	case ReentryRecommendationStatusSelected:
		return ReentryRecommendationStatusSelected
	case ReentryRecommendationStatusIgnored:
		return ReentryRecommendationStatusIgnored
	case ReentryRecommendationStatusStale:
		return ReentryRecommendationStatusStale
	case ReentryRecommendationStatusExpired:
		return ReentryRecommendationStatusExpired
	default:
		return ReentryRecommendationStatusPending
	}
}

func ReentryRecommendationStatusTerminal(status ReentryRecommendationStatus) bool {
	switch NormalizeReentryRecommendationStatus(status) {
	case ReentryRecommendationStatusSelected, ReentryRecommendationStatusIgnored, ReentryRecommendationStatusStale, ReentryRecommendationStatusExpired:
		return true
	default:
		return false
	}
}

func NormalizeReentryCandidateKind(kind ReentryCandidateKind) ReentryCandidateKind {
	switch ReentryCandidateKind(strings.ToLower(strings.TrimSpace(string(kind)))) {
	case ReentryCandidateContinueOperation:
		return ReentryCandidateContinueOperation
	case ReentryCandidateRequestNextLease:
		return ReentryCandidateRequestNextLease
	case ReentryCandidateResumeMission:
		return ReentryCandidateResumeMission
	case ReentryCandidateReviewReleaseReadiness:
		return ReentryCandidateReviewReleaseReadiness
	case ReentryCandidateReviewMemoryHealth:
		return ReentryCandidateReviewMemoryHealth
	case ReentryCandidateReflectWithOperator:
		return ReentryCandidateReflectWithOperator
	case ReentryCandidateClarifyGoal:
		return ReentryCandidateClarifyGoal
	default:
		return ""
	}
}

func NormalizeReentryRecommendationCandidate(candidate ReentryRecommendationCandidate) ReentryRecommendationCandidate {
	candidate.ID = strings.TrimSpace(candidate.ID)
	candidate.Kind = NormalizeReentryCandidateKind(candidate.Kind)
	candidate.Label = strings.TrimSpace(candidate.Label)
	candidate.Summary = strings.TrimSpace(candidate.Summary)
	candidate.PromptText = strings.TrimSpace(candidate.PromptText)
	candidate.AuthorityClass = strings.TrimSpace(candidate.AuthorityClass)
	candidate.BasisRefs = normalizeMissionStringSlice(candidate.BasisRefs)
	return candidate
}

func NormalizeReentryRecommendation(record ReentryRecommendation) ReentryRecommendation {
	record.ID = strings.TrimSpace(record.ID)
	record.Owner = strings.TrimSpace(record.Owner)
	record.SessionID = strings.TrimSpace(record.SessionID)
	record.Scope = NormalizeScopeRef(record.Scope)
	record.TerminalFingerprint = strings.TrimSpace(record.TerminalFingerprint)
	record.Status = NormalizeReentryRecommendationStatus(record.Status)
	record.SelectedCandidateID = strings.TrimSpace(record.SelectedCandidateID)
	record.ResultSummary = strings.TrimSpace(record.ResultSummary)
	out := make([]ReentryRecommendationCandidate, 0, len(record.Candidates))
	for _, candidate := range record.Candidates {
		candidate = NormalizeReentryRecommendationCandidate(candidate)
		if candidate.ID == "" || candidate.Kind == "" || candidate.Label == "" || candidate.PromptText == "" {
			continue
		}
		out = append(out, candidate)
	}
	record.Candidates = out
	return record
}

func (r ReentryRecommendation) Candidate(id string) (ReentryRecommendationCandidate, bool) {
	id = strings.TrimSpace(id)
	if id == "" {
		return ReentryRecommendationCandidate{}, false
	}
	for _, candidate := range NormalizeReentryRecommendation(r).Candidates {
		if candidate.ID == id {
			return candidate, true
		}
	}
	return ReentryRecommendationCandidate{}, false
}
