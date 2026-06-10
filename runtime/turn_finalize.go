//go:build linux

package runtime

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/agent"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/face"
	memstore "github.com/idolum-ai/aphelion/memory"
	"github.com/idolum-ai/aphelion/pipeline"
	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/prompt"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/tool/sandbox"
	"github.com/idolum-ai/aphelion/turn"
)

type turnRenderInput struct {
	Ctx              context.Context
	Scope            sandbox.Scope
	Key              session.SessionKey
	Msg              core.InboundMessage
	Channel          string
	PrincipalRole    string
	OutHistory       []agent.Message
	HistoryInputLen  int
	Result           *core.TurnResult
	FacePolicy       pipeline.FacePolicy
	UseMaterialFloor bool
	MediaOnlyReply   bool
	ReplyText        string
	FloorText        string
	MaterialFloor    core.MaterialPacket
	FallbackOpts     pipeline.FallbackOptions
	FaceAwareness    prompt.RuntimeAwareness
	CurrentFaceModel face.Renderer
	ReplyWithVoice   bool
	AllowStream      bool
	PromptInput      string
	Audit            *turnAuditRecorder
}

type turnRenderResult struct {
	ReplyText     string
	StreamedReply bool
	OutboundID    int64
	OutboundType  string
	ReplyModality string
	Usage         core.TokenUsage
}

