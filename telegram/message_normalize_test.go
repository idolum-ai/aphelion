//go:build linux

package telegram

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestNormalizeMessagePrivate(t *testing.T) {
	now := time.Now().Unix()
	msg := &Message{
		MessageID: 10,
		Date:      now,
		Chat:      &Chat{ID: 7, Type: "private"},
		From:      &User{ID: 3, Username: "alice", FirstName: "Alice"},
		Text:      "hello",
	}

	got := NormalizeMessage(msg)
	if got == nil {
		t.Fatal("expected message to be normalized")
	}
	if got.ChatID != 7 {
		t.Fatalf("chat id = %d, want 7", got.ChatID)
	}
	if got.SenderName != "alice" {
		t.Fatalf("sender name = %q, want %q", got.SenderName, "alice")
	}
	if got.Text != "hello" {
		t.Fatalf("text = %q, want %q", got.Text, "hello")
	}
	if got.MessageID != 10 {
		t.Fatalf("message id = %d, want 10", got.MessageID)
	}
	if got.Timestamp.Unix() != now {
		t.Fatalf("timestamp = %v, want unix %d", got.Timestamp, now)
	}
}

func TestNormalizeMessageReactionPrivate(t *testing.T) {
	now := time.Now().Unix()
	update := &MessageReactionUpdated{
		Chat:      &Chat{ID: 7, Type: "private"},
		User:      &User{ID: 3, Username: "alice"},
		MessageID: 42,
		Date:      now,
		OldReaction: []ReactionType{
			{Type: "emoji", Emoji: "👍"},
		},
		NewReaction: []ReactionType{
			{Type: "emoji", Emoji: "🔥"},
		},
	}

	got := NormalizeMessageReaction(update)
	if got == nil {
		t.Fatal("expected reaction update to be normalized")
	}
	if got.ChatID != 7 || got.SenderID != 3 || got.MessageID != 42 {
		t.Fatalf("inbound = %#v, want chat 7 sender 3 reacted message 42", got)
	}
	if got.Reaction == nil {
		t.Fatalf("Reaction = nil, want reaction payload")
	}
	if got.Reaction.MessageID != 42 || len(got.Reaction.Old) != 1 || got.Reaction.Old[0] != "👍" || len(got.Reaction.New) != 1 || got.Reaction.New[0] != "🔥" {
		t.Fatalf("Reaction = %#v, want old thumbs up/new fire", got.Reaction)
	}
	if !strings.Contains(got.Text, "reaction_update") || !strings.Contains(got.Text, "message_id=42") || !strings.Contains(got.Text, "new=🔥") {
		t.Fatalf("Text = %q, want synthesized reaction text", got.Text)
	}
}

func TestNormalizeMessageReactionRemoval(t *testing.T) {
	update := &MessageReactionUpdated{
		Chat:      &Chat{ID: 7, Type: "private"},
		User:      &User{ID: 3, Username: "alice"},
		MessageID: 42,
		Date:      time.Now().Unix(),
		OldReaction: []ReactionType{
			{Type: "emoji", Emoji: "👍"},
		},
		NewReaction: nil,
	}

	got := NormalizeMessageReaction(update)
	if got == nil || got.Reaction == nil {
		t.Fatalf("NormalizeMessageReaction() = %#v, want reaction removal", got)
	}
	if !strings.Contains(got.Text, "reaction_removed") {
		t.Fatalf("Text = %q, want reaction_removed", got.Text)
	}
}

func TestNormalizeMessageSkipsNonPrivate(t *testing.T) {
	msg := &Message{
		Chat: &Chat{ID: 1, Type: "group"},
		Text: "hi",
	}
	if NormalizeMessage(msg) != nil {
		t.Fatal("expected non-private message to be ignored")
	}
}

func TestNormalizeMessageVoiceOnly(t *testing.T) {
	now := time.Now().Unix()
	msg := &Message{
		MessageID: 11,
		Date:      now,
		Chat:      &Chat{ID: 7, Type: "private"},
		From:      &User{ID: 3, Username: "alice"},
		Voice:     &Voice{FileID: "voice-file", MimeType: "audio/ogg"},
	}

	got := NormalizeMessage(msg)
	if got == nil {
		t.Fatal("expected voice message to be normalized")
	}
	if got.Text != "" {
		t.Fatalf("text = %q, want empty for voice-only input", got.Text)
	}
}

