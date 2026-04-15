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
	Key                  session.SessionKey
	Sess                 *session.Session
	Prepared             pipeline.TurnPrepareContract
	OutHistory           []agent.Message
	HistoryInputLen      int
	Result               *core.TurnResult
	FloorText            string
	FloorMetadata        string
	ReplyText            string
	StreamedReply        bool
	OutboundID           int64
	OutboundType         string
	SendReply            func(context.Context) (int64, string, error)
	RecordOutbound       bool
	Audit                *turnAuditRecorder
	PostCommitHooks      []func() error
	ErrorConvertMessages string
	ErrorLoadPlanState   string
	ErrorLoadOperation   string
	ErrorSaveSession     string
	ErrorRecordOutbound  string
}

type turnCommitResult struct {
	OutboundID   int64
	OutboundType string
	Committed    bool
}

func (r *Runtime) commitTurn(ctx context.Context, input turnCommitInput) (turnCommitResult, error) {
	out := turnCommitResult{}
	convertErrPrefix := input.ErrorConvertMessages
	if convertErrPrefix == "" {
		convertErrPrefix = "convert new messages"
	}
	planStateErrPrefix := input.ErrorLoadPlanState
	if planStateErrPrefix == "" {
		planStateErrPrefix = "load plan state before save"
	}
	operationStateErrPrefix := input.ErrorLoadOperation
	if operationStateErrPrefix == "" {
		operationStateErrPrefix = "load operation state before save"
	}
	saveErrPrefix := input.ErrorSaveSession
	if saveErrPrefix == "" {
		saveErrPrefix = "save session"
	}
	recordOutboundErrPrefix := input.ErrorRecordOutbound
	if recordOutboundErrPrefix == "" {
		recordOutboundErrPrefix = "record outbound reply"
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

	if !input.StreamedReply && input.SendReply != nil {
		outboundID, outboundType, sendErr := input.SendReply(ctx)
		if sendErr != nil {
			return out, sendErr
		}
		out.OutboundID = outboundID
		out.OutboundType = outboundType
	} else {
		out.OutboundID = input.OutboundID
		out.OutboundType = input.OutboundType
	}

	if input.Audit != nil {
		input.Audit.RecordFinalReply(input.ReplyText, input.Result.Media, out.OutboundType)
	}

	if input.RecordOutbound && out.OutboundID != 0 {
		if err := r.store.RecordOutbound(input.Key, input.Sess.TurnCount, out.OutboundID, out.OutboundType); err != nil {
			return out, fmt.Errorf("%s: %w", recordOutboundErrPrefix, err)
		}
	}
	for _, hook := range input.PostCommitHooks {
		if err := hook(); err != nil {
			return out, err
		}
	}
	return out, nil
}
