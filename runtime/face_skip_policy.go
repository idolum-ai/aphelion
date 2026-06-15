//go:build linux

package runtime

import (
	"strings"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/face"
	"github.com/idolum-ai/aphelion/pipeline"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/turn"
)

const faceSkipReasonMaterialStatusReport = string(turn.FaceSkipReasonMaterialStatusReport)

func faceConditionalSkipReason(input turnRenderInput) string {
	if !shouldSkipFaceForMaterialStatusReport(input) {
		return ""
	}
	return faceSkipReasonMaterialStatusReport
}

func shouldSkipFaceForMaterialStatusReport(input turnRenderInput) bool {
	if input.Result == nil || !input.FacePolicy.Render {
		return false
	}
	if input.ReplyWithVoice || input.MediaOnlyReply || len(input.Result.Media) > 0 {
		return false
	}
	if input.FaceAwareness.InboundWasVoice || strings.EqualFold(strings.TrimSpace(input.FaceAwareness.ReplyModalityDefault), replyModalityVoice) || strings.EqualFold(strings.TrimSpace(input.FaceAwareness.ReplyModalityOverride), replyModalityVoice) {
		return false
	}
	packet := input.MaterialFloor
	if !input.UseMaterialFloor || packet.Empty() || len(nonBlankMaterialItems(packet.SceneConstraints)) > 0 {
		return false
	}
	if strings.TrimSpace(input.Result.ProviderFailure) != "" {
		return false
	}
	if packet.Kind != core.MaterialPacketKindStatusReport {
		return false
	}
	return true
}

func nonBlankMaterialItems(items []string) []string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		if trimmed := strings.TrimSpace(item); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func nonBlankContinuityContextItems(items []core.MaterialContinuityContext) []core.MaterialContinuityContext {
	out := make([]core.MaterialContinuityContext, 0, len(items))
	for _, item := range items {
		if !item.Empty() {
			out = append(out, item)
		}
	}
	return out
}

func faceSkipPayload(reason string, input turnRenderInput, fallbackText string) map[string]any {
	packet := input.MaterialFloor
	mediaCount := 0
	if input.Result != nil {
		mediaCount = len(input.Result.Media)
	}
	return map[string]any{
		"reason":             strings.TrimSpace(reason),
		"kind":               strings.TrimSpace(string(packet.Kind)),
		"media_count":        mediaCount,
		"facts":              len(nonBlankMaterialItems(packet.Facts)),
		"allowed_actions":    len(nonBlankMaterialItems(packet.AllowedActions)),
		"commitments":        len(nonBlankMaterialItems(packet.Commitments)),
		"refusals":           len(nonBlankMaterialItems(packet.Refusals)),
		"continuity_context": len(nonBlankContinuityContextItems(packet.ContinuityContext)),
		"notes":              len(nonBlankMaterialItems(packet.Notes)),
		"fallback_chars":     len(strings.TrimSpace(fallbackText)),
	}
}

func faceFallbackTextForInput(input turnRenderInput) string {
	return pipeline.SerializeFloorFallback(input.MaterialFloor, input.FloorText, input.FallbackOpts)
}

func (r *Runtime) structuralFaceRenderSkip(input turnRenderInput) (bool, string) {
	if input.MediaOnlyReply {
		return true, "media_only_reply"
	}
	if input.Result != nil && strings.TrimSpace(input.Result.ProviderFailure) != "" {
		return true, "provider_failure"
	}
	if r != nil && r.faceBackend == face.BackendFloorFallback {
		return true, "face_backend_floor_fallback"
	}
	if input.CurrentFaceModel == nil {
		return true, "face_renderer_unavailable"
	}
	return false, ""
}

func shouldRecordFaceSkipEvent(key session.SessionKey) bool {
	return true
}
