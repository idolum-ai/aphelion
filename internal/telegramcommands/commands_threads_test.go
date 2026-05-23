//go:build linux

package telegramcommands

import (
	"context"
	"strings"
	"testing"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/telegram"
)

func TestThreadsCommandListsPromoteButtons(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := &stubCommandRouter{
		threadsReturn: []session.TelegramThread{
			{ChatID: 1001, ThreadID: 42, DisplaySlot: 1, Status: session.TelegramThreadStatusOpen, CreatedText: "promote this lane"},
		},
	}
	handled, err := handleTelegramThreadCommand(context.Background(), sender, router, core.InboundMessage{
		ChatID:    1001,
		SenderID:  2002,
		MessageID: 3003,
		Text:      "/threads",
	}, "threads")
	if err != nil {
		t.Fatalf("handleTelegramThreadCommand() err = %v", err)
	}
	if !handled || len(sender.inline) != 1 {
		t.Fatalf("handled=%t inline=%d, want threads inline panel", handled, len(sender.inline))
	}
	if !commandRowsContain(sender.inline[0].rows, "Promote 1", "thread_promote:42") {
		t.Fatalf("rows = %#v, want Promote 1 canonical callback", sender.inline[0].rows)
	}
	if !strings.Contains(sender.inline[0].text, "Promote one into a draft handoff") {
		t.Fatalf("panel text = %q, want promote guidance", sender.inline[0].text)
	}
}

func TestThreadPromoteCallbackCreatesDraftThroughRouter(t *testing.T) {
	t.Parallel()

	order := []string{}
	sender := &stubCommandSender{}
	router := &stubCommandRouter{canRestart: true, promoteThreadReturn: "Promotion draft created for thread 3.", order: &order}
	handled, err := handleTelegramCommandCallback(context.Background(), sender, router, telegram.CallbackQuery{
		ID:       "promote-cb",
		Data:     encodeTelegramThreadPromoteCallback(3),
		UpdateID: 707,
		From:     &telegram.User{ID: 2002},
		Message:  &telegram.Message{MessageID: 9004, Chat: &telegram.Chat{ID: 1001}},
	})
	if err != nil {
		t.Fatalf("handleTelegramCommandCallback() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want promote callback handled")
	}
	if router.promoteThreadChatID != 1001 || router.promoteThreadSenderID != 2002 || router.promoteThreadID != 3 {
		t.Fatalf("promote inputs chat=%d sender=%d thread=%d", router.promoteThreadChatID, router.promoteThreadSenderID, router.promoteThreadID)
	}
	if router.threadCallbackChatID != 1001 || router.threadCallbackID != 3 || router.threadCallbackMessageID != 9004 || router.threadCallbackSurface != "thread_promote" {
		t.Fatalf("callback ledger = chat:%d thread:%d msg:%d surface:%q", router.threadCallbackChatID, router.threadCallbackID, router.threadCallbackMessageID, router.threadCallbackSurface)
	}
	if len(sender.answers) != 1 || sender.answers[0].text != "Drafting promotion." {
		t.Fatalf("answers = %#v, want drafting ack", sender.answers)
	}
	if len(order) == 0 || order[0] != "promote" {
		t.Fatalf("order = %#v, want promote after ack", order)
	}
	if len(sender.editClear) != 1 || sender.editClear[0].text != "Promotion draft created for thread 3." {
		t.Fatalf("editClear = %#v, want promotion draft text", sender.editClear)
	}
}

func TestThreadPromoteCallbackIsAdminOnly(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := &stubCommandRouter{canRestart: false}
	handled, err := handleTelegramCommandCallback(context.Background(), sender, router, telegram.CallbackQuery{
		ID:      "promote-cb",
		Data:    encodeTelegramThreadPromoteCallback(3),
		From:    &telegram.User{ID: 2002},
		Message: &telegram.Message{MessageID: 9004, Chat: &telegram.Chat{ID: 1001}},
	})
	if err != nil {
		t.Fatalf("handleTelegramCommandCallback() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want admin-only callback handled")
	}
	if router.promoteThreadID != 0 {
		t.Fatalf("promoteThreadID = %d, want no promote call", router.promoteThreadID)
	}
	if len(sender.answers) != 1 || sender.answers[0].text != "Promote is admin only." {
		t.Fatalf("answers = %#v, want admin-only answer", sender.answers)
	}
	if len(sender.editClear) != 0 {
		t.Fatalf("editClear = %#v, want no message edit", sender.editClear)
	}
}
