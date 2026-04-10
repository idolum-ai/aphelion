//go:build linux

package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/idolum-ai/aphelion/agent"
)

func TestAnthropicCompleteText(t *testing.T) {
	var seen anthropicRequest
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("unexpected method %s", r.Method)
		}
		if got := r.Header.Get("x-api-key"); got != "test-key" {
			t.Fatalf("x-api-key = %q, want %q", got, "test-key")
		}
		if got := r.Header.Get("Anthropic-Version"); got != defaultAnthropicVersion {
			t.Fatalf("Anthropic-Version = %q, want %q", got, defaultAnthropicVersion)
		}

		if err := json.NewDecoder(r.Body).Decode(&seen); err != nil {
			t.Fatalf("decode request: %v", err)
		}

		res := anthropicResponse{
			Content: []anthropicContent{
				{Type: "text", Text: "hello world"},
			},
			Usage: anthropicUsage{
				InputTokens:              5,
				OutputTokens:             3,
				CacheReadInputTokens:     7,
				CacheCreationInputTokens: 11,
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(res)
	})

	client, err := NewAnthropic(AnthropicOptions{
		APIKey:     "test-key",
		Model:      "claude-2",
		MaxTokens:  128,
		HTTPClient: &http.Client{Transport: &testTransport{handler: handler}},
	})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	resp, err := client.Complete(context.Background(), []agent.Message{{Role: "user", Content: "hi"}}, nil)
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	if resp.Content != "hello world" {
		t.Fatalf("content = %q, want %q", resp.Content, "hello world")
	}
	if got := resp.Usage.TotalTokens; got != 8 {
		t.Fatalf("total tokens = %d, want %d", got, 8)
	}
	if resp.Usage.CacheReadTokens != 7 || resp.Usage.CacheWriteTokens != 11 {
		t.Fatalf("cache usage = %+v, want read=7 write=11", resp.Usage)
	}
	if len(seen.Messages) != 1 {
		t.Fatalf("messages = %#v", seen.Messages)
	}
	if len(seen.System) != 0 {
		t.Fatalf("system = %#v, want empty", seen.System)
	}
	if seen.MaxTokens != 128 {
		t.Fatalf("max_tokens = %d, want 128", seen.MaxTokens)
	}
	if seen.Messages[0].Role != "user" {
		t.Fatalf("role = %q, want user", seen.Messages[0].Role)
	}
}

func TestAnthropicCompleteToolCall(t *testing.T) {
	toolInput := json.RawMessage(`{"cmd":"ls"}`)
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(anthropicResponse{
			Content: []anthropicContent{
				{
					Type:  "tool_use",
					ID:    "toolu_123",
					Name:  "shell.exec",
					Input: toolInput,
				},
				{
					Type: "text",
					Text: "tool result after call",
				},
			},
			Usage: anthropicUsage{InputTokens: 1, OutputTokens: 1},
		})
	})

	client, err := NewAnthropic(AnthropicOptions{
		APIKey:     "test-key",
		Model:      "claude-2",
		HTTPClient: &http.Client{Transport: &testTransport{handler: handler}},
	})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	resp, err := client.Complete(context.Background(), []agent.Message{{Role: "user", Content: "execute"}}, nil)
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	if len(resp.ToolCalls) != 1 {
		t.Fatalf("tool calls = %d, want 1", len(resp.ToolCalls))
	}
	call := resp.ToolCalls[0]
	if call.ID != "toolu_123" || call.Name != "shell.exec" {
		t.Fatalf("unexpected tool call = %#v", call)
	}
	if string(call.Input) != string(toolInput) {
		t.Fatalf("tool input = %s, want %s", call.Input, toolInput)
	}
	if resp.Content != "tool result after call" {
		t.Fatalf("content = %q, want %q", resp.Content, "tool result after call")
	}
}

func TestAnthropicCompleteMapsSystemAndToolResults(t *testing.T) {
	var seen anthropicRequest
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&seen); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		_ = json.NewEncoder(w).Encode(anthropicResponse{
			Content: []anthropicContent{{Type: "text", Text: "ok"}},
		})
	})

	client, err := NewAnthropic(AnthropicOptions{
		APIKey:     "test-key",
		Model:      "claude-2",
		HTTPClient: &http.Client{Transport: &testTransport{handler: handler}},
	})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	_, err = client.Complete(context.Background(), []agent.Message{
		{Role: "system", Content: "system prompt"},
		{Role: "assistant", ToolCalls: []agent.ToolCall{{
			ID:    "toolu_123",
			Name:  "exec",
			Input: json.RawMessage(`{"command":"pwd"}`),
		}}},
		{Role: "tool", ToolCallID: "toolu_123", Content: "stdout:\n/home/app"},
	}, []agent.ToolDef{{
		Name:       "exec",
		Parameters: json.RawMessage(`{"type":"object"}`),
	}})
	if err != nil {
		t.Fatalf("complete: %v", err)
	}

	if len(seen.System) != 1 || seen.System[0].Text != "system prompt" {
		t.Fatalf("system = %#v, want single text block", seen.System)
	}
	if len(seen.Messages) != 2 {
		t.Fatalf("message count = %d, want 2", len(seen.Messages))
	}
	if seen.Messages[0].Content[0].Type != "tool_use" {
		t.Fatalf("assistant content = %#v", seen.Messages[0].Content)
	}
	if seen.Messages[1].Content[0].Type != "tool_result" {
		t.Fatalf("tool content = %#v", seen.Messages[1].Content)
	}
	if len(seen.Tools) != 1 || seen.Tools[0].Name != "exec" {
		t.Fatalf("tools = %#v", seen.Tools)
	}
	if seen.Tools[0].CacheControl == nil || seen.Tools[0].CacheControl.Type != "ephemeral" {
		t.Fatalf("tool cache control = %#v, want ephemeral", seen.Tools[0].CacheControl)
	}
}

func TestAnthropicCompletePreservesSystemCacheBreakpoints(t *testing.T) {
	var seen anthropicRequest
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&seen); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		_ = json.NewEncoder(w).Encode(anthropicResponse{
			Content: []anthropicContent{{Type: "text", Text: "ok"}},
		})
	})

	client, err := NewAnthropic(AnthropicOptions{
		APIKey:     "test-key",
		Model:      "claude-2",
		HTTPClient: &http.Client{Transport: &testTransport{handler: handler}},
	})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	_, err = client.Complete(context.Background(), []agent.Message{
		{
			Role:    "system",
			Content: "flattened system prompt",
			SystemBlocks: []agent.SystemBlock{
				{Text: "stable authority"},
				{Text: "stable files", CacheBreakpoint: true},
				{Text: "dynamic memory"},
			},
		},
		{Role: "user", Content: "hi"},
	}, nil)
	if err != nil {
		t.Fatalf("complete: %v", err)
	}

	if len(seen.System) != 3 {
		t.Fatalf("system block count = %d, want 3", len(seen.System))
	}
	if seen.System[1].CacheControl == nil || seen.System[1].CacheControl.Type != "ephemeral" {
		t.Fatalf("cache control on stable breakpoint = %#v, want ephemeral", seen.System[1].CacheControl)
	}
	if seen.System[2].CacheControl != nil {
		t.Fatalf("dynamic block cache control = %#v, want nil", seen.System[2].CacheControl)
	}
}

type testTransport struct {
	handler http.Handler
}

func (t *testTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	rec := httptest.NewRecorder()
	t.handler.ServeHTTP(rec, req)
	return rec.Result(), nil
}