func (r *Runtime) renderTurnReply(input turnRenderInput) (turnRenderResult, error) {
	output := turnRenderResult{ReplyText: strings.TrimSpace(input.ReplyText)}
	if input.Result == nil {
		return output, nil
	}
	if recovery, ok := turnResultBudgetRecovery(input.Result); ok {
		output.ReplyText = turnBudgetRecoveryHandoffText(recovery)
		r.recordExecutionEvent(input.Key, core.ExecutionEventFaceRenderSkipped, "face", "skipped", map[string]any{
			"reason":        "budget_recovery",
			"recovery_kind": string(recovery.Kind),
		}, time.Now().UTC())
		return output, nil
	}

	output.ReplyText = strings.TrimSpace(input.ReplyText)
	if len(input.OutHistory) < input.HistoryInputLen {
		return output, nil
	}
	generatedMessages := input.OutHistory[input.HistoryInputLen:]
	workspaceRoot := faceWorkspaceRoot(input.Scope)
	structuralSkip, structuralSkipReason := r.structuralFaceRenderSkip(input)
	conditionalSkipReason := ""
	if !structuralSkip {
		conditionalSkipReason = faceConditionalSkipReason(input)
	}
	stageResult, err := turn.RunRenderStage(input.Ctx, turn.RenderStageRequest{
		Render: turn.FaceRenderRequest{
			GovernorName:    r.governorName(),
			FaceName:        r.faceName(),
			Channel:         input.Channel,
			PrincipalRole:   input.PrincipalRole,
			WorkspaceRoot:   workspaceRoot,
			FloorText:       input.FloorText,
			MaterialFloor:   input.MaterialFloor,
			LatestUserInput: input.PromptInput,
			Runtime:         input.FaceAwareness,
		},
		FacePolicy:            input.FacePolicy,
		UseMaterialFloor:      input.UseMaterialFloor,
		ReplyWithVoice:        input.ReplyWithVoice,
		AllowStream:           input.AllowStream && !r.personaContextRequestEligible(input),
		Media:                 input.Result.Media,
		ToolLog:               input.Result.ToolLog,
		GeneratedMessages:     generatedMessages,
		InitialReply:          output.ReplyText,
		FallbackOptions:       input.FallbackOpts,
		SkipRender:            structuralSkip,
		SkipRenderReason:      structuralSkipReason,
		ConditionalSkipReason: conditionalSkipReason,
	}, turn.RenderStageCallbacks{
		Stream: func(ctx context.Context, req turn.FaceRenderRequest) (turn.FaceRenderResult, bool, error) {
			streamer, ok := input.CurrentFaceModel.(face.StreamRenderer)
			if !ok {
				return turn.FaceRenderResult{}, false, nil
			}
			editor := r.newStreamEditor(input.Msg)
			if editor == nil {
				return turn.FaceRenderResult{}, false, nil
			}
			streamID := ""
			streamCtx := ctx
			streamCancel := context.CancelFunc(nil)
			if editor.keyboardEditor != nil {
				streamID = r.beginStreamControl(input.Msg.ChatID)
				streamCtx, streamCancel = context.WithCancel(ctx)
				r.attachStreamControlCancel(streamID, streamCancel)
				editor.controlRows = streamStopControlRows(streamID)
				editor.onMessageID = func(messageID int64) {
					r.attachStreamControlMessage(streamID, messageID)
					if input.Msg.TelegramThreadID > 0 && r.store != nil {
						if err := r.store.RecordTelegramCallbackMessageThread(input.Msg.ChatID, messageID, input.Msg.TelegramThreadID, "stream_control", time.Now().UTC()); err != nil {
							log.Printf("WARN record stream callback thread failed chat_id=%d thread_id=%d message_id=%d err=%v", input.Msg.ChatID, input.Msg.TelegramThreadID, messageID, err)
						}
					}
				}
				defer r.finishStreamControl(streamID)
			}
			if streamCancel != nil {
				defer streamCancel()
			}
			faceReq := face.RenderRequest{
				GovernorName:    req.GovernorName,
				FaceName:        req.FaceName,
				Channel:         req.Channel,
				Style:           req.Style,
				PrincipalRole:   req.PrincipalRole,
				WorkspaceRoot:   req.WorkspaceRoot,
				FloorText:       req.FloorText,
				MaterialFloor:   req.MaterialFloor,
				LatestUserInput: req.LatestUserInput,
				Runtime:         req.Runtime,
			}
			renderedReply, streamErr := streamer.RenderStream(streamCtx, faceReq, func(chunk string) error {
				return editor.OnChunk(streamCtx, chunk)
			})
			if streamErr != nil {
				if errors.Is(streamErr, context.Canceled) {
					cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
					defer cancel()
					outboundID, finishErr := editor.FinishStopped(cleanupCtx)
					if finishErr != nil {
						log.Printf("WARN finish stopped streamed reply backend=%s err=%v", r.faceBackend, finishErr)
					}
					renderedText := strings.TrimSpace(renderedReply)
					if renderedText == "" {
						renderedText = "Stopped."
					}
					renderedType := ""
					if outboundID != 0 {
						renderedType = "streaming"
					}
					return turn.FaceRenderResult{
						Text:         renderedText,
						Usage:        consumeFaceUsage(input.CurrentFaceModel),
						Streamed:     true,
						RenderedID:   outboundID,
						RenderedType: renderedType,
					}, true, nil
				}
				editor.Abort(ctx)
				log.Printf("WARN face stream render failed backend=%s err=%v; falling back to non-stream render", r.faceBackend, streamErr)
				return turn.FaceRenderResult{}, false, nil
			}
			outboundID, finishErr := editor.Finish(ctx)
			if finishErr != nil {
				return turn.FaceRenderResult{}, false, fmt.Errorf("finish streamed reply: %w", finishErr)
			}
			renderedType := ""
			if outboundID != 0 {
				renderedType = "streaming"
			}
			return turn.FaceRenderResult{
				Text:         strings.TrimSpace(renderedReply),
				Usage:        consumeFaceUsage(input.CurrentFaceModel),
				Streamed:     true,
				RenderedID:   outboundID,
				RenderedType: renderedType,
			}, true, nil
		},
		Render: func(ctx context.Context, req turn.FaceRenderRequest) (*turn.FaceRenderResult, error) {
			faceReq := face.RenderRequest{
				GovernorName:    req.GovernorName,
				FaceName:        req.FaceName,
				Channel:         req.Channel,
				Style:           req.Style,
				PrincipalRole:   req.PrincipalRole,
				WorkspaceRoot:   req.WorkspaceRoot,
				FloorText:       req.FloorText,
				MaterialFloor:   req.MaterialFloor,
				LatestUserInput: req.LatestUserInput,
				Runtime:         req.Runtime,
			}
			renderedReply, renderErr := input.CurrentFaceModel.Render(ctx, faceReq)
			if renderErr != nil {
				return nil, renderErr
			}
			return &turn.FaceRenderResult{
				Text:  strings.TrimSpace(renderedReply),
				Usage: consumeFaceUsage(input.CurrentFaceModel),
			}, nil
		},
		Fallback: pipeline.SerializeFloorFallback,
	})
	if err != nil {
		return output, err
	}
	if stageResult.RenderError != nil {
		log.Printf("WARN face render failed backend=%s err=%v; using floor_fallback serializer", r.faceBackend, stageResult.RenderError)
	}
	if strings.TrimSpace(string(stageResult.SkipReason)) != "" && shouldRecordFaceSkipEvent(input.Key) {
		r.recordExecutionEvent(input.Key, core.ExecutionEventFaceRenderSkipped, "render", "skipped", faceSkipPayload(string(stageResult.SkipReason), input, stageResult.ReplyText), time.Now().UTC())
	}
	output.ReplyText = strings.TrimSpace(stageResult.ReplyText)
	output.ReplyModality = strings.TrimSpace(stageResult.ReplyModality)
	output.Usage = addTokenUsage(output.Usage, stageResult.Usage)
	output.StreamedReply = stageResult.Streamed
	output.OutboundID = stageResult.RenderedID
	output.OutboundType = stageResult.RenderedType
	if query, ok := extractPersonaContextRequest(output.ReplyText); ok {
		notes := r.fulfillPersonaContextRequest(input.Ctx, input.Scope, firstNonEmpty(strings.TrimSpace(query), strings.TrimSpace(input.PromptInput)), time.Now().UTC())
		if len(notes) > 0 {
			renderedReply, usage, renderErr := r.renderFaceWithRequestedContext(input.Ctx, input, stageResult.Runtime, notes)
			if renderErr != nil {
				log.Printf("WARN face context retry failed backend=%s err=%v; using floor fallback", r.faceBackend, renderErr)
				output.ReplyText = pipeline.FloorTextOrFallback(input.FloorText)
			} else {
				output.ReplyText = strings.TrimSpace(renderedReply)
				output.Usage = addTokenUsage(output.Usage, usage)
				output.StreamedReply = false
				output.OutboundID = 0
				output.OutboundType = ""
			}
		} else {
			output.ReplyText = pipeline.FloorTextOrFallback(input.FloorText)
		}
	}

	output.ReplyText = r.applyTurnConstitution(
		input.Ctx,
		input.Key,
		input.Scope,
		input.Channel,
		input.PrincipalRole,
		input.PromptInput,
		input.CurrentFaceModel,
		stageResult.Runtime,
		input.MaterialFloor,
		input.FloorText,
		output.ReplyText,
		input.Result.Media,
		input.Audit,
	)
	var directiveModality string
	output.ReplyText, directiveModality = extractReplyModalityDirective(output.ReplyText)
	if directiveModality != "" {
		output.ReplyModality = directiveModality
	}
	output.ReplyText = enforceVisibleRecurrenceContract(output.ReplyText, stageResult.Runtime)

	if stageResult.FallbackApplied && output.StreamedReply && output.OutboundID != 0 {
		if err := r.reconcileStreamedFallback(input.Ctx, input.Key, input.Msg, output.OutboundID, output.ReplyText, stageResult.FallbackReason); err != nil {
			log.Printf("WARN streamed face fallback reconciliation failed backend=%s message_id=%d reason=%s err=%v; forcing normal delivery", r.faceBackend, output.OutboundID, stageResult.FallbackReason, err)
			r.recordExecutionEvent(input.Key, core.ExecutionEventStreamFallbackReconcileFail, "render", "failed", map[string]any{
				"message_id": output.OutboundID,
				"reason":     strings.TrimSpace(stageResult.FallbackReason),
				"error":      trimError(err.Error()),
			}, time.Now().UTC())
			output.StreamedReply = false
			output.OutboundID = 0
			output.OutboundType = ""
		} else {
			r.recordExecutionEvent(input.Key, core.ExecutionEventStreamFallbackReconciled, "render", "edited", map[string]any{
				"message_id": output.OutboundID,
				"reason":     strings.TrimSpace(stageResult.FallbackReason),
				"text_chars": len([]rune(strings.TrimSpace(output.ReplyText))),
			}, time.Now().UTC())
		}
	}

	return output, nil
}

