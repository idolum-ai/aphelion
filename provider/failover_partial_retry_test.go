//go:build linux

package provider

import (
	"context"
	"errors"
	"github.com/idolum-ai/aphelion/agent"
	"github.com/idolum-ai/aphelion/core"
	"strings"
	"testing"
)

func TestFailoverChainPassesPartialProviderEvidenceToFallback(t *testing.T) {
	primary := &stubChainProvider{err: stubPartialProviderError{
		msg:        "codex: incomplete response without stored-response continuation",
		responseID: "resp-partial",
		reason:     "without stored-response continuation",
		partial: &agent.Response{
			Content: "partial synthesis from codex",
			ToolCalls: []agent.ToolCall{{
				ID:    "call-1",
				Name:  "exec",
				Input: []byte(`{"command":"git diff"}`),
			}},
		},
	}}
	secondary := &messageContentAssertingProvider{
		reply:    "fallback used partial evidence",
		required: "partial synthesis from codex",
	}

	chain, err := NewFailoverChain([]NamedProvider{
		{Name: "codex", Provider: primary},
		{Name: "native", Provider: secondary},
	})
	if err != nil {
		t.Fatalf("NewFailoverChain() err = %v", err)
	}

	resp, err := chain.CompleteManaged(context.Background(), []agent.Message{{Role: "user", Content: "finish"}}, nil, agent.CompleteOptions{})
	if err != nil {
		t.Fatalf("CompleteManaged() err = %v", err)
	}
	if resp.Content != "fallback used partial evidence" {
		t.Fatalf("content = %q, want fallback used partial evidence", resp.Content)
	}
	if secondary.callCount != 1 {
		t.Fatalf("secondary.callCount = %d, want 1", secondary.callCount)
	}
	if !providerEventsContain(resp.ProviderEvents, core.ExecutionEventProviderPartial) {
		t.Fatalf("provider events = %#v, want partial event", resp.ProviderEvents)
	}
	var partialEvent core.ProviderEvent
	for _, event := range resp.ProviderEvents {
		if event.EventType == core.ExecutionEventProviderPartial {
			partialEvent = event
			break
		}
	}
	if partialEvent.ResponseID != "resp-partial" || partialEvent.PartialContentChars == 0 || partialEvent.PartialToolCalls != 1 {
		t.Fatalf("partial event = %#v, want response id/content/tool count", partialEvent)
	}
}

func TestFailoverChainStreamSkipsOpenAIFamilyAndCompactsAfterContextWindowError(t *testing.T) {
	openAI := &stubChainProvider{err: stubStatusError{code: 400, msg: "codex: stream failed: Your input exceeds the context window of this model"}}
	openAIFallback := &stubChainProvider{reply: "should not run"}
	anthropic := &toolHistoryAssertingProvider{
		reply:               "anthropic compact streamed synthesis",
		requiredToolContent: "important tail evidence",
		maxToolContent:      providerFallbackRecentToolChars + 300,
	}

	chain, err := NewFailoverChain([]NamedProvider{
		{Name: "openai:gpt-5.5", Provider: openAI},
		{Name: "openai:gpt-5.4", Provider: openAIFallback},
		{Name: "anthropic", Provider: anthropic},
	})
	if err != nil {
		t.Fatalf("NewFailoverChain() err = %v", err)
	}

	largeToolOutput := strings.Repeat("large output\n", 9000) + "important tail evidence"
	messages := []agent.Message{
		{Role: "assistant", ToolCalls: []agent.ToolCall{{ID: "call-1", Name: "exec", Input: []byte(`{"cmd":"git diff"}`)}}},
		{Role: "tool", ToolCallID: "call-1", ToolName: "exec", Content: largeToolOutput},
	}
	var streamed strings.Builder
	resp, err := chain.Stream(context.Background(), messages, []agent.ToolDef{{Name: "exec"}}, func(chunk agent.StreamChunk) error {
		streamed.WriteString(chunk.Text)
		return nil
	})
	if err != nil {
		t.Fatalf("Stream() err = %v", err)
	}
	if resp.Content != "anthropic compact streamed synthesis" || streamed.String() != "anthropic compact streamed synthesis" {
		t.Fatalf("content/stream = %q/%q, want compact streamed synthesis", resp.Content, streamed.String())
	}
	if openAIFallback.callCount != 0 {
		t.Fatalf("openAIFallback.callCount = %d, want OpenAI family skipped after streamed context-window failure", openAIFallback.callCount)
	}
	if anthropic.callCount != 1 {
		t.Fatalf("anthropic.callCount = %d, want one compact fallback synthesis", anthropic.callCount)
	}
	if !providerEventsContain(resp.ProviderEvents, core.ExecutionEventProviderFailoverEngaged) {
		t.Fatalf("provider events = %#v, want failover engaged", resp.ProviderEvents)
	}
}

