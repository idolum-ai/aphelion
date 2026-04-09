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
	"github.com/idolum-ai/aphelion/tool/sandbox"
)

type voiceSender interface {
	SendVoiceMessage(ctx context.Context, chatID int64, media core.Media, replyTo *int64) (int64, error)
}

func (r *Runtime) transcribeVoiceIfNeeded(ctx context.Context, scope sandbox.Scope, msg core.InboundMessage) (string, bool, error) {
	text := strings.TrimSpace(msg.Text)
	if text != "" {
		return text, false, nil
	}

	voiceMedia, ok := firstVoiceMedia(msg.Media)
	if !ok {
		return "[empty message]", false, nil
	}
	if r.transcriber == nil {
		return "", true, fmt.Errorf("voice transcription is not configured")
	}

	tmpRoot := voiceTempRoot(scope, r.cfg.Agent)
	if err := os.MkdirAll(tmpRoot, 0o755); err != nil {
		return "", true, fmt.Errorf("create voice temp root: %w", err)
	}
	tmp, err := os.CreateTemp(tmpRoot, "aphelion-voice-*.ogg")
	if err != nil {
		return "", true, fmt.Errorf("create temp voice file: %w", err)
	}
	path := filepath.Clean(tmp.Name())
	defer os.Remove(path)
	if _, err := tmp.Write(voiceMedia.Data); err != nil {
		_ = tmp.Close()
		return "", true, fmt.Errorf("write temp voice file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return "", true, fmt.Errorf("close temp voice file: %w", err)
	}

	transcription, err := r.transcriber.Transcribe(ctx, &media.TranscriptionRequest{Path: path})
	if err != nil {
		return "", true, fmt.Errorf("transcribe voice: %w", err)
	}
	text = strings.TrimSpace(transcription.Text)
	if text == "" {
		text = "[empty voice transcript]"
	}
	return text, true, nil
}

func (r *Runtime) shouldReplyWithVoice(inboundWasVoice bool) bool {
	switch strings.ToLower(strings.TrimSpace(r.voiceMode)) {
	case "all":
		return true
	case "voice_only":
		return inboundWasVoice
	default:
		return false
	}
}

func (r *Runtime) sendReply(ctx context.Context, msg core.InboundMessage, text string, inboundWasVoice bool) (int64, string, error) {
	if r.shouldReplyWithVoice(inboundWasVoice) && r.synth != nil {
		if sender, ok := r.outbound.(voiceSender); ok {
			audio, err := r.synth.Synthesize(ctx, text)
			if err == nil {
				msgID, sendErr := sender.SendVoiceMessage(ctx, msg.ChatID, audio, replyToMessageID(msg.MessageID))
				if sendErr == nil {
					return msgID, "voice", nil
				}
				err = sendErr
			}
		}
	}

	msgID, err := r.outbound.SendMessage(ctx, core.OutboundMessage{
		ChatID:  msg.ChatID,
		Text:    text,
		ReplyTo: replyToMessageID(msg.MessageID),
	})
	if err != nil {
		return 0, "", err
	}
	return msgID, "text", nil
}

func firstVoiceMedia(items []core.Media) (core.Media, bool) {
	for _, item := range items {
		if item.Type == "voice" && len(item.Data) > 0 {
			return item, true
		}
	}
	return core.Media{}, false
}

func replyToMessageID(id int64) *int64 {
	if id == 0 {
		return nil
	}
	return &id
}
