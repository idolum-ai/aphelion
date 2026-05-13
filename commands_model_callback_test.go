//go:build linux

package main

import (
	"context"
	"testing"

	"github.com/idolum-ai/aphelion/telegram"
)

func TestHandleTelegramCommandCallbackPersonaModel(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := stubCommandRouter{
		setPersonaModelReturn: "claude-opus-4-6",
	}
	handled, err := handleTelegramCommandCallback(context.Background(), sender, &router, telegram.CallbackQuery{
		ID:   "cb-1",
		Data: "recipe:persona_model:claude-opus-4-6",
		Message: &telegram.Message{
			MessageID: 91,
			Chat:      &telegram.Chat{ID: 7, Type: "private"},
		},
	})
	if err != nil {
		t.Fatalf("handleTelegramCommandCallback() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if router.setPersonaModelInput != "claude-opus-4-6" {
		t.Fatalf("setPersonaModel input = %q, want claude-opus-4-6", router.setPersonaModelInput)
	}
	if len(sender.answers) != 1 {
		t.Fatalf("answers count = %d, want 1", len(sender.answers))
	}
	if len(sender.edits) != 0 {
		t.Fatalf("edits count = %d, want 0 plain edits", len(sender.edits))
	}
	if len(sender.editClear) != 1 {
		t.Fatalf("editClear count = %d, want 1", len(sender.editClear))
	}
}

func TestHandleTelegramCommandCallbackGovernorEffort(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := stubCommandRouter{
		setGovernorEffortReturn: "high",
	}
	handled, err := handleTelegramCommandCallback(context.Background(), sender, &router, telegram.CallbackQuery{
		ID:   "cb-2",
		Data: "recipe:governor_effort:high",
		Message: &telegram.Message{
			MessageID: 92,
			Chat:      &telegram.Chat{ID: 7, Type: "private"},
		},
	})
	if err != nil {
		t.Fatalf("handleTelegramCommandCallback() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if router.setGovernorEffortInput != "high" {
		t.Fatalf("setGovernorEffort input = %q, want high", router.setGovernorEffortInput)
	}
	if len(sender.answers) != 1 {
		t.Fatalf("answers count = %d, want 1", len(sender.answers))
	}
	if len(sender.edits) != 0 {
		t.Fatalf("edits count = %d, want 0 plain edits", len(sender.edits))
	}
	if len(sender.editClear) != 1 {
		t.Fatalf("editClear count = %d, want 1", len(sender.editClear))
	}
}

func TestPersonaModelButtonLabelIncludesOpus47(t *testing.T) {
	t.Parallel()
	if got := personaModelButtonLabel("claude-opus-4-7"); got != "Opus 4.7" {
		t.Fatalf("personaModelButtonLabel() = %q, want Opus 4.7", got)
	}
	if got := personaModelButtonLabel("gpt-5.5"); got != "GPT-5.5" {
		t.Fatalf("personaModelButtonLabel() = %q, want GPT-5.5", got)
	}
}
