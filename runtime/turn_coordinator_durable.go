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
	"github.com/idolum-ai/aphelion/prompt"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/tool/sandbox"
	"github.com/idolum-ai/aphelion/turn"
	"github.com/idolum-ai/aphelion/workspace"
)

type durableGroupTurnCoordinator struct {
	runtime                   *Runtime
	registered                core.DurableAgent
	livePolicy                core.DurableAgentLivePolicy
	scope                     sandbox.Scope
	msg                       core.InboundMessage
	key                       session.SessionKey
	sess                      *session.Session
	prepared                  pipeline.TurnPrepareContract
	exec                      pipeline.TurnExecutionContract
	facePolicy                pipeline.FacePolicy
	useMaterialFloor          bool
	governorName              string
	faceName                  string
	channelName               string
	principalRole             string
	hiddenInputs              hiddenInputSet
	promptContext             *workspace.PromptContext
	tools                     agent.ToolRegistry
	currentFaceModel          face.Renderer
	baseGovernorAwareness     prompt.RuntimeAwareness
	audit                     *turnAuditRecorder
	allowStream               bool
	pendingParentConversation []core.DurableAgentConversationMessage
	governorContextBuilder    durableWakeGovernorContextBuilder
	lastGovernor              *turn.GovernorResult
	lastFaceAwareness         prompt.RuntimeAwareness
}

func (c *durableGroupTurnCoordinator) Propose(ctx context.Context, req turn.FaceProposalRequest) (*turn.FaceProposalResult, error) {
	if c == nil || c.runtime == nil {
		return nil, fmt.Errorf("durable group coordinator unavailable")
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
		log.Printf("WARN idolum proposal failed backend=%s durable_group=%s err=%v", c.runtime.faceBackend, c.registered.AgentID, err)
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

func (c *durableGroupTurnCoordinator) requestFaceNote(ctx context.Context, mode string, awareness prompt.RuntimeAwareness, priorProposal string, feedback string) (string, core.TokenUsage, error) {
	if c == nil || c.runtime == nil {
		return "", core.TokenUsage{}, fmt.Errorf("durable group coordinator unavailable")
	}
	proposal, usage, err := c.runtime.proposeTurnCoordinatorFace(ctx, turnCoordinatorProposalInput{
		Scope:            c.scope,
		CurrentFaceModel: c.currentFaceModel,
		GovernorName:     c.coordinatorGovernorName(),
		FaceName:         c.coordinatorFaceName(),
		Channel:          c.requestChannel(),
		Mode:             mode,
		PrincipalRole:    c.principalRoleOrLiveRole(),
		LatestUserInput:  c.prepared.LedgerText,
		RuntimeAwareness: awareness,
		PriorProposal:    priorProposal,
		Feedback:         feedback,
	})
	if err != nil {
		log.Printf("WARN idolum proposal failed backend=%s durable_group=%s err=%v", c.runtime.faceBackend, c.registered.AgentID, err)
		return "", core.TokenUsage{}, err
	}
	return strings.TrimSpace(proposal), usage, nil
}

func (c *durableGroupTurnCoordinator) Render(ctx context.Context, req turn.FaceRenderRequest) (*turn.FaceRenderResult, error) {
	if c == nil || c.runtime == nil {
		return nil, fmt.Errorf("durable group coordinator unavailable")
	}
	fallbackOpts := pipeline.FallbackOptions{Channel: c.requestChannel()}
	rendered, err := c.runtime.renderTurnCoordinatorFace(ctx, turnCoordinatorRenderInput{
		Scope:                 c.scope,
		Msg:                   c.msg,
		Key:                   c.key,
		Channel:               c.requestChannel(),
		PrincipalRole:         c.principalRoleOrLiveRole(),
		LastGovernor:          c.lastGovernor,
		LastFaceAwareness:     c.lastFaceAwareness,
		BaseGovernorAwareness: c.baseGovernorAwareness,
		FacePolicy:            c.facePolicy,
		UseMaterialFloor:      c.useMaterialFloor,
		CurrentFaceModel:      c.currentFaceModel,
		ReplyWithVoice:        false,
		AllowStream:           c.allowStream,
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
	return &turn.FaceRenderResult{
		Text:          strings.TrimSpace(rendered.ReplyText),
		Usage:         rendered.Usage,
		Streamed:      rendered.StreamedReply,
		RenderedID:    rendered.OutboundID,
		RenderedType:  rendered.OutboundType,
		ReplyModality: rendered.ReplyModality,
	}, nil
}

func (c *durableGroupTurnCoordinator) Execute(ctx context.Context, req turn.GovernorRequest) (*turn.GovernorResult, error) {
	if c == nil || c.runtime == nil {
		return nil, fmt.Errorf("durable group coordinator unavailable")
	}
	runKind := req.RunKind
	if runKind == "" {
		runKind = session.TurnRunKindInteractive
	}
	c.sess.ChatType = firstNonEmpty(strings.TrimSpace(c.msg.ChatType), "group")
	c.sess.ChatTitle = strings.TrimSpace(c.msg.ChatTitle)
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
		PrincipalRole:         c.principalRoleOrLiveRole(),
		GovernorName:          c.coordinatorGovernorName(),
		FaceName:              c.coordinatorFaceName(),
		RequestFaceNote:       c.requestFaceNote,
		ExtraSystemMessages: []agent.Message{
			{Role: "system", Content: c.governorContext()},
		},
		RunErrPrefix:        "run durable group turn",
		InvalidOutputPrefix: "invalid durable group turn output",
	})
	if err != nil {
		return nil, err
	}
	c.sess = output.Sess
	c.lastFaceAwareness = output.LastFaceAwareness
	c.lastGovernor = output.GovernorResult
	return output.GovernorResult, nil
}

func (c *durableGroupTurnCoordinator) governorContext() string {
	if c == nil {
		return ""
	}
	parts := make([]string, 0, 2)
	if c.governorContextBuilder != nil {
		parts = append(parts, strings.TrimSpace(c.governorContextBuilder(c.registered, c.livePolicy, c.msg, c.pendingParentConversation)))
	} else {
		parts = append(parts, durableTelegramGovernorContext(c.registered, c.livePolicy, c.msg, c.pendingParentConversation))
	}
	if profile := durableAgentProfileContext(c.scope, c.registered); profile != "" {
		parts = append(parts, profile)
	}
	return strings.TrimSpace(strings.Join(compactNonEmptyStrings(parts), "\n\n"))
}

func (c *durableGroupTurnCoordinator) requestChannel() string {
	if c == nil {
		return "telegram_group"
	}
	if trimmed := strings.TrimSpace(c.channelName); trimmed != "" {
		return trimmed
	}
	return "telegram_group"
}

func (c *durableGroupTurnCoordinator) coordinatorGovernorName() string {
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

func (c *durableGroupTurnCoordinator) coordinatorFaceName() string {
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

func (c *durableGroupTurnCoordinator) principalRoleOrLiveRole() string {
	if c == nil {
		return "durable_agent"
	}
	if trimmed := strings.TrimSpace(c.principalRole); trimmed != "" {
		return trimmed
	}
	return "durable_agent"
}
