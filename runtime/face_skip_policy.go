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
	if materialPacketHasRelationalOrCreativePressure(packet) {
		return false
	}
	if !materialPacketLooksLikeStatusReport(packet) {
		return false
	}
	return true
}

func materialPacketLooksLikeStatusReport(packet core.MaterialPacket) bool {
	facts := nonBlankMaterialItems(packet.Facts)
	allowed := nonBlankMaterialItems(packet.AllowedActions)
	commitments := nonBlankMaterialItems(packet.Commitments)
	refusals := nonBlankMaterialItems(packet.Refusals)
	notes := nonBlankMaterialItems(packet.Notes)
	if len(facts) == 0 && len(allowed) == 0 && len(commitments) == 0 && len(refusals) == 0 {
		return false
	}
	if len(notes) > 1 {
		return false
	}
	statusText := append([]string{}, facts...)
	statusText = append(statusText, allowed...)
	statusText = append(statusText, commitments...)
	statusText = append(statusText, refusals...)
	text := strings.ToLower(strings.Join(statusText, "\n"))
	statusTerms := []string{
		"tested", "validated", "verified", "passed", "deployed", "merged", "commit", "branch", "diff", "telemetry", "metric", "token", "cache", "service", "revision", "pr #", "pull request", "report", "evidence", "status", "stopped before",
	}
	for _, term := range statusTerms {
		if strings.Contains(text, term) {
			return true
		}
	}
	return false
}

func materialPacketHasRelationalOrCreativePressure(packet core.MaterialPacket) bool {
	materialText := append([]string{}, packet.Facts...)
	materialText = append(materialText, packet.AllowedActions...)
	materialText = append(materialText, packet.Commitments...)
	materialText = append(materialText, packet.Refusals...)
	materialText = append(materialText, packet.Notes...)
	text := strings.ToLower(strings.Join(materialText, "\n"))
	for _, marker := range []string{
		"tone", "warm", "voice", "relational", "grief", "love", "feel", "felt", "overwhelmed", "creative", "essay", "story", "poem", "music", "ritual", "dream", "persona", "scene", "nuance",
	} {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return false
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

func faceSkipPayload(reason string, input turnRenderInput, fallbackText string) map[string]any {
	packet := input.MaterialFloor
	mediaCount := 0
	if input.Result != nil {
		mediaCount = len(input.Result.Media)
	}
	return map[string]any{
		"reason":          strings.TrimSpace(reason),
		"media_count":     mediaCount,
		"facts":           len(nonBlankMaterialItems(packet.Facts)),
		"allowed_actions": len(nonBlankMaterialItems(packet.AllowedActions)),
		"commitments":     len(nonBlankMaterialItems(packet.Commitments)),
		"refusals":        len(nonBlankMaterialItems(packet.Refusals)),
		"notes":           len(nonBlankMaterialItems(packet.Notes)),
		"fallback_chars":  len(strings.TrimSpace(fallbackText)),
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
