//go:build linux

package session

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

const (
	IdentificationDefaultPlanVersion = "current"
)

type IdentificationLedgerEntryStatus string

const (
	IdentificationLedgerStatusUnidentified IdentificationLedgerEntryStatus = "unidentified"
	IdentificationLedgerStatusPartial      IdentificationLedgerEntryStatus = "partial"
	IdentificationLedgerStatusProposed     IdentificationLedgerEntryStatus = "proposed"
	IdentificationLedgerStatusApproved     IdentificationLedgerEntryStatus = "approved"
	IdentificationLedgerStatusConsumed     IdentificationLedgerEntryStatus = "consumed"
	IdentificationLedgerStatusExpired      IdentificationLedgerEntryStatus = "expired"
	IdentificationLedgerStatusInvalidated  IdentificationLedgerEntryStatus = "invalidated"
)

type IdentificationObservationMethod string

const (
	IdentificationObservationCollision IdentificationObservationMethod = "collision"
	IdentificationObservationStatic    IdentificationObservationMethod = "static"
	IdentificationObservationLookahead IdentificationObservationMethod = "lookahead"
	IdentificationObservationOperator  IdentificationObservationMethod = "operator"
)

type IdentificationObservationProperty string

const (
	IdentificationPropertyApprovalClass IdentificationObservationProperty = "approval_class"
	IdentificationPropertyResource      IdentificationObservationProperty = "resource"
	IdentificationPropertyTimeout       IdentificationObservationProperty = "timeout"
	IdentificationPropertyRetryability  IdentificationObservationProperty = "retryability"
	IdentificationPropertyBundleFit     IdentificationObservationProperty = "bundle_fit"
	IdentificationPropertyContract      IdentificationObservationProperty = "contract"
	IdentificationPropertyTool          IdentificationObservationProperty = "tool"
)

