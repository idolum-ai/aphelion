//go:build linux

package telegramcommands

import (
	"context"
	"strconv"
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
	router := &stubCommandRouter{canRestart: true, promoteThreadReturn: session.TelegramThreadPromotionResult{Text: "Promotion draft created for thread 3.\n\nHandoff: ignored-rendered-handoff\nStatus: draft", HandoffID: "thread-promotion:1001:3:99", ThreadID: 3, Status: session.TelegramThreadPromotionStatusDraft}, order: &order}
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
	if len(sender.editInline) != 1 || !strings.Contains(sender.editInline[0].text, "Promotion draft created for thread 3.") {
		t.Fatalf("editInline = %#v, want promotion draft text with buttons", sender.editInline)
	}
	readyData, ok := commandRowCallbackData(sender.editInline[0].rows, "Ready")
	if !ok {
		t.Fatalf("promotion rows = %#v, want ready button", sender.editInline[0].rows)
	}
	assertThreadPromotionCallbackData(t, 1001, readyData, "ready", "thread-promotion:1001:3:99")
	cancelData, ok := commandRowCallbackData(sender.editInline[0].rows, "Cancel")
	if !ok {
		t.Fatalf("promotion rows = %#v, want cancel button", sender.editInline[0].rows)
	}
	assertThreadPromotionCallbackData(t, 1001, cancelData, "cancel", "thread-promotion:1001:3:99")
}

func TestThreadPromotionActionCallbacksStayWithinTelegramLimit(t *testing.T) {
	t.Parallel()

	for _, handoffID := range []string{
		"thread-promotion:6313146:8:1779540000000000000",
		"thread-promotion:-1001234567890:8:1779540000000000000",
	} {
		chatID := telegramThreadPromotionChatIDForTest(t, handoffID)
		for _, tc := range []struct {
			action string
			data   string
		}{
			{action: "ready", data: encodeTelegramThreadPromotionReadyCallback(handoffID)},
			{action: "cancel", data: encodeTelegramThreadPromotionCancelCallback(handoffID)},
			{action: "refresh", data: encodeTelegramThreadPromotionRefreshCallback(handoffID)},
		} {
			assertThreadPromotionCallbackData(t, chatID, tc.data, tc.action, handoffID)
		}
	}
}

func TestThreadPromotionReadyCallbackIsAdminGatedAndClearsKeyboard(t *testing.T) {
	t.Parallel()

	order := []string{}
	sender := &stubCommandSender{}
	router := &stubCommandRouter{canRestart: true, preparePromotionReturn: session.TelegramThreadPromotionResult{Text: "Promotion handoff ready.\n\nHandoff: ignored-rendered-handoff\nStatus: ready", HandoffID: "thread-promotion:1001:3:99", ThreadID: 3, Status: session.TelegramThreadPromotionStatusReady}, order: &order}
	handled, err := handleTelegramCommandCallback(context.Background(), sender, router, telegram.CallbackQuery{
		ID:      "ready-cb",
		Data:    encodeTelegramThreadPromotionReadyCallback("thread-promotion:1001:3:99"),
		From:    &telegram.User{ID: 2002},
		Message: &telegram.Message{MessageID: 9004, Chat: &telegram.Chat{ID: 1001}},
	})
	if err != nil {
		t.Fatalf("handleTelegramCommandCallback() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want ready callback handled")
	}
	if router.preparePromotionChatID != 1001 || router.preparePromotionSenderID != 2002 || router.preparePromotionHandoffID != "thread-promotion:1001:3:99" {
		t.Fatalf("ready inputs chat=%d sender=%d handoff=%q", router.preparePromotionChatID, router.preparePromotionSenderID, router.preparePromotionHandoffID)
	}
	if router.threadCallbackSurface != "thread_promotion_ready" || router.threadCallbackID != 3 {
		t.Fatalf("callback ledger surface=%q thread=%d", router.threadCallbackSurface, router.threadCallbackID)
	}
	if len(sender.answers) != 1 || sender.answers[0].text != "Marking promotion ready." {
		t.Fatalf("answers = %#v, want ready ack", sender.answers)
	}
	if len(order) == 0 || order[0] != "promotion_ready" {
		t.Fatalf("order = %#v", order)
	}
	if len(sender.editClear) != 1 || !strings.Contains(sender.editClear[0].text, "Promotion handoff ready") {
		t.Fatalf("editClear = %#v, want ready text", sender.editClear)
	}
}

