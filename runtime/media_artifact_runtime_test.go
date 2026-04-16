//go:build linux

package runtime

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/idolum-ai/aphelion/config"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/session"
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

func TestHandleInboundSendsTelegramMediaReply(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	chartPath := filepath.Join(cfg.Agent.Workspace, "chart.png")
	if err := os.WriteFile(chartPath, []byte("png-bytes"), 0o600); err != nil {
		t.Fatalf("WriteFile(%q) err = %v", chartPath, err)
	}
	provider.replyText = "Here you go.\nMEDIA: chart.png"
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

func TestHandleInboundMediaOnlyReplyOmitsNoResponseCaption(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	chartPath := filepath.Join(cfg.Agent.Workspace, "chart.png")
	if err := os.WriteFile(chartPath, []byte("png-bytes"), 0o600); err != nil {
		t.Fatalf("WriteFile(%q) err = %v", chartPath, err)
	}
	provider.replyText = "MEDIA: chart.png"

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
	chartPath := filepath.Join(cfg.Agent.Workspace, "chart.png")
	if err := os.WriteFile(chartPath, []byte("png-bytes"), 0o600); err != nil {
		t.Fatalf("WriteFile(%q) err = %v", chartPath, err)
	}
	provider.replyText = "MEDIA: chart.png"
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

func TestHandleInboundPersistsArtifactReferencesInFloorMetadata(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	provider.replyText = "artifact-aware reply"
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	_, err = rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID:     1301,
		SenderID:   1001,
		SenderName: "admin",
		MessageID:  1,
		Text:       "what do you make of this screenshot?",
		Artifacts: []core.Artifact{
			{ID: "img-2", Channel: "telegram", SourceType: "photo", Kind: "image", MimeType: "image/png", Filename: "screen.png", Data: []byte("image-bytes")},
		},
	})
	if err != nil {
		t.Fatalf("HandleInbound() err = %v", err)
	}

	sess, err := store.Load(session.SessionKey{ChatID: 1301, UserID: 0})
	if err != nil {
		t.Fatalf("Load() err = %v", err)
	}
	if !strings.Contains(sess.LastFloorMetadata, "\"artifact_id\":\"img-2\"") {
		t.Fatalf("LastFloorMetadata = %q, want artifact reference", sess.LastFloorMetadata)
	}
	if !strings.Contains(sess.LastFloorMetadata, "\"handling\":\"attach_for_vision\"") {
		t.Fatalf("LastFloorMetadata = %q, want handling decision", sess.LastFloorMetadata)
	}
	if !strings.Contains(sess.LastFloorMetadata, "\"retention\":\"session_reference\"") {
		t.Fatalf("LastFloorMetadata = %q, want retention decision", sess.LastFloorMetadata)
	}
	if !strings.Contains(sess.LastFloorMetadata, "\"fetch_state\":\"fetched_memory\"") {
		t.Fatalf("LastFloorMetadata = %q, want fetch state", sess.LastFloorMetadata)
	}
	if !strings.Contains(sess.LastFloorMetadata, "decision_summary") {
		t.Fatalf("LastFloorMetadata = %q, want decision summary", sess.LastFloorMetadata)
	}
}

func TestPrepareInboundTurnPDFExtractionFailureFallsBackToPlaceholder(t *testing.T) {
	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	scope, err := rt.scopeForPrincipal(principal.Principal{TelegramUserID: 1001, Role: principal.RoleAdmin})
	if err != nil {
		t.Fatalf("scopeForPrincipal() err = %v", err)
	}

	prepared, err := rt.prepareInboundTurn(context.Background(), scope, core.InboundMessage{
		ChatID:    409,
		SenderID:  1001,
		MessageID: 1,
		Artifacts: []core.Artifact{{
			ID:         "pdf-409",
			Channel:    "telegram",
			SourceType: "document",
			Kind:       "document",
			Subtype:    "pdf",
			Data:       []byte("not-a-real-pdf"),
			MimeType:   "application/pdf",
			Filename:   "broken.pdf",
		}},
	})
	if err != nil {
		t.Fatalf("prepareInboundTurn() err = %v", err)
	}
	if !strings.Contains(prepared.UserText, "PDF attached") {
		t.Fatalf("user text = %q, want PDF placeholder", prepared.UserText)
	}
	if !strings.Contains(prepared.LedgerText, "[pdf attached]") {
		t.Fatalf("ledger text = %q, want pdf attached marker", prepared.LedgerText)
	}
}

