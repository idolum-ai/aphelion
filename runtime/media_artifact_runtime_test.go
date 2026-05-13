//go:build linux

package runtime

import (
	"context"
	"errors"
	"github.com/idolum-ai/aphelion/config"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/session"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHandleInboundAutoModeTranscribesAndRepliesWithVoice(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	provider.replyText = "idolum text"
	provider.faceReplyText = "idolum spoken"
	var synthesized string
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	rt.ConfigureVoice(config.VoiceConfig{Mode: "auto"}, fakeTranscriber{text: "transcribed hello"}, fakeSynth{
		media:    core.Media{Type: "voice", Data: []byte("mp3"), MimeType: "audio/mpeg", Filename: "reply.mp3"},
		lastText: &synthesized,
	})

	_, err = rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID:     1200,
		SenderID:   1001,
		SenderName: "admin",
		MessageID:  77,
		Artifacts:  []core.Artifact{{ID: "voice-1", Channel: "telegram", SourceType: "voice", Kind: "audio", Subtype: "voice_note", Data: []byte("ogg"), MimeType: "audio/ogg", Filename: "voice.ogg"}},
	})
	if err != nil {
		t.Fatalf("HandleInbound() err = %v", err)
	}

	sender.mu.Lock()
	defer sender.mu.Unlock()
	if len(sender.sent) != 0 {
		t.Fatalf("text sends = %d, want 0 in auto mode for voice input", len(sender.sent))
	}
	if len(sender.voice) != 1 {
		t.Fatalf("voice sends = %d, want 1", len(sender.voice))
	}
	if sender.voice[0].ChatID != 1200 {
		t.Fatalf("voice chat id = %d, want 1200", sender.voice[0].ChatID)
	}
	if synthesized != "idolum spoken" {
		t.Fatalf("synthesized text = %q, want idolum spoken", synthesized)
	}

	sess, err := store.Load(session.SessionKey{ChatID: 1200, UserID: 0})
	if err != nil {
		t.Fatalf("Load() err = %v", err)
	}
	if len(sess.Messages) < 2 || sess.Messages[0].Content != "transcribed hello\n\n[voice attached]" {
		t.Fatalf("session messages = %#v, want transcribed user text plus voice marker", sess.Messages)
	}
	if sess.Messages[1].Content != "idolum spoken" {
		t.Fatalf("assistant scene = %q, want idolum spoken", sess.Messages[1].Content)
	}
}

func TestHandleInboundVoiceFallsBackToTextWhenSynthesisFails(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	provider.replyText = "voice fallback text"
	provider.faceReplyText = "voice scene text"
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	rt.ConfigureVoice(config.VoiceConfig{Mode: "auto"}, fakeTranscriber{text: "transcribed hello"}, fakeSynth{
		err: errors.New("tts down"),
	})

	_, err = rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID:     1201,
		SenderID:   1001,
		SenderName: "admin",
		MessageID:  78,
		Artifacts:  []core.Artifact{{ID: "voice-2", Channel: "telegram", SourceType: "voice", Kind: "audio", Subtype: "voice_note", Data: []byte("ogg"), MimeType: "audio/ogg", Filename: "voice.ogg"}},
	})
	if err != nil {
		t.Fatalf("HandleInbound() err = %v", err)
	}

	sender.mu.Lock()
	defer sender.mu.Unlock()
	if len(sender.voice) != 0 {
		t.Fatalf("voice sends = %d, want 0 on synth failure", len(sender.voice))
	}
	if len(sender.sent) != 1 || sender.sent[0].Text != "voice scene text" {
		t.Fatalf("text sends = %#v, want text fallback", sender.sent)
	}
}

