//go:build linux

package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/decision"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/telegram"
)

func TestParseTelegramThreadPrefix(t *testing.T) {
	t.Parallel()

	threadID, text, ok := parseTelegramThreadPrefix("(thread 12)\n\ncreate three children")
	if !ok || threadID != 12 || text != "create three children" {
		t.Fatalf("parseTelegramThreadPrefix() = id:%d text:%q ok:%t", threadID, text, ok)
	}
	if _, _, ok := parseTelegramThreadPrefix("thread 12 create three children"); ok {
		t.Fatal("parseTelegramThreadPrefix() ok=true without typed prefix")
	}
}

func TestThreadCommandRoutesTextWithoutSendingCommandReply(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := &stubCommandRouter{}
	routed, retargeted, handled, err := resolveTelegramThreadStartCommand(context.Background(), sender, router, core.InboundMessage{
		ChatID:          1001,
		SenderID:        2002,
		MessageID:       3003,
		IngressSurface:  "telegram:primary",
		IngressUpdateID: 44,
		Text:            "/thread create three children",
	})
	if err != nil {
		t.Fatalf("resolveTelegramThreadStartCommand() err = %v", err)
	}
	if handled {
		t.Fatal("handled = true, want pipeline to continue with thread payload")
	}
	if !retargeted {
		t.Fatal("retargeted = false, want thread work payload")
	}
	if router.threadStartText != "create three children" || router.threadStartMsg == nil || router.threadStartMsg.IngressUpdateID != 44 {
		t.Fatalf("thread start text=%q msg=%#v, want routed command payload", router.threadStartText, router.threadStartMsg)
	}
	if routed.TelegramThreadID != 1 || routed.Text != "create three children" {
		t.Fatalf("routed = %#v, want thread-targeted payload", routed)
	}
	if len(sender.msgs) != 0 || len(sender.inline) != 0 {
		t.Fatalf("sender msgs=%#v inline=%#v, want no separate command reply", sender.msgs, sender.inline)
	}
}

func TestThreadCommandTreatsSlashArgumentAsThreadWork(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := &stubCommandRouter{}
	routed, retargeted, handled, err := resolveTelegramThreadStartCommand(context.Background(), sender, router, core.InboundMessage{
		ChatID:          1001,
		SenderID:        2002,
		MessageID:       3003,
		IngressSurface:  "telegram:primary",
		IngressUpdateID: 45,
		Text:            "/thread /stop",
	})
	if err != nil {
		t.Fatalf("resolveTelegramThreadStartCommand() err = %v", err)
	}
	if handled || !retargeted {
		t.Fatalf("handled=%t retargeted=%t, want retargeted thread work payload", handled, retargeted)
	}
	if routed.TelegramThreadID != 1 || routed.Text != "/stop" {
		t.Fatalf("routed = %#v, want slash text preserved as side-thread work", routed)
	}
	if len(sender.msgs) != 0 || len(sender.inline) != 0 {
		t.Fatalf("sender msgs=%#v inline=%#v, want no command response", sender.msgs, sender.inline)
	}
}

