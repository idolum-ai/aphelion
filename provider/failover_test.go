//go:build linux

package provider

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/idolum-ai/aphelion/agent"
	"github.com/idolum-ai/aphelion/core"
)

type stubStatusError struct {
	code int
	msg  string
}

func (e stubStatusError) Error() string   { return e.msg }
func (e stubStatusError) StatusCode() int { return e.code }

type stubChainProvider struct {
	reply      string
	err        error
	callCount  int
	streamText string
}

func (s *stubChainProvider) Complete(_ context.Context, _ []agent.Message, _ []agent.ToolDef) (*agent.Response, error) {
	s.callCount++
	if s.err != nil {
		return nil, s.err
	}
	return &agent.Response{
		Content: s.reply,
		Usage:   core.TokenUsage{InputTokens: 1, OutputTokens: 1, TotalTokens: 2},
	}, nil
}

func (s *stubChainProvider) Stream(_ context.Context, _ []agent.Message, _ []agent.ToolDef, cb agent.StreamCallback) (*agent.Response, error) {
	s.callCount++
	if s.err != nil {
		return nil, s.err
	}
	if cb != nil && s.streamText != "" {
		if err := cb(agent.StreamChunk{Type: "text", Text: s.streamText}); err != nil {
			return nil, err
		}
	}
	reply := s.reply
	if reply == "" {
		reply = s.streamText
	}
	return &agent.Response{Content: reply}, nil
}

type openAIToolResultRejectingProvider struct {
	callCount int
}

func (p *openAIToolResultRejectingProvider) Complete(_ context.Context, messages []agent.Message, _ []agent.ToolDef) (*agent.Response, error) {
	p.callCount++
	for _, msg := range messages {
		if strings.EqualFold(strings.TrimSpace(msg.Role), "tool") {
			return nil, stubStatusError{code: 400, msg: "openai: status 400: invalid_request_error: rejected tool_call response for call_id call-1"}
		}
	}
	return &agent.Response{ToolCalls: []agent.ToolCall{{
		ID:    "call-1",
		Name:  "exec",
		Input: []byte(`{"cmd":"git status --short"}`),
	}}}, nil
}

type toolHistoryAssertingProvider struct {
	reply               string
	requiredToolContent string
	maxToolContent      int
	callCount           int
}

func (p *toolHistoryAssertingProvider) Complete(_ context.Context, messages []agent.Message, _ []agent.ToolDef) (*agent.Response, error) {
	p.callCount++
	for _, msg := range messages {
		if p.maxToolContent > 0 && strings.EqualFold(strings.TrimSpace(msg.Role), "tool") && len(msg.Content) > p.maxToolContent {
			return nil, errors.New("tool evidence was not compacted")
		}
		if strings.EqualFold(strings.TrimSpace(msg.Role), "tool") && strings.Contains(msg.Content, p.requiredToolContent) {
			return &agent.Response{Content: p.reply}, nil
		}
	}
	return nil, errors.New("missing expected tool evidence")
}

type fixedToolRegistry struct {
	output    string
	callCount int
}

func (r *fixedToolRegistry) Definitions() []agent.ToolDef {
	return []agent.ToolDef{{Name: "exec"}}
}

func (r *fixedToolRegistry) Execute(_ context.Context, _ string, _ json.RawMessage) (string, error) {
	r.callCount++
	return r.output, nil
}

func TestFailoverChainFallsBackToSecondary(t *testing.T) {
	primary := &stubChainProvider{err: stubStatusError{code: 503, msg: "upstream unavailable"}}
	secondary := &stubChainProvider{reply: "fallback reply"}

	chain, err := NewFailoverChain([]NamedProvider{
		{Name: "anthropic", Provider: primary},
		{Name: "openrouter", Provider: secondary},
	})
	if err != nil {
		t.Fatalf("NewFailoverChain() err = %v", err)
	}

	resp, err := chain.CompleteManaged(context.Background(), []agent.Message{{Role: "user", Content: "hi"}}, nil, agent.CompleteOptions{})
	if err != nil {
		t.Fatalf("CompleteManaged() err = %v", err)
	}
	if resp.Content != "fallback reply" {
		t.Fatalf("content = %q, want fallback reply", resp.Content)
	}
	if primary.callCount == 0 || secondary.callCount == 0 {
		t.Fatalf("call counts primary=%d secondary=%d, want both called", primary.callCount, secondary.callCount)
	}
	if !providerEventsContain(resp.ProviderEvents, core.ExecutionEventProviderAttemptFailed) {
		t.Fatalf("provider events = %#v, want primary failure event", resp.ProviderEvents)
	}
	if !providerEventsContain(resp.ProviderEvents, core.ExecutionEventProviderFailoverEngaged) {
		t.Fatalf("provider events = %#v, want failover event", resp.ProviderEvents)
	}
}