func TestPrepareInboundTurnHydratesTelegramArtifactsAtRuntime(t *testing.T) {
	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	rt.ConfigureVoice(config.VoiceConfig{Mode: "auto"}, fakeTranscriber{text: "spoken transcript"}, fakeSynth{})
	fetcher := &fakeInboundFetcher{data: map[string][]byte{
		"photo-file": []byte("image-bytes"),
		"voice-file": []byte("voice-bytes"),
	}}
	rt.inbound = fetcher
	scope, err := rt.scopeForPrincipal(principal.Principal{TelegramUserID: 1001, Role: principal.RoleAdmin})
	if err != nil {
		t.Fatalf("scopeForPrincipal() err = %v", err)
	}

	prepared, err := rt.prepareInboundTurn(context.Background(), scope, core.InboundMessage{
		ChatID:    1400,
		SenderID:  1001,
		MessageID: 1,
		Artifacts: []core.Artifact{
			{ID: "img-remote", Channel: "telegram", RemoteID: "photo-file", SourceType: "photo", Kind: "image", MimeType: "image/png", Filename: "screen.png"},
			{ID: "voice-remote", Channel: "telegram", RemoteID: "voice-file", SourceType: "voice", Kind: "audio", Subtype: "voice_note", MimeType: "audio/ogg", Filename: "voice.ogg"},
		},
	})
	if err != nil {
		t.Fatalf("prepareInboundTurn() err = %v", err)
	}
	if len(fetcher.requests) != 2 {
		t.Fatalf("fetch requests = %#v, want two runtime downloads", fetcher.requests)
	}
	if len(prepared.AgentMedia) != 1 || string(prepared.AgentMedia[0].Data) != "image-bytes" {
		t.Fatalf("agent media = %#v, want hydrated image bytes", prepared.AgentMedia)
	}
	if !strings.Contains(prepared.UserText, "spoken transcript") {
		t.Fatalf("user text = %q, want hydrated voice transcript", prepared.UserText)
	}
}

func TestPrepareInboundTurnLeavesMetadataOnlyArtifactsUnfetched(t *testing.T) {
	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	fetcher := &fakeInboundFetcher{data: map[string][]byte{
		"video-file": []byte("video-bytes"),
	}}
	rt.inbound = fetcher
	scope, err := rt.scopeForPrincipal(principal.Principal{TelegramUserID: 1001, Role: principal.RoleAdmin})
	if err != nil {
		t.Fatalf("scopeForPrincipal() err = %v", err)
	}

	prepared, err := rt.prepareInboundTurn(context.Background(), scope, core.InboundMessage{
		ChatID:    1401,
		SenderID:  1001,
		MessageID: 1,
		Artifacts: []core.Artifact{{
			ID:         "video-remote",
			Channel:    "telegram",
			RemoteID:   "video-file",
			SourceType: "video",
			Kind:       "video",
			MimeType:   "video/mp4",
			Filename:   "clip.mp4",
		}},
	})
	if err != nil {
		t.Fatalf("prepareInboundTurn() err = %v", err)
	}
	if len(fetcher.requests) != 0 {
		t.Fatalf("fetch requests = %#v, want none for metadata-only artifact", fetcher.requests)
	}
	if !strings.Contains(prepared.UserText, "clip.mp4") {
		t.Fatalf("user text = %q, want metadata note", prepared.UserText)
	}
}