func TestNormalizeMessagePhotoOnly(t *testing.T) {
	now := time.Now().Unix()
	msg := &Message{
		MessageID: 12,
		Date:      now,
		Chat:      &Chat{ID: 7, Type: "private"},
		From:      &User{ID: 3, Username: "alice"},
		Photo:     []PhotoSize{{FileID: "p1", FileSize: 123}, {FileID: "p2", FileSize: 456}},
	}

	got := NormalizeMessage(msg)
	if got == nil {
		t.Fatal("expected photo message to be normalized")
	}
	if got.Text != "" {
		t.Fatalf("text = %q, want empty for photo-only input", got.Text)
	}
}

func TestNormalizeMessagePDFDocumentOnly(t *testing.T) {
	now := time.Now().Unix()
	msg := &Message{
		MessageID: 13,
		Date:      now,
		Chat:      &Chat{ID: 7, Type: "private"},
		From:      &User{ID: 3, Username: "alice"},
		Document:  &Document{FileID: "doc1", FileName: "notes.pdf", MimeType: "application/pdf"},
	}

	got := NormalizeMessage(msg)
	if got == nil {
		t.Fatal("expected pdf document message to be normalized")
	}
}

func TestNormalizeMessageAudioOnly(t *testing.T) {
	now := time.Now().Unix()
	msg := &Message{
		MessageID: 14,
		Date:      now,
		Chat:      &Chat{ID: 7, Type: "private"},
		From:      &User{ID: 3, Username: "alice"},
		Audio:     &Audio{FileID: "audio1", FileName: "memo.mp3", MimeType: "audio/mpeg"},
	}

	got := NormalizeMessage(msg)
	if got == nil {
		t.Fatal("expected audio message to be normalized")
	}
	if got.Text != "" {
		t.Fatalf("text = %q, want empty for audio-only input", got.Text)
	}
}

func TestNormalizeMessageVideoOnly(t *testing.T) {
	now := time.Now().Unix()
	msg := &Message{
		MessageID: 15,
		Date:      now,
		Chat:      &Chat{ID: 7, Type: "private"},
		From:      &User{ID: 3, Username: "alice"},
		Video:     &Video{FileID: "video1", FileName: "clip.mp4", MimeType: "video/mp4"},
	}

	got := NormalizeMessage(msg)
	if got == nil {
		t.Fatal("expected video message to be normalized")
	}
	if got.Text != "" {
		t.Fatalf("text = %q, want empty for video-only input", got.Text)
	}
}

func TestNormalizeMessageStickerOnly(t *testing.T) {
	now := time.Now().Unix()
	msg := &Message{
		MessageID: 16,
		Date:      now,
		Chat:      &Chat{ID: 7, Type: "private"},
		From:      &User{ID: 3, Username: "alice"},
		Sticker:   &Sticker{FileID: "sticker1", MimeType: "image/webp"},
	}

	got := NormalizeMessage(msg)
	if got == nil {
		t.Fatal("expected sticker message to be normalized")
	}
}

func TestNormalizeMessageLocationOnly(t *testing.T) {
	now := time.Now().Unix()
	msg := &Message{
		MessageID: 17,
		Date:      now,
		Chat:      &Chat{ID: 7, Type: "private"},
		From:      &User{ID: 3, Username: "alice"},
		Location:  &Location{Latitude: 40.0, Longitude: -73.0},
	}

	got := NormalizeMessage(msg)
	if got == nil {
		t.Fatal("expected location message to be normalized")
	}
}

func TestNormalizeMessageIncludesReplyContextAndReplyTo(t *testing.T) {
	now := time.Now().Unix()
	msg := &Message{
		MessageID: 30,
		Date:      now,
		Chat:      &Chat{ID: 7, Type: "private"},
		From:      &User{ID: 3, Username: "alice"},
		Text:      "yes, that works",
		ReplyToMessage: &Message{
			MessageID: 28,
			From:      &User{ID: 9, Username: "idolum"},
			Text:      "Please confirm whether we should proceed with the deploy.",
		},
	}

	got := NormalizeMessage(msg)
	if got == nil {
		t.Fatal("expected message to be normalized")
	}
	if got.ReplyTo == nil || *got.ReplyTo != 28 {
		t.Fatalf("ReplyTo = %#v, want 28", got.ReplyTo)
	}
	if !strings.Contains(got.Text, "yes, that works") {
		t.Fatalf("text = %q, want user reply content", got.Text)
	}
	if !strings.Contains(got.Text, "Reply context:") {
		t.Fatalf("text = %q, want Reply context section", got.Text)
	}
	if !strings.Contains(got.Text, "idolum: Please confirm whether we should proceed with the deploy.") {
		t.Fatalf("text = %q, want quoted reply context", got.Text)
	}
}

