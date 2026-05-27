//go:build linux

package telegramcommands

import (
	"context"
	"strings"
	"testing"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/telegram"
)

func TestDurableAgentDetailCallbackRecordsMessageAndShowsLifecycleControls(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := &stubCommandRouter{
		canRestart: true,
		durableAgentsList: []core.DurableAgentStatusSnapshot{
			{
				AgentID:                   "ops-child",
				ChannelKind:               "telegram_dm",
				Status:                    "active",
				Health:                    "ok",
				PolicyVersion:             3,
				TailnetMode:               "tsnet",
				TailnetHostname:           "ops-child",
				ChildRuntimeGrantCount:    2,
				ChildRuntimeBlockedReason: "Codex app-server heartbeat grant expired.",
			},
		},
	}

	handled, err := handleTelegramCommandCallback(context.Background(), sender, router, telegram.CallbackQuery{
		ID:   "cb-agent-detail",
		From: &telegram.User{ID: 1001},
		Data: encodeDurableAgentsDetailCallbackData("ops-child", telegramPageViewList, 1),
		Message: &telegram.Message{
			MessageID: 7007,
			Chat:      &telegram.Chat{ID: 9009, Type: "private"},
		},
	})
	if err != nil {
		t.Fatalf("handleTelegramCommandCallback(agent detail) err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if len(sender.editInline) != 1 {
		t.Fatalf("editInline = %#v, want one detail edit", sender.editInline)
	}
	if got := sender.editInline[0].text; !strings.Contains(got, "Durable Agent") ||
		!strings.Contains(got, "Agent: ops-child") ||
		!strings.Contains(got, "Tailnet host: ops-child") ||
		!strings.Contains(got, "Approvals: 2") {
		t.Fatalf("detail text = %q, want governed agent detail", got)
	}
	for _, want := range []struct {
		label string
		data  string
	}{
		{"Brief", encodeDurableAgentsActionCallbackData(durableAgentsCallbackBrief, "ops-child", telegramPageViewList, 1)},
		{"Park", encodeDurableAgentsActionCallbackData(durableAgentsCallbackPark, "ops-child", telegramPageViewList, 1)},
		{"Retire", encodeDurableAgentsActionCallbackData(durableAgentsCallbackRetireAsk, "ops-child", telegramPageViewList, 1)},
		{"Back", encodeDurableAgentsBackCallbackData(telegramPageViewList, 1)},
	} {
		if !commandRowsContain(sender.editInline[0].rows, want.label, want.data) {
			t.Fatalf("detail rows = %#v, want %s callback", sender.editInline[0].rows, want.label)
		}
	}
	if router.agentCallbackChatID != 9009 || router.agentCallbackMessageID != 7007 ||
		router.agentCallbackAgentID != "ops-child" || router.agentCallbackSurface != "agent_detail" {
		t.Fatalf("agent callback ledger = chat:%d msg:%d agent:%q surface:%q, want detail ledger",
			router.agentCallbackChatID, router.agentCallbackMessageID, router.agentCallbackAgentID, router.agentCallbackSurface)
	}
}

func TestDurableAgentRetireCallbackRequiresConfirmationThenRunsLifecycleAction(t *testing.T) {
	t.Parallel()

	agent := core.DurableAgentStatusSnapshot{
		AgentID:     "ops-child",
		ChannelKind: "telegram_dm",
		Status:      "active",
		Health:      "ok",
	}
	askSender := &stubCommandSender{}
	askRouter := &stubCommandRouter{canRestart: true, durableAgentsList: []core.DurableAgentStatusSnapshot{agent}}
	handled, err := handleTelegramCommandCallback(context.Background(), askSender, askRouter, telegram.CallbackQuery{
		ID:   "cb-agent-retire-ask",
		From: &telegram.User{ID: 1001},
		Data: encodeDurableAgentsActionCallbackData(durableAgentsCallbackRetireAsk, "ops-child", telegramPageViewList, 1),
		Message: &telegram.Message{
			MessageID: 7007,
			Chat:      &telegram.Chat{ID: 9009, Type: "private"},
		},
	})
	if err != nil {
		t.Fatalf("handleTelegramCommandCallback(retire ask) err = %v", err)
	}
	if !handled || len(askSender.editInline) != 1 {
		t.Fatalf("handled=%t editInline=%#v, want confirmation edit", handled, askSender.editInline)
	}
	if got := askSender.editInline[0].text; !strings.Contains(got, "Retire Agent?") ||
		!strings.Contains(got, "Retire removes this child from active use") {
		t.Fatalf("retire ask text = %q, want confirmation details", got)
	}
	if askRouter.durableLifecycleAction != "" {
		t.Fatalf("durableLifecycleAction = %q, want no mutation before confirmation", askRouter.durableLifecycleAction)
	}
	if !commandRowsContain(askSender.editInline[0].rows, "Brief", encodeDurableAgentsActionCallbackData(durableAgentsCallbackRetireBrief, "ops-child", telegramPageViewList, 1)) ||
		!commandRowsContain(askSender.editInline[0].rows, "Retire", encodeDurableAgentsActionCallbackData(durableAgentsCallbackRetireConfirm, "ops-child", telegramPageViewList, 1)) {
		t.Fatalf("retire ask rows = %#v, want brief and confirm controls", askSender.editInline[0].rows)
	}

	confirmSender := &stubCommandSender{}
	confirmRouter := &stubCommandRouter{
		canRestart:             true,
		durableAgentsList:      []core.DurableAgentStatusSnapshot{agent},
		durableLifecycleResult: "action: durable-agent retire\nagent_id: ops-child\nstatus: retired",
	}
	handled, err = handleTelegramCommandCallback(context.Background(), confirmSender, confirmRouter, telegram.CallbackQuery{
		ID:   "cb-agent-retire-confirm",
		From: &telegram.User{ID: 1001},
		Data: encodeDurableAgentsActionCallbackData(durableAgentsCallbackRetireConfirm, "ops-child", telegramPageViewList, 1),
		Message: &telegram.Message{
			MessageID: 7007,
			Chat:      &telegram.Chat{ID: 9009, Type: "private"},
		},
	})
	if err != nil {
		t.Fatalf("handleTelegramCommandCallback(retire confirm) err = %v", err)
	}
	if !handled || confirmRouter.durableLifecycleAction != "retire" || confirmRouter.durableLifecycleAgentID != "ops-child" {
		t.Fatalf("handled=%t lifecycle action=%q agent=%q, want retire ops-child", handled, confirmRouter.durableLifecycleAction, confirmRouter.durableLifecycleAgentID)
	}
	if len(confirmSender.editClear) != 1 || !strings.HasPrefix(confirmSender.editClear[0].text, "(agent ops-child)\n\n") ||
		!strings.Contains(confirmSender.editClear[0].text, "status: retired") {
		t.Fatalf("editClear = %#v, want prefixed retire result without stale buttons", confirmSender.editClear)
	}
}

func TestDurableAgentsAnalyzeCallbackQueuesAnalysisTurn(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := &stubCommandRouter{canRestart: true}
	handled, err := handleTelegramCommandCallback(context.Background(), sender, router, telegram.CallbackQuery{
		ID:       "cb-agent-analyze",
		UpdateID: 8801,
		From:     &telegram.User{ID: 1001},
		Data:     encodeDurableAgentsAnalyzeCallbackData(),
		Message: &telegram.Message{
			MessageID: 7007,
			Chat:      &telegram.Chat{ID: 9009, Type: "private"},
		},
	})
	if err != nil {
		t.Fatalf("handleTelegramCommandCallback(analyze) err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if router.agentAnalyzeMsg == nil {
		t.Fatal("agentAnalyzeMsg = nil, want queued analysis request")
	}
	if router.agentAnalyzeMsg.ChatID != 9009 || router.agentAnalyzeMsg.SenderID != 1001 ||
		router.agentAnalyzeMsg.MessageID != 7007 || router.agentAnalyzeMsg.IngressUpdateID != 8801 ||
		router.agentAnalyzeMsg.IngressSurface != telegramAgentsAnalyzeIngressSurface ||
		router.agentAnalyzeMsg.Text != "/agents analyze" {
		t.Fatalf("agentAnalyzeMsg = %#v, want callback-backed analyze request", *router.agentAnalyzeMsg)
	}
	if len(sender.answers) != 1 || sender.answers[0].text != "Agent board analysis queued." {
		t.Fatalf("answers = %#v, want queued ack", sender.answers)
	}
}

func TestTelegramAgentReplyRoutesParentMessageToLedgeredAgent(t *testing.T) {
	t.Parallel()

	replyTo := int64(7007)
	sender := &stubCommandSender{}
	router := &stubCommandRouter{
		agentReplyOK:       true,
		agentReplyAgentID:  "ops-child",
		startDurableResult: "Queued parent message for durable agent ops-child.",
	}
	handled, err := resolveTelegramAgentReply(context.Background(), sender, router, core.InboundMessage{
		ChatID:    9009,
		SenderID:  1001,
		MessageID: 7008,
		ReplyTo:   &replyTo,
		Text:      "Please check the status of the remote worker.",
	})
	if err != nil {
		t.Fatalf("resolveTelegramAgentReply() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want reply routed to agent")
	}
	if router.agentReplyChatID != 9009 || router.agentReplyMessageID != replyTo {
		t.Fatalf("agent reply lookup = chat:%d msg:%d, want 9009/%d", router.agentReplyChatID, router.agentReplyMessageID, replyTo)
	}
	if router.startDurableAgentID != "ops-child" ||
		router.startDurableMessage != "Please check the status of the remote worker." {
		t.Fatalf("agent message = agent:%q text:%q, want parent message to ops-child", router.startDurableAgentID, router.startDurableMessage)
	}
	if len(sender.msgs) != 1 || !strings.HasPrefix(sender.msgs[0].Text, "(agent ops-child)\n\n") ||
		sender.msgs[0].ReplyTo == nil || *sender.msgs[0].ReplyTo != 7008 {
		t.Fatalf("msgs = %#v, want prefixed reply ack", sender.msgs)
	}
	if router.agentCallbackAgentID != "ops-child" || router.agentCallbackMessageID != 1 || router.agentCallbackSurface != "agent_reply_ack" {
		t.Fatalf("ack ledger = agent:%q msg:%d surface:%q, want agent reply ack ledger",
			router.agentCallbackAgentID, router.agentCallbackMessageID, router.agentCallbackSurface)
	}
}
