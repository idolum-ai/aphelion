//go:build linux

package session

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math"
	"strings"
	"time"
)

const (
	CuriosityLeaseStatusActive    = "active"
	CuriosityLeaseStatusExpired   = "expired"
	CuriosityLeaseStatusExhausted = "exhausted"

	CuriosityLeaseClassReadOnly = "curiosity_read_only"
	CuriosityWorkActionLook     = "look"

	CuriositySourceSession   = "session"
	CuriositySourceMemory    = "memory"
	CuriositySourceWorkspace = "workspace"
	CuriositySourceURL       = "url"

	CuriosityLeaseRetention       = 30 * 24 * time.Hour
	CuriosityObservationRetention = 30 * 24 * time.Hour
)

type CuriosityLease struct {
	ID                 string
	Status             string
	Scope              ScopeRef
	LeaseClass         string
	WorkAction         string
	AllowedSourceKinds []string
	AllowedSourceRefs  []string
	DailyTurnBudget    int
	MaxLooksPerTurn    int
	TurnsUsed          int
	PeriodStart        string
	ApprovedBy         string
	CreatedAt          time.Time
	ExpiresAt          time.Time
	UpdatedAt          time.Time
}

type CuriosityObservation struct {
	ID          int64
	LeaseID     string
	SessionID   string
	ChatID      int64
	UserID      int64
	Scope       ScopeRef
	CandidateID string
	SourceKind  string
	SourceRef   string
	SubjectKey  string
	Summary     string
	Evidence    []RecordReference
	ContentHash string
	Confidence  float64
	ObservedAt  time.Time
	CreatedAt   time.Time
}

type CuriosityObservationInput struct {
	LeaseID     string
	CandidateID string
	SourceKind  string
	SourceRef   string
	SubjectKey  string
	Summary     string
	Evidence    []RecordReference
	ContentHash string
	Confidence  float64
	ObservedAt  time.Time
}

func NormalizeCuriosityLease(lease CuriosityLease, now time.Time) CuriosityLease {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	lease.ID = strings.TrimSpace(lease.ID)
	lease.Status = normalizeCuriosityToken(lease.Status)
	if lease.Status == "" {
		lease.Status = CuriosityLeaseStatusActive
	}
	lease.Scope = NormalizeScopeRef(lease.Scope)
	lease.LeaseClass = normalizeCuriosityToken(lease.LeaseClass)
	if lease.LeaseClass == "" {
		lease.LeaseClass = CuriosityLeaseClassReadOnly
	}
	lease.WorkAction = normalizeCuriosityToken(lease.WorkAction)
	if lease.WorkAction == "" {
		lease.WorkAction = CuriosityWorkActionLook
	}
	lease.AllowedSourceKinds = normalizeCuriosityTokens(lease.AllowedSourceKinds)
	lease.AllowedSourceRefs = normalizeCuriosityRefs(lease.AllowedSourceRefs)
	if lease.DailyTurnBudget < 0 {
		lease.DailyTurnBudget = 0
	}
	if lease.MaxLooksPerTurn < 0 {
		lease.MaxLooksPerTurn = 0
	}
	if lease.TurnsUsed < 0 {
		lease.TurnsUsed = 0
	}
	lease.PeriodStart = strings.TrimSpace(lease.PeriodStart)
	if lease.PeriodStart == "" {
		lease.PeriodStart = now.Format("2006-01-02")
	}
	lease.ApprovedBy = strings.TrimSpace(lease.ApprovedBy)
	if lease.CreatedAt.IsZero() {
		lease.CreatedAt = now
	} else {
		lease.CreatedAt = lease.CreatedAt.UTC()
	}
	if lease.ExpiresAt.IsZero() {
		lease.ExpiresAt = now
	} else {
		lease.ExpiresAt = lease.ExpiresAt.UTC()
	}
	if lease.UpdatedAt.IsZero() {
		lease.UpdatedAt = now
	} else {
		lease.UpdatedAt = lease.UpdatedAt.UTC()
	}
	return lease
}

func NormalizeCuriosityObservationInput(input CuriosityObservationInput, now time.Time) CuriosityObservationInput {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	input.LeaseID = strings.TrimSpace(input.LeaseID)
	input.CandidateID = strings.TrimSpace(input.CandidateID)
	input.SourceKind = normalizeCuriosityToken(input.SourceKind)
	input.SourceRef = strings.TrimSpace(input.SourceRef)
	input.SubjectKey = normalizeCuriositySubject(input.SubjectKey)
	input.Summary = strings.TrimSpace(input.Summary)
	input.Evidence = NormalizeRecordReferences(input.Evidence)
	input.ContentHash = strings.TrimSpace(input.ContentHash)
	input.Confidence = clampCuriosityConfidence(input.Confidence)
	if input.ObservedAt.IsZero() {
		input.ObservedAt = now
	} else {
		input.ObservedAt = input.ObservedAt.UTC()
	}
	if input.SubjectKey == "" {
		input.SubjectKey = normalizeCuriositySubject(input.SourceKind + "-" + shortCuriosityHash(input.SourceRef, input.Summary))
	}
	if input.ContentHash == "" {
		input.ContentHash = "sha256:" + shortCuriosityHash(input.SourceKind, input.SourceRef, input.Summary)
	}
	return input
}

func CuriosityLeaseID(periodStart string, allowedSourceKinds []string, allowedSourceRefs []string) string {
	periodStart = strings.TrimSpace(periodStart)
	if periodStart == "" {
		periodStart = time.Now().UTC().Format("2006-01-02")
	}
	payload, _ := json.Marshal(struct {
		Kinds []string `json:"kinds"`
		Refs  []string `json:"refs"`
	}{
		Kinds: normalizeCuriosityTokens(allowedSourceKinds),
		Refs:  normalizeCuriosityRefs(allowedSourceRefs),
	})
	sum := sha256.Sum256(payload)
	return "curiosity-" + periodStart + "-" + hex.EncodeToString(sum[:])[:12]
}

func normalizeCuriosityToken(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	value = strings.ReplaceAll(value, " ", "_")
	return value
}

func normalizeCuriositySubject(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	value = strings.ReplaceAll(value, " ", "-")
	return value
}

func normalizeCuriosityTokens(values []string) []string {
	out := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = normalizeCuriosityToken(value)
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

func normalizeCuriosityRefs(values []string) []string {
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

func clampCuriosityConfidence(value float64) float64 {
	if value < 0 || math.IsNaN(value) || math.IsInf(value, 0) {
		return 0
	}
	if value > 1 {
		return 1
	}
	return value
}

func shortCuriosityHash(parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:])
}