func TestNormalizeMessageKeepsReplyContextLiteralText(t *testing.T) {
	now := time.Now().Unix()
	msg := &Message{
		MessageID: 33,
		Date:      now,
		Chat:      &Chat{ID: 7, Type: "private"},
		From:      &User{ID: 3, Username: "alice"},
		Text:      "continue",
		ReplyToMessage: &Message{
			MessageID: 32,
			From:      &User{ID: 9, Username: "idolum"},
			Text:      "Correction note.\nPlan: Child Telegram Runner (R1)",
		},
	}

	got := NormalizeMessage(msg)
	if got == nil {
		t.Fatal("expected message to be normalized")
	}
	if !strings.Contains(got.Text, "idolum: Correction note. Plan: Child Telegram Runner (R1)") {
		t.Fatalf("text = %q, want literal reply context content", got.Text)
	}
}

func TestNormalizeMessageDropsReplyContextOnlyTextWithoutArtifacts(t *testing.T) {
	now := time.Now().Unix()
	msg := &Message{
		MessageID: 31,
		Date:      now,
		Chat:      &Chat{ID: 7, Type: "private"},
		From:      &User{ID: 3, Username: "alice"},
		ReplyToMessage: &Message{
			MessageID: 30,
			From:      &User{ID: 9, Username: "idolum"},
			Text:      "Can you confirm?",
		},
	}

	got := NormalizeMessage(msg)
	if got != nil {
		t.Fatalf("NormalizeMessage() = %#v, want nil for reply-context-only message without artifacts", got)
	}
}

func TestNormalizeMessageTruncatesReplyContextSnippet(t *testing.T) {
	now := time.Now().Unix()
	longReply := strings.Repeat("x", replyContextSnippetMaxRunes+80)
	msg := &Message{
		MessageID: 32,
		Date:      now,
		Chat:      &Chat{ID: 7, Type: "private"},
		From:      &User{ID: 3, Username: "alice"},
		Text:      "approved",
		ReplyToMessage: &Message{
			MessageID: 30,
			From:      &User{ID: 9, Username: "idolum"},
			Text:      longReply,
		},
	}

	got := NormalizeMessage(msg)
	if got == nil {
		t.Fatal("expected message to be normalized")
	}
	if !strings.Contains(got.Text, "Reply context:") {
		t.Fatalf("text = %q, want reply context section", got.Text)
	}
	if !strings.Contains(got.Text, "...") {
		t.Fatalf("text = %q, want truncated reply context suffix", got.Text)
	}
	if strings.Contains(got.Text, longReply) {
		t.Fatalf("text unexpectedly contains full long reply context")
	}
}

func TestNormalizeArtifactsEmitsMetadataOnlyRemoteDescriptors(t *testing.T) {
	p := &Poller{}
	artifacts, err := p.normalizeArtifacts(context.Background(), &Message{
		Caption:  "look at this",
		Photo:    []PhotoSize{{FileID: "p1", FileSize: 123}, {FileID: "p2", FileSize: 456}},
		Voice:    &Voice{FileID: "voice-file", MimeType: "audio/ogg", FileSize: 11},
		Document: &Document{FileID: "doc1", FileName: "notes.pdf", MimeType: "application/pdf", FileSize: 99},
	})
	if err != nil {
		t.Fatalf("normalizeArtifacts() err = %v", err)
	}
	if len(artifacts) != 3 {
		t.Fatalf("artifacts len = %d, want 3", len(artifacts))
	}
	for _, artifact := range artifacts {
		if len(artifact.Data) != 0 {
			t.Fatalf("artifact %s has eager data bytes, want metadata-only descriptor", artifact.ID)
		}
		if artifact.RemoteID == "" && artifact.Kind != "structured" {
			t.Fatalf("artifact %#v missing remote id", artifact)
		}
	}
	if artifacts[0].RemoteID != "voice-file" {
		t.Fatalf("voice remote id = %q, want voice-file", artifacts[0].RemoteID)
	}
}
