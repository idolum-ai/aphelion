//go:build linux

package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/telegram"
)

type stubCommandSender struct {
	msgs       []core.OutboundMessage
	inline     []stubInlineCall
	edits      []stubEditCall
	editInline []stubEditInlineCall
	editErr    error
	answers    []stubAnswerCall
	answerErr  error
}

type stubInlineCall struct {
	chatID  int64
	text    string
	rows    [][]telegram.InlineButton
	replyTo *int64
}

type stubEditCall struct {
	chatID    int64
	messageID int64
	text      string
	parseMode string
}

type stubEditInlineCall struct {
	chatID    int64
	messageID int64
	text      string
	parseMode string
	rows      [][]telegram.InlineButton
}

type stubAnswerCall struct {
	id   string
	text string
}

func (s *stubCommandSender) SendMessage(_ context.Context, msg core.OutboundMessage) (int64, error) {
	s.msgs = append(s.msgs, msg)
	return int64(len(s.msgs)), nil
}

func (s *stubCommandSender) SendInlineKeyboard(_ context.Context, chatID int64, text string, rows [][]telegram.InlineButton, replyTo *int64) (int64, error) {
	s.inline = append(s.inline, stubInlineCall{
		chatID:  chatID,
		text:    text,
		rows:    rows,
		replyTo: replyTo,
	})
	return int64(len(s.inline)), nil
}

func (s *stubCommandSender) EditMessageText(_ context.Context, chatID int64, messageID int64, text string, parseMode string) error {
	s.edits = append(s.edits, stubEditCall{
		chatID:    chatID,
		messageID: messageID,
		text:      text,
		parseMode: parseMode,
	})
	return s.editErr
}

func (s *stubCommandSender) EditMessageTextWithInlineKeyboard(_ context.Context, chatID int64, messageID int64, text string, parseMode string, rows [][]telegram.InlineButton) error {
	s.editInline = append(s.editInline, stubEditInlineCall{
		chatID:    chatID,
		messageID: messageID,
		text:      text,
		parseMode: parseMode,
		rows:      rows,
	})
	return s.editErr
}

func (s *stubCommandSender) AnswerCallbackQuery(_ context.Context, id string, text string) error {
	s.answers = append(s.answers, stubAnswerCall{
		id:   id,
		text: text,
	})
	return s.answerErr
}

type stubCommandRouter struct {
	status                      core.SessionStatus
	statusChat                  core.ChatStatusSnapshot
	statusSystem                core.SystemStatusSnapshot
	statusDurables              core.DurableAgentsStatusSnapshot
	statusReadableSummary       string
	statusChatErr               error
	statusSystemErr             error
	statusDurablesErr           error
	stop                        core.StopResult
	stopInput                   int64
	stopCalls                   int
	newResult                   core.NewSessionResult
	newErr                      error
	newChatID                   int64
	newSenderID                 int64
	detach                      core.DetachResult
	detachErr                   error
	detachChatID                int64
	detachSenderID              int64
	personaEffort               string
	governorEffort              string
	canRestart                  bool
	personaModel                string
	personaModelOptions         []string
	governorEffortOptions       []string
	setPersonaModelInput        string
	setGovernorEffortInput      string
	setPersonaModelReturn       string
	setGovernorEffortReturn     string
	setPersonaModelErr          error
	setGovernorEffortErr        error
	continuationState           session.ContinuationState
	continuationStateInput      int64
	continuationStateErr        error
	approveContinuationInput    int64
	approveContinuationApprover int64
	stopContinuationInput       int64
	stopContinuationResult      core.StopResult
	triggerContinuationInput    int64
	restartInput                int64
	restartCalls                int
	queuedReinstallMsg          *core.InboundMessage
	durableWizardChatID         int64
	durableWizardSenderID       int64
	durableWizardAction         string
	durableWizardAgentID        string
	durableWizardAnswers        map[string]any
	durableWizardResult         string
	durableWizardErr            error
	durableAgentsList           []core.DurableAgentStatusSnapshot
	durableAgentsListErr        error
	durableAgentsListSenderID   int64
	startDurableChatID          int64
	startDurableSenderID        int64
	startDurableAgentID         string
	startDurableResult          string
	startDurableErr             error
	memoryReviewBySource        map[memoryReviewSource]memoryReviewSnapshot
	memoryReviewErr             error
	memoryReviewChatID          int64
	memoryReviewSenderID        int64
	memoryReviewSource          memoryReviewSource
	memoryFocusByChat           map[int64]core.MemoryFocus
	clearMemoryFocusChatID      int64
	clearMemoryFocusResult      bool
}

func (s *stubCommandRouter) Stop(chatID int64) core.StopResult {
	s.stopInput = chatID
	s.stopCalls++
	return s.stop
}

func (s *stubCommandRouter) New(chatID int64, senderID int64) (core.NewSessionResult, error) {
	s.newChatID = chatID
	s.newSenderID = senderID
	if s.newErr != nil {
		return core.NewSessionResult{}, s.newErr
	}
	return s.newResult, nil
}

func (s *stubCommandRouter) Detach(chatID int64, senderID int64) (core.DetachResult, error) {
	s.detachChatID = chatID
	s.detachSenderID = senderID
	if s.detachErr != nil {
		return core.DetachResult{}, s.detachErr
	}
	return s.detach, nil
}

func (s stubCommandRouter) Status(chatID int64) core.SessionStatus {
	return s.status
}

func (s stubCommandRouter) StatusChat(chatID int64) (core.ChatStatusSnapshot, error) {
	if s.statusChatErr != nil {
		return core.ChatStatusSnapshot{}, s.statusChatErr
	}
	snapshot := s.statusChat
	if snapshot.ChatID == 0 {
		snapshot.ChatID = chatID
	}
	return snapshot, nil
}

func (s stubCommandRouter) StatusSystem(senderID int64) (core.SystemStatusSnapshot, error) {
	_ = senderID
	if s.statusSystemErr != nil {
		return core.SystemStatusSnapshot{}, s.statusSystemErr
	}
	return s.statusSystem, nil
}

func (s stubCommandRouter) StatusDurables(senderID int64) (core.DurableAgentsStatusSnapshot, error) {
	_ = senderID
	if s.statusDurablesErr != nil {
		return core.DurableAgentsStatusSnapshot{}, s.statusDurablesErr
	}
	return s.statusDurables, nil
}

func (s stubCommandRouter) StatusReadableSummary(ctx context.Context, view string, statusText string) string {
	_ = ctx
	_ = view
	_ = statusText
	return s.statusReadableSummary
}

func (s stubCommandRouter) CurrentEfforts() (string, string) {
	return s.personaEffort, s.governorEffort
}

