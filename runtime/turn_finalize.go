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
)

type turnRenderInput struct {
	Ctx              context.Context
	Scope            sandbox.Scope
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
	FallbackOpts     face.FallbackOptions
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
	Usage         core.TokenUsage
}

func (r *Runtime) renderTurnReply(input turnRenderInput) (turnRenderResult, error) {
	output := turnRenderResult{ReplyText: strings.TrimSpace(input.ReplyText)}
	if input.Result == nil {
		return output, nil
	}

	output.ReplyText = input.ReplyText
	if len(input.OutHistory) < input.HistoryInputLen {
		return output, nil
	}
	generatedMessages := input.OutHistory[input.HistoryInputLen:]

	if input.FaceAwareness.DeliveryMode == "" {
		input.FaceAwareness.DeliveryMode = "text"
	}
	input.FaceAwareness.StreamReply = false
	input.FaceAwareness.ArtifactMode = "scene"

	if input.ReplyWithVoice {
		input.FaceAwareness.DeliveryMode = "voice"
	} else if input.FacePolicy.Render {
		input.FaceAwareness.DeliveryMode = "idolum_render"
	}

	if input.MediaOnlyReply || r.faceBackend == face.BackendFloorFallback || input.CurrentFaceModel == nil {
		output.ReplyText = strings.TrimSpace(output.ReplyText)
		output.ReplyText = r.applyTurnConstitution(
			input.Ctx,
			input.Scope,
			input.Channel,
			input.PrincipalRole,
			input.PromptInput,
			input.CurrentFaceModel,
			input.FaceAwareness,
			input.MaterialFloor,
			input.FloorText,
			output.ReplyText,
			input.Result.Media,
			input.Audit,
		)
		return output, nil
	}

	renderReq := face.RenderRequest{
		GovernorName:    prompt.DefaultGovernorName,
		FaceName:        face.DefaultFaceName,
		Channel:         input.Channel,
		PrincipalRole:   input.PrincipalRole,
		WorkspaceRoot:   faceWorkspaceRoot(input.Scope),
		FloorText:       input.FloorText,
		MaterialFloor:   input.MaterialFloor,
		LatestUserInput: input.PromptInput,
		Runtime:         input.FaceAwareness,
	}

	renderHeuristicText := input.FloorText
	if input.UseMaterialFloor {
		renderHeuristicText = pipeline.FormatFloorTextForRender(input.MaterialFloor, input.FloorText)
	}
	shouldRender := pipeline.ShouldRenderInteractiveIdolumReply(input.FacePolicy, pipeline.RenderDecisionInput{
		UserText:          input.PromptInput,
		FloorText:         renderHeuristicText,
		ToolLog:           input.Result.ToolLog,
		GeneratedMessages: generatedMessages,
	})
	if !shouldRender && !input.ReplyWithVoice {
		input.FaceAwareness.DeliveryMode = "floor_fallback"
		renderReq.Runtime = input.FaceAwareness
	}

	faceRendered := false
	if shouldRender && !input.ReplyWithVoice && input.AllowStream && len(input.Result.Media) == 0 {
		if streamer, ok := input.CurrentFaceModel.(face.StreamRenderer); ok {
			editor := r.newStreamEditor(input.Msg)
			if editor != nil {
				input.FaceAwareness.DeliveryMode = "stream"
				input.FaceAwareness.StreamReply = true
				renderReq.Runtime = input.FaceAwareness
				renderedReply, streamErr := streamer.RenderStream(input.Ctx, renderReq, func(chunk string) error {
					return editor.OnChunk(input.Ctx, chunk)
				})
				if streamErr != nil {
					editor.Abort(input.Ctx)
					log.Printf("WARN face stream render failed backend=%s err=%v; falling back to non-stream render", r.faceBackend, streamErr)
				} else {
					faceRendered = true
					output.ReplyText = strings.TrimSpace(renderedReply)
					if output.ReplyText == "" {
						output.ReplyText = face.SerializeFloorFallback(input.MaterialFloor, input.FloorText, input.FallbackOpts)
					}
					output.Usage = addTokenUsage(output.Usage, consumeFaceUsage(input.CurrentFaceModel))
					output.StreamedReply = true
					outboundID, err := editor.Finish(input.Ctx)
					if err != nil {
						return output, fmt.Errorf("finish streamed reply: %w", err)
					}
					output.OutboundID = outboundID
					if outboundID != 0 {
						output.OutboundType = "streaming"
					}
				}
			}
		}
	}

	if shouldRender && !faceRendered {
		if !input.ReplyWithVoice {
			input.FaceAwareness.DeliveryMode = "idolum_render"
			input.FaceAwareness.StreamReply = false
			renderReq.Runtime = input.FaceAwareness
		}
		renderedReply, renderErr := input.CurrentFaceModel.Render(input.Ctx, renderReq)
		if renderErr != nil {
			log.Printf("WARN face render failed backend=%s err=%v; using floor_fallback serializer", r.faceBackend, renderErr)
		} else {
			output.ReplyText = strings.TrimSpace(renderedReply)
			if output.ReplyText == "" {
				output.ReplyText = face.SerializeFloorFallback(input.MaterialFloor, input.FloorText, input.FallbackOpts)
			}
			output.Usage = addTokenUsage(output.Usage, consumeFaceUsage(input.CurrentFaceModel))
		}
	}

	output.ReplyText = r.applyTurnConstitution(
		input.Ctx,
		input.Scope,
		input.Channel,
		input.PrincipalRole,
		input.PromptInput,
		input.CurrentFaceModel,
		input.FaceAwareness,
		input.MaterialFloor,
		input.FloorText,
		output.ReplyText,
		input.Result.Media,
		input.Audit,
	)

	return output, nil
}