func TestHandleInboundStoresPendingAudioTranscriptionIntent(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	provider.faceReplyText = "ready"
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	rt.ConfigureVoice(config.VoiceConfig{Mode: "auto"}, fakeTranscriber{text: "unused"}, fakeSynth{
		media: core.Media{Type: "voice", Data: []byte("mp3"), MimeType: "audio/mpeg", Filename: "reply.mp3"},
	})

	_, err = rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID:     1205,
		SenderID:   1001,
		SenderName: "admin",
		MessageID:  82,
		Text:       "please transcribe the next audio",
	})
	if err != nil {
		t.Fatalf("HandleInbound() err = %v", err)
	}

	sender.mu.Lock()
	if len(sender.voice) != 0 {
		t.Fatalf("voice sends = %d, want 0 for text-only transcription intent", len(sender.voice))
	}
	sender.mu.Unlock()

	sess, err := store.Load(session.SessionKey{ChatID: 1205, UserID: 0})
	if err != nil {
		t.Fatalf("Load() err = %v", err)
	}
	if !strings.Contains(sess.LastFloorMetadata, `"category":"pending_media_intent"`) {
		t.Fatalf("LastFloorMetadata = %q, want pending media intent", sess.LastFloorMetadata)
	}
	if !strings.Contains(sess.LastFloorMetadata, "next audio should be transcribed and answered in text") {
		t.Fatalf("LastFloorMetadata = %q, want next-audio transcription summary", sess.LastFloorMetadata)
	}
	if !strings.Contains(sess.LastFloorMetadata, `"claim"`) ||
		!strings.Contains(sess.LastFloorMetadata, `"intent":"pending_media_intent"`) ||
		!strings.Contains(sess.LastFloorMetadata, `"proposed_next_action":"transcribe_and_reply_text"`) {
		t.Fatalf("LastFloorMetadata = %q, want typed media intent claim", sess.LastFloorMetadata)
	}
}

func TestHandleInboundPendingAudioTranscriptionIntentForcesTextReply(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	provider.faceReplyText = "ready"
	var synthesized string
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	rt.ConfigureVoice(config.VoiceConfig{Mode: "auto"}, fakeTranscriber{text: "transcribed hello"}, fakeSynth{
		media:    core.Media{Type: "voice", Data: []byte("mp3"), MimeType: "audio/mpeg", Filename: "reply.mp3"},
		lastText: &synthesized,
	})

	_, err = rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID:     1206,
		SenderID:   1001,
		SenderName: "admin",
		MessageID:  83,
		Text:       "please transcribe the next audio",
	})
	if err != nil {
		t.Fatalf("HandleInbound() text err = %v", err)
	}

	provider.faceReplyText = "transcript reply"
	_, err = rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID:     1206,
		SenderID:   1001,
		SenderName: "admin",
		MessageID:  84,
		Artifacts:  []core.Artifact{{ID: "voice-4", Channel: "telegram", SourceType: "voice", Kind: "audio", Subtype: "voice_note", Data: []byte("ogg"), MimeType: "audio/ogg", Filename: "voice.ogg"}},
	})
	if err != nil {
		t.Fatalf("HandleInbound() voice err = %v", err)
	}

	sender.mu.Lock()
	defer sender.mu.Unlock()
	if len(sender.voice) != 0 {
		t.Fatalf("voice sends = %d, want 0 when pending transcription intent requests text", len(sender.voice))
	}
	if synthesized != "" {
		t.Fatalf("synthesized text = %q, want empty", synthesized)
	}
	if len(sender.sent) == 0 {
		t.Fatal("text sends = 0, want a text reply")
	}
	finalText := sender.sent[len(sender.sent)-1].Text
	if len(sender.edits) > 0 {
		finalText = sender.edits[len(sender.edits)-1].Text
	}
	if finalText != "transcript reply" {
		t.Fatalf("final text = %q, want transcript reply", finalText)
	}

	sess, err := store.Load(session.SessionKey{ChatID: 1206, UserID: 0})
	if err != nil {
		t.Fatalf("Load() err = %v", err)
	}
	if !strings.Contains(sess.LastFloorMetadata, `"category":"consumed_pending_media_intent"`) {
		t.Fatalf("LastFloorMetadata = %q, want consumed pending media intent", sess.LastFloorMetadata)
	}
	if strings.Contains(sess.LastFloorMetadata, `"category":"pending_media_intent"`) {
		t.Fatalf("LastFloorMetadata = %q, pending media intent should be consumed", sess.LastFloorMetadata)
	}
	if !strings.Contains(sess.LastFloorMetadata, `"intent":"consumed_pending_media_intent"`) ||
		!strings.Contains(sess.LastFloorMetadata, `"scope":"current_audio"`) {
		t.Fatalf("LastFloorMetadata = %q, want typed consumed media intent claim", sess.LastFloorMetadata)
	}
	if len(sess.Messages) < 4 || !strings.Contains(sess.Messages[2].Content, "transcribed hello") {
		t.Fatalf("session messages = %#v, want transcript in second user turn", sess.Messages)
	}
}

