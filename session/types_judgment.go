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

type JudgmentCompleteness string

const (
	JudgmentCompletenessComplete JudgmentCompleteness = "complete"
	JudgmentCompletenessPartial  JudgmentCompleteness = "partial"
	JudgmentCompletenessAbstain  JudgmentCompleteness = "abstain"
)

type UnknownPredicate struct {
	Kind   string `json:"kind"`
	Target string `json:"target,omitempty"`
	Reason string `json:"reason,omitempty"`
}

type Judgment struct {
	ID                 string
	SessionID          string
	ChatID             int64
	UserID             int64
	Scope              ScopeRef
	TurnRunID          int64
	OperationID        string
	Kind               string
	SchemaVersion      string
	SubjectKey         string
	ClaimKey           string
	InterpreterID      string
	InterpreterVersion string
	InputRefs          []string
	InputHash          string
	ResultJSON         string
	Completeness       JudgmentCompleteness
	Unknowns           []UnknownPredicate
	DependencyRefs     []JudgmentDependencyRef
	SourceFaultDomains []string
	Sensitivity        string
	ContentHash        string
	AsOf               time.Time
	ExpiresAt          time.Time
	CreatedAt          time.Time
}

type JudgmentInput struct {
	ID                 string
	Key                SessionKey
	SessionID          string
	TurnRunID          int64
	OperationID        string
	Kind               string
	SchemaVersion      string
	SubjectKey         string
	ClaimKey           string
	InterpreterID      string
	InterpreterVersion string
	InputRefs          []string
	InputHash          string
	ResultJSON         string
	Completeness       JudgmentCompleteness
	Unknowns           []UnknownPredicate
	DependencyRefs     []JudgmentDependencyRef
	SourceFaultDomains []string
	Sensitivity        string
	ContentHash        string
	AsOf               time.Time
	ExpiresAt          time.Time
	CreatedAt          time.Time
}

type JudgmentChallengeEventKind string

const (
	JudgmentChallengeOpened                      JudgmentChallengeEventKind = "challenge_opened"
	JudgmentChallengeGroundAttached              JudgmentChallengeEventKind = "ground_attached"
	JudgmentChallengeAdjudicationRecorded        JudgmentChallengeEventKind = "adjudication_recorded"
	JudgmentChallengeOperationalResponseRecorded JudgmentChallengeEventKind = "operational_response_recorded"
)

type JudgmentChallengeDisposition string

const (
	JudgmentChallengeSupported    JudgmentChallengeDisposition = "supported"
	JudgmentChallengeContradicted JudgmentChallengeDisposition = "contradicted"
	JudgmentChallengeUnresolved   JudgmentChallengeDisposition = "unresolved"
)

type JudgmentEligibilityStatus string

const (
	JudgmentEligibilityEligible   JudgmentEligibilityStatus = "eligible"
	JudgmentEligibilitySuspended  JudgmentEligibilityStatus = "suspended"
	JudgmentEligibilitySuperseded JudgmentEligibilityStatus = "superseded"
	JudgmentEligibilityExpired    JudgmentEligibilityStatus = "expired"
)

type JudgmentOperationalResponse string

const (
	JudgmentOperationalResponseNone           JudgmentOperationalResponse = "none"
	JudgmentOperationalResponseRecompute      JudgmentOperationalResponse = "recompute"
	JudgmentOperationalResponseBlock          JudgmentOperationalResponse = "block"
	JudgmentOperationalResponseVerify         JudgmentOperationalResponse = "verify"
	JudgmentOperationalResponseRetract        JudgmentOperationalResponse = "retract"
	JudgmentOperationalResponseForwardCorrect JudgmentOperationalResponse = "forward_correct"
	JudgmentOperationalResponseEscalate       JudgmentOperationalResponse = "escalate"
)

type JudgmentChallengeEvent struct {
	EventID             string
	ChallengeID         string
	JudgmentID          string
	SessionID           string
	ChatID              int64
	UserID              int64
	Scope               ScopeRef
	EventKind           JudgmentChallengeEventKind
	GroundRefs          []JudgmentDependencyRef
	Disposition         JudgmentChallengeDisposition
	EligibilityStatus   JudgmentEligibilityStatus
	OperationalResponse JudgmentOperationalResponse
	Reason              string
	CreatedAt           time.Time
}

type JudgmentChallengeEventInput struct {
	EventID             string
	ChallengeID         string
	JudgmentID          string
	Key                 SessionKey
	SessionID           string
	EventKind           JudgmentChallengeEventKind
	GroundRefs          []JudgmentDependencyRef
	Disposition         JudgmentChallengeDisposition
	EligibilityStatus   JudgmentEligibilityStatus
	OperationalResponse JudgmentOperationalResponse
	Reason              string
	CreatedAt           time.Time
}

func JudgmentRef(id string) string {
	return JudgmentUseRef("judgment", id)
}

