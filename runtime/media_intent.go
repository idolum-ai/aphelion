//go:build linux

package runtime

import (
	"encoding/json"
	"strings"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/pipeline"
)

const (
	hiddenInputPendingMediaIntent  = "pending_media_intent"
	hiddenInputConsumedMediaIntent = "consumed_pending_media_intent"
	hiddenInputMediaReplyModality  = "media_reply_modality_decision"

	replyModalityText = "text"
)

func applyMediaIntentPolicy(priorFloorMetadata string, msg core.InboundMessage, prepared *pipeline.TurnPrepareContract) {
	if prepared == nil {
		return
	}
	hasAudio := inboundMessageHasAudio(msg) || prepared.InboundWasVoice
	pendingTranscription := floorHasPendingAudioTranscriptionIntent(priorFloorMetadata)
	currentTranscription := textRequestsAudioTranscription(msg.Text)

	if textRequestsPendingAudioTranscription(msg.Text) && !hasAudio {
		appendPreparedHiddenInput(prepared, hiddenInputPendingMediaIntent, "next audio should be transcribed and answered in text")
		return
	}

	if !hasAudio {
		return
	}
	switch {
	case pendingTranscription:
		prepared.PreferredReplyModality = replyModalityText
		appendPreparedHiddenInput(prepared, hiddenInputConsumedMediaIntent, "pending next-audio transcription intent consumed; answer this audio turn in text")
	case currentTranscription:
		prepared.PreferredReplyModality = replyModalityText
		appendPreparedHiddenInput(prepared, hiddenInputMediaReplyModality, "audio transcription intent requests a text reply for this turn")
	}
}

func appendPreparedHiddenInput(prepared *pipeline.TurnPrepareContract, category string, summary string) {
	category = strings.TrimSpace(category)
	summary = strings.TrimSpace(summary)
	if prepared == nil || category == "" || summary == "" {
		return
	}
	for _, existing := range prepared.ArtifactDecisionInputs {
		if strings.TrimSpace(existing.Category) == category {
			return
		}
	}
	prepared.ArtifactDecisionInputs = append(prepared.ArtifactDecisionInputs, core.HiddenInput{
		Category: category,
		Summary:  summary,
	})
}

func inboundMessageHasAudio(msg core.InboundMessage) bool {
	for _, raw := range msg.Artifacts {
		artifact := core.NormalizeArtifact(raw)
		if artifact.Kind == "audio" || artifact.SourceType == "voice" || artifact.SourceType == "audio" {
			return true
		}
	}
	return false
}

func floorHasPendingAudioTranscriptionIntent(priorFloorMetadata string) bool {
	metadata := strings.TrimSpace(priorFloorMetadata)
	if metadata == "" {
		return false
	}
	var floor core.FloorMetadata
	if err := json.Unmarshal([]byte(metadata), &floor); err != nil {
		return false
	}
	for _, input := range floor.HiddenInputs {
		if strings.TrimSpace(input.Category) != hiddenInputPendingMediaIntent {
			continue
		}
		summary := normalizeMediaIntentText(input.Summary)
		if strings.Contains(summary, "next audio") && strings.Contains(summary, "transcrib") && strings.Contains(summary, "text") {
			return true
		}
	}
	return false
}

func textRequestsPendingAudioTranscription(text string) bool {
	normalized := normalizeMediaIntentText(text)
	if normalized == "" || !containsTranscriptionTerm(normalized) || !containsAudioTerm(normalized) {
		return false
	}
	return strings.Contains(normalized, " next ") ||
		strings.Contains(normalized, " following ") ||
		strings.Contains(normalized, " upcoming ") ||
		strings.Contains(normalized, " subsequent ")
}

func textRequestsAudioTranscription(text string) bool {
	return containsTranscriptionTerm(normalizeMediaIntentText(text))
}

func containsTranscriptionTerm(text string) bool {
	return strings.Contains(text, " transcrib") ||
		strings.Contains(text, " transcript") ||
		strings.Contains(text, " transcription") ||
		strings.Contains(text, " speech to text ") ||
		strings.Contains(text, " write down ")
}

func containsAudioTerm(text string) bool {
	return strings.Contains(text, " audio ") ||
		strings.Contains(text, " voice ") ||
		strings.Contains(text, " voice note ") ||
		strings.Contains(text, " voice memo ") ||
		strings.Contains(text, " recording ") ||
		strings.Contains(text, " spoken ") ||
		strings.Contains(text, " speech ")
}

func normalizeMediaIntentText(text string) string {
	normalized := strings.ToLower(strings.TrimSpace(text))
	if normalized == "" {
		return ""
	}
	replacer := strings.NewReplacer(
		"\n", " ",
		"\t", " ",
		".", " ",
		",", " ",
		"!", " ",
		"?", " ",
		":", " ",
		";", " ",
		"(", " ",
		")", " ",
		"[", " ",
		"]", " ",
		"{", " ",
		"}", " ",
	)
	normalized = replacer.Replace(normalized)
	return " " + strings.Join(strings.Fields(normalized), " ") + " "
}