type turnCommitInput struct {
	Key             session.SessionKey
	Sess            *session.Session
	Prepared        pipeline.TurnPrepareContract
	OutHistory      []agent.Message
	HistoryInputLen int
	Result          *core.TurnResult
	FloorText       string
	FloorMetadata   string
	ReplyText       string
	StreamedReply   bool
	OutboundID      int64
	OutboundType    string
	RecordOutbound  bool
	Audit           *turnAuditRecorder
	Hooks           turnCommitHooks
	ErrCtx          turnCommitErrorContext
}

type turnCommitHooks struct {
	QueueReviewEvents    func() error
	DeliverReviewEvents  func() error
	QueueDurableArtifact func() error
}

type turnCommitErrorContext struct {
	ConvertMessages string
	LoadPlanState   string
	LoadOperation   string
	SaveSession     string
	RecordOutbound  string
}

type turnPersistencePort struct {
	runtime *Runtime
	key     session.SessionKey
	sess    *session.Session
	errCtx  turnCommitErrorContext
	audit   *turnAuditRecorder
}

func (p *turnPersistencePort) Persist(ctx context.Context, req turn.CommitRequest) (*turn.CommitResult, error) {
	if p == nil || p.runtime == nil {
		return nil, fmt.Errorf("turn persistence port is unavailable")
	}
	if req.Result == nil {
		return nil, fmt.Errorf("turn persistence request missing result")
	}
	result, err := p.runtime.persistTurn(ctx, turnCommitInput{
		Key:             p.key,
		Sess:            p.sess,
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

type turnDeliveryPort struct {
	runtime         *Runtime
	key             session.SessionKey
	sess            *session.Session
	msg             core.InboundMessage
	inboundWasVoice bool
	deliver         bool
	recordOutbound  bool
	hooks           turnCommitHooks
	audit           *turnAuditRecorder
	sendErrCtx      string
	recordErrCtx    string
}

func (p *turnDeliveryPort) Deliver(ctx context.Context, req turn.DeliveryRequest) (*turn.DeliveryResult, error) {
	if p == nil || p.runtime == nil {
		return nil, fmt.Errorf("turn delivery port is unavailable")
	}
	if req.Result == nil {
		return nil, nil
	}
	outboundID := req.Result.RenderedID
	outboundType := req.Result.RenderedType

	if !p.deliver || req.Result.RenderedStream {
		if p.audit != nil {
			p.audit.RecordFinalReply(req.Message.Text, req.Message.Media, outboundType)
		}
		if p.recordOutbound && outboundID != 0 {
			if err := p.recordOutboundWithContext(ctx, p.sess, p.key, outboundID, outboundType); err != nil {
				return nil, err
			}
		}
		if err := p.runPostCommitHooks(); err != nil {
			return nil, err
		}
		return &turn.DeliveryResult{
			MessageID: outboundID,
			Kind:      outboundType,
		}, nil
	}

	outboundID, outboundType, err := p.runtime.sendReply(ctx, p.msg, req.Message.Text, req.Message.Media, req.InboundWasVoice)
	if err != nil {
		if p.sendErrCtx == "" {
			return nil, err
		}
		return nil, fmt.Errorf("%s: %w", p.sendErrCtx, err)
	}
	if p.audit != nil {
		p.audit.RecordFinalReply(req.Message.Text, req.Message.Media, outboundType)
	}
	if p.recordOutbound {
		if err := p.recordOutboundWithContext(ctx, p.sess, p.key, outboundID, outboundType); err != nil {
			return nil, err
		}
	}
	if err := p.runPostCommitHooks(); err != nil {
		return nil, err
	}
	return &turn.DeliveryResult{MessageID: outboundID, Kind: outboundType}, nil
}

func (p *turnDeliveryPort) runPostCommitHooks() error {
	if p == nil {
		return nil
	}
	if p.hooks.QueueReviewEvents != nil {
		if err := p.hooks.QueueReviewEvents(); err != nil {
			return err
		}
	}
	if p.hooks.DeliverReviewEvents != nil {
		if err := p.hooks.DeliverReviewEvents(); err != nil {
			return err
		}
	}
	if p.hooks.QueueDurableArtifact != nil {
		if err := p.hooks.QueueDurableArtifact(); err != nil {
			return err
		}
	}
	return nil
}

func (p *turnDeliveryPort) recordOutboundWithContext(_ context.Context, sess *session.Session, key session.SessionKey, outboundID int64, outboundType string) error {
	if sess == nil {
		return fmt.Errorf("turn delivery post-processing missing session")
	}
	if p.recordErrCtx == "" {
		p.recordErrCtx = "record outbound reply"
	}
	if err := p.runtime.store.RecordOutbound(key, sess.TurnCount, outboundID, outboundType); err != nil {
		return fmt.Errorf("%s: %w", p.recordErrCtx, err)
	}
	return nil
}

type turnCommitResult struct {
	OutboundID   int64
	OutboundType string
	Committed    bool
}

func (r *Runtime) persistTurn(ctx context.Context, input turnCommitInput) (turnCommitResult, error) {
	out := turnCommitResult{}
	convertErrPrefix := input.ErrCtx.ConvertMessages
	if convertErrPrefix == "" {
		convertErrPrefix = "convert new messages"
	}
	planStateErrPrefix := input.ErrCtx.LoadPlanState
	if planStateErrPrefix == "" {
		planStateErrPrefix = "load plan state before save"
	}
	operationStateErrPrefix := input.ErrCtx.LoadOperation
	if operationStateErrPrefix == "" {
		operationStateErrPrefix = "load operation state before save"
	}
	saveErrPrefix := input.ErrCtx.SaveSession
	if saveErrPrefix == "" {
		saveErrPrefix = "save session"
	}

	if len(input.OutHistory) < input.HistoryInputLen {
		return out, fmt.Errorf("%s: %w", convertErrPrefix, fmt.Errorf("invalid governor output history window"))
	}

	newMessages, err := session.NewMessagesForTurn(input.Prepared.LedgerText, input.OutHistory[input.HistoryInputLen:], input.Sess.TurnCount)
	if err != nil {
		return out, fmt.Errorf("%s: %w", convertErrPrefix, err)
	}
	newMessages = replaceLastAssistantWithSceneText(newMessages, input.ReplyText)
	newMessages = setLastAssistantFloor(newMessages, input.FloorText)
	newMessages = setLastAssistantFloorMetadata(newMessages, input.FloorMetadata)
	input.Sess.LastFloorText = input.FloorText
	input.Sess.LastFloorMetadata = input.FloorMetadata

	if planState, planErr := r.store.PlanState(input.Key); planErr == nil {
		input.Sess.PlanState = mergeSessionPlanState(input.Sess.PlanState, planState)
	} else {
		return out, fmt.Errorf("%s: %w", planStateErrPrefix, planErr)
	}
	if operationState, operationErr := r.store.OperationState(input.Key); operationErr == nil {
		input.Sess.OperationState = mergeSessionOperationState(input.Sess.OperationState, operationState)
	} else {
		return out, fmt.Errorf("%s: %w", operationStateErrPrefix, operationErr)
	}

	if err := r.store.Save(input.Sess, newMessages, input.Result.TokenUsage); err != nil {
		return out, fmt.Errorf("%s: %w", saveErrPrefix, err)
	}
	out.Committed = true
	out.OutboundID = input.OutboundID
	out.OutboundType = input.OutboundType

	if input.Audit != nil {
		input.Audit.RecordFinalReply(input.ReplyText, input.Result.Media, out.OutboundType)
	}
	return out, nil
}
