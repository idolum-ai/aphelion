//go:build linux

package main

import (
	"os"
	"strings"
	"testing"
)

func TestMainStartsTelegramThreadReminderSweepLoop(t *testing.T) {
	data, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatalf("read main.go: %v", err)
	}
	if !strings.Contains(string(data), "startTelegramThreadReminderSweepLoop(ctx, tgOutbound, commandControl, store)") {
		t.Fatal("main.go must start the background Telegram thread reminder sweep loop")
	}
}
