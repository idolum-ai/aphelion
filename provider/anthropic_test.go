//go:build linux

package provider

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/idolum-ai/aphelion/agent"
	"github.com/idolum-ai/aphelion/core"
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
	if resp.Usage.CacheReadTokens != 7 || resp.Usage.CacheWriteTokens != 11 || resp.Usage.CacheCreationTokens != 11 {
		t.Fatalf("cache usage = %+v, want read=7 write=11 creation=11", resp.Usage)
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

func TestAnthropicCompleteWithOptionsOverridesMaxTokens(t *testing.T) {
	var seen anthropicRequest
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&seen); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		_ = json.NewEncoder(w).Encode(anthropicResponse{Content: []anthropicContent{{Type: "text", Text: "ok"}}})
	})

	client, err := NewAnthropic(AnthropicOptions{
		APIKey:     "test-key",
		Model:      "claude-2",
		MaxTokens:  4096,
		HTTPClient: &http.Client{Transport: &testTransport{handler: handler}},
	})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	_, err = client.CompleteWithOptions(context.Background(), []agent.Message{{Role: "user", Content: "hi"}}, nil, agent.CompleteOptions{MaxTokens: 777})
	if err != nil {
		t.Fatalf("CompleteWithOptions() err = %v", err)
	}
	if seen.MaxTokens != 777 {
		t.Fatalf("max_tokens = %d, want 777", seen.MaxTokens)
	}
}

func TestAnthropicCacheControlCountStaysWithinProviderLimitWithTools(t *testing.T) {
	var seen anthropicRequest
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&seen); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		_ = json.NewEncoder(w).Encode(anthropicResponse{Content: []anthropicContent{{Type: "text", Text: "ok"}}})
	})

	client, err := NewAnthropic(AnthropicOptions{
		APIKey:     "test-key",
		Model:      "claude-2",
		HTTPClient: &http.Client{Transport: &testTransport{handler: handler}},
	})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	blocks := make([]agent.SystemBlock, 5)
	for i := range blocks {
		blocks[i] = agent.SystemBlock{Text: "stable block"}
	}
	markStable := func(i int) { blocks[i].CacheBreakpoint = true }
	markStable(1)
	markStable(2)
	markStable(3)
	markStable(4)

	_, err = client.Complete(context.Background(), []agent.Message{{
		Role:         "system",
		Content:      "flattened system prompt",
		SystemBlocks: blocks,
	}, {Role: "user", Content: "hi"}}, []agent.ToolDef{
		{Name: "exec", Parameters: json.RawMessage(`{"type":"object"}`)},
		{Name: "status", Parameters: json.RawMessage(`{"type":"object"}`)},
	})
	if err != nil {
		t.Fatalf("Complete() err = %v", err)
	}

	count := anthropicCacheControlCount(seen)
	if count > 4 {
		t.Fatalf("cache_control count = %d, want <= 4; system=%#v tools=%#v", count, seen.System, seen.Tools)
	}
	if len(seen.Tools) == 0 || seen.Tools[len(seen.Tools)-1].CacheControl == nil {
		t.Fatalf("last tool cache control = %#v, want reserved breakpoint", seen.Tools)
	}
}

func anthropicCacheControlCount(req anthropicRequest) int {
	count := 0
	for _, block := range req.System {
		if block.CacheControl != nil {
			count++
		}
	}
	for _, msg := range req.Messages {
		for _, block := range msg.Content {
			if block.CacheControl != nil {
				count++
			}
		}
	}
	for _, tool := range req.Tools {
		if tool.CacheControl != nil {
			count++
		}
	}
	return count
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
	if seen.System[1].CacheControl.TTL != "5m" {
		t.Fatalf("cache ttl = %q, want default 5m", seen.System[1].CacheControl.TTL)
	}
	if seen.System[2].CacheControl != nil {
		t.Fatalf("dynamic block cache control = %#v, want nil", seen.System[2].CacheControl)
	}
}

