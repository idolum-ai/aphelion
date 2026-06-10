//go:build linux

package session

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"
)

const (
	InteriorSignalDefaultHalfLife     = 12 * time.Hour
	InteriorSignalDefaultDedupeWindow = 12 * time.Hour
	InteriorSignalDefaultCooldown     = 6 * time.Hour

	InteriorSignalMinimumIntensity                = 0.005
	InteriorSignalZeroWeightObservationRetention  = 24 * time.Hour
	InteriorSignalAppliedObservationRetention     = 30 * 24 * time.Hour
	InteriorSignalInactiveStateObservationHorizon = 30 * 24 * time.Hour
)

type InteriorSignalObservationInput struct {
	Category          string
	SubjectKey        string
	Summary           string
	Source            string
	Evidence          []RecordReference
	SourceFingerprint string
	Weight            float64
	Confidence        float64
	ObservedAt        time.Time
}

type InteriorSignalObservation struct {
	ID                int64
	SessionID         string
	ChatID            int64
	UserID            int64
	Scope             ScopeRef
	Category          string
	SubjectKey        string
	Summary           string
	Source            string
	Evidence          []RecordReference
	SourceFingerprint string
	Weight            float64
	AppliedWeight     float64
	Confidence        float64
	ObservedAt        time.Time
	CreatedAt         time.Time
}

type InteriorSignalState struct {
	SessionID        string
	ChatID           int64
	UserID           int64
	Scope            ScopeRef
	Category         string
	SubjectKey       string
	Summary          string
	Evidence         []RecordReference
	Intensity        float64
	Confidence       float64
	ObservationCount int
	LastObservedAt   time.Time
	LastDecayedAt    time.Time
	LastSurfacedAt   time.Time
	CooldownUntil    time.Time
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

type InteriorSignalRef struct {
	Category   string
	SubjectKey string
}

func NormalizeInteriorSignalObservationInput(input InteriorSignalObservationInput, now time.Time) InteriorSignalObservationInput {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	input.Category = normalizeInteriorSignalToken(input.Category)
	input.SubjectKey = normalizeInteriorSignalSubject(input.SubjectKey)
	input.Summary = strings.TrimSpace(input.Summary)
	input.Source = normalizeInteriorSignalToken(input.Source)
	input.Evidence = NormalizeRecordReferences(input.Evidence)
	input.SourceFingerprint = strings.TrimSpace(input.SourceFingerprint)
	input.Weight = clampInteriorSignal(input.Weight)
	input.Confidence = clampInteriorSignal(input.Confidence)
	if input.ObservedAt.IsZero() {
		input.ObservedAt = now.UTC()
	} else {
		input.ObservedAt = input.ObservedAt.UTC()
	}
	if input.Source == "" {
		input.Source = "hidden_input"
	}
	if input.SubjectKey == "" {
		input.SubjectKey = interiorSignalSubjectKey(input.Category, input.Source, input.Evidence, input.SourceFingerprint, input.Summary)
	}
	if input.SourceFingerprint == "" {
		input.SourceFingerprint = interiorSignalFingerprint(input.Category, input.SubjectKey, input.Source, input.Summary, input.Evidence)
	}
	return input
}

func NormalizeInteriorSignalRef(ref InteriorSignalRef) InteriorSignalRef {
	return InteriorSignalRef{
		Category:   normalizeInteriorSignalToken(ref.Category),
		SubjectKey: normalizeInteriorSignalSubject(ref.SubjectKey),
	}
}

func DecayInteriorSignalIntensity(intensity float64, lastDecayedAt time.Time, now time.Time) float64 {
	intensity = clampInteriorSignal(intensity)
	if intensity == 0 || lastDecayedAt.IsZero() || now.IsZero() || !now.After(lastDecayedAt) {
		return intensity
	}
	elapsed := now.Sub(lastDecayedAt)
	factor := math.Pow(0.5, float64(elapsed)/float64(InteriorSignalDefaultHalfLife))
	decayed := clampInteriorSignal(intensity * factor)
	if decayed < InteriorSignalMinimumIntensity {
		return 0
	}
	return decayed
}

func InteriorSignalInCooldown(state InteriorSignalState, now time.Time) bool {
	return !state.CooldownUntil.IsZero() && now.Before(state.CooldownUntil)
}

func interiorSignalSubjectKey(category string, source string, evidence []RecordReference, sourceFingerprint string, summary string) string {
	seed := interiorSignalSubjectSeed(category, source, evidence, sourceFingerprint, summary)
	terms := strings.FieldsFunc(strings.ToLower(seed), func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9')
	})
	parts := make([]string, 0, 4)
	seen := make(map[string]struct{})
	for _, term := range terms {
		if len(term) < 4 {
			continue
		}
		if _, ok := seen[term]; ok {
			continue
		}
		seen[term] = struct{}{}
		parts = append(parts, term)
		if len(parts) == 4 {
			break
		}
	}
	if len(parts) == 0 {
		parts = []string{"signal"}
	}
	hash := sha256.Sum256([]byte(seed))
	return strings.Join(parts, "-") + "-" + hex.EncodeToString(hash[:])[:8]
}