func TestFailoverChainUsesOpenRouterWhenAnthropicAlsoFailsAfterToolResultRejection(t *testing.T) {
	openAI := &stubChainProvider{err: stubStatusError{code: 422, msg: "openai: status 422: rejected tool_call response"}}
	anthropic := &stubChainProvider{err: stubStatusError{code: 503, msg: "anthropic overloaded"}}
	openRouter := &stubChainProvider{reply: "openrouter final synthesis"}

	chain, err := NewFailoverChain([]NamedProvider{
		{Name: "openai:gpt-5.5", Provider: openAI},
		{Name: "anthropic", Provider: anthropic},
		{Name: "openrouter", Provider: openRouter},
	})
	if err != nil {
		t.Fatalf("NewFailoverChain() err = %v", err)
	}

	messages := []agent.Message{
		{Role: "assistant", ToolCalls: []agent.ToolCall{{ID: "call-1", Name: "exec", Input: []byte(`{"cmd":"git diff"}`)}}},
		{Role: "tool", ToolCallID: "call-1", ToolName: "exec", Content: "diff output"},
	}
	resp, err := chain.CompleteManaged(context.Background(), messages, []agent.ToolDef{{Name: "exec"}}, agent.CompleteOptions{})
	if err != nil {
		t.Fatalf("CompleteManaged() err = %v", err)
	}
	if resp.Content != "openrouter final synthesis" {
		t.Fatalf("content = %q, want openrouter final synthesis", resp.Content)
	}
	if anthropic.callCount == 0 || openRouter.callCount == 0 {
		t.Fatalf("call counts anthropic=%d openrouter=%d, want both after OpenAI rejection", anthropic.callCount, openRouter.callCount)
	}
}

func TestRunTurnSynthesizesWithAnthropicAfterOpenAIToolResultRejection(t *testing.T) {
	openAI := &openAIToolResultRejectingProvider{}
	anthropic := &toolHistoryAssertingProvider{
		reply:               "anthropic synthesized tool evidence",
		requiredToolContent: "stdout: clean",
	}
	chain, err := NewFailoverChain([]NamedProvider{
		{Name: "openai:gpt-5.5", Provider: openAI},
		{Name: "anthropic", Provider: anthropic},
	})
	if err != nil {
		t.Fatalf("NewFailoverChain() err = %v", err)
	}
	tools := &fixedToolRegistry{output: "stdout: clean"}

	result, history, err := agent.RunTurn(context.Background(), chain, tools, &agent.Budget{Max: 4, Caution: 0.7, Warning: 0.9}, nil, []agent.Message{{Role: "user", Content: "inspect the repo"}})
	if err != nil {
		t.Fatalf("RunTurn() err = %v", err)
	}
	if result.Text != "anthropic synthesized tool evidence" {
		t.Fatalf("result text = %q, want anthropic synthesis", result.Text)
	}
	if openAI.callCount != 2 {
		t.Fatalf("openAI.callCount = %d, want initial tool call and post-tool rejection", openAI.callCount)
	}
	if anthropic.callCount != 1 {
		t.Fatalf("anthropic.callCount = %d, want one final synthesis attempt", anthropic.callCount)
	}
	if tools.callCount != 1 {
		t.Fatalf("tool call count = %d, want one tool execution", tools.callCount)
	}
	if !providerEventsContain(result.ProviderEvents, core.ExecutionEventProviderFailoverEngaged) {
		t.Fatalf("provider events = %#v, want failover engaged", result.ProviderEvents)
	}
	foundToolEvidence := false
	for _, msg := range history {
		if strings.EqualFold(strings.TrimSpace(msg.Role), "tool") && strings.Contains(msg.Content, "stdout: clean") {
			foundToolEvidence = true
			break
		}
	}
	if !foundToolEvidence {
		t.Fatalf("history = %#v, want preserved tool evidence", history)
	}
}