func TestPrepareInboundTurnPersistsFetchedArtifactToLocalPath(t *testing.T) {
	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	fetcher := &fakeInboundFetcher{data: map[string][]byte{
		"doc-file": []byte("hello world"),
	}}
	rt.inbound = fetcher
	scope, err := rt.scopeForPrincipal(principal.Principal{TelegramUserID: 1001, Role: principal.RoleAdmin})
	if err != nil {
		t.Fatalf("scopeForPrincipal() err = %v", err)
	}

	prepared, err := rt.prepareInboundTurn(context.Background(), scope, core.InboundMessage{
		ChatID:    1402,
		SenderID:  1001,
		MessageID: 1,
		Artifacts: []core.Artifact{{
			ID:         "doc-remote",
			Channel:    "telegram",
			RemoteID:   "doc-file",
			SourceType: "document",
			Kind:       "document",
			Subtype:    "text",
			Filename:   "notes.txt",
			MimeType:   "text/plain",
		}},
	})
	if err != nil {
		t.Fatalf("prepareInboundTurn() err = %v", err)
	}
	if len(prepared.ArtifactRefs) != 1 {
		t.Fatalf("artifact refs len = %d, want 1", len(prepared.ArtifactRefs))
	}
	root := filepath.Join(cfg.Agent.ExecRoot, ".aphelion", "inbound")
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("ReadDir(%q) err = %v", root, err)
	}
	if len(entries) != 1 {
		t.Fatalf("inbound materialized files = %d, want 1", len(entries))
	}
	path := filepath.Join(root, entries[0].Name())
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%q) err = %v", path, err)
	}
	if string(data) != "hello world" {
		t.Fatalf("materialized file = %q, want hello world", string(data))
	}
}

func TestPrepareInboundTurnDoesNotPersistUnfetchedArtifact(t *testing.T) {
	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	fetcher := &fakeInboundFetcher{data: map[string][]byte{
		"video-file": []byte("video-bytes"),
	}}
	rt.inbound = fetcher
	scope, err := rt.scopeForPrincipal(principal.Principal{TelegramUserID: 1001, Role: principal.RoleAdmin})
	if err != nil {
		t.Fatalf("scopeForPrincipal() err = %v", err)
	}

	_, err = rt.prepareInboundTurn(context.Background(), scope, core.InboundMessage{
		ChatID:    1403,
		SenderID:  1001,
		MessageID: 1,
		Artifacts: []core.Artifact{{
			ID:         "video-remote-2",
			Channel:    "telegram",
			RemoteID:   "video-file",
			SourceType: "video",
			Kind:       "video",
			Filename:   "clip.mp4",
			MimeType:   "video/mp4",
		}},
	})
	if err != nil {
		t.Fatalf("prepareInboundTurn() err = %v", err)
	}
	root := filepath.Join(cfg.Agent.ExecRoot, ".aphelion", "inbound")
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatalf("inbound root stat err = %v, want not-exist", err)
	}
}

func TestHandleInboundPersistsArtifactDecisionHiddenInput(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	provider.replyText = "artifact decision reply"
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	fetcher := &fakeInboundFetcher{data: map[string][]byte{
		"doc-file-hidden": []byte("hello world"),
	}}
	rt.inbound = fetcher

	_, err = rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID:     1302,
		SenderID:   1001,
		SenderName: "admin",
		MessageID:  1,
		Text:       "hold onto this text file",
		Artifacts: []core.Artifact{{
			ID:         "doc-hidden",
			Channel:    "telegram",
			RemoteID:   "doc-file-hidden",
			SourceType: "document",
			Kind:       "document",
			Subtype:    "text",
			Filename:   "notes.txt",
			MimeType:   "text/plain",
		}},
	})
	if err != nil {
		t.Fatalf("HandleInbound() err = %v", err)
	}

	sess, err := store.Load(session.SessionKey{ChatID: 1302, UserID: 0})
	if err != nil {
		t.Fatalf("Load() err = %v", err)
	}
	if !strings.Contains(sess.LastFloorMetadata, "artifact_retention_decision") {
		t.Fatalf("LastFloorMetadata = %q, want artifact retention decision hidden input", sess.LastFloorMetadata)
	}
	if !strings.Contains(sess.LastFloorMetadata, "fetched_local") {
		t.Fatalf("LastFloorMetadata = %q, want fetched_local decision trail", sess.LastFloorMetadata)
	}
}

