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
	state                 *interactiveTurnState
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
}

func (c *interactiveTurnCoordinator) Propose(ctx context.Context, req turn.FaceProposalRequest) (*turn.FaceProposalResult, error) {
	if c == nil || c.runtime == nil {
		return nil, fmt.Errorf("interactive coordinator unavailable")
	}
	c.runtime.markSessionTurnPhase(c.key, "face_proposal", "drafting relationship note before execution")

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

func (c *interactiveTurnCoordinator) requestFaceNote(ctx context.Context, mode string, awareness prompt.RuntimeAwareness, priorProposal string, feedback string) (string, core.TokenUsage, error) {
	if c == nil || c.runtime == nil {
		return "", core.TokenUsage{}, fmt.Errorf("interactive coordinator unavailable")
	}
	proposal, usage, err := c.runtime.proposeTurnCoordinatorFace(ctx, turnCoordinatorProposalInput{
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
		Voice:   c.state.replyWithVoice(),
	}
	rendered, err := c.runtime.renderTurnCoordinatorFace(ctx, turnCoordinatorRenderInput{
		Scope:                 c.scope,
		Msg:                   c.msg,
		Key:                   c.key,
		Channel:               c.requestChannel(),
		PrincipalRole:         c.principalRoleOrActor(),
		LastGovernor:          c.state.governor(),
		LastFaceAwareness:     c.state.faceAwareness(),
		BaseGovernorAwareness: c.baseGovernorAwareness,
		FacePolicy:            c.facePolicy,
		UseMaterialFloor:      c.useMaterialFloor,
		CurrentFaceModel:      c.currentFaceModel,
		ReplyWithVoice:        c.state.replyWithVoice(),
		AllowStream:           true,
		PromptInput:           c.prepared.LedgerText,
		Audit:                 c.audit,
		FallbackOptions:       fallbackOpts,
	})
	if err != nil {
		return nil, err
	}
	if c.state.governor() == nil || c.state.governor().Turn == nil {
		return &turn.FaceRenderResult{}, nil
	}
	return &turn.FaceRenderResult{
		Text:          strings.TrimSpace(rendered.ReplyText),
		Usage:         rendered.Usage,
		Streamed:      rendered.StreamedReply,
		RenderedID:    rendered.OutboundID,
		RenderedType:  rendered.OutboundType,
		ReplyModality: rendered.ReplyModality,
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
	sess := c.state.session()
	if sess == nil {
		return nil, fmt.Errorf("interactive turn state missing session")
	}
	sess.ChatType = "dm"
	sess.UserName = c.msg.SenderName

	output, err := c.runtime.executeTurnCoordinator(ctx, turnCoordinatorExecuteInput{
		Scope:                 c.scope,
		Msg:                   c.msg,
		Key:                   c.key,
		Sess:                  sess,
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
		FaceName:              c.coordinatorFaceName(),
		RequestFaceNote:       c.requestFaceNote,
		RunErrPrefix:          "run turn",
		InvalidOutputPrefix:   "invalid turn output",
	})
	if err != nil {
		return nil, err
	}
	c.state.applyExecution(output, c.runtime.preparedReplyWithVoice(c.prepared))
	return output.GovernorResult, nil
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
	if c.runtime != nil {
		return c.runtime.governorName()
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
	if c.runtime != nil {
		return c.runtime.faceName()
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
