//go:build linux

package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestRunTurnRepairsToolNameAndArgumentsBeforeExecution(t *testing.T) {
	t.Parallel()

	provider := &mockProvider{
		complete: func(_ context.Context, call int, messages []Message, tools []ToolDef) (*Response, error) {
			switch call {
			case 1:
				if len(tools) == 0 {
					t.Fatal("tools unexpectedly empty")
				}
				return &Response{
					ToolCalls: []ToolCall{{
						ID:    "",
						Name:  "update-plan",
						Input: json.RawMessage(`"{\"explanation\":\"Keep going\"}"`),
					}},
				}, nil
			case 2:
				last := messages[len(messages)-1]
				if last.Role != "tool" || last.ToolName != "update_plan" {
					t.Fatalf("last tool message = %#v, want repaired update_plan tool result", last)
				}
				if last.ToolCallID == "" {
					t.Fatalf("last tool message = %#v, want synthetic tool_call_id", last)
				}
				if !strings.Contains(last.Content, "[PLAN_UPDATED]") {
					t.Fatalf("last tool content = %q, want update_plan output", last.Content)
				}
				return &Response{Content: "done"}, nil
			default:
				t.Fatalf("unexpected call %d", call)
				return nil, nil
			}
		},
	}

	tools := &mockTools{
		defs: []ToolDef{{Name: "update_plan"}},
		exec: func(_ context.Context, name string, input json.RawMessage) (string, error) {
			if name != "update_plan" {
				t.Fatalf("tool name = %q, want repaired update_plan", name)
			}
			if string(input) != `{"explanation":"Keep going"}` {
				t.Fatalf("tool input = %s, want repaired unwrapped JSON object", string(input))
			}
			return "[PLAN_UPDATED]\nactive: false\nexplanation: Keep going", nil
		},
	}

	result, _, err := RunTurn(context.Background(), provider, tools, defaultBudget(), nil, nil)
	if err != nil {
		t.Fatalf("RunTurn() err = %v", err)
	}
	if result.Text != "done" {
		t.Fatalf("result.Text = %q, want done", result.Text)
	}
	if len(tools.execCalls) != 1 {
		t.Fatalf("tool calls = %d, want 1", len(tools.execCalls))
	}
}

func TestRunTurnInjectsToolErrorForInvalidToolArguments(t *testing.T) {
	t.Parallel()

	provider := &mockProvider{
		complete: func(_ context.Context, call int, messages []Message, _ []ToolDef) (*Response, error) {
			switch call {
			case 1:
				return &Response{
					ToolCalls: []ToolCall{{
						ID:    "call-1",
						Name:  "exec",
						Input: json.RawMessage(`{"command":"pwd",}`),
					}},
				}, nil
			case 2:
				last := messages[len(messages)-1]
				if last.Role != "tool" || last.ToolCallID != "call-1" {
					t.Fatalf("last tool message = %#v, want recovery tool result", last)
				}
				var failure struct {
					OK          bool   `json:"ok"`
					Code        string `json:"code"`
					ShortReason string `json:"short_reason"`
					RetryHint   string `json:"retry_hint"`
				}
				if err := json.Unmarshal([]byte(last.Content), &failure); err != nil {
					t.Fatalf("decode typed tool failure %q: %v", last.Content, err)
				}
				if failure.OK || failure.Code != "SCHEMA_VIOLATION" || !strings.Contains(failure.ShortReason, "invalid tool arguments") || failure.RetryHint != "Reformulate" {
					t.Fatalf("failure = %#v, want schema violation recovery error", failure)
				}
				return &Response{Content: "recovered"}, nil
			default:
				t.Fatalf("unexpected call %d", call)
				return nil, nil
			}
		},
	}

	tools := &mockTools{
		defs: []ToolDef{{Name: "exec"}},
		exec: func(_ context.Context, _ string, _ json.RawMessage) (string, error) {
			t.Fatal("exec should not run when arguments stay invalid")
			return "", nil
		},
	}

	result, history, err := RunTurn(context.Background(), provider, tools, defaultBudget(), nil, nil)
	if err != nil {
		t.Fatalf("RunTurn() err = %v", err)
	}
	if result.Text != "recovered" {
		t.Fatalf("result.Text = %q, want recovered", result.Text)
	}
	if len(tools.execCalls) != 0 {
		t.Fatalf("tool calls = %d, want 0", len(tools.execCalls))
	}
	found := false
	for _, msg := range history {
		if msg.Role != "tool" {
			continue
		}
		var failure struct {
			Code        string `json:"code"`
			ShortReason string `json:"short_reason"`
		}
		if err := json.Unmarshal([]byte(msg.Content), &failure); err != nil {
			continue
		}
		if failure.Code == "SCHEMA_VIOLATION" && strings.Contains(failure.ShortReason, "invalid tool arguments") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("history = %#v, want typed invalid tool argument recovery message", history)
	}
}

func TestRunTurnBlocksRepeatedNoProgressToolLoop(t *testing.T) {
	t.Parallel()

	provider := &mockProvider{
		complete: func(_ context.Context, call int, messages []Message, _ []ToolDef) (*Response, error) {
			switch call {
			case 1, 2, 3:
				return &Response{
					ToolCalls: []ToolCall{{
						ID:    fmt.Sprintf("exec-%d", call),
						Name:  "exec",
						Input: json.RawMessage(`{"command":"pwd"}`),
					}},
				}, nil
			case 4:
				last := messages[len(messages)-1]
				if last.Role != "tool" || !strings.Contains(last.Content, "no-progress tool loop") {
					t.Fatalf("last tool message = %#v, want loop guard error", last)
				}
				return &Response{Content: "stopped retrying"}, nil
			default:
				t.Fatalf("unexpected call %d", call)
				return nil, nil
			}
		},
	}

	tools := &mockTools{
		defs: []ToolDef{{Name: "exec"}},
		exec: func(_ context.Context, _ string, _ json.RawMessage) (string, error) {
			return "stdout:\n/home/test", nil
		},
	}

	result, _, err := RunTurn(context.Background(), provider, tools, defaultBudget(), nil, nil)
	if err != nil {
		t.Fatalf("RunTurn() err = %v", err)
	}
	if result.Text != "stopped retrying" {
		t.Fatalf("result.Text = %q, want stopped retrying", result.Text)
	}
	if len(tools.execCalls) != 2 {
		t.Fatalf("tool calls = %d, want 2 before loop guard blocked the third retry", len(tools.execCalls))
	}
}
