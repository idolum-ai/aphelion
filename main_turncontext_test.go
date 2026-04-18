//go:build linux

package main

import (
	"context"
	"testing"
	"time"
)

func TestNewTurnContextWithoutTimeoutHasNoDeadlineAndIsCancelable(t *testing.T) {
	t.Parallel()

	ctx, cancel := newTurnContext(context.Background(), 0)
	defer cancel()
	if _, ok := ctx.Deadline(); ok {
		t.Fatal("Deadline() ok = true, want false when timeout is disabled")
	}

	cancel()
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("context was not canceled")
	}
}

func TestNewTurnContextWithTimeoutHasDeadline(t *testing.T) {
	t.Parallel()

	ctx, cancel := newTurnContext(context.Background(), time.Second)
	defer cancel()
	if _, ok := ctx.Deadline(); !ok {
		t.Fatal("Deadline() ok = false, want true when timeout is set")
	}
}

func TestQueueReinstallUsesTemplatedInboundMessage(t *testing.T) {
	t.Parallel()
	if reinstallTemplateMessage == "" {
		t.Fatal("reinstallTemplateMessage empty")
	}
	if reinstallTemplateMessage == "/reinstall" {
		t.Fatal("reinstallTemplateMessage collapsed to command text")
	}
}
