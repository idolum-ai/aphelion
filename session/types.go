//go:build linux

package session

import (
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
)

type TurnRunKind string

const (
	TurnRunKindInteractive TurnRunKind = "interactive"
	TurnRunKindHeartbeat   TurnRunKind = "heartbeat"
	TurnRunKindCron        TurnRunKind = "cron"
	TurnRunKindRecovery    TurnRunKind = "recovery"
)

type TurnRunStatus string

const (
	TurnRunStatusRunning     TurnRunStatus = "running"
	TurnRunStatusCompleted   TurnRunStatus = "completed"
	TurnRunStatusFailed      TurnRunStatus = "failed"
	TurnRunStatusInterrupted TurnRunStatus = "interrupted"
)

type ScopeKind string

const (
	ScopeKindTelegramDM    ScopeKind = "telegram_dm"
	ScopeKindTelegramGroup ScopeKind = "telegram_group"
	ScopeKindHeartbeat     ScopeKind = "heartbeat"
	ScopeKindCron          ScopeKind = "cron"
	ScopeKindRecovery      ScopeKind = "recovery"
	ScopeKindDurableAgent  ScopeKind = "durable_agent"
)

type ScopeRef struct {
	Kind            ScopeKind
	ID              string
	DurableAgentID  string
	ParentScopeKind ScopeKind
	ParentScopeID   string
}

type PlanStatus string

const (
	PlanStatusPending    PlanStatus = "pending"
	PlanStatusInProgress PlanStatus = "in_progress"
	PlanStatusCompleted  PlanStatus = "completed"
)

type PlanStep struct {
	Step   string     `json:"step"`
	Status PlanStatus `json:"status"`
}

type PlanState struct {
	Explanation string     `json:"explanation,omitempty"`
	Steps       []PlanStep `json:"steps,omitempty"`
	UpdatedAt   time.Time  `json:"updated_at,omitempty"`
}

type PlanEventKind string

const (
	PlanEventKindToolUpdated   PlanEventKind = "tool_updated"
	PlanEventKindBrokerageSeed PlanEventKind = "brokerage_seed"
	PlanEventKindRehydrated    PlanEventKind = "rehydrated"
)

type PlanEvent struct {
	ID        int64
	SessionID string
	Kind      PlanEventKind
	PlanState PlanState
	CreatedAt time.Time
}

type OperationStatus string

const (
	OperationStatusIdle      OperationStatus = "idle"
	OperationStatusActive    OperationStatus = "active"
	OperationStatusBlocked   OperationStatus = "blocked"
	OperationStatusCompleted OperationStatus = "completed"
	OperationStatusFailed    OperationStatus = "failed"
)

type ProposalStatus string

const (
	ProposalStatusPending    ProposalStatus = "pending"
	ProposalStatusApproved   ProposalStatus = "approved"
	ProposalStatusDenied     ProposalStatus = "denied"
	ProposalStatusExpired    ProposalStatus = "expired"
	ProposalStatusSuperseded ProposalStatus = "superseded"
)

type FindingConfidence string

const (
	FindingConfidenceLow    FindingConfidence = "low"
	FindingConfidenceMedium FindingConfidence = "medium"
	FindingConfidenceHigh   FindingConfidence = "high"
)

type OperationProposal struct {
	ID            string         `json:"id,omitempty"`
	Kind          string         `json:"kind,omitempty"`
	Summary       string         `json:"summary,omitempty"`
	WhyNow        string         `json:"why_now,omitempty"`
	BoundedEffect string         `json:"bounded_effect,omitempty"`
	Status        ProposalStatus `json:"status,omitempty"`
	UpdatedAt     time.Time      `json:"updated_at,omitempty"`
}

type OperationFinding struct {
	Claim      string            `json:"claim"`
	Confidence FindingConfidence `json:"confidence,omitempty"`
	Basis      string            `json:"basis,omitempty"`
}

type OperationArtifact struct {
	Label string `json:"label,omitempty"`
	Ref   string `json:"ref"`
}

