//go:build linux

package standalonecli

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRunVersionCommandText(t *testing.T) {
	t.Parallel()

	out, err := captureStandaloneStdout(t, func() error {
		return runVersionCommand(nil)
	})
	if err != nil {
		t.Fatalf("runVersionCommand() err = %v", err)
	}
	for _, want := range []string{
		"Aphelion Version",
		"Status:",
		"Why:",
		"Next:",
		"Details:",
		"Evidence:",
		"VCS revision:",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("runVersionCommand() output missing %q in %q", want, out)
		}
	}
}

func TestRunVersionCommandKV(t *testing.T) {
	t.Parallel()

	out, err := captureStandaloneStdout(t, func() error {
		return runVersionCommand([]string{"--format=kv"})
	})
	if err != nil {
		t.Fatalf("runVersionCommand(--format=kv) err = %v", err)
	}
	for _, want := range []string{
		"name: aphelion",
		"module:",
		"version:",
		"go_version:",
		"vcs_revision:",
		"vcs_time:",
		"vcs_modified:",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("runVersionCommand(--format=kv) output missing %q in %q", want, out)
		}
	}
}

func TestRunVersionCommandJSON(t *testing.T) {
	t.Parallel()

	out, err := captureStandaloneStdout(t, func() error {
		return runVersionCommand([]string{"--json"})
	})
	if err != nil {
		t.Fatalf("runVersionCommand(--json) err = %v", err)
	}

	var got versionInfo
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("json.Unmarshal(version --json) err = %v; output=%q", err, out)
	}
	if got.Name != "aphelion" {
		t.Fatalf("name = %q, want aphelion", got.Name)
	}
}

func TestRunVersionCommandRejectsUnknownArg(t *testing.T) {
	t.Parallel()

	if err := runVersionCommand([]string{"unexpected"}); err == nil {
		t.Fatal("runVersionCommand(unexpected) err = nil, want unknown argument error")
	} else if !strings.Contains(err.Error(), "unknown argument") {
		t.Fatalf("runVersionCommand(unexpected) err = %v, want unknown argument detail", err)
	}
}

func captureStandaloneStdout(t *testing.T, fn func() error) (string, error) {
	t.Helper()
	return captureStdoutForTest(fn)
}
