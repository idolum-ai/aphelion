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

type coordinatorProposalCommonInput struct {
	Scope            sandbox.Scope
	CurrentFaceModel face.Renderer
	GovernorName     string
	FaceName         string
	Channel          string
	Mode             string
	PrincipalRole    string
	LatestUserInput  string
	RuntimeAwareness prompt.RuntimeAwareness
	PriorProposal    string
	Feedback         string
}

func (r *Runtime) proposeCoordinatorFaceCommon(ctx context.Context, input coordinatorProposalCommonInput) (string, core.TokenUsage, error) {
	if r == nil {
		return "", core.TokenUsage{}, fmt.Errorf("runtime unavailable")
	}
	proposer, ok := input.CurrentFaceModel.(face.Proposer)
	if input.CurrentFaceModel == nil || !ok || r.faceBackend == face.BackendFloorFallback {
		return "", core.TokenUsage{}, nil
	}
	mode := strings.TrimSpace(input.Mode)
	if mode == "" {
		mode = "proposal"
	}
	proposal, err := proposer.Propose(ctx, face.ProposalRequest{
		GovernorName:      input.GovernorName,
		FaceName:          input.FaceName,
		Channel:           input.Channel,
		Mode:              mode,
		PrincipalRole:     input.PrincipalRole,
		WorkspaceRoot:     faceWorkspaceRoot(input.Scope),
		LatestUserInput:   input.LatestUserInput,
		PriorProposal:     input.PriorProposal,
		BrokerageFeedback: input.Feedback,
		Runtime:           input.RuntimeAwareness,
	})
	if err != nil {
		return "", core.TokenUsage{}, err
	}
	return strings.TrimSpace(proposal), consumeFaceUsage(input.CurrentFaceModel), nil
}

type coordinatorRenderCommonInput struct {
	Scope                 sandbox.Scope
	Msg                   core.InboundMessage
	Channel               string
	PrincipalRole         string
	LastGovernor          *turn.GovernorResult
	LastFaceAwareness     prompt.RuntimeAwareness
	BaseGovernorAwareness prompt.RuntimeAwareness
	FacePolicy            pipeline.FacePolicy
	UseMaterialFloor      bool
	CurrentFaceModel      face.Renderer
	ReplyWithVoice        bool
	AllowStream           bool
	PromptInput           string
	Audit                 *turnAuditRecorder
	FallbackOptions       face.FallbackOptions
}

func (r *Runtime) renderCoordinatorFaceCommon(ctx context.Context, input coordinatorRenderCommonInput) (turnRenderResult, error) {
	if input.LastGovernor == nil || input.LastGovernor.Turn == nil {
		return turnRenderResult{}, nil
	}
	gov := input.LastGovernor
	mediaOnlyReply := len(gov.Turn.Media) > 0 && strings.TrimSpace(gov.Turn.Text) == ""
	replyText := ""
	if !mediaOnlyReply {
		replyText = face.SerializeFloorFallback(gov.MaterialFloor, gov.FloorText, input.FallbackOptions)
	}
	faceAwareness := input.LastFaceAwareness
	if strings.TrimSpace(faceAwareness.DeliveryMode) == "" {
		faceAwareness = input.BaseGovernorAwareness
	}
	return r.renderTurnReply(turnRenderInput{
		Ctx:              ctx,
		Scope:            input.Scope,
		Msg:              input.Msg,
		Channel:          input.Channel,
		PrincipalRole:    input.PrincipalRole,
		OutHistory:       gov.OutHistory,
		HistoryInputLen:  gov.HistoryInputLen,
		Result:           gov.Turn,
		FacePolicy:       input.FacePolicy,
		UseMaterialFloor: input.UseMaterialFloor,
		MediaOnlyReply:   mediaOnlyReply,
		ReplyText:        replyText,
		FloorText:        gov.FloorText,
		MaterialFloor:    gov.MaterialFloor,
		FallbackOpts:     input.FallbackOptions,
		FaceAwareness:    faceAwareness,
		CurrentFaceModel: input.CurrentFaceModel,
		ReplyWithVoice:   input.ReplyWithVoice,
		AllowStream:      input.AllowStream,
		PromptInput:      input.PromptInput,
		Audit:            input.Audit,
	})
}