func TestAnthropicCachePolicyTTLAndOff(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		opts         AnthropicOptions
		wantToolTTL  string
		wantNoCache  bool
		wantNewError string
	}{
		{
			name:        "one hour ttl",
			opts:        AnthropicOptions{APIKey: "test-key", Model: "claude-2", CacheTTL: "1h"},
			wantToolTTL: "1h",
		},
		{
			name:        "cache off",
			opts:        AnthropicOptions{APIKey: "test-key", Model: "claude-2", CacheStrategy: "off", CacheTTL: "1h"},
			wantNoCache: true,
		},
		{
			name:         "invalid ttl",
			opts:         AnthropicOptions{APIKey: "test-key", Model: "claude-2", CacheTTL: "10m"},
			wantNewError: "cache ttl",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var seen anthropicRequest
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if err := json.NewDecoder(r.Body).Decode(&seen); err != nil {
					t.Fatalf("decode request: %v", err)
				}
				_ = json.NewEncoder(w).Encode(anthropicResponse{Content: []anthropicContent{{Type: "text", Text: "ok"}}})
			})
			opts := tt.opts
			opts.HTTPClient = &http.Client{Transport: &testTransport{handler: handler}}
			client, err := NewAnthropic(opts)
			if tt.wantNewError != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantNewError) {
					t.Fatalf("NewAnthropic() err = %v, want %s", err, tt.wantNewError)
				}
				return
			}
			if err != nil {
				t.Fatalf("NewAnthropic() err = %v", err)
			}
			_, err = client.Complete(context.Background(), []agent.Message{{
				Role: "system",
				SystemBlocks: []agent.SystemBlock{
					{Text: "stable", CacheBreakpoint: true},
				},
			}, {Role: "user", Content: "hi"}}, []agent.ToolDef{{
				Name:       "exec",
				Parameters: json.RawMessage(`{"type":"object"}`),
			}})
			if err != nil {
				t.Fatalf("Complete() err = %v", err)
			}
			if tt.wantNoCache {
				if seen.System[0].CacheControl != nil || seen.Tools[0].CacheControl != nil {
					t.Fatalf("cache controls = system:%#v tool:%#v, want nil", seen.System[0].CacheControl, seen.Tools[0].CacheControl)
				}
				return
			}
			if seen.System[0].CacheControl == nil || seen.System[0].CacheControl.TTL != tt.wantToolTTL {
				t.Fatalf("system cache control = %#v, want ttl %s", seen.System[0].CacheControl, tt.wantToolTTL)
			}
			if seen.Tools[0].CacheControl == nil || seen.Tools[0].CacheControl.TTL != tt.wantToolTTL {
				t.Fatalf("tool cache control = %#v, want ttl %s", seen.Tools[0].CacheControl, tt.wantToolTTL)
			}
		})
	}
}

func TestAnthropicCompleteWithThinkingOptions(t *testing.T) {
	var seen anthropicRequest
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&seen); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		_ = json.NewEncoder(w).Encode(anthropicResponse{
			Content: []anthropicContent{
				{Type: "thinking", Thinking: "first reason", Signature: "sig-1"},
				{Type: "text", Text: "ok"},
			},
		})
	})

	client, err := NewAnthropic(AnthropicOptions{
		APIKey:     "test-key",
		Model:      "claude-2",
		MaxTokens:  4096,
		HTTPClient: &http.Client{Transport: &testTransport{handler: handler}},
	})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	resp, err := client.CompleteWithOptions(context.Background(), []agent.Message{{Role: "user", Content: "think"}}, nil, agent.CompleteOptions{
		Reasoning: agent.ReasoningConfig{
			Effort:  agent.ReasoningEffortMedium,
			Summary: agent.ReasoningSummaryCompact,
		},
	})
	if err != nil {
		t.Fatalf("CompleteWithOptions() err = %v", err)
	}

	if seen.Thinking == nil || seen.Thinking.Type != "enabled" {
		t.Fatalf("thinking request = %#v, want enabled", seen.Thinking)
	}
	if seen.Thinking.BudgetTokens != 2048 {
		t.Fatalf("budget_tokens = %d, want 2048", seen.Thinking.BudgetTokens)
	}
	if resp.Thinking != "first reason" {
		t.Fatalf("resp.Thinking = %q, want first reason", resp.Thinking)
	}
	if len(resp.ThinkingMeta) != 1 || resp.ThinkingMeta[0].Signature != "sig-1" {
		t.Fatalf("thinking meta = %#v, want one block with signature", resp.ThinkingMeta)
	}
}

