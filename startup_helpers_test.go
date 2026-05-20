package main

import (
	"errors"
	"fmt"
	"testing"

	"github.com/idolum-ai/aphelion/internal/childcli"
)

func TestExitCodeChildTelegramConfigStartupError(t *testing.T) {
	err := &childcli.ConfigStartupError{Path: "bad.toml", Err: errors.New("bad config")}
	if code := exitCode(fmt.Errorf("telegram child bot startup: %w", err)); code != exitCodeConfig {
		t.Fatalf("exitCode(childcli config startup error) = %d, want %d", code, exitCodeConfig)
	}
}
