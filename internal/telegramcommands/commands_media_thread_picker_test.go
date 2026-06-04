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

func TestMaybeAskTelegramMediaThreadPickerPersistsAndPaginates(t *testing.T) {
	sender := &stubCommandSender{}
	router := &stubCommandRouter{threadsReturn: mediaPickerTestThreads(7)}
	msg := mediaPickerTestInbound()

	handled, err := maybeAskTelegramMediaThreadPicker(context.Background(), sender, router, msg)
	if err != nil {
		t.Fatalf("maybeAskTelegramMediaThreadPicker() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want media picker prompt")
	}
	if len(sender.inline) != 1 {
		t.Fatalf("inline calls = %d, want 1", len(sender.inline))
	}
	if router.mediaPickerRecordChatID != msg.ChatID || router.mediaPickerRecordMessageID != 1 {
		t.Fatalf("record picker chat/message = %d/%d, want %d/1", router.mediaPickerRecordChatID, router.mediaPickerRecordMessageID, msg.ChatID)
	}
	if router.mediaPickerRecordInbound.MessageID != msg.MessageID || len(router.mediaPickerRecordInbound.Artifacts) != 1 {
		t.Fatalf("recorded inbound = %#v, want source media inbound", router.mediaPickerRecordInbound)
	}
	rows := sender.inline[0].rows
	if len(rows) != 8 {
		t.Fatalf("rows = %d, want 8 (6 threads + nav + new)", len(rows))
	}
	if got := rows[0][0].CallbackData; got != "mtpick:thread:101" {
		t.Fatalf("first callback = %q", got)
	}
	if got := rows[6][0].CallbackData; got != "mtpick:page:1" {
		t.Fatalf("next callback = %q", got)
	}
	if got := rows[7][0].CallbackData; got != "mtpick:new" {
		t.Fatalf("new callback = %q", got)
	}
}

func TestMediaThreadPickerCallbackPaginates(t *testing.T) {
	sender := &stubCommandSender{}
	router := &stubCommandRouter{threadsReturn: mediaPickerTestThreads(7)}
	cb := mediaPickerCallback("mtpick:page:1", 44, 900)

	handled, err := handleTelegramCommandCallback(context.Background(), sender, router, cb)
	if err != nil {
		t.Fatalf("handleTelegramCommandCallback() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want page callback handled")
	}
	if len(sender.editInline) != 1 {
		t.Fatalf("editInline calls = %d, want 1", len(sender.editInline))
	}
	rows := sender.editInline[0].rows
	if got := rows[0][0].CallbackData; got != "mtpick:thread:107" {
		t.Fatalf("page first callback = %q", got)
	}
	if got := rows[1][0].CallbackData; got != "mtpick:page:0" {
		t.Fatalf("prev callback = %q", got)
	}
}

func TestMediaThreadPickerCallbackRoutesExistingThread(t *testing.T) {
	sender := &stubCommandSender{}
	inbound := mediaPickerTestInbound()
	router := &stubCommandRouter{threadsReturn: mediaPickerTestThreads(3), mediaPickerReturn: inbound, mediaPickerOK: true}
	cb := mediaPickerCallback("mtpick:thread:102", inbound.ChatID, 77)

	handled, err := handleTelegramCommandCallback(context.Background(), sender, router, cb)
	if err != nil {
		t.Fatalf("handleTelegramCommandCallback() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want thread callback handled")
	}
	if router.mediaPickerGetMessageID != 77 {
		t.Fatalf("picker get message = %d, want 77", router.mediaPickerGetMessageID)
	}
	if router.routeAcceptedMsg == nil || router.routeAcceptedMsg.TelegramThreadID != 102 || len(router.routeAcceptedMsg.Artifacts) != 1 {
		t.Fatalf("routed msg = %#v, want media in thread 102", router.routeAcceptedMsg)
	}
	if router.mediaPickerMarkMessageID != 77 {
		t.Fatalf("mark routed message = %d, want 77", router.mediaPickerMarkMessageID)
	}
	if len(sender.editClear) != 1 || !strings.Contains(sender.editClear[0].text, "thread 2") {
		t.Fatalf("editClear = %#v, want routed-to-thread text", sender.editClear)
	}
}

