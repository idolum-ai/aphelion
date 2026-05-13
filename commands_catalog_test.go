//go:build linux

package main

import (
	"strings"
	"testing"
)

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
		{text: "/doctor", want: "doctor", ok: true},
		{text: "/tailnet", want: "tailnet", ok: true},
		{text: "/agents", want: "agents", ok: true},
		{text: "/memory", want: "memory", ok: true},
		{text: "/mission", want: "mission", ok: true},
		{text: "/model status", want: "model", ok: true},
		{text: "/set_persona_model", want: "set_persona_model", ok: true},
		{text: "/set_governor_effort", want: "set_governor_effort", ok: true},
		{text: "/stop\n\nReply context:\nidolum: Please confirm.", want: "stop", ok: true},
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

func TestDefaultTelegramCommandsAvoidBrandedDescriptions(t *testing.T) {
	t.Parallel()

	for _, cmd := range defaultTelegramCommands {
		lower := strings.ToLower(cmd.Description)
		for _, forbidden := range []string{"aphelion", "idolum"} {
			if strings.Contains(lower, forbidden) {
				t.Fatalf("command %q description = %q, want no branded runtime name", cmd.Command, cmd.Description)
			}
		}
	}
}

func TestDefaultTelegramCommandsIncludeMission(t *testing.T) {
	t.Parallel()

	found := false
	for _, cmd := range defaultTelegramCommands {
		if cmd.Command == "mission" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("defaultTelegramCommands = %#v, want /mission command entry", defaultTelegramCommands)
	}
}

func TestDefaultTelegramCommandsIncludeModel(t *testing.T) {
	t.Parallel()

	found := false
	for _, cmd := range defaultTelegramCommands {
		if cmd.Command == "model" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("defaultTelegramCommands = %#v, want /model command entry", defaultTelegramCommands)
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

func TestDefaultTelegramCommandsIncludeDoctor(t *testing.T) {
	t.Parallel()

	found := false
	for _, cmd := range defaultTelegramCommands {
		if cmd.Command == "doctor" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("defaultTelegramCommands = %#v, want /doctor command entry", defaultTelegramCommands)
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

func TestDefaultTelegramCommandsIncludeTailnet(t *testing.T) {
	t.Parallel()

	found := false
	for _, cmd := range defaultTelegramCommands {
		if cmd.Command == "tailnet" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("defaultTelegramCommands = %#v, want /tailnet command entry", defaultTelegramCommands)
	}
}

func TestDefaultTelegramCommandsIncludeAutoApprove(t *testing.T) {
	t.Parallel()

	found := false
	for _, cmd := range defaultTelegramCommands {
		if cmd.Command == "autoapprove" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("defaultTelegramCommands = %#v, want /autoapprove command entry", defaultTelegramCommands)
	}
}

func TestDefaultTelegramCommandsIncludeAutonomy(t *testing.T) {
	t.Parallel()

	found := false
	for _, cmd := range defaultTelegramCommands {
		if cmd.Command == "autonomy" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("defaultTelegramCommands = %#v, want /autonomy command entry", defaultTelegramCommands)
	}
}
