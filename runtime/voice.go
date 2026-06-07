//go:build linux

package runtime

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/media"
	"github.com/idolum-ai/aphelion/pipeline"
	"github.com/idolum-ai/aphelion/tool/sandbox"
)

type voiceSender interface {
	SendVoiceMessage(ctx context.Context, chatID int64, media core.Media, replyTo *int64) (int64, error)
}

func (r *Runtime) transcribeAudioArtifact(ctx context.Context, scope sandbox.Scope, artifact core.Artifact) (string, error) {
	if len(artifact.Data) == 0 {
		return "", fmt.Errorf("audio bytes unavailable")
	}
	if r.transcriber == nil {
		return "", fmt.Errorf("voice transcription is not configured")
	}

	tmpRoot := voiceTempRoot(scope, r.cfg.Agent)
	if err := os.MkdirAll(tmpRoot, 0o755); err != nil {
		return "", fmt.Errorf("create voice temp root: %w", err)
	}
	ext := strings.ToLower(strings.TrimSpace(filepath.Ext(artifact.Filename)))
	if ext == "" {
		ext = ".ogg"
	}
	tmp, err := os.CreateTemp(tmpRoot, "aphelion-audio-*"+ext)
	if err != nil {
		return "", fmt.Errorf("create temp voice file: %w", err)
	}
	path := filepath.Clean(tmp.Name())
	defer os.Remove(path)
	if _, err := tmp.Write(artifact.Data); err != nil {
		_ = tmp.Close()
		return "", fmt.Errorf("write temp voice file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return "", fmt.Errorf("close temp voice file: %w", err)
	}

	transcription, err := r.transcriber.Transcribe(ctx, &media.TranscriptionRequest{Path: path})
	if err != nil {
		return "", fmt.Errorf("transcribe %s: %w", artifactHumanLabel(artifact), err)
	}
	text := strings.TrimSpace(transcription.Text)
	if text == "" {
		return "[empty voice transcript]", nil
	}
	return text, nil
}

func (r *Runtime) shouldReplyWithVoice(inboundWasVoice bool) bool {
	switch strings.ToLower(strings.TrimSpace(r.voiceMode)) {
	case "all":
		return true
	case "auto":
		return inboundWasVoice
	default:
		return false
	}
}

func (r *Runtime) preparedReplyWithVoice(prepared pipeline.TurnPrepareContract) bool {
	switch strings.ToLower(strings.TrimSpace(prepared.PreferredReplyModality)) {
	case replyModalityText:
		return false
	case "voice":
		return true
	default:
		return r.shouldReplyWithVoice(prepared.InboundWasVoice)
	}
}

func (r *Runtime) sendReply(ctx context.Context, msg core.InboundMessage, text string, media []core.Media, replyWithVoice bool) (int64, string, error) {
	msgID, kind, _, err := r.sendReplyWithDelivery(ctx, msg, text, media, replyWithVoice)
	return msgID, kind, err
}

func (r *Runtime) sendReplyWithDelivery(ctx context.Context, msg core.InboundMessage, text string, media []core.Media, replyWithVoice bool) (int64, string, []int64, error) {
	visibleText := r.prefixTelegramPresentedText(r.telegramPresentationForMessage(msg), text)
	replyTo := r.replyAnchorForTelegramMessage(msg)
	if len(media) > 0 {
		delivery := &core.OutboundDelivery{}
		msgID, err := r.outbound.SendMessage(ctx, core.OutboundMessage{
			ChatID:   msg.ChatID,
			Text:     visibleText,
			Media:    media,
			ReplyTo:  replyTo,
			Delivery: delivery,
		})
		if err != nil {
			return 0, "", nil, err
		}
		return msgID, "media", outboundDeliveryIDs(delivery, msgID), nil
	}

	if replyWithVoice && r.synth != nil {
		if sender, ok := r.outbound.(voiceSender); ok {
			audio, err := r.synth.Synthesize(ctx, text)
			if err == nil {
				msgID, sendErr := sender.SendVoiceMessage(ctx, msg.ChatID, audio, replyTo)
				if sendErr == nil {
					return msgID, "voice", []int64{msgID}, nil
				}
				err = sendErr
			}
		}
	}

	delivery := &core.OutboundDelivery{}
	msgID, err := r.outbound.SendMessage(ctx, core.OutboundMessage{
		ChatID:   msg.ChatID,
		Text:     visibleText,
		ReplyTo:  replyTo,
		Delivery: delivery,
	})
	if err != nil {
		return 0, "", nil, err
	}
	return msgID, "text", outboundDeliveryIDs(delivery, msgID), nil
}

func outboundDeliveryIDs(delivery *core.OutboundDelivery, fallback int64) []int64 {
	seen := map[int64]struct{}{}
	var out []int64
	for _, id := range append([]int64{fallback}, deliveryIDs(delivery)...) {
		if id == 0 {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

func deliveryIDs(delivery *core.OutboundDelivery) []int64 {
	if delivery == nil {
		return nil
	}
	return delivery.MessageIDs
}

func (r *Runtime) replyAnchorForTelegramMessage(msg core.InboundMessage) *int64 {
	if msg.TelegramThreadID > 0 && strings.TrimSpace(msg.DurableAgentID) == "" && r != nil && r.store != nil {
		if anchor, ok, err := r.store.TelegramThreadLastMessage(msg.ChatID, msg.TelegramThreadID); err == nil && ok && anchor.MessageID > 0 {
			return replyToMessageID(anchor.MessageID)
		}
	}
	return replyToMessageID(msg.MessageID)
}

func replyToMessageID(id int64) *int64 {
	if id == 0 {
		return nil
	}
	return &id
}