func TestFailoverChainFallsBackOnProviderBufferLimitWithoutRetryingPrimary(t *testing.T) {
	primary := &stubChainProvider{err: stubStatusError{code: 507, msg: "codex: status 507 server_error: exceeded request buffer limit while retrying upstream"}}
	secondary := &stubChainProvider{reply: "fallback after buffer limit"}

	chain, err := NewFailoverChain([]NamedProvider{
		{Name: "codex", Provider: primary},
		{Name: "anthropic", Provider: secondary},
	})
	if err != nil {
		t.Fatalf("NewFailoverChain() err = %v", err)
	}

	resp, err := chain.CompleteManaged(context.Background(), []agent.Message{{Role: "user", Content: "hi"}}, nil, agent.CompleteOptions{})
	if err != nil {
		t.Fatalf("CompleteManaged() err = %v", err)
	}
	if resp.Content != "fallback after buffer limit" {
		t.Fatalf("content = %q, want buffer-limit fallback reply", resp.Content)
	}
	if primary.callCount != 1 {
		t.Fatalf("primary.callCount = %d, want no same-provider retry after buffer limit", primary.callCount)
	}
	if secondary.callCount != 1 {
		t.Fatalf("secondary.callCount = %d, want fallback provider called", secondary.callCount)
	}
	if providerEventsContain(resp.ProviderEvents, core.ExecutionEventProviderAttemptRetried) {
		t.Fatalf("provider events = %#v, want failover without same-provider retry", resp.ProviderEvents)
	}
	if !providerEventsContain(resp.ProviderEvents, core.ExecutionEventProviderFailoverEngaged) {
		t.Fatalf("provider events = %#v, want failover event", resp.ProviderEvents)
	}
}

func TestFailoverChainRecordsRetryEvents(t *testing.T) {
	primary := &stubChainProvider{err: stubStatusError{code: 503, msg: "upstream unavailable"}}
	secondary := &stubChainProvider{reply: "fallback reply"}

	chain, err := NewFailoverChain([]NamedProvider{
		{Name: "primary", Provider: primary},
		{Name: "secondary", Provider: secondary},
	})
	if err != nil {
		t.Fatalf("NewFailoverChain() err = %v", err)
	}

	resp, err := chain.CompleteManaged(context.Background(), []agent.Message{{Role: "user", Content: "hi"}}, nil, agent.CompleteOptions{})
	if err != nil {
		t.Fatalf("CompleteManaged() err = %v", err)
	}
	if !providerEventsContain(resp.ProviderEvents, core.ExecutionEventProviderAttemptRetried) {
		t.Fatalf("provider events = %#v, want retry event", resp.ProviderEvents)
	}
}

func providerEventsContain(events []core.ProviderEvent, eventType string) bool {
	for _, event := range events {
		if event.EventType == eventType {
			return true
		}
	}
	return false
}

func TestFailoverChainFallsBackOnForbidden(t *testing.T) {
	primary := &stubChainProvider{err: stubStatusError{code: 403, msg: "forbidden"}}
	secondary := &stubChainProvider{reply: "fallback reply"}

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
	if resp.Content != "fallback reply" {
		t.Fatalf("content = %q, want fallback reply", resp.Content)
	}
	if secondary.callCount == 0 {
		t.Fatal("secondary provider was not called after forbidden primary error")
	}
	state := chain.RuntimeState()
	if !state.FallbackActive {
		t.Fatalf("FallbackActive = false, want true")
	}
	if state.ActiveProvider != "native" {
		t.Fatalf("ActiveProvider = %q, want native", state.ActiveProvider)
	}
}

func TestFailoverChainDoesNotCascadeClientErrors(t *testing.T) {
	primary := &stubChainProvider{err: stubStatusError{code: 400, msg: "bad request"}}
	secondary := &stubChainProvider{reply: "should not run"}

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
	var statusErr stubStatusError
	if !errors.As(err, &statusErr) || statusErr.code != 400 {
		t.Fatalf("err = %v, want 400 status error", err)
	}
	if secondary.callCount != 0 {
		t.Fatalf("secondary.callCount = %d, want 0", secondary.callCount)
	}
}

