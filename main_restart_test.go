//go:build linux

package main

import (
	"testing"
	"time"
)

func TestTelegramCommandControlRestartSchedulesProcessExit(t *testing.T) {
	originalExit := processExit
	defer func() {
		processExit = originalExit
	}()

	exited := make(chan int, 1)
	processExit = func(code int) {
		exited <- code
	}

	control := telegramCommandControl{}
	if err := control.Restart(7); err != nil {
		t.Fatalf("Restart() err = %v", err)
	}

	select {
	case code := <-exited:
		if code != exitCodeFailure {
			t.Fatalf("exit code = %d, want %d", code, exitCodeFailure)
		}
	case <-time.After(time.Second):
		t.Fatal("Restart() did not schedule process exit")
	}
}
