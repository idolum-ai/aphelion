//go:build linux

package turn

import (
	"context"
	"time"

	"github.com/idolum-ai/aphelion/agent"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/pipeline"
	"github.com/idolum-ai/aphelion/session"
)

// Engine is the intended boundary for single-turn orchestration.
//
// A future implementation should be able to run interactive, heartbeat, cron,
// recovery, and durable-child turns through the same house-level contract while
// varying only the inputs and ports around it.
type Engine interface {
	Handle(ctx context.Context, req Request) (*Result, error)
}

// Request is the house-level input to one turn.
//
// It carries the inbound event, the scoped session identity, the current
// session snapshot, and any precomputed constraints the process shell has
// already resolved before entering the turn engine.
type Request struct {
	RunKind    session.TurnRunKind
	SessionKey session.SessionKey
	Inbound    core.InboundMessage
	Session    *session.Session
	Now        time.Time
}

// Result is the house-level outcome of one turn.
//
// It preserves the distinction between:
//   - the raw turn result from the governor/tool loop
//   - the governor-owned floor sidecar
//   - the visible scene text the user actually receives
//
// The embedded policy, commit, and delivery fields make the orchestration
// choices inspectable in tests before the package is wired into production.
type Result struct {
	Turn            *core.TurnResult
	Prepared        pipeline.TurnPrepareContract
	OutHistory      []agent.Message
	HistoryInputLen int
	VisibleReply    string
	FloorText       string
	FloorMetadata   string
	MaterialFloor   core.MaterialPacket
	PlanState       session.PlanState
	OperationState  session.OperationState
	RenderedStream  bool
	RenderedID      int64
	RenderedType    string
	Policy          Policy
	ProposalNote    string
	Commit          CommitResult
	Delivery        DeliveryResult
}

// Options are stable house-level defaults for a future engine instance.
type Options struct {
	GovernorName string
	FaceName     string
	Channel      string
	Style        string
}
