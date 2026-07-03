//go:build linux

package session

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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
	IdentificationPropertyApprovalClass  IdentificationObservationProperty = "approval_class"
	IdentificationPropertyResource       IdentificationObservationProperty = "resource"
	IdentificationPropertyTimeout        IdentificationObservationProperty = "timeout"
	IdentificationPropertyRetryability   IdentificationObservationProperty = "retryability"
	IdentificationPropertyBundleFit      IdentificationObservationProperty = "bundle_fit"
	IdentificationPropertyContract       IdentificationObservationProperty = "contract"
	IdentificationPropertyOperatorAction IdentificationObservationProperty = "operator_action"
	IdentificationPropertyTool           IdentificationObservationProperty = "tool"
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
	ObservationID   string
	EntryID         string
	Method          IdentificationObservationMethod
	Property        IdentificationObservationProperty
	Value           string
	EvidenceRef     string
	ActorKind       string
	ActorPrincipal  string
	ActorAction     string
	ExpiresAt       time.Time
	ObservedAt      time.Time
	LastObservedAt  time.Time
	OccurrenceCount int
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
	ObservationID   string
	EntryID         string
	Method          IdentificationObservationMethod
	Property        IdentificationObservationProperty
	Value           string
	EvidenceRef     string
	ActorKind       string
	ActorPrincipal  string
	ActorAction     string
	ExpiresAt       time.Time
	ObservedAt      time.Time
	LastObservedAt  time.Time
	OccurrenceCount int
}

type IdentificationLedgerProjection struct {
	Entry        IdentificationLedgerEntry
	Observations []IdentificationLedgerObservation
	Properties   map[IdentificationObservationProperty][]IdentificationLedgerObservation
}

type AuthorityShapeInput struct {
	Tool          string
	Action        string
	LeaseClass    ContinuationLeaseClass
	ResourceClass string
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
		return ""
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
		IdentificationPropertyOperatorAction,
		IdentificationPropertyTool:
		return IdentificationObservationProperty(normalizeEnumValue(string(property)))
	default:
		return ""
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
	input.ActorKind = normalizeEnumValue(input.ActorKind)
	input.ActorPrincipal = strings.TrimSpace(input.ActorPrincipal)
	input.ActorAction = normalizeEnumValue(input.ActorAction)
	if !input.ExpiresAt.IsZero() {
		input.ExpiresAt = input.ExpiresAt.UTC()
	}
	if !input.ObservedAt.IsZero() {
		input.ObservedAt = input.ObservedAt.UTC()
	}
	if !input.LastObservedAt.IsZero() {
		input.LastObservedAt = input.LastObservedAt.UTC()
	}
	if input.LastObservedAt.IsZero() {
		input.LastObservedAt = input.ObservedAt
	}
	if input.OccurrenceCount <= 0 {
		input.OccurrenceCount = 1
	}
	if input.ObservationID == "" {
		input.ObservationID = IdentificationLedgerObservationIDWithActor(input.EntryID, input.Method, input.Property, input.Value, input.EvidenceRef, input.ActorKind, input.ActorPrincipal, input.ActorAction)
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
		ObservationID:   obs.ObservationID,
		EntryID:         obs.EntryID,
		Method:          obs.Method,
		Property:        obs.Property,
		Value:           obs.Value,
		EvidenceRef:     obs.EvidenceRef,
		ActorKind:       obs.ActorKind,
		ActorPrincipal:  obs.ActorPrincipal,
		ActorAction:     obs.ActorAction,
		ExpiresAt:       obs.ExpiresAt,
		ObservedAt:      obs.ObservedAt,
		LastObservedAt:  obs.LastObservedAt,
		OccurrenceCount: obs.OccurrenceCount,
	})
	return IdentificationLedgerObservation(normalized)
}

func NormalizeAuthorityShapeInput(input AuthorityShapeInput) AuthorityShapeInput {
	input.Tool = strings.ToLower(strings.TrimSpace(input.Tool))
	input.Action = normalizeRecoveryAction(input.Action)
	input.LeaseClass = NormalizeContinuationLeaseClass(input.LeaseClass)
	input.ResourceClass = AuthorityResourceClass(input.ResourceClass)
	return input
}

