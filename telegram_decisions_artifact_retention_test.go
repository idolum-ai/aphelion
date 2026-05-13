//go:build linux

package main

import (
	"context"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/decision"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/telegram"
	"strings"
	"testing"
	"time"
)

func TestHandleArtifactRetentionMessageAudioDefaultsToSessionAndOffersPermanentKeep(t *testing.T) {
	t.Parallel()

	sender := &decisionTestSender{}
	broker := newTelegramDecisionBroker(sender)
	router := &decisionTestRouter{}
	store, err := session.NewSQLiteStore(t.TempDir() + "/sessions.db")
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	defer store.Close()
	keeper := &decisionTestArtifactKeeper{}
	handler := newTelegramDecisionHandler(sender, router, broker, store, keeper)

	msg := core.InboundMessage{
		ChatID:    7,
		SenderID:  42,
		MessageID: 99,
		Artifacts: []core.Artifact{{
			ID:         "voice-1",
			Channel:    "telegram",
			RemoteID:   "voice-file",
			Kind:       "audio",
			SourceType: "voice",
			Subtype:    "voice_note",
			Filename:   "voice.ogg",
			MimeType:   "audio/ogg",
		}},
	}

	handled, err := handler.HandleArtifactRetentionMessage(context.Background(), msg)
	if err != nil {
		t.Fatalf("HandleArtifactRetentionMessage() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if len(router.routed) != 1 {
		t.Fatalf("routed = %#v, want one routed audio message", router.routed)
	}
	artifact := router.routed[0].Artifacts[0]
	if got := artifact.Metadata["aphelion_retention_choice"]; got != "session" {
		t.Fatalf("retention choice = %q, want session", got)
	}
	if got := artifact.DefaultRetention; got != "session_reference" {
		t.Fatalf("DefaultRetention = %q, want session_reference", got)
	}
	if len(sender.inline) != 1 {
		t.Fatalf("inline = %#v, want one non-blocking audio keep prompt", sender.inline)
	}
	if strings.Contains(sender.inline[0].text, "turn") || strings.Contains(sender.inline[0].text, "session") {
		t.Fatalf("inline text = %q, should not expose turn/session retention jargon", sender.inline[0].text)
	}
	if len(sender.inline[0].rows) != 1 || len(sender.inline[0].rows[0]) != 1 {
		t.Fatalf("rows = %#v, want one keep-permanent button", sender.inline[0].rows)
	}
	button := sender.inline[0].rows[0][0]
	if button.Text != "Keep audio" {
		t.Fatalf("button text = %q, want Keep audio", button.Text)
	}
	if strings.Contains(button.CallbackData, "decision:") {
		t.Fatalf("callback data = %q, should use non-blocking audio keep lane", button.CallbackData)
	}
}

func TestHandleAudioMessageAlwaysUsesAgentDecisionAndOnlyOffersPermanentKeep(t *testing.T) {
	t.Parallel()

	sender := &decisionTestSender{}
	broker := newTelegramDecisionBroker(sender)
	router := &decisionTestRouter{}
	store, err := session.NewSQLiteStore(t.TempDir() + "/sessions.db")
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	defer store.Close()
	keeper := &decisionTestArtifactKeeper{}
	handler := newTelegramDecisionHandler(sender, router, broker, store, keeper)

	msg := core.InboundMessage{
		ChatID:    7,
		SenderID:  42,
		MessageID: 99,
		Artifacts: []core.Artifact{{
			ID:         "voice-1",
			Channel:    "telegram",
			RemoteID:   "voice-file",
			Kind:       "audio",
			SourceType: "voice",
			Subtype:    "voice_note",
			Filename:   "voice.ogg",
			MimeType:   "audio/ogg",
		}},
	}

	handled, err := handler.HandleArtifactRetentionMessage(context.Background(), msg)
	if err != nil {
		t.Fatalf("HandleArtifactRetentionMessage() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if len(router.routed) != 1 {
		t.Fatalf("routed = %#v, want one routed audio message", router.routed)
	}
	artifact := router.routed[0].Artifacts[0]
	if got := artifact.Metadata[core.ArtifactMetadataMediaProcessingChoice]; got != "agent" {
		t.Fatalf("media processing choice = %q, want agent", got)
	}
	if got := artifact.Metadata["aphelion_retention_choice"]; got != "session" {
		t.Fatalf("retention choice = %q, want session", got)
	}
	if len(sender.inline) != 1 {
		t.Fatalf("inline = %#v, want only the permanent audio keep offer", sender.inline)
	}
	if len(sender.inline[0].rows) != 1 || len(sender.inline[0].rows[0]) != 1 {
		t.Fatalf("rows = %#v, want one keep-permanent button", sender.inline[0].rows)
	}
	if got := sender.inline[0].rows[0][0].Text; got != "Keep audio" {
		t.Fatalf("button = %q, want Keep audio", got)
	}
	for _, label := range []string{"Transcribe", "Analyze audio", "Agent decide", "Skip"} {
		if hasInlineButton(sender.inline[0].rows, label) {
			t.Fatalf("rows = %#v, should not show media processing button %q", sender.inline[0].rows, label)
		}
	}
}

func TestHandleAudioKeepCallbackSavesWithoutReroutingTurn(t *testing.T) {
	t.Parallel()

	sender := &decisionTestSender{}
	broker := newTelegramDecisionBroker(sender)
	router := &decisionTestRouter{}
	store, err := session.NewSQLiteStore(t.TempDir() + "/sessions.db")
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	defer store.Close()
	keeper := &decisionTestArtifactKeeper{}
	handler := newTelegramDecisionHandler(sender, router, broker, store, keeper)

	msg := core.InboundMessage{
		ChatID:    7,
		SenderID:  42,
		MessageID: 99,
		Artifacts: []core.Artifact{{
			ID:         "voice-1",
			Channel:    "telegram",
			RemoteID:   "voice-file",
			Kind:       "audio",
			SourceType: "voice",
			Subtype:    "voice_note",
			Filename:   "voice.ogg",
			MimeType:   "audio/ogg",
		}},
	}
	if handled, err := handler.HandleArtifactRetentionMessage(context.Background(), msg); err != nil || !handled {
		t.Fatalf("HandleArtifactRetentionMessage() = %v, %v; want handled", handled, err)
	}
	if len(sender.inline) != 1 {
		t.Fatalf("inline = %#v, want one audio keep prompt", sender.inline)
	}
	callbackData := sender.inline[0].rows[0][0].CallbackData
	if err := handler.HandleCallbackQuery(context.Background(), telegram.CallbackQuery{
		ID:   "cb-audio-keep",
		Data: callbackData,
		From: &telegram.User{ID: 42},
		Message: &telegram.Message{
			MessageID: 1,
			Chat:      &telegram.Chat{ID: 7, Type: "private"},
		},
	}); err != nil {
		t.Fatalf("HandleCallbackQuery() err = %v", err)
	}
	if len(keeper.messages) != 1 || keeper.messages[0].MessageID != 99 {
		t.Fatalf("keeper messages = %#v, want original audio message", keeper.messages)
	}
	if len(router.routed) != 1 {
		t.Fatalf("routed = %#v, want no extra model turn after keep callback", router.routed)
	}
	if len(sender.edits) != 1 || !strings.Contains(sender.edits[0].text, "Audio saved permanently") {
		t.Fatalf("edits = %#v, want saved confirmation edit", sender.edits)
	}
	if len(sender.answers) != 1 || sender.answers[0].text != "Saved." {
		t.Fatalf("answers = %#v, want Saved callback ack", sender.answers)
	}
	if _, err := store.PendingArtifactRetention(decision.OwnerKey(7, 42)); err == nil {
		t.Fatal("PendingArtifactRetention() err = nil, want pending audio record deleted")
	}
}

func TestHandleImageMessageRoutesImmediatelyAndOffersPermanentKeep(t *testing.T) {
	t.Parallel()

	sender := &decisionTestSender{}
	broker := newTelegramDecisionBroker(sender)
	router := &decisionTestRouter{}
	store, err := session.NewSQLiteStore(t.TempDir() + "/sessions.db")
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	defer store.Close()
	keeper := &decisionTestArtifactKeeper{}
	handler := newTelegramDecisionHandler(sender, router, broker, store, keeper)

	msg := core.InboundMessage{
		ChatID:    7,
		SenderID:  42,
		MessageID: 100,
		Artifacts: []core.Artifact{{
			ID:         "photo-1",
			Channel:    "telegram",
			RemoteID:   "photo-file",
			Kind:       "image",
			SourceType: "photo",
			Filename:   "photo.jpg",
			MimeType:   "image/jpeg",
		}},
	}

	handled, err := handler.HandleArtifactRetentionMessage(context.Background(), msg)
	if err != nil {
		t.Fatalf("HandleArtifactRetentionMessage() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if len(router.routed) != 1 {
		t.Fatalf("routed = %#v, want immediate image route", router.routed)
	}
	artifact := router.routed[0].Artifacts[0]
	if got := artifact.Metadata["aphelion_retention_choice"]; got != "session" {
		t.Fatalf("retention choice = %q, want session", got)
	}
	if len(sender.inline) != 1 {
		t.Fatalf("inline = %#v, want one non-blocking image keep prompt", sender.inline)
	}
	if strings.Contains(sender.inline[0].text, "How should I retain") {
		t.Fatalf("inline text = %q, should not be blocking retention selector", sender.inline[0].text)
	}
	if got := sender.inline[0].rows[0][0].Text; got != "Keep image" {
		t.Fatalf("button = %q, want Keep image", got)
	}
}

func TestHandleMixedAudioImageRoutesImmediatelyAndMarksAgentDecision(t *testing.T) {
	t.Parallel()

	sender := &decisionTestSender{}
	broker := newTelegramDecisionBroker(sender)
	router := &decisionTestRouter{}
	store, err := session.NewSQLiteStore(t.TempDir() + "/sessions.db")
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	defer store.Close()
	keeper := &decisionTestArtifactKeeper{}
	handler := newTelegramDecisionHandler(sender, router, broker, store, keeper)

	msg := core.InboundMessage{
		ChatID:    7,
		SenderID:  42,
		MessageID: 101,
		Artifacts: []core.Artifact{
			{
				ID:         "voice-1",
				Channel:    "telegram",
				RemoteID:   "voice-file",
				Kind:       "audio",
				SourceType: "voice",
				Subtype:    "voice_note",
				Filename:   "voice.ogg",
				MimeType:   "audio/ogg",
			},
			{
				ID:         "photo-1",
				Channel:    "telegram",
				RemoteID:   "photo-file",
				Kind:       "image",
				SourceType: "photo",
				Filename:   "photo.jpg",
				MimeType:   "image/jpeg",
			},
		},
	}

	handled, err := handler.HandleArtifactRetentionMessage(context.Background(), msg)
	if err != nil {
		t.Fatalf("HandleArtifactRetentionMessage() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if len(router.routed) != 1 {
		t.Fatalf("routed = %#v, want immediate mixed-media route", router.routed)
	}
	routedArtifacts := router.routed[0].Artifacts
	if got := routedArtifacts[0].Metadata[core.ArtifactMetadataMediaProcessingChoice]; got != "agent" {
		t.Fatalf("audio media processing choice = %q, want agent", got)
	}
	for i, artifact := range routedArtifacts {
		if got := artifact.Metadata["aphelion_retention_choice"]; got != "session" {
			t.Fatalf("artifact %d retention choice = %q, want session", i, got)
		}
	}
	if len(sender.inline) != 1 {
		t.Fatalf("inline = %#v, want one non-blocking media keep prompt", sender.inline)
	}
	if got := sender.inline[0].rows[0][0].Text; got != "Keep media" {
		t.Fatalf("button = %q, want Keep media", got)
	}
}

func TestPermanentArtifactKeepSubjectButtonsStayCompact(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		artifacts []core.Artifact
		want      string
	}{
		{
			name: "audio",
			artifacts: []core.Artifact{{
				Channel:  "telegram",
				RemoteID: "audio-file",
				Kind:     "audio",
			}},
			want: "Keep audio",
		},
		{
			name: "image",
			artifacts: []core.Artifact{{
				Channel:  "telegram",
				RemoteID: "image-file",
				Kind:     "image",
			}},
			want: "Keep image",
		},
		{
			name: "video",
			artifacts: []core.Artifact{{
				Channel:  "telegram",
				RemoteID: "video-file",
				Kind:     "video",
			}},
			want: "Keep video",
		},
		{
			name: "sticker",
			artifacts: []core.Artifact{{
				Channel:  "telegram",
				RemoteID: "sticker-file",
				Kind:     "sticker",
			}},
			want: "Keep sticker",
		},
		{
			name: "file",
			artifacts: []core.Artifact{{
				Channel:    "telegram",
				RemoteID:   "doc-file",
				Kind:       "document",
				Subtype:    "text",
				Filename:   "notes.txt",
				SourceType: "document",
			}},
			want: "Keep file",
		},
		{
			name: "mixed",
			artifacts: []core.Artifact{{
				Channel:  "telegram",
				RemoteID: "audio-file",
				Kind:     "audio",
			}, {
				Channel:  "telegram",
				RemoteID: "image-file",
				Kind:     "image",
			}},
			want: "Keep media",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := permanentArtifactKeepSubject(core.InboundMessage{Artifacts: tt.artifacts}).Button
			if got != tt.want {
				t.Fatalf("button = %q, want %q", got, tt.want)
			}
			if words := strings.Fields(got); len(words) > 2 {
				t.Fatalf("button label %q has %d words, want at most 2", got, len(words))
			}
		})
	}
}

func TestDecisionButtonLabelsStayCompact(t *testing.T) {
	t.Parallel()

	labels := []string{
		stopChoiceLabel("please interrupt"),
		queueChoiceLabel("please interrupt"),
		stopChoiceLabel("wait"),
		queueChoiceLabel("wait"),
	}
	for _, choice := range artifactRetentionChoices() {
		labels = append(labels, choice.Label)
	}
	for _, label := range labels {
		if words := strings.Fields(label); len(words) > 2 {
			t.Fatalf("button label %q has %d words, want at most 2", label, len(words))
		}
	}
}

func TestHandleTextDocumentRoutesImmediatelyWithoutBlockingSelector(t *testing.T) {
	t.Parallel()

	sender := &decisionTestSender{}
	broker := newTelegramDecisionBroker(sender)
	router := &decisionTestRouter{}
	store, err := session.NewSQLiteStore(t.TempDir() + "/sessions.db")
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	defer store.Close()
	keeper := &decisionTestArtifactKeeper{}
	handler := newTelegramDecisionHandler(sender, router, broker, store, keeper)

	msg := core.InboundMessage{
		ChatID:    7,
		SenderID:  42,
		MessageID: 102,
		Artifacts: []core.Artifact{{
			ID:         "doc-1",
			Channel:    "telegram",
			RemoteID:   "file-1",
			Kind:       "document",
			SourceType: "document",
			Subtype:    "text",
			Filename:   "notes.txt",
			MimeType:   "text/plain",
		}},
	}

	handled, err := handler.HandleArtifactRetentionMessage(context.Background(), msg)
	if err != nil {
		t.Fatalf("HandleArtifactRetentionMessage() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if len(router.routed) != 1 {
		t.Fatalf("routed = %#v, want immediate text-document route", router.routed)
	}
	if len(sender.inline) != 1 {
		t.Fatalf("inline = %#v, want one non-blocking file keep prompt", sender.inline)
	}
	if strings.Contains(sender.inline[0].text, "How should I retain") {
		t.Fatalf("inline text = %q, should not be blocking retention selector", sender.inline[0].text)
	}
	if got := sender.inline[0].rows[0][0].Text; got != "Keep file" {
		t.Fatalf("button = %q, want Keep file", got)
	}
}

func TestHandleArtifactRetentionMessagePromptsAndRoutesChosenPolicy(t *testing.T) {
	t.Parallel()

	sender := &decisionTestSender{}
	broker := newTelegramDecisionBroker(sender)
	go func() {
		for i := 0; i < 100; i++ {
			if len(sender.inline) > 0 && len(sender.inline[0].rows) > 0 {
				for _, row := range sender.inline[0].rows {
					for _, button := range row {
						id, choice, ok := decision.DecodeCallbackData(button.CallbackData)
						if ok && choice == "local" {
							broker.Resolve(id, choice)
							return
						}
					}
				}
			}
			time.Sleep(5 * time.Millisecond)
		}
	}()
	router := &decisionTestRouter{}
	handler := newTelegramDecisionHandler(sender, router, broker, nil)

	msg := core.InboundMessage{
		ChatID:    7,
		SenderID:  42,
		MessageID: 99,
		Artifacts: []core.Artifact{{
			ID:         "doc-1",
			Channel:    "telegram",
			RemoteID:   "file-1",
			Kind:       "document",
			SourceType: "document",
			Filename:   "bundle.zip",
			MimeType:   "application/zip",
		}},
	}

	handled, err := handler.HandleArtifactRetentionMessage(context.Background(), msg)
	if err != nil {
		t.Fatalf("HandleArtifactRetentionMessage() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if len(sender.inline) != 1 {
		t.Fatalf("inline = %#v, want one selector prompt", sender.inline)
	}
	if !strings.Contains(sender.inline[0].text, "How should I retain this inbound file?") {
		t.Fatalf("inline text = %q, want retention prompt", sender.inline[0].text)
	}
	if len(router.routed) != 1 {
		t.Fatalf("routed = %#v, want one routed message", router.routed)
	}
	artifact := router.routed[0].Artifacts[0]
	if got := artifact.Metadata["aphelion_retention_choice"]; got != "local" {
		t.Fatalf("retention choice = %q, want local", got)
	}
	if got := artifact.Metadata["aphelion_materialize"]; got != "local" {
		t.Fatalf("materialize = %q, want local", got)
	}
	if got := artifact.DefaultRetention; got != "child_local" {
		t.Fatalf("DefaultRetention = %q, want child_local", got)
	}
	if len(sender.edits) != 1 || !strings.Contains(sender.edits[0].text, "save the file locally") {
		t.Fatalf("edits = %#v, want local-save confirmation", sender.edits)
	}
}

func TestHandleArtifactRetentionMessageTimeoutDefaultsToSession(t *testing.T) {
	t.Parallel()

	sender := &decisionTestSender{}
	broker := decision.NewBroker(func(_ context.Context, pending decision.PendingDecision) (decision.Delivery, error) {
		return decision.Delivery{MessageID: 52}, nil
	})
	router := &decisionTestRouter{}
	handler := newTelegramDecisionHandler(sender, router, broker, nil)
	handler.artifactRetentionTimeout = 10 * time.Millisecond

	msg := core.InboundMessage{
		ChatID:    8,
		SenderID:  42,
		MessageID: 100,
		Artifacts: []core.Artifact{{
			ID:         "doc-2",
			Channel:    "telegram",
			RemoteID:   "file-2",
			Kind:       "document",
			SourceType: "document",
			Filename:   "bundle.zip",
			MimeType:   "application/zip",
		}},
	}

	handled, err := handler.HandleArtifactRetentionMessage(context.Background(), msg)
	if err != nil {
		t.Fatalf("HandleArtifactRetentionMessage() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if len(router.routed) != 1 {
		t.Fatalf("routed = %#v, want one routed message", router.routed)
	}
	artifact := router.routed[0].Artifacts[0]
	if got := artifact.Metadata["aphelion_retention_choice"]; got != "session" {
		t.Fatalf("retention choice = %q, want session", got)
	}
	if got := artifact.DefaultRetention; got != "session_reference" {
		t.Fatalf("DefaultRetention = %q, want session_reference", got)
	}
	if len(sender.edits) != 1 || !strings.Contains(sender.edits[0].text, "session by default") {
		t.Fatalf("edits = %#v, want session-timeout confirmation", sender.edits)
	}
}
