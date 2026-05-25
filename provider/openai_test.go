//go:build linux

package provider

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/idolum-ai/aphelion/agent"
	"github.com/idolum-ai/aphelion/core"
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
	if seen.ServiceTier != "" {
		t.Fatalf("service_tier = %q, want omitted standard tier", seen.ServiceTier)
	}
}

func TestOpenAICompleteIncludesPriorityServiceTier(t *testing.T) {
	var seen openAIRequest
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&seen); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		_ = json.NewEncoder(w).Encode(openRouterResponse{
			Choices: []struct {
				Message openRouterResponseMessage `json:"message"`
			}{{Message: openRouterResponseMessage{Content: json.RawMessage(`"fast"`)}}},
		})
	})

	client, err := NewOpenAI(OpenAIOptions{
		APIKey:      "test-key",
		Model:       "gpt-5.4",
		ServiceTier: "fast",
		Transport:   core.ModelTransportOpenAIChat,
		HTTPClient:  &http.Client{Transport: &testTransport{handler: handler}},
	})
	if err != nil {
		t.Fatalf("NewOpenAI() err = %v", err)
	}
	if _, err := client.Complete(context.Background(), []agent.Message{{Role: "user", Content: "hi"}}, nil); err != nil {
		t.Fatalf("Complete() err = %v", err)
	}
	if seen.ServiceTier != core.ModelServiceTierPriority {
		t.Fatalf("service_tier = %q, want priority", seen.ServiceTier)
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
		Verbosity: agent.VerbosityLow,
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
	if !seen.ParallelToolCalls {
		t.Fatal("parallel_tool_calls = false, want true when Responses tools are present")
	}
	if seen.Reasoning["effort"] != "medium" {
		t.Fatalf("reasoning = %#v, want medium effort", seen.Reasoning)
	}
	if seen.Text == nil || seen.Text.Verbosity != "low" {
		t.Fatalf("text config = %#v, want low verbosity", seen.Text)
	}
	if seen.ServiceTier != "" {
		t.Fatalf("service_tier = %q, want omitted standard tier", seen.ServiceTier)
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

func TestOpenAIResponsesIncludesPriorityServiceTier(t *testing.T) {
	var seen openAIResponsesRequest
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&seen); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		_ = json.NewEncoder(w).Encode(openAIResponsesResponse{
			Output: []openAIResponsesOutputItem{{
				Type: "message",
				Content: []struct {
					Type string `json:"type"`
					Text string `json:"text"`
				}{{Type: "output_text", Text: "fast responses"}},
			}},
		})
	})

	client, err := NewOpenAI(OpenAIOptions{
		APIKey:      "test-key",
		Model:       "gpt-5.5",
		ServiceTier: "priority",
		Transport:   core.ModelTransportOpenAIResponses,
		HTTPClient:  &http.Client{Transport: &testTransport{handler: handler}},
	})
	if err != nil {
		t.Fatalf("NewOpenAI() err = %v", err)
	}
	if _, err := client.Complete(context.Background(), []agent.Message{{Role: "user", Content: "hi"}}, nil); err != nil {
		t.Fatalf("Complete() err = %v", err)
	}
	if seen.ServiceTier != core.ModelServiceTierPriority {
		t.Fatalf("service_tier = %q, want priority", seen.ServiceTier)
	}
}

func TestOpenAICompleteWithVerbosityOnlyUsesResponsesAPI(t *testing.T) {
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
			Output: []openAIResponsesOutputItem{{
				Type: "message",
				Content: []struct {
					Type string `json:"type"`
					Text string `json:"text"`
				}{{Type: "output_text", Text: "brief answer"}},
			}},
			Usage: openAIResponsesUsage{InputTokens: 5, OutputTokens: 2, TotalTokens: 7},
		})
	})

	client, err := NewOpenAI(OpenAIOptions{
		APIKey:     "test-key",
		Model:      "gpt-5.5",
		MaxTokens:  128,
		HTTPClient: &http.Client{Transport: &testTransport{handler: handler}},
	})
	if err != nil {
		t.Fatalf("NewOpenAI() err = %v", err)
	}

	resp, err := client.CompleteWithOptions(context.Background(), []agent.Message{
		{Role: "system", Content: "answer concisely"},
		{Role: "user", Content: "hi"},
	}, nil, agent.CompleteOptions{Verbosity: agent.VerbosityLow})
	if err != nil {
		t.Fatalf("CompleteWithOptions() err = %v", err)
	}
	if seenPath != "/v1/responses" {
		t.Fatalf("path = %q, want /v1/responses", seenPath)
	}
	if seen.Model != "gpt-5.5" || seen.MaxOutputTokens != 128 {
		t.Fatalf("request model/tokens = %q/%d, want gpt-5.5/128", seen.Model, seen.MaxOutputTokens)
	}
	if seen.Instructions != "answer concisely" {
		t.Fatalf("instructions = %q, want answer concisely", seen.Instructions)
	}
	if seen.Reasoning != nil {
		t.Fatalf("reasoning = %#v, want nil for verbosity-only request", seen.Reasoning)
	}
	if len(seen.Tools) != 0 || seen.ToolChoice != "" {
		t.Fatalf("tools/tool_choice = %#v/%q, want none", seen.Tools, seen.ToolChoice)
	}
	if seen.Text == nil || seen.Text.Verbosity != "low" {
		t.Fatalf("text config = %#v, want low verbosity", seen.Text)
	}
	if resp.Content != "brief answer" {
		t.Fatalf("content = %q, want brief answer", resp.Content)
	}
	if resp.Usage.InputTokens != 5 || resp.Usage.OutputTokens != 2 || resp.Usage.TotalTokens != 7 {
		t.Fatalf("usage = %+v, want 5/2/7", resp.Usage)
	}
}