func NormalizeJudgmentInput(input JudgmentInput) (JudgmentInput, error) {
	input.SessionID = strings.TrimSpace(input.SessionID)
	if input.SessionID == "" {
		input.SessionID = SessionIDForKey(input.Key)
	}
	input.OperationID = strings.TrimSpace(input.OperationID)
	input.Kind = judgmentUseToken(input.Kind)
	input.SchemaVersion = strings.TrimSpace(input.SchemaVersion)
	if input.SchemaVersion == "" {
		input.SchemaVersion = "v1"
	}
	input.SubjectKey = strings.TrimSpace(input.SubjectKey)
	input.ClaimKey = strings.TrimSpace(input.ClaimKey)
	input.InterpreterID = judgmentUseToken(input.InterpreterID)
	input.InterpreterVersion = strings.TrimSpace(input.InterpreterVersion)
	if input.InterpreterVersion == "" {
		input.InterpreterVersion = "v1"
	}
	input.InputRefs = normalizeStringList(input.InputRefs)
	input.InputHash = strings.TrimSpace(input.InputHash)
	input.ResultJSON = normalizeJudgmentJSON(input.ResultJSON)
	input.Completeness = NormalizeJudgmentCompleteness(input.Completeness)
	input.Unknowns = normalizeUnknownPredicates(input.Unknowns)
	input.DependencyRefs = normalizeJudgmentDependencyRefs(input.DependencyRefs)
	input.SourceFaultDomains = normalizeStringList(input.SourceFaultDomains)
	input.Sensitivity = judgmentUseToken(input.Sensitivity)
	if input.Sensitivity == "" {
		input.Sensitivity = "ordinary"
	}
	input.ContentHash = strings.TrimSpace(input.ContentHash)
	if input.ContentHash == "" {
		input.ContentHash = JudgmentContentHash(input.ResultJSON, input.Unknowns, input.DependencyRefs)
	}
	if input.AsOf.IsZero() {
		input.AsOf = time.Now().UTC()
	}
	if input.CreatedAt.IsZero() {
		input.CreatedAt = input.AsOf
	}
	if input.ID == "" {
		input.ID = judgmentID(input)
	}
	input.ID = strings.TrimSpace(input.ID)
	if input.SessionID == "" {
		return JudgmentInput{}, fmt.Errorf("judgment requires session_id")
	}
	if input.Kind == "" {
		return JudgmentInput{}, fmt.Errorf("judgment requires kind")
	}
	if input.InterpreterID == "" {
		return JudgmentInput{}, fmt.Errorf("judgment requires interpreter_id")
	}
	if input.SubjectKey == "" && input.ClaimKey == "" {
		return JudgmentInput{}, fmt.Errorf("judgment requires subject_key or claim_key")
	}
	if input.ResultJSON == "" {
		return JudgmentInput{}, fmt.Errorf("judgment requires result_json")
	}
	return input, nil
}

func NormalizeJudgmentCompleteness(value JudgmentCompleteness) JudgmentCompleteness {
	switch JudgmentCompleteness(judgmentUseToken(string(value))) {
	case JudgmentCompletenessPartial, JudgmentCompletenessAbstain:
		return JudgmentCompleteness(judgmentUseToken(string(value)))
	default:
		return JudgmentCompletenessComplete
	}
}

