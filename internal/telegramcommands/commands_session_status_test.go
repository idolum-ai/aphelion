//go:build linux

package telegramcommands

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/telegram"
)

func TestHandleTelegramCommandStop(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := stubCommandRouter{
		stop: core.StopResult{ActiveCanceled: true, QueuedDropped: true, ContinuationRevoked: true},
	}
	handled, err := handleTelegramCommand(context.Background(), sender, &router, core.InboundMessage{
		ChatID:    7,
		MessageID: 11,
		Text:      "/stop",
	})
	if err != nil {
		t.Fatalf("handleTelegramCommand() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if len(sender.msgs) != 1 {
		t.Fatalf("message count = %d, want 1", len(sender.msgs))
	}
	if sender.msgs[0].ReplyTo == nil || *sender.msgs[0].ReplyTo != 11 {
		t.Fatalf("reply_to = %#v, want 11", sender.msgs[0].ReplyTo)
	}
}

func TestHandleTelegramCommandNew(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := &stubCommandRouter{
		newResult: core.NewSessionResult{
			ActiveCanceled:           true,
			QueuedDropped:            true,
			ContinuationRevoked:      true,
			PendingDecisionsDetached: 1,
			ContextCleared:           true,
		},
	}
	handled, err := handleTelegramCommand(context.Background(), sender, router, core.InboundMessage{
		ChatID:    7,
		SenderID:  99,
		MessageID: 13,
		Text:      "/new",
	})
	if err != nil {
		t.Fatalf("handleTelegramCommand() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if router.newChatID != 7 || router.newSenderID != 99 {
		t.Fatalf("new inputs = (%d,%d), want (7,99)", router.newChatID, router.newSenderID)
	}
	if len(sender.msgs) != 1 {
		t.Fatalf("message count = %d, want 1", len(sender.msgs))
	}
	if got := sender.msgs[0].Text; !strings.Contains(got, "Started a new session for this chat") || !strings.Contains(got, "Memories were not changed") {
		t.Fatalf("new text = %q, want new-session summary", got)
	}
	if sender.msgs[0].ReplyTo == nil || *sender.msgs[0].ReplyTo != 13 {
		t.Fatalf("reply_to = %#v, want 13", sender.msgs[0].ReplyTo)
	}
}

func TestHandleTelegramCommandDetach(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := &stubCommandRouter{
		detach: core.DetachResult{
			ActiveCanceled:           true,
			QueuedDropped:            true,
			ContinuationRevoked:      true,
			PendingDecisionsDetached: 2,
		},
	}
	handled, err := handleTelegramCommand(context.Background(), sender, router, core.InboundMessage{
		ChatID:    7,
		SenderID:  99,
		MessageID: 12,
		Text:      "/detach",
	})
	if err != nil {
		t.Fatalf("handleTelegramCommand() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if router.detachChatID != 7 || router.detachSenderID != 99 {
		t.Fatalf("detach inputs = (%d,%d), want (7,99)", router.detachChatID, router.detachSenderID)
	}
	if len(sender.msgs) != 1 {
		t.Fatalf("message count = %d, want 1", len(sender.msgs))
	}
	if got := sender.msgs[0].Text; !strings.Contains(got, "Detached") || !strings.Contains(got, "2 pending") {
		t.Fatalf("detach text = %q, want detach summary including pending count", got)
	}
	if sender.msgs[0].ReplyTo == nil || *sender.msgs[0].ReplyTo != 12 {
		t.Fatalf("reply_to = %#v, want 12", sender.msgs[0].ReplyTo)
	}
}

func TestHandleTelegramCommandStatus(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := stubCommandRouter{
		statusChat: core.ChatStatusSnapshot{
			GeneratedAt:   time.Date(2026, 5, 7, 12, 0, 0, 0, time.UTC),
			ChatID:        7,
			ActiveTurnIDs: []uint64{91},
			QueueDepth:    2,
			PendingItems: []core.PendingItem{
				{Kind: core.PendingItemKindQueue, ChatID: 7, Summary: "queue_depth=2"},
			},
		},
		personaEffort:  "sonnet",
		governorEffort: "medium",
	}
	handled, err := handleTelegramCommand(context.Background(), sender, &router, core.InboundMessage{
		ChatID: 7,
		Text:   "/status",
	})
	if err != nil {
		t.Fatalf("handleTelegramCommand() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if len(sender.inline) != 1 {
		t.Fatalf("inline count = %d, want 1", len(sender.inline))
	}
	if got := sender.inline[0].text; !strings.Contains(got, "Status: working") || strings.Contains(got, "Status Scope: chat") {
		t.Fatalf("status text = %q, want human chat status without raw scope", got)
	}
	if got := sender.inline[0].text; !strings.Contains(got, "Next: wait for the active turn; tap Refresh to re-check") {
		t.Fatalf("status text = %q, want state-specific next action", got)
	}
	if got := sender.inline[0].text; !strings.Contains(got, "Evidence: as of 2026-05-07T12:00:00Z; source: chat status projection") {
		t.Fatalf("status text = %q, want as-of evidence line", got)
	}
	foundThisChat := false
	foundPending := false
	foundRefresh := false
	for _, row := range sender.inline[0].rows {
		for _, button := range row {
			switch button.Text {
			case "This Chat":
				foundThisChat = true
			case "Pending Only":
				foundPending = true
			case "Refresh":
				foundRefresh = true
			}
		}
	}
	if !foundThisChat || !foundPending || !foundRefresh {
		t.Fatalf("status keyboard rows = %#v, want user status controls", sender.inline[0].rows)
	}
}

func TestHandleTelegramCommandAgentsShowsButtons(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := &stubCommandRouter{
		canRestart: true,
		durableAgentsList: []core.DurableAgentStatusSnapshot{
			{
				AgentID:     "idolum-daily-review",
				ChannelKind: "scheduled_review",
				Status:      "active",
				Health:      "ok",
			},
			{
				AgentID:          "ops-child",
				ChannelKind:      "telegram_dm",
				Status:           "active",
				Health:           "dormant",
				TailnetMode:      "tsnet",
				TailnetHostname:  "ops-child",
				TailnetSurfaceID: "durable_agent:ops-child:tsnet_http:status",
			},
		},
	}
	handled, err := handleTelegramCommand(context.Background(), sender, router, core.InboundMessage{
		ChatID:    7,
		SenderID:  1001,
		MessageID: 55,
		Text:      "/agents",
	})
	if err != nil {
		t.Fatalf("handleTelegramCommand() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if router.durableAgentsListSenderID != 1001 {
		t.Fatalf("durableAgentsListSenderID = %d, want 1001", router.durableAgentsListSenderID)
	}
	if len(sender.inline) != 1 {
		t.Fatalf("inline count = %d, want 1", len(sender.inline))
	}
	if got := sender.inline[0].text; !strings.Contains(got, "Durable Agents") {
		t.Fatalf("agents text = %q, want Durable Agents heading", got)
	}
	if got := sender.inline[0].text; !strings.Contains(got, "ops-child (telegram_dm | active | dormant); tailnet:tsnet") {
		t.Fatalf("agents text = %q, want tailnet declaration marker", got)
	}
	foundAgent := false
	foundRefresh := false
	foundAnalyze := false
	for _, row := range sender.inline[0].rows {
		for _, button := range row {
			if words := strings.Fields(button.Text); len(words) > 2 {
				t.Fatalf("button label %q has %d words, want at most 2", button.Text, len(words))
			}
			if button.Text == "Agent 1" && button.CallbackData == encodeDurableAgentsDetailCallbackData("idolum-daily-review", telegramPageViewList, 1) {
				foundAgent = true
			}
			if button.Text == "Refresh" {
				foundRefresh = true
			}
			if button.Text == "Analyze" {
				foundAnalyze = true
			}
		}
	}
	if !foundAgent || !foundRefresh || !foundAnalyze {
		t.Fatalf("agents rows = %#v, want agent/detail, analyze, and refresh callbacks", sender.inline[0].rows)
	}
}

func TestHandleTelegramCommandContextShowsReadOnlyPanel(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := &stubCommandRouter{
		statusChat: core.ChatStatusSnapshot{
			GeneratedAt:      time.Date(2026, 5, 7, 12, 0, 0, 0, time.UTC),
			ChatID:           7,
			OperationStatus:  "active",
			OperationSummary: "Implement read-only panels",
			PlanStep:         "Update tests and docs",
		},
		memoryReviewBySource: map[memoryReviewSource]memoryReviewSnapshot{
			memoryReviewSourceSession: {
				Source: memoryReviewSourceSession,
				Query:  "context seed",
				Items:  []memoryReviewItem{{ID: "session:12:user", Label: "turn=12 role=user", Excerpt: "Current context evidence."}},
			},
		},
	}
	handled, err := handleTelegramCommand(context.Background(), sender, router, core.InboundMessage{
		ChatID:    7,
		SenderID:  1001,
		MessageID: 20,
		Text:      "/context",
	})
	if err != nil {
		t.Fatalf("handleTelegramCommand() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if len(sender.inline) != 1 {
		t.Fatalf("inline count = %d, want 1", len(sender.inline))
	}
	text := sender.inline[0].text
	for _, want := range []string{"Context", "read-only context snapshot", "Implement read-only panels", "Writes: none"} {
		if !strings.Contains(text, want) {
			t.Fatalf("context text = %q, want %q", text, want)
		}
	}
	foundAsk := false
	foundRefresh := false
	for _, row := range sender.inline[0].rows {
		for _, button := range row {
			if button.Text == "Ask Me" && button.CallbackData == "context:ask" {
				foundAsk = true
			}
			if button.Text == "Refresh" && button.CallbackData == "context:refresh" {
				foundRefresh = true
			}
		}
	}
	if !foundAsk || !foundRefresh {
		t.Fatalf("context rows = %#v, want Ask Me and Refresh", sender.inline[0].rows)
	}
}

func TestHandleTelegramCommandContextRecordsThreadCallbackMessage(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := &stubCommandRouter{
		statusChat: core.ChatStatusSnapshot{ChatID: 7},
		memoryReviewBySource: map[memoryReviewSource]memoryReviewSnapshot{
			memoryReviewSourceSession: {Source: memoryReviewSourceSession, Query: "thread context"},
		},
	}
	handled, err := handleTelegramCommand(context.Background(), sender, router, core.InboundMessage{
		ChatID:           7,
		SenderID:         1001,
		MessageID:        21,
		TelegramThreadID: 3,
		Text:             "/context",
	})
	if err != nil {
		t.Fatalf("handleTelegramCommand() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if router.threadCallbackChatID != 7 || router.threadCallbackID != 3 || router.threadCallbackMessageID != 1 || router.threadCallbackSurface != "context" {
		t.Fatalf("thread callback record = chat:%d thread:%d message:%d surface:%q, want 7/3/1/context", router.threadCallbackChatID, router.threadCallbackID, router.threadCallbackMessageID, router.threadCallbackSurface)
	}
}

func TestHandleTelegramCommandCallbackContextAskMeQueuesClarification(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := &stubCommandRouter{}
	handled, err := handleTelegramCommandCallback(context.Background(), sender, router, telegram.CallbackQuery{
		ID:       "cb-context-ask",
		From:     &telegram.User{ID: 1001, Username: "admin"},
		Data:     "context:ask",
		UpdateID: 808,
		Message: &telegram.Message{
			MessageID: 95,
			Chat:      &telegram.Chat{ID: 7, Type: "private"},
		},
	})
	if err != nil {
		t.Fatalf("handleTelegramCommandCallback() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if router.clarificationMsg == nil {
		t.Fatal("clarification not queued")
	}
	if got := router.clarificationMsg.Text; !strings.Contains(got, "Ask me concise clarifying questions about the current context") || !strings.Contains(got, "Do not write memory") {
		t.Fatalf("clarification text = %q, want context Ask Me prompt", got)
	}
	if router.clarificationMsg.IngressSurface != telegramContextClarificationIngressSurface || router.clarificationMsg.IngressUpdateID != 808 {
		t.Fatalf("clarification ingress = %s/%d, want context clarification callback work", router.clarificationMsg.IngressSurface, router.clarificationMsg.IngressUpdateID)
	}
}

func TestHandleTelegramCommandCallbackContextRefreshEditsPanel(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := &stubCommandRouter{
		statusChat: core.ChatStatusSnapshot{ChatID: 7, OperationSummary: "Fresh context"},
		memoryReviewBySource: map[memoryReviewSource]memoryReviewSnapshot{
			memoryReviewSourceSession: {Source: memoryReviewSourceSession, Query: "fresh", Items: []memoryReviewItem{{Excerpt: "Fresh recent context."}}},
		},
	}
	handled, err := handleTelegramCommandCallback(context.Background(), sender, router, telegram.CallbackQuery{
		ID:   "cb-context-refresh",
		From: &telegram.User{ID: 1001, Username: "admin"},
		Data: "context:refresh",
		Message: &telegram.Message{
			MessageID: 95,
			Chat:      &telegram.Chat{ID: 7, Type: "private"},
		},
	})
	if err != nil {
		t.Fatalf("handleTelegramCommandCallback() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if len(sender.editInline) != 1 {
		t.Fatalf("editInline count = %d, want 1", len(sender.editInline))
	}
	if got := sender.editInline[0].text; !strings.Contains(got, "Context") || !strings.Contains(got, "Fresh context") {
		t.Fatalf("context panel text = %q, want refreshed context", got)
	}
}

func TestHandleTelegramCommandMemoryShowsButtons(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := &stubCommandRouter{
		memoryReviewBySource: map[memoryReviewSource]memoryReviewSnapshot{
			memoryReviewSourceSession: {
				Source: memoryReviewSourceSession,
				Query:  "investigation thread",
				Items: []memoryReviewItem{
					{
						ID:      "session:12:user",
						Label:   "turn=12 role=user",
						Excerpt: "Can you investigate alternatives for the architecture?",
					},
					{
						ID:      "session:13:assistant",
						Label:   "turn=13 role=assistant",
						Excerpt: "I identified three options with different trade-offs.",
					},
				},
			},
		},
	}
	handled, err := handleTelegramCommand(context.Background(), sender, router, core.InboundMessage{
		ChatID:    7,
		SenderID:  1001,
		MessageID: 21,
		Text:      "/memory",
	})
	if err != nil {
		t.Fatalf("handleTelegramCommand() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if router.memoryReviewChatID != 7 || router.memoryReviewSenderID != 1001 || router.memoryReviewSource != memoryReviewSourceSession {
		t.Fatalf("memory review routing = chat:%d sender:%d source:%q", router.memoryReviewChatID, router.memoryReviewSenderID, router.memoryReviewSource)
	}
	if len(sender.inline) != 1 {
		t.Fatalf("inline count = %d, want 1", len(sender.inline))
	}
	if got := sender.inline[0].text; !strings.Contains(got, "Memory") || !strings.Contains(got, "read-only memory state") {
		t.Fatalf("memory text = %q, want read-only Memory panel", got)
	}
	foundAsk := false
	for _, row := range sender.inline[0].rows {
		for _, button := range row {
			if button.Text == "Ask Me" && strings.Contains(button.CallbackData, "memory:ask:session") {
				foundAsk = true
				break
			}
		}
	}
	if !foundAsk {
		t.Fatalf("rows = %#v, want Ask Me callback button", sender.inline[0].rows)
	}
}

func TestHandleTelegramCommandMemoryRecordsThreadCallbackMessage(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := &stubCommandRouter{
		memoryReviewBySource: map[memoryReviewSource]memoryReviewSnapshot{
			memoryReviewSourceSession: {Source: memoryReviewSourceSession, Query: "thread memory"},
		},
	}
	handled, err := handleTelegramCommand(context.Background(), sender, router, core.InboundMessage{
		ChatID:           7,
		SenderID:         1001,
		MessageID:        21,
		TelegramThreadID: 3,
		Text:             "/memory",
	})
	if err != nil {
		t.Fatalf("handleTelegramCommand() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if router.threadCallbackChatID != 7 || router.threadCallbackID != 3 || router.threadCallbackMessageID != 1 || router.threadCallbackSurface != "memory" {
		t.Fatalf("thread callback record = chat:%d thread:%d message:%d surface:%q, want 7/3/1/memory", router.threadCallbackChatID, router.threadCallbackID, router.threadCallbackMessageID, router.threadCallbackSurface)
	}
}

func TestHandleTelegramCommandCallbackAgentsStartInvokesBackgroundConversation(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := &stubCommandRouter{
		canRestart:         true,
		startDurableResult: "Started background conversation with durable agent idolum-daily-review (wake requested).",
		durableAgentsList: []core.DurableAgentStatusSnapshot{{
			AgentID:     "idolum-daily-review",
			ChannelKind: "scheduled_review",
			Status:      "active",
			Health:      "ok",
		}},
	}
	handled, err := handleTelegramCommandCallback(context.Background(), sender, router, telegram.CallbackQuery{
		ID:   "cb-agents-start",
		From: &telegram.User{ID: 1001, Username: "admin"},
		Data: encodeDurableAgentsActionCallbackData(durableAgentsCallbackBrief, "idolum-daily-review", telegramPageViewList, 1),
		Message: &telegram.Message{
			MessageID: 88,
			Chat:      &telegram.Chat{ID: 7, Type: "private"},
		},
	})
	if err != nil {
		t.Fatalf("handleTelegramCommandCallback() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if router.startDurableChatID != 7 || router.startDurableSenderID != 1001 {
		t.Fatalf("start durable routing = chat:%d sender:%d, want chat:7 sender:1001", router.startDurableChatID, router.startDurableSenderID)
	}
	if router.startDurableAgentID != "idolum-daily-review" {
		t.Fatalf("startDurableAgentID = %q, want idolum-daily-review", router.startDurableAgentID)
	}
	if len(sender.msgs) != 1 {
		t.Fatalf("message count = %d, want 1", len(sender.msgs))
	}
	if got := sender.msgs[0].Text; !strings.Contains(got, "wake requested") {
		t.Fatalf("ack text = %q, want wake status", got)
	}
}

func TestHandleTelegramCommandCallbackMemoryAskMeQueuesClarification(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := &stubCommandRouter{}
	handled, err := handleTelegramCommandCallback(context.Background(), sender, router, telegram.CallbackQuery{
		ID:       "cb-memory-ask",
		From:     &telegram.User{ID: 1001, Username: "admin"},
		Data:     "memory:ask:session",
		UpdateID: 809,
		Message: &telegram.Message{
			MessageID: 95,
			Chat:      &telegram.Chat{ID: 7, Type: "private"},
		},
	})
	if err != nil {
		t.Fatalf("handleTelegramCommandCallback() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if router.clarificationMsg == nil {
		t.Fatal("clarification not queued")
	}
	if got := router.clarificationMsg.Text; !strings.Contains(got, "Ask me concise clarifying questions about memory") || strings.Contains(got, "write memory") && !strings.Contains(got, "Do not write memory") {
		t.Fatalf("clarification text = %q, want memory Ask Me prompt", got)
	}
	if router.clarificationMsg.IngressSurface != telegramMemoryClarificationIngressSurface || router.clarificationMsg.IngressUpdateID != 809 {
		t.Fatalf("clarification ingress = %s/%d, want memory clarification callback work", router.clarificationMsg.IngressSurface, router.clarificationMsg.IngressUpdateID)
	}
}

func TestHandleTelegramCommandCallbackMemoryAskMeTargetsDurableThreadMessage(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := &stubCommandRouter{
		threadReplyOK:     true,
		threadReplyReturn: session.TelegramThread{ChatID: 7, ThreadID: 3, Status: session.TelegramThreadStatusOpen},
	}
	handled, err := handleTelegramCommandCallback(context.Background(), sender, router, telegram.CallbackQuery{
		ID:   "cb-memory-thread",
		From: &telegram.User{ID: 1001, Username: "admin"},
		Data: "memory:ask:session",
		Message: &telegram.Message{
			MessageID: 95,
			Text:      "Memory",
			Chat:      &telegram.Chat{ID: 7, Type: "private"},
		},
	})
	if err != nil {
		t.Fatalf("handleTelegramCommandCallback() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if router.clarificationMsg == nil || router.clarificationMsg.TelegramThreadID != 3 {
		t.Fatalf("clarification msg = %#v, want thread 3", router.clarificationMsg)
	}
}

func TestHandleTelegramCommandCallbackMemoryAskMeDoesNotTrustThreadPrefixWithoutLedger(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := &stubCommandRouter{}
	handled, err := handleTelegramCommandCallback(context.Background(), sender, router, telegram.CallbackQuery{
		ID:   "cb-memory-prefix-only",
		From: &telegram.User{ID: 1001, Username: "admin"},
		Data: "memory:ask:session",
		Message: &telegram.Message{
			MessageID: 96,
			Text:      "(thread 3)\n\nMemory",
			Chat:      &telegram.Chat{ID: 7, Type: "private"},
		},
	})
	if err != nil {
		t.Fatalf("handleTelegramCommandCallback() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if router.clarificationMsg == nil {
		t.Fatal("clarification not queued")
	}
	if router.clarificationMsg.TelegramThreadID != 0 {
		t.Fatalf("clarification msg = %#v, want no thread from presentation text", router.clarificationMsg)
	}
}

func TestHandleTelegramCommandStatusIncludesReadableSummary(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := stubCommandRouter{
		statusChat: core.ChatStatusSnapshot{
			ChatID: 7,
		},
		statusReadableSummary: "Chat 7 is idle right now; no blocking pending items.",
		personaEffort:         "sonnet",
		governorEffort:        "medium",
	}
	handled, err := handleTelegramCommand(context.Background(), sender, &router, core.InboundMessage{
		ChatID: 7,
		Text:   "/status",
	})
	if err != nil {
		t.Fatalf("handleTelegramCommand() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if len(sender.inline) != 1 {
		t.Fatalf("inline count = %d, want 1", len(sender.inline))
	}
	if got := sender.inline[0].text; !strings.Contains(got, "Quick Read: Chat 7 is idle right now; no blocking pending items.") {
		t.Fatalf("status text = %q, want readable quick summary", got)
	}
	if got := sender.inline[0].text; !strings.Contains(got, "Status: idle") || strings.Contains(got, "Status Scope: chat") {
		t.Fatalf("status text = %q, want human status body without raw scope", got)
	}
}

func TestHandleTelegramCommandStatusRewritesInconsistentReadableSummary(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := stubCommandRouter{
		statusChat: core.ChatStatusSnapshot{
			ChatID:          7,
			OperationStatus: "blocked",
			PendingItems: []core.PendingItem{
				{Kind: core.PendingItemKindDecision, ChatID: 7, ID: "decision-1", Summary: "kind=proposal_approval"},
			},
		},
		statusReadableSummary: "Chat 7 is idle right now; no pending items.",
		personaEffort:         "sonnet",
		governorEffort:        "medium",
	}
	handled, err := handleTelegramCommand(context.Background(), sender, &router, core.InboundMessage{
		ChatID: 7,
		Text:   "/status",
	})
	if err != nil {
		t.Fatalf("handleTelegramCommand() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if len(sender.inline) != 1 {
		t.Fatalf("inline count = %d, want 1", len(sender.inline))
	}
	text := strings.ToLower(sender.inline[0].text)
	if strings.Contains(text, "idle right now; no pending items") {
		t.Fatalf("status text = %q, do not want inconsistent readable summary", sender.inline[0].text)
	}
	if !strings.Contains(text, "quick read: chat is blocked") {
		t.Fatalf("status text = %q, want grounded blocked quick summary", sender.inline[0].text)
	}
}

func TestHandleTelegramCommandStatusShowsAdminButtonsForAdmins(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := stubCommandRouter{
		statusChat:     core.ChatStatusSnapshot{ChatID: 7},
		statusSystem:   core.SystemStatusSnapshot{},
		personaEffort:  "opus",
		governorEffort: "high",
		canRestart:     true,
	}
	handled, err := handleTelegramCommand(context.Background(), sender, &router, core.InboundMessage{
		ChatID:   7,
		SenderID: 1001,
		Text:     "/status",
	})
	if err != nil {
		t.Fatalf("handleTelegramCommand() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if len(sender.inline) != 1 {
		t.Fatalf("inline count = %d, want 1", len(sender.inline))
	}
	foundSystem := false
	foundHot := false
	foundFind := false
	foundDurables := false
	for _, row := range sender.inline[0].rows {
		for _, button := range row {
			switch button.Text {
			case "System Overview":
				foundSystem = true
			case "Hot Chats":
				foundHot = true
			case "Find Chat":
				foundFind = true
			case "Durables":
				foundDurables = true
			}
		}
	}
	if !foundSystem || !foundHot || !foundFind || !foundDurables {
		t.Fatalf("admin status keyboard rows = %#v, want admin controls", sender.inline[0].rows)
	}
}

func TestHandleTelegramCommandStatusRecordsThreadCallbackMessage(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := &stubCommandRouter{
		canRestart: true,
		statusMessageSnapshot: core.ChatStatusSnapshot{
			GeneratedAt: time.Date(2026, 5, 7, 15, 0, 0, 0, time.UTC),
			ChatID:      7,
			PendingItems: []core.PendingItem{{
				Kind:    core.PendingItemKindDecision,
				ChatID:  7,
				ID:      "decision-thread-status",
				Summary: "Approve thread-local work.",
			}},
		},
	}
	handled, err := handleTelegramCommand(context.Background(), sender, router, core.InboundMessage{
		ChatID:           7,
		SenderID:         1001,
		MessageID:        40,
		TelegramThreadID: 3,
		Text:             "/status",
	})
	if err != nil {
		t.Fatalf("handleTelegramCommand() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if router.threadCallbackChatID != 7 || router.threadCallbackID != 3 || router.threadCallbackMessageID != 1 || router.threadCallbackSurface != "status" {
		t.Fatalf("thread callback record = chat:%d thread:%d message:%d surface:%q, want 7/3/1/status", router.threadCallbackChatID, router.threadCallbackID, router.threadCallbackMessageID, router.threadCallbackSurface)
	}
	if router.statusMessage == nil || router.statusMessage.TelegramThreadID != 3 {
		t.Fatalf("status message = %#v, want thread-scoped status lookup", router.statusMessage)
	}
	text := sender.inline[0].text
	if !strings.HasPrefix(text, "(thread 3)\n\n") {
		t.Fatalf("status text = %q, want thread prefix", text)
	}
	foundThisThread := false
	for _, row := range sender.inline[0].rows {
		for _, button := range row {
			if button.Text == "This Thread" {
				foundThisThread = true
			}
			if button.Text == "System Overview" || button.Text == "Hot Chats" || button.Text == "Find Chat" || button.Text == "Durables" {
				t.Fatalf("thread status rows = %#v, should not include global admin status controls", sender.inline[0].rows)
			}
		}
	}
	if !foundThisThread {
		t.Fatalf("thread status rows = %#v, want This Thread control", sender.inline[0].rows)
	}
}

func TestHandleTelegramCommandCallbackStatusPreservesThreadScope(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := &stubCommandRouter{
		canRestart:        true,
		threadReplyOK:     true,
		threadReplyReturn: session.TelegramThread{ChatID: 7, ThreadID: 3, DisplaySlot: 3, Status: session.TelegramThreadStatusOpen},
		statusMessageSnapshot: core.ChatStatusSnapshot{
			GeneratedAt: time.Date(2026, 5, 7, 15, 30, 0, 0, time.UTC),
			ChatID:      7,
			PendingItems: []core.PendingItem{{
				Kind:    core.PendingItemKindDecision,
				ChatID:  7,
				ID:      "decision-thread-status",
				Summary: "Approve thread-local work.",
			}},
		},
	}
	handled, err := handleTelegramCommandCallback(context.Background(), sender, router, telegram.CallbackQuery{
		ID:   "cb-thread-status-pending",
		From: &telegram.User{ID: 1001, Username: "admin"},
		Data: "status:pending",
		Message: &telegram.Message{
			MessageID: 99,
			Chat:      &telegram.Chat{ID: 7, Type: "private"},
		},
	})
	if err != nil {
		t.Fatalf("handleTelegramCommandCallback() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if router.statusMessage == nil || router.statusMessage.TelegramThreadID != 3 || router.statusMessage.MessageID != 99 {
		t.Fatalf("status message = %#v, want callback message resolved to thread 3", router.statusMessage)
	}
	if len(sender.editInline) != 1 {
		t.Fatalf("editInline count = %d, want 1", len(sender.editInline))
	}
	text := sender.editInline[0].text
	if !strings.HasPrefix(text, "(thread 3)\n\n") || !strings.Contains(text, "Needs Attention:") {
		t.Fatalf("thread status callback text = %q, want thread-prefixed pending view", text)
	}
	for _, row := range sender.editInline[0].rows {
		for _, button := range row {
			if button.Text == "System Overview" || button.Text == "Hot Chats" || button.Text == "Find Chat" || button.Text == "Durables" {
				t.Fatalf("thread status callback rows = %#v, should not include global admin status controls", sender.editInline[0].rows)
			}
		}
	}
}

func TestHandleTelegramCommandStatusShowsBlockedOperationSignal(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := stubCommandRouter{
		statusChat: core.ChatStatusSnapshot{
			GeneratedAt:      time.Date(2026, 5, 7, 13, 0, 0, 0, time.UTC),
			ChatID:           7,
			OperationStatus:  "blocked",
			OperationStage:   "approval_wait",
			OperationSummary: "Waiting for admin review",
			PlanStepStatus:   "in_progress",
			PlanStep:         "Await admin approval",
		},
		personaEffort:  "opus",
		governorEffort: "high",
	}
	handled, err := handleTelegramCommand(context.Background(), sender, &router, core.InboundMessage{
		ChatID: 7,
		Text:   "/status",
	})
	if err != nil {
		t.Fatalf("handleTelegramCommand() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if len(sender.inline) != 1 {
		t.Fatalf("inline count = %d, want 1", len(sender.inline))
	}
	if got := sender.inline[0].text; !strings.Contains(got, "Status: blocked") {
		t.Fatalf("status text = %q, want blocked status", got)
	}
	if got := sender.inline[0].text; !strings.Contains(got, "Why: Waiting for admin review") {
		t.Fatalf("status text = %q, want blocked operation reason", got)
	}
	if got := sender.inline[0].text; !strings.Contains(got, "Next: resolve the blocker above before continuing") {
		t.Fatalf("status text = %q, want blocked next action", got)
	}
}

func TestHandleTelegramCommandStatusUsesReadableCardInsteadOfRawDump(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := stubCommandRouter{
		statusChat: core.ChatStatusSnapshot{
			GeneratedAt:      time.Date(2026, 5, 7, 14, 0, 0, 0, time.UTC),
			ChatID:           7,
			OperationStatus:  "blocked",
			OperationStage:   "approval_wait",
			OperationSummary: "Waiting for admin review",
			PlanStepStatus:   "in_progress",
			PlanStep:         "Await admin approval",
			ToolLifecycle: []core.ToolLifecycleStatusSnapshot{{
				ToolName:      "browse_page",
				InstallStatus: "verified",
				ProbeStatus:   "passed",
				AuditStatus:   "passed",
			}},
			CapabilityGrants: []core.CapabilityGrantStatusSnapshot{{
				GrantID:        "capg-status",
				Kind:           "purchase",
				Status:         "active",
				GrantedTo:      "family-child",
				AllowedActions: []string{"order"},
			}},
		},
		personaEffort:  "opus",
		governorEffort: "high",
	}
	handled, err := handleTelegramCommand(context.Background(), sender, &router, core.InboundMessage{
		ChatID: 7,
		Text:   "/status",
	})
	if err != nil {
		t.Fatalf("handleTelegramCommand() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if len(sender.inline) != 1 {
		t.Fatalf("inline count = %d, want 1", len(sender.inline))
	}
	text := sender.inline[0].text
	for _, needle := range []string{
		"Status: blocked",
		"Why: Waiting for admin review",
		"Now: Await admin approval",
		"Next: resolve the blocker above before continuing",
		"Evidence: as of 2026-05-07T14:00:00Z; source: chat status projection",
		"Details: /health trace has the full execution trace and source attribution.",
	} {
		if !strings.Contains(text, needle) {
			t.Fatalf("status text = %q, want readable substring %q", text, needle)
		}
	}
	for _, forbidden := range []string{
		"Tool Lifecycle: Source:",
		"Capability Grants: Source:",
		"Source Attribution:",
	} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("status text = %q, should not include raw diagnostic block %q", text, forbidden)
		}
	}
}