func TestHandleInboundSameTurnAudioTranscriptionIntentForcesTextReply(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	provider.faceReplyText = "same turn transcript"
	var synthesized string
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	rt.ConfigureVoice(config.VoiceConfig{Mode: "auto"}, fakeTranscriber{text: "spoken words"}, fakeSynth{
		media:    core.Media{Type: "voice", Data: []byte("mp3"), MimeType: "audio/mpeg", Filename: "reply.mp3"},
		lastText: &synthesized,
	})

	_, err = rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID:     1207,
		SenderID:   1001,
		SenderName: "admin",
		MessageID:  85,
		Text:       "please transcribe this audio",
		Artifacts:  []core.Artifact{{ID: "voice-5", Channel: "telegram", SourceType: "voice", Kind: "audio", Subtype: "voice_note", Data: []byte("ogg"), MimeType: "audio/ogg", Filename: "voice.ogg"}},
	})
	if err != nil {
		t.Fatalf("HandleInbound() err = %v", err)
	}

	sender.mu.Lock()
	defer sender.mu.Unlock()
	if len(sender.voice) != 0 {
		t.Fatalf("voice sends = %d, want 0 for same-turn transcription request", len(sender.voice))
	}
	if synthesized != "" {
		t.Fatalf("synthesized text = %q, want empty", synthesized)
	}
	if len(sender.sent) == 0 {
		t.Fatal("text sends = 0, want text reply")
	}
}

func TestHandleInboundSendsTelegramMediaReply(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	chartPath := filepath.Join(cfg.Agent.ExecRoot, "chart.png")
	if err := os.WriteFile(chartPath, []byte("png-bytes"), 0o600); err != nil {
		t.Fatalf("WriteFile(%q) err = %v", chartPath, err)
	}
	provider.replyText = `Here you go.
MEDIA: {"path":"chart.png"}`
	provider.faceReplyText = "Here you go."

	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	_, err = rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID:     1202,
		SenderID:   1001,
		SenderName: "admin",
		Text:       "send the chart",
		MessageID:  79,
	})
	if err != nil {
		t.Fatalf("HandleInbound() err = %v", err)
	}

	sender.mu.Lock()
	defer sender.mu.Unlock()
	if len(sender.sent) != 1 {
		t.Fatalf("sent len = %d, want 1", len(sender.sent))
	}
	if sender.sent[0].Text != "Here you go." {
		t.Fatalf("caption = %q, want %q", sender.sent[0].Text, "Here you go.")
	}
	if len(sender.sent[0].Media) != 1 {
		t.Fatalf("media len = %d, want 1", len(sender.sent[0].Media))
	}
	if sender.sent[0].Media[0].Type != "image" {
		t.Fatalf("media type = %q, want image", sender.sent[0].Media[0].Type)
	}
	if sender.sent[0].Media[0].Path != chartPath {
		t.Fatalf("media path = %q, want %q", sender.sent[0].Media[0].Path, chartPath)
	}
	if len(sender.voice) != 0 {
		t.Fatalf("voice sends = %d, want 0", len(sender.voice))
	}

	sess, err := store.Load(session.SessionKey{ChatID: 1202, UserID: 0})
	if err != nil {
		t.Fatalf("Load() err = %v", err)
	}
	if got := sess.Messages[len(sess.Messages)-1].FloorContent; got != "Here you go." {
		t.Fatalf("assistant floor = %q, want %q", got, "Here you go.")
	}
}

