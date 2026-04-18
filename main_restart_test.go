//go:build linux

package main

import (
	"context"
	"errors"
	"testing"
	"time"
)

type stubRestartDetacher struct {
	detachAllCalls int
	detachAllErr   error
}

func (s *stubRestartDetacher) DetachByOwner(ctx context.Context, ownerKey string) (int, error) {
	_ = ctx
	_ = ownerKey
	return 0, nil
}

func (s *stubRestartDetacher) DetachAll(ctx context.Context) (int, error) {
	_ = ctx
	s.detachAllCalls++
	if s.detachAllErr != nil {
		return 0, s.detachAllErr
	}
	return 3, nil
}

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

func TestTelegramCommandControlRestartDetachesPendingWhenConfigured(t *testing.T) {
	originalExit := processExit
	defer func() {
		processExit = originalExit
	}()

	exited := make(chan int, 1)
	processExit = func(code int) {
		exited <- code
	}

	detacher := &stubRestartDetacher{}
	control := telegramCommandControl{
		decisionDetacher:       detacher,
		detachPendingOnRestart: true,
	}
	if err := control.Restart(7); err != nil {
		t.Fatalf("Restart() err = %v", err)
	}

	if detacher.detachAllCalls != 1 {
		t.Fatalf("detach all calls = %d, want 1", detacher.detachAllCalls)
	}
	select {
	case <-exited:
	case <-time.After(time.Second):
		t.Fatal("Restart() did not schedule process exit")
	}
}

func TestTelegramCommandControlRestartSkipsDetachWhenDisabled(t *testing.T) {
	originalExit := processExit
	defer func() {
		processExit = originalExit
	}()

	exited := make(chan int, 1)
	processExit = func(code int) {
		exited <- code
	}

	detacher := &stubRestartDetacher{}
	control := telegramCommandControl{
		decisionDetacher:       detacher,
		detachPendingOnRestart: false,
	}
	if err := control.Restart(7); err != nil {
		t.Fatalf("Restart() err = %v", err)
	}
	if detacher.detachAllCalls != 0 {
		t.Fatalf("detach all calls = %d, want 0", detacher.detachAllCalls)
	}
	select {
	case <-exited:
	case <-time.After(time.Second):
		t.Fatal("Restart() did not schedule process exit")
	}
}

func TestTelegramCommandControlRestartContinuesWhenDetachFails(t *testing.T) {
	originalExit := processExit
	defer func() {
		processExit = originalExit
	}()

	exited := make(chan int, 1)
	processExit = func(code int) {
		exited <- code
	}

	detacher := &stubRestartDetacher{detachAllErr: errors.New("db unavailable")}
	control := telegramCommandControl{
		decisionDetacher:       detacher,
		detachPendingOnRestart: true,
	}
	if err := control.Restart(7); err != nil {
		t.Fatalf("Restart() err = %v, want nil even when detach fails", err)
	}
	if detacher.detachAllCalls != 1 {
		t.Fatalf("detach all calls = %d, want 1", detacher.detachAllCalls)
	}
	select {
	case <-exited:
	case <-time.After(time.Second):
		t.Fatal("Restart() did not schedule process exit")
	}
}
