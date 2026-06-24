//go:build linux

package session

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
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
	InputFingerprint string
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
	PacketID         string
	TaskLeaseID      string
	AgentID          string
	Key              SessionKey
	TaskKind         string
	Status           ChildTaskPacketStatus
	AuthorityKind    string
	AuthorityID      string
	GrantID          string
	RequestID        string
	TargetResource   string
	RequiredAction   string
	InputJSON        string
	InputFingerprint string
	CreatedAt        time.Time
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
	ResultID             string
	PacketID             string
	AttemptID            string
	LeaseOwner           string
	LeaseGeneration      int64
	FencingToken         string
	TaskLeaseID          string
	AgentID              string
	SessionID            string
	Status               ChildTaskResultStatus
	ResultKind           string
	Summary              string
	BlockerKind          string
	ErrorText            string
	EvidenceRefs         []string
	NextState            NextActionState
	ResultFingerprint    string
	IntentSetFingerprint string
	CreatedAt            time.Time
}

type ChildTaskResultInput struct {
	ResultID             string
	PacketID             string
	AttemptID            string
	LeaseOwner           string
	LeaseGeneration      int64
	FencingToken         string
	TaskLeaseID          string
	AgentID              string
	Key                  SessionKey
	Status               ChildTaskResultStatus
	ResultKind           string
	Summary              string
	BlockerKind          string
	ErrorText            string
	EvidenceRefs         []string
	NextState            NextActionState
	ResultFingerprint    string
	IntentSetFingerprint string
	CreatedAt            time.Time
}

type ChildTaskOutcomeIntentKind string

const (
	ChildTaskOutcomeIntentParentConversationAck ChildTaskOutcomeIntentKind = "parent_conversation_ack"
	ChildTaskOutcomeIntentScheduledReview       ChildTaskOutcomeIntentKind = "scheduled_review"
	ChildTaskOutcomeIntentGenericFinalize       ChildTaskOutcomeIntentKind = "generic_finalize"
	ChildTaskOutcomeIntentChildBlockerReview    ChildTaskOutcomeIntentKind = "child_blocker_review"
	ChildTaskOutcomeIntentPolicyApplied         ChildTaskOutcomeIntentKind = "policy_applied"
	ChildTaskOutcomeIntentPolicyApplyFailed     ChildTaskOutcomeIntentKind = "policy_apply_failed"
)

type ChildTaskOutcomeIntentStatus string

const (
	ChildTaskOutcomeIntentPending    ChildTaskOutcomeIntentStatus = "pending"
	ChildTaskOutcomeIntentApplying   ChildTaskOutcomeIntentStatus = "applying"
	ChildTaskOutcomeIntentRetryable  ChildTaskOutcomeIntentStatus = "retryable"
	ChildTaskOutcomeIntentApplied    ChildTaskOutcomeIntentStatus = "applied"
	ChildTaskOutcomeIntentDeadLetter ChildTaskOutcomeIntentStatus = "dead_letter"
	ChildTaskOutcomeIntentFailed     ChildTaskOutcomeIntentStatus = "failed"
)

type ChildTaskOutcomeIntent struct {
	IntentID        string
	PacketID        string
	ResultID        string
	AttemptID       string
	Kind            ChildTaskOutcomeIntentKind
	Status          ChildTaskOutcomeIntentStatus
	Sequence        int
	PayloadJSON     string
	ResultRef       string
	IdempotencyKey  string
	LeaseOwner      string
	LeaseGeneration int64
	FencingToken    string
	LeaseExpiresAt  time.Time
	NextAttemptAt   time.Time
	DeadLetterAt    time.Time
	Attempts        int
	LastError       string
	CreatedAt       time.Time
	UpdatedAt       time.Time
	AppliedAt       time.Time
}

type ChildTaskOutcomeIntentInput struct {
	IntentID       string
	PacketID       string
	ResultID       string
	AttemptID      string
	Kind           ChildTaskOutcomeIntentKind
	Sequence       int
	PayloadJSON    string
	ResultRef      string
	IdempotencyKey string
	CreatedAt      time.Time
}