func TestHandleInboundMaterializesProviderImageGenerationMedia(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	provider.replyText = "Draft generated."
	provider.faceReplyText = "Draft generated."
	provider.replyMedia = []core.Media{{
		Type:     "image",
		Data:     []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'},
		MimeType: "image/png",
		Filename: "image-generation-call-ig_test.png",
	}}

	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	_, err = rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID:     1209,
		SenderID:   1001,
		SenderName: "admin",
		Text:       "generate the slide",
		MessageID:  90,
	})
	if err != nil {
		t.Fatalf("HandleInbound() err = %v", err)
	}

	sender.mu.Lock()
	defer sender.mu.Unlock()
	if len(sender.sent) != 1 {
		t.Fatalf("sent len = %d, want 1", len(sender.sent))
	}
	if len(sender.sent[0].Media) != 1 {
		t.Fatalf("media len = %d, want 1", len(sender.sent[0].Media))
	}
	media := sender.sent[0].Media[0]
	if media.Type != "image" || media.MimeType != "image/png" {
		t.Fatalf("media metadata = %#v", media)
	}
	if media.Path == "" || !strings.Contains(filepath.ToSlash(media.Path), "/generated/image-generation/image-generation-call-ig_test.png") {
		t.Fatalf("media path = %q, want generated image path", media.Path)
	}
	if len(media.Data) != 0 {
		t.Fatalf("sent media data len = %d, want materialized path only", len(media.Data))
	}
	raw, err := os.ReadFile(media.Path)
	if err != nil {
		t.Fatalf("read generated media: %v", err)
	}
	if string(raw) != string([]byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}) {
		t.Fatalf("generated bytes = %v", raw)
	}
	if !strings.HasPrefix(filepath.Clean(media.Path), filepath.Join(filepath.Clean(cfg.Agent.ExecRoot), "generated", "image-generation")) {
		t.Fatalf("media path = %q outside workspace generated root %q", media.Path, cfg.Agent.ExecRoot)
	}

	sess, err := store.Load(session.SessionKey{ChatID: 1209, UserID: 0})
	if err != nil {
		t.Fatalf("Load() err = %v", err)
	}
	if got := sess.Messages[len(sess.Messages)-1].FloorContent; got != "Draft generated." {
		t.Fatalf("assistant floor = %q, want Draft generated.", got)
	}
}

func TestHandleInboundMediaOnlyReplyOmitsNoResponseCaption(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	chartPath := filepath.Join(cfg.Agent.ExecRoot, "chart.png")
	if err := os.WriteFile(chartPath, []byte("png-bytes"), 0o600); err != nil {
		t.Fatalf("WriteFile(%q) err = %v", chartPath, err)
	}
	provider.replyText = `MEDIA: {"path":"chart.png"}`

	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	_, err = rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID:     1203,
		SenderID:   1001,
		SenderName: "admin",
		Text:       "send only the file",
		MessageID:  80,
	})
	if err != nil {
		t.Fatalf("HandleInbound() err = %v", err)
	}

	sender.mu.Lock()
	defer sender.mu.Unlock()
	if len(sender.sent) != 1 {
		t.Fatalf("sent len = %d, want 1", len(sender.sent))
	}
	if sender.sent[0].Text != "" {
		t.Fatalf("caption = %q, want empty", sender.sent[0].Text)
	}
	if len(sender.sent[0].Media) != 1 {
		t.Fatalf("media len = %d, want 1", len(sender.sent[0].Media))
	}
	if sender.sent[0].Media[0].Path != chartPath {
		t.Fatalf("media path = %q, want %q", sender.sent[0].Media[0].Path, chartPath)
	}

	sess, err := store.Load(session.SessionKey{ChatID: 1203, UserID: 0})
	if err != nil {
		t.Fatalf("Load() err = %v", err)
	}
	if got := sess.Messages[len(sess.Messages)-1].Content; got != "" {
		t.Fatalf("assistant content = %q, want empty", got)
	}
	if got := sess.Messages[len(sess.Messages)-1].FloorContent; got != "" {
		t.Fatalf("assistant floor = %q, want empty", got)
	}
}

