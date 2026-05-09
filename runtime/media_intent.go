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

func applyMediaIntentPolicy(priorFloorMetadata string, msg core.InboundMessage, prepared *pipeline.TurnPrepareContract, currentClaims []core.InterpretationClaim) {
	if prepared == nil {
		return
	}
	hasAudio := inboundMessageHasAudio(msg) || prepared.InboundWasVoice
	pendingTranscription := floorHasPendingAudioTranscriptionIntent(priorFloorMetadata)
	currentTranscription := currentClaimsRequestAudioTranscription(currentClaims)

	if currentClaimsRequestPendingAudioTranscription(currentClaims) && !hasAudio {
		appendPreparedHiddenInput(prepared, hiddenInputPendingMediaIntent, "next audio should be transcribed and answered in text", mediaIntentClaim(hiddenInputPendingMediaIntent, "next_audio", "transcribe_and_reply_text"))
		return
	}

	if !hasAudio {
		return
	}
	switch {
	case pendingTranscription:
		prepared.PreferredReplyModality = replyModalityText
		appendPreparedHiddenInput(prepared, hiddenInputConsumedMediaIntent, "pending next-audio transcription intent consumed; answer this audio turn in text", mediaIntentClaim(hiddenInputConsumedMediaIntent, "current_audio", "transcribe_and_reply_text"))
	case currentTranscription:
		prepared.PreferredReplyModality = replyModalityText
		appendPreparedHiddenInput(prepared, hiddenInputMediaReplyModality, "audio transcription intent requests a text reply for this turn", mediaIntentClaim(hiddenInputMediaReplyModality, "current_audio", "transcribe_and_reply_text"))
	}
}

func appendPreparedHiddenInput(prepared *pipeline.TurnPrepareContract, category string, summary string, claim core.InterpretationClaim) {
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
	normalizedClaim := core.NormalizeInterpretationClaim(claim)
	prepared.ArtifactDecisionInputs = append(prepared.ArtifactDecisionInputs, core.HiddenInput{
		Category: category,
		Summary:  summary,
		Claim:    &normalizedClaim,
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
		if input.Claim != nil {
			claim := core.NormalizeInterpretationClaim(*input.Claim)
			if claim.Intent == hiddenInputPendingMediaIntent &&
				claim.Scope == "next_audio" &&
				claim.ProposedNextAction == "transcribe_and_reply_text" {
				return true
			}
		}
	}
	return false
}

func mediaIntentClaim(intent string, scope string, nextAction string) core.InterpretationClaim {
	return core.NormalizeInterpretationClaim(core.InterpretationClaim{
		Intent:             strings.TrimSpace(intent),
		Scope:              strings.TrimSpace(scope),
		Risk:               []string{"media_artifact"},
		Confidence:         "high",
		Source:             "operator_media_instruction",
		ProposedNextAction: strings.TrimSpace(nextAction),
	})
}

func currentClaimsRequestPendingAudioTranscription(claims []core.InterpretationClaim) bool {
	for _, claim := range interpretationClaimsWithIntent(claims, hiddenInputPendingMediaIntent) {
		if claim.Scope == "next_audio" && claim.ProposedNextAction == "transcribe_and_reply_text" {
			return true
		}
	}
	return false
}

func currentClaimsRequestAudioTranscription(claims []core.InterpretationClaim) bool {
	for _, claim := range interpretationClaimsWithIntent(claims, hiddenInputMediaReplyModality) {
		if claim.Scope == "current_audio" && claim.ProposedNextAction == "transcribe_and_reply_text" {
			return true
		}
	}
	return false
}
