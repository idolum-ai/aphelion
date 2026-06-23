//go:build linux

package session

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"time"
)

type ChildTaskPacketStatus string

const (
	ChildTaskPacketQueued     ChildTaskPacketStatus = "queued"
	ChildTaskPacketInProgress ChildTaskPacketStatus = "in_progress"
	ChildTaskPacketCompleted  ChildTaskPacketStatus = "completed"
	ChildTaskPacketBlocked    ChildTaskPacketStatus = "blocked"
	ChildTaskPacketFailed     ChildTaskPacketStatus = "failed"
	ChildTaskPacketRevoked    ChildTaskPacketStatus = "revoked"
	ChildTaskPacketExpired    ChildTaskPacketStatus = "expired"
)

type ChildTaskResultStatus string

const (
	ChildTaskResultCompleted ChildTaskResultStatus = "completed"
	ChildTaskResultBlocked   ChildTaskResultStatus = "blocked"
	ChildTaskResultFailed    ChildTaskResultStatus = "failed"
	ChildTaskResultUpdate    ChildTaskResultStatus = "update"
)

type ChildTaskPacket struct {
	PacketID       string
	TaskLeaseID    string
	AgentID        string
	SessionID      string
	ChatID         int64
	UserID         int64
	Scope          ScopeRef
	TaskKind       string
	Status         ChildTaskPacketStatus
	AuthorityKind  string
	AuthorityID    string
	GrantID        string
	RequestID      string
	TargetResource string
	RequiredAction string
	InputJSON      string
	ResultID       string
	CreatedAt      time.Time
	UpdatedAt      time.Time
	TerminalAt     time.Time
}

type ChildTaskPacketInput struct {
	PacketID       string
	TaskLeaseID    string
	AgentID        string
	Key            SessionKey
	TaskKind       string
	Status         ChildTaskPacketStatus
	AuthorityKind  string
	AuthorityID    string
	GrantID        string
	RequestID      string
	TargetResource string
	RequiredAction string
	InputJSON      string
	CreatedAt      time.Time
}

type ChildTaskResult struct {
	ResultID     string
	PacketID     string
	AttemptID    string
	TaskLeaseID  string
	AgentID      string
	SessionID    string
	Status       ChildTaskResultStatus
	ResultKind   string
	Summary      string
	BlockerKind  string
	ErrorText    string
	EvidenceRefs []string
	NextState    NextActionState
	CreatedAt    time.Time
}

type ChildTaskResultInput struct {
	ResultID     string
	PacketID     string
	AttemptID    string
	TaskLeaseID  string
	AgentID      string
	Key          SessionKey
	Status       ChildTaskResultStatus
	ResultKind   string
	Summary      string
	BlockerKind  string
	ErrorText    string
	EvidenceRefs []string
	NextState    NextActionState
	CreatedAt    time.Time
}

func NormalizeChildTaskPacketInput(input ChildTaskPacketInput) ChildTaskPacketInput {
	input.PacketID = strings.TrimSpace(input.PacketID)
	input.TaskLeaseID = strings.TrimSpace(input.TaskLeaseID)
	input.AgentID = strings.TrimSpace(input.AgentID)
	input.TaskKind = normalizeEnumValue(input.TaskKind)
	input.Status = NormalizeChildTaskPacketStatus(input.Status)
	input.AuthorityKind = normalizeEnumValue(input.AuthorityKind)
	input.AuthorityID = strings.TrimSpace(input.AuthorityID)
	input.GrantID = strings.TrimSpace(input.GrantID)
	input.RequestID = strings.TrimSpace(input.RequestID)
	input.TargetResource = strings.TrimSpace(input.TargetResource)
	input.RequiredAction = normalizeEnumValue(input.RequiredAction)
	input.InputJSON = strings.TrimSpace(input.InputJSON)
	if input.TaskLeaseID == "" && input.PacketID != "" {
		input.TaskLeaseID = ChildTaskLeaseID(input.PacketID)
	}
	if input.TaskKind == "" {
		input.TaskKind = "durable_child_task"
	}
	if input.Status == "" {
		input.Status = ChildTaskPacketQueued
	}
	if input.CreatedAt.IsZero() {
		input.CreatedAt = time.Now().UTC()
	} else {
		input.CreatedAt = input.CreatedAt.UTC()
	}
	return input
}