func (r *Runtime) reconcileStreamedFallback(ctx context.Context, key session.SessionKey, msg core.InboundMessage, messageID int64, text string, reason string) error {
	if r == nil || r.outbound == nil {
		return fmt.Errorf("outbound sender unavailable")
	}
	if messageID == 0 {
		return fmt.Errorf("streamed message id is missing")
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return fmt.Errorf("fallback text is empty")
	}
	visibleText := r.prefixTelegramPresentedText(r.telegramPresentationForMessage(msg), text)
	if clearer, ok := r.outbound.(messageKeyboardClearer); ok {
		return clearer.EditMessageTextWithoutInlineKeyboard(ctx, msg.ChatID, messageID, visibleText, "")
	}
	if editor, ok := r.outbound.(messageEditor); ok {
		return editor.EditMessageText(ctx, msg.ChatID, messageID, visibleText, "")
	}
	return fmt.Errorf("outbound sender cannot edit streamed message")
}

func enforceVisibleRecurrenceContract(reply string, aw prompt.RuntimeAwareness) string {
	reply = strings.TrimSpace(reply)
	note := visibleRecurrenceNote(aw)
	if note == "" || reply == "" {
		return reply
	}
	lower := strings.ToLower(reply)
	if strings.Contains(lower, "continuity note:") || (strings.Contains(lower, "prior") && strings.Contains(lower, "thread")) {
		return reply
	}
	return strings.TrimSpace(reply + "\n\n" + note)
}

