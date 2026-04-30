//go:build linux

package turn

import (
	"context"
	"strings"

	"github.com/idolum-ai/aphelion/agent"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/pipeline"
	"github.com/idolum-ai/aphelion/prompt"
)

// RenderStageRequest captures the render-stage decision input for one turn.
type RenderStageRequest struct {
	Render            FaceRenderRequest
	FacePolicy        pipeline.FacePolicy
	UseMaterialFloor  bool
	ReplyWithVoice    bool
	AllowStream       bool
	Media             []core.Media
	ToolLog           []string
	GeneratedMessages []agent.Message
	InitialReply      string
	FallbackOptions   pipeline.FallbackOptions
	SkipRender        bool
}

// RenderStageCallbacks are runtime-supplied hooks for render execution.
type RenderStageCallbacks struct {
	// Stream attempts a streamed render and returns (result, handled, err).
	// handled=true means stream produced the render outcome.
	Stream func(ctx context.Context, req FaceRenderRequest) (FaceRenderResult, bool, error)
	// Render performs non-stream scene rendering.
	Render func(ctx context.Context, req FaceRenderRequest) (*FaceRenderResult, error)
	// Fallback serializes floor text when scene rendering is unavailable.
	Fallback func(packet core.MaterialPacket, floorText string, opts pipeline.FallbackOptions) string
}

// RenderStageResult is the output of render-stage orchestration.
type RenderStageResult struct {
	ReplyText     string
	Runtime       prompt.RuntimeAwareness
	Usage         core.TokenUsage
	Streamed      bool
	RenderedID    int64
	RenderedType  string
	ReplyModality string
	ShouldRender  bool
	RenderError   error
	StreamHandled bool
}

// RunRenderStage applies stream/non-stream/fallback selection for one turn.
func RunRenderStage(ctx context.Context, req RenderStageRequest, callbacks RenderStageCallbacks) (RenderStageResult, error) {
	result := RenderStageResult{
		ReplyText: strings.TrimSpace(req.InitialReply),
		Runtime:   req.Render.Runtime,
	}
	if result.Runtime.DeliveryMode == "" {
		result.Runtime.DeliveryMode = "text"
	}
	result.Runtime.StreamReply = false
	result.Runtime.ArtifactMode = "scene"
	if req.ReplyWithVoice {
		result.Runtime.DeliveryMode = "voice"
	} else if req.FacePolicy.Render {
		result.Runtime.DeliveryMode = "idolum_render"
	}

	if req.SkipRender {
		return result, nil
	}

	renderReq := req.Render
	renderReq.Runtime = result.Runtime

	renderHeuristicText := strings.TrimSpace(req.Render.FloorText)
	if req.UseMaterialFloor {
		renderHeuristicText = pipeline.FormatFloorTextForRender(req.Render.MaterialFloor, renderHeuristicText)
	}
	result.ShouldRender = pipeline.ShouldRenderInteractiveIdolumReply(req.FacePolicy, pipeline.RenderDecisionInput{
		UserText:          req.Render.LatestUserInput,
		FloorText:         renderHeuristicText,
		ToolLog:           req.ToolLog,
		GeneratedMessages: req.GeneratedMessages,
	})
	if !result.ShouldRender && !req.ReplyWithVoice {
		result.Runtime.DeliveryMode = "floor_fallback"
		renderReq.Runtime = result.Runtime
	}

	faceRendered := false
	if result.ShouldRender && !req.ReplyWithVoice && req.AllowStream && len(req.Media) == 0 && callbacks.Stream != nil {
		streamResult, handled, err := callbacks.Stream(ctx, renderReq)
		if err != nil {
			return result, err
		}
		result.StreamHandled = handled
		if handled {
			faceRendered = true
			result.Streamed = streamResult.Streamed
			result.RenderedID = streamResult.RenderedID
			result.RenderedType = strings.TrimSpace(streamResult.RenderedType)
			result.Usage = addTokenUsage(result.Usage, streamResult.Usage)
			result.ReplyText = strings.TrimSpace(streamResult.Text)
			result.ReplyModality = strings.TrimSpace(streamResult.ReplyModality)
			if result.ReplyText == "" {
				result.ReplyText = renderStageFallback(callbacks, req.Render.MaterialFloor, req.Render.FloorText, req.FallbackOptions)
			}
		}
	}

	if result.ShouldRender && !faceRendered && callbacks.Render != nil {
		if !req.ReplyWithVoice {
			result.Runtime.DeliveryMode = "idolum_render"
			result.Runtime.StreamReply = false
			renderReq.Runtime = result.Runtime
		}
		rendered, err := callbacks.Render(ctx, renderReq)
		if err != nil {
			result.RenderError = err
		} else if rendered != nil {
			result.ReplyText = strings.TrimSpace(rendered.Text)
			result.ReplyModality = strings.TrimSpace(rendered.ReplyModality)
			result.Usage = addTokenUsage(result.Usage, rendered.Usage)
			if result.ReplyText == "" {
				result.ReplyText = renderStageFallback(callbacks, req.Render.MaterialFloor, req.Render.FloorText, req.FallbackOptions)
			}
		}
	}

	return result, nil
}

func renderStageFallback(callbacks RenderStageCallbacks, packet core.MaterialPacket, floorText string, opts pipeline.FallbackOptions) string {
	if callbacks.Fallback != nil {
		if text := strings.TrimSpace(callbacks.Fallback(packet, floorText, opts)); text != "" {
			return text
		}
	}
	return pipeline.FloorTextOrFallback(strings.TrimSpace(floorText))
}
