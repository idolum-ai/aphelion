//go:build linux

package telegramcommands

import (
	"context"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/telegram"
	"testing"
)

func TestHandleDeliberationControlCallbackDetailsRequiresAdmin(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := &stubCommandRouter{canRestart: false, toggleProgressUpdated: true}
	handled, err := handleDeliberationControlCallback(context.Background(), sender, router, telegram.CallbackQuery{
		ID:   "cb-details-denied",
		From: &telegram.User{ID: 1002},
		Message: &telegram.Message{
			MessageID: 44,
			Chat:      &telegram.Chat{ID: 9901, Type: "group"},
		},
	}, 73, core.DeliberationControlActionDetails)
	if err != nil {
		t.Fatalf("handleDeliberationControlCallback() err = %v", err)
	}
	if !handled {
		t.Fatal("handleDeliberationControlCallback() handled = false, want true")
	}
	if router.toggleProgressRunID != 0 {
		t.Fatalf("toggleProgressRunID = %d, want no runtime toggle for non-admin details", router.toggleProgressRunID)
	}
	if len(sender.answers) != 1 || sender.answers[0].text != adminDeliberationDetailsCallbackText {
		t.Fatalf("answers = %#v, want admin-only callback answer", sender.answers)
	}
}

func TestHandleDeliberationControlCallbackDetailsPassesSender(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := &stubCommandRouter{canRestart: true, toggleProgressUpdated: true}
	handled, err := handleDeliberationControlCallback(context.Background(), sender, router, telegram.CallbackQuery{
		ID:   "cb-details-admin",
		From: &telegram.User{ID: 1001},
		Message: &telegram.Message{
			MessageID: 45,
			Chat:      &telegram.Chat{ID: 9902, Type: "private"},
		},
	}, 74, core.DeliberationControlActionDetails)
	if err != nil {
		t.Fatalf("handleDeliberationControlCallback() err = %v", err)
	}
	if !handled {
		t.Fatal("handleDeliberationControlCallback() handled = false, want true")
	}
	if router.toggleProgressChatID != 9902 || router.toggleProgressSenderID != 1001 || router.toggleProgressRunID != 74 || !router.toggleProgressDetails {
		t.Fatalf("toggleProgress fields = chat:%d sender:%d run:%d details:%t", router.toggleProgressChatID, router.toggleProgressSenderID, router.toggleProgressRunID, router.toggleProgressDetails)
	}
	if len(sender.answers) != 1 || sender.answers[0].text != "" {
		t.Fatalf("answers = %#v, want empty success answer", sender.answers)
	}
}