const personaContextRequestPrefix = "PERSONA_CONTEXT_REQUEST:"

func extractPersonaContextRequest(reply string) (string, bool) {
	reply = strings.TrimSpace(reply)
	if reply == "" {
		return "", false
	}
	for _, line := range strings.Split(reply, "\n") {
		line = strings.TrimSpace(strings.Trim(line, "`"))
		if line == "" {
			continue
		}
		if !strings.HasPrefix(line, personaContextRequestPrefix) {
			return "", false
		}
		return strings.TrimSpace(strings.TrimPrefix(line, personaContextRequestPrefix)), true
	}
	return "", false
}

func (r *Runtime) personaContextRequestEligible(input turnRenderInput) bool {
	if r == nil || r.semantic == nil || !r.semantic.Enabled() || input.CurrentFaceModel == nil {
		return false
	}
	if input.MediaOnlyReply || input.Result == nil || strings.TrimSpace(input.Result.ProviderFailure) != "" {
		return false
	}
	return input.FaceAwareness.HiddenInputsActive &&
		runtimeAwarenessHasAnyHiddenCategory(input.FaceAwareness, hiddenInputSemanticRecurrence, hiddenInputUnresolvedMemory)
}

func (r *Runtime) fulfillPersonaContextRequest(ctx context.Context, scope sandbox.Scope, query string, now time.Time) []string {
	if r == nil || r.semantic == nil || !r.semantic.Enabled() {
		return nil
	}
	query = strings.TrimSpace(query)
	if query == "" {
		return nil
	}
	semanticScope, principalID := splitSemanticScope(semanticScopeForPrincipal(scope.Principal))
	hits, err := r.semantic.Search(ctx, memstore.SemanticSearchRequest{
		Root:        dynamicPromptRoot(scope),
		Scope:       semanticScope,
		PrincipalID: principalID,
		Query:       query,
		Mode:        memstore.SemanticModeInteractive,
		Limit:       3,
		MaxLen:      2400,
		Now:         now,
	})
	if err != nil || len(hits) == 0 {
		return nil
	}
	notes := make([]string, 0, len(hits))
	for _, hit := range hits {
		excerpt := compactPersonaContextExcerpt(hit.Excerpt)
		if excerpt == "" {
			continue
		}
		source := humanizePersonaContextSource(hit.Source, hit.Kind)
		if source != "" {
			notes = append(notes, source+": "+excerpt)
		} else {
			notes = append(notes, excerpt)
		}
	}
	return notes
}

func compactPersonaContextExcerpt(text string) string {
	text = strings.Join(strings.Fields(strings.TrimSpace(text)), " ")
	if text == "" {
		return ""
	}
	return truncateRunes(text, 420)
}

func humanizePersonaContextSource(source string, kind string) string {
	source = strings.TrimSpace(source)
	kind = strings.TrimSpace(kind)
	base := strings.TrimSpace(strings.TrimSuffix(strings.TrimSuffix(source, ".md"), ".markdown"))
	base = strings.Trim(strings.ReplaceAll(base, "\\", "/"), "/")
	if idx := strings.LastIndex(base, "/"); idx >= 0 {
		base = base[idx+1:]
	}
	base = strings.ReplaceAll(base, "_", " ")
	base = strings.ReplaceAll(base, "-", " ")
	base = strings.TrimSpace(base)
	switch strings.ToLower(base) {
	case "", "memory", "knowledge", "decisions", "questions":
		if kind != "" {
			return "prior " + strings.ReplaceAll(kind, "_", " ")
		}
		return "prior context"
	default:
		return "prior " + base
	}
}

func (r *Runtime) renderFaceWithRequestedContext(ctx context.Context, input turnRenderInput, aw prompt.RuntimeAwareness, notes []string) (string, core.TokenUsage, error) {
	if input.CurrentFaceModel == nil {
		return "", core.TokenUsage{}, fmt.Errorf("face model unavailable")
	}
	faceReq := face.RenderRequest{
		GovernorName:    r.governorName(),
		FaceName:        r.faceName(),
		Channel:         input.Channel,
		Style:           "",
		PrincipalRole:   input.PrincipalRole,
		WorkspaceRoot:   faceWorkspaceRoot(input.Scope),
		FloorText:       input.FloorText,
		MaterialFloor:   input.MaterialFloor,
		LatestUserInput: input.PromptInput,
		ContextNotes:    append([]string(nil), notes...),
		Runtime:         aw,
	}
	rendered, err := input.CurrentFaceModel.Render(ctx, faceReq)
	if err != nil {
		return "", core.TokenUsage{}, err
	}
	rendered = strings.TrimSpace(rendered)
	if rendered == "" {
		return "", core.TokenUsage{}, face.ErrEmptyRender
	}
	return rendered, consumeFaceUsage(input.CurrentFaceModel), nil
}