type OperationState struct {
	ID        string              `json:"id,omitempty"`
	Objective string              `json:"objective,omitempty"`
	Status    OperationStatus     `json:"status,omitempty"`
	Stage     string              `json:"stage,omitempty"`
	Summary   string              `json:"summary,omitempty"`
	Proposal  OperationProposal   `json:"proposal,omitempty"`
	Findings  []OperationFinding  `json:"findings,omitempty"`
	Artifacts []OperationArtifact `json:"artifacts,omitempty"`
	UpdatedAt time.Time           `json:"updated_at,omitempty"`
}

type ToolProposalReviewStatus string

const (
	ToolProposalReviewStatusProposed ToolProposalReviewStatus = "proposed"
	ToolProposalReviewStatusApproved ToolProposalReviewStatus = "approved"
	ToolProposalReviewStatusRejected ToolProposalReviewStatus = "rejected"
)

type ToolProposal struct {
	ProposalID       string                   `json:"proposal_id"`
	ProposedBy       string                   `json:"proposed_by,omitempty"`
	ToolName         string                   `json:"tool_name"`
	WhyNow           string                   `json:"why_now,omitempty"`
	Contract         string                   `json:"contract,omitempty"`
	ReviewStatus     ToolProposalReviewStatus `json:"review_status,omitempty"`
	RegisteredToolID string                   `json:"registered_tool_id,omitempty"`
	CreatedAt        time.Time                `json:"created_at,omitempty"`
	UpdatedAt        time.Time                `json:"updated_at,omitempty"`
}

type RegisteredTool struct {
	ToolName          string    `json:"tool_name"`
	ImplementationRef string    `json:"implementation_ref,omitempty"`
	Registered        bool      `json:"registered"`
	CreatedAt         time.Time `json:"created_at,omitempty"`
	UpdatedAt         time.Time `json:"updated_at,omitempty"`
}

type ToolExposure struct {
	ToolName  string    `json:"tool_name"`
	Principal string    `json:"principal"`
	Active    bool      `json:"active"`
	CreatedAt time.Time `json:"created_at,omitempty"`
	UpdatedAt time.Time `json:"updated_at,omitempty"`
}

type TurnAuthorizationKind string

const (
	TurnAuthorizationKindContinuation TurnAuthorizationKind = "continuation"
)

type TurnAuthorizationStatus string

const (
	TurnAuthorizationStatusIdle     TurnAuthorizationStatus = "idle"
	TurnAuthorizationStatusPending  TurnAuthorizationStatus = "pending"
	TurnAuthorizationStatusApproved TurnAuthorizationStatus = "approved"
	TurnAuthorizationStatusRevoked  TurnAuthorizationStatus = "revoked"
)

type ContinuationIntentDecision string

const (
	ContinuationIntentDecisionContinue ContinuationIntentDecision = "continue"
	ContinuationIntentDecisionStop     ContinuationIntentDecision = "stop"
	ContinuationIntentDecisionHold     ContinuationIntentDecision = "hold"
)

type ContinuationIntent struct {
	Decision    ContinuationIntentDecision `json:"decision,omitempty"`
	Rationale   string                     `json:"rationale,omitempty"`
	NextStep    string                     `json:"next_step,omitempty"`
	Constraints string                     `json:"constraints,omitempty"`
	Confidence  string                     `json:"confidence,omitempty"`
	Ratified    bool                       `json:"ratified,omitempty"`
	UpdatedAt   time.Time                  `json:"updated_at,omitempty"`
}

type TurnAuthorizationState struct {
	Kind                   TurnAuthorizationKind   `json:"kind,omitempty"`
	Status                 TurnAuthorizationStatus `json:"status,omitempty"`
	DecisionID             string                  `json:"decision_id,omitempty"`
	Objective              string                  `json:"objective,omitempty"`
	StageSummary           string                  `json:"stage_summary,omitempty"`
	RemainingTurns         int                     `json:"remaining_turns,omitempty"`
	ApprovedBy             int64                   `json:"approved_by,omitempty"`
	PersonaIntent          ContinuationIntent      `json:"persona_intent,omitempty"`
	GovernorIntent         ContinuationIntent      `json:"governor_intent,omitempty"`
	HandshakeBlockedReason string                  `json:"handshake_blocked_reason,omitempty"`
	UpdatedAt              time.Time               `json:"updated_at,omitempty"`
}