func TestOpenAIResponsesMapsImageGenerationCallToMedia(t *testing.T) {
	png := "iVBORw0KGgo="
	resp := mapOpenAIResponsesResponse(openAIResponsesResponse{
		Output: []openAIResponsesOutputItem{
			{
				Type: "message",
				Content: []struct {
					Type string `json:"type"`
					Text string `json:"text"`
				}{{Type: "output_text", Text: "Draft generated."}},
			},
			{
				Type:          "image_generation_call",
				ID:            "ig_123",
				Status:        "completed",
				RevisedPrompt: "A phosphor gate turns telemetry into care.",
				Result:        png,
			},
		},
	})

	if resp.Content != "Draft generated." {
		t.Fatalf("content = %q", resp.Content)
	}
	if len(resp.Media) != 1 {
		t.Fatalf("media len = %d, want 1", len(resp.Media))
	}
	media := resp.Media[0]
	if media.Type != "image" || media.MimeType != "image/png" || media.Filename != "image-generation-call-ig_123.png" {
		t.Fatalf("media metadata = %#v", media)
	}
	if string(media.Data) != string([]byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}) {
		t.Fatalf("media bytes = %v, want PNG signature", media.Data)
	}
}

func TestOpenAIStreamWithOptionsUsesResponsesAPI(t *testing.T) {
	var (
		seenPath string
		seen     openAIResponsesRequest
	)
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenPath = r.URL.Path
		if err := json.NewDecoder(r.Body).Decode(&seen); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, strings.Join([]string{
			"event: response.output_text.delta",
			`data: {"type":"response.output_text.delta","delta":"hel"}`,
			"",
			"event: response.output_text.delta",
			`data: {"type":"response.output_text.delta","delta":"lo"}`,
			"",
			"event: response.completed",
			`data: {"type":"response.completed","response":{"output_text":"hello","usage":{"input_tokens":3,"output_tokens":2,"total_tokens":5,"input_tokens_details":{"cached_tokens":1,"cache_write_tokens":2}}}}`,
			"",
		}, "\n"))
	})

	client, err := NewOpenAI(OpenAIOptions{
		APIKey:     "test-key",
		Model:      "gpt-5.5",
		MaxTokens:  256,
		Transport:  core.ModelTransportOpenAIResponses,
		HTTPClient: &http.Client{Transport: &testTransport{handler: handler}},
	})
	if err != nil {
		t.Fatalf("NewOpenAI() err = %v", err)
	}

	var chunks []string
	resp, err := client.StreamWithOptions(context.Background(), []agent.Message{
		{Role: "system", Content: "system instructions"},
		{Role: "user", Content: "hi"},
	}, nil, agent.CompleteOptions{
		Reasoning: agent.ReasoningConfig{Effort: agent.ReasoningEffortHigh, Summary: agent.ReasoningSummaryAuto},
		Verbosity: agent.VerbosityHigh,
	}, func(chunk agent.StreamChunk) error {
		chunks = append(chunks, chunk.Text)
		return nil
	})
	if err != nil {
		t.Fatalf("StreamWithOptions() err = %v", err)
	}
	if seenPath != "/v1/responses" {
		t.Fatalf("path = %q, want /v1/responses", seenPath)
	}
	if !seen.Stream {
		t.Fatalf("stream flag = false, want true")
	}
	if seen.Store == nil || *seen.Store {
		t.Fatalf("store = %#v, want false", seen.Store)
	}
	if seen.Reasoning["effort"] != "high" || seen.Reasoning["summary"] != "auto" {
		t.Fatalf("reasoning = %#v, want high/auto", seen.Reasoning)
	}
	if seen.Text == nil || seen.Text.Verbosity != "high" {
		t.Fatalf("text config = %#v, want high verbosity", seen.Text)
	}
	if strings.Join(chunks, "") != "hello" || resp.Content != "hello" {
		t.Fatalf("chunks/content = %#v/%q, want hello", chunks, resp.Content)
	}
	if resp.Usage.InputTokens != 3 || resp.Usage.OutputTokens != 2 || resp.Usage.TotalTokens != 5 {
		t.Fatalf("usage = %+v, want 3/2/5", resp.Usage)
	}
	if resp.Usage.CacheReadTokens != 1 || resp.Usage.CacheWriteTokens != 2 {
		t.Fatalf("cache usage = %+v, want read=1 write=2", resp.Usage)
	}
}

