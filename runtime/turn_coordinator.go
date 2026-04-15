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
	proposer, ok := c.currentFaceModel.(face.Proposer)
	if c.currentFaceModel == nil || !ok || c.runtime.faceBackend == face.BackendFloorFallback {
		return &turn.FaceProposalResult{}, nil
	}

	awareness := req.Runtime
	awareness.ArtifactMode = "scene"
	mode := strings.TrimSpace(req.Mode)
	if mode == "" {
		mode = "proposal"
	}
	proposal, err := proposer.Propose(ctx, face.ProposalRequest{
		GovernorName:      req.GovernorName,
		FaceName:          req.FaceName,
		Channel:           req.Channel,
		Mode:              mode,
		PrincipalRole:     req.PrincipalRole,
		WorkspaceRoot:     faceWorkspaceRoot(c.scope),
		LatestUserInput:   req.LatestUserInput,
		PriorProposal:     "",
		BrokerageFeedback: "",
		Runtime:           awareness,
	})
	if err != nil {
		log.Printf("WARN idolum proposal failed backend=%s principal=%s err=%v", c.runtime.faceBackend, c.actor.Role, err)
		return &turn.FaceProposalResult{}, nil
	}
	return &turn.FaceProposalResult{
		Note:  strings.TrimSpace(proposal),
		Usage: consumeFaceUsage(c.currentFaceModel),
	}, nil
}

func (c *interactiveTurnCoordinator) requestFaceNote(mode string, awareness prompt.RuntimeAwareness, priorProposal string, feedback string) (string, core.TokenUsage, error) {
	if c == nil || c.runtime == nil {
		return "", core.TokenUsage{}, fmt.Errorf("interactive coordinator unavailable")
	}
	proposer, ok := c.currentFaceModel.(face.Proposer)
	if c.currentFaceModel == nil || !ok || c.runtime.faceBackend == face.BackendFloorFallback {
		return "", core.TokenUsage{}, nil
	}

	proposal, err := proposer.Propose(context.Background(), face.ProposalRequest{
		GovernorName:      c.coordinatorGovernorName(),
		FaceName:          c.coordinatorFaceName(),
		Channel:           c.requestChannel(),
		Mode:              strings.TrimSpace(mode),
		PrincipalRole:     c.principalRoleOrActor(),
		WorkspaceRoot:     faceWorkspaceRoot(c.scope),
		LatestUserInput:   c.prepared.LedgerText,
		PriorProposal:     priorProposal,
		BrokerageFeedback: feedback,
		Runtime:           awareness,
	})
	if err != nil {
		log.Printf("WARN idolum proposal failed backend=%s principal=%s err=%v", c.runtime.faceBackend, c.actor.Role, err)
		return "", core.TokenUsage{}, err
	}
	return strings.TrimSpace(proposal), consumeFaceUsage(c.currentFaceModel), nil
}