func (s *stubCommandRouter) ContinuationState(chatID int64) (session.ContinuationState, error) {
	s.continuationStateInput = chatID
	if s.continuationStateErr != nil {
		return session.ContinuationState{}, s.continuationStateErr
	}
	return s.continuationState, nil
}

func (s *stubCommandRouter) ApproveContinuation(chatID int64, approverID int64) (session.ContinuationState, error) {
	s.approveContinuationInput = chatID
	s.approveContinuationApprover = approverID
	if s.continuationState.Status == "" {
		s.continuationState = session.ContinuationState{
			Status:         session.ContinuationStatusApproved,
			DecisionID:     "decision",
			RemainingTurns: 1,
			StageSummary:   "Resume the next bounded step.",
			ApprovedBy:     approverID,
		}
	} else {
		s.continuationState.ApprovedBy = approverID
		s.continuationState.Status = session.ContinuationStatusApproved
	}
	return s.continuationState, nil
}

func (s *stubCommandRouter) StopContinuation(chatID int64) (core.StopResult, error) {
	s.stopContinuationInput = chatID
	return s.stopContinuationResult, nil
}

func (s *stubCommandRouter) TriggerContinuation(ctx context.Context, chatID int64) error {
	s.triggerContinuationInput = chatID
	_ = ctx
	return nil
}

func (s *stubCommandRouter) QueueReinstall(ctx context.Context, msg core.InboundMessage) error {
	copied := msg
	s.queuedReinstallMsg = &copied
	_ = ctx
	return nil
}

func (s *stubCommandRouter) Restart(chatID int64) error {
	s.restartInput = chatID
	s.restartCalls++
	return nil
}

func (s stubCommandRouter) CanRestart(senderID int64) bool {
	_ = senderID
	return s.canRestart
}

func (s stubCommandRouter) CurrentPersonaModel() string {
	return s.personaModel
}

func (s stubCommandRouter) PersonaModelOptions() []string {
	if len(s.personaModelOptions) == 0 {
		return []string{"claude-sonnet-4-6", "claude-opus-4-6", "claude-opus-4-7"}
	}
	return append([]string(nil), s.personaModelOptions...)
}

func (s *stubCommandRouter) SetPersonaModel(model string) (string, error) {
	s.setPersonaModelInput = model
	if s.setPersonaModelErr != nil {
		return "", s.setPersonaModelErr
	}
	if s.setPersonaModelReturn != "" {
		return s.setPersonaModelReturn, nil
	}
	return model, nil
}

func (s stubCommandRouter) GovernorEffortOptions() []string {
	if len(s.governorEffortOptions) == 0 {
		return []string{"low", "medium", "high", "xhigh"}
	}
	return append([]string(nil), s.governorEffortOptions...)
}

func (s *stubCommandRouter) SetGovernorEffort(effort string) (string, error) {
	s.setGovernorEffortInput = effort
	if s.setGovernorEffortErr != nil {
		return "", s.setGovernorEffortErr
	}
	if s.setGovernorEffortReturn != "" {
		return s.setGovernorEffortReturn, nil
	}
	return effort, nil
}

func (s *stubCommandRouter) RunDurableWizard(ctx context.Context, chatID int64, senderID int64, action string, agentID string, wizardAnswers map[string]any) (string, error) {
	_ = ctx
	s.durableWizardChatID = chatID
	s.durableWizardSenderID = senderID
	s.durableWizardAction = action
	s.durableWizardAgentID = agentID
	if wizardAnswers != nil {
		copied := make(map[string]any, len(wizardAnswers))
		for key, value := range wizardAnswers {
			copied[key] = value
		}
		s.durableWizardAnswers = copied
	} else {
		s.durableWizardAnswers = nil
	}
	if s.durableWizardErr != nil {
		return "", s.durableWizardErr
	}
	if strings.TrimSpace(s.durableWizardResult) != "" {
		return s.durableWizardResult, nil
	}
	return "action: durable-agent wizard show\nagent_id: idolum-email\nchannel_kind: email\nwizard_status: in_progress\ncurrent_step: adapter\nmissing: adapter,autonomy\nnext_question: Which inbox adapter should be used (for example gog_cli)?\naddress: idolum@example.com\nadapter: \nautonomy: \nwakeup_mode: poll\npoll_interval: 5m\nsynthesis_cadence: 4h\ncharter:\n", nil
}

func (s *stubCommandRouter) DurableAgentsList(senderID int64) ([]core.DurableAgentStatusSnapshot, error) {
	s.durableAgentsListSenderID = senderID
	if s.durableAgentsListErr != nil {
		return nil, s.durableAgentsListErr
	}
	return append([]core.DurableAgentStatusSnapshot(nil), s.durableAgentsList...), nil
}

func (s *stubCommandRouter) StartDurableAgentConversation(ctx context.Context, chatID int64, senderID int64, agentID string) (string, error) {
	_ = ctx
	s.startDurableChatID = chatID
	s.startDurableSenderID = senderID
	s.startDurableAgentID = agentID
	if s.startDurableErr != nil {
		return "", s.startDurableErr
	}
	if strings.TrimSpace(s.startDurableResult) != "" {
		return s.startDurableResult, nil
	}
	return "Started background conversation with durable agent " + strings.TrimSpace(agentID) + ".", nil
}

func (s *stubCommandRouter) MemoryReviewSnapshot(ctx context.Context, chatID int64, senderID int64, source memoryReviewSource) (memoryReviewSnapshot, error) {
	_ = ctx
	s.memoryReviewChatID = chatID
	s.memoryReviewSenderID = senderID
	s.memoryReviewSource = source
	if s.memoryReviewErr != nil {
		return memoryReviewSnapshot{}, s.memoryReviewErr
	}
	if s.memoryReviewBySource == nil {
		return memoryReviewSnapshot{
			Source: source,
			Query:  "default seed",
		}, nil
	}
	if snapshot, ok := s.memoryReviewBySource[source]; ok {
		return snapshot, nil
	}
	return memoryReviewSnapshot{
		Source: source,
		Query:  "default seed",
	}, nil
}

func (s *stubCommandRouter) MemoryFocus(chatID int64) (core.MemoryFocus, bool) {
	if s.memoryFocusByChat == nil {
		return core.MemoryFocus{}, false
	}
	focus, ok := s.memoryFocusByChat[chatID]
	return focus, ok
}

func (s *stubCommandRouter) SetMemoryFocus(chatID int64, focus core.MemoryFocus) {
	if s.memoryFocusByChat == nil {
		s.memoryFocusByChat = make(map[int64]core.MemoryFocus)
	}
	s.memoryFocusByChat[chatID] = focus
}

