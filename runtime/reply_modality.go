//go:build linux

package runtime

import (
	"strings"

	"github.com/idolum-ai/aphelion/pipeline"
	"github.com/idolum-ai/aphelion/prompt"
)

const replyModalityVoice = "voice"
const replyModalityAuto = "auto"
const replyModalityDirectivePrefix = "reply_modality:"

func (r *Runtime) applyReplyModalityAwareness(aw prompt.RuntimeAwareness, prepared pipeline.TurnPrepareContract) prompt.RuntimeAwareness {
	aw.InboundWasVoice = prepared.InboundWasVoice
	aw.ReplyModalityDefault = replyModalityText
	aw.ReplyModalityReason = "runtime default is text"
	aw.ReplyModalityOverride = "none"

	preferred := strings.ToLower(strings.TrimSpace(prepared.PreferredReplyModality))
	switch preferred {
	case replyModalityText:
		aw.ReplyModalityDefault = replyModalityText
		aw.ReplyModalityReason = "prepared turn requested text reply modality"
		aw.ReplyModalityOverride = replyModalityText
		return aw
	case replyModalityVoice:
		aw.ReplyModalityDefault = replyModalityVoice
		aw.ReplyModalityReason = "prepared turn requested voice reply modality"
		aw.ReplyModalityOverride = replyModalityVoice
		return aw
	}

	switch strings.ToLower(strings.TrimSpace(r.voiceMode)) {
	case "all":
		aw.ReplyModalityDefault = replyModalityVoice
		aw.ReplyModalityReason = "voice.mode=all"
	case replyModalityAuto:
		if prepared.InboundWasVoice {
			aw.ReplyModalityDefault = replyModalityVoice
			aw.ReplyModalityReason = "voice.mode=auto and inbound_was_voice=true"
		} else {
			aw.ReplyModalityReason = "voice.mode=auto and inbound_was_voice=false"
		}
	default:
		aw.ReplyModalityReason = "voice mode is text/off"
	}
	return aw
}

func extractReplyModalityDirective(text string) (string, string) {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	lines := strings.Split(text, "\n")
	kept := make([]string, 0, len(lines))
	modality := ""
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		lower := strings.ToLower(trimmed)
		if strings.HasPrefix(lower, replyModalityDirectivePrefix) {
			candidate := strings.TrimSpace(trimmed[len("REPLY_MODALITY:"):])
			switch strings.ToLower(candidate) {
			case replyModalityText, replyModalityVoice, replyModalityAuto:
				modality = strings.ToLower(candidate)
			}
			continue
		}
		kept = append(kept, line)
	}
	return strings.TrimSpace(strings.Join(kept, "\n")), modality
}
