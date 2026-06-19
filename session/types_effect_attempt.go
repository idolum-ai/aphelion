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

type EffectAttemptStatus string

const (
	EffectAttemptStatusAttempted  EffectAttemptStatus = "attempted"
	EffectAttemptStatusExecuted   EffectAttemptStatus = "executed"
	EffectAttemptStatusSucceeded  EffectAttemptStatus = EffectAttemptStatusExecuted
	EffectAttemptStatusFailed     EffectAttemptStatus = "failed"
	EffectAttemptStatusUncertain  EffectAttemptStatus = "uncertain"
	EffectAttemptStatusVerified   EffectAttemptStatus = "verified"
	EffectAttemptStatusRejected   EffectAttemptStatus = "rejected"
	EffectAttemptStatusSuperseded EffectAttemptStatus = "superseded"
)

type EffectAttempt struct {
	AttemptID           string
	SessionID           string
	ChatID              int64
	UserID              int64
	Scope               ScopeRef
	TurnRunID           int64
	OperationID         string
	PhaseID             string
	LeaseID             string
	ProposalID          string
	WorkMode            string
	Executor            string
	Tool                string
	Command             string
	CommandHash         string
	EffectKind          string
	EffectReason        string
	BoundaryKind        string
	AuthorizationReason string
	SubjectJSON         string
	Status              EffectAttemptStatus
	ErrorText           string
	EvidenceRefs        []string
	StartedAt           time.Time
	CompletedAt         time.Time
	UpdatedAt           time.Time
}

type EffectAttemptInput struct {
	AttemptID           string
	Key                 SessionKey
	TurnRunID           int64
	OperationID         string
	PhaseID             string
	LeaseID             string
	ProposalID          string
	WorkMode            string
	Executor            string
	Tool                string
	Command             string
	EffectKind          string
	EffectReason        string
	BoundaryKind        string
	AuthorizationReason string
	SubjectJSON         string
	Status              EffectAttemptStatus
	ErrorText           string
	EvidenceRefs        []string
	StartedAt           time.Time
	CompletedAt         time.Time
	UpdatedAt           time.Time
}

func EffectAttemptID(sessionID string, turnRunID int64, tool string, command string) string {
	sessionID = strings.TrimSpace(sessionID)
	tool = strings.TrimSpace(tool)
	commandHash := EffectAttemptCommandHash(command)
	seed := strings.Join([]string{sessionID, fmt.Sprintf("%d", turnRunID), tool, commandHash}, "\x00")
	sum := sha256.Sum256([]byte(seed))
	return "eff_" + hex.EncodeToString(sum[:16])
}

func EffectAttemptCommandHash(command string) string {
	normalized := strings.Join(strings.Fields(strings.TrimSpace(command)), " ")
	sum := sha256.Sum256([]byte(normalized))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func NormalizeEffectAttemptInput(input EffectAttemptInput) EffectAttemptInput {
	input.OperationID = strings.TrimSpace(input.OperationID)
	input.PhaseID = strings.TrimSpace(input.PhaseID)
	input.LeaseID = strings.TrimSpace(input.LeaseID)
	input.ProposalID = strings.TrimSpace(input.ProposalID)
	input.WorkMode = strings.TrimSpace(input.WorkMode)
	input.Executor = strings.TrimSpace(input.Executor)
	input.Tool = strings.TrimSpace(input.Tool)
	input.Command = strings.Join(strings.Fields(strings.TrimSpace(input.Command)), " ")
	input.EffectKind = strings.TrimSpace(input.EffectKind)
	input.EffectReason = strings.TrimSpace(input.EffectReason)
	input.BoundaryKind = strings.TrimSpace(input.BoundaryKind)
	input.AuthorizationReason = strings.TrimSpace(input.AuthorizationReason)
	input.ErrorText = strings.TrimSpace(input.ErrorText)
	input.EvidenceRefs = normalizeStringList(input.EvidenceRefs)
	input.SubjectJSON = normalizeEffectAttemptSubjectJSON(input.SubjectJSON)
	input.Status = NormalizeEffectAttemptStatus(input.Status)
	if input.UpdatedAt.IsZero() {
		input.UpdatedAt = time.Now().UTC()
	}
	if input.StartedAt.IsZero() {
		input.StartedAt = input.UpdatedAt
	}
	if input.CompletedAt.IsZero() && effectAttemptStatusCompleted(input.Status) {
		input.CompletedAt = input.UpdatedAt
	}
	sessionID := SessionIDForKey(input.Key)
	if strings.TrimSpace(input.AttemptID) == "" {
		input.AttemptID = EffectAttemptID(sessionID, input.TurnRunID, input.Tool, input.Command)
	}
	input.AttemptID = strings.TrimSpace(input.AttemptID)
	return input
}

func NormalizeEffectAttemptStatus(status EffectAttemptStatus) EffectAttemptStatus {
	switch EffectAttemptStatus(strings.TrimSpace(string(status))) {
	case "succeeded":
		return EffectAttemptStatusExecuted
	case EffectAttemptStatusExecuted,
		EffectAttemptStatusFailed,
		EffectAttemptStatusUncertain,
		EffectAttemptStatusVerified,
		EffectAttemptStatusRejected,
		EffectAttemptStatusSuperseded:
		return EffectAttemptStatus(strings.TrimSpace(string(status)))
	default:
		return EffectAttemptStatusAttempted
	}
}

func EffectAttemptStatusTerminal(status EffectAttemptStatus) bool {
	switch NormalizeEffectAttemptStatus(status) {
	case EffectAttemptStatusVerified, EffectAttemptStatusRejected, EffectAttemptStatusSuperseded:
		return true
	default:
		return false
	}
}

func EffectAttemptStatusRetryBlocking(status EffectAttemptStatus) bool {
	switch NormalizeEffectAttemptStatus(status) {
	case EffectAttemptStatusAttempted, EffectAttemptStatusExecuted, EffectAttemptStatusUncertain:
		return true
	default:
		return false
	}
}

func EffectAttemptHasSideEffects(attempt EffectAttempt) bool {
	kind := strings.ToLower(strings.TrimSpace(attempt.EffectKind))
	if kind == "" || kind == "read_only_inspection" {
		return false
	}
	return true
}

func effectAttemptStatusCompleted(status EffectAttemptStatus) bool {
	switch NormalizeEffectAttemptStatus(status) {
	case EffectAttemptStatusExecuted, EffectAttemptStatusFailed, EffectAttemptStatusUncertain, EffectAttemptStatusVerified, EffectAttemptStatusRejected, EffectAttemptStatusSuperseded:
		return true
	default:
		return false
	}
}

func normalizeEffectAttemptSubjectJSON(raw string) string {
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
