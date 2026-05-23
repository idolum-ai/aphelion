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
	if got := sender.inline[0].text; !strings.Contains(got, "ops-child (telegram_dm | active | dormant | tailnet:tsnet)") {
		t.Fatalf("agents text = %q, want tailnet declaration marker", got)
	}
	foundStart := false
	foundRefresh := false
	for _, row := range sender.inline[0].rows {
		for _, button := range row {
			if words := strings.Fields(button.Text); len(words) > 2 {
				t.Fatalf("button label %q has %d words, want at most 2", button.Text, len(words))
			}
			if strings.Contains(button.CallbackData, "agents:start:idolum-daily-review") {
				foundStart = true
			}
			if button.CallbackData == "agents:refresh" {
				foundRefresh = true
			}
		}
	}
	if !foundStart || !foundRefresh {
		t.Fatalf("agents rows = %#v, want start and refresh callbacks", sender.inline[0].rows)
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
	if got := sender.inline[0].text; !strings.Contains(got, "Memory Review") {
		t.Fatalf("memory text = %q, want Memory Review heading", got)
	}
	foundFocus := false
	for _, row := range sender.inline[0].rows {
		for _, button := range row {
			if strings.Contains(button.CallbackData, "memory:focus:session:1") {
				foundFocus = true
				break
			}
		}
	}
	if !foundFocus {
		t.Fatalf("rows = %#v, want focus callback button", sender.inline[0].rows)
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
	}
	handled, err := handleTelegramCommandCallback(context.Background(), sender, router, telegram.CallbackQuery{
		ID:   "cb-agents-start",
		From: &telegram.User{ID: 1001, Username: "admin"},
		Data: "agents:start:idolum-daily-review",
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

func TestHandleTelegramCommandCallbackMemoryFocusSetsFocus(t *testing.T) {
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
				},
			},
		},
	}
	handled, err := handleTelegramCommandCallback(context.Background(), sender, router, telegram.CallbackQuery{
		ID:   "cb-memory-focus",
		From: &telegram.User{ID: 1001, Username: "admin"},
		Data: "memory:focus:session:1",
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
	focus, ok := router.MemoryFocus(7)
	if !ok {
		t.Fatal("memory focus not set")
	}
	if focus.ItemID != "session:12:user" {
		t.Fatalf("focus item id = %q, want session:12:user", focus.ItemID)
	}
	if len(sender.editInline) != 1 {
		t.Fatalf("editInline count = %d, want 1", len(sender.editInline))
	}
	if got := sender.editInline[0].text; !strings.Contains(got, "Active Focus") {
		t.Fatalf("memory panel text = %q, want Active Focus section", got)
	}
}

func TestHandleTelegramCommandCallbackMemoryFocusTargetsDurableThreadMessage(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := &stubCommandRouter{
		threadReplyOK:     true,
		threadReplyReturn: session.TelegramThread{ChatID: 7, ThreadID: 3, Status: session.TelegramThreadStatusOpen},
		memoryReviewBySource: map[memoryReviewSource]memoryReviewSnapshot{
			memoryReviewSourceSession: {
				Source: memoryReviewSourceSession,
				Query:  "thread investigation",
				Items:  []memoryReviewItem{{ID: "session:12:user", Label: "turn=12 role=user", Excerpt: "Thread-specific evidence."}},
			},
		},
	}
	handled, err := handleTelegramCommandCallback(context.Background(), sender, router, telegram.CallbackQuery{
		ID:   "cb-memory-thread",
		From: &telegram.User{ID: 1001, Username: "admin"},
		Data: "memory:focus:session:1",
		Message: &telegram.Message{
			MessageID: 95,
			Text:      "Memory Review",
			Chat:      &telegram.Chat{ID: 7, Type: "private"},
		},
	})
	if err != nil {
		t.Fatalf("handleTelegramCommandCallback() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if router.threadReplyChatID != 7 || router.threadReplyMessageID != 95 {
		t.Fatalf("thread reply lookup = chat:%d message:%d, want 7/95", router.threadReplyChatID, router.threadReplyMessageID)
	}
	if router.memoryReviewMessage.TelegramThreadID != 3 || router.setMemoryFocusMessage.TelegramThreadID != 3 {
		t.Fatalf("memory messages = review:%#v set:%#v, want durable thread 3", router.memoryReviewMessage, router.setMemoryFocusMessage)
	}
	focus, ok := router.memoryFocusByThread[3]
	if !ok || focus.ItemID != "session:12:user" {
		t.Fatalf("thread focus = %#v ok=%t, want selected thread focus", focus, ok)
	}
}

func TestHandleTelegramCommandCallbackMemoryFocusDoesNotTrustThreadPrefixWithoutLedger(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := &stubCommandRouter{
		memoryReviewBySource: map[memoryReviewSource]memoryReviewSnapshot{
			memoryReviewSourceSession: {
				Source: memoryReviewSourceSession,
				Query:  "main investigation",
				Items:  []memoryReviewItem{{ID: "session:12:user", Label: "turn=12 role=user", Excerpt: "Main evidence."}},
			},
		},
	}
	handled, err := handleTelegramCommandCallback(context.Background(), sender, router, telegram.CallbackQuery{
		ID:   "cb-memory-prefix-only",
		From: &telegram.User{ID: 1001, Username: "admin"},
		Data: "memory:focus:session:1",
		Message: &telegram.Message{
			MessageID: 96,
			Text:      "(thread 3)\n\nMemory Review",
			Chat:      &telegram.Chat{ID: 7, Type: "private"},
		},
	})
	if err != nil {
		t.Fatalf("handleTelegramCommandCallback() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if router.setMemoryFocusMessage.TelegramThreadID != 0 || router.memoryReviewMessage.TelegramThreadID != 0 {
		t.Fatalf("memory messages = review:%#v set:%#v, want no thread from presentation text", router.memoryReviewMessage, router.setMemoryFocusMessage)
	}
	focus, ok := router.MemoryFocus(7)
	if !ok || focus.ItemID != "session:12:user" {
		t.Fatalf("main focus = %#v ok=%t, want selected main focus", focus, ok)
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

func TestHandleTelegramCommandStatusShowsBlockedOperationSignal(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := stubCommandRouter{
		statusChat: core.ChatStatusSnapshot{
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
}

func TestHandleTelegramCommandStatusUsesReadableCardInsteadOfRawDump(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := stubCommandRouter{
		statusChat: core.ChatStatusSnapshot{
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
