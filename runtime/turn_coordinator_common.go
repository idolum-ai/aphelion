//go:build linux

package runtime

import (
	"context"
	"fmt"
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

type turnCoordinatorProposalInput struct {
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

func (r *Runtime) proposeTurnCoordinatorFace(ctx context.Context, input turnCoordinatorProposalInput) (string, core.TokenUsage, error) {
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

type turnCoordinatorRenderInput struct {
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
	FallbackOptions       pipeline.FallbackOptions
}

func (r *Runtime) renderTurnCoordinatorFace(ctx context.Context, input turnCoordinatorRenderInput) (turnRenderResult, error) {
	if input.LastGovernor == nil || input.LastGovernor.Turn == nil {
		return turnRenderResult{}, nil
	}
	gov := input.LastGovernor
	mediaOnlyReply := len(gov.Turn.Media) > 0 && strings.TrimSpace(gov.Turn.Text) == ""
	replyText := ""
	if !mediaOnlyReply {
		replyText = pipeline.SerializeFloorFallback(gov.MaterialFloor, gov.FloorText, input.FallbackOptions)
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

type turnCoordinatorExecuteInput struct {
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
	RequestFaceNote       func(ctx context.Context, mode string, awareness prompt.RuntimeAwareness, priorProposal string, feedback string) (string, core.TokenUsage, error)
	ExtraSystemMessages   []agent.Message
	RunErrPrefix          string
	InvalidOutputPrefix   string
}

type turnCoordinatorExecuteOutput struct {
	Sess              *session.Session
	GovernorResult    *turn.GovernorResult
	LastFaceAwareness prompt.RuntimeAwareness
}

type turnCoordinatorPromptState struct {
	GovernorAwareness prompt.RuntimeAwareness
	SystemBlocks      []agent.SystemBlock
	SystemPrompt      string
}

func (r *Runtime) buildTurnCoordinatorGovernorPrompt(input turnCoordinatorExecuteInput, baseAwareness prompt.RuntimeAwareness, brokerage turnBrokerage) turnCoordinatorPromptState {
	governorAwareness := baseAwareness
	if input.UseMaterialFloor {
		governorAwareness.ArtifactMode = "floor"
	}
	governorAwareness = turn.ApplyBrokerageAwareness(governorAwareness, brokerage.toTurnAwareness())
	governorPrompt := prompt.GovernorRequest{
		GovernorName:     input.GovernorName,
		GovernorBackend:  input.Exec.Backend,
		PrincipalRole:    input.PrincipalRole,
		WorkspaceRoot:    input.Scope.WorkingRoot,
		ToolManifest:     toolManifest(input.Tools),
		ToolCapabilities: toolCapabilities(input.Tools),
		Workspace:        input.PromptContext,
		Runtime:          governorAwareness,
	}
	systemBlocks := prompt.BuildGovernorPromptBlocks(governorPrompt)
	systemPrompt := prompt.RenderSystemBlocks(systemBlocks)
	return turnCoordinatorPromptState{
		GovernorAwareness: governorAwareness,
		SystemBlocks:      systemBlocks,
		SystemPrompt:      systemPrompt,
	}
}

func (r *Runtime) executeTurnCoordinator(ctx context.Context, input turnCoordinatorExecuteInput) (turnCoordinatorExecuteOutput, error) {
	out := turnCoordinatorExecuteOutput{Sess: input.Sess}

	runKind := input.RunKind
	if runKind == "" {
		runKind = session.TurnRunKindInteractive
	}

	baseGovernorAwareness := input.BaseGovernorAwareness
	brokerage := seedTurnBrokerageFromFaceNote(input.FaceNote)
	extraUsage := core.TokenUsage{}
	promptState := r.buildTurnCoordinatorGovernorPrompt(input, baseGovernorAwareness, brokerage)
	systemBlocks := promptState.SystemBlocks
	systemPrompt := promptState.SystemPrompt
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
		governorAwareness := turn.ApplyOperationAwareness(
			turn.ApplyPlanAwareness(baseGovernorAwareness, sess.PlanState),
			sess.OperationState,
		)
		promptState = r.buildTurnCoordinatorGovernorPrompt(input, governorAwareness, brokerage)
		systemBlocks = promptState.SystemBlocks
		systemPrompt = promptState.SystemPrompt
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
