//go:build linux

package governorbackend

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/idolum-ai/aphelion/agent"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestCodexCompleteTextUsesResponsesProtocol(t *testing.T) {
	t.Parallel()

	var (
		seenAuth         string
		seenAccountID    string
		seenPath         string
		seenInstructions string
	)
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenAuth = r.Header.Get("Authorization")
		seenAccountID = r.Header.Get("ChatGPT-Account-ID")
		seenPath = r.URL.Path

		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		seenInstructions, _ = payload["instructions"].(string)
		if payload["model"] != defaultCodexModel {
			t.Fatalf("model = %#v, want %q", payload["model"], defaultCodexModel)
		}

		assertStreamRequest(t, payload, false)
		writeSSE(t, w,
			sseEvent("response.output_text.delta", map[string]any{
				"type":  "response.output_text.delta",
				"delta": "hello from codex",
			}),
			sseEvent("response.completed", map[string]any{
				"type": "response.completed",
				"response": map[string]any{
					"id": "resp1",
					"usage": map[string]any{
						"input_tokens":  10,
						"output_tokens": 5,
						"total_tokens":  15,
						"input_tokens_details": map[string]any{
							"cached_tokens":      3,
							"cache_write_tokens": 2,
						},
					},
				},
			}),
		)
	})

	client, err := NewCodex(CodexOptions{
		BaseURL:     "https://chatgpt.com/backend-api",
		AccessToken: "secret-token",
		AccountID:   "acct-123",
		HTTPClient:  &http.Client{Transport: &testTransport{handler: handler}},
	})
	if err != nil {
		t.Fatalf("NewCodex() err = %v", err)
	}

	resp, err := client.Complete(context.Background(), []agent.Message{
		{Role: "system", Content: "system instructions"},
		{Role: "user", Content: "hi"},
	}, nil)
	if err != nil {
		t.Fatalf("Complete() err = %v", err)
	}
	if resp.Content != "hello from codex" {
		t.Fatalf("content = %q, want hello from codex", resp.Content)
	}
	if resp.Usage.TotalTokens != 15 || resp.Usage.CacheReadTokens != 3 || resp.Usage.CacheWriteTokens != 2 {
		t.Fatalf("usage = %#v, want totals and cache tokens", resp.Usage)
	}
	if seenAuth != "Bearer secret-token" {
		t.Fatalf("authorization = %q, want bearer token", seenAuth)
	}
	if seenAccountID != "acct-123" {
		t.Fatalf("account id = %q, want acct-123", seenAccountID)
	}
	if seenPath != "/backend-api/codex/responses" {
		t.Fatalf("path = %q, want /backend-api/codex/responses", seenPath)
	}
	if seenInstructions != "system instructions" {
		t.Fatalf("instructions = %q, want system instructions", seenInstructions)
	}
}

func TestCodexCompleteUsesConfiguredModel(t *testing.T) {
	t.Parallel()

	var seenModel any
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		seenModel = payload["model"]
		writeSSE(t, w,
			sseEvent("response.output_text.delta", map[string]any{
				"type":  "response.output_text.delta",
				"delta": "ok",
			}),
			sseEvent("response.completed", map[string]any{
				"type": "response.completed",
				"response": map[string]any{
					"id": "resp1",
				},
			}),
		)
	})

	client, err := NewCodex(CodexOptions{
		BaseURL:     "https://chatgpt.com/backend-api",
		AccessToken: "secret-token",
		AccountID:   "acct-123",
		Model:       "gpt-5.5",
		HTTPClient:  &http.Client{Transport: &testTransport{handler: handler}},
	})
	if err != nil {
		t.Fatalf("NewCodex() err = %v", err)
	}

	if _, err := client.Complete(context.Background(), []agent.Message{{Role: "user", Content: "hi"}}, nil); err != nil {
		t.Fatalf("Complete() err = %v", err)
	}
	if seenModel != "gpt-5.5" {
		t.Fatalf("model = %#v, want gpt-5.5", seenModel)
	}
}

func TestCodexCompleteParsesResponseFailedErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		code       string
		message    string
		wantCode   string
		wantRetry  time.Duration
		wantErrSub string
	}{
		{
			name:       "coded overload",
			code:       "server_is_overloaded",
			message:    "Our servers are currently overloaded. Please try again later.",
			wantCode:   "server_is_overloaded",
			wantErrSub: "currently overloaded",
		},
		{
			name:       "text overload",
			message:    "Our servers are currently overloaded. Please try again later.",
			wantCode:   "server_is_overloaded",
			wantErrSub: "currently overloaded",
		},
		{
			name:       "slow down",
			code:       "slow_down",
			message:    "Please slow down and try again later.",
			wantCode:   "slow_down",
			wantErrSub: "slow down",
		},
		{
			name:       "rate limit with delay",
			code:       "rate_limit_exceeded",
			message:    "Rate limit reached for gpt-5.5. Please try again in 11.054s.",
			wantCode:   "rate_limit_exceeded",
			wantRetry:  11054 * time.Millisecond,
			wantErrSub: "Rate limit reached",
		},
		{
			name:       "context window",
			code:       "context_length_exceeded",
			message:    "Your input exceeds the context window of this model.",
			wantCode:   "context_length_exceeded",
			wantErrSub: "context window",
		},
		{
			name:       "invalid prompt",
			code:       "invalid_prompt",
			message:    "Invalid prompt.",
			wantCode:   "invalid_prompt",
			wantErrSub: "Invalid prompt",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				writeSSE(t, w,
					sseEvent("response.failed", map[string]any{
						"type": "response.failed",
						"response": map[string]any{
							"id":     "resp-failed",
							"status": "failed",
							"error": map[string]any{
								"code":    tt.code,
								"message": tt.message,
							},
						},
					}),
				)
			})
			client, err := NewCodex(CodexOptions{
				BaseURL:     "https://chatgpt.com/backend-api",
				AccessToken: "secret-token",
				AccountID:   "acct-123",
				HTTPClient:  &http.Client{Transport: &testTransport{handler: handler}},
			})
			if err != nil {
				t.Fatalf("NewCodex() err = %v", err)
			}
			_, err = client.Complete(context.Background(), []agent.Message{{Role: "user", Content: "hi"}}, nil)
			if err == nil {
				t.Fatal("Complete() err = nil, want response.failed error")
			}
			var failed *codexFailedError
			if !errors.As(err, &failed) {
				t.Fatalf("Complete() err = %T/%v, want codexFailedError", err, err)
			}
			if failed.ProviderFailureCode() != tt.wantCode {
				t.Fatalf("ProviderFailureCode() = %q, want %q", failed.ProviderFailureCode(), tt.wantCode)
			}
			if failed.ProviderRetryAfter() != tt.wantRetry {
				t.Fatalf("ProviderRetryAfter() = %v, want %v", failed.ProviderRetryAfter(), tt.wantRetry)
			}
			if !strings.Contains(err.Error(), tt.wantErrSub) {
				t.Fatalf("err = %q, want substring %q", err.Error(), tt.wantErrSub)
			}
		})
	}
}

