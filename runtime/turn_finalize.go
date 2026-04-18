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
	Usage         core.TokenUsage
}

func (r *Runtime) renderTurnReply(input turnRenderInput) (turnRenderResult, error) {
	output := turnRenderResult{ReplyText: strings.TrimSpace(input.ReplyText)}
	if input.Result == nil {
		return output, nil
	}

	output.ReplyText = strings.TrimSpace(input.ReplyText)
	if len(input.OutHistory) < input.HistoryInputLen {
		return output, nil
	}
	generatedMessages := input.OutHistory[input.HistoryInputLen:]
	workspaceRoot := faceWorkspaceRoot(input.Scope)
	stageResult, err := turn.RunRenderStage(input.Ctx, turn.RenderStageRequest{
		Render: turn.FaceRenderRequest{
			GovernorName:    prompt.DefaultGovernorName,
			FaceName:        face.DefaultFaceName,
			Channel:         input.Channel,
			PrincipalRole:   input.PrincipalRole,
			WorkspaceRoot:   workspaceRoot,
			FloorText:       input.FloorText,
			MaterialFloor:   input.MaterialFloor,
			LatestUserInput: input.PromptInput,
			Runtime:         input.FaceAwareness,
		},
		FacePolicy:        input.FacePolicy,
		UseMaterialFloor:  input.UseMaterialFloor,
		ReplyWithVoice:    input.ReplyWithVoice,
		AllowStream:       input.AllowStream,
		Media:             input.Result.Media,
		ToolLog:           input.Result.ToolLog,
		GeneratedMessages: generatedMessages,
		InitialReply:      output.ReplyText,
		FallbackOptions:   input.FallbackOpts,
		SkipRender:        input.MediaOnlyReply || r.faceBackend == face.BackendFloorFallback || input.CurrentFaceModel == nil,
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
			renderedReply, streamErr := streamer.RenderStream(ctx, faceReq, func(chunk string) error {
				return editor.OnChunk(ctx, chunk)
			})
			if streamErr != nil {
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
	output.ReplyText = strings.TrimSpace(stageResult.ReplyText)
	output.Usage = addTokenUsage(output.Usage, stageResult.Usage)
	output.StreamedReply = stageResult.Streamed
	output.OutboundID = stageResult.RenderedID
	output.OutboundType = stageResult.RenderedType

	output.ReplyText = r.applyTurnConstitution(
		input.Ctx,
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

	return output, nil
}

type turnCommitInput struct {
	Key             session.SessionKey
	Sess            *session.Session
	Msg             core.InboundMessage
	Actor           principal.Principal
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
	QueueReviewEvents       func(result *turn.Result) error
	DeliverReviewEvents     func(result *turn.Result) error
	QueueDurableArtifact    func(result *turn.Result) error
	PostReplyContinuationUI func(ctx context.Context, result *turn.Result) error
}

type turnCommitErrorContext struct {
	ConvertMessages string
	LoadPlanState   string
	LoadOperation   string
	SaveSession     string
	RecordOutbound  string
}

type turnPersistencePort struct {
	runtime      *Runtime
	key          session.SessionKey
	sess         *session.Session
	sessionState interface {
		session() *session.Session
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
	if req.Result == nil {
		return nil, fmt.Errorf("turn persistence request missing result")
	}
	sess := p.currentSession()
	if sess == nil {
		return nil, fmt.Errorf("turn persistence session unavailable")
	}
	result, err := p.runtime.persistTurn(ctx, turnCommitInput{
		Key:             p.key,
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

type turnDeliveryPort struct {
	runtime      *Runtime
	key          session.SessionKey
	sess         *session.Session
	sessionState interface {
		session() *session.Session
	}
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
	return turn.RunDeliveryStage(ctx, turn.DeliveryStageInput{
		Request:        req,
		Deliver:        p.deliver,
		RecordOutbound: p.recordOutbound,
	}, turn.DeliveryStageCallbacks{
		Send: func(ctx context.Context, msg core.OutboundMessage, inboundWasVoice bool) (int64, string, error) {
			outboundID, outboundType, err := p.runtime.sendReply(ctx, p.msg, msg.Text, msg.Media, inboundWasVoice)
			if err != nil {
				if p.sendErrCtx == "" {
					return 0, "", err
				}
				return 0, "", fmt.Errorf("%s: %w", p.sendErrCtx, err)
			}
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
	out := turnCommitResult{
		OutboundID:   input.OutboundID,
		OutboundType: input.OutboundType,
	}
	usage := core.TokenUsage{}
	if input.Result != nil {
		usage = input.Result.TokenUsage
	}

	stageResult, err := turn.RunPersistStage(ctx, turn.PersistStageInput{
		LedgerText:      input.Prepared.LedgerText,
		OutHistory:      input.OutHistory,
		HistoryInputLen: input.HistoryInputLen,
		Session:         input.Sess,
		ReplyText:       input.ReplyText,
		FloorText:       input.FloorText,
		FloorMetadata:   input.FloorMetadata,
		Usage:           usage,
		ErrorContext: turn.PersistStageErrorContext{
			ConvertMessages: input.ErrCtx.ConvertMessages,
			LoadPlanState:   input.ErrCtx.LoadPlanState,
			LoadOperation:   input.ErrCtx.LoadOperation,
			SaveSession:     input.ErrCtx.SaveSession,
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
		Save: func(_ context.Context, sess *session.Session, newMessages []session.Message, usage core.TokenUsage) error {
			return r.store.Save(sess, newMessages, usage)
		},
	})
	if err != nil {
		return out, err
	}
	out.Committed = stageResult.Committed
	if out.Committed && input.Audit != nil && input.Result != nil {
		input.Audit.RecordFinalReply(input.ReplyText, input.Result.Media, out.OutboundType)
	}
	return out, nil
}
