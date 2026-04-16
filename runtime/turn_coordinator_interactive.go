//go:build linux

package runtime

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/idolum-ai/aphelion/agent"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/face"
	"github.com/idolum-ai/aphelion/pipeline"
	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/prompt"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/tool/sandbox"
	"github.com/idolum-ai/aphelion/turn"
	"github.com/idolum-ai/aphelion/workspace"
)

type interactiveTurnCoordinator struct {
	runtime               *Runtime
	actor                 principal.Principal
	scope                 sandbox.Scope
	msg                   core.InboundMessage
	key                   session.SessionKey
	sess                  *session.Session
	prepared              pipeline.TurnPrepareContract
	exec                  pipeline.TurnExecutionContract
	facePolicy            pipeline.FacePolicy
	useMaterialFloor      bool
	governorName          string
	faceName              string
	channelName           string
	principalRole         string
	hiddenInputs          hiddenInputSet
	promptContext         *workspace.PromptContext
	tools                 agent.ToolRegistry
	currentFaceModel      face.Renderer
	baseGovernorAwareness prompt.RuntimeAwareness
	audit                 *turnAuditRecorder
	lastGovernor          *turn.GovernorResult
	lastFaceAwareness     prompt.RuntimeAwareness
	// replyWithVoice captures the active channel-aware voice preference for render.
	replyWithVoice bool
	// lastRenderedReply caches the visible reply text from the last render call.
	lastRenderedReply string
	lastToolLog       []string
}

func (c *interactiveTurnCoordinator) Propose(ctx context.Context, req turn.FaceProposalRequest) (*turn.FaceProposalResult, error) {
	if c == nil || c.runtime == nil {
		return nil, fmt.Errorf("interactive coordinator unavailable")
	}

	awareness := req.Runtime
	awareness.ArtifactMode = "scene"
	proposal, usage, err := c.runtime.proposeTurnCoordinatorFace(ctx, turnCoordinatorProposalInput{
		Scope:            c.scope,
		CurrentFaceModel: c.currentFaceModel,
		GovernorName:     req.GovernorName,
		FaceName:         req.FaceName,
		Channel:          req.Channel,
		Mode:             req.Mode,
		PrincipalRole:    req.PrincipalRole,
		LatestUserInput:  req.LatestUserInput,
		RuntimeAwareness: awareness,
	})
	if err != nil {
		log.Printf("WARN idolum proposal failed backend=%s principal=%s err=%v", c.runtime.faceBackend, c.actor.Role, err)
		return &turn.FaceProposalResult{}, nil
	}
	if strings.TrimSpace(proposal) == "" {
		return &turn.FaceProposalResult{}, nil
	}
	return &turn.FaceProposalResult{
		Note:  strings.TrimSpace(proposal),
		Usage: usage,
	}, nil
}

func (c *interactiveTurnCoordinator) requestFaceNote(mode string, awareness prompt.RuntimeAwareness, priorProposal string, feedback string) (string, core.TokenUsage, error) {
	if c == nil || c.runtime == nil {
		return "", core.TokenUsage{}, fmt.Errorf("interactive coordinator unavailable")
	}
	proposal, usage, err := c.runtime.proposeTurnCoordinatorFace(context.Background(), turnCoordinatorProposalInput{
		Scope:            c.scope,
		CurrentFaceModel: c.currentFaceModel,
		GovernorName:     c.coordinatorGovernorName(),
		FaceName:         c.coordinatorFaceName(),
		Channel:          c.requestChannel(),
		Mode:             mode,
		PrincipalRole:    c.principalRoleOrActor(),
		LatestUserInput:  c.prepared.LedgerText,
		RuntimeAwareness: awareness,
		PriorProposal:    priorProposal,
		Feedback:         feedback,
	})
	if err != nil {
		log.Printf("WARN idolum proposal failed backend=%s principal=%s err=%v", c.runtime.faceBackend, c.actor.Role, err)
		return "", core.TokenUsage{}, err
	}
	return strings.TrimSpace(proposal), usage, nil
}

