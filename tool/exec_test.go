//go:build linux

package tool

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestExecSuccess(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	registry := NewRegistry(workspace, 2*time.Second)

	out, err := registry.Execute(context.Background(), "exec", json.RawMessage(`{"command":"printf 'ok'"}`))
	if err != nil {
		t.Fatalf("Execute() err = %v", err)
	}
	if !strings.Contains(out, "ok") {
		t.Fatalf("output = %q, want command output", out)
	}
}

func TestExecRejectsEscapedWorkdir(t *testing.T) {
	t.Parallel()

	registry := NewRegistry(t.TempDir(), 2*time.Second)
	_, err := registry.Execute(context.Background(), "exec", json.RawMessage(`{"command":"pwd","workdir":"../outside"}`))
	if err == nil {
		t.Fatal("Execute() err = nil, want workspace violation")
	}
}

func TestExecTimeout(t *testing.T) {
	t.Parallel()

	registry := NewRegistry(t.TempDir(), 1*time.Second)
	_, err := registry.Execute(context.Background(), "exec", json.RawMessage(`{"command":"sleep 2"}`))
	if err == nil {
		t.Fatal("Execute() err = nil, want timeout")
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("err = %v, want timeout message", err)
	}
}