type ChildTaskOutcomeIntentClaimInput struct {
	IntentID       string
	LeaseOwner     string
	ClaimedAt      time.Time
	LeaseExpiresAt time.Time
}

type ChildTaskOutcomeIntentCompletionInput struct {
	IntentID        string
	LeaseOwner      string
	LeaseGeneration int64
	FencingToken    string
	CompletedAt     time.Time
}

type ChildTaskOutcomeIntentRetryInput struct {
	IntentID        string
	LeaseOwner      string
	LeaseGeneration int64
	FencingToken    string
	LastError       string
	AttemptedAt     time.Time
	NextAttemptAt   time.Time
	DeadLetter      bool
}

type ChildTaskOutcomeCommitInput struct {
	Result         ChildTaskResultInput
	NextAction     *NextActionInput
	OutcomeIntents []ChildTaskOutcomeIntentInput
	ResolvedAt     time.Time
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
	input.InputFingerprint = strings.TrimSpace(input.InputFingerprint)
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
	if input.InputFingerprint == "" {
		input.InputFingerprint = ChildTaskPacketInputFingerprint(input)
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
	input.ResultFingerprint = strings.TrimSpace(input.ResultFingerprint)
	input.IntentSetFingerprint = strings.TrimSpace(input.IntentSetFingerprint)
	return input
}

func NormalizeChildTaskOutcomeIntentInput(input ChildTaskOutcomeIntentInput) ChildTaskOutcomeIntentInput {
	input.IntentID = strings.TrimSpace(input.IntentID)
	input.PacketID = strings.TrimSpace(input.PacketID)
	input.ResultID = strings.TrimSpace(input.ResultID)
	input.AttemptID = strings.TrimSpace(input.AttemptID)
	input.Kind = ChildTaskOutcomeIntentKind(normalizeEnumValue(string(input.Kind)))
	input.PayloadJSON = strings.TrimSpace(input.PayloadJSON)
	input.ResultRef = strings.TrimSpace(input.ResultRef)
	input.IdempotencyKey = strings.TrimSpace(input.IdempotencyKey)
	if input.PayloadJSON == "" {
		input.PayloadJSON = "{}"
	}
	if input.Sequence <= 0 {
		input.Sequence = 100
	}
	if input.IdempotencyKey == "" {
		input.IdempotencyKey = input.IntentID
	}
	if input.CreatedAt.IsZero() {
		input.CreatedAt = time.Now().UTC()
	} else {
		input.CreatedAt = input.CreatedAt.UTC()
	}
	if input.IntentID == "" {
		input.IntentID = ChildTaskOutcomeIntentID(input.PacketID, input.ResultID, input.Kind)
	}
	if input.IdempotencyKey == "" {
		input.IdempotencyKey = input.IntentID
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

func ChildTaskResultFingerprint(input ChildTaskResultInput) string {
	normalized := NormalizeChildTaskResultInput(input)
	parts := []string{
		normalized.ResultID,
		normalized.PacketID,
		normalized.AttemptID,
		normalized.LeaseOwner,
		time.Unix(normalized.LeaseGeneration, 0).UTC().Format(time.RFC3339Nano),
		normalized.FencingToken,
		normalized.TaskLeaseID,
		normalized.AgentID,
		SessionIDForKey(normalized.Key),
		string(normalized.Status),
		normalized.ResultKind,
		normalized.Summary,
		normalized.BlockerKind,
		normalized.ErrorText,
		strings.Join(normalized.EvidenceRefs, "\x1f"),
		string(normalized.NextState),
	}
	sum := sha256.Sum256([]byte("child_task_result\x00" + strings.Join(parts, "\x00")))
	return "sha256:" + hex.EncodeToString(sum[:])
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

func ChildTaskOutcomeIntentFencingToken(intentID string, leaseOwner string, leaseGeneration int64) string {
	intentID = strings.TrimSpace(intentID)
	leaseOwner = strings.TrimSpace(leaseOwner)
	if intentID == "" || leaseOwner == "" || leaseGeneration <= 0 {
		return ""
	}
	seed := strings.Join([]string{intentID, leaseOwner, "generation", time.Unix(leaseGeneration, 0).UTC().Format(time.RFC3339Nano)}, "\x00")
	sum := sha256.Sum256([]byte(seed))
	return "child_intent_fence:" + hex.EncodeToString(sum[:16])
}

func ChildTaskPacketInputFingerprint(input ChildTaskPacketInput) string {
	normalized := input
	normalized.PacketID = strings.TrimSpace(normalized.PacketID)
	normalized.TaskLeaseID = strings.TrimSpace(normalized.TaskLeaseID)
	normalized.AgentID = strings.TrimSpace(normalized.AgentID)
	normalized.TaskKind = normalizeEnumValue(normalized.TaskKind)
	normalized.AuthorityKind = normalizeEnumValue(normalized.AuthorityKind)
	normalized.AuthorityID = strings.TrimSpace(normalized.AuthorityID)
	normalized.GrantID = strings.TrimSpace(normalized.GrantID)
	normalized.RequestID = strings.TrimSpace(normalized.RequestID)
	normalized.TargetResource = strings.TrimSpace(normalized.TargetResource)
	normalized.RequiredAction = normalizeEnumValue(normalized.RequiredAction)
	normalized.InputJSON = strings.TrimSpace(normalized.InputJSON)
	if normalized.InputJSON == "" {
		normalized.InputJSON = "{}"
	}
	parts := []string{
		normalized.PacketID,
		normalized.TaskLeaseID,
		normalized.AgentID,
		SessionIDForKey(normalized.Key),
		string(defaultScopeForKey(normalized.Key).Kind),
		defaultScopeForKey(normalized.Key).ID,
		defaultScopeForKey(normalized.Key).DurableAgentID,
		normalized.TaskKind,
		normalized.AuthorityKind,
		normalized.AuthorityID,
		normalized.GrantID,
		normalized.RequestID,
		normalized.TargetResource,
		normalized.RequiredAction,
		normalized.InputJSON,
	}
	sum := sha256.Sum256([]byte("child_task_packet_input\x00" + strings.Join(parts, "\x00")))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func ChildTaskOutcomeIntentID(packetID string, resultID string, kind ChildTaskOutcomeIntentKind) string {
	packetID = strings.TrimSpace(packetID)
	resultID = strings.TrimSpace(resultID)
	kind = ChildTaskOutcomeIntentKind(normalizeEnumValue(string(kind)))
	if packetID == "" || resultID == "" || kind == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(strings.Join([]string{"child_task_outcome_intent", packetID, resultID, string(kind)}, "\x00")))
	return "child_intent:" + hex.EncodeToString(sum[:8])
}

func ChildTaskOutcomeIntentSetFingerprint(intents []ChildTaskOutcomeIntentInput) string {
	if len(intents) == 0 {
		sum := sha256.Sum256([]byte("child_task_outcome_intents\x00"))
		return "sha256:" + hex.EncodeToString(sum[:])
	}
	parts := make([]string, 0, len(intents))
	for _, intent := range intents {
		intent = NormalizeChildTaskOutcomeIntentInput(intent)
		parts = append(parts, strings.Join([]string{
			intent.IntentID,
			intent.PacketID,
			intent.ResultID,
			intent.AttemptID,
			string(intent.Kind),
			time.Unix(int64(intent.Sequence), 0).UTC().Format(time.RFC3339Nano),
			intent.PayloadJSON,
			intent.ResultRef,
			intent.IdempotencyKey,
		}, "\x1f"))
	}
	sort.Strings(parts)
	sum := sha256.Sum256([]byte("child_task_outcome_intents\x00" + strings.Join(parts, "\x00")))
	return "sha256:" + hex.EncodeToString(sum[:])
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
