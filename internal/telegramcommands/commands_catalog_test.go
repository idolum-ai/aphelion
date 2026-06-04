//go:build linux

package telegramcommands

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
		{text: "/health", want: "health", ok: true},
		{text: "/tailnet", want: "tailnet", ok: true},
		{text: "/agents", want: "agents", ok: true},
		{text: "/context", want: "context", ok: true},
		{text: "/memory", want: "memory", ok: true},
		{text: "/mission", want: "mission", ok: true},
		{text: "/model status", want: "model", ok: true},
		{text: "/turn_evidence", want: "turn_evidence", ok: true},
		{text: "/turn_evidence@idolum_bot", want: "turn_evidence", ok: true},
		{text: "/auto mode", ok: false},
		{text: "/stop\n\nReply context:\nidolum: Please confirm.", want: "stop", ok: true},
		{text: "/set_persona_model", ok: false},
		{text: "/set_governor_effort", ok: false},
		{text: "/unknown", ok: false},
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

func TestDefaultTelegramCommandsIncludeContext(t *testing.T) {
	t.Parallel()

	found := false
	for _, cmd := range defaultTelegramCommands {
		if cmd.Command == "context" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("defaultTelegramCommands = %#v, want /context command entry", defaultTelegramCommands)
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

func TestDefaultTelegramCommandsIncludeHealth(t *testing.T) {
	t.Parallel()

	found := false
	for _, cmd := range defaultTelegramCommands {
		if cmd.Command == "health" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("defaultTelegramCommands = %#v, want /health command entry", defaultTelegramCommands)
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

func TestDefaultTelegramCommandsIncludeTurnEvidence(t *testing.T) {
	t.Parallel()

	found := false
	for _, cmd := range defaultTelegramCommands {
		if cmd.Command == "turn_evidence" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("defaultTelegramCommands = %#v, want /turn_evidence command entry", defaultTelegramCommands)
	}
}

func TestDefaultTelegramCommandsExcludeAuto(t *testing.T) {
	t.Parallel()

	for _, cmd := range defaultTelegramCommands {
		if cmd.Command == "auto" {
			t.Fatalf("defaultTelegramCommands = %#v, want /auto removed", defaultTelegramCommands)
		}
	}
}