func TestThreadCommandWithoutArgsCreatesEmptyThreadGuide(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := &stubCommandRouter{
		threadCreateReturn: session.TelegramThread{ChatID: 1001, ThreadID: 7, Status: session.TelegramThreadStatusOpen},
	}
	handled, err := handleTelegramCommand(context.Background(), sender, router, core.InboundMessage{
		ChatID:          1001,
		SenderID:        2002,
		MessageID:       3003,
		IngressSurface:  "telegram:primary",
		IngressUpdateID: 46,
		Text:            "/thread",
	})
	if err != nil {
		t.Fatalf("handleTelegramCommand(/thread) err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if router.threadCreateMsg == nil || router.threadCreateMsg.IngressUpdateID != 46 {
		t.Fatalf("threadCreateMsg = %#v, want update-bound create", router.threadCreateMsg)
	}
	if router.threadStartMsg != nil {
		t.Fatalf("threadStartMsg = %#v, want no routed turn", router.threadStartMsg)
	}
	if len(sender.inline) != 1 || !strings.Contains(sender.inline[0].text, "Thread 7") || !strings.Contains(sender.inline[0].text, "(thread 7) create the inbox child") {
		t.Fatalf("inline = %#v, want thread guide", sender.inline)
	}
	if router.threadGuideChatID != 1001 || router.threadGuideID != 7 || router.threadGuideMessageID != 1 {
		t.Fatalf("guide record chat=%d thread=%d message=%d, want 1001/7/1", router.threadGuideChatID, router.threadGuideID, router.threadGuideMessageID)
	}
	if !commandRowsContain(sender.inline[0].rows, "Absorb", "thread_absorb:7") {
		t.Fatalf("rows = %#v, want absorb button", sender.inline[0].rows)
	}
}

func TestThreadPrefixRoutesToThread(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := &stubCommandRouter{threadsReturn: []session.TelegramThread{{ChatID: 1001, ThreadID: 2, DisplaySlot: 2, Status: session.TelegramThreadStatusOpen}}}
	routed, handled, err := resolveTelegramThreadPrefix(context.Background(), sender, router, core.InboundMessage{
		ChatID:          1001,
		SenderID:        2002,
		MessageID:       3003,
		IngressSurface:  "telegram:primary",
		IngressUpdateID: 45,
		Text:            "(thread 2) continue that plan",
	})
	if err != nil {
		t.Fatalf("resolveTelegramThreadPrefix() err = %v", err)
	}
	if handled {
		t.Fatal("handled = true, want normal pipeline to process thread-targeted payload")
	}
	if router.threadRouteID != 2 || router.threadRouteText != "continue that plan" {
		t.Fatalf("thread route id=%d text=%q, want thread 2 payload", router.threadRouteID, router.threadRouteText)
	}
	if routed.TelegramThreadID != 2 || routed.Text != "continue that plan" {
		t.Fatalf("routed = %#v, want thread 2 payload", routed)
	}
	if len(sender.msgs) != 0 {
		t.Fatalf("sender msgs=%#v, want no notice for routed thread", sender.msgs)
	}
}

func TestThreadPrefixTargetsBusyDecisionLane(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	threadRouter := &stubCommandRouter{threadsReturn: []session.TelegramThread{{ChatID: 7, ThreadID: 3, DisplaySlot: 3, Status: session.TelegramThreadStatusOpen}}}
	routed, handled, err := resolveTelegramThreadPrefix(context.Background(), sender, threadRouter, core.InboundMessage{
		ChatID:          7,
		SenderID:        42,
		MessageID:       99,
		IngressSurface:  "telegram:primary",
		IngressUpdateID: 701,
		Text:            "(thread 3) next task",
	})
	if err != nil {
		t.Fatalf("resolveTelegramThreadPrefix() err = %v", err)
	}
	if handled {
		t.Fatal("handled = true, want busy gate to see retargeted side-thread message")
	}

	router := &decisionTestRouter{
		statusForMessageFn: func(msg core.InboundMessage) core.SessionStatus {
			if msg.TelegramThreadID != 3 {
				t.Fatalf("StatusForMessage() msg = %#v, want thread 3", msg)
			}
			return core.SessionStatus{Active: true}
		},
	}
	broker := decision.NewBroker(nil, decision.WithAutoResolver(func(_ context.Context, pending decision.PendingDecision) (decision.AutoResolution, error) {
		if pending.SessionID != "telegram_thread:7:3" || pending.OwnerKey != "session:telegram_thread:7:3:sender:42" {
			t.Fatalf("pending = %#v, want thread-scoped busy decision", pending)
		}
		return decision.AutoResolution{Choice: "queue", Reason: "test"}, nil
	}))
	handler := newTelegramDecisionHandler(&decisionTestSender{}, router, broker, nil)
	busyHandled, err := handler.HandleBusyMessage(context.Background(), routed)
	if err != nil {
		t.Fatalf("HandleBusyMessage() err = %v", err)
	}
	if !busyHandled {
		t.Fatal("busyHandled = false, want thread-scoped busy gate")
	}
	if len(router.routed) != 1 || router.routed[0].TelegramThreadID != 3 || router.routed[0].Text != "next task" {
		t.Fatalf("routed = %#v, want queued side-thread work after busy decision", router.routed)
	}
}

func TestThreadPrefixCanTargetLaneCommand(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := &stubCommandRouter{threadsReturn: []session.TelegramThread{{ChatID: 1001, ThreadID: 2, DisplaySlot: 2, Status: session.TelegramThreadStatusOpen}}}
	routed, handled, err := resolveTelegramThreadPrefix(context.Background(), sender, router, core.InboundMessage{
		ChatID:    1001,
		SenderID:  2002,
		MessageID: 3003,
		Text:      "(thread 2) /stop",
	})
	if err != nil {
		t.Fatalf("resolveTelegramThreadPrefix() err = %v", err)
	}
	if handled {
		t.Fatal("handled = true, want command handler to process targeted lane command")
	}
	if routed.TelegramThreadID != 2 || routed.Text != "/stop" {
		t.Fatalf("routed = %#v, want thread-targeted /stop", routed)
	}
	if router.threadRouteMsg != nil {
		t.Fatalf("threadRouteMsg = %#v, want command not ordinary routed text", router.threadRouteMsg)
	}
}

func TestReplySlashCommandTargetsThreadLane(t *testing.T) {
	t.Parallel()

	replyID := int64(9001)
	sender := &stubCommandSender{}
	router := &stubCommandRouter{
		threadReplyOK:     true,
		threadReplyReturn: session.TelegramThread{ChatID: 1001, ThreadID: 4, Status: session.TelegramThreadStatusOpen},
		stopMessageResult: core.StopResult{ActiveCanceled: true},
	}
	handled, err := handleTelegramCommand(context.Background(), sender, router, core.InboundMessage{
		ChatID:    1001,
		SenderID:  2002,
		MessageID: 3003,
		ReplyTo:   &replyID,
		Text:      "/stop",
	})
	if err != nil {
		t.Fatalf("handleTelegramCommand() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want thread-targeted command handled")
	}
	if router.stopMessage == nil || router.stopMessage.TelegramThreadID != 4 {
		t.Fatalf("stopMessage = %#v, want thread 4 stop", router.stopMessage)
	}
	if len(sender.msgs) != 1 || !strings.HasPrefix(sender.msgs[0].Text, "(thread 4)\n\n") {
		t.Fatalf("msgs = %#v, want thread-prefixed stop reply", sender.msgs)
	}
}

func TestThreadPrefixOverridesReplyTarget(t *testing.T) {
	t.Parallel()

	replyTo := int64(7007)
	sender := &stubCommandSender{}
	router := &stubCommandRouter{
		threadsReturn:     []session.TelegramThread{{ChatID: 1001, ThreadID: 7, DisplaySlot: 7, Status: session.TelegramThreadStatusOpen}},
		threadReplyOK:     true,
		threadReplyReturn: session.TelegramThread{ChatID: 1001, ThreadID: 4, Status: session.TelegramThreadStatusOpen},
	}
	routed, handled, err := resolveTelegramThreadPrefix(context.Background(), sender, router, core.InboundMessage{
		ChatID:    1001,
		SenderID:  2002,
		MessageID: 3003,
		ReplyTo:   &replyTo,
		Text:      "(thread 7) continue that plan",
	})
	if err != nil {
		t.Fatalf("resolveTelegramThreadPrefix() err = %v", err)
	}
	if handled {
		t.Fatal("handled = true, want explicit thread prefix to continue without reply lookup")
	}
	if router.threadRouteID != 7 || router.threadRouteText != "continue that plan" {
		t.Fatalf("thread route id=%d text=%q, want explicit thread 7 payload", router.threadRouteID, router.threadRouteText)
	}
	if routed.TelegramThreadID != 7 || routed.Text != "continue that plan" {
		t.Fatalf("routed = %#v, want explicit thread 7 payload", routed)
	}
	if router.threadReplyMessageID != 0 {
		t.Fatalf("reply lookup message = %d, want no reply lookup after explicit prefix", router.threadReplyMessageID)
	}
}

func TestThreadsCommandListsAbsorbButtons(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := &stubCommandRouter{
		threadsReturn: []session.TelegramThread{
			{ChatID: 1001, ThreadID: 2, Status: session.TelegramThreadStatusOpen, CreatedText: "second task"},
			{ChatID: 1001, ThreadID: 1, Status: session.TelegramThreadStatusClosed, CreatedText: "first task"},
		},
	}
	handled, err := handleTelegramCommand(context.Background(), sender, router, core.InboundMessage{
		ChatID:    1001,
		SenderID:  2002,
		MessageID: 3003,
		Text:      "/threads",
	})
	if err != nil {
		t.Fatalf("handleTelegramCommand(/threads) err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if router.threadsChatID != 1001 {
		t.Fatalf("threadsChatID = %d, want 1001", router.threadsChatID)
	}
	if len(sender.inline) != 1 || !strings.Contains(sender.inline[0].text, "thread 2:") {
		t.Fatalf("inline = %#v, want thread list", sender.inline)
	}
	if !commandRowsContain(sender.inline[0].rows, "Analyze", telegramThreadSummaryCallbackData) || !commandRowsContain(sender.inline[0].rows, "2", "thread_detail:2") {
		t.Fatalf("rows = %#v, want analyze and detail buttons", sender.inline[0].rows)
	}
}

func TestThreadsCommandShowsDisplaySlotWithCanonicalAbsorbCallback(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := &stubCommandRouter{
		threadsReturn: []session.TelegramThread{
			{ChatID: 1001, ThreadID: 42, DisplaySlot: 1, Status: session.TelegramThreadStatusOpen, CreatedText: "current side task"},
		},
	}
	handled, err := handleTelegramCommand(context.Background(), sender, router, core.InboundMessage{
		ChatID:    1001,
		SenderID:  2002,
		MessageID: 3003,
		Text:      "/threads",
	})
	if err != nil {
		t.Fatalf("handleTelegramCommand(/threads) err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if len(sender.inline) != 1 || !strings.Contains(sender.inline[0].text, "thread 1:") || strings.Contains(sender.inline[0].text, "thread 42:") {
		t.Fatalf("inline = %#v, want visible display slot", sender.inline)
	}
	if !commandRowsContain(sender.inline[0].rows, "1", "thread_detail:42") {
		t.Fatalf("rows = %#v, want display-slot label with canonical detail callback", sender.inline[0].rows)
	}
}

func TestAbsorbCommandWithoutArgumentShowsThreadBoard(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := &stubCommandRouter{
		threadsReturn: []session.TelegramThread{
			{ChatID: 1001, ThreadID: 4, Status: session.TelegramThreadStatusOpen, CreatedText: "side task"},
		},
	}
	handled, err := handleTelegramCommand(context.Background(), sender, router, core.InboundMessage{
		ChatID:    1001,
		SenderID:  2002,
		MessageID: 3003,
		Text:      "/absorb",
	})
	if err != nil {
		t.Fatalf("handleTelegramCommand(/absorb) err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if len(sender.inline) != 1 || !commandRowsContain(sender.inline[0].rows, "4", "thread_detail:4") {
		t.Fatalf("inline = %#v, want thread board detail button", sender.inline)
	}
}

func TestThreadAbsorbCallbackClosesThroughRouter(t *testing.T) {
	t.Parallel()

	var order []string
	sender := &stubCommandSender{order: &order}
	router := &stubCommandRouter{absorbThreadReturn: "Absorbed thread 3.", order: &order}
	handled, err := handleTelegramCommandCallback(context.Background(), sender, router, telegram.CallbackQuery{
		ID:   "cb-thread-absorb",
		From: &telegram.User{ID: 2002},
		Data: encodeTelegramThreadAbsorbCallback(3),
		Message: &telegram.Message{
			MessageID: 3003,
			Chat:      &telegram.Chat{ID: 1001, Type: "private"},
		},
	})
	if err != nil {
		t.Fatalf("handleTelegramCommandCallback() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if router.absorbThreadChatID != 1001 || router.absorbThreadSenderID != 2002 || router.absorbThreadID != 3 {
		t.Fatalf("absorb inputs chat=%d sender=%d thread=%d", router.absorbThreadChatID, router.absorbThreadSenderID, router.absorbThreadID)
	}
	if len(sender.answers) != 1 || sender.answers[0].text != "Absorbing." {
		t.Fatalf("answers = %#v, want early absorbing acknowledgement", sender.answers)
	}
	if len(order) < 2 || order[0] != "answer:Absorbing." || order[1] != "absorb" {
		t.Fatalf("order = %#v, want callback answer before absorb", order)
	}
	if len(sender.editClear) != 1 || sender.editClear[0].text != "Absorbed thread 3." {
		t.Fatalf("editClear = %#v, want callback message updated", sender.editClear)
	}
}

func TestThreadAbsorbCallbackRunsWhenEarlyAckIsStale(t *testing.T) {
	t.Parallel()

	staleErr := errors.New("telegram answerCallbackQuery failed: Bad Request: query is too old and response timeout expired or query ID is invalid")
	sender := &stubCommandSender{answerErr: staleErr}
	router := &stubCommandRouter{absorbThreadReturn: "Absorbed thread 3."}
	handled, err := handleTelegramCommandCallback(context.Background(), sender, router, telegram.CallbackQuery{
		ID:   "cb-thread-absorb-stale",
		From: &telegram.User{ID: 2002},
		Data: encodeTelegramThreadAbsorbCallback(3),
		Message: &telegram.Message{
			MessageID: 3003,
			Chat:      &telegram.Chat{ID: 1001, Type: "private"},
		},
	})
	if err != nil {
		t.Fatalf("handleTelegramCommandCallback() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if router.absorbThreadChatID != 1001 || router.absorbThreadSenderID != 2002 || router.absorbThreadID != 3 {
		t.Fatalf("absorb inputs chat=%d sender=%d thread=%d", router.absorbThreadChatID, router.absorbThreadSenderID, router.absorbThreadID)
	}
	if len(sender.answers) != 1 || sender.answers[0].text != "Absorbing." {
		t.Fatalf("answers = %#v, want attempted early absorbing acknowledgement", sender.answers)
	}
	if len(sender.editClear) != 1 || sender.editClear[0].text != "Absorbed thread 3." {
		t.Fatalf("editClear = %#v, want callback message updated after stale ack", sender.editClear)
	}
}

func TestTelegramThreadReplyRoutesToOpenThread(t *testing.T) {
	t.Parallel()

	replyTo := int64(7007)
	sender := &stubCommandSender{}
	router := &stubCommandRouter{
		threadReplyOK:     true,
		threadReplyReturn: session.TelegramThread{ChatID: 1001, ThreadID: 4, Status: session.TelegramThreadStatusOpen},
	}
	routed, handled, err := resolveTelegramThreadReply(context.Background(), sender, router, core.InboundMessage{
		ChatID:    1001,
		SenderID:  2002,
		MessageID: 3003,
		ReplyTo:   &replyTo,
		Text:      "continue that work",
	})
	if err != nil {
		t.Fatalf("resolveTelegramThreadReply() err = %v", err)
	}
	if handled {
		t.Fatal("handled = true, want routed message to continue through ingress")
	}
	if router.threadReplyChatID != 1001 || router.threadReplyMessageID != replyTo {
		t.Fatalf("reply lookup chat=%d message=%d", router.threadReplyChatID, router.threadReplyMessageID)
	}
	if routed.TelegramThreadID != 4 {
		t.Fatalf("TelegramThreadID = %d, want 4", routed.TelegramThreadID)
	}
}

func TestTelegramThreadReplyLeavesSlashCommandGlobal(t *testing.T) {
	t.Parallel()

	replyTo := int64(7007)
	sender := &stubCommandSender{}
	router := &stubCommandRouter{
		threadReplyOK:     true,
		threadReplyReturn: session.TelegramThread{ChatID: 1001, ThreadID: 4, Status: session.TelegramThreadStatusOpen},
	}
	routed, handled, err := resolveTelegramThreadReply(context.Background(), sender, router, core.InboundMessage{
		ChatID:    1001,
		SenderID:  2002,
		MessageID: 3003,
		ReplyTo:   &replyTo,
		Text:      "/status",
	})
	if err != nil {
		t.Fatalf("resolveTelegramThreadReply() err = %v", err)
	}
	if handled {
		t.Fatal("handled = true, want slash command to remain global")
	}
	if router.threadReplyMessageID != 0 {
		t.Fatalf("reply lookup message = %d, want no lookup for slash command", router.threadReplyMessageID)
	}
	if routed.TelegramThreadID != 0 {
		t.Fatalf("TelegramThreadID = %d, want global thread 0", routed.TelegramThreadID)
	}
}

func TestTelegramThreadReplyToClosedThreadIsUserFacing(t *testing.T) {
	t.Parallel()

	replyTo := int64(7007)
	sender := &stubCommandSender{}
	router := &stubCommandRouter{
		threadReplyOK:     true,
		threadReplyReturn: session.TelegramThread{ChatID: 1001, ThreadID: 4, Status: session.TelegramThreadStatusClosed},
	}
	_, handled, err := resolveTelegramThreadReply(context.Background(), sender, router, core.InboundMessage{
		ChatID:    1001,
		SenderID:  2002,
		MessageID: 3003,
		ReplyTo:   &replyTo,
		Text:      "continue that work",
	})
	if err != nil {
		t.Fatalf("resolveTelegramThreadReply() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want closed-thread reply handled with user-facing message")
	}
	if len(sender.msgs) != 1 || !strings.Contains(sender.msgs[0].Text, "Thread 4 is closed.") {
		t.Fatalf("msgs = %#v, want closed-thread message", sender.msgs)
	}
}

func TestThreadsCommandDefaultsToOpenAndShowsNonOpenView(t *testing.T) {
	t.Parallel()

	threads := []session.TelegramThread{
		{ChatID: 1001, ThreadID: 10, DisplaySlot: 1, Status: session.TelegramThreadStatusOpen, CreatedText: "open task"},
		{ChatID: 1001, ThreadID: 9, ArchivedDisplayName: "1-2026-05-17", Status: session.TelegramThreadStatusClosed, CreatedText: "closed task"},
	}
	rendered, rows := renderTelegramThreadsPanel(threads, telegramPageViewList, 1)
	if !strings.Contains(rendered, "thread 1:") || strings.Contains(rendered, "1-2026-05-17") {
		t.Fatalf("open view = %q, want only open display slot", rendered)
	}
	if !commandRowsContain(rows, "Show absorbed", "page:threads:nonopen:1") {
		t.Fatalf("rows = %#v, want Show absorbed", rows)
	}

	rendered, rows = renderTelegramThreadsPanel(threads, telegramPageViewNonOpen, 1)
	if !strings.Contains(rendered, "1-2026-05-17: closed") || strings.Contains(rendered, "thread 1: open") {
		t.Fatalf("non-open view = %q, want archived row only", rendered)
	}
	if !commandRowsContain(rows, "Show open", "page:threads:list:1") {
		t.Fatalf("rows = %#v, want Show open", rows)
	}
}

func TestAbsorbCommandResolvesOpenDisplaySlotToCanonicalThreadID(t *testing.T) {
	t.Parallel()

	router := &stubCommandRouter{
		threadsReturn: []session.TelegramThread{
			{ChatID: 1001, ThreadID: 42, DisplaySlot: 1, Status: session.TelegramThreadStatusOpen},
			{ChatID: 1001, ThreadID: 99, DisplaySlot: 2, Status: session.TelegramThreadStatusOpen},
		},
	}
	sender := &stubCommandSender{}
	msg := core.InboundMessage{ChatID: 1001, SenderID: 2002, MessageID: 3003, Text: "/absorb 1"}

	handled, err := handleTelegramThreadCommand(context.Background(), sender, router, msg, "absorb")
	if err != nil {
		t.Fatalf("handleTelegramThreadCommand() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if router.absorbThreadID != 42 {
		t.Fatalf("absorbThreadID = %d, want canonical thread 42 from display slot 1", router.absorbThreadID)
	}
}

func TestThreadPrefixResolvesOpenDisplaySlotToCanonicalThreadID(t *testing.T) {
	t.Parallel()

	router := &stubCommandRouter{
		threadsReturn: []session.TelegramThread{
			{ChatID: 1001, ThreadID: 42, DisplaySlot: 1, Status: session.TelegramThreadStatusOpen},
		},
	}
	sender := &stubCommandSender{}
	msg := core.InboundMessage{ChatID: 1001, SenderID: 2002, MessageID: 3003, Text: "(thread 1) continue the work"}

	routed, handled, err := resolveTelegramThreadPrefix(context.Background(), sender, router, msg)
	if err != nil {
		t.Fatalf("resolveTelegramThreadPrefix() err = %v", err)
	}
	if handled {
		t.Fatal("handled = true, want routed message to continue")
	}
	if routed.TelegramThreadID != 42 || router.threadRouteID != 42 {
		t.Fatalf("routed thread=%d routeID=%d, want canonical thread 42", routed.TelegramThreadID, router.threadRouteID)
	}
}

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
	if !commandRowsContain(sender.inline[0].rows, "Analyze", "thread_summary") {
		t.Fatalf("rows = %#v, want Analyze", sender.inline[0].rows)
	}
	if !commandRowsContain(sender.inline[0].rows, "1", "thread_detail:42") {
		t.Fatalf("rows = %#v, want thread detail callback for display slot 1", sender.inline[0].rows)
	}
	if commandRowsContain(sender.inline[0].rows, "Promote 1", "thread_promote:42") || commandRowsContain(sender.inline[0].rows, "Absorb 1", "thread_absorb:42") {
		t.Fatalf("rows = %#v, want promote/absorb moved out of board", sender.inline[0].rows)
	}
	if !strings.Contains(sender.inline[0].text, "Side Threads") || !strings.Contains(sender.inline[0].text, "Next:") {
		t.Fatalf("panel text = %q, want operator board guidance", sender.inline[0].text)
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
	if wantHandoffID == "thread-promotion:1001:3:99" && data != "thread_promo_"+wantAction+":"+wantHandoffID {
		t.Fatalf("callback data = %q, want legacy typed handoff callback for %s", data, wantAction)
	}
}
