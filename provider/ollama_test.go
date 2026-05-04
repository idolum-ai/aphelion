//go:build linux

package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/idolum-ai/aphelion/agent"
	"github.com/idolum-ai/aphelion/core"
)

func TestOllamaCompleteMapsRequestResponseAndUsage(t *testing.T) {
	var (
		seenPath string
		seen     ollamaRequest
	)
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenPath = r.URL.Path
		if err := json.NewDecoder(r.Body).Decode(&seen); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		_ = json.NewEncoder(w).Encode(ollamaResponse{
			Message: ollamaMessage{
				Role:    "assistant",
				Content: "done",
				ToolCalls: []ollamaToolCall{{
					Type:     "function",
					Function: ollamaToolCallFunction{Name: "exec", Arguments: json.RawMessage(`{"cmd":"pwd"}`)},
				}},
			},
			PromptEvalCount: 6,
			EvalCount:       4,
		})
	})

	client, err := NewOllama(OllamaOptions{
		BaseURL:    "http://ollama.test",
		Model:      "llama-test",
		MaxTokens:  512,
		HTTPClient: &http.Client{Transport: &testTransport{handler: handler}},
		UserAgent:  "aphelion-test",
	})
	if err != nil {
		t.Fatalf("NewOllama() err = %v", err)
	}

	resp, err := client.Complete(context.Background(), []agent.Message{
		{Role: "system", Content: "system prompt"},
		{Role: "user", Content: "read this", Media: []core.Media{{
			Type:     "photo",
			Data:     []byte("image-bytes"),
			MimeType: "image/png",
		}}},
		{Role: "assistant", ToolCalls: []agent.ToolCall{{
			ID:    "call_1",
			Name:  "exec",
			Input: json.RawMessage(`{"cmd":"ls"}`),
		}}},
		{Role: "tool", ToolCallID: "call_1", ToolName: "exec", Content: "stdout"},
	}, []agent.ToolDef{{
		Name:        "exec",
		Description: "Run a command",
		Parameters:  json.RawMessage(`{"type":"object"}`),
	}})
	if err != nil {
		t.Fatalf("Complete() err = %v", err)
	}
	if seenPath != "/api/chat" {
		t.Fatalf("path = %q", seenPath)
	}
	if seen.Model != "llama-test" || seen.Stream {
		t.Fatalf("model/stream = %q/%v", seen.Model, seen.Stream)
	}
	if seen.Options.NumPredict != 512 {
		t.Fatalf("num_predict = %d, want 512", seen.Options.NumPredict)
	}
	if len(seen.Tools) != 1 || seen.Tools[0].Function.Name != "exec" {
		t.Fatalf("tools = %#v", seen.Tools)
	}
	if len(seen.Messages) != 4 || len(seen.Messages[1].Images) != 1 {
		t.Fatalf("messages = %#v", seen.Messages)
	}
	if len(seen.Messages[2].ToolCalls) != 1 || string(seen.Messages[2].ToolCalls[0].Function.Arguments) != `{"cmd":"ls"}` {
		t.Fatalf("assistant tool calls = %#v", seen.Messages[2].ToolCalls)
	}
	if resp.Content != "done" {
		t.Fatalf("content = %q, want done", resp.Content)
	}
	if len(resp.ToolCalls) != 1 || resp.ToolCalls[0].Name != "exec" || string(resp.ToolCalls[0].Input) != `{"cmd":"pwd"}` {
		t.Fatalf("tool calls = %#v", resp.ToolCalls)
	}
	if resp.Usage.InputTokens != 6 || resp.Usage.OutputTokens != 4 || resp.Usage.TotalTokens != 10 {
		t.Fatalf("usage = %+v", resp.Usage)
	}
}

func TestOllamaStreamMapsTextToolCallsAndUsage(t *testing.T) {
	var seen ollamaRequest
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&seen); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		_, _ = w.Write([]byte(`{"message":{"role":"assistant","content":"he"},"done":false}` + "\n"))
		_, _ = w.Write([]byte(`{"message":{"role":"assistant","content":"llo","tool_calls":[{"type":"function","function":{"name":"exec","arguments":{"cmd":"pwd"}}}]},"prompt_eval_count":3,"eval_count":2,"done":true}` + "\n"))
	})

	client, err := NewOllama(OllamaOptions{
		BaseURL:    "http://ollama.test",
		Model:      "llama-test",
		HTTPClient: &http.Client{Transport: &testTransport{handler: handler}},
	})
	if err != nil {
		t.Fatalf("NewOllama() err = %v", err)
	}

	var streamed strings.Builder
	var toolChunks []agent.ToolCall
	resp, err := client.Stream(context.Background(), []agent.Message{{Role: "user", Content: "hi"}}, nil, func(chunk agent.StreamChunk) error {
		if chunk.Text != "" {
			streamed.WriteString(chunk.Text)
		}
		if chunk.ToolCall != nil {
			toolChunks = append(toolChunks, *chunk.ToolCall)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Stream() err = %v", err)
	}
	if !seen.Stream {
		t.Fatalf("stream = false, want true")
	}
	if streamed.String() != "hello" || resp.Content != "hello" {
		t.Fatalf("stream/content = %q/%q, want hello", streamed.String(), resp.Content)
	}
	if len(resp.ToolCalls) != 1 || len(toolChunks) != 1 || resp.ToolCalls[0].Name != "exec" {
		t.Fatalf("tool calls resp=%#v chunks=%#v", resp.ToolCalls, toolChunks)
	}
	if resp.Usage.TotalTokens != 5 {
		t.Fatalf("usage = %+v, want total 5", resp.Usage)
	}
}