func visibleRecurrenceNote(aw prompt.RuntimeAwareness) string {
	if !aw.HiddenInputsActive || !runtimeAwarenessHasAnyHiddenCategory(aw, hiddenInputSemanticRecurrence, hiddenInputUnresolvedMemory) {
		return ""
	}
	if summary, ok := sanitizeVisibleRecurrenceSummary(aw.ProvenanceSummary); ok {
		return "Continuity note: This resembles " + summary
	}
	return ""
}

func sanitizeVisibleRecurrenceSummary(summary string) (string, bool) {
	summary = strings.TrimSpace(summary)
	if summary == "" {
		return "", false
	}
	lower := strings.ToLower(summary)
	for _, internal := range []string{
		"memory.md",
		"memory/",
		"decisions.md",
		"knowledge.md",
		"questions.md",
		"related prior material",
		"hidden input",
		"open question overlaps",
		"surfacing again",
	} {
		if strings.Contains(lower, internal) {
			return "", false
		}
	}
	for _, sep := range []string{";", "\n"} {
		if idx := strings.Index(summary, sep); idx >= 0 {
			summary = strings.TrimSpace(summary[:idx])
		}
	}
	summary = strings.Trim(summary, " .\t\r\n")
	if summary == "" {
		return "", false
	}
	if !visibleRecurrenceSummaryNamesThread(summary) {
		return "", false
	}
	return clampContinuationText(summary, 220) + ".", true
}

func visibleRecurrenceSummaryNamesThread(summary string) bool {
	lower := strings.ToLower(strings.TrimSpace(summary))
	return strings.Contains(lower, "thread") ||
		strings.Contains(lower, "conversation") ||
		strings.Contains(lower, "mission") ||
		strings.Contains(lower, "lighthouse") ||
		strings.Contains(lower, "proton") ||
		strings.Contains(lower, "recurring")
}

func runtimeAwarenessHasAnyHiddenCategory(aw prompt.RuntimeAwareness, categories ...string) bool {
	wanted := make(map[string]struct{}, len(categories))
	for _, category := range categories {
		category = strings.TrimSpace(category)
		if category != "" {
			wanted[category] = struct{}{}
		}
	}
	for _, category := range aw.HiddenInputCategories {
		if _, ok := wanted[strings.TrimSpace(category)]; ok {
			return true
		}
	}
	return false
}

type turnCommitInput struct {
	Key             session.SessionKey
	RunID           int64
	Scope           sandbox.Scope
	RunKind         session.TurnRunKind
	Sess            *session.Session
	Msg             core.InboundMessage
	Actor           principal.Principal
	Prepared        pipeline.TurnPrepareContract
	OutHistory      []agent.Message
	HistoryInputLen int
	Result          *core.TurnResult
	// FloorText is the governor floor sidecar captured for audit/provenance and
	// future recovery; it is distinct from the visible scene text.
	FloorText string
	// FloorMetadata carries machine-readable sidecar provenance (hidden inputs,
	// signal judgments, retained artifacts) and stays separate from scene text.
	FloorMetadata string
	// ReplyText is the visible assistant scene text that gets delivered/persisted
	// as transcript content for this turn.
	ReplyText      string
	StreamedReply  bool
	OutboundID     int64
	OutboundType   string
	RecordOutbound bool
	Audit          *turnAuditRecorder
	Hooks          turnCommitHooks
	ErrCtx         turnCommitErrorContext
}

type turnCommitHooks struct {
	QueueReviewEvents       func(result *turn.Result) error
	DeliverReviewEvents     func(result *turn.Result) error
	QueueDurableArtifact    func(result *turn.Result) error
	PostReplyContinuationUI func(ctx context.Context, result *turn.Result) error
}

type turnCommitErrorContext struct {
	ConvertMessages  string
	LoadPlanState    string
	LoadOperation    string
	LoadContinuation string
	SaveSession      string
	RecordOutbound   string
}

