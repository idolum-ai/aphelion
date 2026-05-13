//go:build linux

package provider

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/idolum-ai/aphelion/agent"
	"github.com/idolum-ai/aphelion/core"
	"strings"
	"testing"
	"time"
)

type stubStatusError struct {
	code int
	msg  string
}

func (e stubStatusError) Error() string { return e.msg }

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

type openAIToolContextWindowProvider struct {
	callCount int
}

func (p *openAIToolContextWindowProvider) Complete(_ context.Context, messages []agent.Message, _ []agent.ToolDef) (*agent.Response, error) {
	p.callCount++
	for _, msg := range messages {
		if strings.EqualFold(strings.TrimSpace(msg.Role), "tool") {
			return nil, stubStatusError{code: 400, msg: "openai: status 400: invalid_request_error: context window exceeded after tool results"}
		}
	}
	return &agent.Response{ToolCalls: []agent.ToolCall{{
		ID:    "call-1",
		Name:  "exec",
		Input: []byte(`{"cmd":"git status --short"}`),
	}}}, nil
}

type anthropicTwoStepProvider struct {
	callCount int
}

func (p *anthropicTwoStepProvider) Complete(_ context.Context, _ []agent.Message, _ []agent.ToolDef) (*agent.Response, error) {
	p.callCount++
	if p.callCount == 1 {
		return &agent.Response{ToolCalls: []agent.ToolCall{{
			ID:    "call-2",
			Name:  "exec",
			Input: []byte(`{"cmd":"git diff --stat"}`),
		}}}, nil
	}
	return &agent.Response{Content: "anthropic completed after its own tool call"}, nil
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

type messageContentAssertingProvider struct {
	reply     string
	required  string
	callCount int
}

func (p *messageContentAssertingProvider) Complete(_ context.Context, messages []agent.Message, _ []agent.ToolDef) (*agent.Response, error) {
	p.callCount++
	for _, msg := range messages {
		if strings.Contains(msg.Content, p.required) {
			return &agent.Response{Content: p.reply}, nil
		}
	}
	return nil, errors.New("missing expected recovery note")
}

type stubPartialProviderError struct {
	msg        string
	responseID string
	partial    *agent.Response
	reason     string
}

func (e stubPartialProviderError) Error() string {
	return e.msg
}

func (e stubPartialProviderError) PartialProviderResponse() *agent.Response {
	return e.partial
}

func (e stubPartialProviderError) PartialProviderResponseID() string {
	return e.responseID
}

func (e stubPartialProviderError) PartialProviderReason() string {
	return e.reason
}

type stubProviderFailureCodeError struct {
	msg        string
	code       string
	retryAfter time.Duration
}

func (e stubProviderFailureCodeError) Error() string {
	return e.msg
}

func (e stubProviderFailureCodeError) ProviderFailureCode() string {
	return e.code
}

func (e stubProviderFailureCodeError) ProviderRetryAfter() time.Duration {
	return e.retryAfter
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

func TestFailoverChainSkipsOpenAIFamilyOnCodexOverload(t *testing.T) {
	primary := &stubChainProvider{err: stubProviderFailureCodeError{
		code: "server_is_overloaded",
		msg:  "codex: stream failed: Our servers are currently overloaded. Please try again later.",
	}}
	openAI := &stubChainProvider{reply: "should not run"}
	anthropic := &stubChainProvider{reply: "anthropic after codex overload"}

	chain, err := NewFailoverChain([]NamedProvider{
		{Name: "codex", Provider: primary},
		{Name: "openai:gpt-5.5", Provider: openAI},
		{Name: "anthropic", Provider: anthropic},
	})
	if err != nil {
		t.Fatalf("NewFailoverChain() err = %v", err)
	}

	resp, err := chain.CompleteManaged(context.Background(), []agent.Message{{Role: "user", Content: "hi"}}, nil, agent.CompleteOptions{})
	if err != nil {
		t.Fatalf("CompleteManaged() err = %v", err)
	}
	if resp.Content != "anthropic after codex overload" {
		t.Fatalf("content = %q, want anthropic fallback", resp.Content)
	}
	if primary.callCount != 1 {
		t.Fatalf("primary.callCount = %d, want no same-provider retries on Codex overload", primary.callCount)
	}
	if openAI.callCount != 0 {
		t.Fatalf("openAI.callCount = %d, want OpenAI family skipped", openAI.callCount)
	}
	if anthropic.callCount != 1 {
		t.Fatalf("anthropic.callCount = %d, want one fallback call", anthropic.callCount)
	}
	if !providerEventsContain(resp.ProviderEvents, core.ExecutionEventProviderAttemptFailed) {
		t.Fatalf("provider events = %#v, want failed event", resp.ProviderEvents)
	}
	if !providerEventsContain(resp.ProviderEvents, core.ExecutionEventProviderFailoverEngaged) {
		t.Fatalf("provider events = %#v, want failover event", resp.ProviderEvents)
	}
}

func TestFailoverChainFallsBackOnTextOnlyOpenAIOverload(t *testing.T) {
	primary := &stubChainProvider{err: errors.New("codex: stream failed: Our servers are currently overloaded. Please try again later.")}
	anthropic := &stubChainProvider{reply: "anthropic after text overload"}

	chain, err := NewFailoverChain([]NamedProvider{
		{Name: "openai:gpt-5.5", Provider: primary},
		{Name: "anthropic", Provider: anthropic},
	})
	if err != nil {
		t.Fatalf("NewFailoverChain() err = %v", err)
	}

	resp, err := chain.Stream(context.Background(), []agent.Message{{Role: "user", Content: "hi"}}, nil, nil)
	if err != nil {
		t.Fatalf("Stream() err = %v", err)
	}
	if resp.Content != "anthropic after text overload" {
		t.Fatalf("content = %q, want anthropic fallback", resp.Content)
	}
	if primary.callCount != 1 {
		t.Fatalf("primary.callCount = %d, want no same-provider retries on overload", primary.callCount)
	}
	if anthropic.callCount != 1 {
		t.Fatalf("anthropic.callCount = %d, want one fallback call", anthropic.callCount)
	}
}

func TestFailoverChainFallsBackOnOpenAIRateLimitWithoutRetryingPrimary(t *testing.T) {
	primary := &stubChainProvider{err: stubProviderFailureCodeError{
		code: "rate_limit_exceeded",
		msg:  "codex: stream failed: Rate limit reached. Please try again in 11.054s.",
	}}
	anthropic := &stubChainProvider{reply: "anthropic after rate limit"}

	chain, err := NewFailoverChain([]NamedProvider{
		{Name: "codex", Provider: primary},
		{Name: "anthropic", Provider: anthropic},
	})
	if err != nil {
		t.Fatalf("NewFailoverChain() err = %v", err)
	}

	resp, err := chain.CompleteManaged(context.Background(), []agent.Message{{Role: "user", Content: "hi"}}, nil, agent.CompleteOptions{})
	if err != nil {
		t.Fatalf("CompleteManaged() err = %v", err)
	}
	if resp.Content != "anthropic after rate limit" {
		t.Fatalf("content = %q, want anthropic fallback", resp.Content)
	}
	if primary.callCount != 1 {
		t.Fatalf("primary.callCount = %d, want no same-provider retries on OpenAI-family rate limit", primary.callCount)
	}
	if anthropic.callCount != 1 {
		t.Fatalf("anthropic.callCount = %d, want one fallback call", anthropic.callCount)
	}
}

func TestProviderRetryDelayUsesBoundedRetryAfterHint(t *testing.T) {
	err := stubProviderFailureCodeError{msg: "retry later", retryAfter: 150 * time.Millisecond}
	if got := providerRetryDelay(err, 100*time.Millisecond); got != 150*time.Millisecond {
		t.Fatalf("providerRetryDelay() = %v, want retry-after hint", got)
	}
	err.retryAfter = 10 * time.Second
	if got := providerRetryDelay(err, 100*time.Millisecond); got != failoverMaximumBackoff {
		t.Fatalf("providerRetryDelay() = %v, want capped %v", got, failoverMaximumBackoff)
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

func TestFailoverChainTerminalErrorCarriesProviderEvents(t *testing.T) {
	primary := &stubChainProvider{err: stubStatusError{code: 400, msg: "bad request"}}
	secondary := &stubChainProvider{reply: "should not run"}

	chain, err := NewFailoverChain([]NamedProvider{
		{Name: "codex", Provider: primary},
		{Name: "anthropic", Provider: secondary},
	})
	if err != nil {
		t.Fatalf("NewFailoverChain() err = %v", err)
	}

	_, err = chain.CompleteManaged(context.Background(), []agent.Message{{Role: "user", Content: "hi"}}, nil, agent.CompleteOptions{})
	if err == nil {
		t.Fatal("CompleteManaged() err = nil, want terminal error")
	}
	var terminal TerminalProviderError
	if !errors.As(err, &terminal) {
		t.Fatalf("err = %T/%v, want TerminalProviderError", err, err)
	}
	if !providerEventsContain(terminal.ProviderEvents(), core.ExecutionEventProviderAttemptFailed) {
		t.Fatalf("terminal events = %#v, want provider attempt failed", terminal.ProviderEvents())
	}
	if secondary.callCount != 0 {
		t.Fatalf("secondary.callCount = %d, want no fallback for terminal bad request", secondary.callCount)
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