func TestOpenAIStreamWithOptionsMapsImageGenerationCallToMedia(t *testing.T) {
	png := "iVBORw0KGgo="
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, strings.Join([]string{
			"event: response.output_text.delta",
			`data: {"type":"response.output_text.delta","delta":"Draft generated."}`,
			"",
			"event: response.output_item.done",
			`data: {"type":"response.output_item.done","item":{"type":"image_generation_call","id":"ig_stream","status":"completed","result":"` + png + `"}}`,
			"",
			"event: response.completed",
			`data: {"type":"response.completed","response":{"id":"resp1"}}`,
			"",
		}, "\n"))
	})

	client, err := NewOpenAI(OpenAIOptions{
		APIKey:     "test-key",
		Model:      "gpt-5.5",
		Transport:  core.ModelTransportOpenAIResponses,
		HTTPClient: &http.Client{Transport: &testTransport{handler: handler}},
	})
	if err != nil {
		t.Fatalf("NewOpenAI() err = %v", err)
	}

	resp, err := client.StreamWithOptions(context.Background(), []agent.Message{{Role: "user", Content: "make image"}}, nil, agent.CompleteOptions{}, nil)
	if err != nil {
		t.Fatalf("StreamWithOptions() err = %v", err)
	}
	if resp.Content != "Draft generated." {
		t.Fatalf("content = %q", resp.Content)
	}
	if len(resp.Media) != 1 {
		t.Fatalf("media len = %d, want 1", len(resp.Media))
	}
	media := resp.Media[0]
	if media.Type != "image" || media.MimeType != "image/png" || media.Filename != "image-generation-call-ig_stream.png" {
		t.Fatalf("media metadata = %#v", media)
	}
}

func TestOpenAIStreamUsesChatCompletionsTransport(t *testing.T) {
	var (
		seenPath string
		seen     openAIRequest
	)
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenPath = r.URL.Path
		if err := json.NewDecoder(r.Body).Decode(&seen); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, strings.Join([]string{
			`data: {"choices":[{"delta":{"content":"he"}}]}`,
			"",
			`data: {"choices":[{"delta":{"content":"llo"}}]}`,
			"",
			`data: {"usage":{"prompt_tokens":4,"completion_tokens":2,"total_tokens":6,"prompt_tokens_details":{"cached_tokens":1}}}`,
			"",
			"data: [DONE]",
			"",
		}, "\n"))
	})

	client, err := NewOpenAI(OpenAIOptions{
		APIKey:     "test-key",
		Model:      "gpt-5.4",
		Transport:  core.ModelTransportOpenAIChat,
		HTTPClient: &http.Client{Transport: &testTransport{handler: handler}},
	})
	if err != nil {
		t.Fatalf("NewOpenAI() err = %v", err)
	}

	var chunks []string
	resp, err := client.Stream(context.Background(), []agent.Message{{Role: "user", Content: "hi"}}, nil, func(chunk agent.StreamChunk) error {
		chunks = append(chunks, chunk.Text)
		return nil
	})
	if err != nil {
		t.Fatalf("Stream() err = %v", err)
	}
	if seenPath != "/v1/chat/completions" {
		t.Fatalf("path = %q, want /v1/chat/completions", seenPath)
	}
	if !seen.Stream || seen.StreamOptions == nil || !seen.StreamOptions.IncludeUsage {
		t.Fatalf("stream options = stream:%v options:%#v, want stream with usage", seen.Stream, seen.StreamOptions)
	}
	if strings.Join(chunks, "") != "hello" || resp.Content != "hello" {
		t.Fatalf("chunks/content = %#v/%q, want hello", chunks, resp.Content)
	}
	if resp.Usage.InputTokens != 4 || resp.Usage.OutputTokens != 2 || resp.Usage.TotalTokens != 6 {
		t.Fatalf("usage = %+v, want 4/2/6", resp.Usage)
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