func interiorSignalSubjectSeed(category string, source string, evidence []RecordReference, sourceFingerprint string, summary string) string {
	category = normalizeInteriorSignalToken(category)
	source = normalizeInteriorSignalToken(source)
	evidence = NormalizeRecordReferences(evidence)
	for _, ref := range evidence {
		kind, value, ok := interiorSignalDurableEvidenceIdentity(ref)
		if !ok {
			continue
		}
		return strings.TrimSpace(category + " " + source + " " + kind + ":" + value)
	}
	sourceFingerprint = strings.TrimSpace(sourceFingerprint)
	if sourceFingerprint != "" {
		return strings.TrimSpace(category + " " + source + " " + sourceFingerprint)
	}
	return strings.TrimSpace(category + " " + summary)
}

func interiorSignalDurableEvidenceIdentity(ref RecordReference) (string, string, bool) {
	kind := strings.TrimSpace(ref.Kind)
	value := strings.TrimSpace(ref.Ref)
	if kind == "" || value == "" {
		return "", "", false
	}
	switch kind {
	case "memory_file":
		if idx := strings.Index(value, ":sha256:"); idx > 0 {
			value = value[:idx]
		}
	case "memory_store":
	default:
		return "", "", false
	}
	if value == "" {
		return "", "", false
	}
	return kind, value, true
}

func interiorSignalFingerprint(category, subjectKey, source, summary string, evidence []RecordReference) string {
	payload, _ := json.Marshal(struct {
		Category   string            `json:"category"`
		SubjectKey string            `json:"subject_key"`
		Source     string            `json:"source"`
		Summary    string            `json:"summary"`
		Evidence   []RecordReference `json:"evidence"`
	}{
		Category:   normalizeInteriorSignalToken(category),
		SubjectKey: normalizeInteriorSignalSubject(subjectKey),
		Source:     normalizeInteriorSignalToken(source),
		Summary:    strings.TrimSpace(summary),
		Evidence:   NormalizeRecordReferences(evidence),
	})
	sum := sha256.Sum256(payload)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func normalizeInteriorSignalToken(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	value = strings.ReplaceAll(value, " ", "_")
	return value
}

func normalizeInteriorSignalSubject(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	value = strings.ReplaceAll(value, " ", "-")
	return value
}

func clampInteriorSignal(value float64) float64 {
	if value < 0 || math.IsNaN(value) || math.IsInf(value, 0) {
		return 0
	}
	if value > 1 {
		return 1
	}
	return value
}

func encodeInteriorSignalEvidence(refs []RecordReference) (string, error) {
	refs = NormalizeRecordReferences(refs)
	if len(refs) == 0 {
		return "[]", nil
	}
	raw, err := json.Marshal(refs)
	if err != nil {
		return "", fmt.Errorf("encode interior signal evidence: %w", err)
	}
	return string(raw), nil
}

func decodeInteriorSignalEvidence(raw string) ([]RecordReference, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	var refs []RecordReference
	if err := json.Unmarshal([]byte(raw), &refs); err != nil {
		return nil, fmt.Errorf("decode interior signal evidence: %w", err)
	}
	return NormalizeRecordReferences(refs), nil
}
