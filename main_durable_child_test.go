//go:build linux

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/config"
	runtimepkg "github.com/idolum-ai/aphelion/runtime"
)

func TestParseDurableChildWakeTime(t *testing.T) {
	t.Parallel()

	now, err := parseDurableChildWakeTime("")
	if err != nil {
		t.Fatalf("parseDurableChildWakeTime(empty) err = %v", err)
	}
	if now.IsZero() {
		t.Fatal("parseDurableChildWakeTime(empty) returned zero time")
	}

	fixed := "2026-04-20T12:34:56.123456789Z"
	parsed, err := parseDurableChildWakeTime(fixed)
	if err != nil {
		t.Fatalf("parseDurableChildWakeTime(RFC3339Nano) err = %v", err)
	}
	if got := parsed.UTC().Format(time.RFC3339Nano); got != fixed {
		t.Fatalf("parsed = %q, want %q", got, fixed)
	}

	if _, err := parseDurableChildWakeTime("definitely-not-a-time"); err == nil {
		t.Fatal("parseDurableChildWakeTime(invalid) err = nil, want parse error")
	}
}

func TestRunDurableAgentChildCommandRequiresMessageOrAgent(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	bootstrapPath := filepath.Join(root, "child-bootstrap.json")
	cfg := config.Default()
	cfg.Sessions.DBPath = filepath.Join(root, "sessions.db")
	raw, err := json.Marshal(runtimepkg.DurableAgentChildBootstrap{Config: cfg})
	if err != nil {
		t.Fatalf("json.Marshal(bootstrap) err = %v", err)
	}
	if err := os.WriteFile(bootstrapPath, raw, 0o600); err != nil {
		t.Fatalf("WriteFile(bootstrap) err = %v", err)
	}

	err = runDurableAgentChildCommand([]string{"--bootstrap", bootstrapPath})
	if err == nil {
		t.Fatal("runDurableAgentChildCommand() err = nil, want missing mode error")
	}
	if !strings.Contains(err.Error(), "requires --message or --agent") {
		t.Fatalf("runDurableAgentChildCommand() err = %v, want missing mode detail", err)
	}
}