type IdentificationLedgerEntry struct {
	EntryID     string
	PlanID      string
	PlanVersion string
	SessionID   string
	StepRef     string
	ShapeHash   string
	LabelRef    string
	Status      IdentificationLedgerEntryStatus
	ExpiresAt   time.Time
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

type IdentificationLedgerObservation struct {
	ObservationID string
	EntryID       string
	Method        IdentificationObservationMethod
	Property      IdentificationObservationProperty
	Value         string
	EvidenceRef   string
	ExpiresAt     time.Time
	ObservedAt    time.Time
}

type IdentificationLedgerEntryInput struct {
	EntryID     string
	PlanID      string
	PlanVersion string
	SessionID   string
	StepRef     string
	ShapeHash   string
	LabelRef    string
	Status      IdentificationLedgerEntryStatus
	ExpiresAt   time.Time
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

type IdentificationLedgerObservationInput struct {
	ObservationID string
	EntryID       string
	Method        IdentificationObservationMethod
	Property      IdentificationObservationProperty
	Value         string
	EvidenceRef   string
	ExpiresAt     time.Time
	ObservedAt    time.Time
}

type IdentificationLedgerProjection struct {
	Entry        IdentificationLedgerEntry
	Observations []IdentificationLedgerObservation
	Properties   map[IdentificationObservationProperty][]IdentificationLedgerObservation
}

func IdentificationPlanIDForSession(sessionID string) string {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return ""
	}
	return "session:" + sessionID
}

func NormalizeIdentificationLedgerEntryStatus(status IdentificationLedgerEntryStatus) IdentificationLedgerEntryStatus {
	switch IdentificationLedgerEntryStatus(normalizeEnumValue(string(status))) {
	case IdentificationLedgerStatusUnidentified,
		IdentificationLedgerStatusPartial,
		IdentificationLedgerStatusProposed,
		IdentificationLedgerStatusApproved,
		IdentificationLedgerStatusConsumed,
		IdentificationLedgerStatusExpired,
		IdentificationLedgerStatusInvalidated:
		return IdentificationLedgerEntryStatus(normalizeEnumValue(string(status)))
	default:
		return IdentificationLedgerStatusUnidentified
	}
}

func NormalizeIdentificationObservationMethod(method IdentificationObservationMethod) IdentificationObservationMethod {
	switch IdentificationObservationMethod(normalizeEnumValue(string(method))) {
	case IdentificationObservationCollision,
		IdentificationObservationStatic,
		IdentificationObservationLookahead,
		IdentificationObservationOperator:
		return IdentificationObservationMethod(normalizeEnumValue(string(method)))
	default:
		return ""
	}
}

func NormalizeIdentificationObservationProperty(property IdentificationObservationProperty) IdentificationObservationProperty {
	switch IdentificationObservationProperty(normalizeEnumValue(string(property))) {
	case IdentificationPropertyApprovalClass,
		IdentificationPropertyResource,
		IdentificationPropertyTimeout,
		IdentificationPropertyRetryability,
		IdentificationPropertyBundleFit,
		IdentificationPropertyContract,
		IdentificationPropertyTool:
		return IdentificationObservationProperty(normalizeEnumValue(string(property)))
	default:
		return IdentificationObservationProperty(normalizeEnumValue(string(property)))
	}
}

func NormalizeIdentificationLedgerEntryInput(input IdentificationLedgerEntryInput) IdentificationLedgerEntryInput {
	input.EntryID = strings.TrimSpace(input.EntryID)
	input.PlanID = strings.TrimSpace(input.PlanID)
	input.PlanVersion = strings.TrimSpace(input.PlanVersion)
	input.SessionID = strings.TrimSpace(input.SessionID)
	input.StepRef = strings.TrimSpace(input.StepRef)
	input.ShapeHash = strings.TrimSpace(input.ShapeHash)
	input.LabelRef = strings.TrimSpace(input.LabelRef)
	statusRaw := strings.TrimSpace(string(input.Status))
	input.Status = NormalizeIdentificationLedgerEntryStatus(input.Status)
	if input.PlanVersion == "" {
		input.PlanVersion = IdentificationDefaultPlanVersion
	}
	if statusRaw == "" {
		if input.LabelRef != "" {
			input.Status = IdentificationLedgerStatusProposed
		} else {
			input.Status = IdentificationLedgerStatusPartial
		}
	}
	if !input.ExpiresAt.IsZero() {
		input.ExpiresAt = input.ExpiresAt.UTC()
	}
	if !input.CreatedAt.IsZero() {
		input.CreatedAt = input.CreatedAt.UTC()
	}
	if !input.UpdatedAt.IsZero() {
		input.UpdatedAt = input.UpdatedAt.UTC()
	}
	if input.EntryID == "" {
		input.EntryID = IdentificationLedgerEntryID(input.PlanID, input.PlanVersion, input.SessionID, input.StepRef, input.ShapeHash)
	}
	return input
}

func NormalizeIdentificationLedgerObservationInput(input IdentificationLedgerObservationInput) IdentificationLedgerObservationInput {
	input.ObservationID = strings.TrimSpace(input.ObservationID)
	input.EntryID = strings.TrimSpace(input.EntryID)
	input.Method = NormalizeIdentificationObservationMethod(input.Method)
	input.Property = NormalizeIdentificationObservationProperty(input.Property)
	input.Value = strings.TrimSpace(input.Value)
	input.EvidenceRef = strings.TrimSpace(input.EvidenceRef)
	if !input.ExpiresAt.IsZero() {
		input.ExpiresAt = input.ExpiresAt.UTC()
	}
	if !input.ObservedAt.IsZero() {
		input.ObservedAt = input.ObservedAt.UTC()
	}
	if input.ObservationID == "" {
		input.ObservationID = IdentificationLedgerObservationID(input.EntryID, input.Method, input.Property, input.Value, input.EvidenceRef)
	}
	return input
}

func NormalizeIdentificationLedgerEntry(entry IdentificationLedgerEntry) IdentificationLedgerEntry {
	normalized := NormalizeIdentificationLedgerEntryInput(IdentificationLedgerEntryInput{
		EntryID:     entry.EntryID,
		PlanID:      entry.PlanID,
		PlanVersion: entry.PlanVersion,
		SessionID:   entry.SessionID,
		StepRef:     entry.StepRef,
		ShapeHash:   entry.ShapeHash,
		LabelRef:    entry.LabelRef,
		Status:      entry.Status,
		ExpiresAt:   entry.ExpiresAt,
		CreatedAt:   entry.CreatedAt,
		UpdatedAt:   entry.UpdatedAt,
	})
	return IdentificationLedgerEntry(normalized)
}

func NormalizeIdentificationLedgerObservation(obs IdentificationLedgerObservation) IdentificationLedgerObservation {
	normalized := NormalizeIdentificationLedgerObservationInput(IdentificationLedgerObservationInput{
		ObservationID: obs.ObservationID,
		EntryID:       obs.EntryID,
		Method:        obs.Method,
		Property:      obs.Property,
		Value:         obs.Value,
		EvidenceRef:   obs.EvidenceRef,
		ExpiresAt:     obs.ExpiresAt,
		ObservedAt:    obs.ObservedAt,
	})
	return IdentificationLedgerObservation(normalized)
}

func ValidateIdentificationLedgerEntryInput(input IdentificationLedgerEntryInput) error {
	input = NormalizeIdentificationLedgerEntryInput(input)
	if input.PlanID == "" {
		return fmt.Errorf("identification ledger entry requires plan_id")
	}
	if input.PlanVersion == "" {
		return fmt.Errorf("identification ledger entry requires plan_version")
	}
	if input.SessionID == "" {
		return fmt.Errorf("identification ledger entry requires session_id")
	}
	if input.StepRef == "" {
		return fmt.Errorf("identification ledger entry requires step_ref")
	}
	if input.ShapeHash == "" {
		return fmt.Errorf("identification ledger entry requires shape_hash")
	}
	if input.EntryID == "" {
		return fmt.Errorf("identification ledger entry requires entry_id")
	}
	if want := IdentificationLedgerEntryID(input.PlanID, input.PlanVersion, input.SessionID, input.StepRef, input.ShapeHash); input.EntryID != want {
		return fmt.Errorf("identification ledger entry_id mismatch")
	}
	return nil
}

func ValidateIdentificationLedgerObservationInput(input IdentificationLedgerObservationInput) error {
	input = NormalizeIdentificationLedgerObservationInput(input)
	if input.EntryID == "" {
		return fmt.Errorf("identification ledger observation requires entry_id")
	}
	if input.Method == "" {
		return fmt.Errorf("identification ledger observation requires method")
	}
	if input.Property == "" {
		return fmt.Errorf("identification ledger observation requires property")
	}
	if input.Value == "" {
		return fmt.Errorf("identification ledger observation requires value")
	}
	if input.ObservationID == "" {
		return fmt.Errorf("identification ledger observation requires observation_id")
	}
	if want := IdentificationLedgerObservationID(input.EntryID, input.Method, input.Property, input.Value, input.EvidenceRef); input.ObservationID != want {
		return fmt.Errorf("identification ledger observation_id mismatch")
	}
	return nil
}

func IdentificationLedgerEntryID(planID string, planVersion string, sessionID string, stepRef string, shapeHash string) string {
	seed := strings.Join([]string{
		strings.TrimSpace(planID),
		strings.TrimSpace(planVersion),
		strings.TrimSpace(sessionID),
		strings.TrimSpace(stepRef),
		strings.TrimSpace(shapeHash),
	}, "\x00")
	sum := sha256.Sum256([]byte(seed))
	return "ident:" + hex.EncodeToString(sum[:16])
}

func IdentificationLedgerObservationID(entryID string, method IdentificationObservationMethod, property IdentificationObservationProperty, value string, evidenceRef string) string {
	seed := strings.Join([]string{
		strings.TrimSpace(entryID),
		string(NormalizeIdentificationObservationMethod(method)),
		string(NormalizeIdentificationObservationProperty(property)),
		strings.TrimSpace(value),
		strings.TrimSpace(evidenceRef),
	}, "\x00")
	sum := sha256.Sum256([]byte(seed))
	return "idobs:" + hex.EncodeToString(sum[:16])
}
