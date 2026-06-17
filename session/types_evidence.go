//go:build linux

package session

import "time"

const (
	EvidenceSourceExecutionEvent       = "execution_event"
	EvidenceSourceTurnRun              = "turn_run"
	EvidenceSourceMessage              = "message"
	EvidenceSourceSessionState         = "session_state"
	EvidenceSourceOperationState       = "operation_state"
	EvidenceSourcePlanState            = "plan_state"
	EvidenceSourceContinuationState    = "continuation_state"
	EvidenceSourceReviewEvent          = "review_event"
	EvidenceSourceCapabilityRequest    = "capability_request"
	EvidenceSourceCapabilityGrant      = "capability_grant"
	EvidenceSourceCapabilityInvocation = "capability_invocation"
	EvidenceSourceMission              = "mission"
	EvidenceSourceMissionEvent         = "mission_event"
	EvidenceSourceMissionHandoff       = "mission_handoff"
	EvidenceSourceMissionResult        = "mission_result"
	EvidenceSourceCuriosity            = "curiosity_observation"
	EvidenceSourceInteriorSignal       = "interior_signal"
	EvidenceSourceReentry              = "reentry_recommendation"
	EvidenceSourceTelegramIngress      = "telegram_ingress"
	EvidenceSourceTelegramMediaPicker  = "telegram_media_picker"
	EvidenceSourceArtifact             = "artifact"
	EvidenceSourceToolOutput           = "tool_output"
)

const (
	EvidenceStatusObserved    = "observed"
	EvidenceStatusClaimed     = "claimed"
	EvidenceStatusProjection  = "projection"
	EvidenceStatusAttested    = "attested"
	EvidenceStatusGap         = "gap"
	EvidenceRedactionNone     = "none"
	EvidenceRedactionDigest   = "digest"
	EvidenceRedactionMetadata = "metadata_only"
)

type EvidenceObject struct {
	ID              string
	EvidenceType    string
	SourceKind      string
	SourceRef       string
	SourceTable     string
	SourceID        string
	SessionID       string
	ChatID          int64
	UserID          int64
	Scope           ScopeRef
	AuthorityClass  string
	EpistemicStatus string
	RedactionClass  string
	SubjectKey      string
	Summary         string
	Digest          string
	PayloadJSON     string
	PayloadHash     string
	ObservedAt      time.Time
	CreatedAt       time.Time
}

type EvidenceObjectInput struct {
	ID              string
	EvidenceType    string
	SourceKind      string
	SourceRef       string
	SourceTable     string
	SourceID        string
	SessionID       string
	ChatID          int64
	UserID          int64
	Scope           ScopeRef
	AuthorityClass  string
	EpistemicStatus string
	RedactionClass  string
	SubjectKey      string
	Summary         string
	Digest          string
	PayloadJSON     string
	ObservedAt      time.Time
	CreatedAt       time.Time
}

type EvidenceLink struct {
	ID             int64
	FromEvidenceID string
	ToEvidenceID   string
	LinkType       string
	Source         string
	Confidence     float64
	MetadataJSON   string
	CreatedAt      time.Time
}

type EvidenceLinkInput struct {
	FromEvidenceID string
	ToEvidenceID   string
	LinkType       string
	Source         string
	Confidence     float64
	MetadataJSON   string
	CreatedAt      time.Time
}

type EvidenceHydrationQuery struct {
	SessionID           string
	Key                 SessionKey
	OperationID         string
	Query               string
	RequiredEvidenceIDs []string
	Limit               int
	IncludeCrossScope   bool
	Now                 time.Time
}

type EvidenceHydrationResult struct {
	RunID              string
	SessionID          string
	Query              string
	Selected           []EvidenceObject
	Required           []EvidenceObject
	MissingEvidenceIDs []string
	FallbackUsed       bool
	FallbackReason     string
	CreatedAt          time.Time
}

type EvidenceHydrationRunInput struct {
	ID                  string
	SessionID           string
	ChatID              int64
	UserID              int64
	Scope               ScopeRef
	OperationID         string
	Query               string
	Mode                string
	Status              string
	SelectedEvidenceIDs []string
	MissingEvidenceIDs  []string
	FallbackUsed        bool
	FallbackReason      string
	CreatedAt           time.Time
}

type EvidenceLedgerStats struct {
	ObjectCount       int
	LatestEvidenceID  string
	LatestSourceKind  string
	LatestObservedAt  time.Time
	HydrationRunCount int
	LatestHydrationID string
	LatestHydratedAt  time.Time
}