func TestRunTurnKeepsFallbackProviderForLaterCallsInSameTurn(t *testing.T) {
	openAI := &openAIToolContextWindowProvider{}
	anthropic := &anthropicTwoStepProvider{}
	chain, err := NewFailoverChain([]NamedProvider{
		{Name: "openai:gpt-5.5", Provider: openAI},
		{Name: "anthropic", Provider: anthropic},
	})
	if err != nil {
		t.Fatalf("NewFailoverChain() err = %v", err)
	}
	tools := &fixedToolRegistry{output: "stdout: clean"}

	result, _, err := agent.RunTurn(context.Background(), chain, tools, &agent.Budget{Max: 6, Caution: 0.7, Warning: 0.9}, nil, []agent.Message{{Role: "user", Content: "inspect the repo"}})
	if err != nil {
		t.Fatalf("RunTurn() err = %v", err)
	}
	if result.Text != "anthropic completed after its own tool call" {
		t.Fatalf("result text = %q, want anthropic final synthesis", result.Text)
	}
	if openAI.callCount != 2 {
		t.Fatalf("openAI.callCount = %d, want initial tool call and one post-tool context failure only", openAI.callCount)
	}
	if anthropic.callCount != 2 {
		t.Fatalf("anthropic.callCount = %d, want fallback synthesis and later sticky call", anthropic.callCount)
	}
	if tools.callCount != 2 {
		t.Fatalf("tool call count = %d, want both tool executions", tools.callCount)
	}
}

func TestFailoverChainFallsBackToAnthropicAfterOpenAIModelsUnavailable(t *testing.T) {
	primary := &stubChainProvider{err: stubStatusError{code: 404, msg: "model gpt-5.5 not found"}}
	secondary := &stubChainProvider{err: stubStatusError{code: 404, msg: "model gpt-5.4 not found"}}
	tertiary := &stubChainProvider{reply: "anthropic fallback"}

	chain, err := NewFailoverChain([]NamedProvider{
		{Name: "openai:gpt-5.5", Provider: primary},
		{Name: "openai:gpt-5.4", Provider: secondary},
		{Name: "anthropic", Provider: tertiary},
	})
	if err != nil {
		t.Fatalf("NewFailoverChain() err = %v", err)
	}

	resp, err := chain.CompleteManaged(context.Background(), []agent.Message{{Role: "user", Content: "hi"}}, nil, agent.CompleteOptions{})
	if err != nil {
		t.Fatalf("CompleteManaged() err = %v", err)
	}
	if resp.Content != "anthropic fallback" {
		t.Fatalf("content = %q, want anthropic fallback", resp.Content)
	}
	if tertiary.callCount == 0 {
		t.Fatal("anthropic fallback was not called")
	}
}

