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
	if rows[1].Content != "" || rows[1].ContentChars != 0 {
		t.Fatalf("assistant tool-call scratch content = (%q, %d), want hidden", rows[1].Content, rows[1].ContentChars)
	}
	if rows[2].Role != "tool" || rows[2].ToolName != "exec" || rows[2].ToolID != "call-1" {
		t.Fatalf("tool row = %#v", rows[2])
	}
	if rows[3].Role != "assistant" || rows[3].Content != "done" {
		t.Fatalf("final assistant row = %#v, want visible final response", rows[3])
	}
}

func TestToAgentHistoryBackfillsPendingToolIdentity(t *testing.T) {
	t.Parallel()

	history, err := ToAgentHistory([]Message{
		{ID: 1, Role: "assistant", Content: "running", ToolCalls: `[{"id":"t1","name":"exec","input":{"command":"pwd"}}]`},
		{ID: 2, Role: "tool", Content: "stdout:\n/home/test"},
	})
	if err != nil {
		t.Fatalf("ToAgentHistory() err = %v", err)
	}

	if len(history) != 2 {
		t.Fatalf("history len = %d, want 2", len(history))
	}
	if history[1].Role != "tool" {
		t.Fatalf("history[1] = %#v, want tool message", history[1])
	}
	if history[1].ToolCallID != "t1" || history[1].ToolName != "exec" {
		t.Fatalf("history[1] = %#v, want repaired tool identity", history[1])
	}
}

func TestToAgentHistorySynthesizesMissingToolResultsAndDropsOrphans(t *testing.T) {
	t.Parallel()

	history, err := ToAgentHistory([]Message{
		{ID: 1, Role: "assistant", Content: "running", ToolCalls: `[{"id":"t1","name":"exec","input":{"command":"pwd"}}]`},
		{ID: 2, Role: "assistant", Content: "done"},
		{ID: 3, Role: "tool", Content: "orphan output", ToolID: "orphan", ToolName: "exec"},
	})
	if err != nil {
		t.Fatalf("ToAgentHistory() err = %v", err)
	}

	if len(history) != 3 {
		t.Fatalf("history len = %d, want 3", len(history))
	}
	if history[1].Role != "tool" || history[1].ToolCallID != "t1" || history[1].ToolName != "exec" {
		t.Fatalf("history[1] = %#v, want synthesized tool result", history[1])
	}
	if history[1].Content != "tool_error: missing tool result in persisted transcript" {
		t.Fatalf("history[1].Content = %q, want synthesized tool error", history[1].Content)
	}
	if history[2].Role != "assistant" || history[2].Content != "done" {
		t.Fatalf("history[2] = %#v, want trailing assistant", history[2])
	}
}

func TestNewMessagesForTurnWithContextCarriesTurnProvenance(t *testing.T) {
	t.Parallel()

	rows, err := NewMessagesForTurnWithContext(continuationApprovedEventTextForTest, nil, 4, TurnMessageContext{
		ActorUserID:       1002,
		ActorRole:         "approved_user",
		EventOrigin:       "turn_authorization",
		EventOriginDetail: "continuation",
	})
	if err != nil {
		t.Fatalf("NewMessagesForTurnWithContext() err = %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("row count = %d, want 1", len(rows))
	}
	if rows[0].ActorUserID != 1002 || rows[0].ActorRole != "approved_user" {
		t.Fatalf("actor provenance = %#v, want approved user", rows[0])
	}
	if rows[0].EventOrigin != "turn_authorization" || rows[0].EventOriginDetail != "continuation" {
		t.Fatalf("event provenance = (%q, %q), want turn_authorization/continuation", rows[0].EventOrigin, rows[0].EventOriginDetail)
	}
}