func TestPrepareInboundTurnExplicitTurnRetentionSkipsLocalPersistence(t *testing.T) {
	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	fetcher := &fakeInboundFetcher{data: map[string][]byte{
		"doc-turn": []byte("hello turn"),
	}}
	rt.inbound = fetcher
	scope, err := rt.scopeForPrincipal(principal.Principal{TelegramUserID: 1001, Role: principal.RoleAdmin})
	if err != nil {
		t.Fatalf("scopeForPrincipal() err = %v", err)
	}

	prepared, err := rt.prepareInboundTurn(context.Background(), scope, core.InboundMessage{
		ChatID:    1501,
		SenderID:  1001,
		MessageID: 1,
		Artifacts: []core.Artifact{{
			ID:         "doc-turn",
			Channel:    "telegram",
			RemoteID:   "doc-turn",
			SourceType: "document",
			Kind:       "document",
			Subtype:    "text",
			Filename:   "notes.txt",
			MimeType:   "text/plain",
			Metadata: map[string]string{
				"aphelion_retention_choice": "turn",
				"aphelion_materialize":      "memory_only",
			},
			DefaultRetention: "ephemeral",
		}},
	})
	if err != nil {
		t.Fatalf("prepareInboundTurn() err = %v", err)
	}
	if len(prepared.ArtifactRefs) != 1 {
		t.Fatalf("artifact refs len = %d, want 1", len(prepared.ArtifactRefs))
	}
	if got := prepared.ArtifactRefs[0].FetchState; got != "fetched_memory" {
		t.Fatalf("FetchState = %q, want fetched_memory", got)
	}
	root := filepath.Join(cfg.Agent.ExecRoot, ".aphelion", "inbound")
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatalf("inbound root stat err = %v, want not-exist", err)
	}
}

func TestPrepareInboundTurnExplicitLocalRetentionForcesFetchAndPersistence(t *testing.T) {
	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	fetcher := &fakeInboundFetcher{data: map[string][]byte{
		"video-local": []byte("video-bytes"),
	}}
	rt.inbound = fetcher
	scope, err := rt.scopeForPrincipal(principal.Principal{TelegramUserID: 1001, Role: principal.RoleAdmin})
	if err != nil {
		t.Fatalf("scopeForPrincipal() err = %v", err)
	}

	prepared, err := rt.prepareInboundTurn(context.Background(), scope, core.InboundMessage{
		ChatID:    1502,
		SenderID:  1001,
		MessageID: 1,
		Artifacts: []core.Artifact{{
			ID:         "video-local",
			Channel:    "telegram",
			RemoteID:   "video-local",
			SourceType: "video",
			Kind:       "video",
			Filename:   "clip.mp4",
			MimeType:   "video/mp4",
			Metadata: map[string]string{
				"aphelion_retention_choice": "local",
				"aphelion_materialize":      "local",
			},
			DefaultRetention: "child_local",
			RetentionCeiling: "child_local",
		}},
	})
	if err != nil {
		t.Fatalf("prepareInboundTurn() err = %v", err)
	}
	if len(prepared.ArtifactRefs) != 1 {
		t.Fatalf("artifact refs len = %d, want 1", len(prepared.ArtifactRefs))
	}
	if got := prepared.ArtifactRefs[0].FetchState; got != "fetched_local" {
		t.Fatalf("FetchState = %q, want fetched_local", got)
	}
	if got := prepared.ArtifactRefs[0].Retention; got != "child_local" {
		t.Fatalf("Retention = %q, want child_local", got)
	}
	root := filepath.Join(cfg.Agent.ExecRoot, ".aphelion", "inbound")
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("ReadDir(%q) err = %v", root, err)
	}
	if len(entries) != 1 {
		t.Fatalf("inbound materialized files = %d, want 1", len(entries))
	}
}