func (c *interactiveTurnCoordinator) Propose(ctx context.Context, req turn.FaceProposalRequest) (*turn.FaceProposalResult, error) {
	if c == nil || c.runtime == nil {
		return nil, fmt.Errorf("interactive coordinator unavailable")
	}

	awareness := req.Runtime
	awareness.ArtifactMode = "scene"
	proposal, usage, err := c.runtime.proposeCoordinatorFaceCommon(ctx, coordinatorProposalCommonInput{
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
	proposal, usage, err := c.runtime.proposeCoordinatorFaceCommon(context.Background(), coordinatorProposalCommonInput{
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
	fallbackOpts := face.FallbackOptions{
		Channel: c.requestChannel(),
		Voice:   c.replyWithVoice,
	}
	rendered, err := c.runtime.renderCoordinatorFaceCommon(ctx, coordinatorRenderCommonInput{
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

	output, err := c.runtime.executeCoordinatorTurnCommon(ctx, coordinatorExecuteCommonInput{
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

type durableGroupTurnCoordinator struct {
	runtime               *Runtime
	registered            core.DurableAgent
	livePolicy            core.DurableAgentLivePolicy
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
	allowStream           bool
	lastGovernor          *turn.GovernorResult
	lastFaceAwareness     prompt.RuntimeAwareness
	lastRenderedReply     string
}

func (c *durableGroupTurnCoordinator) Propose(ctx context.Context, req turn.FaceProposalRequest) (*turn.FaceProposalResult, error) {
	if c == nil || c.runtime == nil {
		return nil, fmt.Errorf("durable group coordinator unavailable")
	}

	awareness := req.Runtime
	awareness.ArtifactMode = "scene"
	proposal, usage, err := c.runtime.proposeCoordinatorFaceCommon(ctx, coordinatorProposalCommonInput{
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

func (c *durableGroupTurnCoordinator) requestFaceNote(mode string, awareness prompt.RuntimeAwareness, priorProposal string, feedback string) (string, core.TokenUsage, error) {
	if c == nil || c.runtime == nil {
		return "", core.TokenUsage{}, fmt.Errorf("durable group coordinator unavailable")
	}
	proposal, usage, err := c.runtime.proposeCoordinatorFaceCommon(context.Background(), coordinatorProposalCommonInput{
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
	fallbackOpts := face.FallbackOptions{Channel: c.requestChannel()}
	rendered, err := c.runtime.renderCoordinatorFaceCommon(ctx, coordinatorRenderCommonInput{
		Scope:                 c.scope,
		Msg:                   c.msg,
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
	c.lastRenderedReply = strings.TrimSpace(rendered.ReplyText)
	return &turn.FaceRenderResult{
		Text:         strings.TrimSpace(rendered.ReplyText),
		Usage:        rendered.Usage,
		Streamed:     rendered.StreamedReply,
		RenderedID:   rendered.OutboundID,
		RenderedType: rendered.OutboundType,
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

	output, err := c.runtime.executeCoordinatorTurnCommon(ctx, coordinatorExecuteCommonInput{
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
		RequestFaceNote:       c.requestFaceNote,
		ExtraSystemMessages: []agent.Message{
			{Role: "system", Content: durableGroupGovernorContext(c.registered, c.livePolicy, c.msg)},
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

type coordinatorExecuteCommonInput struct {
	Scope                 sandbox.Scope
	Msg                   core.InboundMessage
	Key                   session.SessionKey
	Sess                  *session.Session
	Prepared              pipeline.TurnPrepareContract
	Exec                  pipeline.TurnExecutionContract
	UseMaterialFloor      bool
	HiddenInputs          hiddenInputSet
	PromptContext         *workspace.PromptContext
	Tools                 agent.ToolRegistry
	BaseGovernorAwareness prompt.RuntimeAwareness
	Audit                 *turnAuditRecorder
	RunKind               session.TurnRunKind
	FaceNote              string
	Channel               string
	PrincipalRole         string
	GovernorName          string
	RequestFaceNote       func(mode string, awareness prompt.RuntimeAwareness, priorProposal string, feedback string) (string, core.TokenUsage, error)
	ExtraSystemMessages   []agent.Message
	RunErrPrefix          string
	InvalidOutputPrefix   string
}

type coordinatorExecuteCommonOutput struct {
	Sess              *session.Session
	GovernorResult    *turn.GovernorResult
	LastFaceAwareness prompt.RuntimeAwareness
}

func (r *Runtime) executeCoordinatorTurnCommon(ctx context.Context, input coordinatorExecuteCommonInput) (coordinatorExecuteCommonOutput, error) {
	out := coordinatorExecuteCommonOutput{Sess: input.Sess}

	runKind := input.RunKind
	if runKind == "" {
		runKind = session.TurnRunKindInteractive
	}

	baseGovernorAwareness := input.BaseGovernorAwareness
	governorAwareness := baseGovernorAwareness
	brokerage := turnBrokerage{}
	extraUsage := core.TokenUsage{}
	if note := strings.TrimSpace(input.FaceNote); note != "" {
		brokerage.IdolumNote = note
		brokerage.Active = strings.TrimSpace(note) != ""
		if suggestedContract := pipeline.ParseExecutionContract(note); suggestedContract != nil {
			brokerage.Phase = brokeragePhaseName(brokerage.Active, "brokerage")
			brokerage.SuggestedExecutionContract = suggestedContract
		} else {
			brokerage.Phase = brokeragePhaseName(brokerage.Active, "proposal")
		}
	}

	if input.UseMaterialFloor {
		governorAwareness.ArtifactMode = "floor"
	}

	governorAwareness = turn.ApplyBrokerageAwareness(governorAwareness, brokerage.toTurnAwareness())
	governorPrompt := prompt.GovernorRequest{
		GovernorName:    input.GovernorName,
		GovernorBackend: input.Exec.Backend,
		PrincipalRole:   input.PrincipalRole,
		WorkspaceRoot:   input.Scope.WorkingRoot,
		ToolManifest:    toolManifest(input.Tools),
		Workspace:       input.PromptContext,
		Runtime:         governorAwareness,
	}
	systemBlocks := prompt.BuildGovernorPromptBlocks(governorPrompt)
	systemPrompt := prompt.RenderSystemBlocks(systemBlocks)
	input.Sess.SystemPrompt = systemPrompt

	sess, history, maybeErr := r.maybeCompactSession(ctx, input.Key, input.Sess, systemBlocks, input.Prepared.UserText, brokerage.IdolumNote)
	if maybeErr != nil {
		return out, fmt.Errorf("maybe compact session: %w", maybeErr)
	}
	out.Sess = sess

	if brokerage.Active && brokerage.Phase == "brokerage" && strings.TrimSpace(brokerage.IdolumNote) != "" && input.RequestFaceNote != nil {
		updated, usage := r.convergeTurnBrokerage(ctx, input.Exec, baseGovernorAwareness, systemBlocks, history, input.Prepared.UserText, brokerage, input.RequestFaceNote, input.Audit)
		extraUsage = addTokenUsage(extraUsage, usage)
		brokerage = updated
		if brokerage.Phase == "brokerage" && strings.TrimSpace(brokerage.Ratification) == "accept" {
			sess.PlanState = maybeSeedPlanFromBrokerage(sess.PlanState, brokerage)
		}
		governorAwareness = turn.ApplyOperationAwareness(
			turn.ApplyPlanAwareness(
				turn.ApplyBrokerageAwareness(baseGovernorAwareness, brokerage.toTurnAwareness()),
				sess.PlanState,
			),
			sess.OperationState,
		)
		governorPrompt.Runtime = governorAwareness
		systemBlocks = prompt.BuildGovernorPromptBlocks(governorPrompt)
		systemPrompt = prompt.RenderSystemBlocks(systemBlocks)
		sess.SystemPrompt = systemPrompt
	}

	progress := r.newToolProgressReporter(input.Msg, sess.PlanState, input.Audit)
	monitor := r.startTurnMonitor(input.Key, runKind, input.Prepared.LedgerText, progress, input.Audit)
	var monitorErr error
	defer monitor.Finish(ctx, monitorErr)
	tools := monitor.observeTools(input.Tools)

	systemCount := 1
	if strings.TrimSpace(systemPrompt) == "" {
		systemCount = 0
	}
	turnInput := make([]agent.Message, 0, len(history)+2+len(input.ExtraSystemMessages)+systemCount)
	if systemPrompt != "" {
		turnInput = append(turnInput, agent.Message{Role: "system", Content: systemPrompt, SystemBlocks: systemBlocks})
	}
	turnInput = append(turnInput, input.ExtraSystemMessages...)
	if advisory := brokerageContextForGovernor(brokerage); advisory != "" {
		turnInput = append(turnInput, agent.Message{Role: "system", Content: advisory})
	}
	turnInput = append(turnInput, history...)
	turnInput = append(turnInput, agent.Message{Role: "user", Content: input.Prepared.UserText, Media: input.Prepared.AgentMedia})

	turnResult, outHistory, runErr := agent.RunTurn(ctx, input.Exec.Provider, tools, &agent.Budget{
		Max:     r.cfg.Agent.MaxIterations,
		Caution: 0.7,
		Warning: 0.9,
	}, r.reasoningOptionsForRun(runKind), turnInput)
	if runErr != nil {
		monitorErr = fmt.Errorf("%s: %w", firstNonEmpty(strings.TrimSpace(input.RunErrPrefix), "run turn"), runErr)
		return out, monitorErr
	}
	if len(outHistory) < len(turnInput) {
		monitorErr = fmt.Errorf("%s: history shrank from %d to %d", firstNonEmpty(strings.TrimSpace(input.InvalidOutputPrefix), "invalid turn output"), len(turnInput), len(outHistory))
		return out, monitorErr
	}

	turnResult.Text, turnResult.Media = extractOutboundReplyMedia(input.Scope, turnResult.Text, turnResult.Media)
	if input.Audit != nil {
		input.Audit.RecordGovernorReply(turnResult.Text, turnResult.Media)
	}
	sess.TurnCount++

	mediaOnlyReply := len(turnResult.Media) > 0 && strings.TrimSpace(turnResult.Text) == ""
	materialFloor := core.MaterialPacket{}
	floorText := ""
	if !mediaOnlyReply {
		materialFloor, floorText, _ = pipeline.BuildFloorFromGovernor(turnResult.Text, input.UseMaterialFloor)
	}
	floorMetadataState := input.HiddenInputs.Metadata()
	floorMetadataState.Artifacts = append(floorMetadataState.Artifacts, input.Prepared.ArtifactRefs...)
	floorMetadata := encodeFloorMetadata(floorMetadataState)

	if operationState, operationErr := r.store.OperationState(input.Key); operationErr == nil {
		sess.OperationState = mergeSessionOperationState(sess.OperationState, operationState)
	} else {
		monitorErr = fmt.Errorf("load operation state before save: %w", operationErr)
		return out, monitorErr
	}

	out.LastFaceAwareness = turn.ApplyOperationAwareness(
		turn.ApplyBrokerageAwareness(
			r.governorRuntimeAwareness(input.Scope, runKind, input.Channel, input.Exec),
			brokerage.toTurnAwareness(),
		),
		sess.OperationState,
	)

	governorResult := &turn.GovernorResult{
		Turn:            turnResult,
		OutHistory:      outHistory,
		HistoryInputLen: len(turnInput),
		FloorText:       floorText,
		FloorMetadata:   floorMetadata,
		MaterialFloor:   materialFloor,
		PlanState:       sess.PlanState,
		OperationState:  sess.OperationState,
		Prepared:        input.Prepared,
		Usage:           extraUsage,
	}
	turnResult.TokenUsage = addTokenUsage(turnResult.TokenUsage, extraUsage)

	out.Sess = sess
	out.GovernorResult = governorResult
	return out, nil
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
	return prompt.DefaultGovernorName
}

func (c *durableGroupTurnCoordinator) coordinatorFaceName() string {
	if c == nil {
		return face.DefaultFaceName
	}
	if trimmed := strings.TrimSpace(c.faceName); trimmed != "" {
		return trimmed
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