func TestFailoverChainExhaustedErrorHasUserFacingFailure(t *testing.T) {
	primary := &stubChainProvider{err: stubStatusError{code: 503, msg: "primary down"}}
	secondary := &stubChainProvider{err: stubStatusError{code: 503, msg: "secondary down"}}

	chain, err := NewFailoverChain([]NamedProvider{
		{Name: "anthropic", Provider: primary},
		{Name: "openrouter", Provider: secondary},
	})
	if err != nil {
		t.Fatalf("NewFailoverChain() err = %v", err)
	}

	_, err = chain.CompleteManaged(context.Background(), []agent.Message{{Role: "user", Content: "hi"}}, nil, agent.CompleteOptions{})
	if err == nil {
		t.Fatal("CompleteManaged() err = nil, want error")
	}
	var exhausted ExhaustedError
	if !errors.As(err, &exhausted) {
		t.Fatalf("err = %v, want ExhaustedError", err)
	}
	if got := exhausted.UserFacingFailure(); got == "" {
		t.Fatal("UserFacingFailure() = empty, want guidance")
	}
}

func TestFailoverChainFallsBackOnInterruptedStreamError(t *testing.T) {
	primary := &stubChainProvider{err: errors.New("codex: stream closed before response.completed")}
	secondary := &stubChainProvider{reply: "fallback after stream interruption"}

	chain, err := NewFailoverChain([]NamedProvider{
		{Name: "codex", Provider: primary},
		{Name: "native", Provider: secondary},
	})
	if err != nil {
		t.Fatalf("NewFailoverChain() err = %v", err)
	}

	resp, err := chain.CompleteManaged(context.Background(), []agent.Message{{Role: "user", Content: "hi"}}, nil, agent.CompleteOptions{})
	if err != nil {
		t.Fatalf("CompleteManaged() err = %v", err)
	}
	if resp.Content != "fallback after stream interruption" {
		t.Fatalf("content = %q, want fallback after stream interruption", resp.Content)
	}
	if secondary.callCount == 0 {
		t.Fatal("secondary provider was not called after interrupted stream error")
	}
}

func TestFailoverChainFallsBackOnCodexContinuationFailureWithoutRetryingPrimary(t *testing.T) {
	for _, errText := range []string{
		"codex: incomplete response without stored-response continuation",
		"codex: incomplete response missing response id",
		"codex: response remained incomplete after 3 continuation attempts",
	} {
		t.Run(errText, func(t *testing.T) {
			primary := &stubChainProvider{err: errors.New(errText)}
			secondary := &stubChainProvider{reply: "fallback after codex continuation failure"}

			chain, err := NewFailoverChain([]NamedProvider{
				{Name: "codex", Provider: primary},
				{Name: "native", Provider: secondary},
			})
			if err != nil {
				t.Fatalf("NewFailoverChain() err = %v", err)
			}

			resp, err := chain.CompleteManaged(context.Background(), []agent.Message{{Role: "user", Content: "hi"}}, nil, agent.CompleteOptions{})
			if err != nil {
				t.Fatalf("CompleteManaged() err = %v", err)
			}
			if resp.Content != "fallback after codex continuation failure" {
				t.Fatalf("content = %q, want fallback after codex continuation failure", resp.Content)
			}
			if primary.callCount != 1 {
				t.Fatalf("primary.callCount = %d, want 1 deterministic continuation failure attempt", primary.callCount)
			}
			if secondary.callCount == 0 {
				t.Fatal("secondary provider was not called after codex continuation failure")
			}
		})
	}
}

func TestIsRetryableProviderErrorTreatsInterruptedStreamsAsRetryable(t *testing.T) {
	if !isRetryableProviderError(errors.New("codex: stream closed before response.completed")) {
		t.Fatal("interrupted stream error not treated as retryable")
	}
	if !shouldFailoverOnError(errors.New("unexpected EOF while reading event stream")) {
		t.Fatal("unexpected EOF stream error not treated as failover-eligible")
	}
}

func TestShouldFailoverOnCodexContinuationFailureButNotRetrySameProvider(t *testing.T) {
	err := errors.New("codex: incomplete response without stored-response continuation")
	if isRetryableProviderError(err) {
		t.Fatal("codex continuation failure should not retry the same provider")
	}
	if !shouldFailoverOnError(err) {
		t.Fatal("codex continuation failure should fall over to the next provider")
	}
}