func TestAnthropicCompleteMapsImageMediaBlocks(t *testing.T) {
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

	_, err = client.Complete(context.Background(), []agent.Message{{
		Role:    "user",
		Content: "what is in this screenshot?",
		Media: []core.Media{{
			Type:     "photo",
			Data:     []byte("image-bytes"),
			MimeType: "image/png",
			Filename: "screenshot.png",
		}},
	}}, nil)
	if err != nil {
		t.Fatalf("complete: %v", err)
	}

	if len(seen.Messages) != 1 || len(seen.Messages[0].Content) != 2 {
		t.Fatalf("messages = %#v", seen.Messages)
	}
	if seen.Messages[0].Content[0].Type != "image" {
		t.Fatalf("first content block = %#v, want image", seen.Messages[0].Content[0])
	}
	source, ok := seen.Messages[0].Content[0].Source.(map[string]any)
	if !ok {
		raw, _ := json.Marshal(seen.Messages[0].Content[0].Source)
		t.Fatalf("image source = %s, want object", raw)
	}
	if source["media_type"] != "image/png" {
		t.Fatalf("media_type = %v, want image/png", source["media_type"])
	}
	if seen.Messages[0].Content[1].Type != "text" || seen.Messages[0].Content[1].Text != "what is in this screenshot?" {
		t.Fatalf("text block = %#v", seen.Messages[0].Content[1])
	}
}

func TestAnthropicCompleteWithThinkingSummaryNone(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(anthropicResponse{
			Content: []anthropicContent{
				{Type: "thinking", Thinking: "hidden reason", Signature: "sig-1"},
				{Type: "text", Text: "ok"},
			},
		})
	})

	client, err := NewAnthropic(AnthropicOptions{
		APIKey:     "test-key",
		Model:      "claude-2",
		MaxTokens:  4096,
		HTTPClient: &http.Client{Transport: &testTransport{handler: handler}},
	})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	resp, err := client.CompleteWithOptions(context.Background(), []agent.Message{{Role: "user", Content: "think"}}, nil, agent.CompleteOptions{
		Reasoning: agent.ReasoningConfig{
			Effort:  agent.ReasoningEffortLow,
			Summary: agent.ReasoningSummaryNone,
		},
	})
	if err != nil {
		t.Fatalf("CompleteWithOptions() err = %v", err)
	}
	if resp.Thinking != "" {
		t.Fatalf("resp.Thinking = %q, want empty", resp.Thinking)
	}
	if len(resp.ThinkingMeta) != 1 {
		t.Fatalf("thinking meta len = %d, want 1", len(resp.ThinkingMeta))
	}
}

func TestAnthropicStreamText(t *testing.T) {
	var seen anthropicRequest
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&seen); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, strings.Join([]string{
			"event: message_start",
			`data: {"type":"message_start","message":{"usage":{"input_tokens":12,"cache_creation_input_tokens":40}}}`,
			"",
			"event: content_block_start",
			`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
			"",
			"event: content_block_delta",
			`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hel"}}`,
			"",
			"event: content_block_delta",
			`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"lo"}}`,
			"",
			"event: message_delta",
			`data: {"type":"message_delta","usage":{"output_tokens":5,"cache_read_input_tokens":22}}`,
			"",
			"event: message_stop",
			`data: {"type":"message_stop"}`,
			"",
		}, "\n"))
	})

	client, err := NewAnthropic(AnthropicOptions{
		APIKey:     "test-key",
		Model:      "claude-2",
		HTTPClient: &http.Client{Transport: &testTransport{handler: handler}},
	})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	var chunks []string
	resp, err := client.Stream(context.Background(), []agent.Message{{Role: "user", Content: "hi"}}, nil, func(chunk agent.StreamChunk) error {
		chunks = append(chunks, chunk.Text)
		return nil
	})
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	if !seen.Stream {
		t.Fatalf("stream flag = false, want true")
	}
	if strings.Join(chunks, "") != "hello" {
		t.Fatalf("chunks = %#v, want hello", chunks)
	}
	if resp.Content != "hello" {
		t.Fatalf("resp.Content = %q, want hello", resp.Content)
	}
	if resp.Usage.InputTokens != 12 || resp.Usage.OutputTokens != 5 {
		t.Fatalf("usage = %+v, want input 12 output 5", resp.Usage)
	}
	if resp.Usage.CacheReadTokens != 22 || resp.Usage.CacheWriteTokens != 40 || resp.Usage.CacheCreationTokens != 40 {
		t.Fatalf("cache usage = %+v, want read 22 write 40 creation 40", resp.Usage)
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