func (c *interactiveTurnCoordinator) Render(ctx context.Context, req turn.FaceRenderRequest) (*turn.FaceRenderResult, error) {
	if c == nil || c.runtime == nil {
		return nil, fmt.Errorf("interactive coordinator unavailable")
	}
	if c.lastGovernor == nil || c.lastGovernor.Turn == nil {
		return &turn.FaceRenderResult{}, nil
	}
	gov := c.lastGovernor
	mediaOnlyReply := len(gov.Turn.Media) > 0 && strings.TrimSpace(gov.Turn.Text) == ""
	replyText := ""
	fallbackOpts := face.FallbackOptions{
		Channel: c.requestChannel(),
		Voice:   c.replyWithVoice,
	}
	if !mediaOnlyReply {
		replyText = face.SerializeFloorFallback(gov.MaterialFloor, gov.FloorText, fallbackOpts)
	}
	faceAwareness := c.lastFaceAwareness
	if strings.TrimSpace(faceAwareness.DeliveryMode) == "" {
		faceAwareness = c.baseGovernorAwareness
	}
	rendered, err := c.runtime.renderTurnReply(turnRenderInput{
		Ctx:              ctx,
		Scope:            c.scope,
		Msg:              c.msg,
		Channel:          c.requestChannel(),
		PrincipalRole:    c.principalRoleOrActor(),
		OutHistory:       gov.OutHistory,
		HistoryInputLen:  gov.HistoryInputLen,
		Result:           gov.Turn,
		FacePolicy:       c.facePolicy,
		UseMaterialFloor: c.useMaterialFloor,
		MediaOnlyReply:   mediaOnlyReply,
		ReplyText:        replyText,
		FloorText:        gov.FloorText,
		MaterialFloor:    gov.MaterialFloor,
		FallbackOpts:     fallbackOpts,
		FaceAwareness:    faceAwareness,
		CurrentFaceModel: c.currentFaceModel,
		ReplyWithVoice:   c.replyWithVoice,
		AllowStream:      true,
		PromptInput:      c.prepared.LedgerText,
		Audit:            c.audit,
	})
	if err != nil {
		return nil, err
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
	channel := c.requestChannel()
	principalRole := c.principalRoleOrActor()
	governorName := c.coordinatorGovernorName()

	baseGovernorAwareness := c.baseGovernorAwareness
	governorAwareness := baseGovernorAwareness
	brokerage := turnBrokerage{}
	extraUsage := core.TokenUsage{}
	if note := strings.TrimSpace(req.FaceNote); note != "" {
		brokerage.IdolumNote = note
		brokerage.Active = strings.TrimSpace(note) != ""
		if suggestedContract := pipeline.ParseExecutionContract(note); suggestedContract != nil {
			brokerage.Phase = brokeragePhaseName(brokerage.Active, "brokerage")
			brokerage.SuggestedExecutionContract = suggestedContract
		} else {
			brokerage.Phase = brokeragePhaseName(brokerage.Active, "proposal")
		}
	}

	if c.useMaterialFloor {
		governorAwareness.ArtifactMode = "floor"
	}
	c.sess.ChatType = "dm"
	c.sess.UserName = c.msg.SenderName

	governorAwareness = turn.ApplyBrokerageAwareness(governorAwareness, brokerage.toTurnAwareness())
	governorPrompt := prompt.GovernorRequest{
		GovernorName:    governorName,
		GovernorBackend: c.exec.Backend,
		PrincipalRole:   principalRole,
		WorkspaceRoot:   c.scope.WorkingRoot,
		ToolManifest:    toolManifest(c.tools),
		Workspace:       c.promptContext,
		Runtime:         governorAwareness,
	}
	systemBlocks := prompt.BuildGovernorPromptBlocks(governorPrompt)
	systemPrompt := prompt.RenderSystemBlocks(systemBlocks)
	c.sess.SystemPrompt = systemPrompt

	sess, history, maybeErr := c.runtime.maybeCompactSession(ctx, c.key, c.sess, systemBlocks, c.prepared.UserText, brokerage.IdolumNote)
	if maybeErr != nil {
		return nil, fmt.Errorf("maybe compact session: %w", maybeErr)
	}
	c.sess = sess

	if brokerage.Active && brokerage.Phase == "brokerage" && strings.TrimSpace(brokerage.IdolumNote) != "" {
		updated, usage := c.runtime.convergeTurnBrokerage(ctx, c.exec, baseGovernorAwareness, systemBlocks, history, c.prepared.UserText, brokerage, c.requestFaceNote, c.audit)
		extraUsage = addTokenUsage(extraUsage, usage)
		brokerage = updated
		if brokerage.Phase == "brokerage" && strings.TrimSpace(brokerage.Ratification) == "accept" {
			c.sess.PlanState = maybeSeedPlanFromBrokerage(c.sess.PlanState, brokerage)
		}
		governorAwareness = turn.ApplyOperationAwareness(
			turn.ApplyPlanAwareness(
				turn.ApplyBrokerageAwareness(baseGovernorAwareness, brokerage.toTurnAwareness()),
				c.sess.PlanState,
			),
			c.sess.OperationState,
		)
		governorPrompt.Runtime = governorAwareness
		systemBlocks = prompt.BuildGovernorPromptBlocks(governorPrompt)
		systemPrompt = prompt.RenderSystemBlocks(systemBlocks)
		c.sess.SystemPrompt = systemPrompt
	}

	progress := c.runtime.newToolProgressReporter(c.msg, c.sess.PlanState, c.audit)
	monitor := c.runtime.startTurnMonitor(c.key, runKind, c.prepared.LedgerText, progress, c.audit)
	var monitorErr error
	defer monitor.Finish(ctx, monitorErr)
	tools := monitor.observeTools(c.tools)

	input := make([]agent.Message, 0, len(history)+2)
	if systemPrompt != "" {
		input = append(input, agent.Message{Role: "system", Content: systemPrompt, SystemBlocks: systemBlocks})
	}
	if advisory := brokerageContextForGovernor(brokerage); advisory != "" {
		input = append(input, agent.Message{Role: "system", Content: advisory})
	}
	input = append(input, history...)
	input = append(input, agent.Message{Role: "user", Content: c.prepared.UserText, Media: c.prepared.AgentMedia})

	turnResult, outHistory, runErr := agent.RunTurn(ctx, c.exec.Provider, tools, &agent.Budget{
		Max:     c.runtime.cfg.Agent.MaxIterations,
		Caution: 0.7,
		Warning: 0.9,
	}, c.runtime.reasoningOptionsForRun(runKind), input)
	if runErr != nil {
		monitorErr = fmt.Errorf("run turn: %w", runErr)
		return nil, monitorErr
	}
	if len(outHistory) < len(input) {
		monitorErr = fmt.Errorf("invalid turn output: history shrank from %d to %d", len(input), len(outHistory))
		return nil, monitorErr
	}

	turnResult.Text, turnResult.Media = extractOutboundReplyMedia(c.scope, turnResult.Text, turnResult.Media)
	if c.audit != nil {
		c.audit.RecordGovernorReply(turnResult.Text, turnResult.Media)
	}
	c.sess.TurnCount++

	mediaOnlyReply := len(turnResult.Media) > 0 && strings.TrimSpace(turnResult.Text) == ""
	materialFloor := core.MaterialPacket{}
	floorText := ""
	if !mediaOnlyReply {
		materialFloor, floorText, _ = pipeline.BuildFloorFromGovernor(turnResult.Text, c.useMaterialFloor)
	}
	floorMetadataState := c.hiddenInputs.Metadata()
	floorMetadataState.Artifacts = append(floorMetadataState.Artifacts, c.prepared.ArtifactRefs...)
	floorMetadata := encodeFloorMetadata(floorMetadataState)

	if operationState, operationErr := c.runtime.store.OperationState(c.key); operationErr == nil {
		c.sess.OperationState = mergeSessionOperationState(c.sess.OperationState, operationState)
	} else {
		monitorErr = fmt.Errorf("load operation state before save: %w", operationErr)
		return nil, monitorErr
	}

	c.lastFaceAwareness = turn.ApplyOperationAwareness(
		turn.ApplyBrokerageAwareness(
			c.runtime.governorRuntimeAwareness(c.scope, runKind, channel, c.exec),
			brokerage.toTurnAwareness(),
		),
		c.sess.OperationState,
	)
	c.replyWithVoice = c.runtime.shouldReplyWithVoice(c.prepared.InboundWasVoice) && len(turnResult.Media) == 0

	governorResult := &turn.GovernorResult{
		Turn:            turnResult,
		OutHistory:      outHistory,
		HistoryInputLen: len(input),
		FloorText:       floorText,
		FloorMetadata:   floorMetadata,
		MaterialFloor:   materialFloor,
		PlanState:       c.sess.PlanState,
		OperationState:  c.sess.OperationState,
		Prepared:        c.prepared,
		Usage:           extraUsage,
	}
	c.lastGovernor = governorResult
	turnResult.TokenUsage = addTokenUsage(turnResult.TokenUsage, extraUsage)
	return governorResult, nil
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
	proposer, ok := c.currentFaceModel.(face.Proposer)
	if c.currentFaceModel == nil || !ok || c.runtime.faceBackend == face.BackendFloorFallback {
		return &turn.FaceProposalResult{}, nil
	}

	awareness := req.Runtime
	awareness.ArtifactMode = "scene"
	proposal, err := proposer.Propose(ctx, face.ProposalRequest{
		GovernorName:      req.GovernorName,
		FaceName:          req.FaceName,
		Channel:           req.Channel,
		Mode:              req.Mode,
		PrincipalRole:     req.PrincipalRole,
		WorkspaceRoot:     faceWorkspaceRoot(c.scope),
		LatestUserInput:   req.LatestUserInput,
		PriorProposal:     "",
		BrokerageFeedback: "",
		Runtime:           awareness,
	})
	if err != nil {
		log.Printf("WARN idolum proposal failed backend=%s durable_group=%s err=%v", c.runtime.faceBackend, c.registered.AgentID, err)
		return &turn.FaceProposalResult{}, nil
	}
	return &turn.FaceProposalResult{
		Note:  strings.TrimSpace(proposal),
		Usage: consumeFaceUsage(c.currentFaceModel),
	}, nil
}

func (c *durableGroupTurnCoordinator) requestFaceNote(mode string, awareness prompt.RuntimeAwareness, priorProposal string, feedback string) (string, core.TokenUsage, error) {
	if c == nil || c.runtime == nil {
		return "", core.TokenUsage{}, fmt.Errorf("durable group coordinator unavailable")
	}
	proposer, ok := c.currentFaceModel.(face.Proposer)
	if c.currentFaceModel == nil || !ok || c.runtime.faceBackend == face.BackendFloorFallback {
		return "", core.TokenUsage{}, nil
	}

	proposal, err := proposer.Propose(context.Background(), face.ProposalRequest{
		GovernorName:      c.coordinatorGovernorName(),
		FaceName:          c.coordinatorFaceName(),
		Channel:           c.requestChannel(),
		Mode:              strings.TrimSpace(mode),
		PrincipalRole:     c.principalRoleOrLiveRole(),
		WorkspaceRoot:     faceWorkspaceRoot(c.scope),
		LatestUserInput:   c.prepared.LedgerText,
		PriorProposal:     priorProposal,
		BrokerageFeedback: feedback,
		Runtime:           awareness,
	})
	if err != nil {
		log.Printf("WARN idolum proposal failed backend=%s durable_group=%s err=%v", c.runtime.faceBackend, c.registered.AgentID, err)
		return "", core.TokenUsage{}, err
	}
	return strings.TrimSpace(proposal), consumeFaceUsage(c.currentFaceModel), nil
}

func (c *durableGroupTurnCoordinator) Render(ctx context.Context, req turn.FaceRenderRequest) (*turn.FaceRenderResult, error) {
	if c == nil || c.runtime == nil {
		return nil, fmt.Errorf("durable group coordinator unavailable")
	}
	if c.lastGovernor == nil || c.lastGovernor.Turn == nil {
		return &turn.FaceRenderResult{}, nil
	}
	gov := c.lastGovernor
	mediaOnlyReply := len(gov.Turn.Media) > 0 && strings.TrimSpace(gov.Turn.Text) == ""
	replyText := ""
	fallbackOpts := face.FallbackOptions{Channel: c.requestChannel()}
	if !mediaOnlyReply {
		replyText = face.SerializeFloorFallback(gov.MaterialFloor, gov.FloorText, fallbackOpts)
	}
	faceAwareness := c.lastFaceAwareness
	if strings.TrimSpace(faceAwareness.DeliveryMode) == "" {
		faceAwareness = c.baseGovernorAwareness
	}
	rendered, err := c.runtime.renderTurnReply(turnRenderInput{
		Ctx:              ctx,
		Scope:            c.scope,
		Msg:              c.msg,
		Channel:          c.requestChannel(),
		PrincipalRole:    c.principalRoleOrLiveRole(),
		OutHistory:       gov.OutHistory,
		HistoryInputLen:  gov.HistoryInputLen,
		Result:           gov.Turn,
		FacePolicy:       c.facePolicy,
		UseMaterialFloor: c.useMaterialFloor,
		MediaOnlyReply:   mediaOnlyReply,
		ReplyText:        replyText,
		FloorText:        gov.FloorText,
		MaterialFloor:    gov.MaterialFloor,
		FallbackOpts:     fallbackOpts,
		FaceAwareness:    faceAwareness,
		CurrentFaceModel: c.currentFaceModel,
		ReplyWithVoice:   false,
		AllowStream:      c.allowStream,
		PromptInput:      c.prepared.LedgerText,
		Audit:            c.audit,
	})
	if err != nil {
		return nil, err
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
	channel := c.requestChannel()
	principalRole := c.principalRoleOrLiveRole()
	governorName := c.coordinatorGovernorName()

	baseGovernorAwareness := c.baseGovernorAwareness
	governorAwareness := baseGovernorAwareness
	brokerage := turnBrokerage{}
	extraUsage := core.TokenUsage{}
	if note := strings.TrimSpace(req.FaceNote); note != "" {
		brokerage.IdolumNote = note
		brokerage.Active = strings.TrimSpace(note) != ""
		if suggestedContract := pipeline.ParseExecutionContract(note); suggestedContract != nil {
			brokerage.Phase = brokeragePhaseName(brokerage.Active, "brokerage")
			brokerage.SuggestedExecutionContract = suggestedContract
		} else {
			brokerage.Phase = brokeragePhaseName(brokerage.Active, "proposal")
		}
	}
	if c.useMaterialFloor {
		governorAwareness.ArtifactMode = "floor"
	}
	c.sess.ChatType = firstNonEmpty(strings.TrimSpace(c.msg.ChatType), "group")
	c.sess.ChatTitle = strings.TrimSpace(c.msg.ChatTitle)
	c.sess.UserName = c.msg.SenderName

	governorAwareness = turn.ApplyBrokerageAwareness(governorAwareness, brokerage.toTurnAwareness())
	governorPrompt := prompt.GovernorRequest{
		GovernorName:    governorName,
		GovernorBackend: c.exec.Backend,
		PrincipalRole:   principalRole,
		WorkspaceRoot:   c.scope.WorkingRoot,
		ToolManifest:    toolManifest(c.tools),
		Workspace:       c.promptContext,
		Runtime:         governorAwareness,
	}
	systemBlocks := prompt.BuildGovernorPromptBlocks(governorPrompt)
	systemPrompt := prompt.RenderSystemBlocks(systemBlocks)
	c.sess.SystemPrompt = systemPrompt

	sess, history, maybeErr := c.runtime.maybeCompactSession(ctx, c.key, c.sess, systemBlocks, c.prepared.UserText, brokerage.IdolumNote)
	if maybeErr != nil {
		return nil, fmt.Errorf("maybe compact session: %w", maybeErr)
	}
	c.sess = sess

	if brokerage.Active && brokerage.Phase == "brokerage" && strings.TrimSpace(brokerage.IdolumNote) != "" {
		updated, usage := c.runtime.convergeTurnBrokerage(ctx, c.exec, baseGovernorAwareness, systemBlocks, history, c.prepared.UserText, brokerage, c.requestFaceNote, c.audit)
		extraUsage = addTokenUsage(extraUsage, usage)
		brokerage = updated
		if brokerage.Active && brokerage.Phase == "brokerage" && strings.TrimSpace(brokerage.Ratification) == "accept" {
			c.sess.PlanState = maybeSeedPlanFromBrokerage(c.sess.PlanState, brokerage)
		}
		governorAwareness = turn.ApplyOperationAwareness(
			turn.ApplyPlanAwareness(
				turn.ApplyBrokerageAwareness(baseGovernorAwareness, brokerage.toTurnAwareness()),
				c.sess.PlanState,
			),
			c.sess.OperationState,
		)
		governorPrompt.Runtime = governorAwareness
		systemBlocks = prompt.BuildGovernorPromptBlocks(governorPrompt)
		systemPrompt = prompt.RenderSystemBlocks(systemBlocks)
		c.sess.SystemPrompt = systemPrompt
	}

	progress := c.runtime.newToolProgressReporter(c.msg, c.sess.PlanState, c.audit)
	monitor := c.runtime.startTurnMonitor(c.key, runKind, c.prepared.LedgerText, progress, c.audit)
	var monitorErr error
	defer monitor.Finish(ctx, monitorErr)
	tools := monitor.observeTools(c.tools)

	input := make([]agent.Message, 0, len(history)+3)
	if systemPrompt != "" {
		input = append(input, agent.Message{Role: "system", Content: systemPrompt, SystemBlocks: systemBlocks})
	}
	input = append(input, agent.Message{Role: "system", Content: durableGroupGovernorContext(c.registered, c.livePolicy, c.msg)})
	if advisory := brokerageContextForGovernor(brokerage); advisory != "" {
		input = append(input, agent.Message{Role: "system", Content: advisory})
	}
	input = append(input, history...)
	input = append(input, agent.Message{Role: "user", Content: c.prepared.UserText, Media: c.prepared.AgentMedia})

	turnResult, outHistory, runErr := agent.RunTurn(ctx, c.exec.Provider, tools, &agent.Budget{
		Max:     c.runtime.cfg.Agent.MaxIterations,
		Caution: 0.7,
		Warning: 0.9,
	}, c.runtime.reasoningOptionsForRun(runKind), input)
	if runErr != nil {
		monitorErr = fmt.Errorf("run durable group turn: %w", runErr)
		return nil, monitorErr
	}
	if len(outHistory) < len(input) {
		monitorErr = fmt.Errorf("invalid durable group turn output: history shrank from %d to %d", len(input), len(outHistory))
		return nil, monitorErr
	}

	turnResult.Text, turnResult.Media = extractOutboundReplyMedia(c.scope, turnResult.Text, turnResult.Media)
	if c.audit != nil {
		c.audit.RecordGovernorReply(turnResult.Text, turnResult.Media)
	}
	c.sess.TurnCount++

	mediaOnlyReply := len(turnResult.Media) > 0 && strings.TrimSpace(turnResult.Text) == ""
	materialFloor := core.MaterialPacket{}
	floorText := ""
	if !mediaOnlyReply {
		materialFloor, floorText, _ = pipeline.BuildFloorFromGovernor(turnResult.Text, c.useMaterialFloor)
	}
	floorMetadataState := c.hiddenInputs.Metadata()
	floorMetadataState.Artifacts = append(floorMetadataState.Artifacts, c.prepared.ArtifactRefs...)
	floorMetadata := encodeFloorMetadata(floorMetadataState)

	if operationState, operationErr := c.runtime.store.OperationState(c.key); operationErr == nil {
		c.sess.OperationState = mergeSessionOperationState(c.sess.OperationState, operationState)
	} else {
		monitorErr = fmt.Errorf("load operation state before save: %w", operationErr)
		return nil, monitorErr
	}

	c.lastFaceAwareness = turn.ApplyOperationAwareness(
		turn.ApplyBrokerageAwareness(
			c.runtime.governorRuntimeAwareness(c.scope, runKind, channel, c.exec),
			brokerage.toTurnAwareness(),
		),
		c.sess.OperationState,
	)

	governorResult := &turn.GovernorResult{
		Turn:            turnResult,
		OutHistory:      outHistory,
		HistoryInputLen: len(input),
		FloorText:       floorText,
		FloorMetadata:   floorMetadata,
		MaterialFloor:   materialFloor,
		PlanState:       c.sess.PlanState,
		OperationState:  c.sess.OperationState,
		Prepared:        c.prepared,
		Usage:           extraUsage,
	}
	c.lastGovernor = governorResult
	turnResult.TokenUsage = addTokenUsage(turnResult.TokenUsage, extraUsage)
	return governorResult, nil
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