type turnPersistencePort struct {
	runtime      *Runtime
	key          session.SessionKey
	scope        sandbox.Scope
	sess         *session.Session
	sessionState interface {
		session() *session.Session
	}
	runIDSource interface {
		turnRunID() int64
	}
	msg    core.InboundMessage
	actor  principal.Principal
	errCtx turnCommitErrorContext
	audit  *turnAuditRecorder
}

func (p *turnPersistencePort) Persist(ctx context.Context, req turn.CommitRequest) (*turn.CommitResult, error) {
	if p == nil || p.runtime == nil {
		return nil, fmt.Errorf("turn persistence port is unavailable")
	}
	p.runtime.markSessionTurnPhase(p.key, "persist", "writing turn result and sidecars to durable storage")
	if req.Result == nil {
		return nil, fmt.Errorf("turn persistence request missing result")
	}
	sess := p.currentSession()
	if sess == nil {
		return nil, fmt.Errorf("turn persistence session unavailable")
	}
	result, err := p.runtime.persistTurn(ctx, turnCommitInput{
		Key:             p.key,
		RunID:           p.currentRunID(),
		Scope:           p.scope,
		RunKind:         req.Request.RunKind,
		Sess:            sess,
		Msg:             p.msg,
		Actor:           p.actor,
		Prepared:        req.Result.Prepared,
		OutHistory:      req.Result.OutHistory,
		HistoryInputLen: req.Result.HistoryInputLen,
		Result:          req.Result.Turn,
		FloorText:       req.Result.FloorText,
		FloorMetadata:   req.Result.FloorMetadata,
		ReplyText:       req.Result.VisibleReply,
		StreamedReply:   req.Result.RenderedStream,
		OutboundID:      req.Result.RenderedID,
		OutboundType:    req.Result.RenderedType,
		RecordOutbound:  false,
		Audit:           p.audit,
		ErrCtx:          p.errCtx,
	})
	if err != nil {
		return nil, err
	}
	return &turn.CommitResult{Persisted: result.Committed}, nil
}

func (p *turnPersistencePort) currentSession() *session.Session {
	if p == nil {
		return nil
	}
	if p.sessionState != nil {
		if sess := p.sessionState.session(); sess != nil {
			return sess
		}
	}
	return p.sess
}

func (p *turnPersistencePort) currentRunID() int64 {
	if p == nil {
		return 0
	}
	if p.runIDSource != nil {
		if id := p.runIDSource.turnRunID(); id != 0 {
			return id
		}
	}
	return 0
}

type turnDeliveryPort struct {
	runtime      *Runtime
	key          session.SessionKey
	sess         *session.Session
	sessionState interface {
		session() *session.Session
	}
	runIDSource interface {
		turnRunID() int64
	}
	msg                                   core.InboundMessage
	inboundWasVoice                       bool
	deliver                               bool
	recordOutbound                        bool
	hooks                                 turnCommitHooks
	audit                                 *turnAuditRecorder
	sendErrCtx                            string
	recordErrCtx                          string
	deliveryMsgIDs                        []int64
	deferBudgetRecoveryToWorkFailureRetry bool
}

func (p *turnDeliveryPort) Deliver(ctx context.Context, req turn.DeliveryRequest) (*turn.DeliveryResult, error) {
	if p == nil || p.runtime == nil {
		return nil, fmt.Errorf("turn delivery port is unavailable")
	}
	p.runtime.markSessionTurnPhase(p.key, "deliver", "sending or finalizing outbound delivery")
	if _, ok := turnResultBudgetRecoveryFromTurnResult(req.Result); ok {
		return p.deliverBudgetRecovery(ctx, req)
	}
	return turn.RunDeliveryStage(ctx, turn.DeliveryStageInput{
		Request:        req,
		Deliver:        p.deliver,
		RecordOutbound: p.recordOutbound,
	}, turn.DeliveryStageCallbacks{
		Send: func(ctx context.Context, msg core.OutboundMessage, replyWithVoice bool) (int64, string, error) {
			outboundID, outboundType, messageIDs, err := p.runtime.sendReplyWithDelivery(ctx, p.msg, msg.Text, msg.Media, replyWithVoice)
			p.deliveryMsgIDs = messageIDs
			if err != nil {
				p.runtime.recordExecutionEvent(p.key, core.ExecutionEventDeliveryFinalFailed, "delivery", "failed", map[string]any{
					"error": trimError(err.Error()),
				}, time.Now().UTC())
				if p.sendErrCtx == "" {
					return 0, "", err
				}
				return 0, "", fmt.Errorf("%s: %w", p.sendErrCtx, err)
			}
			payload := map[string]any{
				"message_id": outboundID,
				"kind":       strings.TrimSpace(outboundType),
			}
			if len(messageIDs) > 0 {
				payload["message_ids"] = append([]int64(nil), messageIDs...)
				payload["chunk_count"] = len(messageIDs)
			}
			p.runtime.recordExecutionEvent(p.key, core.ExecutionEventDeliveryFinalSent, "delivery", "sent", payload, time.Now().UTC())
			return outboundID, outboundType, nil
		},
		RecordFinal: func(text string, media []core.Media, kind string) {
			if p.audit != nil {
				p.audit.RecordFinalReply(text, media, kind)
			}
		},
		RecordOutbound: func(ctx context.Context, messageID int64, kind string) error {
			return p.recordOutboundWithContext(ctx, p.currentSession(), p.key, messageID, kind)
		},
		PostCommit: func(postCtx context.Context) error {
			return p.runPostCommitHooks(postCtx, req.Result)
		},
	})
}

