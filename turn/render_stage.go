//go:build linux

package turn

import (
	"context"
	"strings"
	"unicode"

	"github.com/idolum-ai/aphelion/agent"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/pipeline"
	"github.com/idolum-ai/aphelion/prompt"
)

// RenderStageRequest captures the render-stage decision input for one turn.
type RenderStageRequest struct {
	Render                FaceRenderRequest
	FacePolicy            pipeline.FacePolicy
	UseMaterialFloor      bool
	ReplyWithVoice        bool
	AllowStream           bool
	Media                 []core.Media
	ToolLog               []string
	GeneratedMessages     []agent.Message
	InitialReply          string
	FallbackOptions       pipeline.FallbackOptions
	SkipRender            bool
	SkipRenderReason      string
	ConditionalSkipReason string
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
type FaceSkipReason string

const (
	FaceSkipReasonStructuralSkip       FaceSkipReason = "structural_skip"
	FaceSkipReasonRenderPolicy         FaceSkipReason = "render_policy"
	FaceSkipReasonMaterialStatusReport FaceSkipReason = "material_status_report"
)

func NormalizeFaceSkipReason(reason string) FaceSkipReason {
	switch FaceSkipReason(strings.TrimSpace(reason)) {
	case FaceSkipReasonStructuralSkip:
		return FaceSkipReasonStructuralSkip
	case FaceSkipReasonRenderPolicy:
		return FaceSkipReasonRenderPolicy
	case FaceSkipReasonMaterialStatusReport:
		return FaceSkipReasonMaterialStatusReport
	default:
		return FaceSkipReason(strings.TrimSpace(reason))
	}
}

// RenderStageResult is the output of render-stage orchestration.
type RenderStageResult struct {
	ReplyText       string
	Runtime         prompt.RuntimeAwareness
	Usage           core.TokenUsage
	Streamed        bool
	RenderedID      int64
	RenderedType    string
	ReplyModality   string
	ShouldRender    bool
	RenderError     error
	StreamHandled   bool
	SkipReason      FaceSkipReason
	FallbackReason  string
	FallbackApplied bool
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
		result.SkipReason = NormalizeFaceSkipReason(firstNonEmpty(strings.TrimSpace(req.SkipRenderReason), string(FaceSkipReasonStructuralSkip)))
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
	if result.ShouldRender && strings.TrimSpace(req.ConditionalSkipReason) != "" && !req.ReplyWithVoice {
		result.ShouldRender = false
		result.SkipReason = NormalizeFaceSkipReason(req.ConditionalSkipReason)
	}
	if !result.ShouldRender && !req.ReplyWithVoice {
		if result.SkipReason == "" {
			result.SkipReason = FaceSkipReasonRenderPolicy
		}
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
			applyRenderCompletenessFallback(&result, callbacks, req)
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
			applyRenderCompletenessFallback(&result, callbacks, req)
		}
	}

	return result, nil
}

func applyRenderCompletenessFallback(result *RenderStageResult, callbacks RenderStageCallbacks, req RenderStageRequest) {
	if result == nil || req.ReplyWithVoice {
		return
	}
	reason := incompleteFaceRenderFallbackReason(result.ReplyText, req.Render.FloorText, req.Render.MaterialFloor)
	if reason == "" && strings.TrimSpace(result.ReplyText) != "" {
		return
	}
	fallback := renderStageFallback(callbacks, req.Render.MaterialFloor, req.Render.FloorText, req.FallbackOptions)
	if strings.TrimSpace(fallback) == "" {
		return
	}
	result.ReplyText = fallback
	result.Runtime.DeliveryMode = "floor_fallback"
	if reason == "" {
		reason = "empty_face_render"
	}
	result.FallbackReason = reason
	result.FallbackApplied = true
}

func incompleteFaceRenderFallbackReason(renderedText string, floorText string, packet core.MaterialPacket) string {
	rendered := strings.TrimSpace(renderedText)
	floor := strings.TrimSpace(floorText)
	if rendered == "" || floor == "" || packet.Empty() {
		return ""
	}
	if materialPacketHasSceneConstraints(packet) {
		return ""
	}
	if !materialPacketLooksOperational(packet) {
		return ""
	}
	if !looksTruncatedSentence(rendered) {
		return ""
	}
	// A deliberately conservative ratio: normal face summaries may be shorter than
	// the floor, but an operational scene ending mid-sentence at less than two
	// thirds of the complete floor is safer as deterministic floor fallback.
	if len([]rune(rendered))*3 >= len([]rune(floor))*2 {
		return ""
	}
	return "partial_face_render"
}

func materialPacketHasSceneConstraints(packet core.MaterialPacket) bool {
	for _, constraint := range packet.SceneConstraints {
		trimmed := strings.TrimSpace(constraint)
		if trimmed == "" {
			continue
		}
		// Scene constraints can intentionally authorize a short/shaped visible
		// render. Do not let the generic operational-length heuristic override
		// that unless the constraint itself asks the face to preserve completeness.
		lower := strings.ToLower(trimmed)
		if strings.Contains(lower, "complete") || strings.Contains(lower, "completeness") || strings.Contains(lower, "preserve") || strings.Contains(lower, "full") || strings.Contains(lower, "evidence") {
			continue
		}
		return true
	}
	return false
}

func materialPacketLooksOperational(packet core.MaterialPacket) bool {
	terms := []string{
		"active", "artifact", "binary", "branch", "build", "checksum", "commit", "deploy", "deployment", "evidence", "installed", "pid", "pr #", "pull request", "restart", "revision", "service", "status", "verify-deploy", "verified", "vcs_modified",
	}
	text := strings.ToLower(strings.Join([]string{
		strings.Join(packet.Facts, "\n"),
		strings.Join(packet.AllowedActions, "\n"),
		strings.Join(packet.Commitments, "\n"),
		strings.Join(packet.Refusals, "\n"),
		strings.Join(packet.Notes, "\n"),
	}, "\n"))
	for _, term := range terms {
		if strings.Contains(text, term) {
			return true
		}
	}
	return false
}

func looksTruncatedSentence(text string) bool {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return false
	}
	last := lastNonSpaceRune(trimmed)
	if last == 0 {
		return false
	}
	switch last {
	case '.', '!', '?', '…', '`', ')', ']', '}', '"', '\'':
		return false
	case ':', ';':
		return true
	default:
		return unicode.IsLetter(last) || unicode.IsDigit(last)
	}
}

func lastNonSpaceRune(text string) rune {
	for _, r := range reverseRunes(text) {
		if !unicode.IsSpace(r) {
			return r
		}
	}
	return 0
}

func reverseRunes(text string) []rune {
	runes := []rune(text)
	for i, j := 0, len(runes)-1; i < j; i, j = i+1, j-1 {
		runes[i], runes[j] = runes[j], runes[i]
	}
	return runes
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func renderStageFallback(callbacks RenderStageCallbacks, packet core.MaterialPacket, floorText string, opts pipeline.FallbackOptions) string {
	if callbacks.Fallback != nil {
		if text := strings.TrimSpace(callbacks.Fallback(packet, floorText, opts)); text != "" {
			return text
		}
	}
	return pipeline.FloorTextOrFallback(strings.TrimSpace(floorText))
}