type ContinuationStatus = TurnAuthorizationStatus

const (
	ContinuationStatusIdle     = TurnAuthorizationStatusIdle
	ContinuationStatusPending  = TurnAuthorizationStatusPending
	ContinuationStatusApproved = TurnAuthorizationStatusApproved
	ContinuationStatusRevoked  = TurnAuthorizationStatusRevoked
)

type ContinuationState = TurnAuthorizationState

// SessionKey identifies a unique session.
type SessionKey struct {
	ChatID int64
	UserID int64 // 0 for shared group sessions or DMs
	Scope  ScopeRef
}

// Session stores conversation state, cache metadata, and accounting.
type Session struct {
	SessionID    string
	ChatID       int64
	UserID       int64 // 0 for shared group sessions
	Scope        ScopeRef
	Messages     []Message
	SystemPrompt string
	// LastFloorText stores the governor-owned floor text sidecar for audit.
	// The visible transcript in Messages stores the delivered scene text.
	LastFloorText     string
	LastFloorMetadata string
	CreatedAt         time.Time
	UpdatedAt         time.Time
	TurnCount         int

	// Cache tracking
	CacheState CacheState

	// Compaction
	CompactionLog []CompactionEntry

	// Planning
	PlanState PlanState

	// Operations
	OperationState OperationState

	// Continuation approval state
	ContinuationState ContinuationState

	// Token accounting (cumulative across all turns)
	TotalInputTokens  int64
	TotalOutputTokens int64
	TotalCacheRead    int64
	TotalCacheWrite   int64

	// Provider state
	LastProvider string
	LastModel    string

	// Agent state
	ActiveToolCalls int
	LastError       string

	// Chat metadata
	ChatType  string // "dm" or "group"
	ChatTitle string
	UserName  string
}

// CacheState tracks prompt cache behavior over time.
type CacheState struct {
	LastWriteBlock    int
	BlocksSinceWrite  int
	LastWriteTime     time.Time
	HitRate           float64
	ConsecutiveMisses int
}

// CompactionEntry records a single compaction event.
type CompactionEntry struct {
	Timestamp    time.Time
	TurnsBefore  int
	TurnsAfter   int
	TokensBefore int
	TokensAfter  int
	Summary      string
	Strategy     string // "summarize" or "truncate"
}

// ReviewEvent is a bounded digest from a source session to the admin DM.
type ReviewEvent struct {
	ID                int64
	SourceSessionID   string
	SourceChatID      int64
	SourceUserID      int64
	SourceRole        string
	SourceScope       ScopeRef
	TargetSessionID   string
	TargetAdminChatID int64
	TargetScope       ScopeRef
	TurnFrom          int
	TurnTo            int
	Summary           string
	MetadataJSON      string
	Status            string // "pending" | "delivered" | "dismissed"
	CreatedAt         time.Time
	DeliveredAt       time.Time
}

type DurableAgentPolicyUpdate struct {
	ID                  int64
	AgentID             string
	SourceReviewEventID int64
	PreviousVersion     int64
	NewVersion          int64
	PolicyHash          string
	PolicyJSON          string
	Reason              string
	AppliedAt           time.Time
}

type DurableAgentBootstrapUpdate struct {
	ID                  int64
	AgentID             string
	SourceReviewEventID int64
	ActorUserID         int64
	ActorRole           string
	UpdateKind          string
	PreviousBootstrap   core.NodeLLMBootstrap
	NewBootstrap        core.NodeLLMBootstrap
	Reason              string
	AppliedAt           time.Time
}

// TurnRun stores machine-authored facts about a turn lifecycle for recovery.
type TurnRun struct {
	ID                    int64
	SessionID             string
	ChatID                int64
	UserID                int64
	Scope                 ScopeRef
	Kind                  TurnRunKind
	Status                TurnRunStatus
	RequestText           string
	StartedAt             time.Time
	CompletedAt           time.Time
	LastActivityAt        time.Time
	LastToolName          string
	LastToolPreview       string
	ToolCallsStarted      int
	ToolCallsFinished     int
	LastToolResultPreview string
	LastToolError         string
	ProgressMessageID     int64
	ErrorText             string
	RecoverySummary       string
	RecoveryLoggedAt      time.Time
}

