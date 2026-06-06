//go:build linux

package runtime

import (
	"context"
	"fmt"
	"strings"
	"time"

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
	Key                   session.SessionKey
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
	r.markSessionTurnPhase(input.Key, "render", "authoring visible scene from governor floor")
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
		Key:              input.Key,
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
	FaceName              string
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
		ToolManifest:     toolManifestForRunKind(input.Tools, input.RunKind),
		ToolCapabilities: toolCapabilitiesForRunKind(input.Tools, input.RunKind),
		Workspace:        input.PromptContext,
		Runtime:          governorAwareness,
		CacheStrategy:    r.promptCacheStrategyForExecution(input.Exec),
	}
	systemBlocks := prompt.BuildGovernorPromptBlocks(governorPrompt)
	systemPrompt := prompt.RenderSystemBlocks(systemBlocks)
	return turnCoordinatorPromptState{
		GovernorAwareness: governorAwareness,
		SystemBlocks:      systemBlocks,
		SystemPrompt:      systemPrompt,
	}
}

func suppressTurnCoordinatorProviderOperationalAlert(input turnCoordinatorExecuteInput) bool {
	// Scheduled-review durable-agent turns have their own parent-visible
	// failure artifact/backoff lane. Keep provider attempt execution events,
	// but do not also emit a generic provider System warning for the same
	// handled scheduled-review wake failure. Other durable-agent channels still
	// use the generic provider operational-alert path.
	return strings.TrimSpace(input.PrincipalRole) == "durable_agent" && strings.TrimSpace(input.Channel) == scheduledReviewChannelKind
}

func (r *Runtime) promptCacheStrategyForExecution(exec pipeline.TurnExecutionContract) string {
	if r == nil || r.cfg == nil {
		return ""
	}
	if strings.EqualFold(strings.TrimSpace(exec.ProviderName), "anthropic") {
		return r.cfg.Providers.Anthropic.CacheStrategy
	}
	return ""
}

