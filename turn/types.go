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

// Engine is the boundary for single-turn orchestration.
//
// Production implementations support interactive, heartbeat, cron, recovery,
// and durable-child turns through one contract while varying inputs and ports
// by turn species.
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
	// PreparedUserText lets runtime adapters pass the prepared ledger/user text
	// that face stages should see when raw inbound text has already been shaped.
	PreparedUserText string
	// InboundWasVoice preserves whether the inbound turn started from a voice
	// artifact so downstream policy can reason about the original modality.
	InboundWasVoice bool
	// ReplyWithVoice is the resolved delivery modality after runtime defaults
	// and per-turn media intent overrides have been applied.
	ReplyWithVoice bool
}

// Result is the house-level outcome of one turn.
//
// It preserves the distinction between:
//   - the raw turn result from the governor/tool loop
//   - the governor-owned floor sidecar
//   - the visible scene text the user actually receives
//
// The embedded policy, commit, and delivery fields keep orchestration choices
// inspectable in tests and runtime adapters.
type Result struct {
	RunID           int64
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
	PersonaIntent   session.ContinuationIntent
	GovernorIntent  session.ContinuationIntent
	RenderedStream  bool
	RenderedID      int64
	RenderedType    string
	ReplyWithVoice  bool
	ReplyModality   string
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