func TestCodexHTTPStatusErrorCarriesFailureCodeAndRetryAfter(t *testing.T) {
	now := time.Date(2026, 5, 4, 12, 0, 0, 0, time.UTC)
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "2.5")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"detail":"Rate limit reached for gpt-5.5."}`))
	})
	client, err := NewCodex(CodexOptions{
		BaseURL:     "https://chatgpt.com/backend-api",
		AccessToken: "secret-token",
		AccountID:   "acct-123",
		HTTPClient:  &http.Client{Transport: &testTransport{handler: handler}},
		Now:         func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("NewCodex() err = %v", err)
	}

	_, err = client.Complete(context.Background(), []agent.Message{{Role: "user", Content: "hi"}}, nil)
	if err == nil {
		t.Fatal("Complete() err = nil, want HTTP status error")
	}
	var apiErr codexAPIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("Complete() err = %T/%v, want codexAPIError", err, err)
	}
	if apiErr.ProviderFailureCode() != codexFailureCodeRateLimit {
		t.Fatalf("ProviderFailureCode() = %q, want %q", apiErr.ProviderFailureCode(), codexFailureCodeRateLimit)
	}
	if apiErr.ProviderRetryAfter() != 2500*time.Millisecond {
		t.Fatalf("ProviderRetryAfter() = %v, want 2.5s", apiErr.ProviderRetryAfter())
	}
}

func TestCodexCompleteToolCallViaResponsesOutput(t *testing.T) {
	t.Parallel()

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		assertStreamRequest(t, payload, false)
		writeSSE(t, w,
			sseEvent("response.output_item.done", map[string]any{
				"type": "response.output_item.done",
				"item": map[string]any{
					"type":      "function_call",
					"name":      "exec",
					"call_id":   "tc1",
					"arguments": `{"command":"pwd"}`,
				},
			}),
			sseEvent("response.completed", map[string]any{
				"type": "response.completed",
				"response": map[string]any{
					"id": "resp1",
				},
			}),
		)
	})

	client, err := NewCodex(CodexOptions{
		BaseURL:     "https://chatgpt.com/backend-api",
		AccessToken: "secret-token",
		AccountID:   "acct-123",
		HTTPClient:  &http.Client{Transport: &testTransport{handler: handler}},
	})
	if err != nil {
		t.Fatalf("NewCodex() err = %v", err)
	}

	resp, err := client.Complete(context.Background(), []agent.Message{{Role: "user", Content: "run pwd"}}, []agent.ToolDef{{
		Name:       "exec",
		Parameters: json.RawMessage(`{"type":"object"}`),
	}})
	if err != nil {
		t.Fatalf("Complete() err = %v", err)
	}
	if len(resp.ToolCalls) != 1 {
		t.Fatalf("tool calls len = %d, want 1", len(resp.ToolCalls))
	}
	if resp.ToolCalls[0].ID != "tc1" || resp.ToolCalls[0].Name != "exec" {
		t.Fatalf("tool call = %#v", resp.ToolCalls[0])
	}
	if got := string(resp.ToolCalls[0].Input); got != `{"command":"pwd"}` {
		t.Fatalf("tool input = %q, want pwd payload", got)
	}
}

func TestCodexCompleteMapsImageGenerationCallToMedia(t *testing.T) {
	t.Parallel()

	png := "iVBORw0KGgo="
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeSSE(t, w,
			sseEvent("response.output_text.delta", map[string]any{
				"type":  "response.output_text.delta",
				"delta": "Draft generated.",
			}),
			sseEvent("response.output_item.done", map[string]any{
				"type": "response.output_item.done",
				"item": map[string]any{
					"type":           "image_generation_call",
					"id":             "ig_456",
					"status":         "completed",
					"revised_prompt": "A quiet phosphor gate.",
					"result":         png,
				},
			}),
			sseEvent("response.completed", map[string]any{
				"type":     "response.completed",
				"response": map[string]any{"id": "resp1"},
			}),
		)
	})

	client, err := NewCodex(CodexOptions{
		BaseURL:     "https://chatgpt.com/backend-api",
		AccessToken: "secret-token",
		AccountID:   "acct-123",
		HTTPClient:  &http.Client{Transport: &testTransport{handler: handler}},
	})
	if err != nil {
		t.Fatalf("NewCodex() err = %v", err)
	}

	resp, err := client.Complete(context.Background(), []agent.Message{{Role: "user", Content: "make image"}}, nil)
	if err != nil {
		t.Fatalf("Complete() err = %v", err)
	}
	if resp.Content != "Draft generated." {
		t.Fatalf("content = %q", resp.Content)
	}
	if len(resp.Media) != 1 {
		t.Fatalf("media len = %d, want 1", len(resp.Media))
	}
	media := resp.Media[0]
	if media.Type != "image" || media.MimeType != "image/png" || media.Filename != "image-generation-call-ig_456.png" {
		t.Fatalf("media metadata = %#v", media)
	}
	if string(media.Data) != string([]byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}) {
		t.Fatalf("media bytes = %v, want PNG signature", media.Data)
	}
}

func TestCodexRequestIncludesImageGenerationBuiltInTool(t *testing.T) {
	t.Parallel()

	var seenTools []any
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if tools, ok := payload["tools"].([]any); ok {
			seenTools = tools
		}
		writeSSE(t, w,
			sseEvent("response.output_text.delta", map[string]any{
				"type":  "response.output_text.delta",
				"delta": "blocked",
			}),
			sseEvent("response.completed", map[string]any{
				"type":     "response.completed",
				"response": map[string]any{"id": "resp1"},
			}),
		)
	})

	client, err := NewCodex(CodexOptions{
		BaseURL:     "https://chatgpt.com/backend-api",
		AccessToken: "secret-token",
		AccountID:   "acct-123",
		HTTPClient:  &http.Client{Transport: &testTransport{handler: handler}},
	})
	if err != nil {
		t.Fatalf("NewCodex() err = %v", err)
	}

	_, err = client.Complete(context.Background(), []agent.Message{{Role: "user", Content: "make image"}}, []agent.ToolDef{{Name: "image_generation", Parameters: json.RawMessage(`{"type":"builtin","output_format":"png"}`)}})
	if err != nil {
		t.Fatalf("Complete() err = %v", err)
	}
	if len(seenTools) != 1 {
		t.Fatalf("tools len = %d, want 1: %#v", len(seenTools), seenTools)
	}
	tool, ok := seenTools[0].(map[string]any)
	if !ok {
		t.Fatalf("tool = %#v, want object", seenTools[0])
	}
	if tool["type"] != "image_generation" || tool["output_format"] != "png" {
		t.Fatalf("image_generation tool = %#v, want built-in tool spec", tool)
	}
}

func TestCodexCompleteMapsAssistantHistoryAsOutputText(t *testing.T) {
	t.Parallel()

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		assertStreamRequest(t, payload, false)
		input, ok := payload["input"].([]any)
		if !ok || len(input) < 2 {
			t.Fatalf("input = %#v, want at least user and assistant items", payload["input"])
		}
		assistantItem, ok := input[1].(map[string]any)
		if !ok {
			t.Fatalf("assistant item = %#v, want object", input[1])
		}
		content, ok := assistantItem["content"].([]any)
		if !ok || len(content) == 0 {
			t.Fatalf("assistant content = %#v, want blocks", assistantItem["content"])
		}
		block, ok := content[0].(map[string]any)
		if !ok {
			t.Fatalf("assistant block = %#v, want object", content[0])
		}
		if block["type"] != "output_text" {
			t.Fatalf("assistant block type = %#v, want output_text", block["type"])
		}

		writeSSE(t, w,
			sseEvent("response.output_text.delta", map[string]any{
				"type":  "response.output_text.delta",
				"delta": "ok",
			}),
			sseEvent("response.completed", map[string]any{
				"type": "response.completed",
				"response": map[string]any{
					"id": "resp1",
				},
			}),
		)
	})

	client, err := NewCodex(CodexOptions{
		BaseURL:     "https://chatgpt.com/backend-api",
		AccessToken: "secret-token",
		AccountID:   "acct-123",
		HTTPClient:  &http.Client{Transport: &testTransport{handler: handler}},
	})
	if err != nil {
		t.Fatalf("NewCodex() err = %v", err)
	}

	_, err = client.Complete(context.Background(), []agent.Message{
		{Role: "user", Content: "first"},
		{Role: "assistant", Content: "second"},
		{Role: "user", Content: "third"},
	}, nil)
	if err != nil {
		t.Fatalf("Complete() err = %v", err)
	}
}

func TestCodexCompletePreservesReasoningItemsForFullContextReplay(t *testing.T) {
	t.Parallel()

	var (
		requestCount int
		secondInput  []any
	)
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if requestCount == 2 {
			items, ok := payload["input"].([]any)
			if !ok {
				t.Fatalf("input = %#v, want []any", payload["input"])
			}
			secondInput = append([]any(nil), items...)
		}
		writeSSE(t, w,
			sseEvent("response.output_item.done", map[string]any{
				"type": "response.output_item.done",
				"item": map[string]any{
					"type":    "reasoning",
					"id":      "rs_123",
					"summary": []map[string]any{{"text": "private reasoning summary"}},
				},
			}),
			sseEvent("response.output_text.delta", map[string]any{
				"type":  "response.output_text.delta",
				"delta": "hello",
			}),
			sseEvent("response.completed", map[string]any{
				"type": "response.completed",
				"response": map[string]any{
					"id": "resp1",
				},
			}),
		)
	})

	client, err := NewCodex(CodexOptions{
		BaseURL:        "https://chatgpt.com/backend-api",
		AccessToken:    "secret-token",
		AccountID:      "acct-123",
		StoreResponses: true,
		HTTPClient:     &http.Client{Transport: &testTransport{handler: handler}},
	})
	if err != nil {
		t.Fatalf("NewCodex() err = %v", err)
	}

	first, err := client.Complete(context.Background(), []agent.Message{{Role: "user", Content: "first"}}, nil)
	if err != nil {
		t.Fatalf("first Complete() err = %v", err)
	}
	state, ok := decodeCodexProviderState(first.ProviderState)
	if !ok {
		t.Fatalf("provider state = %s, want codex provider state", string(first.ProviderState))
	}
	if len(state.ReasoningItems) != 1 {
		t.Fatalf("reasoning items len = %d, want 1", len(state.ReasoningItems))
	}

	_, err = client.Complete(context.Background(), []agent.Message{
		{Role: "user", Content: "first"},
		{Role: "assistant", Content: first.Content, Thinking: first.Thinking, ThinkingMeta: first.ThinkingMeta, ProviderState: first.ProviderState},
		{Role: "user", Content: "second"},
	}, nil)
	if err != nil {
		t.Fatalf("second Complete() err = %v", err)
	}
	if requestCount != 2 {
		t.Fatalf("request count = %d, want 2", requestCount)
	}
	if len(secondInput) < 3 {
		t.Fatalf("second input len = %d, want at least reasoning + assistant + user", len(secondInput))
	}
	reasoningItem, ok := secondInput[1].(map[string]any)
	if !ok {
		t.Fatalf("replayed reasoning item = %#v, want object", secondInput[1])
	}
	if reasoningItem["type"] != "reasoning" {
		t.Fatalf("replayed reasoning type = %#v, want reasoning", reasoningItem["type"])
	}
	if reasoningItem["id"] != "rs_123" {
		t.Fatalf("replayed reasoning id = %#v, want rs_123", reasoningItem["id"])
	}
}

func TestCodexCompleteStatusError(t *testing.T) {
	t.Parallel()

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"unauthorized"}`))
	})

	client, err := NewCodex(CodexOptions{
		BaseURL:     "https://chatgpt.com/backend-api",
		AccessToken: "secret-token",
		AccountID:   "acct-123",
		HTTPClient:  &http.Client{Transport: &testTransport{handler: handler}},
	})
	if err != nil {
		t.Fatalf("NewCodex() err = %v", err)
	}

	_, err = client.Complete(context.Background(), []agent.Message{{Role: "user", Content: "hi"}}, nil)
	if err == nil {
		t.Fatal("Complete() err = nil, want status error")
	}
	if !strings.Contains(err.Error(), "status 401") {
		t.Fatalf("error = %v, want status code", err)
	}
	if !strings.Contains(err.Error(), `{"error":"unauthorized"}`) {
		t.Fatalf("error = %v, want body excerpt", err)
	}
	if !errors.Is(err, ErrCodexUnauthorized) {
		t.Fatalf("error = %v, want ErrCodexUnauthorized", err)
	}
}