func TestHandleInboundExplicitMediaBeatsVoiceSynthesis(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	chartPath := filepath.Join(cfg.Agent.ExecRoot, "chart.png")
	if err := os.WriteFile(chartPath, []byte("png-bytes"), 0o600); err != nil {
		t.Fatalf("WriteFile(%q) err = %v", chartPath, err)
	}
	provider.replyText = `MEDIA: {"path":"chart.png"}`
	var synthesized string

	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	rt.ConfigureVoice(config.VoiceConfig{Mode: "auto"}, fakeTranscriber{text: "transcribed hello"}, fakeSynth{
		media:    core.Media{Type: "voice", Data: []byte("mp3"), MimeType: "audio/mpeg", Filename: "reply.mp3"},
		lastText: &synthesized,
	})

	_, err = rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID:     1204,
		SenderID:   1001,
		SenderName: "admin",
		MessageID:  81,
		Artifacts:  []core.Artifact{{ID: "voice-3", Channel: "telegram", SourceType: "voice", Kind: "audio", Subtype: "voice_note", Data: []byte("ogg"), MimeType: "audio/ogg", Filename: "voice.ogg"}},
	})
	if err != nil {
		t.Fatalf("HandleInbound() err = %v", err)
	}

	sender.mu.Lock()
	defer sender.mu.Unlock()
	if len(sender.voice) != 0 {
		t.Fatalf("voice sends = %d, want 0", len(sender.voice))
	}
	if len(sender.sent) != 1 {
		t.Fatalf("text/media sends = %d, want 1", len(sender.sent))
	}
	if len(sender.sent[0].Media) != 1 {
		t.Fatalf("media len = %d, want 1", len(sender.sent[0].Media))
	}
	if sender.sent[0].Media[0].Path != chartPath {
		t.Fatalf("media path = %q, want %q", sender.sent[0].Media[0].Path, chartPath)
	}
	if synthesized != "" {
		t.Fatalf("synthesized text = %q, want empty", synthesized)
	}
}

func TestHandleInboundVoiceFallbackSerializerUsesVoiceOverlay(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	provider.proposalReplyText = "Keep it steady and direct."
	provider.replyText = strings.Join([]string{
		"FACTS:",
		"- The repo was inspected.",
		"COMMITMENTS:",
		"- Keep the answer focused on the next move.",
		"REFUSALS:",
		"- Pretend the tests passed when they did not.",
		"SCENE_CONSTRAINTS:",
		"- Do not become lyrical.",
	}, "\n")
	provider.faceErr = errors.New("face unavailable")

	var synthesized string
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	rt.ConfigureVoice(config.VoiceConfig{Mode: "auto"}, fakeTranscriber{text: "help me think this through"}, fakeSynth{
		media:    core.Media{Type: "voice", Data: []byte("mp3"), MimeType: "audio/mpeg", Filename: "reply.mp3"},
		lastText: &synthesized,
	})

	_, err = rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID:     1202,
		SenderID:   1001,
		SenderName: "admin",
		MessageID:  79,
		Artifacts:  []core.Artifact{{ID: "voice-3", Channel: "telegram", SourceType: "voice", Kind: "audio", Subtype: "voice_note", Data: []byte("ogg"), MimeType: "audio/ogg", Filename: "voice.ogg"}},
	})
	if err != nil {
		t.Fatalf("HandleInbound() err = %v", err)
	}

	want := "Here's what matters: The repo was inspected. I'll keep the answer focused on the next move. I won't pretend the tests passed when they did not."
	if synthesized != want {
		t.Fatalf("synthesized text = %q, want %q", synthesized, want)
	}

	sender.mu.Lock()
	defer sender.mu.Unlock()
	if len(sender.sent) != 0 {
		t.Fatalf("text sends = %d, want 0 in voice fallback mode", len(sender.sent))
	}
	if len(sender.voice) != 1 {
		t.Fatalf("voice sends = %d, want 1", len(sender.voice))
	}

	sess, err := store.Load(session.SessionKey{ChatID: 1202, UserID: 0})
	if err != nil {
		t.Fatalf("Load() err = %v", err)
	}
	if len(sess.Messages) < 2 || sess.Messages[1].Content != want {
		t.Fatalf("session assistant text = %q, want voice-shaped fallback transcript", sess.Messages[1].Content)
	}
}

