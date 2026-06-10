//go:build linux

package main

import (
	"errors"
	"os"
	"strings"
	"testing"
)

func TestFirstPositionalArgFindsFirstNonEmpty(t *testing.T) {
	t.Parallel()

	got, ok := firstPositionalArg([]string{"", "   ", "m", "other"})
	if !ok {
		t.Fatal("ok = false, want true")
	}
	if got != "m" {
		t.Fatalf("firstPositionalArg() = %q, want m", got)
	}
}

func TestFirstPositionalArgReturnsFalseWhenEmpty(t *testing.T) {
	t.Parallel()

	got, ok := firstPositionalArg([]string{"", " \t "})
	if ok {
		t.Fatalf("ok = true with value %q, want false", got)
	}
	if got != "" {
		t.Fatalf("value = %q, want empty", got)
	}
}

func TestTopLevelHelpRequestReturnsWithoutError(t *testing.T) {
	oldArgs := os.Args
	t.Cleanup(func() { os.Args = oldArgs })
	os.Args = []string{"aphelion", "--help"}

	if err := run(); err != nil {
		t.Fatalf("run(--help) err = %v, want nil", err)
	}
}

func TestUnknownCommandReturnsGroupedUsageErrorWithSuggestion(t *testing.T) {
	oldArgs := os.Args
	t.Cleanup(func() { os.Args = oldArgs })
	os.Args = []string{"aphelion", "verfy-deploy"}

	err := run()
	var usageErr *cliUsageError
	if !errors.As(err, &usageErr) {
		t.Fatalf("run(unknown) err = %T %v, want cliUsageError", err, err)
	}
	text := usageErr.Error()
	for _, want := range []string{"Unknown command: verfy-deploy", "Did you mean: verify-deploy?", "Commands:", "Setup and deploy:", "Examples:"} {
		if !strings.Contains(text, want) {
			t.Fatalf("usage error = %q, want %q", text, want)
		}
	}
}

func TestTopLevelHelpListsStandaloneStatus(t *testing.T) {
	text := renderTopLevelHelp("")
	for _, want := range []string{"status", "aphelion status --config ~/.aphelion/aphelion.toml", "aphelion status --config ~/.aphelion/aphelion.toml --format=json"} {
		if !strings.Contains(text, want) {
			t.Fatalf("help text missing %q in %q", want, text)
		}
	}
}

func TestUnknownCommandSuggestsStandaloneStatus(t *testing.T) {
	text := renderUnknownCommandHelp("stauts")
	if !strings.Contains(text, "Did you mean: status?") {
		t.Fatalf("unknown command help = %q, want status suggestion", text)
	}
}

func TestTopLevelVersionAliasReturnsVersionOutput(t *testing.T) {
	oldArgs := os.Args
	t.Cleanup(func() { os.Args = oldArgs })
	os.Args = []string{"aphelion", "--version"}

	out, err := captureStdout(t, run)
	if err != nil {
		t.Fatalf("run(--version) err = %v, want nil", err)
	}
	for _, want := range []string{"Aphelion Version", "VCS revision:", "Use --json"} {
		if !strings.Contains(out, want) {
			t.Fatalf("run(--version) output = %q, want %q", out, want)
		}
	}
}

func TestTopLevelShortVersionAliasPassesJSONFlag(t *testing.T) {
	oldArgs := os.Args
	t.Cleanup(func() { os.Args = oldArgs })
	os.Args = []string{"aphelion", "-v", "--json"}

	out, err := captureStdout(t, run)
	if err != nil {
		t.Fatalf("run(-v --json) err = %v, want nil", err)
	}
	for _, want := range []string{"\"name\": \"aphelion\"", "\"go_version\":"} {
		if !strings.Contains(out, want) {
			t.Fatalf("run(-v --json) output = %q, want %q", out, want)
		}
	}
}

func TestUnknownTopLevelFlagReturnsGroupedUsageError(t *testing.T) {
	oldArgs := os.Args
	t.Cleanup(func() { os.Args = oldArgs })
	os.Args = []string{"aphelion", "--bogus"}

	err := run()
	var usageErr *cliUsageError
	if !errors.As(err, &usageErr) {
		t.Fatalf("run(--bogus) err = %T %v, want cliUsageError", err, err)
	}
	text := usageErr.Error()
	for _, want := range []string{"Unknown flag: --bogus", "Try: aphelion --help", "Commands:", "Examples:"} {
		if !strings.Contains(text, want) {
			t.Fatalf("usage error = %q, want %q", text, want)
		}
	}
}

func TestDaemonConfigFlagIsNotTreatedAsUnknownTopLevelFlag(t *testing.T) {
	if flagName, ok := unknownTopLevelFlag([]string{"--config=/tmp/aphelion.toml"}); ok {
		t.Fatalf("unknownTopLevelFlag(--config=...) = %q true, want false", flagName)
	}
	if flagName, ok := unknownTopLevelFlag([]string{"--check-config=false"}); ok {
		t.Fatalf("unknownTopLevelFlag(--check-config=false) = %q true, want false", flagName)
	}
}