func JudgmentContentHash(resultJSON string, unknowns []UnknownPredicate, deps []JudgmentDependencyRef) string {
	seed := strings.Join([]string{
		normalizeJudgmentJSON(resultJSON),
		encodeUnknownPredicates(unknowns),
		encodeJudgmentDependencyRefs(deps),
	}, "\x00")
	sum := sha256.Sum256([]byte(seed))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func NormalizeJudgmentChallengeEventInput(input JudgmentChallengeEventInput) (JudgmentChallengeEventInput, error) {
	input.ChallengeID = strings.TrimSpace(input.ChallengeID)
	input.JudgmentID = strings.TrimSpace(input.JudgmentID)
	input.SessionID = strings.TrimSpace(input.SessionID)
	if input.SessionID == "" {
		input.SessionID = SessionIDForKey(input.Key)
	}
	input.EventKind = NormalizeJudgmentChallengeEventKind(input.EventKind)
	input.GroundRefs = normalizeJudgmentDependencyRefs(input.GroundRefs)
	input.Disposition = NormalizeJudgmentChallengeDisposition(input.Disposition)
	input.EligibilityStatus = NormalizeJudgmentEligibilityStatus(input.EligibilityStatus)
	input.OperationalResponse = NormalizeJudgmentOperationalResponse(input.OperationalResponse)
	input.Reason = strings.TrimSpace(input.Reason)
	if input.CreatedAt.IsZero() {
		input.CreatedAt = time.Now().UTC()
	}
	if input.ChallengeID == "" && input.JudgmentID != "" {
		input.ChallengeID = "jchal_" + shortJudgmentHash(input.JudgmentID)
	}
	if input.EventID == "" {
		input.EventID = judgmentChallengeEventID(input)
	}
	input.EventID = strings.TrimSpace(input.EventID)
	if input.ChallengeID == "" {
		return JudgmentChallengeEventInput{}, fmt.Errorf("judgment challenge event requires challenge_id")
	}
	if input.JudgmentID == "" {
		return JudgmentChallengeEventInput{}, fmt.Errorf("judgment challenge event requires judgment_id")
	}
	return input, nil
}

func NormalizeJudgmentChallengeEventKind(value JudgmentChallengeEventKind) JudgmentChallengeEventKind {
	switch JudgmentChallengeEventKind(judgmentUseToken(string(value))) {
	case JudgmentChallengeGroundAttached, JudgmentChallengeAdjudicationRecorded, JudgmentChallengeOperationalResponseRecorded:
		return JudgmentChallengeEventKind(judgmentUseToken(string(value)))
	default:
		return JudgmentChallengeOpened
	}
}

func NormalizeJudgmentChallengeDisposition(value JudgmentChallengeDisposition) JudgmentChallengeDisposition {
	switch JudgmentChallengeDisposition(judgmentUseToken(string(value))) {
	case JudgmentChallengeSupported, JudgmentChallengeContradicted:
		return JudgmentChallengeDisposition(judgmentUseToken(string(value)))
	default:
		return JudgmentChallengeUnresolved
	}
}

func NormalizeJudgmentEligibilityStatus(value JudgmentEligibilityStatus) JudgmentEligibilityStatus {
	switch JudgmentEligibilityStatus(judgmentUseToken(string(value))) {
	case JudgmentEligibilityEligible, JudgmentEligibilitySuperseded, JudgmentEligibilityExpired:
		return JudgmentEligibilityStatus(judgmentUseToken(string(value)))
	default:
		return JudgmentEligibilitySuspended
	}
}

func NormalizeJudgmentOperationalResponse(value JudgmentOperationalResponse) JudgmentOperationalResponse {
	switch JudgmentOperationalResponse(judgmentUseToken(string(value))) {
	case JudgmentOperationalResponseRecompute, JudgmentOperationalResponseBlock, JudgmentOperationalResponseVerify,
		JudgmentOperationalResponseRetract, JudgmentOperationalResponseForwardCorrect, JudgmentOperationalResponseEscalate:
		return JudgmentOperationalResponse(judgmentUseToken(string(value)))
	default:
		return JudgmentOperationalResponseNone
	}
}

func judgmentID(input JudgmentInput) string {
	seed := strings.Join([]string{
		input.SessionID,
		fmt.Sprintf("%d", input.TurnRunID),
		input.OperationID,
		input.Kind,
		input.SchemaVersion,
		input.SubjectKey,
		input.ClaimKey,
		input.InterpreterID,
		input.InterpreterVersion,
		strings.Join(input.InputRefs, ","),
		input.InputHash,
		input.ContentHash,
	}, "\x00")
	sum := sha256.Sum256([]byte(seed))
	return "judg_" + hex.EncodeToString(sum[:16])
}

func judgmentChallengeEventID(input JudgmentChallengeEventInput) string {
	seed := strings.Join([]string{
		input.ChallengeID,
		input.JudgmentID,
		string(input.EventKind),
		encodeJudgmentDependencyRefs(input.GroundRefs),
		string(input.Disposition),
		string(input.EligibilityStatus),
		string(input.OperationalResponse),
		input.Reason,
		input.CreatedAt.UTC().Format(time.RFC3339Nano),
	}, "\x00")
	sum := sha256.Sum256([]byte(seed))
	return "jchev_" + hex.EncodeToString(sum[:16])
}

func shortJudgmentHash(seed string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(seed)))
	return hex.EncodeToString(sum[:8])
}

func normalizeJudgmentJSON(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "{}"
	}
	var payload any
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return "{}"
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "{}"
	}
	return string(encoded)
}

func normalizeUnknownPredicates(unknowns []UnknownPredicate) []UnknownPredicate {
	out := make([]UnknownPredicate, 0, len(unknowns))
	seen := map[string]struct{}{}
	for _, unknown := range unknowns {
		unknown.Kind = judgmentUseToken(unknown.Kind)
		unknown.Target = strings.TrimSpace(unknown.Target)
		unknown.Reason = strings.TrimSpace(unknown.Reason)
		if unknown.Kind == "" {
			continue
		}
		key := strings.Join([]string{unknown.Kind, unknown.Target, unknown.Reason}, "\x00")
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, unknown)
	}
	return out
}

func encodeUnknownPredicates(unknowns []UnknownPredicate) string {
	raw, err := json.Marshal(normalizeUnknownPredicates(unknowns))
	if err != nil {
		return "[]"
	}
	return string(raw)
}

func decodeUnknownPredicates(raw string) []UnknownPredicate {
	var unknowns []UnknownPredicate
	if err := json.Unmarshal([]byte(strings.TrimSpace(raw)), &unknowns); err != nil {
		return nil
	}
	return normalizeUnknownPredicates(unknowns)
}