func (p *turnDeliveryPort) runPostCommitHooks(ctx context.Context, result *turn.Result) error {
	if p == nil {
		return nil
	}
	if p.hooks.QueueReviewEvents != nil {
		if err := p.hooks.QueueReviewEvents(result); err != nil {
			return err
		}
	}
	if p.hooks.DeliverReviewEvents != nil {
		if err := p.hooks.DeliverReviewEvents(result); err != nil {
			return err
		}
	}
	if p.hooks.QueueDurableArtifact != nil {
		if err := p.hooks.QueueDurableArtifact(result); err != nil {
			return err
		}
	}
	if p.hooks.PostReplyContinuationUI != nil {
		if err := p.hooks.PostReplyContinuationUI(ctx, result); err != nil {
			return err
		}
	}
	return nil
}

func (p *turnDeliveryPort) currentSession() *session.Session {
	if p == nil {
		return nil
	}
	if p.sessionState != nil {
		if sess := p.sessionState.session(); sess != nil {
			return sess
		}
	}
	return p.sess
}

func (p *turnDeliveryPort) currentRunID() int64 {
	if p == nil || p.runIDSource == nil {
		return 0
	}
	return p.runIDSource.turnRunID()
}

func (p *turnDeliveryPort) recordOutboundWithContext(_ context.Context, sess *session.Session, key session.SessionKey, outboundID int64, outboundType string) error {
	if sess == nil {
		return fmt.Errorf("turn delivery post-processing missing session")
	}
	if p.recordErrCtx == "" {
		p.recordErrCtx = "record outbound reply"
	}
	messageIDs := p.deliveryMsgIDs
	if len(messageIDs) == 0 {
		messageIDs = []int64{outboundID}
	}
	for _, messageID := range messageIDs {
		if messageID == 0 {
			continue
		}
		if err := p.runtime.store.RecordOutbound(key, sess.TurnCount, messageID, outboundType); err != nil {
			return fmt.Errorf("%s: %w", p.recordErrCtx, err)
		}
		if p.msg.TelegramThreadID > 0 && strings.TrimSpace(p.msg.DurableAgentID) == "" {
			if err := p.runtime.store.RecordTelegramThreadLastMessage(p.msg.ChatID, p.msg.TelegramThreadID, messageID, outboundType, time.Now().UTC()); err != nil {
				return fmt.Errorf("record telegram thread reply anchor: %w", err)
			}
		}
	}
	return nil
}

type turnCommitResult struct {
	OutboundID   int64
	OutboundType string
	Committed    bool
}

