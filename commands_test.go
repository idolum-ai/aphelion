//go:build linux

package main

import (
	"context"
	"testing"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/telegram"
)

type stubCommandSender struct {
	msgs      []core.OutboundMessage
	inline    []stubInlineCall
	edits     []stubEditCall
	answers   []stubAnswerCall
	answerErr error
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
	return nil
}

func (s *stubCommandSender) AnswerCallbackQuery(_ context.Context, id string, text string) error {
	s.answers = append(s.answers, stubAnswerCall{
		id:   id,
		text: text,
	})
	return s.answerErr
}

type stubCommandRouter struct {
	status                   core.SessionStatus
	stop                     core.StopResult
	personaEffort            string
	governorEffort           string
	toggledPersona           string
	toggledGovernor          string
	personaModel             string
	personaModelOptions      []string
	governorEffortOptions    []string
	setPersonaModelInput     string
	setGovernorEffortInput   string
	setPersonaModelReturn    string
	setGovernorEffortReturn  string
	setPersonaModelErr       error
	setGovernorEffortErr     error
	continuationState        session.ContinuationState
	approveContinuationInput int64
	revokeContinuationInput  int64
	triggerContinuationInput int64
}

func (s stubCommandRouter) Stop(chatID int64) core.StopResult {
	return s.stop
}

func (s stubCommandRouter) Status(chatID int64) core.SessionStatus {
	return s.status
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

func (s *stubCommandRouter) ApproveContinuation(chatID int64) (session.ContinuationState, error) {
	s.approveContinuationInput = chatID
	if s.continuationState.Status == "" {
		s.continuationState = session.ContinuationState{Status: session.ContinuationStatusApproved, RemainingTurns: 1, StageSummary: "Resume the next bounded step."}
	}
	return s.continuationState, nil
}

func (s *stubCommandRouter) RevokeContinuation(chatID int64) (session.ContinuationState, error) {
	s.revokeContinuationInput = chatID
	s.continuationState = session.ContinuationState{Status: session.ContinuationStatusRevoked}
	return s.continuationState, nil
}

func (s *stubCommandRouter) TriggerContinuation(ctx context.Context, chatID int64) error {
	s.triggerContinuationInput = chatID
	_ = ctx
	return nil
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
		{text: "/help extra", want: "help", ok: true},
		{text: "/status@my_bot", want: "status", ok: true},
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

func TestHandleTelegramCommandStatus(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := stubCommandRouter{
		status:         core.SessionStatus{Active: true, Queued: true},
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
	if len(sender.msgs) != 1 {
		t.Fatalf("message count = %d, want 1", len(sender.msgs))
	}
	if got := sender.msgs[0].Text; got == "" || got == "Current state: idle." {
		t.Fatalf("status text = %q, want active status", got)
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
	router := stubCommandRouter{continuationState: session.ContinuationState{Status: session.ContinuationStatusApproved, RemainingTurns: 1, StageSummary: "Resume the next bounded step."}}
	handled, err := handleTelegramCommandCallback(context.Background(), sender, &router, telegram.CallbackQuery{
		ID:      "cb-continue",
		Data:    encodeContinuationCallbackData("approve"),
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
