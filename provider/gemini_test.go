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

func TestGeminiCompleteMapsRequestResponseAndProviderState(t *testing.T) {
	var (
		seenPath   string
		seenQuery  string
		seenAPIKey string
		seen       geminiRequest
	)
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenPath = r.URL.Path
		seenQuery = r.URL.RawQuery
		seenAPIKey = r.Header.Get("x-goog-api-key")
		if err := json.NewDecoder(r.Body).Decode(&seen); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		_ = json.NewEncoder(w).Encode(geminiResponse{
			Candidates: []geminiCandidate{{
				Content: geminiContent{
					Role: "model",
					Parts: []geminiPart{
						{Text: "done"},
						{FunctionCall: &geminiFunctionCall{Name: "exec", Args: json.RawMessage(`{"cmd":"pwd"}`)}},
					},
				},
			}},
			Usage: geminiUsage{PromptTokenCount: 9, CandidatesTokenCount: 4, TotalTokenCount: 13, CachedContentTokens: 2},
		})
	})

	client, err := NewGemini(GeminiOptions{
		APIKey:     "gemini-key",
		BaseURL:    "https://gemini.test/v1beta",
		Model:      "gemini-test",
		MaxTokens:  2048,
		HTTPClient: &http.Client{Transport: &testTransport{handler: handler}},
		UserAgent:  "aphelion-test",
	})
	if err != nil {
		t.Fatalf("NewGemini() err = %v", err)
	}

	providerState := json.RawMessage(`{"role":"model","parts":[{"text":"internal","thought":true,"thoughtSignature":"sig-1"},{"functionCall":{"name":"exec","args":{"cmd":"ls"}}}]}`)
	resp, err := client.Complete(context.Background(), []agent.Message{
		{Role: "system", SystemBlocks: []agent.SystemBlock{{Text: "system instructions"}}},
		{Role: "user", Content: "read this", Media: []core.Media{{
			Type:     "photo",
			Data:     []byte("image-bytes"),
			MimeType: "image/jpeg",
		}}},
		{Role: "assistant", ProviderState: providerState},
		{Role: "tool", ToolName: "exec", ToolCallID: "gemini_call_1", Content: "stdout"},
	}, []agent.ToolDef{{
		Name:        "exec",
		Description: "Run a command",
		Parameters:  json.RawMessage(`{"type":"object"}`),
	}})
	if err != nil {
		t.Fatalf("Complete() err = %v", err)
	}
	if seenPath != "/v1beta/models/gemini-test:generateContent" {
		t.Fatalf("path = %q", seenPath)
	}
	if strings.Contains(seenQuery, "key=") {
		t.Fatalf("query = %q, did not want api key in URL", seenQuery)
	}
	if seenAPIKey != "gemini-key" {
		t.Fatalf("x-goog-api-key = %q, want configured key", seenAPIKey)
	}
	if seen.SystemInstruction == nil || len(seen.SystemInstruction.Parts) != 1 || seen.SystemInstruction.Parts[0].Text != "system instructions" {
		t.Fatalf("system instruction = %#v", seen.SystemInstruction)
	}
	if len(seen.Tools) != 1 || len(seen.Tools[0].FunctionDeclarations) != 1 || seen.Tools[0].FunctionDeclarations[0].Name != "exec" {
		t.Fatalf("tools = %#v", seen.Tools)
	}
	if seen.GenerationConfig.MaxOutputTokens != 2048 {
		t.Fatalf("max output tokens = %d", seen.GenerationConfig.MaxOutputTokens)
	}
	if len(seen.Contents) != 3 {
		t.Fatalf("contents = %#v", seen.Contents)
	}
	if seen.Contents[0].Role != "user" || len(seen.Contents[0].Parts) != 2 || seen.Contents[0].Parts[0].InlineData == nil {
		t.Fatalf("user content = %#v", seen.Contents[0])
	}
	if !seen.Contents[1].Parts[0].Thought || seen.Contents[1].Parts[0].ThoughtSignature != "sig-1" {
		t.Fatalf("provider state content = %#v", seen.Contents[1])
	}
	if seen.Contents[2].Role != "function" || seen.Contents[2].Parts[0].FunctionResponse == nil || seen.Contents[2].Parts[0].FunctionResponse.Name != "exec" {
		t.Fatalf("tool response content = %#v", seen.Contents[2])
	}
	if resp.Content != "done" {
		t.Fatalf("content = %q, want done", resp.Content)
	}
	if len(resp.ToolCalls) != 1 || resp.ToolCalls[0].Name != "exec" || string(resp.ToolCalls[0].Input) != `{"cmd":"pwd"}` {
		t.Fatalf("tool calls = %#v", resp.ToolCalls)
	}
	if len(resp.ProviderState) == 0 {
		t.Fatalf("provider state = empty")
	}
	if resp.Usage.InputTokens != 9 || resp.Usage.OutputTokens != 4 || resp.Usage.TotalTokens != 13 || resp.Usage.CacheReadTokens != 2 {
		t.Fatalf("usage = %+v", resp.Usage)
	}
}

func TestGeminiStreamMapsTextToolCallsAndUsage(t *testing.T) {
	var seenPath string
	var seenQuery string
	var seenAPIKey string
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenPath = r.URL.Path
		seenQuery = r.URL.RawQuery
		seenAPIKey = r.Header.Get("x-goog-api-key")
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(`data: {"candidates":[{"content":{"role":"model","parts":[{"text":"he"}]}}]}` + "\n\n"))
		_, _ = w.Write([]byte(`data: {"candidates":[{"content":{"role":"model","parts":[{"text":"llo"},{"functionCall":{"name":"exec","args":{"cmd":"pwd"}}}]}}],"usageMetadata":{"promptTokenCount":3,"candidatesTokenCount":2,"totalTokenCount":5}}` + "\n\n"))
	})

	client, err := NewGemini(GeminiOptions{
		APIKey:     "gemini-key",
		BaseURL:    "https://gemini.test/v1beta",
		Model:      "gemini-test",
		HTTPClient: &http.Client{Transport: &testTransport{handler: handler}},
	})
	if err != nil {
		t.Fatalf("NewGemini() err = %v", err)
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
	if seenPath != "/v1beta/models/gemini-test:streamGenerateContent" {
		t.Fatalf("path = %q", seenPath)
	}
	if strings.Contains(seenQuery, "key=") || !strings.Contains(seenQuery, "alt=sse") {
		t.Fatalf("query = %q, want alt=sse without api key", seenQuery)
	}
	if seenAPIKey != "gemini-key" {
		t.Fatalf("x-goog-api-key = %q, want configured key", seenAPIKey)
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
