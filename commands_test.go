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
	statusChatErr               error
	statusSystemErr             error
	stop                        core.StopResult
	detach                      core.DetachResult
	detachErr                   error
	detachChatID                int64
	detachSenderID              int64
	personaEffort               string
	governorEffort              string
	canRestart                  bool
	toggledPersona              string
	toggledGovernor             string
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
}

func (s stubCommandRouter) Stop(chatID int64) core.StopResult {
	return s.stop
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

func (s stubCommandRouter) TogglePersonaEffort() (string, error) {
	return s.toggledPersona, nil
}

func (s stubCommandRouter) ToggleGovernorEffort() (string, error) {
	return s.toggledGovernor, nil
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

func TestParseTelegramCommand(t *testing.T) {
	t.Parallel()

	tests := []struct {
		text string
		want string
		ok   bool
	}{
		{text: "/stop", want: "stop", ok: true},
		{text: "/detach", want: "detach", ok: true},
		{text: "/help extra", want: "help", ok: true},
		{text: "/status@my_bot", want: "status", ok: true},
		{text: "/restart", want: "restart", ok: true},
		{text: "/reinstall", want: "reinstall", ok: true},
		{text: "/toggle_persona_effort", want: "toggle_persona_effort", ok: true},
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
	if got := sender.inline[0].text; !strings.Contains(got, "status_scope=chat") {
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
	for _, row := range sender.inline[0].rows {
		for _, button := range row {
			switch button.Text {
			case "System Overview":
				foundSystem = true
			case "Hot Chats":
				foundHot = true
			case "Find Chat":
				foundFind = true
			}
		}
	}
	if !foundSystem || !foundHot || !foundFind {
		t.Fatalf("admin status keyboard rows = %#v, want admin controls", sender.inline[0].rows)
	}
}

func TestHandleTelegramCommandTogglePersonaEffort(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := stubCommandRouter{toggledPersona: "opus"}
	handled, err := handleTelegramCommand(context.Background(), sender, &router, core.InboundMessage{
		ChatID: 7,
		Text:   "/toggle_persona_effort",
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
	if got := sender.msgs[0].Text; got == "" || got == "Idolum persona effort is now sonnet." {
		t.Fatalf("toggle text = %q, want persona toggle status", got)
	}
}

func TestHandleTelegramCommandToggleGovernorEffort(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := stubCommandRouter{toggledGovernor: "high"}
	handled, err := handleTelegramCommand(context.Background(), sender, &router, core.InboundMessage{
		ChatID: 7,
		Text:   "/toggle_governor_effort",
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
	if got := sender.msgs[0].Text; got == "" || got == "Governor effort is now medium." {
		t.Fatalf("toggle text = %q, want governor toggle status", got)
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
	if got := sender.editInline[0].text; !strings.Contains(got, "status_scope=system") {
		t.Fatalf("system status text = %q, want system scope", got)
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
