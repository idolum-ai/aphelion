//go:build linux

package main

import (
	"context"
	"testing"

	"github.com/idolum-ai/aphelion/core"
)

type stubCommandSender struct {
	msgs []core.OutboundMessage
}

func (s *stubCommandSender) SendMessage(_ context.Context, msg core.OutboundMessage) (int64, error) {
	s.msgs = append(s.msgs, msg)
	return int64(len(s.msgs)), nil
}

type stubCommandRouter struct {
	status          core.SessionStatus
	stop            core.StopResult
	personaEffort   string
	governorEffort  string
	toggledPersona  string
	toggledGovernor string
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
		stop: core.StopResult{ActiveCanceled: true, QueuedDropped: true},
	}
	handled, err := handleTelegramCommand(context.Background(), sender, router, core.InboundMessage{
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
	handled, err := handleTelegramCommand(context.Background(), sender, router, core.InboundMessage{
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
	handled, err := handleTelegramCommand(context.Background(), sender, router, core.InboundMessage{
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
	handled, err := handleTelegramCommand(context.Background(), sender, router, core.InboundMessage{
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
