//go:build linux

package provider

import (
	"encoding/json"
	"testing"

	"github.com/idolum-ai/aphelion/agent"
)

func TestProviderResponsesMapOutputLimitReasons(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		resp *agent.Response
	}{
		{
			name: "openrouter length",
			resp: mapOpenRouterResponse(openRouterResponse{
				Choices: []openRouterChoice{{
					Message:      openRouterResponseMessage{Content: json.RawMessage(`"partial"`)},
					FinishReason: "length",
				}},
			}),
		},
		{
			name: "openai responses incomplete",
			resp: mapOpenAIResponsesResponse(openAIResponsesResponse{
				Status:            "incomplete",
				IncompleteDetails: openAIResponsesIncompleteDetails{Reason: "max_output_tokens"},
				OutputText:        "partial",
			}),
		},
		{
			name: "anthropic max tokens",
			resp: mapAnthropicResponse(anthropicResponse{
				Content:    []anthropicContent{{Type: "text", Text: "partial"}},
				StopReason: "max_tokens",
			}, agent.ReasoningSummaryNone),
		},
		{
			name: "gemini max tokens",
			resp: mapGeminiResponse(geminiResponse{
				Candidates: []geminiCandidate{{
					Content:      geminiContent{Role: "model", Parts: []geminiPart{{Text: "partial"}}},
					FinishReason: "MAX_TOKENS",
				}},
			}),
		},
		{
			name: "ollama num predict",
			resp: mapOllamaResponse(ollamaResponse{
				Message:    ollamaMessage{Role: "assistant", Content: "partial"},
				DoneReason: "num_predict",
			}),
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if !agent.ResponseOutputLimitHit(tc.resp) {
				t.Fatalf("ResponseOutputLimitHit(%+v) = false, want true", tc.resp)
			}
		})
	}
}
