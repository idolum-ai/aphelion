//go:build linux

package telegramcommands

import (
	"context"
	"testing"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/telegram"
)

func TestModelCallbackCodecRoundTripsAllModelSlots(t *testing.T) {
	t.Parallel()

	for _, slot := range core.ModelSlotNames() {
		data := encodeModelCallbackData(modelCallbackSlot, slot, "")
		action, gotSlot, value, ok := decodeModelCallbackData(data)
		if !ok {
			t.Fatalf("decodeModelCallbackData(%q) ok = false", data)
		}
		if action != modelCallbackSlot || gotSlot != slot || value != "" {
			t.Fatalf("decodeModelCallbackData(%q) = %s/%s/%s, want slot/%s/empty", data, action, gotSlot, value, slot)
		}
	}
}

func TestHandleTelegramCommandCallbackIgnoresRetiredRecipeModelCallbacks(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := stubCommandRouter{}
	for _, data := range []string{
		"recipe:persona_model:claude-opus-4-6",
		"recipe:governor_effort:high",
	} {
		handled, err := handleTelegramCommandCallback(context.Background(), sender, &router, telegram.CallbackQuery{
			ID:   "cb-retired-model",
			Data: data,
			Message: &telegram.Message{
				MessageID: 91,
				Chat:      &telegram.Chat{ID: 7, Type: "private"},
			},
		})
		if err != nil {
			t.Fatalf("handleTelegramCommandCallback(%q) err = %v", data, err)
		}
		if handled {
			t.Fatalf("handleTelegramCommandCallback(%q) handled = true, want false", data)
		}
	}
	if len(sender.answers) != 0 || len(sender.edits) != 0 || len(sender.editClear) != 0 {
		t.Fatalf("sender mutated for retired callbacks: answers=%#v edits=%#v clear=%#v", sender.answers, sender.edits, sender.editClear)
	}
}

func TestHandleTelegramCommandCallbackKeepsModelCallbackLane(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := stubCommandRouter{
		canRestart:    true,
		modelStatuses: nil,
	}
	handled, err := handleTelegramCommandCallback(context.Background(), sender, &router, telegram.CallbackQuery{
		ID:   "cb-model-status",
		From: &telegram.User{ID: 1001},
		Data: "model:status",
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
	if len(sender.answers) != 1 {
		t.Fatalf("answers count = %d, want 1", len(sender.answers))
	}
}
