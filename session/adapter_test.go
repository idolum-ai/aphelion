//go:build linux

package session

import (
	"encoding/json"
	"testing"

	"github.com/idolum-ai/aphelion/agent"
)

func TestToAgentHistorySkipsCompactedAndParsesToolCalls(t *testing.T) {
	t.Parallel()

	history, err := ToAgentHistory([]Message{
		{ID: 1, Role: "assistant", Content: "old", Compacted: true},
		{ID: 2, Role: "assistant", Content: "ready", ToolCalls: `[{"id":"t1","name":"exec","input":{"command":"pwd"}}]`},
		{ID: 3, Role: "tool", Content: "stdout", ToolID: "t1"},
	})
	if err != nil {
		t.Fatalf("ToAgentHistory() err = %v", err)
	}

	if len(history) != 2 {
		t.Fatalf("history len = %d, want 2", len(history))
	}
	if len(history[0].ToolCalls) != 1 || history[0].ToolCalls[0].ID != "t1" {
		t.Fatalf("tool calls = %#v", history[0].ToolCalls)
	}
	if history[1].Role != "tool" || history[1].ToolCallID != "t1" {
		t.Fatalf("tool message = %#v", history[1])
	}
}

func TestNewMessagesForTurn(t *testing.T) {
	t.Parallel()

	generated := []agent.Message{
		{
			Role:    "assistant",
			Content: "running",
			ToolCalls: []agent.ToolCall{{
				ID:    "call-1",
				Name:  "exec",
				Input: json.RawMessage(`{"command":"ls"}`),
			}},
		},
		{
			Role:       "tool",
			Content:    "stdout:\nfile.txt",
			ToolCallID: "call-1",
		},
		{
			Role:    "assistant",
			Content: "done",
		},
	}

	rows, err := NewMessagesForTurn("hi", generated, 3)
	if err != nil {
		t.Fatalf("NewMessagesForTurn() err = %v", err)
	}

	if len(rows) != 4 {
		t.Fatalf("rows len = %d, want 4", len(rows))
	}
	if rows[0].Role != "user" || rows[0].TurnIndex != 3 {
		t.Fatalf("user row = %#v", rows[0])
	}
	if rows[1].ToolCalls == "" {
		t.Fatalf("assistant tool calls missing: %#v", rows[1])
	}
	if rows[2].Role != "tool" || rows[2].ToolName != "exec" || rows[2].ToolID != "call-1" {
		t.Fatalf("tool row = %#v", rows[2])
	}
}
