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
	PacketID         string
	TaskLeaseID      string
	AgentID          string
	SessionID        string
	ChatID           int64
	UserID           int64
	Scope            ScopeRef
	TaskKind         string
	Status           ChildTaskPacketStatus
	AuthorityKind    string
	AuthorityID      string
	GrantID          string
	RequestID        string
	TargetResource   string
	RequiredAction   string
	InputJSON        string
	ActiveAttemptID  string
	LeaseOwner       string
	LeaseGeneration  int64
	FencingToken     string
	LeaseExpiresAt   time.Time
	LeaseHeartbeatAt time.Time
	LeaseReleasedAt  time.Time
	ResultID         string
	CreatedAt        time.Time
	UpdatedAt        time.Time
	TerminalAt       time.Time
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

type ChildTaskAttemptClaimInput struct {
	PacketID       string
	AttemptID      string
	LeaseOwner     string
	AgentID        string
	Key            SessionKey
	ClaimedAt      time.Time
	LeaseExpiresAt time.Time
}

type ChildTaskAttemptHeartbeatInput struct {
	PacketID        string
	AttemptID       string
	LeaseOwner      string
	LeaseGeneration int64
	FencingToken    string
	HeartbeatAt     time.Time
	LeaseExpiresAt  time.Time
}

type ChildTaskAttemptReleaseInput struct {
	PacketID        string
	AttemptID       string
	LeaseOwner      string
	LeaseGeneration int64
	FencingToken    string
	ReleasedAt      time.Time
}

type ChildTaskResult struct {
	ResultID        string
	PacketID        string
	AttemptID       string
	LeaseOwner      string
	LeaseGeneration int64
	FencingToken    string
	TaskLeaseID     string
	AgentID         string
	SessionID       string
	Status          ChildTaskResultStatus
	ResultKind      string
	Summary         string
	BlockerKind     string
	ErrorText       string
	EvidenceRefs    []string
	NextState       NextActionState
	CreatedAt       time.Time
}

type ChildTaskResultInput struct {
	ResultID        string
	PacketID        string
	AttemptID       string
	LeaseOwner      string
	LeaseGeneration int64
	FencingToken    string
	TaskLeaseID     string
	AgentID         string
	Key             SessionKey
	Status          ChildTaskResultStatus
	ResultKind      string
	Summary         string
	BlockerKind     string
	ErrorText       string
	EvidenceRefs    []string
	NextState       NextActionState
	CreatedAt       time.Time
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

func NormalizeChildTaskAttemptClaimInput(input ChildTaskAttemptClaimInput) ChildTaskAttemptClaimInput {
	input.PacketID = strings.TrimSpace(input.PacketID)
	input.AttemptID = strings.TrimSpace(input.AttemptID)
	input.LeaseOwner = strings.TrimSpace(input.LeaseOwner)
	input.AgentID = strings.TrimSpace(input.AgentID)
	if input.ClaimedAt.IsZero() {
		input.ClaimedAt = time.Now().UTC()
	} else {
		input.ClaimedAt = input.ClaimedAt.UTC()
	}
	if input.AttemptID == "" && input.PacketID != "" {
		input.AttemptID = ChildTaskAttemptID(input.PacketID, input.ClaimedAt.Format(time.RFC3339Nano))
	}
	if input.LeaseOwner == "" {
		input.LeaseOwner = firstNonEmptyString(input.AgentID, input.AttemptID)
	}
	if input.LeaseExpiresAt.IsZero() {
		input.LeaseExpiresAt = input.ClaimedAt.Add(30 * time.Minute)
	} else {
		input.LeaseExpiresAt = input.LeaseExpiresAt.UTC()
	}
	return input
}

func NormalizeChildTaskAttemptHeartbeatInput(input ChildTaskAttemptHeartbeatInput) ChildTaskAttemptHeartbeatInput {
	input.PacketID = strings.TrimSpace(input.PacketID)
	input.AttemptID = strings.TrimSpace(input.AttemptID)
	input.LeaseOwner = strings.TrimSpace(input.LeaseOwner)
	input.FencingToken = strings.TrimSpace(input.FencingToken)
	if input.HeartbeatAt.IsZero() {
		input.HeartbeatAt = time.Now().UTC()
	} else {
		input.HeartbeatAt = input.HeartbeatAt.UTC()
	}
	if input.LeaseExpiresAt.IsZero() {
		input.LeaseExpiresAt = input.HeartbeatAt.Add(30 * time.Minute)
	} else {
		input.LeaseExpiresAt = input.LeaseExpiresAt.UTC()
	}
	return input
}

func NormalizeChildTaskAttemptReleaseInput(input ChildTaskAttemptReleaseInput) ChildTaskAttemptReleaseInput {
	input.PacketID = strings.TrimSpace(input.PacketID)
	input.AttemptID = strings.TrimSpace(input.AttemptID)
	input.LeaseOwner = strings.TrimSpace(input.LeaseOwner)
	input.FencingToken = strings.TrimSpace(input.FencingToken)
	if input.ReleasedAt.IsZero() {
		input.ReleasedAt = time.Now().UTC()
	} else {
		input.ReleasedAt = input.ReleasedAt.UTC()
	}
	return input
}

func NormalizeChildTaskResultInput(input ChildTaskResultInput) ChildTaskResultInput {
	input.ResultID = strings.TrimSpace(input.ResultID)
	input.PacketID = strings.TrimSpace(input.PacketID)
	input.AttemptID = strings.TrimSpace(input.AttemptID)
	input.LeaseOwner = strings.TrimSpace(input.LeaseOwner)
	input.FencingToken = strings.TrimSpace(input.FencingToken)
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

func ChildTaskFencingToken(packetID string, attemptID string, leaseGeneration int64) string {
	packetID = strings.TrimSpace(packetID)
	attemptID = strings.TrimSpace(attemptID)
	if packetID == "" || attemptID == "" || leaseGeneration <= 0 {
		return ""
	}
	seed := strings.Join([]string{packetID, attemptID, "generation", time.Unix(leaseGeneration, 0).UTC().Format(time.RFC3339Nano)}, "\x00")
	sum := sha256.Sum256([]byte(seed))
	return "child_fence:" + hex.EncodeToString(sum[:16])
}

func ChildTaskPacketStatusTerminal(status ChildTaskPacketStatus) bool {
	switch NormalizeChildTaskPacketStatus(status) {
	case ChildTaskPacketCompleted,
		ChildTaskPacketBlocked,
		ChildTaskPacketFailed,
		ChildTaskPacketRevoked,
		ChildTaskPacketExpired:
		return true
	default:
		return false
	}
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