func (s *stubCommandRouter) ClearMemoryFocus(chatID int64) bool {
	s.clearMemoryFocusChatID = chatID
	if s.memoryFocusByChat != nil {
		if _, ok := s.memoryFocusByChat[chatID]; ok {
			delete(s.memoryFocusByChat, chatID)
			return true
		}
	}
	return s.clearMemoryFocusResult
}

func TestParseTelegramCommand(t *testing.T) {
	t.Parallel()

	tests := []struct {
		text string
		want string
		ok   bool
	}{
		{text: "/stop", want: "stop", ok: true},
		{text: "/new", want: "new", ok: true},
		{text: "/detach", want: "detach", ok: true},
		{text: "/help extra", want: "help", ok: true},
		{text: "/status@my_bot", want: "status", ok: true},
		{text: "/restart", want: "restart", ok: true},
		{text: "/reinstall", want: "reinstall", ok: true},
		{text: "/debug", want: "debug", ok: true},
		{text: "/agents", want: "agents", ok: true},
		{text: "/memory", want: "memory", ok: true},
		{text: "/set_persona_model", want: "set_persona_model", ok: true},
		{text: "/set_governor_effort", want: "set_governor_effort", ok: true},
		{text: "/tmp/file", ok: false},
		{text: " /start ", want: "start", ok: true},
		{text: "hello", ok: false},
	}

	for _, tt := range tests {
		got, ok := parseTelegramCommand(tt.text)
		if ok != tt.ok || got != tt.want {
			t.Fatalf("parseTelegramCommand(%q) = (%q, %v), want (%q, %v)", tt.text, got, ok, tt.want, tt.ok)
		}
	}
}

func TestDefaultTelegramCommandsIncludeMemory(t *testing.T) {
	t.Parallel()

	found := false
	for _, cmd := range defaultTelegramCommands {
		if cmd.Command == "memory" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("defaultTelegramCommands = %#v, want /memory command entry", defaultTelegramCommands)
	}
}

func TestDefaultTelegramCommandsIncludeAgents(t *testing.T) {
	t.Parallel()

	found := false
	for _, cmd := range defaultTelegramCommands {
		if cmd.Command == "agents" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("defaultTelegramCommands = %#v, want /agents command entry", defaultTelegramCommands)
	}
}

func TestDefaultTelegramCommandsIncludeDebug(t *testing.T) {
	t.Parallel()

	found := false
	for _, cmd := range defaultTelegramCommands {
		if cmd.Command == "debug" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("defaultTelegramCommands = %#v, want /debug command entry", defaultTelegramCommands)
	}
}