func TestThreadPromotionRefreshCallbackUsesTypedResultForButtons(t *testing.T) {
	t.Parallel()

	order := []string{}
	sender := &stubCommandSender{}
	router := &stubCommandRouter{
		canRestart: true,
		supersedePromotionReturn: session.TelegramThreadPromotionResult{
			Text:      "Previous promotion handoff superseded.\n\nHandoff: ignored-rendered-handoff\nStatus: draft",
			HandoffID: "thread-promotion:1001:3:123",
			ThreadID:  3,
			Status:    session.TelegramThreadPromotionStatusDraft,
		},
		order: &order,
	}
	oldHandoffID := "thread-promotion:1001:3:99"
	handled, err := handleTelegramCommandCallback(context.Background(), sender, router, telegram.CallbackQuery{
		ID:      "refresh-cb",
		Data:    encodeTelegramThreadPromotionRefreshCallback(oldHandoffID),
		From:    &telegram.User{ID: 2002},
		Message: &telegram.Message{MessageID: 9004, Chat: &telegram.Chat{ID: 1001}},
	})
	if err != nil {
		t.Fatalf("handleTelegramCommandCallback() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want refresh callback handled")
	}
	if router.supersedePromotionHandoffID != oldHandoffID {
		t.Fatalf("supersede handoff = %q, want original callback handoff %q", router.supersedePromotionHandoffID, oldHandoffID)
	}
	if router.threadCallbackSurface != "thread_promotion_refresh" || router.threadCallbackID != 3 {
		t.Fatalf("callback ledger surface=%q thread=%d", router.threadCallbackSurface, router.threadCallbackID)
	}
	if len(order) == 0 || order[0] != "promotion_refresh" {
		t.Fatalf("order = %#v", order)
	}
	if len(sender.editInline) != 1 || !strings.Contains(sender.editInline[0].text, "Previous promotion handoff superseded") {
		t.Fatalf("editInline = %#v, want refreshed draft text with buttons", sender.editInline)
	}
	readyData, ok := commandRowCallbackData(sender.editInline[0].rows, "Ready")
	if !ok {
		t.Fatalf("promotion rows = %#v, want ready button", sender.editInline[0].rows)
	}
	assertThreadPromotionCallbackData(t, 1001, readyData, "ready", "thread-promotion:1001:3:123")
}

func TestThreadPromoteCallbackWithReadyResultClearsKeyboard(t *testing.T) {
	t.Parallel()

	order := []string{}
	sender := &stubCommandSender{}
	router := &stubCommandRouter{
		canRestart: true,
		promoteThreadReturn: session.TelegramThreadPromotionResult{
			Text:      "Promotion handoff ready for thread 3.\n\nNext gate: approve/apply may create a durable child.",
			HandoffID: "thread-promotion:1001:3:99",
			ThreadID:  3,
			Status:    session.TelegramThreadPromotionStatusReady,
		},
		order: &order,
	}
	handled, err := handleTelegramCommandCallback(context.Background(), sender, router, telegram.CallbackQuery{
		ID:      "promote-ready-cb",
		Data:    encodeTelegramThreadPromoteCallback(3),
		From:    &telegram.User{ID: 2002},
		Message: &telegram.Message{MessageID: 9004, Chat: &telegram.Chat{ID: 1001}},
	})
	if err != nil {
		t.Fatalf("handleTelegramCommandCallback() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want promote callback handled")
	}
	if len(sender.editInline) != 0 {
		t.Fatalf("editInline = %#v, want no draft controls for ready handoff", sender.editInline)
	}
	if len(sender.editClear) != 1 || !strings.Contains(sender.editClear[0].text, "Promotion handoff ready") {
		t.Fatalf("editClear = %#v, want ready text without controls", sender.editClear)
	}
	for _, forbidden := range []string{"Promotion draft already exists", "tap Ready"} {
		if strings.Contains(sender.editClear[0].text, forbidden) {
			t.Fatalf("ready promote callback text contains %q:\n%s", forbidden, sender.editClear[0].text)
		}
	}
	if len(order) == 0 || order[0] != "promote" {
		t.Fatalf("order = %#v, want promote", order)
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
	if len(sender.editClear) != 0 || len(sender.editInline) != 0 {
		t.Fatalf("edits = %#v/%#v, want no message edit", sender.editClear, sender.editInline)
	}
}

func commandRowCallbackData(rows [][]telegram.InlineButton, label string) (string, bool) {
	for _, row := range rows {
		for _, button := range row {
			if button.Text == label {
				return button.CallbackData, true
			}
		}
	}
	return "", false
}

func assertThreadPromotionCallbackData(t *testing.T, chatID int64, data string, wantAction string, wantHandoffID string) {
	t.Helper()
	if data == "" || len(data) > core.TelegramCallbackDataMaxBytes {
		t.Fatalf("callback data for %s = %q len=%d, want non-empty <= %d", wantAction, data, len(data), core.TelegramCallbackDataMaxBytes)
	}
	action, handoffID, ok := decodeTelegramThreadPromotionActionCallback(data)
	if !ok {
		t.Fatalf("decodeTelegramThreadPromotionActionCallback(%q) ok=false", data)
	}
	handoffID = telegramThreadPromotionCallbackHandoffID(chatID, handoffID)
	if action != wantAction || handoffID != wantHandoffID {
		t.Fatalf("decodeTelegramThreadPromotionActionCallback(%q) = action:%q handoff:%q, want action:%q handoff:%q", data, action, handoffID, wantAction, wantHandoffID)
	}
}

func telegramThreadPromotionChatIDForTest(t *testing.T, handoffID string) int64 {
	t.Helper()
	parts := strings.Split(handoffID, ":")
	if len(parts) < 4 || parts[0] != "thread-promotion" {
		t.Fatalf("invalid handoff id fixture %q", handoffID)
	}
	chatID, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		t.Fatalf("parse chat id from %q: %v", handoffID, err)
	}
	return chatID
}