func AuthorityShapeHash(input AuthorityShapeInput) string {
	input = NormalizeAuthorityShapeInput(input)
	if input.Tool == "" && input.Action == "" && input.LeaseClass == "" && input.ResourceClass == "" {
		return ""
	}
	payload := map[string]any{
		"contract":       "aphelion.authority_shape.v1",
		"tool":           input.Tool,
		"action":         input.Action,
		"lease_class":    string(input.LeaseClass),
		"resource_class": input.ResourceClass,
	}
	raw, _ := json.Marshal(payload)
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func AuthorityShapeHashForContinuationRecoveryContract(contract ContinuationRecoveryContract) string {
	contract = NormalizeContinuationRecoveryContract(contract)
	resourceClass := AuthorityResourceClass(firstNonEmptyStore(contract.GrantTargetResource, contract.Resource))
	if resourceClass == "" && contract.LeaseClass == ContinuationLeaseClassChildWake {
		resourceClass = "durable_agent"
	}
	return AuthorityShapeHash(AuthorityShapeInput{
		Tool:          contract.Tool,
		Action:        contract.ToolAction,
		LeaseClass:    contract.LeaseClass,
		ResourceClass: resourceClass,
	})
}

func AuthorityResourceClass(resource string) string {
	resource = strings.TrimSpace(resource)
	if resource == "" {
		return ""
	}
	if strings.HasPrefix(resource, "/") {
		return "file_path"
	}
	if idx := strings.Index(resource, ":"); idx > 0 {
		return normalizeEnumValue(resource[:idx])
	}
	return normalizeEnumValue(resource)
}

func ValidateIdentificationLedgerEntryInput(input IdentificationLedgerEntryInput) error {
	if err := ValidateIdentificationLedgerEntryEnums(input); err != nil {
		return err
	}
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
	if input.Status == "" {
		return fmt.Errorf("identification ledger entry requires status")
	}
	if want := IdentificationLedgerEntryID(input.PlanID, input.PlanVersion, input.SessionID, input.StepRef, input.ShapeHash); input.EntryID != want {
		return fmt.Errorf("identification ledger entry_id mismatch")
	}
	return nil
}

func ValidateIdentificationLedgerObservationInput(input IdentificationLedgerObservationInput) error {
	if err := ValidateIdentificationLedgerObservationEnums(input); err != nil {
		return err
	}
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
	if want := IdentificationLedgerObservationIDWithActor(input.EntryID, input.Method, input.Property, input.Value, input.EvidenceRef, input.ActorKind, input.ActorPrincipal, input.ActorAction); input.ObservationID != want {
		return fmt.Errorf("identification ledger observation_id mismatch")
	}
	return nil
}

func ValidateIdentificationLedgerEntryEnums(input IdentificationLedgerEntryInput) error {
	if raw := strings.TrimSpace(string(input.Status)); raw != "" && !identificationLedgerEntryStatusKnown(raw) {
		return fmt.Errorf("identification ledger entry unknown status %q", raw)
	}
	return nil
}

func ValidateIdentificationLedgerObservationEnums(input IdentificationLedgerObservationInput) error {
	if raw := strings.TrimSpace(string(input.Method)); raw != "" && !identificationObservationMethodKnown(raw) {
		return fmt.Errorf("identification ledger observation unknown method %q", raw)
	}
	if raw := strings.TrimSpace(string(input.Property)); raw != "" && !identificationObservationPropertyKnown(raw) {
		return fmt.Errorf("identification ledger observation unknown property %q", raw)
	}
	return nil
}

func identificationLedgerEntryStatusKnown(raw string) bool {
	switch IdentificationLedgerEntryStatus(normalizeEnumValue(raw)) {
	case IdentificationLedgerStatusUnidentified,
		IdentificationLedgerStatusPartial,
		IdentificationLedgerStatusProposed,
		IdentificationLedgerStatusApproved,
		IdentificationLedgerStatusConsumed,
		IdentificationLedgerStatusExpired,
		IdentificationLedgerStatusInvalidated:
		return true
	default:
		return false
	}
}

func identificationObservationMethodKnown(raw string) bool {
	switch IdentificationObservationMethod(normalizeEnumValue(raw)) {
	case IdentificationObservationCollision,
		IdentificationObservationStatic,
		IdentificationObservationLookahead,
		IdentificationObservationOperator:
		return true
	default:
		return false
	}
}

func identificationObservationPropertyKnown(raw string) bool {
	switch IdentificationObservationProperty(normalizeEnumValue(raw)) {
	case IdentificationPropertyApprovalClass,
		IdentificationPropertyResource,
		IdentificationPropertyTimeout,
		IdentificationPropertyRetryability,
		IdentificationPropertyBundleFit,
		IdentificationPropertyContract,
		IdentificationPropertyOperatorAction,
		IdentificationPropertyTool:
		return true
	default:
		return false
	}
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
	return IdentificationLedgerObservationIDWithActor(entryID, method, property, value, evidenceRef, "", "", "")
}

func IdentificationLedgerObservationIDWithActor(entryID string, method IdentificationObservationMethod, property IdentificationObservationProperty, value string, evidenceRef string, actorKind string, actorPrincipal string, actorAction string) string {
	seed := strings.Join([]string{
		strings.TrimSpace(entryID),
		string(NormalizeIdentificationObservationMethod(method)),
		string(NormalizeIdentificationObservationProperty(property)),
		strings.TrimSpace(value),
		strings.TrimSpace(evidenceRef),
		normalizeEnumValue(actorKind),
		strings.TrimSpace(actorPrincipal),
		normalizeEnumValue(actorAction),
	}, "\x00")
	sum := sha256.Sum256([]byte(seed))
	return "idobs:" + hex.EncodeToString(sum[:16])
}
