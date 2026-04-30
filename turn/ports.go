//go:build linux

package turn

import (
	"context"

	"github.com/idolum-ai/aphelion/agent"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/pipeline"
	"github.com/idolum-ai/aphelion/prompt"
	"github.com/idolum-ai/aphelion/session"
)

// GovernorPort is the execution surface the turn engine expects from the
// governor-side pipeline.
type GovernorPort interface {
	Execute(ctx context.Context, req GovernorRequest) (*GovernorResult, error)
}

// GovernorRequest is the orchestration-facing governor input.
//
// It stays smaller than the eventual runtime prompt assembly. The goal here is
// to express the turn boundary, not freeze the entire governor contract yet.
type GovernorRequest struct {
	RunKind    session.TurnRunKind
	SessionKey session.SessionKey
	Inbound    core.InboundMessage
	Session    *session.Session
	FaceNote   string
	Policy     Policy
}

// GovernorResult is the orchestration-facing governor outcome.
//
// It surfaces the raw turn result alongside the floor sidecar that later face
// rendering and persistence depend on.
type GovernorResult struct {
	Turn            *core.TurnResult
	Prepared        pipeline.TurnPrepareContract
	OutHistory      []agent.Message
	HistoryInputLen int
	FloorText       string
	FloorMetadata   string
	MaterialFloor   core.MaterialPacket
	PlanState       session.PlanState
	OperationState  session.OperationState
	PersonaIntent   session.ContinuationIntent
	GovernorIntent  session.ContinuationIntent
	Usage           core.TokenUsage
}

// FacePort is the relationship surface the turn engine expects from the
// face-side pipeline.
//
// Proposal and render are separate because the face appears twice in the turn:
// once before execution as conversational pressure, and once after execution as
// scene authorship.
type FacePort interface {
	Propose(ctx context.Context, req FaceProposalRequest) (*FaceProposalResult, error)
	Render(ctx context.Context, req FaceRenderRequest) (*FaceRenderResult, error)
}

// FaceProposalRequest is the pre-execution face input.
//
// It corresponds to Idolum's first appearance in the turn: proposing posture,
// pressure, or brokerage before the governor/tool loop executes.
type FaceProposalRequest struct {
	GovernorName    string
	FaceName        string
	Channel         string
	Style           string
	Mode            string
	PrincipalRole   string
	WorkspaceRoot   string
	LatestUserInput string
	Runtime         prompt.RuntimeAwareness
}

// FaceProposalResult is the face-side outcome of a proposal pass.
//
// Usage is explicit here so a future turn engine does not need out-of-band
// renderer-specific accounting hooks to aggregate token costs honestly.
type FaceProposalResult struct {
	Note  string
	Usage core.TokenUsage
}

// FaceRenderRequest is the post-execution face input.
//
// It corresponds to Idolum's second appearance in the turn: staging the visible
// scene from the governor-owned floor and latest user context.
type FaceRenderRequest struct {
	GovernorName    string
	FaceName        string
	Channel         string
	Style           string
	PrincipalRole   string
	WorkspaceRoot   string
	FloorText       string
	MaterialFloor   core.MaterialPacket
	LatestUserInput string
	Runtime         prompt.RuntimeAwareness
}

// FaceRenderResult is the visible outcome of face scene authorship.
type FaceRenderResult struct {
	Text          string
	Usage         core.TokenUsage
	Streamed      bool
	RenderedID    int64
	RenderedType  string
	ReplyModality string
}

// PersistencePort captures the durable write boundary the turn engine should
// use instead of reaching directly into storage concerns.
type PersistencePort interface {
	Persist(ctx context.Context, req CommitRequest) (*CommitResult, error)
}

// DeliveryPort captures outbound delivery as an explicit port rather than an
// implicit side effect inside the orchestration flow.
type DeliveryPort interface {
	Deliver(ctx context.Context, req DeliveryRequest) (*DeliveryResult, error)
}

// DeliveryRequest is the explicit outbound handoff from orchestration to the
// channel delivery surface.
type DeliveryRequest struct {
	Message         core.OutboundMessage
	InboundWasVoice bool
	ReplyWithVoice  bool
	Result          *Result
}

// DeliveryResult records the machine-visible outcome of outbound delivery.
type DeliveryResult struct {
	MessageID int64
	Kind      string
}