func (r *Runtime) persistTurn(ctx context.Context, input turnCommitInput) (turnCommitResult, error) {
	out := turnCommitResult{
		OutboundID:   input.OutboundID,
		OutboundType: input.OutboundType,
	}
	usage := core.TokenUsage{}
	if input.Result != nil {
		usage = input.Result.TokenUsage
	}
	sceneText := strings.TrimSpace(input.ReplyText)
	floorText := strings.TrimSpace(input.FloorText)
	floorMetadata := strings.TrimSpace(input.FloorMetadata)

	stageResult, err := turn.RunPersistStage(ctx, turn.PersistStageInput{
		LedgerText:      input.Prepared.LedgerText,
		OutHistory:      input.OutHistory,
		HistoryInputLen: input.HistoryInputLen,
		Session:         input.Sess,
		ReplyText:       sceneText,
		FloorText:       floorText,
		FloorMetadata:   floorMetadata,
		Usage:           usage,
		ErrorContext: turn.PersistStageErrorContext{
			ConvertMessages:  input.ErrCtx.ConvertMessages,
			LoadPlanState:    input.ErrCtx.LoadPlanState,
			LoadOperation:    input.ErrCtx.LoadOperation,
			LoadContinuation: input.ErrCtx.LoadContinuation,
			SaveSession:      input.ErrCtx.SaveSession,
		},
	}, turn.PersistStageCallbacks{
		BuildMessages: func(ledgerText string, generated []agent.Message, turnIndex int) ([]session.Message, error) {
			msgCtx := session.TurnMessageContext{
				ActorUserID:       input.Actor.TelegramUserID,
				ActorRole:         string(input.Actor.Role),
				EventOrigin:       inboundOriginLabel(input.Msg),
				EventOriginDetail: inboundOriginDetailLabel(input.Msg),
			}
			return session.NewMessagesForTurnWithContext(ledgerText, generated, turnIndex, msgCtx)
		},
		ApplyScene: replaceLastAssistantWithSceneText,
		ApplyFloor: func(messages []session.Message, floorText string, floorMetadata string) []session.Message {
			messages = setLastAssistantFloor(messages, floorText)
			return setLastAssistantFloorMetadata(messages, floorMetadata)
		},
		LoadPlanState: func(context.Context) (session.PlanState, error) {
			return r.store.PlanState(input.Key)
		},
		MergePlanState: mergeSessionPlanState,
		LoadOperationState: func(context.Context) (session.OperationState, error) {
			return r.store.OperationState(input.Key)
		},
		MergeOperationState: mergeSessionOperationState,
		LoadContinuationState: func(context.Context) (session.ContinuationState, error) {
			return r.store.ContinuationState(input.Key)
		},
		MergeContinuationState: mergeSessionContinuationState,
		Save: func(_ context.Context, sess *session.Session, newMessages []session.Message, usage core.TokenUsage) error {
			return r.store.Save(sess, newMessages, usage)
		},
	})
	if err != nil {
		return out, err
	}
	out.Committed = stageResult.Committed
	if out.Committed && input.RunID != 0 {
		if err := r.store.UpdateTurnRunAccounting(input.RunID, input.Sess.TurnCount, stageResult.NewMessages, usage); err != nil {
			return out, err
		}
	}
	if out.Committed && input.Audit != nil && input.Result != nil {
		input.Audit.RecordFinalReply(sceneText, input.Result.Media, out.OutboundType)
	}
	if out.Committed && input.Sess != nil {
		r.recordTurnSidecarsCapturedEvent(input.Key, input.Sess)
	}
	if out.Committed && input.Result != nil {
		r.maybeCaptureTurnMemory(ctx, input)
	}
	return out, nil
}

func (r *Runtime) recordTurnSidecarsCapturedEvent(key session.SessionKey, sess *session.Session) {
	if r == nil || sess == nil {
		return
	}
	opStatus, opStage, opSummary := operationStatusFields(sess.OperationState)
	phaseID, phaseStatus, phaseSummary, phaseCompleted, phaseTotal, phasePlanActive := operationPhasePlanStatusFields(sess.OperationState)
	planStepStatus, planStep := planStatusFields(sess.PlanState)
	planCompleted, planTotal, planFullyExecuted := planProgressFields(sess.PlanState)
	hiddenCategories, hiddenSummary := hiddenInputStatusFields(sess.LastFloorMetadata)
	payload := map[string]any{
		"operation_status":        strings.TrimSpace(opStatus),
		"operation_stage":         strings.TrimSpace(opStage),
		"operation_summary":       strings.TrimSpace(opSummary),
		"phase_plan_active":       phasePlanActive,
		"phase_current_id":        strings.TrimSpace(phaseID),
		"phase_current_status":    strings.TrimSpace(phaseStatus),
		"phase_current_summary":   strings.TrimSpace(phaseSummary),
		"phase_completed":         phaseCompleted,
		"phase_total":             phaseTotal,
		"plan_step_status":        strings.TrimSpace(planStepStatus),
		"plan_step":               strings.TrimSpace(planStep),
		"plan_completed_steps":    planCompleted,
		"plan_total_steps":        planTotal,
		"plan_fully_executed":     planFullyExecuted,
		"hidden_input_summary":    strings.TrimSpace(hiddenSummary),
		"hidden_input_categories": hiddenCategories,
		"hidden_input_category":   strings.Join(hiddenCategories, ","),
	}
	r.recordExecutionEvent(
		key,
		core.ExecutionEventTurnSidecarsCaptured,
		"persist",
		"captured",
		payload,
		time.Now().UTC(),
	)
}
