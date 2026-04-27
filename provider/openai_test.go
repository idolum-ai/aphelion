//go:build linux

package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/idolum-ai/aphelion/agent"
)

func TestOpenAICompleteTextUsageAndReasoning(t *testing.T) {
	var seen openAIRequest
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
				{Message: openRouterResponseMessage{Content: json.RawMessage(`"hello from openai"`)}},
			},
			Usage: openRouterUsage{PromptTokens: 13, CompletionTokens: 8, TotalTokens: 21},
		})
	})

	client, err := NewOpenAI(OpenAIOptions{
		APIKey:     "test-key",
		Model:      "gpt-5.5",
		MaxTokens:  1024,
		HTTPClient: &http.Client{Transport: &testTransport{handler: handler}},
	})
	if err != nil {
		t.Fatalf("NewOpenAI() err = %v", err)
	}

	resp, err := client.CompleteWithOptions(context.Background(), []agent.Message{{Role: "user", Content: "hi"}}, nil, agent.CompleteOptions{
		Reasoning: agent.ReasoningConfig{Effort: agent.ReasoningEffortXHigh},
	})
	if err != nil {
		t.Fatalf("CompleteWithOptions() err = %v", err)
	}
	if resp.Content != "hello from openai" {
		t.Fatalf("content = %q, want hello from openai", resp.Content)
	}
	if resp.Usage.InputTokens != 13 || resp.Usage.OutputTokens != 8 || resp.Usage.TotalTokens != 21 {
		t.Fatalf("usage = %+v, want prompt=13 completion=8 total=21", resp.Usage)
	}
	if seen.Model != "gpt-5.5" {
		t.Fatalf("model = %q, want gpt-5.5", seen.Model)
	}
	if seen.MaxCompletionTokens != 1024 {
		t.Fatalf("max_completion_tokens = %d, want 1024", seen.MaxCompletionTokens)
	}
	if seen.ReasoningEffort != "xhigh" {
		t.Fatalf("reasoning_effort = %q, want xhigh", seen.ReasoningEffort)
	}
}

func TestOpenAICompleteMapsTools(t *testing.T) {
	var seen openAIRequest
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
		})
	})

	client, err := NewOpenAI(OpenAIOptions{
		APIKey:     "test-key",
		Model:      "gpt-5.5",
		HTTPClient: &http.Client{Transport: &testTransport{handler: handler}},
	})
	if err != nil {
		t.Fatalf("NewOpenAI() err = %v", err)
	}

	resp, err := client.Complete(context.Background(), []agent.Message{
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
	if len(seen.Tools) != 1 || seen.Tools[0].Function.Name != "exec" || seen.ToolChoice != "auto" {
		t.Fatalf("tools/tool_choice = %#v/%q", seen.Tools, seen.ToolChoice)
	}
	if len(resp.ToolCalls) != 1 || resp.ToolCalls[0].Name != "exec" {
		t.Fatalf("tool calls = %#v", resp.ToolCalls)
	}
}

func TestOpenAICompleteWithToolsAndReasoningUsesResponsesAPI(t *testing.T) {
	var (
		seenPath string
		seen     openAIResponsesRequest
	)
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenPath = r.URL.Path
		if err := json.NewDecoder(r.Body).Decode(&seen); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		_ = json.NewEncoder(w).Encode(openAIResponsesResponse{
			Output: []openAIResponsesOutputItem{
				{
					Type: "message",
					Content: []struct {
						Type string `json:"type"`
						Text string `json:"text"`
					}{{Type: "output_text", Text: "done"}},
				},
				{
					Type:      "function_call",
					CallID:    "call_2",
					Name:      "exec",
					Arguments: json.RawMessage(`"{\"command\":\"pwd\"}"`),
				},
			},
			Usage: openAIResponsesUsage{InputTokens: 13, OutputTokens: 8, TotalTokens: 21},
		})
	})

	client, err := NewOpenAI(OpenAIOptions{
		APIKey:     "test-key",
		Model:      "gpt-5.5",
		MaxTokens:  512,
		HTTPClient: &http.Client{Transport: &testTransport{handler: handler}},
	})
	if err != nil {
		t.Fatalf("NewOpenAI() err = %v", err)
	}

	resp, err := client.CompleteWithOptions(context.Background(), []agent.Message{
		{Role: "system", Content: "system instructions"},
		{Role: "assistant", ToolCalls: []agent.ToolCall{{
			ID:    "call_1",
			Name:  "exec",
			Input: json.RawMessage(`{"command":"ls"}`),
		}}},
		{Role: "tool", ToolCallID: "call_1", Content: "stdout"},
		{Role: "user", Content: "continue"},
	}, []agent.ToolDef{{
		Name:        "exec",
		Description: "Run a command",
		Parameters:  json.RawMessage(`{"type":"object"}`),
	}}, agent.CompleteOptions{
		Reasoning: agent.ReasoningConfig{Effort: agent.ReasoningEffortMedium},
	})
	if err != nil {
		t.Fatalf("CompleteWithOptions() err = %v", err)
	}
	if seenPath != "/v1/responses" {
		t.Fatalf("path = %q, want /v1/responses", seenPath)
	}
	if seen.Model != "gpt-5.5" || seen.MaxOutputTokens != 512 {
		t.Fatalf("request model/tokens = %q/%d, want gpt-5.5/512", seen.Model, seen.MaxOutputTokens)
	}
	if seen.Instructions != "system instructions" {
		t.Fatalf("instructions = %q, want system instructions", seen.Instructions)
	}
	if len(seen.Tools) != 1 || seen.Tools[0]["name"] != "exec" || seen.ToolChoice != "auto" {
		t.Fatalf("responses tools/tool_choice = %#v/%q", seen.Tools, seen.ToolChoice)
	}
	if seen.Reasoning["effort"] != "medium" {
		t.Fatalf("reasoning = %#v, want medium effort", seen.Reasoning)
	}
	if !responsesInputHasType(seen.Input, "function_call_output") {
		t.Fatalf("responses input = %#v, want function_call_output item", seen.Input)
	}
	if resp.Content != "done" {
		t.Fatalf("content = %q, want done", resp.Content)
	}
	if len(resp.ToolCalls) != 1 || resp.ToolCalls[0].ID != "call_2" || resp.ToolCalls[0].Name != "exec" || string(resp.ToolCalls[0].Input) != `{"command":"pwd"}` {
		t.Fatalf("tool calls = %#v, want normalized responses function call", resp.ToolCalls)
	}
	if resp.Usage.InputTokens != 13 || resp.Usage.OutputTokens != 8 || resp.Usage.TotalTokens != 21 {
		t.Fatalf("usage = %+v, want responses usage", resp.Usage)
	}
}

func responsesInputHasType(input []map[string]any, typ string) bool {
	for _, item := range input {
		if item["type"] == typ {
			return true
		}
	}
	return false
}