func TestHandleInboundAutoModeTextInputStaysText(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	provider.replyText = "plain text reply"
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	rt.ConfigureVoice(config.VoiceConfig{Mode: "auto"}, fakeTranscriber{text: "unused"}, fakeSynth{
		media: core.Media{Type: "voice", Data: []byte("mp3"), MimeType: "audio/mpeg", Filename: "reply.mp3"},
	})

	_, err = rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID:     1202,
		SenderID:   1001,
		SenderName: "admin",
		MessageID:  79,
		Text:       "hello there",
	})
	if err != nil {
		t.Fatalf("HandleInbound() err = %v", err)
	}

	sender.mu.Lock()
	defer sender.mu.Unlock()
	if len(sender.voice) != 0 {
		t.Fatalf("voice sends = %d, want 0 for text input in auto mode", len(sender.voice))
	}
	if len(sender.sent) != 1 {
		t.Fatalf("text sends = %d, want 1", len(sender.sent))
	}
	finalText := sender.sent[0].Text
	if len(sender.edits) > 0 {
		finalText = sender.edits[len(sender.edits)-1].Text
	}
	if finalText != "plain text reply" {
		t.Fatalf("final text = %q, want plain text reply", finalText)
	}
}

func TestPrepareInboundTurnProcessesArtifacts(t *testing.T) {
	cfg, store, provider, sender := buildRuntimeFixtures(t)
	_ = store
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	rt.ConfigureVoice(config.VoiceConfig{Mode: "auto"}, fakeTranscriber{text: "spoken transcript"}, fakeSynth{})
	scope, err := rt.scopeForPrincipal(principal.Principal{TelegramUserID: 1001, Role: principal.RoleAdmin})
	if err != nil {
		t.Fatalf("scopeForPrincipal() err = %v", err)
	}

	prepared, err := rt.prepareInboundTurn(context.Background(), scope, core.InboundMessage{
		ChatID:    1300,
		SenderID:  1001,
		MessageID: 1,
		Artifacts: []core.Artifact{
			{ID: "img-1", Channel: "telegram", SourceType: "photo", Kind: "image", MimeType: "image/png", Filename: "screen.png", Data: []byte("image-bytes")},
			{ID: "voice-1", Channel: "telegram", SourceType: "voice", Kind: "audio", Subtype: "voice_note", MimeType: "audio/ogg", Filename: "voice.ogg", Data: []byte("voice-bytes")},
			{ID: "video-1", Channel: "telegram", SourceType: "video", Kind: "video", MimeType: "video/mp4", Filename: "clip.mp4", SizeBytes: 42},
			{ID: "loc-1", Channel: "telegram", SourceType: "location", Kind: "structured", Metadata: map[string]string{"latitude": "40.0", "longitude": "-73.0"}},
		},
	})
	if err != nil {
		t.Fatalf("prepareInboundTurn() err = %v", err)
	}

	if !prepared.MediaAttached || prepared.MediaMode != "vision" {
		t.Fatalf("prepared media = attached:%t mode:%q, want vision artifact handling", prepared.MediaAttached, prepared.MediaMode)
	}
	if len(prepared.AgentMedia) != 1 {
		t.Fatalf("agent media len = %d, want 1 image artifact for vision", len(prepared.AgentMedia))
	}
	if !strings.Contains(prepared.UserText, "spoken transcript") {
		t.Fatalf("user text = %q, want audio transcript", prepared.UserText)
	}
	if !strings.Contains(prepared.UserText, "clip.mp4") || !strings.Contains(prepared.UserText, "location") {
		t.Fatalf("user text = %q, want video and location summaries", prepared.UserText)
	}
	if !strings.Contains(prepared.LedgerText, "[image attached]") || !strings.Contains(prepared.LedgerText, "[voice attached]") || !strings.Contains(prepared.LedgerText, "[video attached]") {
		t.Fatalf("ledger text = %q, want artifact markers", prepared.LedgerText)
	}
}
