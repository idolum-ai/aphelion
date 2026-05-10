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