func (r *Runtime) executeTurnCoordinator(ctx context.Context, input turnCoordinatorExecuteInput) (turnCoordinatorExecuteOutput, error) {
	out := turnCoordinatorExecuteOutput{Sess: input.Sess}
	monitorErr := error(nil)

	runKind := input.RunKind
	if runKind == "" {
		runKind = session.TurnRunKindInteractive
	}

	progress := r.newToolProgressReporter(input.Key, input.Msg, input.Audit)
	monitor, err := r.startTurnMonitor(ctx, input.Key, runKind, input.Prepared.LedgerText, progress, input.Audit, input.Msg)
	if err != nil {
		return out, err
	}
	ctx = monitor.Context()
	defer monitor.Finish(ctx, monitorErr)

	baseGovernorAwareness := input.BaseGovernorAwareness
	initialSurface, faceNote := extractDeliberationSurfaceMarkdown(input.FaceNote)
	if progress != nil && initialSurface != "" {
		progress.Surface(ctx, initialSurface)
	}
	brokerage := seedTurnBrokerageFromFaceNote(faceNote)
	extraUsage := core.TokenUsage{}
	if brokerage.Active {
		r.markSessionTurnPhase(input.Key, "brokerage", "converging proposal and ratification before governor execution")
	}
	promptState := r.buildTurnCoordinatorGovernorPrompt(input, baseGovernorAwareness, brokerage)
	systemBlocks := promptState.SystemBlocks
	systemPrompt := promptState.SystemPrompt
	input.Sess.SystemPrompt = systemPrompt

	sess, history, maybeErr := r.maybeCompactSession(ctx, input.Key, input.Sess, systemBlocks, input.Prepared.UserText, brokerage.IdolumNote)
	if maybeErr != nil {
		monitorErr = fmt.Errorf("maybe compact session: %w", maybeErr)
		return out, monitorErr
	}
	out.Sess = sess

	requestFaceNote := input.RequestFaceNote
	if requestFaceNote != nil {
		requestFaceNote = func(ctx context.Context, mode string, awareness prompt.RuntimeAwareness, priorProposal string, feedback string) (string, core.TokenUsage, error) {
			note, usage, err := input.RequestFaceNote(ctx, mode, awareness, priorProposal, feedback)
			if err != nil {
				return note, usage, err
			}
			surface, cleaned := extractDeliberationSurfaceMarkdown(note)
			if progress != nil && surface != "" {
				progress.Surface(ctx, surface)
			}
			return cleaned, usage, nil
		}
	}

	if brokerage.Active && brokerage.Phase == "brokerage" && strings.TrimSpace(brokerage.IdolumNote) != "" && requestFaceNote != nil {
		updated, usage := r.convergeTurnBrokerage(
			ctx,
			input.Exec,
			baseGovernorAwareness,
			systemBlocks,
			history,
			input.Prepared.UserText,
			brokerage,
			requestFaceNote,
			input.Audit,
			func(ctx context.Context, text string) {
				if progress != nil {
					progress.Surface(ctx, text)
				}
			},
		)
		extraUsage = addTokenUsage(extraUsage, usage)
		brokerage = updated
		if brokerage.Phase == "brokerage" && strings.TrimSpace(brokerage.Ratification) == "accept" {
			sess.PlanState = maybeSeedPlanFromBrokerage(sess.PlanState, brokerage)
		}
		governorAwareness := turn.ApplyOperationAwareness(
			turn.ApplyPlanAwareness(baseGovernorAwareness, sess.PlanState),
			sess.OperationState,
		)
		governorAwareness = turn.ApplyContinuationAwareness(governorAwareness, sess.ContinuationState)
		promptState = r.buildTurnCoordinatorGovernorPrompt(input, governorAwareness, brokerage)
		systemBlocks = promptState.SystemBlocks
		systemPrompt = promptState.SystemPrompt
		sess.SystemPrompt = systemPrompt
	}

	tools := monitor.observeTools(input.Tools)
	r.markSessionTurnPhase(input.Key, "governor", "running governor and tool loop")

	systemCount := 1
	if strings.TrimSpace(systemPrompt) == "" {
		systemCount = 0
	}
	extraSystemMessages := append([]agent.Message(nil), input.ExtraSystemMessages...)
	if recall := r.maybeAggressivePrefetchSystemMessage(ctx, input.Scope, runKind, input.Prepared.LedgerText, time.Now().UTC()); strings.TrimSpace(recall) != "" {
		extraSystemMessages = append(extraSystemMessages, agent.Message{Role: "system", Content: recall})
	}

	turnInput := make([]agent.Message, 0, len(history)+2+len(extraSystemMessages)+systemCount)
	if systemPrompt != "" {
		turnInput = append(turnInput, agent.Message{Role: "system", Content: systemPrompt, SystemBlocks: systemBlocks})
	}
	turnInput = append(turnInput, extraSystemMessages...)
	if advisory := brokerageContextForGovernor(input.FaceName, brokerage); advisory != "" {
		turnInput = append(turnInput, agent.Message{Role: "system", Content: advisory})
	}
	turnInput = append(turnInput, history...)
	turnInput = append(turnInput, agent.Message{Role: "user", Content: input.Prepared.UserText, Media: input.Prepared.AgentMedia})

	providerStarted := time.Now()
	perceptionBudget := buildTurnPerceptionBudgetContract(turnPerceptionBudgetInput{
		RunKind:       runKind,
		HiddenInputs:  input.HiddenInputs,
		PromptContext: input.PromptContext,
		SystemBlocks:  systemBlocks,
		ExtraSystem:   extraSystemMessages,
		History:       history,
		UserText:      input.Prepared.UserText,
		ArtifactRefs:  input.Prepared.ArtifactRefs,
	})
	providerAttemptPayload := mergePerceptionBudgetPayload(map[string]any{
		"backend":       strings.TrimSpace(input.Exec.Backend),
		"provider":      strings.TrimSpace(input.Exec.ProviderName),
		"model":         strings.TrimSpace(input.Exec.ModelName),
		"provider_path": strings.Join(input.Exec.ProviderPath, ","),
		"history_count": len(history),
		"tool_count":    len(toolManifestForRunKind(tools, runKind)),
	}, perceptionBudget)
	r.recordExecutionEvent(input.Key, core.ExecutionEventProviderAttemptStarted, "provider", "started", providerAttemptPayload, time.Now().UTC())

	runOpts := r.reasoningOptionsForRun(runKind)
	if runOpts == nil {
		runOpts = &agent.CompleteOptions{}
	}
	runOpts.Observer = monitor
	runOpts.ContextBudget = r.providerContextBudget()
	turnResult, outHistory, runErr := agent.RunTurn(ctx, input.Exec.Provider, tools, &agent.Budget{
		Max:     r.cfg.Agent.MaxIterations,
		Caution: 0.7,
		Warning: 0.9,
	}, runOpts, turnInput)
	if runErr != nil {
		failureKind := core.ProviderFailureKind(runErr)
		r.recordExecutionEvent(input.Key, core.ExecutionEventProviderAttemptFailed, "provider", "failed", map[string]any{
			"backend":              strings.TrimSpace(input.Exec.Backend),
			"provider":             strings.TrimSpace(input.Exec.ProviderName),
			"model":                strings.TrimSpace(input.Exec.ModelName),
			"error":                trimError(runErr.Error()),
			"failure_kind":         failureKind,
			"retryable":            core.ProviderFailureRetryable(failureKind),
			"failover_eligible":    core.ProviderFailureFailoverEligible(failureKind),
			"provider_duration_ms": durationMillis(time.Since(providerStarted)),
		}, time.Now().UTC())
		if !suppressTurnCoordinatorProviderOperationalAlert(input) {
			r.reportOperationalIssueAsync("provider", runErr)
		}
		monitorErr = fmt.Errorf("%s: %w", firstNonEmpty(strings.TrimSpace(input.RunErrPrefix), "run turn"), runErr)
		return out, monitorErr
	}
	r.recordProviderAttemptEvents(input.Key, input.Exec, turnResult)
	if turnResult != nil {
		r.warnProviderFailovers(ctx, input.Key, turnResult.ProviderEvents)
	}
	if turnResult != nil && strings.TrimSpace(turnResult.ProviderFailure) != "" {
		providerFailure := strings.TrimSpace(turnResult.ProviderFailure)
		failureKind := core.ProviderFailureKind(fmt.Errorf("%s", providerFailure))
		r.recordExecutionEvent(input.Key, core.ExecutionEventProviderAttemptFailed, "provider", "failed", map[string]any{
			"backend":              strings.TrimSpace(input.Exec.Backend),
			"provider":             strings.TrimSpace(input.Exec.ProviderName),
			"model":                strings.TrimSpace(input.Exec.ModelName),
			"error":                trimError(providerFailure),
			"failure_kind":         failureKind,
			"retryable":            core.ProviderFailureRetryable(failureKind),
			"failover_eligible":    core.ProviderFailureFailoverEligible(failureKind),
			"provider_duration_ms": durationMillis(time.Since(providerStarted)),
		}, time.Now().UTC())
		if !suppressTurnCoordinatorProviderOperationalAlert(input) {
			r.reportOperationalIssueAsync("provider", fmt.Errorf("%s", strings.TrimSpace(turnResult.ProviderFailure)))
		}
	} else {
		providerName := strings.TrimSpace(input.Exec.ProviderName)
		if turnResult != nil {
			providerName = providerNameAfterProviderEvents(providerName, turnResult.ProviderEvents)
		}
		payload := map[string]any{
			"backend":              strings.TrimSpace(input.Exec.Backend),
			"provider":             providerName,
			"model":                strings.TrimSpace(input.Exec.ModelName),
			"provider_duration_ms": durationMillis(time.Since(providerStarted)),
		}
		if turnResult != nil {
			appendTokenUsagePayload(payload, turnResult.TokenUsage)
		}
		r.recordExecutionEvent(input.Key, core.ExecutionEventProviderAttemptSucceeded, "provider", "succeeded", payload, time.Now().UTC())
	}
	if len(outHistory) < len(turnInput) {
		monitorErr = fmt.Errorf("%s: history shrank from %d to %d", firstNonEmpty(strings.TrimSpace(input.InvalidOutputPrefix), "invalid turn output"), len(turnInput), len(outHistory))
		return out, monitorErr
	}

	turnResult.Media, monitorErr = materializeGeneratedReplyMedia(input.Scope, turnResult.Media)
	if monitorErr != nil {
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
	out.LastFaceAwareness = turn.ApplyContinuationAwareness(out.LastFaceAwareness, sess.ContinuationState)

	personaIntent, _ := parseContinuationIntentContract(brokerage.IdolumNote)
	governorIntent, _ := parseContinuationIntentContract(brokerage.RatificationRecord)

	governorResult := &turn.GovernorResult{
		Turn:            turnResult,
		OutHistory:      outHistory,
		HistoryInputLen: len(turnInput),
		FloorText:       floorText,
		FloorMetadata:   floorMetadata,
		MaterialFloor:   materialFloor,
		PlanState:       sess.PlanState,
		OperationState:  sess.OperationState,
		PersonaIntent:   personaIntent,
		GovernorIntent:  governorIntent,
		Prepared:        input.Prepared,
		Usage:           extraUsage,
	}
	turnResult.TokenUsage = addTokenUsage(turnResult.TokenUsage, extraUsage)

	out.Sess = sess
	out.GovernorResult = governorResult
	return out, nil
}