func (c *interactiveTurnCoordinator) Render(ctx context.Context, req turn.FaceRenderRequest) (*turn.FaceRenderResult, error) {
	if c == nil || c.runtime == nil {
		return nil, fmt.Errorf("interactive coordinator unavailable")
	}
	fallbackOpts := pipeline.FallbackOptions{
		Channel: c.requestChannel(),
		Voice:   c.replyWithVoice,
	}
	rendered, err := c.runtime.renderTurnCoordinatorFace(ctx, turnCoordinatorRenderInput{
		Scope:                 c.scope,
		Msg:                   c.msg,
		Channel:               c.requestChannel(),
		PrincipalRole:         c.principalRoleOrActor(),
		LastGovernor:          c.lastGovernor,
		LastFaceAwareness:     c.lastFaceAwareness,
		BaseGovernorAwareness: c.baseGovernorAwareness,
		FacePolicy:            c.facePolicy,
		UseMaterialFloor:      c.useMaterialFloor,
		CurrentFaceModel:      c.currentFaceModel,
		ReplyWithVoice:        c.replyWithVoice,
		AllowStream:           true,
		PromptInput:           c.prepared.LedgerText,
		Audit:                 c.audit,
		FallbackOptions:       fallbackOpts,
	})
	if err != nil {
		return nil, err
	}
	if c.lastGovernor == nil || c.lastGovernor.Turn == nil {
		return &turn.FaceRenderResult{}, nil
	}
	c.lastRenderedReply = strings.TrimSpace(rendered.ReplyText)
	c.lastToolLog = nil
	if c.lastGovernor != nil && c.lastGovernor.Turn != nil {
		c.lastToolLog = c.lastGovernor.Turn.ToolLog
	}
	return &turn.FaceRenderResult{
		Text:         strings.TrimSpace(rendered.ReplyText),
		Usage:        rendered.Usage,
		Streamed:     rendered.StreamedReply,
		RenderedID:   rendered.OutboundID,
		RenderedType: rendered.OutboundType,
	}, nil
}

func (c *interactiveTurnCoordinator) Execute(ctx context.Context, req turn.GovernorRequest) (*turn.GovernorResult, error) {
	if c == nil || c.runtime == nil {
		return nil, fmt.Errorf("interactive coordinator unavailable")
	}
	runKind := req.RunKind
	if runKind == "" {
		runKind = session.TurnRunKindInteractive
	}
	c.sess.ChatType = "dm"
	c.sess.UserName = c.msg.SenderName

	output, err := c.runtime.executeTurnCoordinator(ctx, turnCoordinatorExecuteInput{
		Scope:                 c.scope,
		Msg:                   c.msg,
		Key:                   c.key,
		Sess:                  c.sess,
		Prepared:              c.prepared,
		Exec:                  c.exec,
		UseMaterialFloor:      c.useMaterialFloor,
		HiddenInputs:          c.hiddenInputs,
		PromptContext:         c.promptContext,
		Tools:                 c.tools,
		BaseGovernorAwareness: c.baseGovernorAwareness,
		Audit:                 c.audit,
		RunKind:               runKind,
		FaceNote:              req.FaceNote,
		Channel:               c.requestChannel(),
		PrincipalRole:         c.principalRoleOrActor(),
		GovernorName:          c.coordinatorGovernorName(),
		RequestFaceNote:       c.requestFaceNote,
		RunErrPrefix:          "run turn",
		InvalidOutputPrefix:   "invalid turn output",
	})
	if err != nil {
		return nil, err
	}
	c.sess = output.Sess
	c.lastFaceAwareness = output.LastFaceAwareness
	c.lastGovernor = output.GovernorResult
	c.replyWithVoice = c.runtime.shouldReplyWithVoice(c.prepared.InboundWasVoice) && len(output.GovernorResult.Turn.Media) == 0
	return output.GovernorResult, nil
}

func (c *interactiveTurnCoordinator) getTurnToolLog() []string {
	if c == nil || c.lastGovernor == nil || c.lastGovernor.Turn == nil {
		return nil
	}
	return c.lastGovernor.Turn.ToolLog
}

func (c *interactiveTurnCoordinator) requestChannel() string {
	if c == nil {
		return "telegram"
	}
	if trimmed := strings.TrimSpace(c.channelName); trimmed != "" {
		return trimmed
	}
	return "telegram"
}

func (c *interactiveTurnCoordinator) coordinatorGovernorName() string {
	if c == nil {
		return prompt.DefaultGovernorName
	}
	if trimmed := strings.TrimSpace(c.governorName); trimmed != "" {
		return trimmed
	}
	return prompt.DefaultGovernorName
}

func (c *interactiveTurnCoordinator) coordinatorFaceName() string {
	if c == nil {
		return face.DefaultFaceName
	}
	if trimmed := strings.TrimSpace(c.faceName); trimmed != "" {
		return trimmed
	}
	return face.DefaultFaceName
}

func (c *interactiveTurnCoordinator) principalRoleOrActor() string {
	if c == nil {
		return ""
	}
	if trimmed := strings.TrimSpace(c.principalRole); trimmed != "" {
		return trimmed
	}
	return string(c.actor.Role)
}