func TestMediaThreadPickerCallbackCreatesNewThread(t *testing.T) {
	sender := &stubCommandSender{}
	inbound := mediaPickerTestInbound()
	router := &stubCommandRouter{
		threadsReturn:     mediaPickerTestThreads(1),
		mediaPickerReturn: inbound,
		mediaPickerOK:     true,
		threadStartReturn: session.TelegramThread{ChatID: inbound.ChatID, ThreadID: 909, DisplaySlot: 9, Status: session.TelegramThreadStatusOpen},
	}
	cb := mediaPickerCallback("mtpick:new", inbound.ChatID, 88)

	handled, err := handleTelegramCommandCallback(context.Background(), sender, router, cb)
	if err != nil {
		t.Fatalf("handleTelegramCommandCallback() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want new-thread callback handled")
	}
	if router.threadStartMsg == nil || router.threadStartMsg.MessageID != inbound.MessageID {
		t.Fatalf("threadStartMsg = %#v", router.threadStartMsg)
	}
	if router.routeAcceptedMsg == nil || router.routeAcceptedMsg.TelegramThreadID != 909 {
		t.Fatalf("routed msg = %#v, want new thread 909", router.routeAcceptedMsg)
	}
	if router.mediaPickerMarkMessageID != 88 {
		t.Fatalf("mark routed message = %d, want 88", router.mediaPickerMarkMessageID)
	}
	if len(sender.editClear) != 1 || !strings.Contains(sender.editClear[0].text, "new thread 9") {
		t.Fatalf("editClear = %#v", sender.editClear)
	}
}

func mediaPickerTestInbound() core.InboundMessage {
	return core.InboundMessage{ChatID: 44, SenderID: 12, MessageID: 700, Text: "caption", Artifacts: []core.Artifact{{SourceType: "voice", ID: "voice-file"}}}
}

func mediaPickerTestThreads(n int) []session.TelegramThread {
	threads := make([]session.TelegramThread, 0, n)
	for i := 1; i <= n; i++ {
		threads = append(threads, session.TelegramThread{ChatID: 44, ThreadID: int64(100 + i), DisplaySlot: int64(i), ArchivedDisplayName: "Thread label", Status: session.TelegramThreadStatusOpen})
	}
	return threads
}

func mediaPickerCallback(data string, chatID int64, messageID int64) telegram.CallbackQuery {
	return telegram.CallbackQuery{ID: "cb1", Data: data, Message: &telegram.Message{MessageID: messageID, Chat: &telegram.Chat{ID: chatID, Type: "private"}}}
}

func TestMediaThreadPickerCallbackRejectsInvalidThreadID(t *testing.T) {
	sender := &stubCommandSender{}
	inbound := mediaPickerTestInbound()
	router := &stubCommandRouter{threadsReturn: mediaPickerTestThreads(3), mediaPickerReturn: inbound, mediaPickerOK: true}
	cb := mediaPickerCallback("mtpick:thread:not-a-thread", inbound.ChatID, 77)

	handled, err := handleTelegramCommandCallback(context.Background(), sender, router, cb)
	if err != nil {
		t.Fatalf("handleTelegramCommandCallback() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want invalid thread callback handled")
	}
	if len(sender.answers) != 1 || sender.answers[0].text != "Invalid thread choice." {
		t.Fatalf("answers = %#v, want invalid thread answer", sender.answers)
	}
	if router.mediaPickerGetMessageID != 0 {
		t.Fatalf("picker lookup message = %d, want no lookup for invalid thread id", router.mediaPickerGetMessageID)
	}
	if router.routeAcceptedMsg != nil {
		t.Fatalf("routeAcceptedMsg = %#v, want no routing", router.routeAcceptedMsg)
	}
}