func NormalizeChildTaskResultInput(input ChildTaskResultInput) ChildTaskResultInput {
	input.ResultID = strings.TrimSpace(input.ResultID)
	input.PacketID = strings.TrimSpace(input.PacketID)
	input.AttemptID = strings.TrimSpace(input.AttemptID)
	input.TaskLeaseID = strings.TrimSpace(input.TaskLeaseID)
	input.AgentID = strings.TrimSpace(input.AgentID)
	input.Status = NormalizeChildTaskResultStatus(input.Status)
	input.ResultKind = normalizeEnumValue(input.ResultKind)
	input.Summary = strings.TrimSpace(input.Summary)
	input.BlockerKind = normalizeEnumValue(input.BlockerKind)
	input.ErrorText = strings.TrimSpace(input.ErrorText)
	input.EvidenceRefs = normalizeStringList(input.EvidenceRefs)
	nextStateProvided := strings.TrimSpace(string(input.NextState)) != ""
	if nextStateProvided {
		input.NextState = NormalizeNextActionState(input.NextState)
	}
	if input.CreatedAt.IsZero() {
		input.CreatedAt = time.Now().UTC()
	} else {
		input.CreatedAt = input.CreatedAt.UTC()
	}
	if input.TaskLeaseID == "" && input.PacketID != "" {
		input.TaskLeaseID = ChildTaskLeaseID(input.PacketID)
	}
	if input.AttemptID == "" && input.PacketID != "" {
		input.AttemptID = ChildTaskAttemptID(input.PacketID, input.CreatedAt.Format(time.RFC3339Nano))
	}
	if input.ResultID == "" && input.AgentID != "" && input.PacketID != "" {
		input.ResultID = ChildTaskResultID(input.AgentID, input.PacketID, input.AttemptID)
	}
	if input.Status == "" {
		input.Status = ChildTaskResultBlocked
	}
	if input.ResultKind == "" {
		switch input.Status {
		case ChildTaskResultCompleted:
			input.ResultKind = "completion"
		case ChildTaskResultBlocked:
			input.ResultKind = "blocker"
		case ChildTaskResultUpdate:
			input.ResultKind = "update"
		default:
			input.ResultKind = "result"
		}
	}
	if !nextStateProvided {
		input.NextState = childTaskNextStateForResult(input.Status)
	}
	return input
}

func NormalizeChildTaskPacketStatus(status ChildTaskPacketStatus) ChildTaskPacketStatus {
	switch ChildTaskPacketStatus(normalizeEnumValue(string(status))) {
	case ChildTaskPacketQueued,
		ChildTaskPacketInProgress,
		ChildTaskPacketCompleted,
		ChildTaskPacketBlocked,
		ChildTaskPacketFailed,
		ChildTaskPacketRevoked,
		ChildTaskPacketExpired:
		return ChildTaskPacketStatus(normalizeEnumValue(string(status)))
	default:
		return ""
	}
}

func NormalizeChildTaskResultStatus(status ChildTaskResultStatus) ChildTaskResultStatus {
	switch ChildTaskResultStatus(normalizeEnumValue(string(status))) {
	case ChildTaskResultCompleted,
		ChildTaskResultBlocked,
		ChildTaskResultFailed,
		ChildTaskResultUpdate:
		return ChildTaskResultStatus(normalizeEnumValue(string(status)))
	default:
		return ""
	}
}

func ChildTaskLeaseID(packetID string) string {
	packetID = strings.TrimSpace(packetID)
	if packetID == "" {
		return ""
	}
	sum := sha256.Sum256([]byte("child_task_lease\x00" + packetID))
	return "child_task_lease:" + hex.EncodeToString(sum[:8])
}

func ChildTaskAttemptID(packetID string, attemptSeed string) string {
	packetID = strings.TrimSpace(packetID)
	attemptSeed = strings.TrimSpace(attemptSeed)
	if packetID == "" {
		return ""
	}
	sum := sha256.Sum256([]byte("child_task_attempt\x00" + packetID + "\x00" + attemptSeed))
	return "child_attempt:" + hex.EncodeToString(sum[:8])
}

func ChildTaskResultID(agentID string, packetID string, attemptID string) string {
	seed := strings.Join([]string{strings.TrimSpace(agentID), strings.TrimSpace(packetID), strings.TrimSpace(attemptID), "result"}, ":")
	sum := sha256.Sum256([]byte(seed))
	return "child_result:" + hex.EncodeToString(sum[:8])
}

func childTaskPacketStatusForResult(status ChildTaskResultStatus) ChildTaskPacketStatus {
	switch NormalizeChildTaskResultStatus(status) {
	case ChildTaskResultCompleted:
		return ChildTaskPacketCompleted
	case ChildTaskResultFailed:
		return ChildTaskPacketFailed
	case ChildTaskResultUpdate:
		return ChildTaskPacketInProgress
	default:
		return ChildTaskPacketBlocked
	}
}

func childTaskNextStateForResult(status ChildTaskResultStatus) NextActionState {
	switch NormalizeChildTaskResultStatus(status) {
	case ChildTaskResultCompleted:
		return NextActionTerminal
	case ChildTaskResultFailed:
		return NextActionBlockedNeedsResourceRepair
	case ChildTaskResultUpdate:
		return NextActionWaitingForChild
	default:
		return NextActionBlockedNeedsAuthority
	}
}
