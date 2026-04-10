//go:build linux

package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/idolum-ai/aphelion/agent"
	"github.com/idolum-ai/aphelion/core"
)

func TestOpenRouterCompleteTextAndUsage(t *testing.T) {
	var seen openRouterRequest
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Fatalf("Authorization = %q, want Bearer test-key", got)
		}
		if err := json.NewDecoder(r.Body).Decode(&seen); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		_ = json.NewEncoder(w).Encode(openRouterResponse{
			Choices: []struct {
				Message openRouterResponseMessage `json:"message"`
			}{
				{Message: openRouterResponseMessage{Content: json.RawMessage(`"hello from openrouter"`)}},
			},
			Usage: openRouterUsage{
				PromptTokens:     11,
				CompletionTokens: 7,
				PromptTokensDetails: struct {
					CachedTokens     int64 `json:"cached_tokens"`
					CacheWriteTokens int64 `json:"cache_write_tokens"`
				}{CachedTokens: 5, CacheWriteTokens: 9},
			},
		})
	})

	client, err := NewOpenRouter(OpenRouterOptions{
		APIKey:     "test-key",
		Model:      "anthropic/claude-sonnet-4-6",
		HTTPClient: &http.Client{Transport: &testTransport{handler: handler}},
	})
	if err != nil {
		t.Fatalf("NewOpenRouter() err = %v", err)
	}

	resp, err := client.Complete(context.Background(), []agent.Message{{Role: "user", Content: "hi"}}, nil)
	if err != nil {
		t.Fatalf("Complete() err = %v", err)
	}
	if resp.Content != "hello from openrouter" {
		t.Fatalf("content = %q, want hello from openrouter", resp.Content)
	}
	if resp.Usage.InputTokens != 11 || resp.Usage.OutputTokens != 7 || resp.Usage.TotalTokens != 18 {
		t.Fatalf("usage = %+v, want prompt=11 completion=7 total=18", resp.Usage)
	}
	if resp.Usage.CacheReadTokens != 5 || resp.Usage.CacheWriteTokens != 9 {
		t.Fatalf("cache usage = %+v, want read=5 write=9", resp.Usage)
	}
	if seen.Model != "anthropic/claude-sonnet-4-6" {
		t.Fatalf("model = %q", seen.Model)
	}
}

func TestOpenRouterCompleteMapsToolsAndToolResults(t *testing.T) {
	var seen openRouterRequest
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&seen); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		_ = json.NewEncoder(w).Encode(openRouterResponse{
			Choices: []struct {
				Message openRouterResponseMessage `json:"message"`
			}{
				{
					Message: openRouterResponseMessage{
						Content: json.RawMessage(`"done"`),
						ToolCalls: []openRouterToolCall{{
							ID:   "call_1",
							Type: "function",
							Function: openRouterToolCallTarget{
								Name:      "exec",
								Arguments: `{"command":"pwd"}`,
							},
						}},
					},
				},
			},
			Usage: openRouterUsage{PromptTokens: 1, CompletionTokens: 1, TotalTokens: 2},
		})
	})

	client, err := NewOpenRouter(OpenRouterOptions{
		APIKey:     "test-key",
		Model:      "anthropic/claude-sonnet-4-6",
		HTTPClient: &http.Client{Transport: &testTransport{handler: handler}},
	})
	if err != nil {
		t.Fatalf("NewOpenRouter() err = %v", err)
	}

	resp, err := client.Complete(context.Background(), []agent.Message{
		{Role: "system", Content: "system prompt"},
		{Role: "assistant", ToolCalls: []agent.ToolCall{{
			ID:    "toolu_1",
			Name:  "exec",
			Input: json.RawMessage(`{"command":"ls"}`),
		}}},
		{Role: "tool", ToolCallID: "toolu_1", Content: "stdout"},
	}, []agent.ToolDef{{
		Name:       "exec",
		Parameters: json.RawMessage(`{"type":"object"}`),
	}})
	if err != nil {
		t.Fatalf("Complete() err = %v", err)
	}
	if len(seen.Tools) != 1 || seen.Tools[0].Function.Name != "exec" {
		t.Fatalf("tools = %#v", seen.Tools)
	}
	if len(seen.Messages) != 3 {
		t.Fatalf("messages = %#v", seen.Messages)
	}
	if seen.Messages[1].Role != "assistant" || len(seen.Messages[1].ToolCalls) != 1 {
		t.Fatalf("assistant message = %#v", seen.Messages[1])
	}
	if seen.Messages[2].Role != "tool" || seen.Messages[2].ToolCallID != "toolu_1" {
		t.Fatalf("tool message = %#v", seen.Messages[2])
	}
	if len(resp.ToolCalls) != 1 || resp.ToolCalls[0].Name != "exec" {
		t.Fatalf("tool calls = %#v", resp.ToolCalls)
	}
}

func TestOpenRouterCompleteMapsImageMediaParts(t *testing.T) {
	var seen openRouterRequest
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&seen); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		_ = json.NewEncoder(w).Encode(openRouterResponse{
			Choices: []struct {
				Message openRouterResponseMessage `json:"message"`
			}{
				{Message: openRouterResponseMessage{Content: json.RawMessage(`"ok"`)}},
			},
		})
	})

	client, err := NewOpenRouter(OpenRouterOptions{
		APIKey:     "test-key",
		Model:      "anthropic/claude-sonnet-4-6",
		HTTPClient: &http.Client{Transport: &testTransport{handler: handler}},
	})
	if err != nil {
		t.Fatalf("NewOpenRouter() err = %v", err)
	}

	_, err = client.Complete(context.Background(), []agent.Message{{
		Role:    "user",
		Content: "read this",
		Media: []core.Media{{
			Type:     "photo",
			Data:     []byte("image-bytes"),
			MimeType: "image/jpeg",
			Filename: "photo.jpg",
		}},
	}}, nil)
	if err != nil {
		t.Fatalf("Complete() err = %v", err)
	}

	if len(seen.Messages) != 1 {
		t.Fatalf("messages = %#v", seen.Messages)
	}
	parts, ok := seen.Messages[0].Content.([]any)
	if !ok || len(parts) != 2 {
		t.Fatalf("content = %#v, want two-part multimodal array", seen.Messages[0].Content)
	}
	imagePart, ok := parts[0].(map[string]any)
	if !ok || imagePart["type"] != "image_url" {
		t.Fatalf("image part = %#v", parts[0])
	}
	textPart, ok := parts[1].(map[string]any)
	if !ok || textPart["text"] != "read this" {
		t.Fatalf("text part = %#v", parts[1])
	}
}