// ExecutionEvent stores one append-only event in the transparent execution sequence.
type ExecutionEvent struct {
	ID          int64
	SessionID   string
	ChatID      int64
	UserID      int64
	Scope       ScopeRef
	Seq         int64
	EventType   string
	Stage       string
	Status      string
	CausedBySeq int64
	PayloadJSON string
	CreatedAt   time.Time
}

// ExecutionEventInput is the write input for append-only execution events.
type ExecutionEventInput struct {
	EventType   string
	Stage       string
	Status      string
	CausedBySeq int64
	PayloadJSON string
	CreatedAt   time.Time
}

// PendingDecisionRecord persists broker decisions that are awaiting callback resolution.
type PendingDecisionRecord struct {
	ID                string
	Sequence          uint64
	OwnerKey          string
	Kind              string
	ChatID            int64
	SenderID          int64
	MessageID         int64
	Prompt            string
	Details           string
	ChoicesJSON       string
	DefaultChoice     string
	TimeoutNanos      int64
	DeliveryMessageID int64
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

// PendingArtifactRetentionRecord persists inbound artifact context while a
// retention decision is outstanding so routing can resume asynchronously.
type PendingArtifactRetentionRecord struct {
	OwnerKey           string
	ChatID             int64
	SenderID           int64
	InboundMessageJSON string
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

// PendingBusyDecisionRecord persists inbound message context while a busy
// stop/queue decision is outstanding so routing can resume asynchronously.
type PendingBusyDecisionRecord struct {
	OwnerKey           string
	ChatID             int64
	SenderID           int64
	InboundMessageJSON string
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

type ContinuationStateRecord struct {
	Key       SessionKey
	State     ContinuationState
	UpdatedAt time.Time
}

type SessionStatusState struct {
	PlanState           PlanState
	OperationState      OperationState
	LastFloorMetadata   string
	TurnCount           int
	OutboundCountAtTurn int
}

// Message is one persisted conversation message.
type Message struct {
	ID                int64
	SessionID         string
	ChatID            int64
	UserID            int64
	ActorUserID       int64
	ActorRole         string
	EventOrigin       string
	EventOriginDetail string
	Role              string
	Content           string
	FloorContent      string
	FloorMetadata     string
	ToolCalls         string
	ToolID            string
	ToolName          string
	Thinking          string
	CreatedAt         time.Time
	TurnIndex         int
	ContentChars      int
	Compacted         bool
}

type TurnMessageContext struct {
	ActorUserID       int64
	ActorRole         string
	EventOrigin       string
	EventOriginDetail string
}

type SearchHit struct {
	SessionID    string
	ChatID       int64
	UserID       int64
	TurnIndex    int
	Role         string
	Content      string
	FloorContent string
	CreatedAt    time.Time
}

type ArtifactRecord struct {
	ArtifactID       string
	SessionID        string
	ChatID           int64
	UserID           int64
	TurnIndex        int
	SourceType       string
	Kind             string
	Summary          string
	Handling         string
	Retention        string
	FetchState       string
	MaterializedPath string
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

type RhizomeNode struct {
	ID         int64
	Scope      string
	Name       string
	EventCount int
	LastSeenAt time.Time
}

func NormalizePlanState(state PlanState) PlanState {
	state.Explanation = strings.TrimSpace(state.Explanation)
	steps := make([]PlanStep, 0, len(state.Steps))
	for _, step := range state.Steps {
		text := strings.TrimSpace(step.Step)
		if text == "" {
			continue
		}
		status := NormalizePlanStatus(step.Status)
		if status == "" {
			status = PlanStatusPending
		}
		steps = append(steps, PlanStep{
			Step:   text,
			Status: status,
		})
	}
	state.Steps = steps
	if state.UpdatedAt.IsZero() && (len(state.Steps) > 0 || state.Explanation != "") {
		state.UpdatedAt = time.Now().UTC()
	}
	return state
}

func (s PlanState) Active() bool {
	return len(NormalizePlanState(s).Steps) > 0
}

func (s PlanState) FormattedSteps() []string {
	normalized := NormalizePlanState(s)
	out := make([]string, 0, len(normalized.Steps))
	for _, step := range normalized.Steps {
		out = append(out, "["+string(step.Status)+"] "+step.Step)
	}
	return out
}

func NormalizePlanStatus(status PlanStatus) PlanStatus {
	value := strings.ToLower(strings.TrimSpace(string(status)))
	value = strings.ReplaceAll(value, "-", "_")
	value = strings.ReplaceAll(value, " ", "_")
	switch PlanStatus(value) {
	case PlanStatusPending, PlanStatusInProgress, PlanStatusCompleted:
		return PlanStatus(value)
	default:
		return ""
	}
}

func NormalizeOperationState(state OperationState) OperationState {
	state.ID = strings.TrimSpace(state.ID)
	state.Objective = strings.TrimSpace(state.Objective)
	state.Status = NormalizeOperationStatus(state.Status)
	state.Stage = normalizeOperationStage(state.Stage)
	state.Summary = strings.TrimSpace(state.Summary)
	state.Proposal = normalizeOperationProposal(state.Proposal)

	findings := make([]OperationFinding, 0, len(state.Findings))
	for _, finding := range state.Findings {
		claim := strings.TrimSpace(finding.Claim)
		if claim == "" {
			continue
		}
		confidence := NormalizeFindingConfidence(finding.Confidence)
		if confidence == "" {
			confidence = FindingConfidenceMedium
		}
		findings = append(findings, OperationFinding{
			Claim:      claim,
			Confidence: confidence,
			Basis:      strings.TrimSpace(finding.Basis),
		})
	}
	state.Findings = findings

	artifacts := make([]OperationArtifact, 0, len(state.Artifacts))
	for _, artifact := range state.Artifacts {
		ref := strings.TrimSpace(artifact.Ref)
		if ref == "" {
			continue
		}
		artifacts = append(artifacts, OperationArtifact{
			Label: strings.TrimSpace(artifact.Label),
			Ref:   ref,
		})
	}
	state.Artifacts = artifacts

	if state.UpdatedAt.IsZero() && state.Active() {
		state.UpdatedAt = time.Now().UTC()
	}
	return state
}

func (s OperationState) Active() bool {
	normalized := s
	return strings.TrimSpace(normalized.ID) != "" ||
		strings.TrimSpace(normalized.Objective) != "" ||
		strings.TrimSpace(string(normalized.Status)) != "" ||
		strings.TrimSpace(normalized.Stage) != "" ||
		strings.TrimSpace(normalized.Summary) != "" ||
		normalized.Proposal.Active() ||
		len(normalized.Findings) > 0 ||
		len(normalized.Artifacts) > 0
}

func (p ToolProposal) Active() bool {
	return strings.TrimSpace(p.ProposalID) != "" ||
		strings.TrimSpace(p.ProposedBy) != "" ||
		strings.TrimSpace(p.ToolName) != "" ||
		strings.TrimSpace(p.WhyNow) != "" ||
		strings.TrimSpace(p.Contract) != "" ||
		strings.TrimSpace(string(p.ReviewStatus)) != "" ||
		strings.TrimSpace(p.RegisteredToolID) != ""
}

func NormalizeToolProposalReviewStatus(status ToolProposalReviewStatus) ToolProposalReviewStatus {
	value := normalizeEnumValue(string(status))
	switch ToolProposalReviewStatus(value) {
	case ToolProposalReviewStatusProposed, ToolProposalReviewStatusApproved, ToolProposalReviewStatusRejected:
		return ToolProposalReviewStatus(value)
	default:
		return ""
	}
}

func NormalizeToolProposal(proposal ToolProposal) ToolProposal {
	proposal.ProposalID = strings.TrimSpace(proposal.ProposalID)
	proposal.ProposedBy = strings.TrimSpace(proposal.ProposedBy)
	proposal.ToolName = strings.TrimSpace(proposal.ToolName)
	proposal.WhyNow = strings.TrimSpace(proposal.WhyNow)
	proposal.Contract = strings.TrimSpace(proposal.Contract)
	proposal.ReviewStatus = NormalizeToolProposalReviewStatus(proposal.ReviewStatus)
	proposal.RegisteredToolID = strings.TrimSpace(proposal.RegisteredToolID)
	if proposal.ReviewStatus == "" && proposal.Active() {
		proposal.ReviewStatus = ToolProposalReviewStatusProposed
	}
	if proposal.Contract == "" && proposal.Active() {
		proposal.Contract = "{}"
	}
	if proposal.CreatedAt.IsZero() && proposal.Active() {
		proposal.CreatedAt = time.Now().UTC()
	}
	if proposal.UpdatedAt.IsZero() && proposal.Active() {
		proposal.UpdatedAt = time.Now().UTC()
	}
	return proposal
}

func NormalizeRegisteredTool(tool RegisteredTool) RegisteredTool {
	tool.ToolName = strings.TrimSpace(tool.ToolName)
	tool.ImplementationRef = strings.TrimSpace(tool.ImplementationRef)
	if tool.CreatedAt.IsZero() && tool.ToolName != "" {
		tool.CreatedAt = time.Now().UTC()
	}
	if tool.UpdatedAt.IsZero() && tool.ToolName != "" {
		tool.UpdatedAt = time.Now().UTC()
	}
	return tool
}

func NormalizeToolExposure(exposure ToolExposure) ToolExposure {
	exposure.ToolName = strings.TrimSpace(exposure.ToolName)
	exposure.Principal = strings.TrimSpace(exposure.Principal)
	if exposure.CreatedAt.IsZero() && exposure.ToolName != "" && exposure.Principal != "" {
		exposure.CreatedAt = time.Now().UTC()
	}
	if exposure.UpdatedAt.IsZero() && exposure.ToolName != "" && exposure.Principal != "" {
		exposure.UpdatedAt = time.Now().UTC()
	}
	return exposure
}

func NormalizeTurnAuthorizationState(state TurnAuthorizationState) TurnAuthorizationState {
	state.Kind = TurnAuthorizationKind(strings.TrimSpace(string(state.Kind)))
	state.Status = TurnAuthorizationStatus(strings.TrimSpace(string(state.Status)))
	state.DecisionID = strings.TrimSpace(state.DecisionID)
	state.Objective = strings.TrimSpace(state.Objective)
	state.StageSummary = strings.TrimSpace(state.StageSummary)
	state.PersonaIntent = normalizeContinuationIntent(state.PersonaIntent)
	state.GovernorIntent = normalizeContinuationIntent(state.GovernorIntent)
	state.HandshakeBlockedReason = normalizeContinuationStage(state.HandshakeBlockedReason)
	if state.Kind == "" && (state.Status != "" || state.DecisionID != "" || state.Objective != "" || state.StageSummary != "" || state.RemainingTurns > 0 || state.ApprovedBy > 0) {
		state.Kind = TurnAuthorizationKindContinuation
	}
	if state.RemainingTurns < 0 {
		state.RemainingTurns = 0
	}
	if state.Status == TurnAuthorizationStatusIdle || state.Status == TurnAuthorizationStatusRevoked {
		state.ApprovedBy = 0
		state.DecisionID = ""
	}
	if state.UpdatedAt.IsZero() && (state.Kind != "" || state.Status != "" || state.DecisionID != "" || state.Objective != "" || state.StageSummary != "" || state.RemainingTurns > 0 || state.ApprovedBy > 0) {
		state.UpdatedAt = time.Now().UTC()
	}
	return state
}

func NormalizeContinuationState(state ContinuationState) ContinuationState {
	if strings.TrimSpace(string(state.Kind)) == "" {
		state.Kind = TurnAuthorizationKindContinuation
	}
	return NormalizeTurnAuthorizationState(state)
}

func (s TurnAuthorizationState) Active() bool {
	state := NormalizeTurnAuthorizationState(s)
	return state.Status == TurnAuthorizationStatusPending || state.Status == TurnAuthorizationStatusApproved
}

func (p OperationProposal) Active() bool {
	return strings.TrimSpace(p.ID) != "" ||
		strings.TrimSpace(p.Kind) != "" ||
		strings.TrimSpace(p.Summary) != "" ||
		strings.TrimSpace(p.WhyNow) != "" ||
		strings.TrimSpace(p.BoundedEffect) != "" ||
		strings.TrimSpace(string(p.Status)) != ""
}

func NormalizeOperationStatus(status OperationStatus) OperationStatus {
	value := normalizeEnumValue(string(status))
	switch OperationStatus(value) {
	case OperationStatusIdle, OperationStatusActive, OperationStatusBlocked, OperationStatusCompleted, OperationStatusFailed:
		return OperationStatus(value)
	default:
		return ""
	}
}

func NormalizeProposalStatus(status ProposalStatus) ProposalStatus {
	value := normalizeEnumValue(string(status))
	switch ProposalStatus(value) {
	case ProposalStatusPending, ProposalStatusApproved, ProposalStatusDenied, ProposalStatusExpired, ProposalStatusSuperseded:
		return ProposalStatus(value)
	default:
		return ""
	}
}

func NormalizeFindingConfidence(confidence FindingConfidence) FindingConfidence {
	value := normalizeEnumValue(string(confidence))
	switch FindingConfidence(value) {
	case FindingConfidenceLow, FindingConfidenceMedium, FindingConfidenceHigh:
		return FindingConfidence(value)
	default:
		return ""
	}
}

func normalizeOperationProposal(proposal OperationProposal) OperationProposal {
	proposal.ID = strings.TrimSpace(proposal.ID)
	proposal.Kind = normalizeOperationStage(proposal.Kind)
	proposal.Summary = strings.TrimSpace(proposal.Summary)
	proposal.WhyNow = strings.TrimSpace(proposal.WhyNow)
	proposal.BoundedEffect = strings.TrimSpace(proposal.BoundedEffect)
	proposal.Status = NormalizeProposalStatus(proposal.Status)
	if proposal.Status == "" && proposal.Active() {
		proposal.Status = ProposalStatusPending
	}
	if proposal.UpdatedAt.IsZero() && proposal.Active() {
		proposal.UpdatedAt = time.Now().UTC()
	}
	return proposal
}

func normalizeOperationStage(stage string) string {
	return normalizeEnumValue(stage)
}

func normalizeContinuationIntent(intent ContinuationIntent) ContinuationIntent {
	intent.Decision = normalizeContinuationIntentDecision(intent.Decision)
	intent.Rationale = strings.TrimSpace(intent.Rationale)
	intent.NextStep = strings.TrimSpace(intent.NextStep)
	intent.Constraints = strings.TrimSpace(intent.Constraints)
	intent.Confidence = normalizeContinuationConfidence(intent.Confidence)
	if intent.UpdatedAt.IsZero() && (intent.Decision != "" || intent.Rationale != "" || intent.NextStep != "" || intent.Constraints != "" || intent.Confidence != "" || intent.Ratified) {
		intent.UpdatedAt = time.Now().UTC()
	}
	return intent
}

func normalizeContinuationIntentDecision(decision ContinuationIntentDecision) ContinuationIntentDecision {
	switch normalizeEnumValue(string(decision)) {
	case string(ContinuationIntentDecisionContinue):
		return ContinuationIntentDecisionContinue
	case string(ContinuationIntentDecisionStop):
		return ContinuationIntentDecisionStop
	case string(ContinuationIntentDecisionHold):
		return ContinuationIntentDecisionHold
	default:
		return ""
	}
}

func normalizeContinuationConfidence(confidence string) string {
	switch normalizeEnumValue(confidence) {
	case "low", "medium", "high":
		return normalizeEnumValue(confidence)
	default:
		return ""
	}
}

func normalizeContinuationStage(value string) string {
	return normalizeEnumValue(value)
}

func normalizeEnumValue(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, "-", "_")
	value = strings.ReplaceAll(value, " ", "_")
	return value
}

type RhizomeEvent struct {
	ID        int64
	Scope     string
	Source    string
	Salience  float64
	Concepts  []string
	CreatedAt time.Time
}

type RhizomeEdge struct {
	ID               int64
	Scope            string
	LeftConcept      string
	RightConcept     string
	Strength         float64
	RecurrenceCount  int
	LastReinforcedAt time.Time
	DecayState       string
	LastSource       string
}
