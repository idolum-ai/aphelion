//go:build linux

package session

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"time"
)

type NextActionState string

const (
	NextActionReadyToExecute             NextActionState = "ready_to_execute"
	NextActionBlockedNeedsAuthority      NextActionState = "blocked_needs_authority"
	NextActionBlockedNeedsResourceRepair NextActionState = "blocked_needs_resource_repair"
	NextActionNeedsVerification          NextActionState = "needs_verification"
	NextActionWaitingForChild            NextActionState = "waiting_for_child"
	NextActionWaitingForOperator         NextActionState = "waiting_for_operator"
	NextActionScheduledRetry             NextActionState = "scheduled_retry"
	NextActionExternalDependency         NextActionState = "external_dependency"
	NextActionSuperseded                 NextActionState = "superseded"
	NextActionCancelled                  NextActionState = "cancelled"
	NextActionTerminal                   NextActionState = "terminal"
)

type NextActionRecord struct {
	RecordID           string
	SessionID          string
	ChatID             int64
	UserID             int64
	Scope              ScopeRef
	TurnRunID          int64
	Owner              string
	State              NextActionState
	SubjectKind        string
	SubjectRef         string
	CausalRefs         []string
	NextAction         string
	RequiredAuthority  string
	ResourceBlocker    string
	Verifier           string
	RetryPolicy        string
	OperationKind      string
	OperationTool      string
	OperationInputJSON string
	OperatorProjection string
	CreatedAt          time.Time
	ResolvedAt         time.Time
}

type NextActionInput struct {
	RecordID           string
	Key                SessionKey
	TurnRunID          int64
	Owner              string
	State              NextActionState
	SubjectKind        string
	SubjectRef         string
	CausalRefs         []string
	NextAction         string
	RequiredAuthority  string
	ResourceBlocker    string
	Verifier           string
	RetryPolicy        string
	OperationKind      string
	OperationTool      string
	OperationInputJSON string
	OperatorProjection string
	CreatedAt          time.Time
}

type NextActionResolutionInput struct {
	RecordID    string
	Key         SessionKey
	Owner       string
	SubjectKind string
	SubjectRef  string
	Reason      string
	ResolvedAt  time.Time
}

func NormalizeNextActionInput(input NextActionInput) NextActionInput {
	input.RecordID = strings.TrimSpace(input.RecordID)
	input.Owner = strings.TrimSpace(input.Owner)
	input.State = NormalizeNextActionState(input.State)
	input.SubjectKind = normalizeEnumValue(input.SubjectKind)
	input.SubjectRef = strings.TrimSpace(input.SubjectRef)
	input.CausalRefs = normalizeStringList(input.CausalRefs)
	input.NextAction = strings.TrimSpace(input.NextAction)
	input.RequiredAuthority = strings.TrimSpace(input.RequiredAuthority)
	input.ResourceBlocker = strings.TrimSpace(input.ResourceBlocker)
	input.Verifier = strings.TrimSpace(input.Verifier)
	input.RetryPolicy = strings.TrimSpace(input.RetryPolicy)
	input.OperationKind = normalizeEnumValue(input.OperationKind)
	input.OperationTool = strings.TrimSpace(input.OperationTool)
	input.OperationInputJSON = strings.TrimSpace(input.OperationInputJSON)
	input.OperatorProjection = strings.TrimSpace(input.OperatorProjection)
	if input.Owner == "" {
		input.Owner = "runtime"
	}
	if input.SubjectKind == "" {
		input.SubjectKind = "workflow"
	}
	if input.SubjectRef == "" {
		input.SubjectRef = input.SubjectKind
	}
	if input.NextAction == "" {
		input.NextAction = defaultNextActionText(input.State)
	}
	if input.OperatorProjection == "" {
		input.OperatorProjection = input.NextAction
	}
	if input.CreatedAt.IsZero() {
		input.CreatedAt = time.Now().UTC()
	}
	if input.RecordID == "" {
		input.RecordID = NextActionRecordID(SessionIDForKey(input.Key), input.SubjectKind, input.SubjectRef, input.State, input.CreatedAt)
	}
	return input
}

func NormalizeNextActionResolutionInput(input NextActionResolutionInput) NextActionResolutionInput {
	input.RecordID = strings.TrimSpace(input.RecordID)
	input.Owner = strings.TrimSpace(input.Owner)
	input.SubjectKind = normalizeEnumValue(input.SubjectKind)
	input.SubjectRef = strings.TrimSpace(input.SubjectRef)
	input.Reason = normalizeEnumValue(input.Reason)
	if input.Owner == "" {
		input.Owner = "runtime"
	}
	if input.SubjectKind == "" {
		input.SubjectKind = "workflow"
	}
	if input.SubjectRef == "" {
		input.SubjectRef = input.SubjectKind
	}
	if input.Reason == "" {
		input.Reason = "resolved"
	}
	if input.ResolvedAt.IsZero() {
		input.ResolvedAt = time.Now().UTC()
	}
	return input
}

func NormalizeNextActionState(state NextActionState) NextActionState {
	switch NextActionState(normalizeEnumValue(string(state))) {
	case NextActionReadyToExecute,
		NextActionBlockedNeedsAuthority,
		NextActionBlockedNeedsResourceRepair,
		NextActionNeedsVerification,
		NextActionWaitingForChild,
		NextActionWaitingForOperator,
		NextActionScheduledRetry,
		NextActionExternalDependency,
		NextActionSuperseded,
		NextActionCancelled,
		NextActionTerminal:
		return NextActionState(normalizeEnumValue(string(state)))
	default:
		return NextActionWaitingForOperator
	}
}

func NextActionRecordID(sessionID string, subjectKind string, subjectRef string, state NextActionState, at time.Time) string {
	seed := strings.Join([]string{
		strings.TrimSpace(sessionID),
		normalizeEnumValue(subjectKind),
		strings.TrimSpace(subjectRef),
		string(NormalizeNextActionState(state)),
		at.UTC().Format(time.RFC3339Nano),
	}, "\x00")
	sum := sha256.Sum256([]byte(seed))
	return "next_" + hex.EncodeToString(sum[:16])
}

func defaultNextActionText(state NextActionState) string {
	switch NormalizeNextActionState(state) {
	case NextActionReadyToExecute:
		return "execute the approved bounded step"
	case NextActionBlockedNeedsAuthority:
		return "request the missing bounded authority"
	case NextActionBlockedNeedsResourceRepair:
		return "repair the resource boundary before retry"
	case NextActionNeedsVerification:
		return "run bounded verification before retry"
	case NextActionWaitingForChild:
		return "wait for child task result or bounded blocker"
	case NextActionWaitingForOperator:
		return "ask the operator for the next bounded decision"
	case NextActionScheduledRetry:
		return "wait for the scheduled retry window"
	case NextActionExternalDependency:
		return "wait for external dependency or surface a bounded retry"
	case NextActionSuperseded:
		return "retire this stale step and use the replacement"
	case NextActionCancelled:
		return "stop because the work was cancelled"
	case NextActionTerminal:
		return "stop; the workflow is terminal"
	default:
		return "surface a bounded next step"
	}
}