func TestDefaultTelegramCommandsIncludeNew(t *testing.T) {
	t.Parallel()

	found := false
	for _, cmd := range defaultTelegramCommands {
		if cmd.Command == "new" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("defaultTelegramCommands = %#v, want /new command entry", defaultTelegramCommands)
	}
}

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
	if got := sender.inline[0].text; !strings.Contains(got, "Status Scope: chat") {
		t.Fatalf("status text = %q, want chat scope status", got)
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
				ChannelKind: "daily_review",
				Status:      "active",
				Health:      "ok",
			},
			{
				AgentID:     "ops-child",
				ChannelKind: "telegram_dm",
				Status:      "active",
				Health:      "dormant",
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
	foundStart := false
	foundRefresh := false
	for _, row := range sender.inline[0].rows {
		for _, button := range row {
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
	if got := sender.inline[0].text; !strings.Contains(got, "Status Scope: chat") {
		t.Fatalf("status text = %q, want machine status body", got)
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
	if got := sender.inline[0].text; !strings.Contains(got, "summary state=blocked") {
		t.Fatalf("status text = %q, want blocked summary state", got)
	}
	if got := sender.inline[0].text; !strings.Contains(got, "Current Signal: operation:blocked:approval_wait") {
		t.Fatalf("status text = %q, want blocked operation signal", got)
	}
}

func TestHandleTelegramCommandDebugForNonAdminShowsChatDebugOnly(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := stubCommandRouter{
		statusChat: core.ChatStatusSnapshot{
			ChatID: 7,
			LatestTurnRun: &core.TurnRunStatusSnapshot{
				ID:                    91,
				Status:                "running",
				Kind:                  "interactive",
				RequestText:           "debug this run",
				LastToolName:          "exec",
				LastToolPreview:       `{"command":"curl -fsS https://api.github.com/zen"}`,
				LastToolResultPreview: "stdout: Keep it logically awesome.",
			},
		},
		statusReadableSummary: "Chat 7 is working and currently running exec.",
		personaEffort:         "sonnet",
		governorEffort:        "medium",
		canRestart:            false,
	}
	handled, err := handleTelegramCommand(context.Background(), sender, &router, core.InboundMessage{
		ChatID:   7,
		SenderID: 1002,
		Text:     "/debug",
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
	if got := sender.inline[0].text; !strings.Contains(got, "Quick Read: Chat 7 is working and currently running exec.") {
		t.Fatalf("debug text = %q, want quick summary", got)
	}
	if got := sender.inline[0].text; strings.Contains(got, "status_scope=chat") {
		t.Fatalf("debug text = %q, do not want full chat section before read more", got)
	}
	if got := sender.inline[0].text; strings.Contains(got, "status_scope=system") {
		t.Fatalf("debug text = %q, do not want admin system section for non-admin", got)
	}
	if len(sender.inline[0].rows) != 1 || len(sender.inline[0].rows[0]) != 1 {
		t.Fatalf("rows = %#v, want one Read More button", sender.inline[0].rows)
	}
	if got := sender.inline[0].rows[0][0].Text; got != "Read More" {
		t.Fatalf("button text = %q, want Read More", got)
	}
	if got := sender.inline[0].rows[0][0].CallbackData; got != "debug:more" {
		t.Fatalf("callback = %q, want debug:more", got)
	}
}

func TestHandleTelegramCommandDebugForAdminIncludesSystemAndDurables(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := stubCommandRouter{
		statusChat: core.ChatStatusSnapshot{
			ChatID: 7,
		},
		statusSystem: core.SystemStatusSnapshot{
			ActiveTurnCount: 1,
		},
		statusDurables: core.DurableAgentsStatusSnapshot{
			TotalAgents: 1,
			Agents: []core.DurableAgentStatusSnapshot{
				{
					AgentID:     "family-group",
					ChannelKind: "telegram_group",
					Status:      "active",
					Health:      "ok",
				},
			},
		},
		personaEffort:         "opus",
		governorEffort:        "high",
		canRestart:            true,
		statusReadableSummary: "Admin debug snapshot ready.",
	}
	handled, err := handleTelegramCommand(context.Background(), sender, &router, core.InboundMessage{
		ChatID:   7,
		SenderID: 1001,
		Text:     "/debug",
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
	if got := sender.inline[0].text; !strings.Contains(got, "Quick Read: Admin debug snapshot ready.") {
		t.Fatalf("debug text = %q, want quick summary in collapsed view", got)
	}
	if got := sender.inline[0].text; strings.Contains(got, "status_scope=chat") {
		t.Fatalf("debug text = %q, do not want full snapshot in collapsed view", got)
	}
	if len(sender.inline[0].rows) != 1 || len(sender.inline[0].rows[0]) != 1 {
		t.Fatalf("rows = %#v, want one Read More button", sender.inline[0].rows)
	}
	if got := sender.inline[0].rows[0][0].CallbackData; got != "debug:more" {
		t.Fatalf("callback = %q, want debug:more", got)
	}
}

func TestHandleTelegramCommandCallbackDebugReadMoreExpandsFullSnapshot(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := stubCommandRouter{
		statusChat: core.ChatStatusSnapshot{
			ChatID: 7,
			LatestTurnRun: &core.TurnRunStatusSnapshot{
				ID:                    91,
				Status:                "running",
				Kind:                  "interactive",
				RequestText:           "debug this run",
				LastToolName:          "exec",
				LastToolPreview:       `{"command":"curl -fsS https://api.github.com/zen"}`,
				LastToolResultPreview: "stdout: Keep it logically awesome.",
			},
		},
		statusReadableSummary: "Chat 7 is working and currently running exec.",
		personaEffort:         "sonnet",
		governorEffort:        "medium",
		canRestart:            false,
	}
	handled, err := handleTelegramCommandCallback(context.Background(), sender, &router, telegram.CallbackQuery{
		ID:   "cb-debug-more",
		From: &telegram.User{ID: 1002, Username: "approved"},
		Data: "debug:more",
		Message: &telegram.Message{
			MessageID: 201,
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
	if len(sender.edits) != 1 {
		t.Fatalf("edits count = %d, want 1", len(sender.edits))
	}
	full := sender.edits[0].text
	for _, msg := range sender.msgs {
		full += "\n" + msg.Text
	}
	if !strings.Contains(full, "Status Scope: chat") {
		t.Fatalf("full debug text = %q, want chat section", full)
	}
	if !strings.Contains(full, "Debug Chat:") {
		t.Fatalf("full debug text = %q, want debug_chat section", full)
	}
	if !strings.Contains(full, "Last Exec Command: \"curl -fsS https://api.github.com/zen\"") {
		t.Fatalf("full debug text = %q, want decoded last exec command", full)
	}
	if strings.Contains(full, "status_scope=system") {
		t.Fatalf("full debug text = %q, do not want admin sections for non-admin", full)
	}
}

func TestHandleTelegramCommandHelpHidesAdminRestartForNonAdmin(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := stubCommandRouter{
		personaEffort:  "sonnet",
		governorEffort: "medium",
		canRestart:     false,
	}
	handled, err := handleTelegramCommand(context.Background(), sender, &router, core.InboundMessage{
		ChatID:   7,
		SenderID: 1002,
		Text:     "/help",
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
	if strings.Contains(sender.msgs[0].Text, "\n/restart - ") {
		t.Fatalf("help text = %q, want admin-only /restart hidden for non-admins", sender.msgs[0].Text)
	}
}

func TestHandleTelegramCommandHelpShowsAdminRestartForAdmin(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := stubCommandRouter{
		personaEffort:  "sonnet",
		governorEffort: "medium",
		canRestart:     true,
	}
	handled, err := handleTelegramCommand(context.Background(), sender, &router, core.InboundMessage{
		ChatID:   7,
		SenderID: 1001,
		Text:     "/help",
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
	if !strings.Contains(sender.msgs[0].Text, "\n/restart - ") {
		t.Fatalf("help text = %q, want admin /restart command listed", sender.msgs[0].Text)
	}
	if !strings.Contains(sender.msgs[0].Text, "\n/debug - ") {
		t.Fatalf("help text = %q, want /debug command listed", sender.msgs[0].Text)
	}
}

func TestHandleTelegramCommandStartHidesAdminRestartForNonAdmin(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := stubCommandRouter{
		personaEffort:  "sonnet",
		governorEffort: "medium",
		canRestart:     false,
	}
	handled, err := handleTelegramCommand(context.Background(), sender, &router, core.InboundMessage{
		ChatID:   7,
		SenderID: 1002,
		Text:     "/start",
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
	if strings.Contains(sender.msgs[0].Text, "\n/restart - ") {
		t.Fatalf("start text = %q, want admin-only /restart hidden for non-admins", sender.msgs[0].Text)
	}
	if !strings.Contains(sender.msgs[0].Text, "\n/debug - ") {
		t.Fatalf("start text = %q, want /debug command listed", sender.msgs[0].Text)
	}
}

func TestHandleTelegramCommandSetPersonaModel(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := stubCommandRouter{
		personaModel:        "claude-sonnet-4-6",
		personaModelOptions: []string{"claude-sonnet-4-6", "claude-opus-4-6", "claude-opus-4-7"},
	}
	handled, err := handleTelegramCommand(context.Background(), sender, &router, core.InboundMessage{
		ChatID:    7,
		MessageID: 19,
		Text:      "/set_persona_model",
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
	if sender.inline[0].replyTo == nil || *sender.inline[0].replyTo != 19 {
		t.Fatalf("reply_to = %#v, want 19", sender.inline[0].replyTo)
	}
	if len(sender.inline[0].rows) == 0 || len(sender.inline[0].rows[0]) == 0 {
		t.Fatalf("rows = %#v, want non-empty", sender.inline[0].rows)
	}
	if sender.inline[0].rows[0][0].CallbackData == "" {
		t.Fatalf("callback data empty in rows %#v", sender.inline[0].rows)
	}
}

func TestHandleTelegramCommandSetGovernorEffort(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := stubCommandRouter{
		governorEffortOptions: []string{"medium", "high"},
	}
	handled, err := handleTelegramCommand(context.Background(), sender, &router, core.InboundMessage{
		ChatID:    7,
		MessageID: 20,
		Text:      "/set_governor_effort",
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
	if len(sender.inline[0].rows) == 0 {
		t.Fatalf("rows = %#v, want non-empty", sender.inline[0].rows)
	}
}

func TestHandleTelegramCommandCallbackStatusSystemForAdmin(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := stubCommandRouter{
		canRestart: true,
		statusSystem: core.SystemStatusSnapshot{
			ActiveChatIDs: []int64{7, 8},
			HotChats: []core.ChatStatusRollup{
				{ChatID: 7, PendingCount: 2},
				{ChatID: 8, PendingCount: 1},
			},
		},
	}
	handled, err := handleTelegramCommandCallback(context.Background(), sender, &router, telegram.CallbackQuery{
		ID:   "cb-status-system",
		From: &telegram.User{ID: 1001, Username: "admin"},
		Data: "status:system",
		Message: &telegram.Message{
			MessageID: 96,
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
	if len(sender.editInline) != 1 {
		t.Fatalf("editInline count = %d, want 1", len(sender.editInline))
	}
	if got := sender.editInline[0].text; !strings.Contains(got, "Status Scope: system") {
		t.Fatalf("system status text = %q, want system scope", got)
	}
}

func TestHandleTelegramCommandCallbackStatusDurablesForAdmin(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := stubCommandRouter{
		canRestart: true,
		statusDurables: core.DurableAgentsStatusSnapshot{
			TotalAgents: 1,
			Agents: []core.DurableAgentStatusSnapshot{
				{
					AgentID:            "family-group",
					ChannelKind:        "telegram_group",
					Status:             "active",
					Health:             "ok",
					PolicyVersion:      2,
					PolicyHash:         "abc123",
					PolicyDrift:        "admin_review",
					PolicyOutboundMode: "read_only",
				},
			},
		},
	}
	handled, err := handleTelegramCommandCallback(context.Background(), sender, &router, telegram.CallbackQuery{
		ID:   "cb-status-durables",
		From: &telegram.User{ID: 1001, Username: "admin"},
		Data: "status:durables",
		Message: &telegram.Message{
			MessageID: 196,
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
	if len(sender.editInline) != 1 {
		t.Fatalf("editInline count = %d, want 1", len(sender.editInline))
	}
	if got := sender.editInline[0].text; !strings.Contains(got, "Status Scope: durables") {
		t.Fatalf("durables status text = %q, want durables scope", got)
	}
}

func TestHandleTelegramCommandCallbackStatusSystemDeniedForNonAdmin(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := stubCommandRouter{canRestart: false}
	handled, err := handleTelegramCommandCallback(context.Background(), sender, &router, telegram.CallbackQuery{
		ID:   "cb-status-system-denied",
		From: &telegram.User{ID: 1002, Username: "approved"},
		Data: "status:system",
		Message: &telegram.Message{
			MessageID: 97,
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
	if !strings.Contains(strings.ToLower(sender.answers[0].text), "admin") {
		t.Fatalf("answer text = %q, want admin-only denial", sender.answers[0].text)
	}
	if len(sender.editInline) != 0 {
		t.Fatalf("editInline count = %d, want 0 for denied callback", len(sender.editInline))
	}
}

func TestHandleTelegramCommandCallbackStatusFindChatShowsChatButtons(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := stubCommandRouter{
		canRestart: true,
		statusSystem: core.SystemStatusSnapshot{
			HotChats: []core.ChatStatusRollup{
				{ChatID: 9001, PendingCount: 3},
				{ChatID: 9002, PendingCount: 1},
			},
		},
	}
	handled, err := handleTelegramCommandCallback(context.Background(), sender, &router, telegram.CallbackQuery{
		ID:   "cb-status-find",
		From: &telegram.User{ID: 1001, Username: "admin"},
		Data: "status:find",
		Message: &telegram.Message{
			MessageID: 98,
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
	foundChatButton := false
	for _, row := range sender.editInline[0].rows {
		for _, button := range row {
			if strings.Contains(button.CallbackData, "status:chat:9001") {
				foundChatButton = true
			}
		}
	}
	if !foundChatButton {
		t.Fatalf("rows = %#v, want chat drill-down callback", sender.editInline[0].rows)
	}
}

func TestHandleTelegramCommandCallbackStatusChunksOverflowDeterministically(t *testing.T) {
	t.Parallel()

	pending := make([]core.PendingItem, 0, 120)
	for i := 0; i < 120; i++ {
		pending = append(pending, core.PendingItem{
			Kind:    core.PendingItemKindDecision,
			ChatID:  int64(7000 + i%3),
			ID:      "decision-overflow-" + strings.Repeat("x", 20),
			Summary: strings.Repeat("very long pending summary ", 4),
		})
	}

	sender := &stubCommandSender{}
	router := stubCommandRouter{
		canRestart: true,
		statusSystem: core.SystemStatusSnapshot{
			PendingItems: pending,
		},
	}
	handled, err := handleTelegramCommandCallback(context.Background(), sender, &router, telegram.CallbackQuery{
		ID:   "cb-status-overflow",
		From: &telegram.User{ID: 1001, Username: "admin"},
		Data: "status:system",
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
	if len(sender.editInline) != 1 {
		t.Fatalf("editInline count = %d, want 1", len(sender.editInline))
	}
	if len([]rune(sender.editInline[0].text)) > 3800 {
		t.Fatalf("edited text rune length = %d, want <= 3800", len([]rune(sender.editInline[0].text)))
	}
	if len(sender.msgs) == 0 {
		t.Fatalf("follow-up messages = %#v, want overflow chunks", sender.msgs)
	}
}

func TestHandleTelegramCommandCallbackDeliberationStop(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := stubCommandRouter{
		stop: core.StopResult{ActiveCanceled: true, QueuedDropped: true},
		statusChat: core.ChatStatusSnapshot{
			ChatID: 7,
			LatestTurnRun: &core.TurnRunStatusSnapshot{
				ID:     501,
				ChatID: 7,
				Status: string(session.TurnRunStatusRunning),
			},
		},
	}
	handled, err := handleTelegramCommandCallback(context.Background(), sender, &router, telegram.CallbackQuery{
		ID:   "cb-delib-stop",
		From: &telegram.User{ID: 1002, Username: "approved"},
		Data: core.EncodeDeliberationControlCallbackData(501, core.DeliberationControlActionStop),
		Message: &telegram.Message{
			MessageID: 240,
			Chat:      &telegram.Chat{ID: 7, Type: "private"},
		},
	})
	if err != nil {
		t.Fatalf("handleTelegramCommandCallback() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if router.stopCalls != 1 || router.stopInput != 7 {
		t.Fatalf("stop calls/input = (%d,%d), want (1,7)", router.stopCalls, router.stopInput)
	}
	if len(sender.answers) != 1 {
		t.Fatalf("answers count = %d, want 1", len(sender.answers))
	}
	if len(sender.edits) != 1 {
		t.Fatalf("edits count = %d, want 1", len(sender.edits))
	}
	if got := sender.edits[0].text; !strings.Contains(got, "Stopped the current turn and cleared queued work for this chat.") {
		t.Fatalf("edited text = %q, want stop summary", got)
	}
}

func TestHandleTelegramCommandCallbackDeliberationDetach(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := stubCommandRouter{
		detach: core.DetachResult{
			ActiveCanceled:           true,
			QueuedDropped:            true,
			ContinuationRevoked:      true,
			PendingDecisionsDetached: 1,
		},
		statusChat: core.ChatStatusSnapshot{
			ChatID: 7,
			LatestTurnRun: &core.TurnRunStatusSnapshot{
				ID:     777,
				ChatID: 7,
				Status: string(session.TurnRunStatusRunning),
			},
		},
	}
	handled, err := handleTelegramCommandCallback(context.Background(), sender, &router, telegram.CallbackQuery{
		ID:   "cb-delib-detach",
		From: &telegram.User{ID: 1002, Username: "approved"},
		Data: core.EncodeDeliberationControlCallbackData(777, core.DeliberationControlActionDetach),
		Message: &telegram.Message{
			MessageID: 241,
			Chat:      &telegram.Chat{ID: 7, Type: "private"},
		},
	})
	if err != nil {
		t.Fatalf("handleTelegramCommandCallback() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if router.detachChatID != 7 || router.detachSenderID != 1002 {
		t.Fatalf("detach inputs = (%d,%d), want (7,1002)", router.detachChatID, router.detachSenderID)
	}
	if len(sender.answers) != 1 {
		t.Fatalf("answers count = %d, want 1", len(sender.answers))
	}
	if len(sender.edits) != 1 {
		t.Fatalf("edits count = %d, want 1", len(sender.edits))
	}
	if got := sender.edits[0].text; !strings.Contains(got, "Detached this chat from pending work") {
		t.Fatalf("edited text = %q, want detach summary", got)
	}
}

func TestHandleTelegramCommandCallbackDeliberationRejectsStaleRun(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := stubCommandRouter{
		statusChat: core.ChatStatusSnapshot{
			ChatID: 7,
			LatestTurnRun: &core.TurnRunStatusSnapshot{
				ID:     700,
				ChatID: 7,
				Status: string(session.TurnRunStatusCompleted),
			},
		},
	}
	handled, err := handleTelegramCommandCallback(context.Background(), sender, &router, telegram.CallbackQuery{
		ID:   "cb-delib-stale",
		From: &telegram.User{ID: 1002, Username: "approved"},
		Data: core.EncodeDeliberationControlCallbackData(701, core.DeliberationControlActionStop),
		Message: &telegram.Message{
			MessageID: 242,
			Chat:      &telegram.Chat{ID: 7, Type: "private"},
		},
	})
	if err != nil {
		t.Fatalf("handleTelegramCommandCallback() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if router.stopCalls != 0 {
		t.Fatalf("stopCalls = %d, want 0 for stale callback", router.stopCalls)
	}
	if router.detachChatID != 0 {
		t.Fatalf("detachChatID = %d, want 0 for stale callback", router.detachChatID)
	}
	if len(sender.answers) != 1 {
		t.Fatalf("answers count = %d, want 1", len(sender.answers))
	}
	if sender.answers[0].text != staleDeliberationCallbackText {
		t.Fatalf("answer text = %q, want stale callback warning", sender.answers[0].text)
	}
	if len(sender.edits) != 0 {
		t.Fatalf("edits count = %d, want 0 for stale callback", len(sender.edits))
	}
}

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
	if len(sender.edits) != 1 {
		t.Fatalf("edits count = %d, want 1", len(sender.edits))
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
	if len(sender.edits) != 1 {
		t.Fatalf("edits count = %d, want 1", len(sender.edits))
	}
}

func TestHandleTelegramCommandCallbackContinuationApprove(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := stubCommandRouter{continuationState: session.ContinuationState{
		Status:         session.ContinuationStatusPending,
		DecisionID:     "decision-1",
		RemainingTurns: 1,
		StageSummary:   "Resume the next bounded step.",
	}}
	handled, err := handleTelegramCommandCallback(context.Background(), sender, &router, telegram.CallbackQuery{
		ID:      "cb-continue",
		From:    &telegram.User{ID: 1002, Username: "approved"},
		Data:    encodeContinuationCallbackData("decision-1", "approve"),
		Message: &telegram.Message{MessageID: 93, Chat: &telegram.Chat{ID: 7, Type: "private"}},
	})
	if err != nil {
		t.Fatalf("handleTelegramCommandCallback() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if router.approveContinuationInput != 7 {
		t.Fatalf("approveContinuationInput = %d, want 7", router.approveContinuationInput)
	}
	if router.approveContinuationApprover != 1002 {
		t.Fatalf("approveContinuationApprover = %d, want 1002", router.approveContinuationApprover)
	}
	if router.triggerContinuationInput != 7 {
		t.Fatalf("triggerContinuationInput = %d, want 7", router.triggerContinuationInput)
	}
	if router.continuationStateInput != 7 {
		t.Fatalf("continuationStateInput = %d, want 7", router.continuationStateInput)
	}
	if len(sender.edits) != 1 {
		t.Fatalf("edits count = %d, want 1", len(sender.edits))
	}
}

func TestHandleTelegramCommandCallbackContinuationApproveContinuesWhenEditFails(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{editErr: errors.New("telegram editMessageText failed: message is not modified")}
	router := stubCommandRouter{continuationState: session.ContinuationState{
		Status:         session.ContinuationStatusPending,
		DecisionID:     "decision-2",
		RemainingTurns: 1,
		StageSummary:   "Resume the next bounded step.",
	}}
	handled, err := handleTelegramCommandCallback(context.Background(), sender, &router, telegram.CallbackQuery{
		ID:      "cb-continue-edit-fail",
		From:    &telegram.User{ID: 1002, Username: "approved"},
		Data:    encodeContinuationCallbackData("decision-2", "approve"),
		Message: &telegram.Message{MessageID: 193, Chat: &telegram.Chat{ID: 7, Type: "private"}},
	})
	if err != nil {
		t.Fatalf("handleTelegramCommandCallback() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if router.triggerContinuationInput != 7 {
		t.Fatalf("triggerContinuationInput = %d, want 7", router.triggerContinuationInput)
	}
	if len(sender.edits) != 1 {
		t.Fatalf("edits count = %d, want 1", len(sender.edits))
	}
}

func TestPersonaModelButtonLabelIncludesOpus47(t *testing.T) {
	t.Parallel()
	if got := personaModelButtonLabel("claude-opus-4-7"); got != "Opus 4.7" {
		t.Fatalf("personaModelButtonLabel() = %q, want Opus 4.7", got)
	}
}

func TestHandleTelegramCommandCallbackContinuationStopRendersCombinedStopResult(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := stubCommandRouter{
		continuationState:      session.ContinuationState{Status: session.ContinuationStatusPending, DecisionID: "decision-3", RemainingTurns: 1},
		stopContinuationResult: core.StopResult{ContinuationRevoked: true},
	}
	handled, err := handleTelegramCommandCallback(context.Background(), sender, &router, telegram.CallbackQuery{
		ID:      "cb-stop",
		Data:    encodeContinuationCallbackData("decision-3", "stop"),
		Message: &telegram.Message{MessageID: 94, Chat: &telegram.Chat{ID: 7, Type: "private"}},
	})
	if err != nil {
		t.Fatalf("handleTelegramCommandCallback() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if router.stopContinuationInput != 7 {
		t.Fatalf("stopContinuationInput = %d, want 7", router.stopContinuationInput)
	}
	if len(sender.edits) != 1 {
		t.Fatalf("edits count = %d, want 1", len(sender.edits))
	}
	if sender.edits[0].text != "Revoked continuation approval for this chat." {
		t.Fatalf("edit text = %q, want continuation revoke text", sender.edits[0].text)
	}
}

func TestHandleTelegramCommandCallbackContinuationStopRendersNoOpStopResult(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := stubCommandRouter{
		continuationState:      session.ContinuationState{Status: session.ContinuationStatusPending, DecisionID: "decision-4", RemainingTurns: 1},
		stopContinuationResult: core.StopResult{},
	}
	handled, err := handleTelegramCommandCallback(context.Background(), sender, &router, telegram.CallbackQuery{
		ID:      "cb-stop-none",
		Data:    encodeContinuationCallbackData("decision-4", "stop"),
		Message: &telegram.Message{MessageID: 95, Chat: &telegram.Chat{ID: 7, Type: "private"}},
	})
	if err != nil {
		t.Fatalf("handleTelegramCommandCallback() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if len(sender.edits) != 1 {
		t.Fatalf("edits count = %d, want 1", len(sender.edits))
	}
	if sender.edits[0].text != "Continuation approval was already inactive for this chat." {
		t.Fatalf("edit text = %q, want inactive continuation text", sender.edits[0].text)
	}
}

func TestHandleTelegramCommandCallbackContinuationRejectsStaleDecisionID(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := stubCommandRouter{
		continuationState: session.ContinuationState{
			Status:         session.ContinuationStatusPending,
			DecisionID:     "decision-current",
			RemainingTurns: 1,
		},
	}
	handled, err := handleTelegramCommandCallback(context.Background(), sender, &router, telegram.CallbackQuery{
		ID:      "cb-stale",
		From:    &telegram.User{ID: 1002, Username: "approved"},
		Data:    encodeContinuationCallbackData("decision-old", "approve"),
		Message: &telegram.Message{MessageID: 196, Chat: &telegram.Chat{ID: 7, Type: "private"}},
	})
	if err != nil {
		t.Fatalf("handleTelegramCommandCallback() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if router.approveContinuationInput != 0 {
		t.Fatalf("approveContinuationInput = %d, want 0 for stale callback", router.approveContinuationInput)
	}
	if router.triggerContinuationInput != 0 {
		t.Fatalf("triggerContinuationInput = %d, want 0 for stale callback", router.triggerContinuationInput)
	}
	if len(sender.answers) != 1 {
		t.Fatalf("answers count = %d, want 1", len(sender.answers))
	}
	if sender.answers[0].text != staleContinuationCallbackText {
		t.Fatalf("answer text = %q, want stale callback warning", sender.answers[0].text)
	}
	if len(sender.edits) != 0 {
		t.Fatalf("edits count = %d, want 0 for stale callback", len(sender.edits))
	}
}

func TestDurableWizardInlineRowsFromTextInProgress(t *testing.T) {
	t.Parallel()

	text := "action: durable-agent wizard show\nagent_id: idolum-email\nchannel_kind: email\nwizard_status: in_progress\ncurrent_step: autonomy\nmissing: autonomy,surface_rules\nnext_question: Should the child be observe_only, local_drafts, review_before_reply, or reply_within_charter?\naddress: idolum@example.com\nadapter: gog_cli\nautonomy: \nwakeup_mode: poll\npoll_interval: 5m\nsynthesis_cadence: 4h\ncharter:\n"
	rows := durableWizardInlineRowsFromText(text)
	if len(rows) < 2 {
		t.Fatalf("rows len = %d, want at least option row and controls", len(rows))
	}
	foundObserveOnly := false
	for _, row := range rows {
		for _, button := range row {
			if strings.EqualFold(button.Text, "Observe only") {
				foundObserveOnly = true
			}
		}
	}
	if !foundObserveOnly {
		t.Fatalf("rows = %#v, want Observe only button", rows)
	}
	last := rows[len(rows)-1]
	if len(last) != 2 || last[0].Text != "Cancel" || last[1].Text != "Refresh" {
		t.Fatalf("last row = %#v, want [Cancel|Refresh] controls", last)
	}
}

func TestDurableWizardInlineRowsFromTextReady(t *testing.T) {
	t.Parallel()

	text := "action: durable-agent wizard show\nagent_id: idolum-email\nchannel_kind: email\nwizard_status: ready\ncurrent_step: -\nmissing: -\naddress: idolum@example.com\nadapter: gog_cli\nautonomy: observe_only\nwakeup_mode: poll\npoll_interval: 5m\nsynthesis_cadence: 4h\ncharter: Read-only child.\n"
	rows := durableWizardInlineRowsFromText(text)
	if len(rows) != 1 {
		t.Fatalf("rows len = %d, want 1 finalize control row", len(rows))
	}
	if len(rows[0]) != 2 || rows[0][0].Text != "Cancel" || rows[0][1].Text != "Finalize" {
		t.Fatalf("row = %#v, want [Cancel|Finalize]", rows[0])
	}
}

func TestHandleTelegramCommandCallbackDurableWizardAnswer(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := stubCommandRouter{
		canRestart:          true,
		durableWizardResult: "action: durable-agent wizard show\nagent_id: idolum-email\nchannel_kind: email\nwizard_status: ready\ncurrent_step: -\nmissing: -\naddress: idolum@example.com\nadapter: gog_cli\nautonomy: observe_only\nwakeup_mode: poll\npoll_interval: 5m\nsynthesis_cadence: 4h\ncharter: Read-only child.\n",
	}
	handled, err := handleTelegramCommandCallback(context.Background(), sender, &router, telegram.CallbackQuery{
		ID:   "cb-durable-answer",
		From: &telegram.User{ID: 1001, Username: "admin"},
		Data: encodeDurableWizardAnswerCallbackData("autonomy", "observe_only"),
		Message: &telegram.Message{
			MessageID: 210,
			Chat:      &telegram.Chat{ID: 7, Type: "private"},
			Text:      "action: durable-agent wizard show\nagent_id: idolum-email\nchannel_kind: email\nwizard_status: in_progress\ncurrent_step: autonomy\nmissing: autonomy,surface_rules\naddress: idolum@example.com\nadapter: gog_cli\nautonomy: \nwakeup_mode: poll\npoll_interval: 5m\nsynthesis_cadence: 4h\ncharter:\n",
		},
	})
	if err != nil {
		t.Fatalf("handleTelegramCommandCallback() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if router.durableWizardChatID != 7 || router.durableWizardSenderID != 1001 {
		t.Fatalf("wizard callback routing = (%d,%d), want (7,1001)", router.durableWizardChatID, router.durableWizardSenderID)
	}
	if router.durableWizardAction != "wizard_answer" {
		t.Fatalf("durableWizardAction = %q, want wizard_answer", router.durableWizardAction)
	}
	if router.durableWizardAgentID != "idolum-email" {
		t.Fatalf("durableWizardAgentID = %q, want idolum-email", router.durableWizardAgentID)
	}
	if got := router.durableWizardAnswers["autonomy"]; got != "observe_only" {
		t.Fatalf("durableWizardAnswers[autonomy] = %#v, want observe_only", got)
	}
	if len(sender.answers) != 1 {
		t.Fatalf("answers count = %d, want 1", len(sender.answers))
	}
	if len(sender.editInline) != 1 {
		t.Fatalf("editInline count = %d, want 1", len(sender.editInline))
	}
	if len(sender.editInline[0].rows) != 1 || len(sender.editInline[0].rows[0]) != 2 {
		t.Fatalf("rows = %#v, want finalize controls", sender.editInline[0].rows)
	}
	if sender.editInline[0].rows[0][0].Text != "Cancel" || sender.editInline[0].rows[0][1].Text != "Finalize" {
		t.Fatalf("row = %#v, want [Cancel|Finalize]", sender.editInline[0].rows[0])
	}
}

func TestHandleTelegramCommandCallbackDurableWizardRejectsStaleStep(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := stubCommandRouter{canRestart: true}
	handled, err := handleTelegramCommandCallback(context.Background(), sender, &router, telegram.CallbackQuery{
		ID:   "cb-durable-stale",
		From: &telegram.User{ID: 1001, Username: "admin"},
		Data: encodeDurableWizardAnswerCallbackData("autonomy", "observe_only"),
		Message: &telegram.Message{
			MessageID: 211,
			Chat:      &telegram.Chat{ID: 7, Type: "private"},
			Text:      "action: durable-agent wizard show\nagent_id: idolum-email\nchannel_kind: email\nwizard_status: in_progress\ncurrent_step: adapter\nmissing: adapter,autonomy\naddress: idolum@example.com\nadapter: \nautonomy: \n",
		},
	})
	if err != nil {
		t.Fatalf("handleTelegramCommandCallback() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if router.durableWizardAction != "" {
		t.Fatalf("durableWizardAction = %q, want no wizard execution for stale callback", router.durableWizardAction)
	}
	if len(sender.answers) != 1 {
		t.Fatalf("answers count = %d, want 1", len(sender.answers))
	}
	if sender.answers[0].text != staleDurableWizardCallbackText {
		t.Fatalf("answer text = %q, want stale wizard callback warning", sender.answers[0].text)
	}
	if len(sender.editInline) != 0 {
		t.Fatalf("editInline count = %d, want 0 for stale callback", len(sender.editInline))
	}
}

func TestHandleTelegramCommandReinstallQueuesRequest(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := &stubCommandRouter{}
	msg := core.InboundMessage{ChatID: 7, SenderID: 1001, SenderName: "admin", MessageID: 11, Text: "/reinstall"}
	handled, err := handleTelegramCommand(context.Background(), sender, router, msg)
	if err != nil {
		t.Fatalf("handleTelegramCommand() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if router.queuedReinstallMsg == nil {
		t.Fatal("queuedReinstallMsg = nil, want queued message")
	}
	if router.queuedReinstallMsg.ChatID != msg.ChatID || router.queuedReinstallMsg.SenderID != msg.SenderID {
		t.Fatalf("queued reinstall msg = %#v, want original routing identity", router.queuedReinstallMsg)
	}
	if router.queuedReinstallMsg.Text != msg.Text {
		t.Fatalf("queued reinstall text = %q, want original command text at command-router boundary", router.queuedReinstallMsg.Text)
	}
	if len(sender.msgs) != 1 || sender.msgs[0].Text != "Queued a reinstall request as a normal turn in this chat." {
		t.Fatalf("sender msgs = %#v, want queued reinstall ack", sender.msgs)
	}
}

func TestHandleTelegramCommandRestartForcesRestart(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := &stubCommandRouter{canRestart: true}
	msg := core.InboundMessage{ChatID: 7, SenderID: 1001, SenderName: "admin", MessageID: 12, Text: "/restart"}
	handled, err := handleTelegramCommand(context.Background(), sender, router, msg)
	if err != nil {
		t.Fatalf("handleTelegramCommand() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if router.restartCalls != 1 || router.restartInput != msg.ChatID {
		t.Fatalf("restart calls/input = (%d,%d), want (1,%d)", router.restartCalls, router.restartInput, msg.ChatID)
	}
	if len(sender.msgs) != 1 {
		t.Fatalf("sender msgs = %#v, want one restart ack", sender.msgs)
	}
	if sender.msgs[0].Text != "Restarting the gateway now. Active and queued work will be dropped." {
		t.Fatalf("restart ack text = %q, want restart confirmation", sender.msgs[0].Text)
	}
}

func TestHandleTelegramCommandRestartDeniedForNonAdmin(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := &stubCommandRouter{canRestart: false}
	msg := core.InboundMessage{ChatID: 7, SenderID: 2002, SenderName: "approved", MessageID: 13, Text: "/restart"}
	handled, err := handleTelegramCommand(context.Background(), sender, router, msg)
	if err != nil {
		t.Fatalf("handleTelegramCommand() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if router.restartCalls != 0 {
		t.Fatalf("restart calls = %d, want 0 for denied restart", router.restartCalls)
	}
	if len(sender.msgs) != 1 {
		t.Fatalf("sender msgs = %#v, want one deny ack", sender.msgs)
	}
	if sender.msgs[0].Text != "Restart denied. Only Telegram admins can run /restart." {
		t.Fatalf("deny ack text = %q, want denied confirmation", sender.msgs[0].Text)
	}
}