func TestFailoverChainFallsBackBetweenOpenAIModelsOnModelUnavailable(t *testing.T) {
	primary := &stubChainProvider{err: stubStatusError{code: 404, msg: "model gpt-5.5 not found"}}
	secondary := &stubChainProvider{reply: "fallback openai model"}
	tertiary := &stubChainProvider{reply: "should not run"}

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
	if resp.Content != "fallback openai model" {
		t.Fatalf("content = %q, want fallback openai model", resp.Content)
	}
	if secondary.callCount == 0 {
		t.Fatal("secondary OpenAI model was not called")
	}
	if tertiary.callCount != 0 {
		t.Fatalf("tertiary.callCount = %d, want 0", tertiary.callCount)
	}
}

func TestFailoverChainDoesNotCascadeOpenAIClientErrorToAnthropic(t *testing.T) {
	primary := &stubChainProvider{err: stubStatusError{code: 400, msg: "bad request"}}
	secondary := &stubChainProvider{reply: "should not run"}

	chain, err := NewFailoverChain([]NamedProvider{
		{Name: "openai:gpt-5.5", Provider: primary},
		{Name: "anthropic", Provider: secondary},
	})
	if err != nil {
		t.Fatalf("NewFailoverChain() err = %v", err)
	}

	_, err = chain.CompleteManaged(context.Background(), []agent.Message{{Role: "user", Content: "hi"}}, nil, agent.CompleteOptions{})
	if err == nil {
		t.Fatal("CompleteManaged() err = nil, want error")
	}
	if secondary.callCount != 0 {
		t.Fatalf("secondary.callCount = %d, want 0", secondary.callCount)
	}
}

func TestFailoverChainSkipsOpenAIFamilyAfterToolResultRejection(t *testing.T) {
	primary := &stubChainProvider{err: stubStatusError{code: 400, msg: "openai: status 400: invalid_request_error: no tool output found for call_id call-1"}}
	openAIFallback := &stubChainProvider{reply: "should not run"}
	anthropic := &stubChainProvider{reply: "anthropic final synthesis"}

	chain, err := NewFailoverChain([]NamedProvider{
		{Name: "openai:gpt-5.5", Provider: primary},
		{Name: "openai:gpt-5.4", Provider: openAIFallback},
		{Name: "anthropic", Provider: anthropic},
	})
	if err != nil {
		t.Fatalf("NewFailoverChain() err = %v", err)
	}

	messages := []agent.Message{
		{Role: "user", Content: "inspect the repo"},
		{Role: "assistant", ToolCalls: []agent.ToolCall{{ID: "call-1", Name: "exec", Input: []byte(`{"cmd":"git status"}`)}}},
		{Role: "tool", ToolCallID: "call-1", ToolName: "exec", Content: "clean"},
	}
	resp, err := chain.CompleteManaged(context.Background(), messages, []agent.ToolDef{{Name: "exec"}}, agent.CompleteOptions{})
	if err != nil {
		t.Fatalf("CompleteManaged() err = %v", err)
	}
	if resp.Content != "anthropic final synthesis" {
		t.Fatalf("content = %q, want anthropic final synthesis", resp.Content)
	}
	if primary.callCount == 0 {
		t.Fatal("primary OpenAI provider was not called")
	}
	if openAIFallback.callCount != 0 {
		t.Fatalf("openAIFallback.callCount = %d, want 0 after tool-result rejection", openAIFallback.callCount)
	}
	if anthropic.callCount == 0 {
		t.Fatal("anthropic provider was not called")
	}
	if !providerEventsContain(resp.ProviderEvents, core.ExecutionEventProviderFailoverEngaged) {
		t.Fatalf("provider events = %#v, want failover engaged", resp.ProviderEvents)
	}
}

func TestFailoverChainSkipsOpenAIFamilyAndCompactsAfterContextWindowError(t *testing.T) {
	openAI := &stubChainProvider{err: stubStatusError{code: 400, msg: "codex: stream failed: Your input exceeds the context window of this model"}}
	openAIFallback := &stubChainProvider{reply: "should not run"}
	anthropic := &toolHistoryAssertingProvider{
		reply:               "anthropic compact synthesis",
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
	resp, err := chain.CompleteManaged(context.Background(), messages, []agent.ToolDef{{Name: "exec"}}, agent.CompleteOptions{})
	if err != nil {
		t.Fatalf("CompleteManaged() err = %v", err)
	}
	if resp.Content != "anthropic compact synthesis" {
		t.Fatalf("content = %q, want anthropic compact synthesis", resp.Content)
	}
	if openAIFallback.callCount != 0 {
		t.Fatalf("openAIFallback.callCount = %d, want OpenAI family skipped after context-window failure", openAIFallback.callCount)
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
